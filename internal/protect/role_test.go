package protect

import (
	"strings"
	"testing"
)

func roleLine(t *testing.T, name, suffix string) (Reader, string) {
	t.Helper()
	id := signer(t, name)
	line := id.AsReader().String() + suffix
	rd, err := ParseReaderLine(line)
	if err != nil {
		t.Fatalf("%s: %v", line, err)
	}
	return rd, line
}

func TestRoleParsesAndPrints(t *testing.T) {
	for _, want := range []Role{RoleNone, RoleReader, RoleWriter, RoleAdmin} {
		suffix := ""
		if want != RoleNone {
			suffix = " role:" + want.String()
		}
		rd, line := roleLine(t, "ada", suffix)
		if rd.Role != want || rd.String() != line {
			t.Errorf("role %v: parsed %v, printed %q, want %q", want, rd.Role, rd.String(), line)
		}
	}
	_, line := roleLine(t, "bob", "")
	for _, bad := range []string{" role:root", " admin", " role:", " role:admin extra"} {
		if _, err := ParseReaderLine(line + bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestRoleOf(t *testing.T) {
	a, _ := roleLine(t, "ada", " role:admin")
	w, _ := roleLine(t, "wes", " role:writer")
	r, _ := roleLine(t, "ray", "")
	fp := func(x Reader) string { return SSHFingerprint(x.Sign) }
	on := []Reader{a, w, r}
	for _, c := range []struct {
		who  Reader
		want Role
	}{{a, RoleAdmin}, {w, RoleWriter}, {r, RoleReader}} {
		if got, ok := RoleOf(on, fp(c.who)); !ok || got != c.want {
			t.Errorf("%s: %v %v, want %v", c.who.Name, got, ok, c.want)
		}
	}
	// Roles off: every key is an admin.
	off := []Reader{{Name: "x", Sign: r.Sign, Key: r.Key}}
	if got, ok := RoleOf(off, fp(r)); !ok || got != RoleAdmin {
		t.Errorf("roles off: %v %v", got, ok)
	}
	if _, ok := RoleOf(on, "SHA256:nobody"); ok {
		t.Error("an unlisted key has a role")
	}
	if !RoleWriter.Allows(RoleReader) || RoleReader.Allows(RoleWriter) || !RoleAdmin.Allows(RoleAdmin) {
		t.Error("the order is not linear")
	}
}

func TestNextRolesKeepsAKeyThatLeftAsFormer(t *testing.T) {
	ada, _ := roleLine(t, "ada", " role:admin")
	dan, _ := roleLine(t, "dan", " role:writer")
	eve, _ := roleLine(t, "eve", "")

	active, former := NextRoles([]Reader{ada, eve}, []Reader{ada, dan}, nil, nil)
	if len(active) != 2 || active[1].Role != RoleReader {
		t.Fatalf("active = %+v", active)
	}
	if len(former) != 1 || former[0].Name != "dan" || former[0].Key != SSHFingerprint(dan.Sign) {
		t.Fatalf("former = %+v", former)
	}
	vouch := []Vouch{{Key: former[0].Key, Txn: "20261027T084112Z-e8d40e", Digest: strings.Repeat("ab", 32)}}
	text := FormatRolesSet(active, former, vouch)
	rs, fs, vs, err := ParseRolesSet(text)
	if err != nil || len(rs) != 2 || len(fs) != 1 || len(vs) != 1 || FormatRolesSet(rs, fs, vs) != text {
		t.Fatalf("round trip: %v\n%s", err, text)
	}

	// The key comes back: it is active and no longer former. A new person named dan gets another name.
	again, formerAgain := NextRoles([]Reader{ada, dan}, rs, fs, nil)
	if len(again) != 2 || len(formerAgain) != 1 || formerAgain[0].Name != "eve" {
		t.Fatalf("after dan returned: %+v %+v", again, formerAgain)
	}
	dan2, _ := roleLine(t, "dan", "")
	_, formerClash := NextRoles([]Reader{ada, dan2}, rs, fs, []string{"SHA256:" + "A" + "B" + "CDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnop"})
	names := map[string]bool{}
	for _, f := range formerClash {
		if names[f.Name] {
			t.Errorf("name %q twice in %+v", f.Name, formerClash)
		}
		names[f.Name] = true
	}
	if _, _, _, err := ParseRolesSet("dan SHA256:short role:former\n"); err == nil {
		t.Error("a short fingerprint was accepted")
	}
}
