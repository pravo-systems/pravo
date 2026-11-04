package convert

import (
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// markup is a string whose characters a JSON encoder escapes for HTML.
const markup = "<h1>Tom & Jerry's \"page\"</h1>\n"

func TestRenderCUEKeepsMarkupCharacters(t *testing.T) {
	obj := tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"content": tftypes.String,
		"tags":    tftypes.List{ElementType: tftypes.String},
		"a<b&c":   tftypes.String,
	}}
	v := tftypes.NewValue(obj, map[string]tftypes.Value{
		"content": tftypes.NewValue(tftypes.String, markup),
		"tags":    tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, []tftypes.Value{tftypes.NewValue(tftypes.String, "<b>&</b>")}),
		"a<b&c":   tftypes.NewValue(tftypes.String, "x"),
	})
	got := RenderCUE(v, "", nil, nil)
	if strings.Contains(got, `\u00`) {
		t.Errorf("RenderCUE escapes markup characters:\n%s", got)
	}
	for _, want := range []string{
		`content: "<h1>Tom & Jerry's \"page\"</h1>\n"`,
		`"<b>&</b>"`,
		`"a<b&c": "x"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderCUE lacks %s:\n%s", want, got)
		}
	}
}

// The text RenderCUE writes for a string is CUE that evaluates to the string.
func TestRenderCUEStringsRoundTrip(t *testing.T) {
	ctx := cuecontext.New()
	for _, s := range []string{
		markup,
		"",
		"tab\there",
		`back\slash and \(interpolation)`,
		"line\u2028separator",
		"snowman \u2603 and <\u00e9>",
		"nul\x00byte",
	} {
		rendered := RenderCUE(tftypes.NewValue(tftypes.String, s), "", nil, nil)
		v := ctx.CompileString(rendered)
		got, err := v.String()
		if err != nil || got != s {
			t.Errorf("RenderCUE(%q) = %s, which evaluates to %q (%v)", s, rendered, got, err)
		}
	}
}

func TestFieldLabelKeepsMarkupCharacters(t *testing.T) {
	for in, want := range map[string]string{
		"plain":  "plain",
		"a<b":    `"a<b"`,
		"x&y":    `"x&y"`,
		"a-b":    `"a-b"`,
		"":       `""`,
		"for":    `"for"`,
		"<tag>":  `"<tag>"`,
		"a.b>c&": `"a.b>c&"`,
	} {
		if got := FieldLabel(in); got != want {
			t.Errorf("FieldLabel(%q) = %s, want %s", in, got, want)
		}
	}
}

// A provider that stores a configured block in another shape (fewer elements, other order, other
// values) must not make the fill conflict with the configuration.
func TestFillKeepsConfiguredListWhenProviderReshapesIt(t *testing.T) {
	ctx := cuecontext.New()
	elem := tftypes.Object{AttributeTypes: map[string]tftypes.Type{"enabled": tftypes.Bool, "arn": tftypes.String}}
	lst := tftypes.List{ElementType: elem}
	mk := func(enabled bool, arn string) tftypes.Value {
		return tftypes.NewValue(elem, map[string]tftypes.Value{
			"enabled": tftypes.NewValue(tftypes.Bool, enabled),
			"arn":     tftypes.NewValue(tftypes.String, arn),
		})
	}
	cases := []struct {
		name, cfg string
		state     tftypes.Value
		wantOK    bool
	}{
		{"block normalised to empty", `[{enabled: false, arn: string}]`, tftypes.NewValue(lst, []tftypes.Value{}), false},
		{"extra stored element", `[{enabled: true, arn: string}]`, tftypes.NewValue(lst, []tftypes.Value{mk(true, "a"), mk(true, "b")}), false},
		{"stored value differs", `[{enabled: false, arn: string}]`, tftypes.NewValue(lst, []tftypes.Value{mk(true, "a")}), true},
	}
	for _, c := range cases {
		cfg := ctx.CompileString(c.cfg)
		got, ok := fillValue(cfg, c.state)
		if ok != c.wantOK {
			t.Errorf("%s: filled = %v, want %v (%v)", c.name, ok, c.wantOK, got)
			continue
		}
		if !ok {
			continue
		}
		v := cfg.Unify(ctx.Encode(got))
		if err := v.Validate(); err != nil {
			t.Errorf("%s: fill conflicts with the configuration: %v", c.name, err)
		}
	}
}

// A provider may return the elements of a set in another order than the configuration lists them;
// computed attributes still go to the element they belong to.
func TestFillPairsSetElementsByConfiguredValues(t *testing.T) {
	ctx := cuecontext.New()
	elem := tftypes.Object{AttributeTypes: map[string]tftypes.Type{"name": tftypes.String, "arn": tftypes.String}}
	mk := func(n, a string) tftypes.Value {
		return tftypes.NewValue(elem, map[string]tftypes.Value{"name": tftypes.NewValue(tftypes.String, n), "arn": tftypes.NewValue(tftypes.String, a)})
	}
	st := tftypes.NewValue(tftypes.Set{ElementType: elem}, []tftypes.Value{mk("b", "arn-b"), mk("a", "arn-a")})
	cfg := ctx.CompileString(`[{name: "a", arn: string}, {name: "b", arn: string}]`)
	got, ok := fillValue(cfg, st)
	if !ok {
		t.Fatal("not filled")
	}
	v := cfg.Unify(ctx.Encode(got))
	if err := v.Validate(); err != nil {
		t.Fatal(err)
	}
	a, _ := v.LookupPath(cue.ParsePath("[0].arn")).String()
	b, _ := v.LookupPath(cue.ParsePath("[1].arn")).String()
	if a != "arn-a" || b != "arn-b" {
		t.Errorf("arns = %q, %q", a, b)
	}
}
