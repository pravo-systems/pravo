package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"cuelang.org/go/cue"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/plugin"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
)

// PlanFileFormat is the saved plan format version that this build writes and
// reads.
const PlanFileFormat = 1

// PlanFile is the on-disk form of a plan. Provider values are stored as
// msgpack so that unknown values survive; the types come from the provider
// schemas, which are pinned by the recorded provider versions.
type PlanFile struct {
	Format  int       `json:"format"`
	Created time.Time `json:"created"`
	Command string    `json:"command"`
	// Unsealed is set by a PlanStore on a plan that was read from a sealed state and
	// was stored before the state was sealed.
	Unsealed bool `json:"-"`
	// Digest is the SHA-256 in hex of the bytes that this plan was parsed from, set by
	// ReadPlanFile and by a PlanStore. An apply records it, so it is the plan that was run.
	Digest string `json:"-"`
	// Lineage of the state graph the plan was made against.
	Lineage string `json:"lineage"`
	// ConfigFingerprint digests every CUE file of the root.
	ConfigFingerprint string `json:"config_fingerprint"`
	// ConfigFiles maps the name of each file that the fingerprint digests to a short
	// digest of it, so that an apply that finds the configuration changed can say
	// which files differ.
	ConfigFiles map[string]string `json:"config_files,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	SysTags     bool              `json:"sys_tags,omitempty"`
	// Providers maps provider local names to the versions the plan used.
	Providers map[string]string `json:"providers"`
	Destroy   bool              `json:"destroy,omitempty"`
	// NoRefresh records that the plan was made with -refresh=false.
	NoRefresh bool          `json:"no_refresh,omitempty"`
	Stats     Stats         `json:"stats"`
	Changes   []savedChange `json:"changes"`
	// Relabels lists the records whose provider a provider $from renames.
	Relabels []Relabel `json:"provider_moves,omitempty"`
	// Inputs maps the address of every record that the changes read to its
	// revision when the plan was made. The records themselves are not in the file.
	Inputs map[string]int `json:"inputs,omitempty"`
	// InputGenerations maps those of the records to the generation they were in.
	// A record that has none is not listed.
	InputGenerations map[string]string `json:"input_generations,omitempty"`
	// EvalOutputs names the outputs that the apply evaluates.
	EvalOutputs []string `json:"eval_outputs,omitempty"`
	// DropOutputs names the recorded outputs that the apply removes.
	DropOutputs []string `json:"drop_outputs,omitempty"`
	// OldOutputs holds the recorded value of each output that the apply evaluates
	// or removes.
	OldOutputs map[string]json.RawMessage `json:"old_outputs,omitempty"`
	// Outputs holds the outputs that were known after planning.
	Outputs map[string]json.RawMessage `json:"outputs,omitempty"`
	// UnknownOutputs lists outputs that are known only after apply.
	UnknownOutputs []string `json:"unknown_outputs,omitempty"`
	// SensitiveOutputs lists outputs that are hidden in listings.
	SensitiveOutputs []string `json:"sensitive_outputs,omitempty"`
	// Actor, Reason and GitRevision attribute the plan.
	Actor       string `json:"actor,omitempty"`
	Reason      string `json:"reason,omitempty"`
	GitRevision string `json:"git_revision,omitempty"`
	// Reservation, when the plan reserved its nodes.
	Reservation *ReservationView `json:"reservation,omitempty"`
	// ApplyTxn is the transaction of the last apply made from the file.
	ApplyTxn string `json:"apply_txn,omitempty"`
}

type savedDiag struct {
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
	Detail   string `json:"detail,omitempty"`
}

type savedChange struct {
	Reasons     []string `json:"reasons,omitempty"`
	Addr        string   `json:"addr"`
	Mode        string   `json:"mode"`
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	Action      Action   `json:"action"`
	ProviderKey string   `json:"provider_key"`
	ConfigHash  string   `json:"config_hash,omitempty"`

	Config        []byte `json:"config,omitempty"`
	Prior         []byte `json:"prior,omitempty"`
	Planned       []byte `json:"planned,omitempty"`
	PlannedCreate []byte `json:"planned_create,omitempty"`

	PriorPrivate    []byte   `json:"prior_private,omitempty"`
	PlannedPrivate  []byte   `json:"planned_private,omitempty"`
	CreatePrivate   []byte   `json:"create_private,omitempty"`
	RequiresReplace []string `json:"requires_replace,omitempty"`
	Deps            []string `json:"dependencies,omitempty"`

	CreateBeforeDestroy bool `json:"create_before_destroy,omitempty"`
	PreventDestroy      bool `json:"prevent_destroy,omitempty"`
	Deferred            bool `json:"deferred,omitempty"`
	Skipped             bool `json:"skipped,omitempty"`
	Refreshed           bool `json:"refreshed,omitempty"`
	Kept                bool `json:"kept,omitempty"`
	Drifted             bool `json:"drifted,omitempty"`
	Forced              bool `json:"forced,omitempty"`
	Fresh               bool `json:"fresh,omitempty"`

	// PriorRevision is the state node's revision when the plan was made (0
	// when the node did not exist), and PriorGeneration its generation (empty
	// when the node did not exist or has none).
	PriorRevision   int         `json:"prior_revision"`
	PriorGeneration string      `json:"prior_generation,omitempty"`
	Warnings        []savedDiag `json:"warnings,omitempty"`
	// Applied is set once the change was applied from this file.
	Applied    bool   `json:"applied,omitempty"`
	AppliedTxn string `json:"applied_txn,omitempty"`
	Imported   bool   `json:"imported,omitempty"`
	ImportID   string `json:"import_id,omitempty"`
	// ImportIdentity says that ImportID is an identity, written as pairs.
	ImportIdentity bool   `json:"import_identity,omitempty"`
	MovedFrom      string `json:"moved_from,omitempty"`
}

func packValue(v tftypes.Value) ([]byte, error) {
	if v.Type() == nil {
		return nil, nil
	}
	return v.MarshalMsgPack(v.Type())
}

func unpackValue(data []byte, t tftypes.Type) (tftypes.Value, error) {
	if len(data) == 0 {
		return tftypes.Value{}, nil
	}
	return tftypes.ValueFromMsgPack(data, t)
}

// commandLine is the command line as it was typed. Options.Command is what a
// sealed state shows of it, and Options.Args, when the caller set them, are the
// arguments it was made from. The plan file keeps the original: it is sealed
// with the plan, or written to a file of the person who asked for it.
func (e *Engine) commandLine() string {
	if len(e.Opts.Args) > 0 {
		return "pravo " + strings.Join(e.Opts.Args, " ")
	}
	return e.Opts.Command
}

// buildPlanFile makes the plan file of a plan: what SavePlan writes and what a
// stored plan holds.
func (e *Engine) buildPlanFile(plan *Plan, tags []string, sysTags bool) (*PlanFile, error) {
	pf := &PlanFile{
		Format: PlanFileFormat, Created: time.Now().UTC(), Command: e.commandLine(), Lineage: e.State.Lineage(),
		Tags: tags, SysTags: sysTags, Providers: map[string]string{}, Destroy: plan.Destroy, NoRefresh: plan.NoRefresh, Stats: plan.Stats,
		Outputs: map[string]json.RawMessage{}, Actor: e.Opts.Actor, Reason: e.Opts.Reason, GitRevision: e.Opts.GitRevision,
		Reservation: plan.Reservation, Relabels: plan.Relabels,
	}
	fp, err := e.Root.Fingerprint()
	if err != nil {
		return nil, err
	}
	pf.ConfigFingerprint = fp
	if files, err := e.Root.FileHashes(); err == nil {
		pf.ConfigFiles = files
	}
	for local, rp := range e.Providers.Resolved {
		pf.Providers[local] = rp.Version
	}
	pf.Inputs, pf.InputGenerations, pf.EvalOutputs, pf.DropOutputs = plan.Inputs, plan.InputGenerations, plan.EvalOutputs, plan.DropOutputs
	pf.OldOutputs = plan.OldOutputs
	for _, ch := range plan.Changes {
		if ch.Action == ActionOpen || ch.Action == ActionCall {
			// Ephemeral results and function results are never written
			// anywhere; they are recomputed when the plan is applied.
			continue
		}
		if ch.leftAlone() {
			// A node the plan did nothing with has no entry: the apply does not
			// walk it, and the records of the nodes it reads are listed as inputs.
			continue
		}
		sc := savedChange{
			Addr: ch.Addr, Mode: ch.Mode, Type: ch.Type, Name: ch.Name, Action: ch.Action, ProviderKey: ch.ProviderKey,
			ConfigHash: ch.ConfigHash, PriorPrivate: ch.PriorPrivate, PlannedPrivate: ch.PlannedPrivate, CreatePrivate: ch.CreatePrivate,
			Deps: ch.Deps, CreateBeforeDestroy: ch.CreateBeforeDestroy, PreventDestroy: ch.PreventDestroy,
			Deferred: ch.Deferred, Skipped: ch.Skipped, Refreshed: ch.Refreshed, Kept: ch.Kept, Drifted: ch.Drifted, Forced: ch.Forced, Fresh: ch.Fresh,
			Imported: ch.Imported, ImportID: ch.ImportID, ImportIdentity: ch.ImportIdentity, MovedFrom: ch.MovedFrom, Reasons: ch.Reasons,
		}
		sc.PriorRevision, sc.PriorGeneration = ch.PriorRevision, ch.PriorGeneration
		if ch.Action == ActionMove {
			// The version is that of the record that moves, which is at its old address.
			pf.Changes = append(pf.Changes, sc)
			continue
		}
		cfg := ch.Config
		if ch.Schema != nil {
			cfg = withoutWriteOnly(ch.Schema.Block, cfg)
		}
		if sc.Config, err = packValue(cfg); err != nil {
			return nil, fmt.Errorf("%s: %w", ch.Addr, err)
		}
		if sc.Prior, err = packValue(ch.Prior); err != nil {
			return nil, fmt.Errorf("%s: %w", ch.Addr, err)
		}
		if sc.Planned, err = packValue(ch.Planned); err != nil {
			return nil, fmt.Errorf("%s: %w", ch.Addr, err)
		}
		if sc.PlannedCreate, err = packValue(ch.PlannedCreate); err != nil {
			return nil, fmt.Errorf("%s: %w", ch.Addr, err)
		}
		for _, p := range ch.RequiresReplace {
			if steps := p.Steps(); len(steps) > 0 {
				if n, ok := steps[0].(tftypes.AttributeName); ok {
					sc.RequiresReplace = append(sc.RequiresReplace, string(n))
				}
			}
		}
		for _, w := range ch.Warnings {
			sc.Warnings = append(sc.Warnings, savedDiag{Severity: w.Severity, Summary: w.Summary, Detail: w.Detail})
		}
		pf.Changes = append(pf.Changes, sc)
	}
	names := make([]string, 0, len(plan.Outputs))
	for n := range plan.Outputs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if plan.SensitiveOutputs[n] {
			pf.SensitiveOutputs = append(pf.SensitiveOutputs, n)
		}
		v := plan.Outputs[n]
		if !convert.IsConcrete(v) {
			pf.UnknownOutputs = append(pf.UnknownOutputs, n)
			continue
		}
		b, err := outputJSON(v)
		if err != nil {
			return nil, fmt.Errorf("output %s: %w", n, err)
		}
		pf.Outputs[n] = b
	}
	return pf, nil
}

// SavePlan writes the plan to path.
func (e *Engine) SavePlan(path string, plan *Plan, tags []string, sysTags bool) error {
	pf, err := e.buildPlanFile(plan, tags, sysTags)
	if err != nil {
		return err
	}
	data, err := indentJSON(pf)
	if err != nil {
		return err
	}
	return writeFile0600(path, data)
}

// writeFile0600 writes a plan to a file that only its owner can read: it holds
// the values of what it changes.
func writeFile0600(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

// ReadPlanFile parses a plan file without checking it against anything.
func ReadPlanFile(path string) (*PlanFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pf, err := parsePlanFile(path, data)
	if err != nil {
		return nil, err
	}
	pf.Digest = PlanDigest(data)
	return pf, nil
}

// parsePlanFile parses the bytes of a plan file. name is what the plan is called
// in messages: a path, or the id of a stored plan.
func parsePlanFile(name string, data []byte) (*PlanFile, error) {
	var pf PlanFile
	if err := json.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("%s is not a pravo plan file: %w", name, err)
	}
	if pf.Format == 0 || pf.Changes == nil && pf.Providers == nil {
		return nil, fmt.Errorf("%s is not a pravo plan file", name)
	}
	if pf.Format > PlanFileFormat {
		return nil, fmt.Errorf("%s was written by a newer pravo (plan format %d)", name, pf.Format)
	}
	return &pf, nil
}

// ErrStalePlan is returned when a saved plan no longer matches the
// configuration, providers or state it was made against.
type ErrStalePlan struct {
	Reasons []string `json:"reasons"`
}

func (e *ErrStalePlan) Error() string {
	return "saved plan is stale; run \"pravo plan\" again:\n  - " + strings.Join(e.Reasons, "\n  - ")
}

// newPlan makes the plan that a file stands for, with the changes it holds.
func (e *Engine) newPlan(pf *PlanFile) (*Plan, error) {
	plan := &Plan{Outputs: map[string]cue.Value{}, Destroy: pf.Destroy, NoRefresh: pf.NoRefresh, Stats: pf.Stats, SavedOutputs: pf.Outputs, UnknownOutputs: pf.UnknownOutputs, SensitiveOutputs: stringSet(pf.SensitiveOutputs), Actor: pf.Actor, Reservation: pf.Reservation, Relabels: pf.Relabels,
		Inputs: pf.Inputs, InputGenerations: pf.InputGenerations, EvalOutputs: pf.EvalOutputs, DropOutputs: pf.DropOutputs, OldOutputs: pf.OldOutputs}
	for _, sc := range pf.Changes {
		ch, err := e.restoreChange(sc)
		if err != nil {
			return nil, err
		}
		plan.Changes = append(plan.Changes, ch)
	}
	plan.Write = plan.writeAddrs()
	return plan, nil
}

// LoadPlanUnchecked turns a plan file back into a Plan without checking
// whether it is still applicable (for "pravo show").
func (e *Engine) LoadPlanUnchecked(pf *PlanFile) (*Plan, error) {
	return e.newPlan(pf)
}

// LoadPlan turns a plan file back into a Plan against the engine's schemas
// and verifies that configuration, providers and state are unchanged.
func (e *Engine) LoadPlan(pf *PlanFile) (*Plan, error) {
	return e.LoadPlanFor(pf, nil)
}

// LoadPlanFor is LoadPlan for an apply of the changes named in only: a change
// outside the selection that was written since the plan was made does not make
// the plan stale. An empty selection is the whole plan.
//
// The plan is stale when the configuration, the state lineage or a provider
// differs, when a change that the apply writes finds its node at another
// revision, or when a record that the changes read has another revision. A node
// that the plan left alone and that no change reads is not looked at.
func (e *Engine) LoadPlanFor(pf *PlanFile, only []string) (*Plan, error) {
	var reasons []string
	if reason := ConfigChange(e.Root, pf); reason != "" {
		reasons = append(reasons, reason)
	}
	if pf.Lineage != e.State.Lineage() {
		reasons = append(reasons, "the plan was made against a different state (lineage mismatch)")
	}
	for local, ver := range pf.Providers {
		if rp, ok := e.Providers.Resolved[local]; !ok || rp.Version != ver {
			reasons = append(reasons, fmt.Sprintf("provider %s is now a different version than %s used by the plan", local, ver))
		}
	}
	plan, err := e.newPlan(pf)
	if err != nil {
		return nil, err
	}
	inputs, generations := pf.Inputs, pf.InputGenerations
	selected := stringSet(only)
	for _, sc := range pf.Changes {
		if sc.Action == ActionMove && !sc.Applied {
			// The record to move is at the old address as the plan saw it, or it was moved already.
			if reason := e.moveReason(sc); reason != "" {
				reasons = append(reasons, reason)
			}
			continue
		}
		if sc.Applied || sc.Skipped || sc.Kept || sc.Action == ActionMove || (selected != nil && !selected[sc.Addr]) {
			continue
		}
		if reason := e.versionReason(sc.Addr, sc.PriorRevision, sc.PriorGeneration, false); reason != "" {
			reasons = append(reasons, reason)
		}
	}
	read := make([]string, 0, len(inputs))
	for addr := range inputs {
		read = append(read, addr)
	}
	sort.Strings(read)
	for _, addr := range read {
		if reason := e.versionReason(addr, inputs[addr], generations[addr], true); reason != "" {
			reasons = append(reasons, reason)
		}
	}
	if len(reasons) > 0 {
		return nil, &ErrStalePlan{Reasons: reasons}
	}
	return plan, nil
}

func (e *Engine) restoreChange(sc savedChange) (*Change, error) {
	ch := &Change{
		Addr: sc.Addr, Mode: sc.Mode, Type: sc.Type, Name: sc.Name, Action: sc.Action, ProviderKey: sc.ProviderKey,
		ConfigHash: sc.ConfigHash, PriorPrivate: sc.PriorPrivate, PlannedPrivate: sc.PlannedPrivate, CreatePrivate: sc.CreatePrivate,
		Deps: sc.Deps, CreateBeforeDestroy: sc.CreateBeforeDestroy, PreventDestroy: sc.PreventDestroy,
		Deferred: sc.Deferred, Skipped: sc.Skipped, Refreshed: sc.Refreshed, Kept: sc.Kept, Drifted: sc.Drifted, Forced: sc.Forced, Fresh: sc.Fresh,
		Applied: sc.Applied, AppliedTxn: sc.AppliedTxn, Imported: sc.Imported, ImportID: sc.ImportID, ImportIdentity: sc.ImportIdentity, MovedFrom: sc.MovedFrom,
		Reasons: sc.Reasons, PriorRevision: sc.PriorRevision, PriorGeneration: sc.PriorGeneration,
	}
	if sc.Action == ActionMove {
		return ch, nil
	}
	ps, ok := e.Schemas[config.LocalName(sc.ProviderKey)]
	if !ok {
		return nil, fmt.Errorf("%s: provider %s is not available", sc.Addr, config.LocalName(sc.ProviderKey))
	}
	var s *schema.Schema
	if sc.Mode == "data" {
		s = ps.DataSource(sc.Type)
	} else {
		s = ps.Resource(sc.Type)
	}
	if s == nil {
		return nil, fmt.Errorf("%s: provider has no %s type %q", sc.Addr, sc.Mode, sc.Type)
	}
	ch.Schema = s
	t := s.Block.Type()
	var err error
	if ch.Config, err = unpackValue(sc.Config, t); err != nil {
		return nil, fmt.Errorf("%s: config: %w", sc.Addr, err)
	}
	if ch.Prior, err = unpackValue(sc.Prior, t); err != nil {
		return nil, fmt.Errorf("%s: prior state: %w", sc.Addr, err)
	}
	if ch.Planned, err = unpackValue(sc.Planned, t); err != nil {
		return nil, fmt.Errorf("%s: planned state: %w", sc.Addr, err)
	}
	if ch.PlannedCreate, err = unpackValue(sc.PlannedCreate, t); err != nil {
		return nil, fmt.Errorf("%s: planned replacement: %w", sc.Addr, err)
	}
	if ch.Prior.Type() == nil {
		ch.Prior = tftypes.NewValue(t, nil)
	}
	for _, n := range sc.RequiresReplace {
		ch.RequiresReplace = append(ch.RequiresReplace, tftypes.NewAttributePath().WithAttributeName(n))
	}
	for _, w := range sc.Warnings {
		ch.Warnings = append(ch.Warnings, plugin.Diagnostic{Severity: w.Severity, Summary: w.Summary, Detail: w.Detail})
	}
	return ch, nil
}

// consistent reports whether actual agrees with saved everywhere saved is
// known: a provider may resolve unknowns during apply, but must not change
// values it had already decided on.
func consistent(saved, actual tftypes.Value) bool {
	if !saved.IsKnown() {
		return true
	}
	if !actual.IsKnown() {
		return false
	}
	if saved.IsNull() || actual.IsNull() {
		return saved.IsNull() == actual.IsNull()
	}
	t := saved.Type()
	switch {
	case t.Is(tftypes.Object{}) || t.Is(tftypes.Map{}):
		var sm, am map[string]tftypes.Value
		if saved.As(&sm) != nil || actual.As(&am) != nil || len(sm) != len(am) {
			return false
		}
		for k, sv := range sm {
			av, ok := am[k]
			if !ok || !consistent(sv, av) {
				return false
			}
		}
		return true
	case t.Is(tftypes.List{}) || t.Is(tftypes.Tuple{}):
		var sl, al []tftypes.Value
		if saved.As(&sl) != nil || actual.As(&al) != nil || len(sl) != len(al) {
			return false
		}
		for i := range sl {
			if !consistent(sl[i], al[i]) {
				return false
			}
		}
		return true
	case t.Is(tftypes.Set{}):
		// Sets cannot be correlated element-wise when they hold unknowns;
		// require equality only when both are fully known.
		if saved.IsFullyKnown() {
			return saved.Equal(actual)
		}
		return true
	}
	return saved.Equal(actual)
}

// checkSaved compares the change computed during apply with the saved one: the
// change of the file being applied, or the change the plan of this run showed.
// The apply does at most what the plan said, and what it does is what the plan
// showed. The action is the same, and every value that the plan knew is the one
// the provider plans now (consistent). A data source that the apply read again
// returned what the plan read.
//
// A node that the plan would have changed and that has nothing to do by now,
// because a value it was waiting for came out as it already was, is fine when it
// already holds what the plan would have made of it. A node that does not is not:
// the plan was made from other values than the apply sees, and what the apply
// would leave is not what was approved.
func (e *Engine) checkSaved(ch *Change) error {
	e.mu.Lock()
	saved, ok := e.saved[ch.Addr]
	e.mu.Unlock()
	if !ok {
		return e.stale(ch.Addr, "not part of the saved plan", "it is not part of the plan")
	}
	idle := func(a Action) bool { return a == ActionNoop || a == ActionRead }
	if saved.Action != ch.Action && !idle(ch.Action) {
		return e.stale(ch.Addr,
			fmt.Sprintf("the saved plan would %s but the provider now plans to %s", saved.Action, ch.Action),
			fmt.Sprintf("the plan would %s it, the provider now plans to %s", saved.Action, ch.Action))
	}
	switch ch.Action {
	case ActionCreate, ActionUpdate:
		if !consistent(saved.Planned, ch.Planned) {
			return e.stale(ch.Addr, "the provider produced a different plan than the saved one", "the provider produced a different plan than before")
		}
	case ActionReplace:
		if !consistent(saved.PlannedCreate, ch.PlannedCreate) {
			return e.stale(ch.Addr, "the provider produced a different replacement plan than the saved one", "the provider produced a different replacement plan than before")
		}
	}
	if saved.Action == ActionRead && ch.Action == ActionRead && !consistent(saved.Planned, ch.Planned) {
		return e.stale(ch.Addr, "the data source returned something other than what the saved plan read", "the data source returned something other than what the plan read")
	}
	if idle(ch.Action) {
		// The change that the plan promised is made when the node holds what the plan
		// would have made of it, wherever the unknown values came out.
		var made tftypes.Value
		switch saved.Action {
		case ActionCreate, ActionUpdate:
			made = saved.Planned
		case ActionReplace:
			made = saved.PlannedCreate
		}
		if made.Type() != nil && !consistent(made, ch.Prior) {
			return e.stale(ch.Addr,
				fmt.Sprintf("the saved plan would %s but the provider now plans to %s, and the record is not what the plan would make", saved.Action, ch.Action),
				fmt.Sprintf("the plan would %s it, the provider now plans to %s, and the record is not what the plan would make", saved.Action, ch.Action))
		}
	}
	return nil
}

// stale is the error of an apply that stops because a node would not be what the
// plan said. A saved plan and the plan that this run made are stale alike: exit
// code 3.
func (e *Engine) stale(addr, ofSaved, ofRun string) error {
	if e.approved {
		return WithCode(ExitStale, fmt.Errorf("%s: the plan changed while it waited: %s; run \"pravo plan\" again", addr, ofRun))
	}
	return WithCode(ExitStale, fmt.Errorf("%s: %s; run \"pravo plan\" again", addr, ofSaved))
}

// staleReason is the reason a plan is stale when its record at addr is now in
// the version have and the plan expected want: "" when they are the same. A record
// that is at the revision the plan expected but in another generation was removed
// and created again.
func staleReason(addr string, have, want state.Version, reads bool) string {
	var reason string
	switch {
	case have == want:
		return ""
	case have.Revision == want.Revision:
		reason = fmt.Sprintf("%s was removed and created again in state (revision %d, in another generation than the plan expected)", addr, have.Revision)
	default:
		reason = fmt.Sprintf("%s changed in state (revision %d, plan expected %d)", addr, have.Revision, want.Revision)
	}
	if reads {
		reason += "; the plan reads it"
	}
	return reason
}

// versionReason compares the record that the store holds at addr with the version
// a saved plan expected, and returns the reason the plan is stale, or "".
func (e *Engine) versionReason(addr string, rev int, gen string, reads bool) string {
	haveRev, haveGen := e.versionOf(addr)
	return staleReason(addr, state.Version{Revision: haveRev, Generation: haveGen}, state.Version{Revision: rev, Generation: gen}, reads)
}

// checkRevisions compares the records in the backend with the plan once the
// locks are held: every node the plan writes is at the revision and in the
// generation the plan saw, and so is every record in inputs, the ones the walk
// reads that the plan lists. The store was read when the command started, so a
// write by another process since then is not in it; the backend is. A difference
// stops the apply before anything is touched, whether the plan is a file or the
// one this run printed and waited on. The records the plan reads are not locked: a
// write to one of them between this check and the read in the walk is not seen.
func (e *Engine) checkRevisions(plan *Plan, inputs map[string]state.Version) error {
	want := map[string]state.Version{}
	for _, ch := range plan.Changes {
		if ch.writes() {
			want[ch.Addr] = state.Version{Revision: ch.PriorRevision, Generation: ch.PriorGeneration}
		}
	}
	reads := map[string]bool{}
	for addr, v := range inputs {
		if _, ok := want[addr]; !ok {
			want[addr], reads[addr] = v, true
		}
	}
	if len(want) == 0 {
		return nil
	}
	addrs := make([]string, 0, len(want))
	for addr := range want {
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)
	have, err := e.State.Versions(addrs)
	if err != nil {
		return err
	}
	var reasons []string
	for _, addr := range addrs {
		if reason := staleReason(addr, have[addr], want[addr], reads[addr]); reason != "" {
			reasons = append(reasons, reason)
		}
	}
	switch {
	case len(reasons) == 0:
		return nil
	case e.saved != nil:
		return WithCode(ExitStale, &ErrStalePlan{Reasons: reasons})
	}
	return WithCode(ExitStale, fmt.Errorf("the plan changed while it waited; run \"pravo plan\" again:\n  - %s", strings.Join(reasons, "\n  - ")))
}

// pending reports whether a change does something when applied.
func pending(ch *Change) bool {
	switch ch.Action {
	case ActionCreate, ActionUpdate, ActionReplace, ActionDelete:
		return true
	case ActionRead:
		return ch.Deferred
	}
	return false
}

// needed reports whether the nodes that read the node of a change need it applied
// first: a pending change, and a data source whose read returned something other
// than its record. The nodes that read the latter were planned from what it
// returned.
func needed(ch *Change) bool {
	return pending(ch) || ch.Action == ActionRead && ch.writes()
}

// Remaining returns the plan without the changes that were already applied.
func (p *Plan) Remaining() *Plan {
	out := *p
	out.Changes = nil
	for _, ch := range p.Changes {
		if !ch.Applied {
			out.Changes = append(out.Changes, ch)
		}
	}
	out.narrow()
	return &out
}

// narrow makes the lists of a plan that was cut down say what the part writes.
// The nodes it reads are found again when it is applied. Inputs keeps the
// revisions that the file lists, of which the apply checks those it reads.
func (p *Plan) narrow() {
	p.Write, p.Support = p.writeAddrs(), nil
}

// ErrNotClosed is returned by Subset when the selection leaves out changes
// that the selected ones depend on.
type ErrNotClosed struct {
	Missing []string
	Closure []string
}

func (e *ErrNotClosed) Error() string {
	return fmt.Sprintf("the selection is not closed under dependencies; it also needs %s\n  (the smallest closed selection is -only %s)",
		strings.Join(e.Missing, ", "), strings.Join(e.Closure, ","))
}

// Subset returns the part of the plan that applies only the given addresses.
// The selection must be closed: every pending change a selected change
// depends on must be selected too (or already applied), and a selected
// delete requires every pending delete that depends on it. Changes that are
// not pending are never required.
func (p *Plan) Subset(addrs []string) (*Plan, error) {
	byAddr := map[string]*Change{}
	for _, ch := range p.Changes {
		byAddr[ch.Addr] = ch
	}
	selected := map[string]bool{}
	for _, a := range addrs {
		ch, ok := byAddr[a]
		if !ok {
			return nil, fmt.Errorf("%s is not part of the saved plan", a)
		}
		if ch.Applied {
			return nil, fmt.Errorf("%s was already applied from this plan", a)
		}
		selected[a] = true
	}
	// Compute the closure and the missing set.
	closure := map[string]bool{}
	for a := range selected {
		closure[a] = true
	}
	for changed := true; changed; {
		changed = false
		for a := range closure {
			ch := byAddr[a]
			if ch.Action == ActionDelete {
				for _, other := range p.Changes {
					if other.Action != ActionDelete || other.Applied || closure[other.Addr] {
						continue
					}
					for _, d := range other.Deps {
						if d == a {
							closure[other.Addr] = true
							changed = true
						}
					}
				}
				continue
			}
			for _, d := range ch.Deps {
				dep, ok := byAddr[d]
				if ok && needed(dep) && !dep.Applied && !closure[d] {
					closure[d] = true
					changed = true
				}
			}
		}
	}
	var missing, all []string
	for a := range closure {
		all = append(all, a)
		if !selected[a] {
			missing = append(missing, a)
		}
	}
	sort.Strings(missing)
	sort.Strings(all)
	if len(missing) > 0 {
		return nil, &ErrNotClosed{Missing: missing, Closure: all}
	}
	out := *p
	out.Changes = nil
	for _, ch := range p.Changes {
		if selected[ch.Addr] {
			out.Changes = append(out.Changes, ch)
		} else if needed(ch) && !ch.Applied {
			out.Partial = true
		}
	}
	out.narrow()
	return &out, nil
}

// LeftToApply reports whether the apply of what remains of the plan does
// anything. A change to a provider is left to apply until it is applied. The
// records and outputs that a plan only writes are left until an apply from the
// file has finished: a plan whose only change is an output has no other change
// to mark, and an apply that failed after it applied the other changes marked
// them and did not record the outputs.
func (pf *PlanFile) LeftToApply(remaining *Plan) bool {
	if remaining.HasWrites() {
		return true
	}
	return !remaining.Destroy && remaining.NeedsApply() && pf.ApplyTxn == ""
}

// MarkApplied records in the plan file that the given changes were applied,
// so that the file can be used again for the rest of the plan. It marks the
// changes in the file as it is when the lock is held, not in pf, so that the
// marks of applies from one file that run at the same time all stay in it.
func MarkApplied(path string, pf *PlanFile, addrs []string, txn string) error {
	return markApplied(path, pf, addrs, txn, true)
}

// MarkPartlyApplied records in the plan file the changes that an apply finished
// before it failed or was interrupted, as MarkApplied does for one that
// succeeded. The apply did not finish: the file does not say that an apply from it
// did, so the outputs and the records the plan only writes are still left to
// apply, and what is left of the plan can be applied from the file.
func MarkPartlyApplied(path string, pf *PlanFile, addrs []string, txn string) error {
	return markApplied(path, pf, addrs, txn, false)
}

func markApplied(path string, pf *PlanFile, addrs []string, txn string, finished bool) error {
	pf.markApplied(addrs, txn, finished)
	return updatePlanFile(path, func(cur *PlanFile) error {
		if !cur.Created.Equal(pf.Created) || cur.Lineage != pf.Lineage || cur.ConfigFingerprint != pf.ConfigFingerprint {
			return fmt.Errorf("%s holds another plan now", path)
		}
		cur.markApplied(addrs, txn, finished)
		return nil
	})
}

// markApplied marks the changes at the given addresses as applied by txn. The
// transaction of the file is that of an apply that finished.
func (pf *PlanFile) markApplied(addrs []string, txn string, finished bool) {
	if finished {
		pf.ApplyTxn = txn
	} else {
		pf.ApplyTxn = "" // an apply that did not finish did not record the outputs
	}
	done := stringSet(addrs)
	for i := range pf.Changes {
		if done[pf.Changes[i].Addr] {
			pf.Changes[i].Applied = true
			pf.Changes[i].AppliedTxn = txn
		}
	}
}

// fromState records a no-op change for a resource that is not part of the
// selection being applied and returns its recorded state for filling.
func (e *Engine) fromState(r *config.Resource) (tftypes.Value, error) {
	s, err := e.resourceSchema(r)
	if err != nil {
		return tftypes.Value{}, err
	}
	ch := &Change{Addr: r.Address(), Mode: r.Mode, Type: r.Type, Name: r.Name, ProviderKey: r.ProviderKey, Schema: s, Action: ActionNoop, Skipped: true}
	if r.Mode == "data" {
		ch.Action = ActionRead
	}
	sr := e.State.Get(r.Address())
	if sr == nil {
		e.record(ch)
		return tftypes.Value{}, nil
	}
	v, err := storedValue(sr, s)
	if err != nil {
		return tftypes.Value{}, fmt.Errorf("%s: %w", r.Address(), err)
	}
	ch.Prior, ch.Planned = v, v
	e.record(ch)
	return v, nil
}

func stringSet(list []string) map[string]bool {
	if len(list) == 0 {
		return nil
	}
	m := make(map[string]bool, len(list))
	for _, s := range list {
		m[s] = true
	}
	return m
}
