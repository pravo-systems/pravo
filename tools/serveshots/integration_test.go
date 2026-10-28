package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScenariosWithPravo runs the preparation steps against a real pravo
// binary: SERVESHOTS_BIN=path/to/pravo go test ./tools/serveshots. It needs the
// providers random, local and null, from the cache or the network, and the go
// tool to build the fake provider of the team (or SERVESHOTS_FAKEPROVIDER, a
// built one). It checks that each scenario applies with the number of
// resources it declares, that the addresses the shots select exist in the
// state, that the changed situation drifts and plans, and that the team has
// the plans and the recorded runs that its shots name.
func TestScenariosWithPravo(t *testing.T) {
	bin := os.Getenv("SERVESHOTS_BIN")
	if bin == "" {
		t.Skip("set SERVESHOTS_BIN to a pravo binary to run the scenarios")
	}
	bin, err := filepath.Abs(bin)
	if err != nil {
		t.Fatal(err)
	}
	r := &pravoRunner{Bin: bin}
	ctx := context.Background()
	for _, sc := range scenarios {
		if sc.hasTeam() {
			continue
		}
		t.Run(sc.Name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "ws")
			if _, err := prepareClean(ctx, r, sc, dir); err != nil {
				t.Fatal(err)
			}
			res, err := r.mustRun(ctx, dir, []int{0}, "state", "list")
			if err != nil {
				t.Fatal(err)
			}
			checkSelections(t, sc, strings.Fields(res.Out))
		})
	}
	t.Run("platform stored", func(t *testing.T) {
		sc, _ := findScenario("platform")
		dir := filepath.Join(t.TempDir(), "ws")
		w, err := prepareStored(ctx, r, sc, dir)
		if err != nil {
			t.Fatal(err)
		}
		res, err := r.mustRun(ctx, dir, []int{0}, "plan", "list", "--all")
		if err != nil {
			t.Fatal(err)
		}
		for actor, id := range w.Plans {
			if !strings.Contains(res.Out, id) {
				t.Errorf("pravo plan list --all does not list the plan of %s, %s:\n%s", actor, id, lastLines(res.Out, 8))
			}
		}
		// The plan of ana is the one that the shots lay over the graph.
		res, err = r.mustRun(ctx, dir, []int{0}, "show", w.Plans["ana"])
		if err != nil {
			t.Fatal(err)
		}
		if want := "# api.services.orders.config (local_file) must be replaced"; !strings.Contains(res.Out, want) {
			t.Errorf("the plan of ana lacks %q:\n%s", want, lastLines(res.Out, 12))
		}
	})
	t.Run("platform changed", func(t *testing.T) {
		sc, _ := findScenario("platform")
		dir := filepath.Join(t.TempDir(), "ws")
		w, err := prepareChanged(ctx, r, sc, dir)
		if err != nil {
			t.Fatal(err)
		}
		res, err := r.mustRun(ctx, dir, []int{0}, "show", w.Plan)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			"# api.services.orders.config (local_file) must be replaced",
			"# edge.waf.rules (local_file) will be created",
			"# edge.balancer.reload (null_resource) will be destroyed",
		} {
			if !strings.Contains(res.Out, want) {
				t.Errorf("the plan lacks %q:\n%s", want, lastLines(res.Out, 12))
			}
		}
	})
	t.Run("team", func(t *testing.T) {
		sc, _ := findScenario("team")
		s, err := startStore()
		if err != nil {
			t.Fatal(err)
		}
		defer s.stop()
		provider := os.Getenv("SERVESHOTS_FAKEPROVIDER")
		if provider == "" {
			if provider, err = buildFakeProvider(ctx, filepath.Join(t.TempDir(), "bin")); err != nil {
				t.Fatal(err)
			}
		}
		dir := filepath.Join(t.TempDir(), "team")
		w, err := prepareTeam(ctx, r, sc, dir, teamDeps{Store: s, FakeProvider: provider})
		if err != nil {
			t.Fatal(err)
		}
		res, err := r.mustRunEnv(ctx, dir, w.Env, []int{0}, "state", "list")
		if err != nil {
			t.Fatal(err)
		}
		checkSelections(t, sc, strings.Fields(res.Out))
		res, err = r.mustRunEnv(ctx, dir, w.Env, []int{0}, "plan", "list", "--all")
		if err != nil {
			t.Fatal(err)
		}
		for label, id := range w.Plans {
			if !strings.Contains(res.Out, id) {
				t.Errorf("pravo plan list --all does not list the plan %s, %s:\n%s", label, id, lastLines(res.Out, 8))
			}
		}
		// Every span that a shot names is in the run that the shot opens.
		for _, sh := range allShots() {
			if sh.Scenario != sc.Name {
				continue
			}
			if _, err := resolveLabels(sh.URLFragment(), w); err != nil {
				t.Errorf("%s: %v", sh.Name, err)
			}
		}
	})
}

// checkSelections checks that every address a shot of the scenario selects or
// names is a resource or a group in the state, whose addresses are addrs.
func checkSelections(t *testing.T, sc scenario, addrs []string) {
	t.Helper()
	if len(addrs) != sc.Resources {
		t.Errorf("%d resources in the state, the scenario says %d", len(addrs), sc.Resources)
	}
	has := map[string]bool{}
	prefix := map[string]bool{}
	for _, a := range addrs {
		has[a] = true
		parts := strings.Split(a, ".")
		for i := 1; i < len(parts); i++ {
			prefix[strings.Join(parts[:i], ".")] = true
		}
	}
	for _, s := range allShots() {
		if s.Scenario != sc.Name {
			continue
		}
		// A node that only the changed situation adds or removes is not in
		// the state of a clean workspace, and no shot selects one. A shot may
		// select a group, the prefix of an address.
		if sel := s.Fragment.Sel; sel != "" && !has[sel] && !prefix[sel] {
			t.Errorf("%s selects %s, which is neither a resource nor a group in the state", s.Name, sel)
		}
		for _, list := range [][]string{s.Fragment.Collapse, s.Fragment.Expand} {
			for _, p := range list {
				// c:GROUP|MODE|TYPE names the cluster of a type in a group.
				if rest, ok := strings.CutPrefix(p, "c:"); ok {
					p, _, _ = strings.Cut(rest, "|")
				}
				if !prefix[p] && !has[p] {
					t.Errorf("%s names the group %s, which is not in the state", s.Name, p)
				}
			}
		}
	}
}
