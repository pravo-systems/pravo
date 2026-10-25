package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/schema"
)

// The policy workbench: a rule is held against plans that exist, to see which it would refuse
// and why, before it is stored. The server answers with a function of what it is
// sent and of what it can read: the rule is compiled and unified with the documents of
// the plans in a CUE context of its own, and nothing is written. The rule travels in
// the address of a GET, as every request of the viewer does.

const (
	// maxTryRule bounds the text of a rule that is tried.
	maxTryRule = 32 << 10
	// maxTryPlans bounds the plans one request names.
	maxTryPlans = 100
	// maxPlanFiles bounds the plan files that are looked for in the project.
	maxPlanFiles = 100
	// maxPlanFileSize bounds a plan file that is read.
	maxPlanFileSize = 64 << 20
	// maxDocText bounds the JSON of a document or a change that is sent.
	maxDocText = 256 << 10
	// maxRefLen bounds the reference to a plan.
	maxRefLen = 400
)

// workbenchCache is what the workbench has worked out for one model: the document of each
// plan that was asked for, and the CUE context the rules are tried in. A model is
// not changed after it is built, and this is the memory of the requests it answers.
type workbenchCache struct {
	once  sync.Once
	mu    sync.Mutex
	trier *engine.Trier
	docs  map[string]*wbDoc
}

// wbDoc is the document of a plan, or why there is none.
type wbDoc struct {
	state   string
	note    string
	doc     engine.Doc
	judged  bool
	secrets *secretSet
	changes int
	// head and outline are made when the document is looked at.
	outline []DocChange
}

func (c *workbenchCache) tr() *engine.Trier {
	c.once.Do(func() {
		c.trier = engine.NewTrier()
		c.docs = map[string]*wbDoc{}
	})
	return c.trier
}

// planFiles are the plan files of the project, read once for each version of the file.
type planFileCache struct {
	mu    sync.Mutex
	files map[string]*planFileEntry
}

type planFileEntry struct {
	sig string
	pf  *engine.PlanFile
	err error
}

// workbench answers GET /api/workbench.
func (s *Server) workbench(m *model) *Workbench {
	wb := &Workbench{Version: APIVersion, Revision: m.rev, SpecOK: m.spec.root != nil, Plans: []WorkbenchPlan{}, Policies: []WorkbenchPolicy{},
		Schema: schema.PlanDocumentSource(), Examples: make([]WorkbenchExample, 0, len(workbenchExamples))}
	for _, ex := range workbenchExamples {
		ex.Policy = policyDeclaration(strings.ReplaceAll(ex.ID, "-", "_"), ex.Rule)
		wb.Examples = append(wb.Examples, ex)
	}
	if m.stored != nil {
		for _, e := range m.stored.entries {
			meta := e.Meta
			wb.Plans = append(wb.Plans, WorkbenchPlan{Ref: meta.ID, Kind: "stored", Label: meta.ID, Status: string(e.Status), Open: e.Status.Open(),
				Author: m.clipText(meta.Author), Created: meta.Created, Add: meta.Summary.Add, Change: meta.Summary.Change, Destroy: meta.Summary.Destroy})
		}
	}
	files, note := s.planFileList(m)
	wb.Plans = append(wb.Plans, files...)
	wb.Note = note
	if m.pol != nil {
		for _, p := range m.pol.Policies {
			t := m.polText[p.Name]
			if t.inForce == "" && t.declared == "" {
				continue
			}
			wp := WorkbenchPolicy{Name: p.Name, Status: p.Status}
			if t.inForce != "" {
				wp.InForce = m.scrubText(policyDeclaration(p.Name, clip(t.inForce, maxPolicyText)))
			}
			if t.declared != "" {
				wp.Declared = m.scrubText(policyDeclaration(p.Name, clip(t.declared, maxPolicyText)))
			}
			wb.Policies = append(wb.Policies, wp)
		}
	}
	return wb
}

// planFileList finds the plan files of the project: the one the viewer was started with, and the
// files in the project directory and the directories below it that read as one.
func (s *Server) planFileList(m *model) ([]WorkbenchPlan, string) {
	var out []WorkbenchPlan
	skipped := 0
	for _, path := range s.planFilePaths() {
		pf, err := s.readPlanFileAt(path)
		if err != nil {
			skipped++
			continue
		}
		rel := s.relPath(path)
		wp := WorkbenchPlan{Ref: "file:" + rel, Kind: "file", Label: m.clipText(rel), Created: pf.Created, Open: true}
		for _, sc := range pf.Changes {
			switch sc.Action {
			case engine.ActionCreate:
				wp.Add++
			case engine.ActionUpdate:
				wp.Change++
			case engine.ActionReplace:
				wp.Add++
				wp.Destroy++
			case engine.ActionDelete:
				wp.Destroy++
			}
		}
		out = append(out, wp)
	}
	note := ""
	if skipped > 0 {
		note = strconv.Itoa(skipped) + " file(s) that look like plan files could not be read."
	}
	return out, note
}

func (s *Server) relPath(path string) string {
	if rel, err := filepath.Rel(s.opts.Dir, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

// planFilePaths lists the files that may be plan files: the one the viewer shows, and the regular
// files of the project directory and of the directories one level below it whose first
// bytes are the start of a plan file. Hidden directories, the state and the generated
// packages are not looked into.
func (s *Server) planFilePaths() []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if abs, err := filepath.Abs(p); err == nil && !seen[abs] && len(out) < maxPlanFiles {
			seen[abs] = true
			out = append(out, abs)
		}
	}
	if s.opts.PlanFile != "" {
		add(s.opts.PlanFile)
	}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			path := filepath.Join(dir, name)
			switch {
			case strings.HasPrefix(name, "."):
			case e.IsDir():
				if depth < 1 && name != "pravo.state" && name != "cue.mod" && name != "node_modules" && name != "vendor" {
					walk(path, depth+1)
				}
			case e.Type().IsRegular() && looksLikePlanFile(path):
				add(path)
			}
		}
	}
	walk(s.opts.Dir, 0)
	sort.Strings(out)
	return out
}

// looksLikePlanFile reads the first bytes of a file: a plan file is a JSON object that begins with its format.
func looksLikePlanFile(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() > maxPlanFileSize || fi.Size() < 20 {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := f.Read(head)
	head = bytes.TrimSpace(head[:n])
	return len(head) > 0 && head[0] == '{' && bytes.Contains(head, []byte(`"format"`)) && bytes.Contains(head, []byte(`"created"`))
}

// readPlanFileAt reads a plan file, again only when it changed.
func (s *Server) readPlanFileAt(path string) (*engine.PlanFile, error) {
	sig := fileSignature(path)
	s.files.mu.Lock()
	defer s.files.mu.Unlock()
	if e := s.files.files[path]; e != nil && e.sig == sig {
		return e.pf, e.err
	}
	if s.files.files == nil {
		s.files.files = map[string]*planFileEntry{}
	}
	pf, err := engine.ReadPlanFile(path)
	s.files.files[path] = &planFileEntry{sig: sig, pf: pf, err: err}
	return pf, err
}

// docFor finds the document that a plan is judged by, working it out the first time.
func (s *Server) docFor(ctx context.Context, m *model, ref string) *wbDoc {
	wb := &m.wb
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.tr()
	if d := wb.docs[ref]; d != nil {
		return d
	}
	d := s.makeDoc(ctx, m, ref, wb.trier)
	// An unknown reference is not remembered, and a request that was given up is not either.
	if d.state != "unknown" && ctx.Err() == nil {
		wb.docs[ref] = d
	}
	return d
}

func (s *Server) makeDoc(ctx context.Context, m *model, ref string, tr *engine.Trier) *wbDoc {
	fail := func(state, note string) *wbDoc { return &wbDoc{state: state, note: note} }
	var pf *engine.PlanFile
	switch {
	case strings.HasPrefix(ref, "file:"):
		var path string
		for _, p := range s.planFilePaths() {
			if s.relPath(p) == strings.TrimPrefix(ref, "file:") {
				path = p
			}
		}
		if path == "" {
			return fail("unknown", "no such plan file")
		}
		var err error
		if pf, err = s.readPlanFileAt(path); err != nil {
			return fail("unreadable", "The file could not be read as a plan: "+headline(err.Error()))
		}
	case engine.IsPlanID(ref) && m.stored != nil && m.stored.byID[ref] != nil:
		var sealed bool
		var err error
		if pf, sealed, err = m.stored.planFile(ctx, m.stored.byID[ref]); err != nil {
			return fail("unreadable", "The plan could not be read: "+headline(err.Error()))
		}
		if sealed {
			return fail("sealed", "The values of this plan are sealed: this identity cannot open it.")
		}
	default:
		return fail("unknown", "no such plan")
	}
	if m.spec.root == nil {
		return fail("unreadable", "The specification does not load, so the values of the plan cannot be decoded.")
	}
	m.spec.cueMu.Lock()
	defer m.spec.cueMu.Unlock()
	eng, err := s.planEngine(m, pf)
	if err != nil {
		return fail("unreadable", "The values of the plan cannot be decoded: "+m.scrubText(headline(err.Error()))+".")
	}
	data, plan, err := eng.PolicyDocument(pf)
	if err != nil {
		return fail("unreadable", "The values of the plan cannot be decoded: "+m.scrubText(headline(err.Error()))+".")
	}
	d := &wbDoc{state: "judged", doc: engine.DocOf(data, plan), judged: pf.ChangesResources(), changes: len(plan.Changes), secrets: m.secrets.clone()}
	po := &planOverlay{changes: map[string]*engine.Change{}, secrets: d.secrets}
	for _, ch := range plan.Changes {
		po.changes[ch.Addr] = ch
	}
	po.collectSecrets()
	if !d.judged {
		d.state = "not-judged"
		d.note = "The plan changes no resource, so no policy judges it."
	}
	tr.Add(ref, d.doc)
	for i, ch := range plan.Changes {
		d.outline = append(d.outline, DocChange{Index: i, Addr: ch.Addr, Action: string(ch.Action), Type: ch.Type})
	}
	return d
}

// tryHandler answers GET /api/workbench/try?rule=...&ref=...: the text is a file of policies, and each policy in it is
// tried on every plan named by ref.
func (s *Server) tryHandler(w http.ResponseWriter, r *http.Request, m *model) {
	q := r.URL.Query()
	text := q.Get("rule")
	if len(text) > maxTryRule {
		writeError(w, http.StatusBadRequest, "the policies are too long to try")
		return
	}
	refs := q["ref"]
	if len(refs) > maxTryPlans {
		writeError(w, http.StatusBadRequest, "too many plans: try at most "+strconv.Itoa(maxTryPlans)+" at once")
		return
	}
	for _, ref := range refs {
		if len(ref) > maxRefLen || strings.ContainsAny(ref, "\x00\n\r") {
			writeError(w, http.StatusBadRequest, "ref is not the reference of a plan")
			return
		}
	}
	began := time.Now()
	out := &WorkbenchTry{Version: APIVersion, Plans: make([]TriedPlan, 0, len(refs)), Policies: []engine.DraftPolicy{}}
	tr := m.wb.tr()
	var docs []*wbDoc
	var keys []string
	for _, ref := range refs {
		if r.Context().Err() != nil {
			return
		}
		d := s.docFor(r.Context(), m, ref)
		docs = append(docs, d)
		if d.state == "judged" {
			keys = append(keys, ref)
		}
	}
	res := tr.TryFile(text, keys)
	if res.Problem != nil {
		p := *res.Problem
		p.Message = m.scrubText(clip(p.Message, maxText))
		out.Check = &p
	}
	if res.Policies != nil {
		out.Policies = res.Policies
	}
	k := 0
	for i, ref := range refs {
		d := docs[i]
		tp := TriedPlan{Ref: ref, State: d.state, Note: m.scrubText(clip(d.note, 300)), Changes: d.changes}
		if d.state == "judged" {
			tp.Draft = s.cleanResult(m, d, res.Plans[k])
			k++
		}
		out.Plans = append(out.Plans, tp)
	}
	out.Took = time.Since(began).Milliseconds()
	writeJSON(w, http.StatusOK, out)
}

// policyDeclaration is a rule as a specification would declare it: the import, the name and the policy.
func policyDeclaration(name, rule string) string {
	var b strings.Builder
	b.WriteString("import \"pravo.dev/pravo\"\n\n")
	labels := strings.Split(name, ".")
	for i, l := range labels {
		if i > 0 {
			b.WriteString(": ")
		}
		if cueIdent.MatchString(l) {
			b.WriteString(l)
		} else {
			b.WriteString(strconv.Quote(l))
		}
	}
	b.WriteString(": pravo.#Policy & {\n\trule: {\n")
	for _, line := range strings.Split(strings.TrimRight(rule, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString("\t\t" + line + "\n")
	}
	b.WriteString("\t}\n}\n")
	return b.String()
}

var cueIdent = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// cleanResult masks what a rule's refusal quotes of a plan: the text of an error holds
// the values it names.
func (s *Server) cleanResult(m *model, d *wbDoc, res engine.RuleResult) *engine.RuleResult {
	issues := make([]engine.RuleIssue, len(res.Issues))
	for i, is := range res.Issues {
		is.Message = clip(stripControl(m.scrubTextWith(d.secrets, is.Message)), maxText)
		is.Got = clip(stripControl(m.scrubTextWith(d.secrets, is.Got)), 240)
		is.Change = clip(stripControl(m.scrubTextWith(d.secrets, is.Change)), 300)
		is.Path = clip(stripControl(is.Path), 300)
		issues[i] = is
	}
	res.Issues = issues
	return &res
}

// docHandler answers GET /api/workbench/doc?ref=...&i=N: the document of a plan as a rule sees it,
// and the change N of it.
func (s *Server) docHandler(w http.ResponseWriter, r *http.Request, m *model) {
	ref := r.URL.Query().Get("ref")
	if ref == "" || len(ref) > maxRefLen {
		writeError(w, http.StatusBadRequest, "ref is required: the id of a stored plan or file: and the path of a plan file")
		return
	}
	d := s.docFor(r.Context(), m, ref)
	switch d.state {
	case "unknown":
		writeError(w, http.StatusNotFound, d.note)
		return
	case "sealed", "unreadable":
		writeError(w, http.StatusConflict, d.note)
		return
	}
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(d.doc.JSON))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	resp := &WorkbenchDoc{Version: APIVersion, Ref: ref, Outline: d.outline}
	if len(resp.Outline) > 2000 {
		resp.Outline, resp.Truncated = resp.Outline[:2000], true
	}
	for i := range resp.Outline {
		resp.Outline[i].Addr = clip(stripControl(m.scrubTextWith(d.secrets, resp.Outline[i].Addr)), 300)
	}
	changes, _ := doc["changes"].([]any)
	if v := r.URL.Query().Get("i"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n >= len(changes) {
			writeError(w, http.StatusBadRequest, "i is the index of a change of the document")
			return
		}
		resp.Change = s.prettyDoc(m, d, changes[n], resp)
	}
	doc["changes"] = "(" + strconv.Itoa(len(changes)) + " changes, listed in the outline)"
	resp.Head = s.prettyDoc(m, d, doc, resp)
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) prettyDoc(m *model, d *wbDoc, v any, into *WorkbenchDoc) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	text := m.scrubTextWith(d.secrets, string(b))
	if len(text) > maxDocText {
		into.Truncated = true
		text = text[:maxDocText] + "\n…"
	}
	return text
}
