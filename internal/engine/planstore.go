package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
)

// A stored plan is a saved plan that lives in the state, where anyone who can
// read the state can find it. It is three objects below PlanDir:
//
//	plans/<id>.plan.json      the plan, in the format of a plan file. Written once and never changed.
//	plans/<id>.meta.json      the summary that a listing reads. Written once and never changed.
//	plans/<id>.progress.json  what has been applied from the plan, and who is using or removing it.
//	                          The only object that is replaced, and only through ReplaceIf.
//
// The id is "plan-" and the first characters of the digest of the plan, so the
// same bytes are always the same plan. The plan is written before its summary: a
// plan with no summary is a write that did not finish.
const (
	// PlanDir is the prefix of the stored plans in the state.
	PlanDir = "plans"

	planExt     = ".plan.json"
	metaExt     = ".meta.json"
	progressExt = ".progress.json"

	// PlanMetaFormat is the layout version of the summary and the progress object.
	PlanMetaFormat = 1

	// DefaultPlanTTL is how long an unapplied stored plan is kept.
	DefaultPlanTTL = 7 * 24 * time.Hour
	// DefaultAppliedKeep is how long a plan that was applied completely is kept.
	DefaultAppliedKeep = 24 * time.Hour
	// DefaultStaleGrace is how long a plan that can no longer be applied is kept
	// after it was first seen to be stale.
	DefaultStaleGrace = time.Hour

	// planIDHex is the number of hexadecimal digits of the digest in an id.
	planIDHex = 8
	// claimTimeout is how long a claim to remove a plan holds. A collector that
	// stopped after it made the claim does not keep the plan from being used.
	claimTimeout = 10 * time.Minute
	// applyingKeep is how long a plan counts as in use after an apply began on it
	// and did not say that it ended.
	applyingKeep = 24 * time.Hour
)

var planIDRe = regexp.MustCompile(`^plan-[0-9a-f]{6,64}$`)

// IsPlanID reports whether ref has the form of a stored plan's id or of the
// beginning of one: plan- and at least six hexadecimal digits.
func IsPlanID(ref string) bool { return planIDRe.MatchString(ref) }

// Errors of the plan store.
var (
	// ErrNoPlan is returned for an id that no stored plan has.
	ErrNoPlan = errors.New("no such stored plan")
	// ErrPlanRemoving is returned when a plan is being removed by a collector.
	ErrPlanRemoving = errors.New("the plan is being removed")
	errSkip         = errors.New("skip")
)

// PlanCounts is the number of actions of a plan, as the plan prints them.
type PlanCounts struct {
	Add     int `json:"add"`
	Change  int `json:"change"`
	Destroy int `json:"destroy"`
	Read    int `json:"read,omitempty"`
}

// PlanTouch is a change of a stored plan: what it does to which resource.
type PlanTouch struct {
	Addr   string `json:"addr"`
	Mode   string `json:"mode,omitempty"`
	Type   string `json:"type,omitempty"`
	Action Action `json:"action"`
}

// PlanBase is the version of a record that a plan expects, so that a listing can
// tell without reading the plan that it can no longer be applied.
type PlanBase struct {
	Revision   int    `json:"revision"`
	Generation string `json:"generation,omitempty"`
	// Reads is set for a record that the plan reads and does not write.
	Reads bool `json:"reads,omitempty"`
}

// PlanMeta is the summary of a stored plan.
type PlanMeta struct {
	Format  int       `json:"format"`
	ID      string    `json:"id"`
	Created time.Time `json:"created"`
	// Expires is when an unapplied plan is collected.
	Expires time.Time `json:"expires"`
	// Author is the actor that made the plan, or the user of the machine when it
	// has none.
	Author      string `json:"author"`
	Actor       string `json:"actor,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Command     string `json:"command,omitempty"`
	GitRevision string `json:"git_revision,omitempty"`
	// Tags and SysTags are the -t and -T of the command that made the plan: the
	// command that applies it needs the same.
	Tags    []string `json:"tags,omitempty"`
	SysTags bool     `json:"sys_tags,omitempty"`
	Lineage string   `json:"lineage"`
	// Fingerprint digests the configuration the plan was made from.
	Fingerprint string              `json:"config_fingerprint"`
	Destroy     bool                `json:"destroy,omitempty"`
	Summary     PlanCounts          `json:"summary"`
	Changes     []PlanTouch         `json:"changes"`
	Bases       map[string]PlanBase `json:"bases,omitempty"`
	// Needs says, for a change, which other changes of the plan an apply of it
	// has to apply with it: the pending changes it depends on, and for a delete the
	// pending deletes that depend on it. These are the rules of Plan.Subset, one
	// step at a time; a selection is closed when it holds everything that its
	// changes need. A change that needs nothing has no entry. Needs is nil in a
	// summary that was written before it was kept, which says nothing about the
	// dependencies: such a plan is judged as a whole.
	Needs       map[string][]string `json:"needs"`
	Reservation *ReservationView    `json:"reservation,omitempty"`
	// Size is the size of the plan object in bytes.
	Size int `json:"size"`
}

// ApplyCommand is the command that applies the plan, as it is typed.
func (m *PlanMeta) ApplyCommand() string {
	var b strings.Builder
	b.WriteString("pravo apply " + m.ID)
	for _, t := range m.Tags {
		b.WriteString(" -t " + shellWord(t))
	}
	if m.SysTags {
		b.WriteString(" -T")
	}
	return b.String()
}

// ShellWord quotes s for a shell when it needs it.
func ShellWord(s string) string { return shellWord(s) }

// shellWord quotes s for a shell when it needs it.
func shellWord(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-.,:=/@+%") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// PlanClaim is a claim on a plan: an apply that uses it, or a collector that
// removes it.
type PlanClaim struct {
	By string    `json:"by"`
	At time.Time `json:"at"`
}

// PlanProgress is what happened to a plan after it was stored.
type PlanProgress struct {
	Format int    `json:"format"`
	ID     string `json:"id"`
	// Applied maps the address of each change applied from the plan to the
	// transaction that applied it.
	Applied  map[string]string `json:"applied,omitempty"`
	ApplyTxn string            `json:"apply_txn,omitempty"`
	// Applying holds a claim for each apply from the plan that runs. Applies of
	// disjoint parts of one plan run at the same time, and each ends its own claim.
	Applying []PlanClaim `json:"applying,omitempty"`
	// Collecting is set by a collector that is about to remove the plan.
	Collecting *PlanClaim `json:"collecting,omitempty"`
	// StaleSince is when a collector first saw that the plan can no longer be applied.
	StaleSince *time.Time `json:"stale_since,omitempty"`
	// DoneAt is when the last change of the plan was applied.
	DoneAt  *time.Time `json:"done_at,omitempty"`
	Updated time.Time  `json:"updated"`
}

// runningApplies returns the claims of the applies that still count as running:
// an apply that was killed leaves its claim, which lapses after a day.
func (p *PlanProgress) runningApplies(now time.Time) []PlanClaim {
	var out []PlanClaim
	for _, c := range p.Applying {
		if now.Sub(c.At) < applyingKeep {
			out = append(out, c)
		}
	}
	return out
}

// ApplyRunning reports whether an apply from the plan is running at now.
func (p *PlanProgress) ApplyRunning(now time.Time) bool { return len(p.runningApplies(now)) > 0 }

// PlanStatus is where a stored plan stands.
type PlanStatus string

// The statuses, in the order a reader should ask about them.
const (
	// PlanApplied: every change was applied.
	PlanApplied PlanStatus = "applied"
	// PlanExpired: it was not applied before it expired.
	PlanExpired PlanStatus = "expired"
	// PlanStale: the configuration, the state or a provider moved on, so it can no longer be applied.
	PlanStale PlanStatus = "stale"
	// PlanPartlyStale: the state moved on under some of the changes that are left,
	// and the rest can still be applied, by name: the entry lists both.
	PlanPartlyStale PlanStatus = "partly-stale"
	// PlanPartial: some changes were applied and the rest can still be.
	PlanPartial PlanStatus = "partial"
	// PlanPending: it can be applied.
	PlanPending PlanStatus = "pending"
)

// Open reports whether a plan with the status can still be applied, all of it or
// a part.
func (s PlanStatus) Open() bool {
	return s == PlanPending || s == PlanPartial || s == PlanPartlyStale
}

// PlanEntry is a stored plan with its progress and where it stands now.
type PlanEntry struct {
	Meta     *PlanMeta
	Progress *PlanProgress
	Status   PlanStatus
	// Reasons say why a plan is stale. For a partly-stale plan they are the
	// reasons of the conflicts, in the order of the addresses.
	Reasons []string
	// Conflicts, for a partly-stale plan, holds each change that cannot be applied
	// now and why: a change whose record moved since the plan was made, with the
	// reason, and a change that has to be applied with one that cannot be.
	Conflicts map[string][]string
	// Live, for a partly-stale plan, is the part that can still be applied, sorted:
	// the changes that are left, that do not conflict and that need nothing that
	// does, with the changes they need. It is a closed selection for "apply --only".
	Live []string
	// moved holds the addresses in Conflicts whose own record moved.
	moved map[string]bool
}

// OnlyFlag returns the flag that makes an apply apply the live part of a
// partly-stale plan, "--only a,b,c", written as the shell reads it, or "" when
// the plan has no live part.
func (e *PlanEntry) OnlyFlag() string {
	if len(e.Live) == 0 {
		return ""
	}
	// The flag reads its value as a line of comma separated values: an address that
	// holds a comma, a quote or a line break needs quotes of its own.
	var line strings.Builder
	w := csv.NewWriter(&line)
	_ = w.Write(e.Live)
	w.Flush()
	return "--only " + shellWord(strings.TrimSuffix(line.String(), "\n"))
}

// ApplyLiveCommand is the command that applies the live part of the plan, as it
// is typed: the command that applies the plan, with the flag of OnlyFlag. It is
// "" when the plan has no live part.
func (e *PlanEntry) ApplyLiveCommand() string {
	if len(e.Live) == 0 {
		return ""
	}
	return e.Meta.ApplyCommand() + " " + e.OnlyFlag()
}

// FirstConflict returns the reason of the first change, by address, whose own
// record moved, or "" when the plan has no conflict.
func (e *PlanEntry) FirstConflict() string {
	addrs := make([]string, 0, len(e.Conflicts))
	for a := range e.Conflicts {
		if e.moved[a] || e.moved == nil {
			addrs = append(addrs, a)
		}
	}
	sort.Strings(addrs)
	for _, a := range addrs {
		if reasons := e.Conflicts[a]; len(reasons) > 0 {
			return reasons[0]
		}
	}
	return ""
}

// Unapplied is how many changes the plan has left to apply: those of the summary
// that were not applied, and the changes that conflict although the summary does
// not list them, such as a data source that the plan read.
func (e *PlanEntry) Unapplied() int {
	seen := map[string]bool{}
	for _, t := range e.Meta.Changes {
		if _, done := e.Progress.Applied[t.Addr]; !done {
			seen[t.Addr] = true
		}
	}
	for a := range e.Conflicts {
		seen[a] = true
	}
	return len(seen)
}

// AppliedCount is how many of the changes that the summary lists were applied.
// The progress object also holds the data sources, no-ops and kept records that
// an apply with --only had to select; the summary does not list those.
func (e *PlanEntry) AppliedCount() int {
	n := 0
	for _, c := range e.Meta.Changes {
		if _, ok := e.Progress.Applied[c.Addr]; ok {
			n++
		}
	}
	return n
}

// PlanCheck is what a listing compares a plan with: the state. The
// configuration and the providers are not compared: they belong to a working
// copy, and a plan that was made from another one is not stale for the people
// who share the state. An apply compares them.
type PlanCheck struct {
	Lineage string
	// Versions returns the version of the records at addrs.
	Versions func(addrs []string) (map[string]state.Version, error)
}

// PlanCheckFor returns what the plans are compared with: the lineage and the
// records of the store.
func PlanCheckFor(st *state.Store) *PlanCheck {
	return &PlanCheck{Lineage: st.Lineage(), Versions: st.Versions}
}

// memoized returns a check that reads each record once, however many plans
// look at it. It is for one listing or one collection, not for longer.
func memoized(check *PlanCheck) *PlanCheck {
	if check == nil || check.Versions == nil {
		return check
	}
	cache := map[string]state.Version{}
	c := *check
	c.Versions = func(addrs []string) (map[string]state.Version, error) {
		var need []string
		for _, a := range addrs {
			if _, ok := cache[a]; !ok {
				need = append(need, a)
			}
		}
		if len(need) > 0 {
			got, err := check.Versions(need)
			if err != nil {
				return nil, err
			}
			for a, v := range got {
				cache[a] = v
			}
		}
		out := make(map[string]state.Version, len(addrs))
		for _, a := range addrs {
			out[a] = cache[a]
		}
		return out, nil
	}
	return &c
}

// PlanStore reads and writes the stored plans of one state.
type PlanStore struct {
	B state.Backend
	// Now is the clock; tests set it.
	Now func() time.Time
}

// NewPlanStore returns the plan store of a state backend.
func NewPlanStore(b state.Backend) *PlanStore { return &PlanStore{B: b, Now: time.Now} }

func (ps *PlanStore) now() time.Time {
	if ps.Now != nil {
		return ps.Now().UTC()
	}
	return time.Now().UTC()
}

func planKey(id string) string     { return PlanDir + "/" + id + planExt }
func metaKey2(id string) string    { return PlanDir + "/" + id + metaExt }
func progressKey(id string) string { return PlanDir + "/" + id + progressExt }

// planObject splits the key of an object of a plan into its id and kind
// ("plan", "meta" or "progress"). The key of anything else is not a plan object.
func planObject(key string) (id, kind string, ok bool) {
	name, found := strings.CutPrefix(key, PlanDir+"/")
	if !found || strings.Contains(name, "/") {
		return "", "", false
	}
	for ext, k := range map[string]string{planExt: "plan", metaExt: "meta", progressExt: "progress"} {
		if base, has := strings.CutSuffix(name, ext); has && planIDRe.MatchString(base) {
			return base, k, true
		}
	}
	return "", "", false
}

// digestID is the id that the digest of a plan gives, with n hexadecimal digits.
func digestID(data []byte, n int) string {
	sum := sha256.Sum256(data)
	return "plan-" + hex.EncodeToString(sum[:])[:n]
}

// newPlanMeta summarises a plan file. Author is who made it.
func newPlanMeta(pf *PlanFile, author string, ttl time.Duration, size int) *PlanMeta {
	if ttl <= 0 {
		ttl = DefaultPlanTTL
	}
	m := &PlanMeta{
		Format: PlanMetaFormat, Created: pf.Created, Expires: pf.Created.Add(ttl), Author: author, Actor: pf.Actor, Reason: pf.Reason,
		Command: pf.Command, GitRevision: pf.GitRevision, Tags: pf.Tags, SysTags: pf.SysTags, Lineage: pf.Lineage, Fingerprint: pf.ConfigFingerprint, Destroy: pf.Destroy,
		Reservation: pf.Reservation, Size: size, Bases: map[string]PlanBase{}, Changes: []PlanTouch{},
	}
	touched := map[string]bool{}
	if m.Author == "" {
		m.Author = "unknown"
	}
	for _, sc := range pf.Changes {
		touches := true
		switch {
		case sc.Action == ActionCreate:
			m.Summary.Add++
		case sc.Action == ActionUpdate:
			m.Summary.Change++
		case sc.Action == ActionDelete:
			m.Summary.Destroy++
		case sc.Action == ActionReplace:
			m.Summary.Add++
			m.Summary.Destroy++
		case sc.Action == ActionRead:
			// Reads are counted as the plan counts them; only a deferred read is a
			// change that an apply makes.
			if !sc.Skipped {
				m.Summary.Read++
			}
			touches = sc.Deferred
		case sc.Action == ActionMove || sc.Imported:
		default:
			touches = false
		}
		if touches {
			m.Changes = append(m.Changes, PlanTouch{Addr: sc.Addr, Mode: sc.Mode, Type: sc.Type, Action: sc.Action})
			touched[sc.Addr] = true
		}
		if sc.Applied || sc.Skipped || sc.Kept || sc.Action == ActionMove {
			continue
		}
		m.Bases[sc.Addr] = PlanBase{Revision: sc.PriorRevision, Generation: sc.PriorGeneration}
	}
	for addr, rev := range pf.Inputs {
		if _, ok := m.Bases[addr]; !ok {
			m.Bases[addr] = PlanBase{Revision: rev, Generation: pf.InputGenerations[addr], Reads: true}
		}
	}
	sort.Slice(m.Changes, func(i, j int) bool { return m.Changes[i].Addr < m.Changes[j].Addr })
	m.Needs = needsOf(pf.Changes, touched)
	return m
}

// needsOf works out the Needs of a summary from the saved changes of a plan.
// touched holds the addresses that the summary lists. Only a change that an
// apply can select has an entry: one that the summary lists, and one that another
// change can need.
func needsOf(saved []savedChange, touched map[string]bool) map[string][]string {
	changes := make([]*Change, len(saved))
	byAddr := make(map[string]*Change, len(saved))
	for i, sc := range saved {
		changes[i] = &Change{Addr: sc.Addr, Mode: sc.Mode, Action: sc.Action, Deps: sc.Deps, Deferred: sc.Deferred, Skipped: sc.Skipped, Kept: sc.Kept, Applied: sc.Applied}
		byAddr[sc.Addr] = changes[i]
	}
	out := map[string][]string{}
	for addr, need := range requirements(changes) {
		if touched[addr] || needed(byAddr[addr]) {
			out[addr] = need
		}
	}
	return out
}

// requirements returns, for each change of a plan that was not applied, the
// changes that an apply of it has to apply with it, sorted, and none for a change
// that needs nothing. They are the rules of Plan.Subset, one step at a time: a
// pending change, or a data source that returned something other than its record,
// that a change depends on is needed by it, and a delete is needed by every
// pending delete that depends on it. A change that was applied needs nothing and
// is needed by no one.
func requirements(changes []*Change) map[string][]string {
	byAddr := make(map[string]*Change, len(changes))
	deletedBy := map[string][]string{}
	for _, ch := range changes {
		byAddr[ch.Addr] = ch
		if ch.Action != ActionDelete || ch.Applied {
			continue
		}
		for _, d := range ch.Deps {
			deletedBy[d] = append(deletedBy[d], ch.Addr)
		}
	}
	out := map[string][]string{}
	for _, ch := range changes {
		if ch.Applied {
			continue
		}
		var list []string
		if ch.Action == ActionDelete {
			list = deletedBy[ch.Addr]
		} else {
			for _, d := range ch.Deps {
				if dep, ok := byAddr[d]; ok && needed(dep) && !dep.Applied {
					list = append(list, d)
				}
			}
		}
		set := map[string]bool{}
		var need []string
		for _, a := range list {
			if a != ch.Addr && !set[a] {
				set[a] = true
				need = append(need, a)
			}
		}
		if len(need) > 0 {
			sort.Strings(need)
			out[ch.Addr] = need
		}
	}
	return out
}

// Put stores a plan. data is the plan file, as SavePlan writes it, and meta its
// summary; Put sets meta.ID and meta.Size. A plan that is stored again keeps its id.
func (ps *PlanStore) Put(ctx context.Context, data []byte, meta *PlanMeta) error {
	var id string
	for n := planIDHex; n <= 24; n += 4 {
		id = digestID(data, n)
		ok, err := ps.B.PutIfAbsent(ctx, planKey(id), data)
		if err != nil {
			return fmt.Errorf("storing the plan: %w", err)
		}
		if ok {
			break
		}
		old, err := ps.B.Get(ctx, planKey(id))
		if err != nil {
			return fmt.Errorf("storing the plan: %w", err)
		}
		if bytes.Equal(old, data) {
			break
		}
		id = ""
	}
	if id == "" {
		return errors.New("storing the plan: no free id")
	}
	meta.ID, meta.Size = id, len(data)
	enc, err := indentJSON(meta)
	if err != nil {
		return err
	}
	if _, err := ps.B.PutIfAbsent(ctx, metaKey2(id), enc); err != nil {
		return fmt.Errorf("storing the plan: %w", err)
	}
	// A collector that took the plan, written but without its summary, for a write
	// that did not finish may have removed it meanwhile: its clock is ahead of ours.
	if _, err := ps.B.Get(ctx, planKey(id)); errors.Is(err, state.ErrNotFound) {
		return fmt.Errorf("storing the plan: %s was removed by a collection while it was being stored; check the clocks of the machines that share the state", id)
	}
	return nil
}

// ids lists the ids of the plans that have any object, with the kinds each has.
func (ps *PlanStore) ids(ctx context.Context) (map[string]map[string]bool, error) {
	keys, err := ps.B.List(ctx, PlanDir)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]bool{}
	for _, k := range keys {
		id, kind, ok := planObject(k)
		if !ok {
			continue
		}
		if out[id] == nil {
			out[id] = map[string]bool{}
		}
		out[id][kind] = true
	}
	return out, nil
}

// Resolve turns an id, or the beginning of one, into the id of the stored plan
// that has it. A beginning that fits several plans is an error that lists them.
func (ps *PlanStore) Resolve(ctx context.Context, ref string) (string, error) {
	if !IsPlanID(ref) {
		return "", fmt.Errorf("%q is not the id of a stored plan: ids look like plan-2f9c1a4e", ref)
	}
	all, err := ps.ids(ctx)
	if err != nil {
		return "", err
	}
	var match []string
	for id, kinds := range all {
		if kinds["plan"] && strings.HasPrefix(id, ref) {
			match = append(match, id)
		}
	}
	sort.Strings(match)
	switch len(match) {
	case 0:
		return "", fmt.Errorf("%w: %s (see: pravo plan list --all)", ErrNoPlan, ref)
	case 1:
		return match[0], nil
	}
	return "", fmt.Errorf("%s fits %d stored plans: %s", ref, len(match), strings.Join(match, ", "))
}

// Meta reads the summary of a plan.
func (ps *PlanStore) Meta(ctx context.Context, id string) (*PlanMeta, error) {
	data, err := ps.B.Get(ctx, metaKey2(id))
	if errors.Is(err, state.ErrNotFound) {
		return nil, fmt.Errorf("%w: %s", ErrNoPlan, id)
	}
	if err != nil {
		return nil, err
	}
	var m PlanMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("the summary of %s is not readable: %w", id, err)
	}
	if m.Format > PlanMetaFormat {
		return nil, fmt.Errorf("%s was stored by a newer pravo (format %d)", id, m.Format)
	}
	if m.ID != id {
		// The id is the name of the object: a summary that says another one is not
		// what it was stored as, and what it names is not to be shown or built on.
		return nil, fmt.Errorf("the summary of %s names another plan, %q", id, m.ID)
	}
	return &m, nil
}

// Progress reads what has been applied from a plan. A plan that nothing was done
// with has an empty progress.
func (ps *PlanStore) Progress(ctx context.Context, id string) (*PlanProgress, error) {
	data, err := ps.B.Get(ctx, progressKey(id))
	if errors.Is(err, state.ErrNotFound) {
		return &PlanProgress{Format: PlanMetaFormat, ID: id}, nil
	}
	if err != nil {
		return nil, err
	}
	var p PlanProgress
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("the progress of %s is not readable: %w", id, err)
	}
	return &p, nil
}

// ReadFile reads the plan, in the form of a plan file, with what has been
// applied from it marked in the changes, so that the code that applies a plan
// file applies the rest of it.
func (ps *PlanStore) ReadFile(ctx context.Context, id string) (*PlanFile, *PlanProgress, error) {
	data, err := ps.B.Get(ctx, planKey(id))
	if errors.Is(err, state.ErrNotFound) {
		return nil, nil, fmt.Errorf("%w: %s", ErrNoPlan, id)
	}
	if err != nil {
		return nil, nil, err
	}
	pf, err := parsePlanFile(id, data)
	if err != nil {
		return nil, nil, err
	}
	pr, err := ps.Progress(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	pf.applyProgress(pr)
	return pf, pr, nil
}

// applyProgress marks the changes that the progress says were applied.
func (pf *PlanFile) applyProgress(pr *PlanProgress) {
	if pr == nil {
		return
	}
	if pr.ApplyTxn != "" {
		pf.ApplyTxn = pr.ApplyTxn
	}
	for i := range pf.Changes {
		if txn, ok := pr.Applied[pf.Changes[i].Addr]; ok {
			pf.Changes[i].Applied = true
			pf.Changes[i].AppliedTxn = txn
		}
	}
}

// Pull writes the plan to a file, as "plan --out" would have.
func (ps *PlanStore) Pull(ctx context.Context, id, path string) error {
	pf, _, err := ps.ReadFile(ctx, id)
	if err != nil {
		return err
	}
	data, err := indentJSON(pf)
	if err != nil {
		return err
	}
	return writeFile0600(path, data)
}

// updateProgress changes the progress object of a plan with edit, creating the
// object when the plan has none. edit returns errSkip to decline: the object is
// then not written and updateProgress returns errSkip.
func (ps *PlanStore) updateProgress(ctx context.Context, id string, edit func(p *PlanProgress) error) error {
	for attempt := 0; attempt < 8; attempt++ {
		_, err := ps.B.Get(ctx, progressKey(id))
		if errors.Is(err, state.ErrNotFound) {
			p := &PlanProgress{Format: PlanMetaFormat, ID: id}
			if err := edit(p); err != nil {
				return err
			}
			p.Updated = ps.now()
			data, err := indentJSON(p)
			if err != nil {
				return err
			}
			ok, err := ps.B.PutIfAbsent(ctx, progressKey(id), data)
			if err != nil {
				return err
			}
			if ok {
				return nil
			}
			continue
		}
		if err != nil {
			return err
		}
		var editErr error
		ok, err := ps.B.ReplaceIf(ctx, progressKey(id), func(current []byte) ([]byte, bool) {
			var p PlanProgress
			if err := json.Unmarshal(current, &p); err != nil {
				editErr = fmt.Errorf("the progress of %s is not readable: %w", id, err)
				return nil, false
			}
			if p.ID == "" {
				p.ID, p.Format = id, PlanMetaFormat
			}
			if editErr = edit(&p); editErr != nil {
				return nil, false
			}
			p.Updated = ps.now()
			data, err := indentJSON(&p)
			if err != nil {
				editErr = err
				return nil, false
			}
			return data, true
		})
		switch {
		case errors.Is(err, state.ErrNotFound):
			continue
		case err != nil:
			return err
		case editErr != nil:
			return editErr
		case ok:
			return nil
		}
		// The object changed between the read and the write: look again.
	}
	return errors.New("the plan is changing too fast to update: try again")
}

// BeginApply says that an apply from the plan starts, so that no collector
// removes it meanwhile. It fails when a collector has claimed the plan. Other
// applies from the plan keep their claims.
func (ps *PlanStore) BeginApply(ctx context.Context, id, owner string) error {
	now := ps.now()
	err := ps.updateProgress(ctx, id, func(p *PlanProgress) error {
		if p.Collecting != nil && now.Sub(p.Collecting.At) < claimTimeout {
			return ErrPlanRemoving
		}
		p.Collecting = nil
		claims := []PlanClaim{}
		for _, c := range p.runningApplies(now) {
			if c.By != owner {
				claims = append(claims, c)
			}
		}
		p.Applying = append(claims, PlanClaim{By: owner, At: now})
		return nil
	})
	if errors.Is(err, ErrPlanRemoving) {
		return fmt.Errorf("%w: a collection is removing %s; run \"pravo plan --store\" again", ErrPlanRemoving, id)
	}
	return err
}

// EndApply says that the apply of the owner is over. It is best effort: a claim
// that is not cleared lapses after a day.
func (ps *PlanStore) EndApply(ctx context.Context, id, owner string) {
	_ = ps.updateProgress(ctx, id, func(p *PlanProgress) error {
		var kept []PlanClaim
		for _, c := range p.Applying {
			if c.By != owner {
				kept = append(kept, c)
			}
		}
		if len(kept) == len(p.Applying) {
			return errSkip
		}
		p.Applying = kept
		return nil
	})
}

// MarkApplied records that the changes at addrs were applied by txn. pending
// lists the changes that were left to apply when that apply began: the plan is
// done when every one of them has been applied, by this apply or by others that
// ran from the plan at the same time.
func (ps *PlanStore) MarkApplied(ctx context.Context, id string, addrs []string, txn string, pending []string) error {
	if _, err := ps.B.Get(ctx, metaKey2(id)); errors.Is(err, state.ErrNotFound) {
		return fmt.Errorf("%w: %s was removed while it was applied", ErrNoPlan, id)
	}
	now := ps.now()
	return ps.updateProgress(ctx, id, func(p *PlanProgress) error {
		if p.Applied == nil {
			p.Applied = map[string]string{}
		}
		for _, a := range addrs {
			p.Applied[a] = txn
		}
		p.ApplyTxn = txn
		if p.DoneAt == nil {
			done := true
			for _, a := range pending {
				if _, ok := p.Applied[a]; !ok {
					done = false
					break
				}
			}
			if done {
				p.DoneAt = &now
			}
		}
		return nil
	})
}

// planReadWidth is how many plans are read at the same time.
const planReadWidth = 16

// planRead is the summary and the progress of one plan, as read.
type planRead struct {
	meta     *PlanMeta
	metaErr  error
	progress *PlanProgress
	progErr  error
}

// readPlans reads the summary and the progress of the plans at ids, several at a
// time. A plan that the listing shows no progress object for has an empty
// progress, and none is asked for.
func (ps *PlanStore) readPlans(ctx context.Context, all map[string]map[string]bool, ids []string) map[string]*planRead {
	out := make(map[string]*planRead, len(ids))
	for _, id := range ids {
		out[id] = &planRead{}
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, planReadWidth)
	for _, id := range ids {
		wg.Add(1)
		go func(id string, r *planRead) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r.meta, r.metaErr = ps.Meta(ctx, id)
			if all[id]["progress"] {
				r.progress, r.progErr = ps.Progress(ctx, id)
			} else {
				r.progress = &PlanProgress{Format: PlanMetaFormat, ID: id}
			}
		}(id, out[id])
	}
	wg.Wait()
	return out
}

// SkippedPlan is a stored plan that a listing could not read: its summary or its
// progress is damaged, or was written by a newer pravo.
type SkippedPlan struct {
	ID  string
	Err error
}

// Entries reads every stored plan with its progress and works out where it
// stands. check may be nil: no plan is then called stale. A plan that a collector
// is removing, a write that did not finish, and a plan whose summary or progress
// cannot be read are not listed.
func (ps *PlanStore) Entries(ctx context.Context, check *PlanCheck) ([]*PlanEntry, error) {
	out, _, err := ps.EntriesAndSkipped(ctx, check)
	return out, err
}

// EntriesAndSkipped is Entries that also says which plans it could not read.
func (ps *PlanStore) EntriesAndSkipped(ctx context.Context, check *PlanCheck) ([]*PlanEntry, []SkippedPlan, error) {
	all, err := ps.ids(ctx)
	if err != nil {
		return nil, nil, err
	}
	var whole []string
	for id, kinds := range all {
		if kinds["plan"] && kinds["meta"] {
			whole = append(whole, id)
		}
	}
	sort.Strings(whole)
	reads := ps.readPlans(ctx, all, whole)
	now := ps.now()
	check = memoized(check)
	var out []*PlanEntry
	var skipped []SkippedPlan
	for _, id := range whole {
		r := reads[id]
		if r.metaErr != nil || r.progErr != nil {
			skipped = append(skipped, SkippedPlan{ID: id, Err: errors.Join(r.metaErr, r.progErr)})
			continue
		}
		if r.progress.Collecting != nil && now.Sub(r.progress.Collecting.At) < claimTimeout {
			continue
		}
		out = append(out, newPlanEntry(r.meta, r.progress, check, now))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Meta.Created.Equal(out[j].Meta.Created) {
			return out[i].Meta.Created.After(out[j].Meta.Created)
		}
		return out[i].Meta.ID < out[j].Meta.ID
	})
	return out, skipped, nil
}

// Entry reads one stored plan, by its id, with its progress and where it stands.
func (ps *PlanStore) Entry(ctx context.Context, id string, check *PlanCheck) (*PlanEntry, error) {
	m, err := ps.Meta(ctx, id)
	if err != nil {
		return nil, err
	}
	p, err := ps.Progress(ctx, id)
	if err != nil {
		return nil, err
	}
	return newPlanEntry(m, p, check, ps.now()), nil
}

// planStatus works out where a plan stands. The reasons are those that make it
// stale, or for a partly-stale plan those of its conflicts.
func planStatus(m *PlanMeta, p *PlanProgress, check *PlanCheck, now time.Time) (PlanStatus, []string) {
	v := judgePlan(m, p, check, now)
	return v.status, v.reasons
}

// newPlanEntry makes the entry of a plan, with where it stands.
func newPlanEntry(m *PlanMeta, p *PlanProgress, check *PlanCheck, now time.Time) *PlanEntry {
	v := judgePlan(m, p, check, now)
	return &PlanEntry{Meta: m, Progress: p, Status: v.status, Reasons: v.reasons, Conflicts: v.conflicts, Live: v.live, moved: v.moved}
}

// planVerdict is where a plan stands.
type planVerdict struct {
	status  PlanStatus
	reasons []string
	// conflicts, live and moved are those of a partly-stale plan.
	conflicts map[string][]string
	live      []string
	moved     map[string]bool
}

// judgePlan works out where a plan stands, change by change.
//
// A plan that was made against another state, or that reads a record that moved,
// is stale as a whole: what its changes would do was worked out from those
// records. Otherwise a change whose own record moved conflicts, and so does a
// change that has to be applied with one that conflicts (Needs). The changes that
// are left are the live part. A plan with a conflict and a live part is
// partly-stale; one with no live part is stale, as is one whose summary does not
// say what its changes need.
func judgePlan(m *PlanMeta, p *PlanProgress, check *PlanCheck, now time.Time) planVerdict {
	if p.DoneAt != nil {
		return planVerdict{status: PlanApplied}
	}
	if now.After(m.Expires) {
		return planVerdict{status: PlanExpired}
	}
	open := PlanPending
	if len(p.Applied) > 0 {
		open = PlanPartial
	}
	moved := movedRecords(m, p, check)
	if len(moved.reasons) == 0 {
		return planVerdict{status: open}
	}
	whole := planVerdict{status: PlanStale, reasons: moved.reasons}
	if moved.global || m.Needs == nil {
		return whole
	}
	blocked := blockedChanges(m, p, moved.changes)
	live := liveChanges(m, p, blocked)
	if len(live) == 0 {
		return whole
	}
	v := planVerdict{status: PlanPartlyStale, conflicts: map[string][]string{}, live: live, moved: map[string]bool{}}
	addrs := make([]string, 0, len(blocked))
	for addr := range blocked {
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)
	for _, addr := range addrs {
		var reasons []string
		if r, ok := moved.changes[addr]; ok {
			reasons, v.moved[addr] = []string{r}, true
		} else {
			reasons = []string{fmt.Sprintf("%s must be applied with %s, which cannot be applied", addr, blocked[addr])}
		}
		v.conflicts[addr] = reasons
		v.reasons = append(v.reasons, reasons...)
	}
	return v
}

// planMoves is what a plan expects of the state and does not find.
type planMoves struct {
	// reasons lists why, in the order of the addresses.
	reasons []string
	// global is set when the plan is stale as a whole: it was made against another
	// state, or it reads a record that moved.
	global bool
	// changes maps the address of each change whose own record moved to the reason.
	changes map[string]string
}

// movedRecords compares the state with what the plan expects of it: the lineage,
// and the record of each change and of each input that was not applied.
func movedRecords(m *PlanMeta, p *PlanProgress, check *PlanCheck) planMoves {
	var out planMoves
	if check == nil {
		return out
	}
	if check.Lineage != "" && m.Lineage != check.Lineage {
		out.reasons, out.global = append(out.reasons, "the plan was made against a different state (lineage mismatch)"), true
	}
	if check.Versions == nil || len(out.reasons) > 0 {
		return out
	}
	var addrs []string
	for addr := range m.Bases {
		if _, done := p.Applied[addr]; !done {
			addrs = append(addrs, addr)
		}
	}
	sort.Strings(addrs)
	if len(addrs) == 0 {
		return out
	}
	have, err := check.Versions(addrs)
	if err != nil {
		return out
	}
	for _, addr := range addrs {
		b := m.Bases[addr]
		r := staleReason(addr, have[addr], state.Version{Revision: b.Revision, Generation: b.Generation}, b.Reads)
		if r == "" {
			continue
		}
		out.reasons = append(out.reasons, r)
		if b.Reads || !selectable(m, addr) {
			// A record that the plan reads, or one that no apply of a part would take
			// as a change of its own, such as a no-op that other changes read, makes the
			// whole plan stale: the changes that read it were planned from the record as
			// it was.
			out.global = true
			continue
		}
		if out.changes == nil {
			out.changes = map[string]string{}
		}
		out.changes[addr] = r
	}
	return out
}

// selectable says whether an apply of a part can take addr as a change: it is a
// change of the summary, or one that other changes have to be applied with.
func selectable(m *PlanMeta, addr string) bool {
	for _, t := range m.Changes {
		if t.Addr == addr {
			return true
		}
	}
	for a, need := range m.Needs {
		if a == addr {
			return true
		}
		for _, n := range need {
			if n == addr {
				return true
			}
		}
	}
	return false
}

// blockedChanges returns the changes that cannot be applied, each with the change
// that blocks it, or for a change whose own record moved with "". A change that
// has to be applied with one that cannot be is blocked itself, by the nearest
// such change. A change that was applied is not part of it.
func blockedChanges(m *PlanMeta, p *PlanProgress, moved map[string]string) map[string]string {
	needing := map[string][]string{}
	for addr, need := range m.Needs {
		if _, done := p.Applied[addr]; done {
			continue
		}
		for _, n := range need {
			if _, done := p.Applied[n]; !done {
				needing[n] = append(needing[n], addr)
			}
		}
	}
	queue := make([]string, 0, len(moved))
	blocked := make(map[string]string, len(moved))
	for addr := range moved {
		queue = append(queue, addr)
		blocked[addr] = ""
	}
	sort.Strings(queue)
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		dependents := needing[n]
		sort.Strings(dependents)
		for _, addr := range dependents {
			if _, ok := blocked[addr]; !ok {
				blocked[addr] = n
				queue = append(queue, addr)
			}
		}
	}
	return blocked
}

// liveChanges returns the part of a plan that can still be applied, sorted: the
// changes of the summary that were not applied and are not blocked, and the changes
// that they need.
func liveChanges(m *PlanMeta, p *PlanProgress, blocked map[string]string) []string {
	live := map[string]bool{}
	var walk func(addr string)
	walk = func(addr string) {
		if live[addr] {
			return
		}
		live[addr] = true
		for _, n := range m.Needs[addr] {
			if _, done := p.Applied[n]; !done {
				walk(n)
			}
		}
	}
	for _, t := range m.Changes {
		if _, done := p.Applied[t.Addr]; done || t.Action == ActionMove {
			continue // a move was made when the plan was, and no apply repeats it
		}
		if _, no := blocked[t.Addr]; !no {
			walk(t.Addr)
		}
	}
	out := make([]string, 0, len(live))
	for addr := range live {
		out = append(out, addr)
	}
	sort.Strings(out)
	return out
}

// planStaleReasons says why a plan, taken whole, can no longer be applied to the
// state: what a plan that is stale as a whole reports.
func planStaleReasons(m *PlanMeta, p *PlanProgress, check *PlanCheck) []string {
	return movedRecords(m, p, check).reasons
}

// Overlap is a pending plan that changes a resource that another plan changes.
type Overlap struct {
	Plan *PlanMeta
	// Addrs are the resources both plans change.
	Addrs []string
}

// touchedBy returns the addresses that the plan of e still changes: those of the
// summary that were not applied, once each.
func (e *PlanEntry) touchedBy() []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range e.Meta.Changes {
		if _, done := e.Progress.Applied[t.Addr]; done || seen[t.Addr] {
			continue
		}
		seen[t.Addr] = true
		out = append(out, t.Addr)
	}
	return out
}

// changing returns the addresses that the plan of e will still change: the
// unapplied ones, less those that conflict in a partly-stale plan, which it can
// not apply.
func (e *PlanEntry) changing() []string {
	var out []string
	for _, a := range e.touchedBy() {
		if _, blocked := e.Conflicts[a]; !blocked {
			out = append(out, a)
		}
	}
	return out
}

// Overlaps returns the open plans among entries, other than the one with id
// self, that change a resource that touches also changes. A change that a plan
// already applied is not one it makes any more, and nor is one that a partly-stale
// plan cannot apply.
func Overlaps(entries []*PlanEntry, self string, touches []PlanTouch) []Overlap {
	mine := map[string]bool{}
	for _, t := range touches {
		mine[t.Addr] = true
	}
	var out []Overlap
	for _, e := range entries {
		if e.Meta.ID == self || !e.Status.Open() {
			continue
		}
		var addrs []string
		for _, a := range e.changing() {
			if mine[a] {
				addrs = append(addrs, a)
			}
		}
		if len(addrs) > 0 {
			sort.Strings(addrs)
			out = append(out, Overlap{Plan: e.Meta, Addrs: addrs})
		}
	}
	return out
}

// Touching returns the entries that change the resource at addr.
func Touching(entries []*PlanEntry, addr string) []*PlanEntry {
	var out []*PlanEntry
	for _, e := range entries {
		for _, a := range e.touchedBy() {
			if a == addr {
				out = append(out, e)
				break
			}
		}
	}
	return out
}

// SummarizePlan returns the summary that a stored plan of plan would have, for
// the command that wants to compare a plan with the stored ones.
func (e *Engine) SummarizePlan(plan *Plan, tags []string, sysTags bool) (*PlanMeta, error) {
	pf, err := e.buildPlanFile(plan, tags, sysTags)
	if err != nil {
		return nil, err
	}
	return newPlanMeta(pf, e.State.Owner(), 0, 0), nil
}

// StorePlan writes the plan into the state as a stored plan and returns its
// summary. ttl is how long it is kept if it is not applied; zero is the default.
func (e *Engine) StorePlan(ctx context.Context, plan *Plan, tags []string, sysTags bool, ttl time.Duration) (*PlanMeta, error) {
	pf, err := e.buildPlanFile(plan, tags, sysTags)
	if err != nil {
		return nil, err
	}
	data, err := indentJSON(pf)
	if err != nil {
		return nil, err
	}
	meta := newPlanMeta(pf, e.State.Owner(), ttl, len(data))
	if err := NewPlanStore(e.State.Backend()).Put(ctx, data, meta); err != nil {
		return nil, err
	}
	return meta, nil
}
