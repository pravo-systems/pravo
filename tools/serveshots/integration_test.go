package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
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
	// The scenarios with a catalog list it through the fake provider, and the
	// team situations need it too.
	provider := os.Getenv("SERVESHOTS_FAKEPROVIDER")
	if provider == "" {
		if provider, err = buildFakeProvider(ctx, filepath.Join(t.TempDir(), "bin")); err != nil {
			t.Fatal(err)
		}
	}
	for _, sc := range scenarios {
		if sc.hasTeam() || sc.Resources == 0 {
			continue
		}
		t.Run(sc.Name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "ws")
			if _, err := prepareClean(ctx, r, sc, dir, provider); err != nil {
				t.Fatal(err)
			}
			res, err := r.mustRun(ctx, dir, []int{0}, "state", "list")
			if err != nil {
				t.Fatal(err)
			}
			// A shot of the changed situation may select a resource that only
			// the plan has: one that the edits add, or one that a move names.
			var planned []string
			if sc.hasChanges() {
				w, err := prepareChanged(ctx, r, sc, filepath.Join(t.TempDir(), "changed"), provider)
				if err != nil {
					t.Fatal(err)
				}
				shown, err := r.mustRun(ctx, w.Dir, []int{0}, "show", w.Plan)
				if err != nil {
					t.Fatal(err)
				}
				planned = plannedAddrs(shown.Out)
			}
			checkSelections(t, sc, strings.Fields(res.Out), planned)
		})
	}
	t.Run("c02 stored", func(t *testing.T) {
		sc, _ := findScenario("c02")
		dir := filepath.Join(t.TempDir(), "ws")
		w, err := prepareStored(ctx, r, sc, dir, provider)
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
	})
	t.Run("platform stored", func(t *testing.T) {
		sc, _ := findScenario("platform")
		dir := filepath.Join(t.TempDir(), "ws")
		w, err := prepareStored(ctx, r, sc, dir, provider)
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
		w, err := prepareChanged(ctx, r, sc, dir, provider)
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
	s, err := startStore()
	if err != nil {
		t.Fatal(err)
	}
	defer s.stop()
	for _, sc := range scenarios {
		if !sc.hasTeam() {
			continue
		}
		t.Run(sc.Name+" team", func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), sc.Name)
			w, err := prepareTeam(ctx, r, sc, dir, teamDeps{Store: s, FakeProvider: provider})
			if err != nil {
				t.Fatal(err)
			}
			res, err := r.mustRunEnv(ctx, dir, w.Env, []int{0}, "state", "list")
			if err != nil {
				t.Fatal(err)
			}
			checkSelections(t, sc, strings.Fields(res.Out), nil)
			res, err = r.mustRunEnv(ctx, dir, w.Env, []int{0}, "plan", "list", "--all")
			if err != nil {
				t.Fatal(err)
			}
			for label, id := range w.Plans {
				if !strings.Contains(res.Out, id) {
					t.Errorf("pravo plan list --all does not list the plan %s, %s:\n%s", label, id, lastLines(res.Out, 8))
				}
			}
			for _, st := range sc.Team.Steps {
				if st.Kind == "save" {
					if _, err := os.Stat(filepath.Join(dir, st.File)); err != nil {
						t.Errorf("the plan file %s: %v", st.File, err)
					}
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
}

var (
	plannedRe = regexp.MustCompile(`(?m)^  # (\S+) (?:\(|will be)`)
	movedRe   = regexp.MustCompile(`(?m)^  # \S+ will be moved to (\S+)`)
)

// plannedAddrs reads the addresses that a printed plan changes, and the new
// addresses of its moves.
func plannedAddrs(out string) []string {
	var addrs []string
	for _, m := range plannedRe.FindAllStringSubmatch(out, -1) {
		addrs = append(addrs, m[1])
	}
	for _, m := range movedRe.FindAllStringSubmatch(out, -1) {
		addrs = append(addrs, m[1])
	}
	return addrs
}

// checkSelections checks that every address a shot of the scenario selects or
// names is a resource or a group in the state, whose addresses are addrs, or
// one of the planned addresses of the changed situation.
func checkSelections(t *testing.T, sc scenario, addrs, planned []string) {
	t.Helper()
	if len(addrs) != sc.Resources {
		t.Errorf("%d resources in the state, the scenario says %d", len(addrs), sc.Resources)
	}
	has := map[string]bool{}
	prefix := map[string]bool{}
	for _, a := range append(append([]string(nil), addrs...), planned...) {
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
