package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"pravo.dev/pravo/internal/state"
)

// <, > and & are written as they are, in the values pravo encodes and in the
// text that is already encoded; the result is valid JSON that reads back as
// the same value.
func TestJSONTextKeepsHTMLCharacters(t *testing.T) {
	v := map[string]any{
		"plain":   "a<b>&c",
		"raw":     json.RawMessage(`{"seed":"x\u003c\u0026\u003e\u003C"}`),
		"literal": `\u003c is six characters here, and so is \\u0026`,
		"quote":   `say "<hi>" \ & bye`,
		"nested":  []any{map[string]any{"k": "<&>"}},
	}
	for _, indent := range []bool{false, true} {
		b, err := jsonText(v, indent)
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		if strings.Contains(text, `"x\u003c`) || strings.Contains(text, `a\u003cb`) {
			t.Errorf("indent %v: an escaped < was kept:\n%s", indent, text)
		}
		for _, want := range []string{`"plain":`, `a<b>&c`, `"seed":"x<&><"`, `{"k":"<&>"}`} {
			if !indent && !strings.Contains(text, want) {
				t.Errorf("the text lacks %q:\n%s", want, text)
			}
		}
		if !strings.HasSuffix(text, "\n") {
			t.Errorf("the text does not end with a newline: %q", text)
		}
		var back map[string]any
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatalf("indent %v: the text is not JSON: %v\n%s", indent, err, text)
		}
		if back["plain"] != "a<b>&c" || back["literal"] != v["literal"] || back["quote"] != v["quote"] {
			t.Errorf("indent %v: the values read back as %v", indent, back)
		}
		if raw := back["raw"].(map[string]any)["seed"]; raw != "x<&><" {
			t.Errorf("indent %v: the raw value reads back as %q", indent, raw)
		}
	}
}

// The three characters are the only escapes that are undone.
func TestUnescapeHTMLLeavesOtherEscapes(t *testing.T) {
	for in, want := range map[string]string{
		`"\u003c\u003E\u0026"`:       `"<>&"`,
		`"\\u003c"`:                  `"\\u003c"`,
		`"\\\u003c"`:                 `"\\<"`,
		`"\u0041\u00e9\n\"\u003c"`:   `"\u0041\u00e9\n\"<"`,
		`"\u003`:                     `"\u003`,
		`"x\`:                        `"x\`,
		`"\u2028\u2029\u0000\u001f"`: `"\u2028\u2029\u0000\u001f"`,
		`no escapes`:                 `no escapes`,
	} {
		if got := string(unescapeHTML([]byte(in))); got != want {
			t.Errorf("unescapeHTML(%s) = %s, want %s", in, got, want)
		}
	}
}

const htmlSpec = `package estate

import "pravo.dev/pravo"

url: pravo.#Output & {value: "http://x/?a=1&b=<2>"}
`

// Every command that prints JSON for a person or a program writes the text of
// a value as it is.
func TestJSONOutputKeepsHTMLCharacters(t *testing.T) {
	dir := specDir(t, map[string]string{"main.cue": htmlSpec})
	if text, code := runPravo(t, dir, "init"); code != 0 {
		t.Fatalf("init exited %d:\n%s", code, text)
	}
	const want = "http://x/?a=1&b=<2>"
	commands := [][]string{
		{"plan", "--json"},
		{"apply", "--json", "-auto-approve"},
		{"output", "--json"},
		{"output", "url", "--json"},
		{"state", "query", "--json", "outputs"},
		{"plan", "--json", "-out", "p.plan"},
		{"show", filepath.Join(dir, "p.plan"), "--json"},
	}
	for _, args := range commands {
		text, code := runPravo(t, dir, args...)
		if code != 0 {
			t.Errorf("pravo %s: exit %d:\n%s", strings.Join(args, " "), code, text)
			continue
		}
		if strings.Contains(text, `\u003c`) || strings.Contains(text, `\u0026`) || strings.Contains(text, `\u003e`) {
			t.Errorf("pravo %s escapes <, > or &:\n%s", strings.Join(args, " "), text)
		}
		if !strings.Contains(text, want) {
			t.Errorf("pravo %s does not print %q:\n%s", strings.Join(args, " "), want, text)
		}
		var doc any
		dec := json.NewDecoder(strings.NewReader(text))
		for dec.More() {
			if err := dec.Decode(&doc); err != nil {
				t.Errorf("pravo %s prints text that is not JSON: %v\n%s", strings.Join(args, " "), err, text)
				break
			}
		}
	}
}

// The attributes of a node are stored with their escapes; state show prints
// the characters.
func TestStateShowJSONKeepsHTMLCharacters(t *testing.T) {
	dir := specDir(t, map[string]string{"main.cue": "package estate\n\nsize: 3\n"})
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	node := &state.Resource{Addr: "bucket", Mode: "managed", Type: "t_bucket", Name: "bucket", Attributes: []byte(`{"id":"b1","note":"a\u003cb\u003e\u0026c"}`)}
	if err := st.Set(node, ""); err != nil {
		t.Fatal(err)
	}
	text, code := runPravo(t, dir, "state", "show", "bucket", "--json")
	if code != 0 || !strings.Contains(text, `"note": "a<b>&c"`) {
		t.Errorf("state show --json: exit %d:\n%s", code, text)
	}
}

// The server writes the text of a message as it is.
func TestMCPServerKeepsHTMLCharacters(t *testing.T) {
	var out bytes.Buffer
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n")
	if err := serveMCP(context.Background(), in, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if strings.Contains(text, `\u003c`) || !strings.Contains(text, "out=<file>") {
		t.Errorf("the reply escapes < or >:\n%s", text)
	}
	var reply struct {
		Result struct {
			Instructions string `json:"instructions"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &reply); err != nil || !strings.Contains(reply.Result.Instructions, "out=<file>") {
		t.Errorf("the reply does not read back: %v\n%s", err, text)
	}
}

// noNulls reports the paths in a JSON document that hold null.
func noNulls(path string, v any) []string {
	switch x := v.(type) {
	case nil:
		return []string{path}
	case map[string]any:
		var out []string
		for k, e := range x {
			out = append(out, noNulls(path+"."+k, e)...)
		}
		return out
	case []any:
		var out []string
		for i, e := range x {
			out = append(out, noNulls(path+"[]"+string(rune('0'+i%10)), e)...)
		}
		return out
	}
	return nil
}

// A list that has nothing in it is printed as [] and never as null, whether
// the command has no state, no modules, no providers or no locks to report.
func TestEmptyListsInJSONAreNotNull(t *testing.T) {
	dir := specDir(t, map[string]string{"main.cue": "package estate\n\nimport \"pravo.dev/pravo\"\n\nurl: pravo.#Output & {value: \"x\"}\n"})
	if text, code := runPravo(t, dir, "init"); code != 0 {
		t.Fatalf("init exited %d:\n%s", code, text)
	}
	cases := []struct {
		args []string
		want string // text that the document must contain
	}{
		{[]string{"state", "log", "--json"}, "[]"},
		{[]string{"state", "locks", "--json"}, "[]"},
		{[]string{"mod", "verify", "--json"}, `"problems": []`},
		{[]string{"mod", "list", "--json"}, "[]"},
		{[]string{"providers", "--json"}, "[]"},
		{[]string{"plan", "--json"}, `"changes": []`},
		{[]string{"drift", "--json"}, `"drifted": []`},
		{[]string{"why", "nothing", "--json"}, `"reasons": [`},
		{[]string{"validate", "--json"}, `"valid": true`},
		{[]string{"output", "--json"}, "{}"},
	}
	for _, c := range cases {
		text, code := runPravo(t, dir, c.args...)
		if code != 0 {
			t.Errorf("pravo %s: exit %d:\n%s", strings.Join(c.args, " "), code, text)
			continue
		}
		if !strings.Contains(text, c.want) {
			t.Errorf("pravo %s: the output lacks %q:\n%s", strings.Join(c.args, " "), c.want, text)
		}
		var doc any
		if err := json.Unmarshal([]byte(text), &doc); err != nil {
			t.Errorf("pravo %s: %v:\n%s", strings.Join(c.args, " "), err, text)
			continue
		}
		if nulls := noNulls("$", doc); len(nulls) > 0 {
			t.Errorf("pravo %s prints null at %v:\n%s", strings.Join(c.args, " "), nulls, text)
		}
	}
}

// With a node in the state, the lists of the node are empty and not null.
func TestEmptyListsOfANodeInJSONAreNotNull(t *testing.T) {
	dir := specDir(t, map[string]string{"main.cue": "package estate\n\nsize: 3\n"})
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Set(&state.Resource{Addr: "bucket", Mode: "managed", Type: "t_bucket", Name: "bucket", Attributes: []byte(`{"id":"b1"}`)}, ""); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"state", "show", "bucket", "--json"},
		{"state", "locks", "--json"},
		{"state", "query", "--json", "edges"},
	} {
		text, code := runPravo(t, dir, args...)
		if code != 0 {
			t.Errorf("pravo %s: exit %d:\n%s", strings.Join(args, " "), code, text)
			continue
		}
		var doc any
		if err := json.Unmarshal([]byte(text), &doc); err != nil {
			t.Errorf("pravo %s: %v:\n%s", strings.Join(args, " "), err, text)
			continue
		}
		if nulls := noNulls("$", doc); len(nulls) > 0 {
			t.Errorf("pravo %s prints null at %v:\n%s", strings.Join(args, " "), nulls, text)
		}
	}
	if text, _ := runPravo(t, dir, "state", "show", "bucket", "--json"); !strings.Contains(text, `"dependents": []`) {
		t.Errorf("a node without dependents:\n%s", text)
	}
}

// A transaction file that lacks its list of changes prints an empty list.
func TestTransactionWithoutChangesPrintsAnEmptyList(t *testing.T) {
	dir := specDir(t, map[string]string{"main.cue": "package estate\n\nsize: 3\n"})
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	txns, err := st.Transactions()
	if err != nil || txns == nil || len(txns) != 0 {
		t.Fatalf("Transactions() = %#v, %v; want an empty list", txns, err)
	}
	txn := st.Begin("edit", "pravo state rm")
	if err := txn.Finish("applied", "nothing", true); err != nil {
		t.Fatal(err)
	}
	text, code := runPravo(t, dir, "state", "log", "--json")
	if code != 0 || !strings.Contains(text, `"changes": []`) || strings.Contains(text, "null") {
		t.Errorf("state log --json: exit %d:\n%s", code, text)
	}
}

// The plan document lists the nodes the plan ran. --include-skipped adds the
// nodes it left alone; a root that declares none gives the same document.
func TestPlanJSONTakesIncludeSkipped(t *testing.T) {
	dir, _ := pendingDir(t)
	for _, args := range [][]string{{"plan", "--json"}, {"plan", "--json", "--include-skipped"}} {
		text, code := runPravo(t, dir, args...)
		if code != 0 {
			t.Fatalf("pravo %s: exit %d:\n%s", strings.Join(args, " "), code, text)
		}
		var doc struct {
			Changes []struct{ Addr, Action string }
		}
		if err := json.Unmarshal([]byte(text), &doc); err != nil {
			t.Fatalf("pravo %s prints text that is not JSON: %v\n%s", strings.Join(args, " "), err, text)
		}
		if len(doc.Changes) != 1 || doc.Changes[0].Addr != "cfg" || doc.Changes[0].Action != "delete" {
			t.Errorf("pravo %s lists %+v, want the delete of cfg", strings.Join(args, " "), doc.Changes)
		}
	}
}
