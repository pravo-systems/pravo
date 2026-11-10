package sensitivity

import (
	"encoding/base64"
	"encoding/hex"
	"html"
	"net/url"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/schema"
)

func TestTaintedFindsEveryForm(t *testing.T) {
	const secret = "s3cr3t Value/&!<x>"
	for name, text := range map[string]string{
		"raw":    "a " + secret + " b",
		"query":  "postgres://u:" + url.QueryEscape(secret) + "@h",
		"path":   "/x/" + url.PathEscape(secret),
		"html":   html.EscapeString(secret),
		"hex":    hex.EncodeToString([]byte(secret)),
		"upper":  strings.ToUpper(hex.EncodeToString([]byte(secret))),
		"base64": "d=" + base64.StdEncoding.EncodeToString([]byte("k="+secret)),
		"b64url": base64.URLEncoding.EncodeToString([]byte(secret)),
	} {
		if !Tainted(text, []string{secret}) {
			t.Errorf("%s: a form of the secret is not found in %q", name, text)
		}
	}
	if Tainted("nothing to see here", []string{secret}) || Tainted(secret, []string{"short"}) {
		t.Error("a text without the secret, or a secret that is too short, is tainted")
	}
}

func TestPathPredicateAndNames(t *testing.T) {
	blk := &schema.Block{Attributes: []*schema.Attribute{
		{Name: "db_password"}, {Name: "plain"}, {Name: "w", WriteOnly: true},
		{Name: "cfg", NestedType: &schema.Object{Nesting: schema.NestingSingle, Attributes: []*schema.Attribute{{Name: "pw", Sensitive: true}}}},
	}}
	sens := PathPredicate(blk)
	for _, p := range [][]string{{"db_password"}, {"w"}, {"cfg", "pw"}} {
		if !sens(p) {
			t.Errorf("%v is not withheld", p)
		}
	}
	if sens([]string{"plain"}) || sens([]string{"cfg", "other"}) {
		t.Error("a plain path is withheld")
	}
	if !HasWriteOnly(blk) {
		t.Error("the write-only attribute is not seen")
	}
	if got := SensitiveNames(blk); !got["db_password"] || !got["pw"] || !got["w"] || got["plain"] {
		t.Errorf("names: %v", got)
	}
}

func TestWriteOnlyVersionIsNotGuarded(t *testing.T) {
	blk := &schema.Block{Attributes: []*schema.Attribute{{Name: "private_key_pem_wo"}, {Name: "private_key_pem_wo_version"}}}
	sens := PathPredicate(blk)
	if sens([]string{"private_key_pem_wo_version"}) || !sens([]string{"private_key_pem_wo"}) {
		t.Error("the version counter must show and the value must not")
	}
}
