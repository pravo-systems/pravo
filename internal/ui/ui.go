// Package ui renders plans and messages for the terminal.
package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/format"
	cuejson "cuelang.org/go/encoding/json"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/mattn/go-isatty"

	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/schema"
)

// Printer writes coloured output.
type Printer struct {
	Out   io.Writer
	Color bool
}

// NewPrinter detects whether colour should be used for w. noColor turns it off:
// the caller passes it set when NO_COLOR is, as the environment is not read here.
func NewPrinter(w io.Writer, noColor bool) *Printer {
	color := !noColor
	if f, ok := w.(*os.File); ok && color {
		color = isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
	}
	return &Printer{Out: w, Color: color}
}

const (
	Reset  = "\033[0m"
	Bold   = "\033[1m"
	Dim    = "\033[2m"
	Red    = "\033[31m"
	Green  = "\033[32m"
	Yellow = "\033[33m"
	Cyan   = "\033[36m"
)

// C wraps s in a colour code when colour is enabled.
func (p *Printer) C(code, s string) string {
	if !p.Color {
		return s
	}
	return code + s + Reset
}

// Printf writes formatted output.
func (p *Printer) Printf(format string, args ...any) {
	fmt.Fprintf(p.Out, format, args...)
}

// Println writes a line.
func (p *Printer) Println(args ...any) {
	fmt.Fprintln(p.Out, args...)
}

// Error prints an error message.
func (p *Printer) Error(err error) {
	fmt.Fprintf(p.Out, "\n%s %s\n", p.C(Red+Bold, "Error:"), err)
}

// Warning prints a warning message.
func (p *Printer) Warning(msg string) {
	fmt.Fprintf(p.Out, "%s %s\n", p.C(Yellow+Bold, "Warning:"), msg)
}

func actionSymbol(a engine.Action) (sym, color string) {
	switch a {
	case engine.ActionCreate:
		return "+", Green
	case engine.ActionUpdate:
		return "~", Yellow
	case engine.ActionReplace:
		return "-/+", Red
	case engine.ActionDelete:
		return "-", Red
	case engine.ActionRead:
		return "<=", Cyan
	}
	return " ", ""
}

func actionVerb(a engine.Action) string {
	switch a {
	case engine.ActionCreate:
		return "will be created"
	case engine.ActionUpdate:
		return "will be updated in-place"
	case engine.ActionReplace:
		return "must be replaced"
	case engine.ActionDelete:
		return "will be destroyed"
	case engine.ActionRead:
		return "will be read"
	}
	return "is unchanged"
}

// RenderPlan prints the plan in CUE syntax with OpenTofu-style markers.
func (p *Printer) RenderPlan(plan *engine.Plan) {
	var shown []*engine.Change
	for _, c := range plan.Changes {
		if c.Action == engine.ActionNoop && c.Imported {
			shown = append(shown, c)
			continue
		}
		if c.Action == engine.ActionNoop || c.Action == engine.ActionOpen || c.Action == engine.ActionCall {
			continue
		}
		if c.Action == engine.ActionRead && !c.Deferred {
			continue
		}
		shown = append(shown, c)
	}
	if len(shown) == 0 && len(plan.Policies) > 0 && len(plan.Relabels) == 0 {
		p.Println()
		p.renderPolicies(plan)
		p.renderStats(plan)
		p.renderOutputs(plan)
		return
	}
	if len(shown) == 0 && len(plan.Relabels) == 0 {
		p.Println()
		p.Println(p.C(Bold+Green, "No changes.") + " " + noChanges(plan))
		p.renderStats(plan)
		p.renderOutputs(plan)
		return
	}
	p.Println()
	p.Println("pravo will perform the following actions:")
	for _, r := range plan.Relabels {
		p.Println()
		p.Println(p.C(Bold, fmt.Sprintf("  # %s will have its provider changed from %s to %s", r.Addr, r.From, r.To)) + p.C(Dim, " (record relabelled; no change to the object)"))
	}
	for _, c := range shown {
		p.Println()
		sym, color := actionSymbol(c.Action)
		if c.Action == engine.ActionMove {
			p.Println(p.C(Bold, fmt.Sprintf("  # %s will be moved to %s", c.MovedFrom, c.Addr)) + p.C(Dim, " (record renamed; no change to the object)"))
			continue
		}
		header := fmt.Sprintf("  # %s %s", addrWithType(c), actionVerb(c.Action))
		if c.Imported {
			from := "id"
			if c.ImportIdentity {
				from = "the identity"
			}
			if c.Action == engine.ActionNoop {
				header = fmt.Sprintf("  # %s will be imported from %s %q and matches the specification", c.Addr, from, c.ImportID)
			} else {
				header = fmt.Sprintf("  # %s will be imported from %s %q and %s", c.Addr, from, c.ImportID, actionVerb(c.Action))
			}
		}
		if c.Deferred {
			header += p.C(Dim, " (configuration depends on values known after apply)")
		}
		if c.Applied {
			header += p.C(Dim, " (already applied in txn "+c.AppliedTxn+")")
		}
		p.Println(p.C(Bold, header))
		if c.Action == engine.ActionNoop {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s %s: ", p.pad(p.C(color, sym)), pathLabels(c.Addr))
		p.renderChange(&b, c, "")
		p.Println(b.String())
		for _, w := range c.Warnings {
			p.Warning(fmt.Sprintf("%s: %s", c.Addr, w.Summary))
		}
	}
	add, change, destroy, _ := plan.Counts()
	p.Println()
	// what the specification declares about the state is counted after the objects: a plan that has only those
	// says what the apply will write
	var declared string
	imports, moves := 0, 0
	for _, c := range plan.Changes {
		switch {
		case c.Applied:
		case c.Action == engine.ActionMove:
			moves++
		case c.Imported:
			imports++
		}
	}
	if imports > 0 {
		declared += fmt.Sprintf(", %s to import", p.C(Cyan, fmt.Sprint(imports)))
	}
	if moves > 0 {
		declared += fmt.Sprintf(", %s to move", p.C(Cyan, fmt.Sprint(moves)))
	}
	p.Printf("%s %s to add, %s to change, %s to destroy%s.\n", p.C(Bold, "Plan:"), p.C(Green, fmt.Sprint(add)), p.C(Yellow, fmt.Sprint(change)), p.C(Red, fmt.Sprint(destroy)), declared)
	p.renderPolicies(plan)
	for _, h := range plan.Hints {
		p.Println(p.C(Yellow, "Hint: ") + h)
	}
	p.renderStats(plan)
	p.renderOutputs(plan)
}

// renderPolicies prints what the plan changes in the policies in force: a count, and
// each policy with the version it was made from and the digest of its new text.
func (p *Printer) renderPolicies(plan *engine.Plan) {
	if len(plan.Policies) == 0 {
		return
	}
	added, changed, removed := plan.PolicyCounts()
	p.Printf("%s %s new, %s changed, %s removed.\n", p.C(Bold, "Policies:"), p.C(Green, fmt.Sprintf("+%d", added)), p.C(Yellow, fmt.Sprintf("~%d", changed)), p.C(Red, fmt.Sprintf("-%d", removed)))
	for _, en := range plan.Policies {
		switch {
		case en.Digest == "":
			p.Printf("  - %s (version %d)\n", en.Name, en.Base)
		case en.Base == 0:
			p.Printf("  + %s (sha256:%s)\n", en.Name, en.Digest[:12])
		default:
			p.Printf("  ~ %s (version %d, sha256:%s)\n", en.Name, en.Base, en.Digest[:12])
		}
	}
}

// noChanges is the sentence that follows "No changes." It says that the
// infrastructure matches the configuration when every record was read or is
// within its refresh age, which is the freshness the specification accepts. A
// record outside the plan that was never read (Stats.NotRead) may differ, and
// the sentence then says how many.
func noChanges(plan *engine.Plan) string {
	switch {
	case plan.NoRefresh:
		return "The specification matches the state. Nothing was read from the infrastructure."
	case plan.Stats.NotRead == 1:
		return "The specification matches the state. 1 record was not read; run pravo drift to compare it with the infrastructure."
	case plan.Stats.NotRead > 1:
		return fmt.Sprintf("The specification matches the state. %d records were not read; run pravo drift to compare them with the infrastructure.", plan.Stats.NotRead)
	}
	return "Your infrastructure matches the configuration."
}

// addrWithType is the address with the provider type beside it, so a reader
// sees what kind of thing the path names: "api.config (local_file)".
func addrWithType(c *engine.Change) string {
	if c.Type == "" {
		return c.Addr
	}
	return fmt.Sprintf("%s (%s)", c.Addr, c.Type)
}

// pathLabels writes an address as nested CUE fields: "api.config" becomes
// "api: config", and a list element keeps its index: "servers[0]".
func pathLabels(addr string) string {
	p := cue.ParsePath(addr)
	if p.Err() != nil {
		return addr
	}
	var b strings.Builder
	for i, sel := range p.Selectors() {
		if sel.Type() == cue.IndexLabel {
			b.WriteString("[" + sel.String() + "]")
			continue
		}
		if i > 0 {
			b.WriteString(": ")
		}
		b.WriteString(sel.String())
	}
	return b.String()
}

// renderStats prints how much of the graph the plan walked.
func (p *Printer) renderStats(plan *engine.Plan) {
	st := plan.Stats
	// A root with no resource or data source has nothing to plan; what it opens
	// or calls is still said.
	if st.Total == 0 && !plan.Destroy && st.Opened == 0 && st.Called == 0 {
		return
	}
	var parts []string
	if plan.Destroy {
		parts = append(parts, fmt.Sprintf("%d resources in state", len(plan.Changes)))
	} else {
		if st.Total > 0 {
			parts = append(parts, fmt.Sprintf("planned %d", st.Reached))
		}
		if st.Skipped > 0 {
			parts = append(parts, fmt.Sprintf("skipped %d", st.Skipped))
		}
		if st.Removed > 0 {
			parts = append(parts, fmt.Sprintf("removed %d", st.Removed))
		}
	}
	if st.Refreshed > 0 {
		parts = append(parts, fmt.Sprintf("refreshed %d", st.Refreshed))
	}
	if st.Drifted > 0 {
		parts = append(parts, fmt.Sprintf("drifted %d", st.Drifted))
	}
	if st.Fresh > 0 {
		parts = append(parts, fmt.Sprintf("within refresh age %d", st.Fresh))
	}
	if st.NotRead > 0 {
		parts = append(parts, fmt.Sprintf("not read %d", st.NotRead))
	}
	if st.Opened > 0 {
		parts = append(parts, fmt.Sprintf("opened %d", st.Opened))
	}
	if st.Called > 0 {
		parts = append(parts, fmt.Sprintf("called %d", st.Called))
	}
	if st.Imported > 0 {
		parts = append(parts, fmt.Sprintf("to import %d", st.Imported))
	}
	if st.Moved > 0 {
		parts = append(parts, fmt.Sprintf("to move %d", st.Moved))
	}
	p.Println(p.C(Dim, "Graph: "+strings.Join(parts, " · ")))
}

// renderOutputs shows the outputs the plan changes, each with the value it has
// now and the one it will have. The others are counted: their values are in
// "pravo output" and "pravo serve", and a plan that lists them all fills a log
// with values nobody is deciding on.
func (p *Printer) renderOutputs(plan *engine.Plan) {
	all := map[string]bool{}
	for n := range plan.Outputs {
		all[n] = true
	}
	for n := range plan.SavedOutputs {
		all[n] = true
	}
	for _, n := range plan.UnknownOutputs {
		all[n] = true
	}
	changing := map[string]bool{}
	for _, n := range plan.EvalOutputs {
		changing[n] = true
	}
	for _, n := range plan.DropOutputs {
		changing[n] = true
	}
	names := make([]string, 0, len(changing))
	for n := range changing {
		if !all[n] {
			// An output the specification no longer declares.
			names = append(names, n)
			continue
		}
		if _, ok := p.outputChange(plan, n); ok {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	unchanged := len(all)
	for _, n := range names {
		if all[n] {
			unchanged--
		}
	}
	if len(all) == 0 && len(names) == 0 {
		return
	}
	p.Println()
	if len(names) == 0 {
		p.Println(p.C(Dim, fmt.Sprintf("Outputs: %s unchanged.", countOutputs(unchanged))))
		return
	}
	p.Println("Outputs:")
	for _, n := range names {
		line, _ := p.outputChange(plan, n)
		p.Printf("  %s: %s\n", convert.FieldLabel(n), line)
	}
	if unchanged > 0 {
		p.Println(p.C(Dim, fmt.Sprintf("  %s unchanged.", countOutputs(unchanged))))
	}
}

func countOutputs(n int) string {
	if n == 1 {
		return "1 output"
	}
	return fmt.Sprintf("%d outputs", n)
}

// outputChange words what the plan does to output n: "old → new", or just the
// new value when the output has no recorded one. It reports false for an output
// whose value stays as it is recorded. A sensitive value is compared and then
// shown as "(sensitive)".
func (p *Printer) outputChange(plan *engine.Plan, n string) (string, bool) {
	var old string
	var oldJSON []byte
	oldKnown, oldSensitive := false, false
	if raw, ok := plan.OldOutputs[n]; ok {
		oldJSON, oldSensitive = engine.UnwrapOutput(raw)
		oldKnown = true
		old = JSONToCUE(oldJSON, "  ")
	}
	var now string
	var nowJSON []byte
	switch v, ok := plan.Outputs[n]; {
	case ok && convert.IsConcrete(v):
		now = FormatCUE(convert.Default(v), "  ")
		nowJSON, _ = convert.Default(v).MarshalJSON()
	case ok:
		now = p.C(Dim, "(known after apply)")
	default:
		if raw, saved := plan.SavedOutputs[n]; saved {
			now = JSONToCUE(raw, "  ")
			nowJSON = raw
		} else if contains(plan.UnknownOutputs, n) {
			now = p.C(Dim, "(known after apply)")
		} else {
			now = p.C(Dim, "(removed)")
		}
	}
	if oldKnown && nowJSON != nil && sameJSON(oldJSON, nowJSON) {
		return "", false
	}
	if plan.SensitiveOutputs[n] || oldSensitive {
		return p.C(Dim, "(sensitive)"), true
	}
	if !oldKnown {
		return now, true
	}
	return old + " → " + now, true
}

// sameJSON reports whether two JSON documents hold the same value, whatever
// their layout.
func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// pad right-aligns the symbol in a 3-character column.
func (p *Printer) pad(sym string) string {
	plain := sym
	if p.Color {
		plain = stripColor(sym)
	}
	return strings.Repeat(" ", 3-len(plain)) + sym
}

func stripColor(s string) string {
	for _, c := range []string{Reset, Bold, Dim, Red, Green, Yellow, Cyan} {
		s = strings.ReplaceAll(s, c, "")
	}
	return s
}

func (p *Printer) renderChange(b *strings.Builder, c *engine.Change, indent string) {
	if c.Schema == nil {
		b.WriteString(p.C(Dim, "(removed from state)"))
		return
	}
	sens := sensitiveFunc(c.Schema.Block)
	switch c.Action {
	case engine.ActionCreate:
		p.renderValue(b, "+", Green, c.Planned, indent, sens, nil, nil)
	case engine.ActionDelete:
		p.renderValue(b, "-", Red, c.Prior, indent, sens, nil, nil)
	case engine.ActionRead:
		p.renderValue(b, "<=", Cyan, c.Planned, indent, sens, nil, nil)
	case engine.ActionUpdate:
		p.renderDiff(b, c.Prior, c.Planned, indent, sens, nil, replaceSet(c.RequiresReplace))
	case engine.ActionReplace:
		after := c.PlannedCreate
		if after.Type() == nil {
			after = c.Planned
		}
		p.renderDiff(b, c.Prior, after, indent, sens, nil, replaceSet(c.RequiresReplace))
	}
}

func replaceSet(paths []*tftypes.AttributePath) map[string]bool {
	out := map[string]bool{}
	for _, ap := range paths {
		if steps := ap.Steps(); len(steps) > 0 {
			if n, ok := steps[0].(tftypes.AttributeName); ok {
				out[string(n)] = true
			}
		}
	}
	return out
}

// sensitiveFunc returns a predicate over attribute paths.
func sensitiveFunc(blk *schema.Block) func([]string) bool {
	return func(path []string) bool {
		b := blk
		var obj *schema.Object
		for i := 0; i < len(path); i++ {
			name := path[i]
			if obj != nil {
				a := obj.Attribute(name)
				if a == nil {
					return false
				}
				if a.Sensitive {
					return true
				}
				if a.NestedType == nil {
					return false
				}
				obj = a.NestedType
				if obj.Nesting != schema.NestingSingle {
					i++
				}
				continue
			}
			if a := b.Attribute(name); a != nil {
				if a.Sensitive {
					return true
				}
				if a.NestedType == nil {
					return false
				}
				obj = a.NestedType
				if obj.Nesting != schema.NestingSingle {
					i++
				}
				continue
			}
			nb := b.BlockType(name)
			if nb == nil {
				return false
			}
			b = nb.Block
			if nb.Nesting != schema.NestingSingle && nb.Nesting != schema.NestingGroup {
				i++
			}
		}
		return false
	}
}

func (p *Printer) line(b *strings.Builder, sym, color, indent, text string) {
	b.WriteString("\n")
	b.WriteString(p.pad(p.C(color, sym)))
	b.WriteString(" ")
	b.WriteString(indent)
	b.WriteString(text)
}

func (p *Printer) value(v tftypes.Value, indent string, sens func([]string) bool, path []string) string {
	s := convert.RenderCUE(v, indent, sens, path)
	switch s {
	case "(known after apply)", "(sensitive)":
		return p.C(Dim, s)
	}
	return s
}

func (p *Printer) renderValue(b *strings.Builder, sym, color string, v tftypes.Value, indent string, sens func([]string) bool, path []string, _ map[string]bool) {
	if !v.IsKnown() || v.IsNull() || !(v.Type().Is(tftypes.Object{}) || v.Type().Is(tftypes.Map{})) {
		b.WriteString(p.value(v, indent, sens, path))
		return
	}
	var m map[string]tftypes.Value
	_ = v.As(&m)
	var keys []string
	for _, k := range sortedKeys(m) {
		if !m[k].IsNull() {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		b.WriteString("{}")
		return
	}
	b.WriteString("{")
	width := maxWidth(keys)
	for _, k := range keys {
		label := convert.FieldLabel(k) + ":"
		p.line(b, sym, color, indent+convert.Indent, fmt.Sprintf("%-*s ", width+1, label))
		p.renderValue(b, sym, color, m[k], indent+convert.Indent, sens, append(path, k), nil)
	}
	b.WriteString("\n")
	b.WriteString(p.pad(" ") + " " + indent + "}")
}

func (p *Printer) renderDiff(b *strings.Builder, before, after tftypes.Value, indent string, sens func([]string) bool, path []string, replace map[string]bool) {
	structural := func(v tftypes.Value) bool {
		return v.IsKnown() && !v.IsNull() && (v.Type().Is(tftypes.Object{}) || v.Type().Is(tftypes.Map{}))
	}
	if !structural(before) || !structural(after) {
		if before.Equal(after) {
			b.WriteString(p.value(after, indent, sens, path))
			return
		}
		b.WriteString(p.value(before, indent, sens, path))
		b.WriteString(p.C(Yellow, " -> "))
		b.WriteString(p.value(after, indent, sens, path))
		return
	}
	var bm, am map[string]tftypes.Value
	_ = before.As(&bm)
	_ = after.As(&am)
	keySet := map[string]bool{}
	for k := range bm {
		keySet[k] = true
	}
	for k := range am {
		keySet[k] = true
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	width := maxWidth(keys)
	b.WriteString("{")
	for _, k := range keys {
		bv, bok := bm[k]
		av, aok := am[k]
		label := fmt.Sprintf("%-*s ", width+1, convert.FieldLabel(k)+":")
		sub := append(path, k)
		inner := indent + convert.Indent
		switch {
		case !bok || (bv.IsNull() && !av.IsNull()):
			p.line(b, "+", Green, inner, label)
			p.renderValue(b, "+", Green, av, inner, sens, sub, nil)
		case !aok || (av.IsNull() && !bv.IsNull()):
			p.line(b, "-", Red, inner, label)
			p.renderValue(b, "-", Red, bv, inner, sens, sub, nil)
		case bv.IsNull() && av.IsNull():
			continue
		case bv.Equal(av):
			p.line(b, " ", "", inner, label)
			b.WriteString(p.value(av, inner, sens, sub))
		default:
			p.line(b, "~", Yellow, inner, label)
			p.renderDiff(b, bv, av, inner, sens, sub, nil)
			if len(path) == 0 && replace[k] {
				b.WriteString(p.C(Red, " # forces replacement"))
			}
		}
	}
	b.WriteString("\n")
	b.WriteString(p.pad(" ") + " " + indent + "}")
}

func sortedKeys(m map[string]tftypes.Value) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func maxWidth(keys []string) int {
	w := 0
	for _, k := range keys {
		if l := len(convert.FieldLabel(k)); l > w {
			w = l
		}
	}
	return w
}

// JSONToCUE renders JSON data as CUE source, indenting continuation lines.
func JSONToCUE(data []byte, indent string) string {
	expr, err := cuejson.Extract("", data)
	if err != nil {
		return string(data)
	}
	b, err := format.Node(expr)
	if err != nil {
		return string(data)
	}
	return strings.ReplaceAll(strings.TrimSpace(string(b)), "\n", "\n"+indent)
}

// FormatCUE renders a concrete CUE value as formatted source, indenting
// continuation lines with indent.
func FormatCUE(v cue.Value, indent string) string {
	node := v.Syntax(cue.Final(), cue.Concrete(true), cue.Docs(false), cue.Attributes(false))
	b, err := format.Node(node)
	if err != nil {
		return fmt.Sprint(v)
	}
	s := strings.TrimSpace(string(b))
	return strings.ReplaceAll(s, "\n", "\n"+indent)
}
