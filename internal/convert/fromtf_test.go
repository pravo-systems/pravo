package convert

import (
	"strings"
	"testing"

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
