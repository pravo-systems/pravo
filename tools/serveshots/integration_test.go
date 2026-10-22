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
// providers random, local and null, from the cache or the network. It checks
// that each scenario applies with the number of resources it declares, that the
// addresses the shots select exist in the state, and that the changed
// situation drifts and plans.
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
		t.Run(sc.Name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "ws")
			if _, err := prepareClean(ctx, r, sc, dir); err != nil {
				t.Fatal(err)
			}
			res, err := r.mustRun(ctx, dir, []int{0}, "state", "list")
			if err != nil {
				t.Fatal(err)
			}
			addrs := strings.Fields(res.Out)
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
				// A node that only the changed situation adds or removes is not
				// in the state of a clean workspace, and no shot selects one. A
				// shot may select a module, the prefix of an address.
				if sel := s.Fragment.Sel; sel != "" && !has[sel] && !prefix[sel] {
					t.Errorf("%s selects %s, which is neither a resource nor a module in the state", s.Name, sel)
				}
				for _, list := range [][]string{s.Fragment.Collapse, s.Fragment.Expand} {
					for _, p := range list {
						// c:MODULE|MODE|TYPE names the cluster of a type in a module.
						if rest, ok := strings.CutPrefix(p, "c:"); ok {
							p, _, _ = strings.Cut(rest, "|")
						}
						if !prefix[p] && !has[p] {
							t.Errorf("%s names the module %s, which is not in the state", s.Name, p)
						}
					}
				}
			}
		})
	}
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
}
