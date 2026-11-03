package protect

// Property test: whatever readers file the
// command line accepts, the roles that NextRoles and FormatRolesSet make must parse again,
// because pravo stores them before it reads them back.

import (
	"crypto/ed25519"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func simKey(r *rand.Rand) ed25519.PublicKey {
	pub := make([]byte, 32)
	r.Read(pub)
	return ed25519.PublicKey(pub)
}

func simFP(r *rand.Rand) string {
	const alpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	b := make([]byte, 43)
	for i := range b {
		b[i] = alpha[r.Intn(len(alpha))]
	}
	return "SHA256:" + string(b)
}

func simName(r *rand.Rand, pool []string) string {
	if r.Intn(4) == 0 {
		n := 1 + r.Intn(64)
		var sb strings.Builder
		for i := 0; i < n; i++ {
			c := "abcXYZ019"[r.Intn(9)]
			if i > 0 && r.Intn(8) == 0 {
				c = "._-"[r.Intn(3)]
			}
			sb.WriteByte(c)
		}
		return sb.String()
	}
	return pool[r.Intn(len(pool))]
}

func TestRolesRoundTrip(t *testing.T) {
	pool := []string{"ada", "Ada", "dan", "dan-2", "dan-3", "eve", "former-abcdefgh", "former-ab_cd-ef", strings.Repeat("E", 64), strings.Repeat("e", 63), strings.Repeat("x", 62), "a", "9"}
	bad := map[string]int{}
	first := map[string]string{}
	const runs = 3000
	for seed := int64(0); seed < runs; seed++ {
		r := rand.New(rand.NewSource(seed))
		nfile := 1 + r.Intn(6)
		var file []Reader
		names := map[string]bool{}
		for i := 0; i < nfile; i++ {
			nm := simName(r, pool)
			if names[strings.ToLower(nm)] || strings.EqualFold(nm, RecoveryName) || !nameRE.MatchString(nm) {
				continue
			}
			names[strings.ToLower(nm)] = true
			role := []Role{RoleNone, RoleReader, RoleWriter, RoleAdmin}[r.Intn(4)]
			sign := simKey(r)
			if len(file) > 0 && r.Intn(25) == 0 {
				continue // the same signing key on two lines: the readers file parser refuses it
			}
			file = append(file, Reader{Name: nm, Sign: sign, Role: role})
		}
		if len(file) == 0 {
			continue
		}
		// previous roles: some of the file's keys under old names, some departed keys
		var prev []Reader
		var prevFormer []FormerKey
		if r.Intn(2) == 0 {
			for _, f := range file {
				if r.Intn(2) == 0 {
					prev = append(prev, Reader{Name: simName(r, pool), Sign: f.Sign, Role: RoleReader})
				}
			}
			for i := 0; i < r.Intn(3); i++ {
				prev = append(prev, Reader{Name: simName(r, pool), Sign: simKey(r), Role: RoleWriter})
			}
			for i := 0; i < r.Intn(3); i++ {
				prevFormer = append(prevFormer, FormerKey{Name: simName(r, pool), Key: simFP(r)})
			}
		}
		var seen []string
		for i := 0; i < r.Intn(3); i++ {
			seen = append(seen, simFP(r))
		}
		// prev must itself be something that was stored, so its names must be unique and valid
		pn := map[string]bool{}
		okPrev := true
		for _, p := range prev {
			if pn[strings.ToLower(p.Name)] || !nameRE.MatchString(p.Name) {
				okPrev = false
			}
			pn[strings.ToLower(p.Name)] = true
		}
		for _, f := range prevFormer {
			if pn[strings.ToLower(f.Name)] || !nameRE.MatchString(f.Name) {
				okPrev = false
			}
			pn[strings.ToLower(f.Name)] = true
		}
		if !okPrev {
			continue
		}
		active, former := NextRoles(file, prev, prevFormer, seen)
		text := FormatRolesSet(active, former, nil)
		if _, _, _, err := ParseRolesSet(text); err != nil {
			// classify
			kind := "other"
			switch {
			case strings.Contains(err.Error(), "listed twice"):
				kind = "the same signing key on two lines"
			case strings.Contains(err.Error(), "name is not valid"):
				kind = "a name that is not valid or used twice"
			}
			bad[kind]++
			if _, ok := first[kind]; !ok {
				first[kind] = fmt.Sprintf("seed %d: %v\nfile=%v prev=%v prevFormer=%v seen=%v\n%s", seed, err, namesOf(file), namesOf(prev), prevFormer, seen, text)
			}
		}
	}
	for k, n := range bad {
		t.Errorf("%d of %d inputs: %s\n%s", n, runs, k, first[k])
	}
}

func namesOf(rs []Reader) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Name)
	}
	return out
}
