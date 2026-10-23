package engine

import (
	"testing"

	"github.com/pravo-systems/pravo/internal/config"
)

// The M of "planned N of M" is the resources and data sources: what the plan
// plans or skips. The tasks of a graph that are not planned against a record are
// not in it, or the numbers of the line would not add up.
func TestPlannedOfCountsResourcesAndDataSourcesOnly(t *testing.T) {
	var rs []*config.Resource
	for _, mode := range []string{"managed", "managed", "data", "builtin", "builtin", "ephemeral", "call"} {
		rs = append(rs, &config.Resource{Mode: mode})
	}
	e := &Engine{Root: &config.Root{Resources: rs}}
	if got := e.countPlannable(); got != 3 {
		t.Errorf("countPlannable = %d, want 3", got)
	}
	e.resetRun(true)
	if e.stats.Total != 3 {
		t.Errorf("Total = %d, want 3", e.stats.Total)
	}
}
