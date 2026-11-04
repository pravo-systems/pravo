package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

// A readers file that does not pin the roles in the state must not raise the role of the key
// that applies: the chain in the state only restricts (F38).
func TestUnpinnedChainLowersRolesOfTheFile(t *testing.T) {
	b := newBoundHarness(t)
	ctx := context.Background()
	chainRole := func(role protect.Role) {
		t.Helper()
		// The first version is signed by an admin of itself; a later one by an admin of the one
		// before, so a key that ends up a writer is demoted by another admin's version.
		other, err := protect.NewIdentity("root", "")
		if err != nil {
			t.Fatal(err)
		}
		o := other.AsReader()
		o.Role = protect.RoleAdmin
		r := b.readers[0]
		r.Role = protect.RoleAdmin
		if err := b.state.ReplaceRoles(ctx, protect.FormatRolesSet([]protect.Reader{r, o}, nil, nil), time.Now()); err != nil {
			t.Fatal(err)
		}
		if role == protect.RoleAdmin {
			return
		}
		r.Role = role
		if err := b.state.PutRolesVersion(ctx, 2, protect.FormatRolesSet([]protect.Reader{r, o}, nil, nil), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	removal := &Plan{Policies: []PolicyEntry{{Name: "second_opinion", Base: 1}}}
	need := &ApprovalNeed{Min: 1, Role: protect.RoleWriter, By: []string{"second_opinion@1"}}

	file := func(role protect.Role) []protect.Reader {
		r := b.readers[0]
		r.Role = role
		return []protect.Reader{r}
	}
	for _, c := range []struct {
		name      string
		chain     protect.Role
		file      []protect.Reader
		refuses   bool
		fromState bool
	}{
		{"writer in chain, admin in file", protect.RoleWriter, file(protect.RoleAdmin), true, true},
		{"writer in chain, roles off in file", protect.RoleWriter, file(protect.RoleNone), true, true},
		{"admin in chain, admin in file", protect.RoleAdmin, file(protect.RoleAdmin), false, false},
		{"admin in chain, roles off in file", protect.RoleAdmin, file(protect.RoleNone), false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			chainRole(c.chain)
			e := &Engine{State: b.state, Opts: Options{Readers: c.file}}
			err := e.checkPolicyRoles(ctx, removal)
			if (err != nil) != c.refuses {
				t.Fatalf("checkPolicyRoles: %v, refuses want %v", err, c.refuses)
			}
			if err != nil && !strings.Contains(err.Error(), "roles stored in the state") {
				t.Errorf("the refusal does not say where the role comes from: %v", err)
			}
			// Without a stored plan an admin is exempt and anybody else is refused.
			err = e.checkApprovals(ctx, need)
			if (err != nil) != c.refuses {
				t.Fatalf("checkApprovals: %v, refuses want %v", err, c.refuses)
			}
		})
	}
}

func TestCapRoles(t *testing.T) {
	b := newBoundHarness(t)
	file := b.readers
	stored := []protect.Reader{}
	got := protect.CapRoles(file, stored)
	if r, _ := protect.RoleOf(got, b.state.SigningKey()); r != protect.RoleReader {
		t.Errorf("a key the chain does not list is %v, want reader", r)
	}
}
