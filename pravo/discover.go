package pravo

import (
	"context"
	"fmt"
	"strings"

	"github.com/pravo-systems/pravo/internal/engine"
)

// DiscoverOptions say which discoveries run.
type DiscoverOptions struct {
	// Names are the discoveries to run. Without names, Discover runs the ones
	// that have not run yet and the ones whose scope changed since they ran.
	Names []string
	// All runs every discovery again. It does not go with Names.
	All bool
	// Error is called with the error of a discovery that failed, as soon as it
	// does. The others still run.
	Error func(name string, err error)
}

// DiscoverResult is what Discover did.
type DiscoverResult struct {
	// Chosen are the discoveries that ran.
	Chosen []*Discovery
	// Failed is how many of them failed.
	Failed int
	// Report sets the runs of all the discoveries against the records of the state.
	Report *DiscoveryReport
	// Problems are the runs in the state that could not be read.
	Problems []error
}

// Discover runs the discoveries of the specification, which list the objects
// that exist in a scope, and keeps what they list in the state. A discovery
// lists and manages nothing: no plan or apply reads it. The error is that of
// choosing the discoveries (with the code of an invalid specification) or of
// reading the state.
func (w *Workspace) Discover(ctx context.Context, o DiscoverOptions) (*DiscoverResult, error) {
	if err := w.needPlanning(); err != nil {
		return nil, err
	}
	if o.All && len(o.Names) > 0 {
		return nil, fmt.Errorf("All runs every discovery; name some or set All, not both")
	}
	chosen, err := w.chooseDiscoveries(o)
	if err != nil {
		return nil, engine.WithCode(engine.ExitInvalid, err)
	}
	res := &DiscoverResult{Chosen: chosen}
	eng := w.Engine(EngineOptions{})
	for _, d := range chosen {
		run, err := eng.Discover(ctx, d)
		if err == nil {
			err = w.state.PutDiscovery(run)
		}
		if err != nil {
			res.Failed++
			if o.Error != nil {
				o.Error(d.Name, err)
			}
		}
	}
	res.Report, res.Problems, err = w.DiscoveryReport()
	if err != nil {
		return nil, err
	}
	return res, nil
}

// DiscoveryReport reads the runs and the acknowledgements of the state and sets
// them against its records. problems are the runs that could not be read.
func (w *Workspace) DiscoveryReport() (rep *DiscoveryReport, problems []error, err error) {
	if w.state == nil {
		return nil, nil, fmt.Errorf("pravo: discoveries live in the state: open the workspace with Config.State set")
	}
	runs, bad, err := w.state.Discoveries()
	if err != nil {
		return nil, nil, err
	}
	for _, p := range bad {
		problems = append(problems, p)
	}
	return engine.BuildDiscoveryReport(engine.DiscoveryInputs{Root: w.root, Runs: runs, Records: w.state.List()}), problems, nil
}

// chooseDiscoveries picks the discoveries to run: the ones named, every one, or
// the ones that have not run and the ones whose scope changed.
func (w *Workspace) chooseDiscoveries(o DiscoverOptions) ([]*Discovery, error) {
	var chosen []*Discovery
	if len(o.Names) > 0 {
		for _, name := range o.Names {
			d := w.root.DiscoveryByName(name)
			if d == nil {
				return nil, UnknownDiscovery(w.root, name)
			}
			chosen = append(chosen, d)
		}
		return chosen, nil
	}
	runs, _, err := w.state.Discoveries()
	if err != nil {
		return nil, err
	}
	for _, d := range w.root.Discoveries {
		run := runs[d.Name]
		if o.All || d.ScopeErr != nil || run == nil || run.Digest != d.Digest {
			chosen = append(chosen, d)
		}
	}
	return chosen, nil
}

// UnknownDiscovery is the error for a name that is not a discovery of the root.
func UnknownDiscovery(root *Root, name string) error {
	known := root.DiscoveryNames()
	if len(known) == 0 {
		return fmt.Errorf("there is no discovery %q: the specification has none", name)
	}
	return fmt.Errorf("there is no discovery %q in the specification; it has %s", name, strings.Join(known, ", "))
}
