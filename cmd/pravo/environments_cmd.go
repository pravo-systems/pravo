package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"cuelang.org/go/cue"
	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/sensitivity"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/ui"
)

func environmentsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "environments",
		Aliases: []string{"envs"},
		Short:   "Show what each environment of the specification runs, and the next step of the release",
		Long: `Reads the state of each environment that $environments declares, without a key and without a
provider, and shows the ladder: which environment follows which, what each runs (the specification
by its id, and the git revision it was applied from), since when and by whom, and how the release of
this directory stands in each. The last line is the next command to type.

  pravo environments                 the ladder, for the release of this directory
  pravo environments -t image=1.4    for the release with these tags
  pravo environments diff staging prod
  pravo environments record -t env=prod

An environment whose settings or state cannot be read here (a variable this shell does not set, a
bucket these credentials cannot read) is shown as such, and the others still are.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return showEnvironments(cmd) },
	}
	pf := cmd.PersistentFlags()
	pf.StringVar(&flags.AccessDir, "access-dir", "", accessDirUsage)
	pf.StringArrayVarP(&flags.Tags, "tag", "t", nil, tagUsage)
	pf.BoolVarP(&flags.SysTags, "inject-vars", "T", false, sysTagsUsage)
	cmd.AddCommand(environmentsDiffCmd(), environmentsRecordCmd())
	return cmd
}

// loadAnyEnvironment loads the specification for the environment that -t names, else for its
// default, else for the first of the ladder: what a command that looks at every environment
// needs of it is the same in each (the release, the ladder).
func loadAnyEnvironment() (*config.Root, error) {
	tags := flags.Tags
	if _, ok := config.EnvironmentOf(tags); !ok {
		list, err := config.ReadEnvironmentSettings(".", config.LoadOptions{Tags: tags, SysTags: flags.SysTags, Env: environment()})
		if err != nil {
			return nil, engine.WithCode(engine.ExitInvalid, err)
		}
		if len(list) == 0 {
			return nil, engine.WithCode(engine.ExitInvalid, errors.New("this specification declares no environments. Declare them in $environments, one row each, and say in $after which one each follows"))
		}
		name := list[0].Name
		for _, es := range list {
			if es.Default {
				name = es.Name
			}
		}
		tags = config.WithEnvironment(tags, name)
	}
	saved := flags.Tags
	flags.Tags = tags
	defer func() { flags.Tags = saved }()
	root, err := loadRoot()
	if err != nil {
		return nil, err
	}
	if root.Environment == "" {
		return nil, engine.WithCode(engine.ExitInvalid, errors.New("this specification declares no environments. Declare them in $environments, one row each, and say in $after which one each follows"))
	}
	return root, nil
}

// environmentJSON is one environment in the JSON of "pravo environments".
type environmentJSON struct {
	Name    string   `json:"name"`
	After   []string `json:"after"`
	Default bool     `json:"default,omitempty"`
	State   string   `json:"state,omitempty"`
	Sealed  bool     `json:"sealed"`
	// Runs is the newest note of what the state runs.
	Runs *runJSON `json:"runs,omitempty"`
	// Release says how the release of this directory stands here: runs, promoted, waived,
	// ready (every environment before runs it) or empty.
	Release  string     `json:"release,omitempty"`
	Promoted *admitJSON `json:"promoted,omitempty"`
	Problem  string     `json:"problem,omitempty"`
}

type runJSON struct {
	Fingerprint string    `json:"fingerprint"`
	GitRevision string    `json:"git_revision,omitempty"`
	Txn         string    `json:"txn,omitempty"`
	At          time.Time `json:"at"`
	By          string    `json:"by,omitempty"`
	Verified    bool      `json:"verified"`
}

type admitJSON struct {
	Kind string    `json:"kind"`
	By   string    `json:"by"`
	At   time.Time `json:"at"`
	Note string    `json:"note,omitempty"`
}

// envRow is what "pravo environments" found of one environment.
type envRow struct {
	j    environmentJSON
	runs bool // runs the release of this directory
	// otherTags is set when it runs the specification of this directory with other tags.
	otherTags bool
	gate      *engine.GateCheck
}

func showEnvironments(cmd *cobra.Command) error {
	ctx := cmd.Context()
	root, err := loadAnyEnvironment()
	if err != nil {
		return err
	}
	rel, err := engine.ReleaseOf(root)
	if err != nil {
		return err
	}
	rows := readEnvironmentRows(ctx, root, rel)
	next := nextStep(root, rows)
	if flags.JSON {
		envs := make([]environmentJSON, len(rows))
		for i, r := range rows {
			envs[i] = r.j
		}
		return emitJSON(map[string]any{"ladder": config.LadderText(root.Environments), "release": releaseJSON(root, rel), "environments": envs, "next": next})
	}
	out.Println(config.LadderText(root.Environments))
	out.Println()
	table := [][]string{{"ENV", "FOLLOWS", "RUNS", "GIT", "SINCE", "BY", ""}}
	for _, r := range rows {
		row := []string{r.j.Name, strings.Join(r.j.After, ", "), "-", "", "", "", status(r)}
		if row[1] == "" {
			row[1] = "-"
		}
		if n := r.j.Runs; n != nil {
			row[2], row[3], row[4] = engine.ShortID(n.Fingerprint), shortRev(n.GitRevision), ago(&n.At)
			row[5] = "-"
			if n.By != "" {
				row[5] = safeName(n.By)
			}
		}
		table = append(table, row)
	}
	printTable(table, -1)
	out.Println()
	out.Printf("This directory: %s\n", strings.TrimSpace(engine.ShortID(rel.Fingerprint)+" "+engine.TagFlags(root.Tags)))
	out.Printf("Next: %s\n", next)
	return nil
}

func releaseJSON(root *config.Root, rel engine.Release) map[string]any {
	names := make([]string, 0, len(rel.Tags))
	for n := range rel.Tags {
		names = append(names, n)
	}
	sort.Strings(names)
	return map[string]any{"fingerprint": rel.Fingerprint, "tags": names}
}

func shortRev(rev string) string {
	if len(rev) > 7 && !strings.Contains(rev[:7], "-") {
		return rev[:7] + strings.TrimPrefix(rev[7:], strings.TrimSuffix(rev[7:], "-dirty"))
	}
	return rev
}

// status is the last column of a row: what stands in the way, or how the release of this
// directory stands there.
func status(r envRow) string {
	if r.j.Problem != "" {
		return r.j.Problem
	}
	var parts []string
	if r.j.Runs == nil {
		parts = append(parts, "nothing applied yet")
	}
	switch r.j.Release {
	case "runs":
		parts = append(parts, "runs this directory")
	case "promoted":
		parts = append(parts, fmt.Sprintf("promoted by %s %s ago", engine.PlainText(r.j.Promoted.By), engine.RoundAge(time.Since(r.j.Promoted.At))))
	case "waived":
		parts = append(parts, fmt.Sprintf("waived by %s %s ago", engine.PlainText(r.j.Promoted.By), engine.RoundAge(time.Since(r.j.Promoted.At))))
	case "ready":
		parts = append(parts, "ready to promote")
	}
	if r.j.Runs != nil && r.j.Release != "runs" && r.otherTags {
		parts = append(parts, "runs this specification with other tags")
	}
	if !r.j.Sealed && r.j.State != "" && len(r.j.After) > 0 {
		parts = append(parts, "not sealed, takes any release")
	}
	return strings.Join(parts, "; ")
}

// readEnvironmentRows reads the state of each environment in the order of the ladder.
func readEnvironmentRows(ctx context.Context, root *config.Root, rel engine.Release) []envRow {
	envs := &environmentsOf{root: root}
	rows := make([]envRow, 0, len(root.Environments))
	runs := map[string]bool{}
	for _, env := range root.Environments {
		s := envs.open(ctx, env.Name)
		r := envRow{j: environmentJSON{Name: env.Name, After: env.After, Default: env.Default, Sealed: s.Sealed}}
		if r.j.After == nil {
			r.j.After = []string{}
		}
		if s.Err == nil || s.es.Err == nil {
			r.j.State = s.es.Where()
		}
		if s.Err != nil {
			r.j.Problem = "not readable here: " + firstLine(s.Err)
		}
		if p := engine.RecordProblem(&config.Root{Environment: env.Name, Environments: root.Environments}, s.Record); s.Err == nil && p != "" {
			r.j.Problem = p
		}
		if n := s.Note; n != nil && s.Err == nil {
			r.j.Runs = &runJSON{Fingerprint: n.Fingerprint, GitRevision: n.GitRevision, Txn: n.Txn, At: n.At, By: s.NoteBy, Verified: s.Sealed && s.NoteProblem == ""}
			if s.Sealed && s.NoteProblem != "" && r.j.Problem == "" {
				r.j.Problem = s.NoteProblem
			}
			if (!s.Sealed || s.NoteProblem == "") && rel.Same(n) {
				r.runs, r.j.Release = true, "runs"
				runs[env.Name] = true
			}
			r.otherTags = !r.runs && n.Fingerprint == rel.Fingerprint
		}
		after := s.after()
		if !r.runs && len(after) > 0 && s.Err == nil && !s.Missing && s.Sealed {
			g := engine.GateReader{State: s.st, Environment: env.Name, Readers: s.rf.Readers, Upstream: envs.readers, Now: time.Now()}
			c := g.Check(ctx, after, s.Record, rel)
			r.gate = &c
			if c.Met {
				r.j.Release = "promoted"
				if c.Admission.Kind == state.PromotionWaived {
					r.j.Release = "waived"
				}
				r.j.Promoted = &admitJSON{Kind: c.Admission.Kind, By: c.Admission.By, At: c.Admission.At, Note: c.Admission.Note}
			}
		}
		if !r.runs && r.j.Release == "" && len(after) > 0 && s.Sealed && !slices.ContainsFunc(after, func(a string) bool { return !runs[a] }) {
			r.j.Release = "ready"
		}
		s.close()
		rows = append(rows, r)
	}
	return rows
}

// nextStep is the command that moves the release of this directory one step along: an apply
// in the first environment that does not run it and may take it, or the promotion into it.
func nextStep(root *config.Root, rows []envRow) string {
	for i, r := range rows {
		if !r.runs {
			return stepCommand(root, rows, i)
		}
	}
	return "nothing: every environment runs this directory"
}

// stepCommand is the command that moves the release into the environment of row i.
func stepCommand(root *config.Root, rows []envRow, i int) string {
	r, env := rows[i], root.Environments[i]
	switch {
	case len(env.After) == 0, r.j.Release == "promoted", r.j.Release == "waived", !r.j.Sealed && r.j.State != "":
		return engine.ApplyCommand(root, env.Name)
	}
	return engine.PromoteCommand(root, env.Name)
}

// ---------- environments record ----------

func environmentsRecordCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "record",
		Short: "Make the record of an environment in its state say what the specification says",
		Long: `The first apply in the state of an environment records which environment the state serves and
which ones it follows. An apply for another environment, or one whose row in $environments follows
others, is refused, so that a state copied or pointed at by mistake is not written over and a gate is
not dropped by an edit of the specification. When the change is meant, an admin of the state records
it:

  pravo environments record -t env=prod

For a specification that declares no environments any more, it records that the state serves none.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			root, err := loadRoot()
			if err != nil {
				return err
			}
			if stateMissing(ctx, root) {
				return engine.WithCode(engine.ExitRefused, errors.New("there is no state here yet, and the first apply records what it serves"))
			}
			st, err := openStateStore(ctx, root)
			if err != nil {
				return err
			}
			defer st.Close()
			rf := readersOf(root)
			eff, _, err := rf.withRoles(st)
			if err != nil {
				return engine.WithCode(engine.ExitUntrusted, errors.New(printable(err.Error())))
			}
			rec, err := st.EnvironmentRecord(ctx, eff.Readers)
			if err != nil {
				return secretErr(err)
			}
			next := state.EnvironmentRecord{}
			if cur, ok := root.Current(); ok {
				next.Name = cur.Name
				for _, a := range cur.After {
					u := state.Upstream{Name: a}
					if rec != nil && rec.Name == cur.Name {
						u.Lineage = rec.Lineage(a)
					}
					next.After = append(next.After, u)
				}
			}
			if engine.RecordProblem(root, rec) == "" && (rec != nil || next.Name == "") {
				out.Println("The record already says what the specification says: " + recordText(rec) + ".")
				return nil
			}
			if rec != nil {
				if msg, code := roleProblem(rf, st, protect.RoleAdmin, "change what the state records of its environment"); msg != "" {
					return engine.WithCode(code, errors.New(msg))
				}
			}
			seq := 1
			if rec != nil {
				seq = rec.Seq + 1
			}
			if err := st.PutEnvironmentRecord(ctx, seq, next); err != nil {
				if errors.Is(err, state.ErrEnvironmentChanged) {
					return engine.WithCode(engine.ExitStale, fmt.Errorf("%w; run the command again", err))
				}
				return secretErr(err)
			}
			out.Println("Recorded: " + recordText(&next) + ".")
			return nil
		},
	}
}

func recordText(rec *state.EnvironmentRecord) string {
	switch {
	case rec == nil || rec.Name == "":
		return "this state serves no environment"
	case len(rec.After) == 0:
		return "this state serves " + rec.Name + ", which follows no environment"
	}
	return "this state serves " + rec.Name + ", which follows " + joinAnd(rec.AfterNames())
}

// ---------- environments diff ----------

func environmentsDiffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diff <environment> <environment>",
		Short: "Compare what the specification declares in two environments",
		Long: `Evaluates the specification once for each environment and compares the resources it declares:
which ones only one of them has, and the fields whose values differ. It compares the specification,
not the states: "pravo plan -t env=NAME" compares one environment with what it runs. A field the
provider marks sensitive, or whose name says it is a secret, is compared and not shown; a value known
only at apply, such as the id of another resource, is shown as such.`,
		Example: "  pravo environments diff staging prod -t image=1.4",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return diffEnvironments(cmd, args[0], args[1])
		},
	}
}

// declared is what the specification declares in one environment: each resource by address,
// with its fields as JSON values, and the fields that are withheld.
type declared struct {
	order     []string
	fields    map[string]map[string]any
	withheld  map[string]map[string]bool
	resources int
}

const unknownValue = "(known at apply)"

func declaredIn(ctx context.Context, env string) (*declared, error) {
	saved := flags.Tags
	flags.Tags = config.WithEnvironment(flags.Tags, env)
	defer func() { flags.Tags = saved }()
	s, err := newSession(ctx, sessionOptions{install: true, schemas: true})
	if err != nil {
		return nil, err
	}
	defer s.close()
	d := &declared{fields: map[string]map[string]any{}, withheld: map[string]map[string]bool{}}
	for _, r := range s.root.Resources {
		if r.Builtin || r.Mode == "call" {
			continue
		}
		addr := r.Address()
		d.order = append(d.order, addr)
		fields := map[string]any{}
		hidden := map[string]bool{}
		if ps := s.schemas[r.LocalProvider()]; ps != nil {
			b := ps.Resource(r.Type)
			switch r.Mode {
			case "data":
				b = ps.DataSource(r.Type)
			case "ephemeral":
				b = ps.Ephemeral(r.Type)
			}
			if b != nil && b.Block != nil {
				for name, sens := range sensitivity.TopSensitive(b.Block) {
					hidden[name] = sens
				}
			}
		}
		v := s.root.Value.LookupPath(r.Path)
		it, err := v.Fields(cue.Optional(false))
		if err != nil {
			continue
		}
		for it.Next() {
			name := it.Selector().String()
			if strings.HasPrefix(name, "$") || strings.HasPrefix(name, "#") || strings.HasPrefix(name, "_") {
				continue
			}
			if sensitivity.Withheld(name, hidden[name], false) {
				hidden[name] = true
			}
			fields[name] = jsonOf(it.Value())
		}
		d.fields[addr], d.withheld[addr] = fields, hidden
	}
	d.resources = len(d.order)
	return d, nil
}

// jsonOf is the value as plain JSON values, with unknownValue where it is not concrete.
func jsonOf(v cue.Value) any {
	data, err := v.MarshalJSON()
	if err != nil {
		// A list or a struct with one value that is not concrete, such as an attribute the
		// provider computes, is followed, so that the values beside it still compare.
		if v.IncompleteKind() == cue.ListKind {
			it, err := v.List()
			if err != nil {
				return unknownValue
			}
			out := []any{}
			for it.Next() {
				out = append(out, jsonOf(it.Value()))
			}
			return out
		}
		if v.IncompleteKind() != cue.StructKind {
			return unknownValue
		}
		out := map[string]any{}
		it, err := v.Fields(cue.Optional(false))
		if err != nil {
			return unknownValue
		}
		for it.Next() {
			out[it.Selector().String()] = jsonOf(it.Value())
		}
		return out
	}
	var x any
	if json.Unmarshal(data, &x) != nil {
		return unknownValue
	}
	return x
}

type fieldDiff struct {
	Path string `json:"path"`
	A    any    `json:"a,omitempty"`
	B    any    `json:"b,omitempty"`
	// Sensitive is set for a field whose values are not shown: they differ.
	Sensitive bool `json:"sensitive,omitempty"`
}

type resourceDiff struct {
	Address string      `json:"address"`
	Only    string      `json:"only,omitempty"`
	Fields  []fieldDiff `json:"fields,omitempty"`
}

func diffEnvironments(cmd *cobra.Command, a, b string) error {
	ctx := cmd.Context()
	if a == b {
		return engine.WithCode(engine.ExitInvalid, errors.New("name two different environments"))
	}
	if _, ok := config.EnvironmentOf(flags.Tags); ok {
		return engine.WithCode(engine.ExitInvalid, fmt.Errorf("the environments are the arguments of diff, without -t %s", config.EnvTag))
	}
	da, err := declaredIn(ctx, a)
	if err != nil {
		return err
	}
	db, err := declaredIn(ctx, b)
	if err != nil {
		return err
	}
	var diffs []resourceDiff
	same := 0
	seen := map[string]bool{}
	for _, addr := range append(slices.Clone(da.order), db.order...) {
		if seen[addr] {
			continue
		}
		seen[addr] = true
		fa, inA := da.fields[addr]
		fb, inB := db.fields[addr]
		switch {
		case !inB:
			diffs = append(diffs, resourceDiff{Address: addr, Only: a})
			continue
		case !inA:
			diffs = append(diffs, resourceDiff{Address: addr, Only: b})
			continue
		}
		hidden := map[string]bool{}
		for k, v := range da.withheld[addr] {
			hidden[k] = hidden[k] || v
		}
		for k, v := range db.withheld[addr] {
			hidden[k] = hidden[k] || v
		}
		var fds []fieldDiff
		names := map[string]bool{}
		for k := range fa {
			names[k] = true
		}
		for k := range fb {
			names[k] = true
		}
		sorted := make([]string, 0, len(names))
		for k := range names {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			va, okA := fa[k]
			vb, okB := fb[k]
			if !okA {
				va = nil
			}
			if !okB {
				vb = nil
			}
			for _, fd := range valueDiffs(k, va, vb, 0) {
				if hidden[k] {
					fd.A, fd.B, fd.Sensitive = nil, nil, true
				}
				fds = append(fds, fd)
			}
		}
		if len(fds) == 0 {
			same++
			continue
		}
		diffs = append(diffs, resourceDiff{Address: addr, Fields: fds})
	}
	if flags.JSON {
		if diffs == nil {
			diffs = []resourceDiff{}
		}
		return emitJSON(map[string]any{"a": a, "b": b, "resources": diffs, "same": same})
	}
	if len(diffs) == 0 {
		out.Printf("%s and %s declare the same %d resources, with the same values.\n", a, b, same)
		return nil
	}
	out.Printf("Comparing %s with %s:\n\n", a, b)
	for _, d := range diffs {
		switch d.Only {
		case a:
			out.Printf("  %s %s  %s\n", out.C(ui.Red, "-"), d.Address, out.C(ui.Dim, "only in "+a))
		case b:
			out.Printf("  %s %s  %s\n", out.C(ui.Green, "+"), d.Address, out.C(ui.Dim, "only in "+b))
		default:
			out.Printf("  %s %s\n", out.C(ui.Yellow, "~"), d.Address)
			for _, f := range d.Fields {
				if f.Sensitive {
					out.Printf("      %s: %s\n", f.Path, out.C(ui.Dim, "differs (sensitive)"))
					continue
				}
				out.Printf("      %s: %s → %s\n", f.Path, shown(f.A), shown(f.B))
			}
		}
	}
	out.Println()
	noun := "resources differ"
	if len(diffs) == 1 {
		noun = "resource differs"
	}
	out.Printf("%d %s; %d %s the same.\n", len(diffs), noun, same, map[bool]string{true: "is", false: "are"}[same == 1])
	return nil
}

// valueDiffs lists where a and b differ, as dotted paths below path. Objects, and lists of the
// same length, are followed; a list whose length differs, or a scalar, is one difference.
func valueDiffs(path string, a, b any, depth int) []fieldDiff {
	la, okA := a.([]any)
	lb, okB := b.([]any)
	if okA && okB && len(la) == len(lb) && depth < 8 {
		var out []fieldDiff
		for i := range la {
			out = append(out, valueDiffs(fmt.Sprintf("%s[%d]", path, i), la[i], lb[i], depth+1)...)
		}
		return out
	}
	ma, okA := a.(map[string]any)
	mb, okB := b.(map[string]any)
	if okA && okB && depth < 8 {
		names := map[string]bool{}
		for k := range ma {
			names[k] = true
		}
		for k := range mb {
			names[k] = true
		}
		sorted := make([]string, 0, len(names))
		for k := range names {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		var out []fieldDiff
		for _, k := range sorted {
			out = append(out, valueDiffs(path+"."+k, ma[k], mb[k], depth+1)...)
		}
		return out
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) == string(jb) {
		return nil
	}
	return []fieldDiff{{Path: path, A: a, B: b}}
}

// shown writes a value of a field in one short line.
func shown(v any) string {
	if v == nil {
		return "(not set)"
	}
	if s, ok := v.(string); ok && s == unknownValue {
		return s
	}
	data, _ := json.Marshal(v)
	text := printable(string(data))
	if r := []rune(text); len(r) > 80 {
		text = string(r[:77]) + "..."
	}
	return text
}
