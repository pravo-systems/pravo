package engine

import "testing"

// FuzzReadPlanBytes feeds the plan file reader what a file of any origin may
// hold, since "pravo show", "apply" and the studio take files from outside. It
// must return an error or a plan, and never panic.
func FuzzReadPlanBytes(f *testing.F) {
	f.Add([]byte(`{"format":1,"changes":[]}`))
	f.Add([]byte(`{"format":1,"providers":{},"changes":[{"addr":"a.b","action":"create"}]}`))
	f.Add([]byte(`{"v":1,"alg":"x","ct":"AA"}`))
	f.Add([]byte(`{"format":99999999999999999999}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`[`))
	f.Fuzz(func(t *testing.T, data []byte) {
		pf, err := ReadPlanBytes("fuzz", data, nil)
		if err == nil && pf == nil {
			t.Fatal("no plan and no error")
		}
	})
}
