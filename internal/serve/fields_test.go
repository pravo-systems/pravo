package serve

import (
	"testing"
)

func TestGlob(t *testing.T) {
	for _, c := range []struct {
		pat, s string
		want   bool
	}{
		{"abc", "abc", true}, {"abc", "abd", false}, {"a*", "abc", true}, {"*c", "abc", true}, {"a*c", "abc", true},
		{"a*c", "ac", true}, {"a*a", "a", false}, {"*b*", "abc", true}, {"*x*", "abc", false}, {"*", "", true}, {"a*b*c", "axxbyyc", true},
	} {
		if got := glob(c.pat, c.s); got != c.want {
			t.Errorf("glob(%q, %q) = %v", c.pat, c.s, got)
		}
	}
}

func TestPathMatches(t *testing.T) {
	for _, c := range []struct {
		pat, path string
		want      bool
	}{
		{"region", "region", true}, {"region", "settings.region", true}, {"region", "regions", false}, {"settings.region", "settings.region", true},
		{"settings.region", "x.settings.region", false}, {"rule.*.cidr", "rule.0.cidr", true}, {"tags.*", "tags.env", true}, {"tags.*", "tag.env", false}, {"zones", "zones.0", true}, {"settings", "settings.region", true}, {"rule.*", "rule.0.cidr", true},
	} {
		if got := pathMatches(c.pat, c.path); got != c.want {
			t.Errorf("pathMatches(%q, %q) = %v", c.pat, c.path, got)
		}
	}
}

func TestFlattenAttrWithholds(t *testing.T) {
	attrs := []Attr{
		{Name: "name", Value: "db"},
		{Name: "password", Redacted: "name", Value: "hunter2"},
		{Name: "port", Value: float64(5432)},
		{Name: "gone", Null: true},
		{Name: "settings", Children: []Attr{{Name: "region", Value: "eu"}, {Name: "key", Redacted: "sensitive"}}},
		{Name: "zones", Children: []Attr{{Name: "0", Value: "x"}}},
	}
	var rows []fieldRow
	for i := range attrs {
		rows = flattenAttr(rows, attrs[i].Name, &attrs[i])
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.path] = r.value
	}
	want := map[string]string{"name": "db", "port": "5432", "settings.region": "eu", "zones.0": "x"}
	if len(got) != len(want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func fieldAddrs(p *project, q string) []string {
	p.t.Helper()
	var f Fields
	if err := jsonUnmarshal(p.get("/api/fields?q="+queryEscape(q)), &f); err != nil {
		p.t.Fatal(err)
	}
	return f.Addrs
}

// A field search finds what the detail shows and nothing that it withholds.
func TestFieldSearch(t *testing.T) {
	p := newProject(t)
	has := func(q string, want ...string) {
		t.Helper()
		got := fieldAddrs(p, q)
		if len(got) != len(want) {
			t.Errorf("%q: %v, want %v", q, got, want)
			return
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%q: %v, want %v", q, got, want)
				return
			}
		}
	}
	has("settings.region=eu", "services.a.db", "services.b.db")
	has("region=EU", "services.a.db", "services.b.db") // a bare name, any case
	has("name=shared", "shared")
	has("port=5432", "services.a.db", "services.b.db", "shared")
	has("tags.owner=team", "services.a.db", "services.b.db")
	has("zones.1=eu-2", "services.a.db", "services.b.db")
	has("rule.*.cidr=10.*", "services.a.db", "services.b.db")
	has("name=*b", "services.b.db")
	has("zones", "services.a.db", "services.b.db") // the field exists
	has("value=db.example", "host")                // an output
	// withheld values never match, whatever they are named, and neither does their presence
	for _, q := range []string{"blob=" + secretBlob, "blob", "api_token=" + secretToken, "payload=" + secretWrite, "tags.db_password=" + secretMapKey,
		"settings.key=" + secretNested, "rule.0.pin=" + secretPin, "legacy=" + secretUnknown, "key", "pin", "value=" + secretBlob} {
		if got := fieldAddrs(p, q); len(got) != 0 {
			t.Errorf("%q matched %v", q, got)
		}
	}
	if got := fieldAddrs(p, ""); len(got) != 0 {
		t.Errorf("an empty query matched %v", got)
	}
}
