package protect

import (
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
