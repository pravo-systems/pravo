package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

func promotionOf(t *testing.T, id *protect.Identity, at time.Time, tags map[string]string) *state.Promotion {
	t.Helper()
	p := &state.Promotion{V: 1, Kind: state.PromotionKind, Fingerprint: "f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1", Tags: tags,
		Txn: "20260101T000000Z-aaaaaa", Applied: at, At: at, Note: "n"}
	if err := p.Sign(id, "lineage-1"); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAPromotionCountsWhenPinnedSpecifiedAndOldEnough(t *testing.T) {
	ada, err := protect.NewIdentity("ada", "")
	if err != nil {
		t.Fatal(err)
	}
	eve, err := protect.NewIdentity("eve", "")
	if err != nil {
		t.Fatal(err)
	}
	pin := protect.SSHFingerprint(ada.SignPub)
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	tags := func(env, image string) map[string]string {
		return state.SpecTagDigests([]string{"env=" + env, "image=" + image})
	}
	need := func(min, max string) PromotionNeed {
		n := PromotionNeed{PromoteSpec: PromoteSpec{Signers: []string{pin}, Vary: []string{"env"}, MinAge: min, MaxAge: max}}
		var err error
		if min != "" {
			n.minAge, err = ParseDuration(min)
		}
		if max != "" && err == nil {
			n.maxAge, err = ParseDuration(max)
		}
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	cases := []struct {
		name  string
		p     *state.Promotion
		here  map[string]string
		need  PromotionNeed
		want  string // "" counts; else a part of the reason
		level int
	}{
		{"counts", promotionOf(t, ada, now.Add(-time.Hour), tags("staging", "1.4")), tags("prod", "1.4"), need("", ""), "", 4},
		{"another key", promotionOf(t, eve, now.Add(-time.Hour), tags("staging", "1.4")), tags("prod", "1.4"), need("", ""), "does not pin", 0},
		{"another version tag", promotionOf(t, ada, now.Add(-time.Hour), tags("staging", "1.4")), tags("prod", "1.5"), need("", ""), "tag image has another value", 2},
		{"tag not set before", promotionOf(t, ada, now.Add(-time.Hour), state.SpecTagDigests([]string{"env=staging"})), tags("prod", "1.4"), need("", ""), "tag image is set now", 2},
		{"env does not vary", promotionOf(t, ada, now.Add(-time.Hour), tags("staging", "1.4")), tags("prod", "1.4"), PromotionNeed{PromoteSpec: PromoteSpec{Signers: []string{pin}}}, "add it to vary", 2},
		{"too young", promotionOf(t, ada, now.Add(-time.Hour), tags("staging", "1.4")), tags("prod", "1.4"), need("24h", ""), "stand for 24h", 3},
		{"old enough", promotionOf(t, ada, now.Add(-30*time.Hour), tags("staging", "1.4")), tags("prod", "1.4"), need("24h", "7d"), "", 4},
		{"expired", promotionOf(t, ada, now.Add(-8*24*time.Hour), tags("staging", "1.4")), tags("prod", "1.4"), need("", "7d"), "counts one for 7d", 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, level := c.need.judge(c.p, "lineage-1", c.here, now)
			if c.want == "" && got != "" || !strings.Contains(got, c.want) {
				t.Fatalf("judge = %q, want %q", got, c.want)
			}
			if level != c.level {
				t.Fatalf("level = %d, want %d", level, c.level)
			}
		})
	}
	t.Run("another state", func(t *testing.T) {
		got, _ := need("", "").judge(promotionOf(t, ada, now, tags("staging", "1")), "lineage-2", tags("prod", "1"), now)
		if !strings.Contains(got, "does not verify") {
			t.Fatalf("an attestation copied into another state: %q", got)
		}
	})
	t.Run("changed after signing", func(t *testing.T) {
		p := promotionOf(t, ada, now, tags("staging", "1"))
		p.Fingerprint = "00000000000000000000000000000000"
		got, _ := need("", "").judge(p, "lineage-1", tags("prod", "1"), now)
		if !strings.Contains(got, "does not verify") {
			t.Fatalf("an altered attestation: %q", got)
		}
	})
}
