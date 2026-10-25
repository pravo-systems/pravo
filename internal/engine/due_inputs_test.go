package engine

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/env"
)

// agedAll makes every record of the state as old as ago.
func agedAll(t *testing.T, h *harness, ago time.Duration, addrs ...string) {
	t.Helper()
	for _, addr := range addrs {
		r := *h.state.Get(addr)
		at := time.Now().UTC().Add(-ago)
		r.RefreshedAt = &at
		if err := h.state.Set(&r, ""); err != nil {
			t.Fatal(err)
		}
	}
}

// driftDue reads the records that are due, with a new engine, as a command does.
func driftDue(t *testing.T, h *harness, s spec) (*DueReport, error) {
	t.Helper()
	h.reopen()
	h.fake.resetCalls()
	h.fake.configured = nil
	return h.engine(s, Options{DefaultRefresh: "1m", Env: env.OS()}).DriftDue(context.Background(), 0)
}

// A provider that is set up from the environment, from an ephemeral resource, from
// a function call or from a resource whose own configuration reads the environment
// is set up for the reads of "pravo drift --due" as it is for a plan: the sweep
// that a scheduled job runs covers the records of such a provider.
func TestDriftDueSetsUpAProviderFromWhatTheStateDoesNotHold(t *testing.T) {
	t.Setenv("PRAVO_DUE_REGION", "us-9")
	cases := []struct {
		name   string
		s      spec
		region string
		calls  map[string]int
	}{
		{
			name: "an environment variable",
			s: spec{
				cue:       "home: {name: \"PRAVO_DUE_REGION\", default: \"eu-1\", value: string}\nprov: {region: home.value}\na: " + thing("a", "") + "\n",
				resources: []string{"a"}, builtins: []string{"home"}, providers: map[string]string{"fake": "prov"},
			},
			region: "us-9",
		},
		{
			name: "an ephemeral resource",
			s: spec{
				cue:       "sess: {name: \"sess\", token: string | null}\nprov: {region: sess.token}\na: " + thing("a", "") + "\n",
				resources: []string{"a"}, ephemerals: []string{"sess"}, providers: map[string]string{"fake": "prov", "fake.docs": "unset"},
				keys: map[string]string{"sess": "fake.docs"},
			},
			region: "token-sess",
			calls:  map[string]int{"OpenEphemeral": 1, "CloseEphemeral": 1},
		},
		{
			name: "a function call",
			s: spec{
				cue:       "fn: {args: [\"abc\"], result: string | null}\nprov: {region: fn.result}\na: " + thing("a", "") + "\n",
				resources: []string{"a"}, calls: []string{"fn"}, providers: map[string]string{"fake": "prov"},
			},
			region: "ABC",
			calls:  map[string]int{"CallFunction": 1},
		},
		{
			name: "a resource that reads the environment",
			s: spec{
				cue: "home: {name: \"PRAVO_DUE_REGION\", default: \"eu-1\", value: string}\nbase: " + thing("base", `content: home.value`) +
					"\nprov: {region: base.content}\na: " + thing("a", "") + "\n",
				resources: []string{"base", "a"}, builtins: []string{"home"}, providers: map[string]string{"fake": "prov"},
				keys: map[string]string{"base": "fake.docs"},
			},
			region: "us-9",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.apply(c.s)
			agedAll(t, h, time.Hour, "a")
			rep, err := driftDue(t, h, c.s)
			if err != nil {
				t.Fatalf("drift --due: %v", err)
			}
			if !reflect.DeepEqual(rep.Read, []string{"a"}) {
				t.Errorf("read %v, want [a]", rep.Read)
			}
			if got := h.fake.regions(); len(got) == 0 || got[len(got)-1] != c.region {
				t.Errorf("the provider was configured with the regions %q, want it to end with %q", got, c.region)
			}
			if got := h.fake.readNames(); !reflect.DeepEqual(got, []string{"a"}) {
				t.Errorf("the provider read %v, want [a]", got)
			}
			for call, n := range c.calls {
				if got := h.fake.called(call); got != n {
					t.Errorf("%s ran %d times, want %d", call, got, n)
				}
			}
		})
	}
}

// A provider that is set up from a resource whose record is missing cannot be set
// up: the error names the resource.
func TestDriftDueNamesTheRecordThatAProviderNeeds(t *testing.T) {
	s := spec{
		cue:       "base: " + thing("base", `content: "eu-1"`) + "\nprov: {region: base.content}\na: " + thing("a", "") + "\n",
		resources: []string{"base", "a"}, providers: map[string]string{"fake": "prov"},
		keys: map[string]string{"base": "fake.docs"},
	}
	h := newHarness(t)
	h.apply(spec{cue: "a: " + thing("a", "") + "\n", resources: []string{"a"}})
	agedAll(t, h, time.Hour, "a")
	_, err := driftDue(t, h, s)
	if err == nil || !strings.Contains(err.Error(), "provider fake is set up from base, which the state does not hold") {
		t.Fatalf("error = %v, want it to name base", err)
	}
}

// The ephemeral resources a due read opens are closed when it ends, also when the
// read fails.
func TestDriftDueClosesWhatItOpened(t *testing.T) {
	s := spec{
		cue:       "sess: {name: \"sess\", token: string | null}\nprov: {region: sess.token}\na: " + thing("a", "") + "\n",
		resources: []string{"a"}, ephemerals: []string{"sess"}, providers: map[string]string{"fake": "prov", "fake.docs": "unset"},
		keys: map[string]string{"sess": "fake.docs"},
	}
	h := newHarness(t)
	h.apply(s)
	agedAll(t, h, time.Hour, "a")
	h.fake.vanish("a")
	if _, err := driftDue(t, h, s); err != nil {
		t.Fatal(err)
	}
	if o, c := h.fake.called("OpenEphemeral"), h.fake.called("CloseEphemeral"); o != 1 || c != 1 {
		t.Errorf("opened %d and closed %d ephemeral resources, want 1 and 1", o, c)
	}
}
