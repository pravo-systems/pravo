package serve

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
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
	"github.com/pravo-systems/pravo/internal/state"
)

// The policy studio: a rule is held against plans that exist, to see which it would refuse
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

// studioCache is what the studio has worked out for one model: the document of each
// plan that was asked for, and the CUE context the rules are tried in. A model is
// not changed after it is built, and this is the memory of the requests it answers.
type studioCache struct {
	once  sync.Once
	mu    sync.Mutex
	trier *engine.Trier
	docs  map[string]*studioDoc
}

// studioDoc is the document of a plan, or why there is none.
type studioDoc struct {
	state   string
	note    string
	doc     engine.Doc
	judged  bool
	secrets *secretSet
	changes int
	// head and outline are made when the document is looked at.
	outline []DocChange
}

func (c *studioCache) tr() *engine.Trier {
	c.once.Do(func() {
		c.trier = engine.NewTrier()
		c.docs = map[string]*studioDoc{}
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

// studio answers GET /api/studio.
func (s *Server) studio(m *model) *Studio {
	studio := &Studio{Version: APIVersion, Revision: m.rev, SpecOK: m.spec.root != nil, Plans: []StudioPlan{}, Policies: []StudioPolicy{},
		Schema: schema.PlanDocumentSource(), Fields: planSchema(), Examples: make([]StudioExample, 0, len(studioExamples))}
	for _, ex := range studioExamples {
		ex.Policy = policyDeclaration(strings.ReplaceAll(ex.ID, "-", "_"), ex.Title, ex.Rule)
		studio.Examples = append(studio.Examples, ex)
	}
	if m.stored != nil {
		for _, e := range m.stored.entries {
			meta := e.Meta
			studio.Plans = append(studio.Plans, StudioPlan{Ref: meta.ID, Kind: "stored", Label: meta.ID, Status: string(e.Status), Open: e.Status.Open(),
				Author: m.clipText(meta.Author), Created: meta.Created, Add: meta.Summary.Add, Change: meta.Summary.Change, Destroy: meta.Summary.Destroy})
		}
	}
	files, note := s.planFileList(m)
	studio.Plans = append(studio.Plans, files...)
	studio.Plans = append(studio.Plans, s.uploads.list(m)...)
	studio.Note = note
	studio.State = &StudioState{Ref: stateRef, Resources: len(stateChanges(m))}
	if m.pol != nil {
		desc := map[string]string{}
		if root := m.spec.root; root != nil {
			for _, p := range root.Policies {
				desc[p.Name] = p.Description
			}
		}
		for _, p := range m.pol.Policies {
			t := m.polText[p.Name]
			if t.inForce == "" && t.declared == "" {
				continue
			}
			wp := StudioPolicy{Name: p.Name, Status: p.Status, Description: m.clipText(desc[p.Name])}
			if t.inForce != "" {
				wp.InForce = m.scrubText(policyDeclaration(p.Name, wp.Description, clip(t.inForce, maxPolicyText)))
			}
			if t.declared != "" {
				wp.Declared = m.scrubText(policyDeclaration(p.Name, wp.Description, clip(t.declared, maxPolicyText)))
			}
			studio.Policies = append(studio.Policies, wp)
		}
	}
	return studio
}

// planFileList finds the plan files of the project: the one the viewer was started with, and the
// files in the project directory and the directories below it that read as one.
func (s *Server) planFileList(m *model) ([]StudioPlan, string) {
	var out []StudioPlan
	skipped := 0
	for _, path := range s.planFilePaths() {
		pf, err := s.readPlanFileAt(path, m)
		if err != nil {
			skipped++
			continue
		}
		rel := s.relPath(path)
		wp := studioPlanOf("file:"+rel, "file", m.clipText(rel), pf)
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

// looksLikePlanFile reads the first bytes of a file: a plan file is a JSON object that begins with its
// format, and the plan file of a sealed state is an envelope that begins with its version and holds
// what was sealed. The envelope opens for a reader of the state, and is counted as unreadable otherwise.
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
	if len(head) == 0 || head[0] != '{' {
		return false
	}
	if bytes.Contains(head, []byte(`"format"`)) && bytes.Contains(head, []byte(`"created"`)) {
		return true
	}
	rest := bytes.TrimLeft(head[1:], " \t\r\n")
	return bytes.HasPrefix(rest, []byte(`"v"`)) && bytes.Contains(head, []byte(`"sealed"`))
}

// readPlanFileAt reads a plan file, again only when it changed.
func (s *Server) readPlanFileAt(path string, m *model) (*engine.PlanFile, error) {
	// A plan file that a sealed state wrote opens with the key of a reader, which the state of the model gives.
	var sl state.Sealer
	if m.store != nil {
		sl = m.store.Sealer()
	}
	sig := fileSignature(path) + strconv.FormatBool(sl != nil)
	s.files.mu.Lock()
	defer s.files.mu.Unlock()
	if e := s.files.files[path]; e != nil && e.sig == sig {
		return e.pf, e.err
	}
	if s.files.files == nil {
		s.files.files = map[string]*planFileEntry{}
	}
	pf, err := engine.ReadPlanFileWith(path, sl)
	s.files.files[path] = &planFileEntry{sig: sig, pf: pf, err: err}
	return pf, err
}

// docFor finds the document that a plan is judged by, working it out the first time.
func (s *Server) docFor(ctx context.Context, m *model, ref string) *studioDoc {
	studio := &m.studio
	studio.mu.Lock()
	defer studio.mu.Unlock()
	studio.tr()
	if d := studio.docs[ref]; d != nil {
		return d
	}
	d := s.makeDoc(ctx, m, ref, studio.trier)
	// An unknown reference is not remembered, and a request that was given up is not either.
	if d.state != "unknown" && ctx.Err() == nil {
		studio.docs[ref] = d
	}
	return d
}

func (s *Server) makeDoc(ctx context.Context, m *model, ref string, tr *engine.Trier) *studioDoc {
	fail := func(state, note string) *studioDoc { return &studioDoc{state: state, note: note} }
	if ref == stateRef {
		return stateDoc(m, tr)
	}
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
		if pf, err = s.readPlanFileAt(path, m); err != nil {
			return fail("unreadable", "The file could not be read as a plan: "+headline(err.Error()))
		}
	case strings.HasPrefix(ref, "upload:"):
		if pf = s.uploads.get(ref); pf == nil {
			return fail("unknown", "This file is not held any more: drop it again.")
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
	data, plan, err := eng.PolicyDocument(pf, time.Now())
	if err != nil {
		return fail("unreadable", "The values of the plan cannot be decoded: "+m.scrubText(headline(err.Error()))+".")
	}
	d := &studioDoc{state: "judged", doc: engine.DocOf(data, plan), judged: pf.ChangesResources(), changes: len(plan.Changes), secrets: m.secrets.clone()}
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

// stateRef names the state in a request: a plan that updates every resource of the state to what it holds.
const stateRef = "state"

// stateChanges are the changes of that plan: one update for each resource that has values to read, each from
// what the record holds to the same. Data sources and records that are sealed to this identity are left out.
func stateChanges(m *model) []engine.ChangeView {
	var out []engine.ChangeView
	for _, a := range m.order {
		mn := m.nodes[a]
		if mn == nil || mn.rec == nil || mn.rec.Mode != "managed" {
			continue
		}
		raw := mn.rec.Attributes
		if len(raw) == 0 || string(raw) == "null" || jsonDepth(raw) > maxRawDepth {
			continue
		}
		out = append(out, engine.ChangeView{Addr: a, Mode: mn.rec.Mode, Type: mn.rec.Type, Name: mn.rec.Name, Action: engine.ActionUpdate,
			Provider: mn.rec.ProviderKey, Before: raw, After: raw, Dependencies: mn.rec.Dependencies})
	}
	return out
}

// stateDoc is the document of the state as a plan that updates each resource to what it holds: what a policy would see
// of every resource if one day a plan touched it.
func stateDoc(m *model, tr *engine.Trier) *studioDoc {
	changes := stateChanges(m)
	if len(changes) == 0 {
		return &studioDoc{state: "not-judged", note: "The state holds no resource whose values this identity can read."}
	}
	doc, err := engine.StateDoc(changes)
	if err != nil {
		return &studioDoc{state: "unreadable", note: "The state could not be made into a document: " + m.scrubText(headline(err.Error()))}
	}
	d := &studioDoc{state: "judged", doc: doc, judged: true, changes: len(changes), secrets: m.secrets.clone()}
	tr.Add(stateRef, doc)
	for i, c := range changes {
		d.outline = append(d.outline, DocChange{Index: i, Addr: c.Addr, Action: string(c.Action), Type: c.Type})
	}
	return d
}

// tryHandler answers GET /api/studio/try?rule=...&ref=...: the text is a file of policies, and each policy in it is
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
	out := &StudioTry{Version: APIVersion, Plans: make([]TriedPlan, 0, len(refs)), Policies: []engine.DraftPolicy{}}
	tr := m.studio.tr()
	var docs []*studioDoc
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

// policyDeclaration is a rule as a specification would declare it: the import, the name, the description when there is one, and the policy.
func policyDeclaration(name, description, rule string) string {
	var b strings.Builder
	imports, rule := hoistImports(rule)
	if len(imports) == 0 {
		b.WriteString("import \"pravo.dev/pravo\"\n\n")
	} else {
		b.WriteString("import (\n\t\"pravo.dev/pravo\"\n")
		for _, im := range imports {
			b.WriteString("\t" + im + "\n")
		}
		b.WriteString(")\n\n")
	}
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
	b.WriteString(": pravo.#Policy & {\n")
	if description != "" {
		b.WriteString("\tdescription: " + strconv.Quote(description) + "\n")
	}
	b.WriteString("\trule: {\n")
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

// hoistImports takes the import declarations off the top of a stored rule, where they sit as written in its file, so
// that they can go to the top of the policy file, where CUE wants them. It returns each imported package, with its
// alias if it has one, and what is left of the rule.
func hoistImports(rule string) ([]string, string) {
	lines := strings.Split(rule, "\n")
	var specs []string
	i := 0
	for i < len(lines) {
		t := strings.TrimSpace(lines[i])
		switch {
		case t == "":
			i++
			continue
		case strings.HasPrefix(t, "import ("):
			i++
			for i < len(lines) && strings.TrimSpace(lines[i]) != ")" {
				if sp := strings.TrimSpace(lines[i]); sp != "" {
					specs = append(specs, sp)
				}
				i++
			}
			i++
			continue
		case strings.HasPrefix(t, "import "):
			specs = append(specs, strings.TrimSpace(strings.TrimPrefix(t, "import ")))
			i++
			continue
		}
		break
	}
	var out []string
	for _, sp := range specs {
		if !strings.Contains(sp, "pravo.dev/pravo") {
			out = append(out, sp)
		}
	}
	return out, strings.Join(lines[i:], "\n")
}

var cueIdent = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// cleanResult masks what a rule's refusal quotes of a plan: the text of an error holds
// the values it names.
func (s *Server) cleanResult(m *model, d *studioDoc, res engine.RuleResult) *engine.RuleResult {
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

// docHandler answers GET /api/studio/doc?ref=...&i=N: the document of a plan as a rule sees it,
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
	resp := &StudioDoc{Version: APIVersion, Ref: ref, Outline: d.outline}
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

func (s *Server) prettyDoc(m *model, d *studioDoc, v any, into *StudioDoc) string {
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

// studioPlanOf describes a plan file for the list of plans: its counts are those of the summary of a plan.
func studioPlanOf(ref, kind, label string, pf *engine.PlanFile) StudioPlan {
	wp := StudioPlan{Ref: ref, Kind: kind, Label: label, Created: pf.Created, Open: true}
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
	return wp
}

// planUploads holds the plan files that a person dropped on the studio. They live in memory only, the newest
// maxUploads of them, and are not written anywhere: a file is judged and forgotten.
type planUploads struct {
	mu    sync.Mutex
	order []string
	byRef map[string]*heldPlan
}

type heldPlan struct {
	name string
	pf   *engine.PlanFile
}

const maxUploads = 8

func (u *planUploads) get(ref string) *engine.PlanFile {
	u.mu.Lock()
	defer u.mu.Unlock()
	if h := u.byRef[ref]; h != nil {
		return h.pf
	}
	return nil
}

func (u *planUploads) put(ref, name string, pf *engine.PlanFile) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.byRef == nil {
		u.byRef = map[string]*heldPlan{}
	}
	if _, ok := u.byRef[ref]; !ok {
		u.order = append(u.order, ref)
	}
	u.byRef[ref] = &heldPlan{name: name, pf: pf}
	for len(u.order) > maxUploads {
		delete(u.byRef, u.order[0])
		u.order = u.order[1:]
	}
}

func (u *planUploads) list(m *model) []StudioPlan {
	u.mu.Lock()
	defer u.mu.Unlock()
	var out []StudioPlan
	for _, ref := range u.order {
		h := u.byRef[ref]
		out = append(out, studioPlanOf(ref, "upload", m.clipText(h.name), h.pf))
	}
	return out
}

// uploadHandler answers POST /api/studio/plan?name=NAME. The body is a plan file, sealed or not. The server
// reads it, keeps it in memory under a reference, and answers with its entry for the list of plans. It is the one
// request that is not a GET, and it writes nothing: no file, no state.
func (s *Server) uploadHandler(w http.ResponseWriter, r *http.Request, m *model) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// A form of another page cannot send this type without a preflight, which the server does not answer.
	if r.Header.Get("Content-Type") != "application/octet-stream" {
		writeError(w, http.StatusUnsupportedMediaType, "send the plan file as application/octet-stream")
		return
	}
	name := filepath.Base(strings.TrimSpace(r.URL.Query().Get("name")))
	if name == "" || name == "." || len(name) > 200 || strings.ContainsAny(name, "\x00\n\r") {
		name = "plan file"
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPlanFileSize))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "the file is too large to try")
		return
	}
	var sl state.Sealer
	if m.store != nil {
		sl = m.store.Sealer()
	}
	pf, err := engine.ReadPlanBytes(name, data, sl)
	if err != nil {
		writeError(w, http.StatusBadRequest, m.scrubText(headline(err.Error())))
		return
	}
	sum := sha256.Sum256(data)
	ref := "upload:" + hex.EncodeToString(sum[:6])
	s.uploads.put(ref, name, pf)
	writeJSON(w, http.StatusOK, studioPlanOf(ref, "upload", m.clipText(name), pf))
}
