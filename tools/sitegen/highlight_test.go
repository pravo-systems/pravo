package main

import (
	"strings"
	"testing"
)

// classOf returns the class of the first token whose text is exactly text.
func classOf(toks []tok, text string) (string, bool) {
	for _, t := range toks {
		if t.text == text {
			return t.cls, true
		}
	}
	return "", false
}

func TestCUETokens(t *testing.T) {
	src := "package main\n\nimport \"strings\"\n\n#Server: {\n\tname!:   string // required\n\tsize?:   int & >=1 & <=10 | *3\n" +
		"\ttags:    [string]: string\n\tlabel:   \"web-\\(name)-\\(strings.ToUpper(env))\"\n" +
		"\traw:     #\"a \"quoted\" \\#(x)\"#\n\tmulti: \"\"\"\n\t\tline \\(1 + 2)\n\t\t\"\"\"\n" +
		"\tlist: [for k, v in tags if v != \"\" {k}]\n\t@go(Server)\n\tn: 1_000 + 0x1F + 1.5e3 + 1Ki\n" +
		"\t_hidden: _|_\n\tbytes: 'abc'\n\tany: _\n\tr: len(x) + list.Sum(y)\n\t_#Hidden: {a: b: c}\n\tok: true\n}\n"
	toks := lexCUE(src)
	want := map[string]string{
		"package": "kw", "import": "kw", "\"strings\"": "str", "#Server": "def",
		"name": "id", "!": "op", "string": "type", "// required": "com",
		"size": "id", "?": "op", "int": "type", "&": "op", ">=": "op", "1": "num", "<=": "op", "10": "num",
		"|": "op", "*": "op", "3": "num", "tags": "id",
		"\"web-": "str", "\\(": "op", "strings.ToUpper": "fn",
		"#\"a \"quoted\" ": "str", "\\#(": "op", "\"#": "str",
		"for": "kw", "in": "kw", "if": "kw", "!=": "op", "\"\"": "str",
		"@go(Server)": "fn", "1_000": "num", "0x1F": "num", "1.5e3": "num", "1Ki": "num",
		"_hidden": "id", "_|_": "type", "'abc'": "str", "_": "type",
		"len": "fn", "list.Sum": "fn", "_#Hidden": "def", "true": "num",
	}
	for text, cls := range want {
		got, ok := classOf(toks, text)
		if !ok {
			t.Errorf("no token %q", text)
			continue
		}
		if got != cls {
			t.Errorf("token %q: class %q, want %q", text, got, cls)
		}
	}
	// The tokens reassemble to the source.
	var b strings.Builder
	for _, tk := range toks {
		b.WriteString(tk.text)
	}
	if b.String() != src {
		t.Errorf("tokens do not reassemble to the source")
	}
	// A multi-line string is one string token, across the interpolation.
	var multi []string
	for _, tk := range toks {
		if tk.cls == "str" && strings.Contains(tk.text, "line") {
			multi = append(multi, tk.text)
		}
	}
	if len(multi) != 1 || !strings.HasPrefix(multi[0], "\"\"\"") {
		t.Errorf("multi-line string tokens: %q", multi)
	}
}

func TestCUEUnterminatedString(t *testing.T) {
	toks := lexCUE("a: \"open\nb: 1\n")
	if c, _ := classOf(toks, "b"); c != "id" {
		t.Errorf("label after an unterminated string: %q", c)
	}
}

func TestHCLTokens(t *testing.T) {
	src := "# comment\nresource \"aws_s3_bucket\" \"b\" {\n  bucket = \"x-${var.env}-${upper(\"a\")}\"\n  count  = var.enabled ? 1 : 0\n" +
		"  dynamic \"rule\" {\n    for_each = var.rules\n  }\n  /* block\n  comment */\n  content = <<-EOT\n  hello ${x}\n  EOT\n" +
		"  names = [for s in var.list : upper(s) if s != \"\"]\n  type = list(string)\n  ok = true\n}\n"
	toks := lexHCL(src)
	want := map[string]string{
		"# comment": "com", "resource": "kw", "\"aws_s3_bucket\"": "str", "bucket": "id", "${": "op",
		"upper": "fn", "count": "id", "?": "op", "1": "num", "dynamic": "kw", "for_each": "id",
		"/* block\n  comment */": "com", "<<-EOT\n  hello ${x}\n  EOT": "str",
		"for": "kw", "in": "kw", "if": "kw", "!=": "op", "list": "fn", "string": "type", "true": "num",
	}
	for text, cls := range want {
		got, ok := classOf(toks, text)
		if !ok {
			t.Errorf("no token %q", text)
			continue
		}
		if got != cls {
			t.Errorf("token %q: class %q, want %q", text, got, cls)
		}
	}
	var b strings.Builder
	for _, tk := range toks {
		b.WriteString(tk.text)
	}
	if b.String() != src {
		t.Errorf("tokens do not reassemble to the source")
	}
}

func TestOtherLanguages(t *testing.T) {
	cases := []struct {
		lang, src, text, cls string
	}{
		{"json", `{"a": 1, "b": true}`, `"a"`, "id"},
		{"json", `{"a": 1}`, "1", "num"},
		{"yaml", "name: plan # c\n", "name", "id"},
		{"yaml", "name: plan # c\n", "# c", "com"},
		{"bash", "echo \"hi\" # note\n", "# note", "com"},
		{"sh", "if true; then echo x; fi\n", "if", "kw"},
		{"go", "package main\n", "package", "kw"},
		{"diff", "@@ -1 +1 @@\n-a\n+b\n", "@@ -1 +1 @@\n", "com"},
	}
	for _, c := range cases {
		got, ok := classOf(highlight(c.lang, c.src), c.text)
		if !ok || got != c.cls {
			t.Errorf("%s %q: class %q (found %v), want %q", c.lang, c.text, got, ok, c.cls)
		}
	}
	// Unknown languages and text stay plain.
	for _, lang := range []string{"nonesuch", "text", ""} {
		toks := highlight(lang, "a: b\n")
		if len(toks) != 1 || toks[0].cls != "" {
			t.Errorf("%q: %v", lang, toks)
		}
	}
}

func TestSplitLines(t *testing.T) {
	lines := splitLines([]tok{{"kw", "a\nb"}, {"", "\n\nc"}})
	if len(lines) != 4 {
		t.Fatalf("lines: %v", lines)
	}
	if len(lines[2]) != 0 {
		t.Errorf("third line should be empty: %v", lines[2])
	}
	// A trailing newline does not add a line.
	if got := len(splitLines([]tok{{"", "a\nb\n"}})); got != 2 {
		t.Errorf("a source ending in a newline has %d lines, want 2", got)
	}
	if got := len(splitLines(highlight("text", codeText("a\nb\n\n\n")))); got != 2 {
		t.Errorf("trimmed block has %d lines", got)
	}
}
