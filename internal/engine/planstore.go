package engine

import (
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

	"github.com/pravo-systems/pravo/internal/protect"
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
//
// In a sealed state the plan and the values in the summary are sealed to the
// readers of the state; see planseal.go. The objects are still written once, and
// replaced only when a plan is sealed or sealed again in place.
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
	// ErrTooLarge is returned for an object of a stored plan that is larger than
	// what is read of it. It is wrapped with the id, the kind and the size.
	ErrTooLarge = errors.New("a stored plan object is too large")
)

// What is read of one stored plan. The objects are written by whoever can write
// to the state, so what a reader takes in, and decodes, is bounded: a larger
// object is refused with ErrTooLarge. The size is known once the object is
// read, so the bytes were transferred once; they are not decoded or kept.
const (
	// MaxPlanMetaBytes bounds the summary of a plan.
	MaxPlanMetaBytes = 8 << 20
	// MaxPlanProgressBytes bounds the progress object of a plan.
	MaxPlanProgressBytes = 1 << 20
	// MaxPlanFileBytes bounds the plan itself.
	MaxPlanFileBytes = 256 << 20
)

func tooLarge(kind, id string, size, limit int) error {
	return fmt.Errorf("%w: the %s of %s is %d bytes, and at most %d are read", ErrTooLarge, kind, id, size, limit)
}

// PlanMetaCache keeps the summaries that a PlanStore has read, by plan id. A
// summary is written once and never changed, so a listing that is made again
// need not read and decode it again; the progress, which is replaced, is read
// each time. A summary that is refused or cannot be decoded is kept as the
// error, for the same reason. An error of the backend is not kept. The cache is
// of use to one reader that lists the state again and again, as the viewer does;
// it assumes that an id is not stored again with another summary while it is
// listed.
type PlanMetaCache struct {
	mu sync.Mutex
	m  map[string]metaResult
}

type metaResult struct {
	meta *PlanMeta
	err  error
}

// NewPlanMetaCache returns an empty cache.
func NewPlanMetaCache() *PlanMetaCache { return &PlanMetaCache{m: map[string]metaResult{}} }

// Len is the number of summaries held.
func (c *PlanMetaCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

func (c *PlanMetaCache) get(id string) (metaResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.m[id]
	return r, ok
}

func (c *PlanMetaCache) put(id string, r metaResult) {
	c.mu.Lock()
	c.m[id] = r
	c.mu.Unlock()
}

// forget drops the summary of one plan, for a plan whose summary was rewritten.
func (c *PlanMetaCache) forget(id string) {
	c.mu.Lock()
	delete(c.m, id)
	c.mu.Unlock()
}

// retain forgets the ids that the listing did not show a summary for.
func (c *PlanMetaCache) retain(all map[string]map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id := range c.m {
		if !all[id]["meta"] {
			delete(c.m, id)
		}
	}
}

// PlanCounts is the number of actions of a plan, as the plan prints them.
type PlanCounts struct {
	Add     int `json:"add"`
	Change  int `json:"change"`
	Destroy int `json:"destroy"`
	Read    int `json:"read,omitempty"`
}

// PlanTouch is a change of a stored plan: what it does to which resource.
type PlanTouch struct {
	Addr string `json:"addr"`
	// From is the address that a move takes the record from: the plan changes that resource too.
	From   string `json:"from,omitempty"`
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

	// Sealed holds, in the summary of a sealed state, the command line and the tags
	// as they were typed. Command and Tags then hold the form that the history of
	// the state shows (see state.RedactArgs). A PlanStore that can open the box puts
	// the originals back and clears Sealed.
	Sealed *protect.Sealed `json:"sealed,omitempty"`
	// Redacted is set on a summary that was read from a sealed state without a key
	// that opens its box: Command and Tags are the form that the history shows.
	Redacted bool `json:"-"`
	// Unsealed is set on a summary that was read from a sealed state and was
	// written before the state was sealed: it is readable by anyone who can read
	// the store, and stays so until pravo protect rewrap --all.
	Unsealed bool `json:"-"`
	// shownCommand is the form of the command line that a sealed state shows, as
	// the code that made the summary worked it out from the arguments.
	shownCommand string
}

// ApplyCommand is the command that applies the plan, as it is typed. For a summary
// that is Redacted the values of the tags are placeholders.
func (m *PlanMeta) ApplyCommand() string { return m.applyCommand(m.Redacted) }

// ApplyTemplate is ApplyCommand with a placeholder for the value of every tag,
// whether or not the summary could be opened: the command for a place that shows
// what the plan was made with and not the values it was given, such as the viewer.
func (m *PlanMeta) ApplyTemplate() string { return m.applyCommand(true) }

func (m *PlanMeta) applyCommand(placeholders bool) string {
	var b strings.Builder
	b.WriteString("pravo apply " + m.ID)
	for _, t := range m.Tags {
		if !placeholders {
			b.WriteString(" -t " + shellWord(t))
			continue
		}
		// The value is not shown (it is sealed and this store cannot open it, or it
		// is not for this place): a placeholder that a shell does not take for a
		// value, so that pasting the line fails.
		k, _, ok := strings.Cut(t, "=")
		if !ok {
			b.WriteString(" -t <value>")
			continue
		}
		b.WriteString(" -t " + shellWord(k) + "=<value>")
	}
	if m.SysTags {
		b.WriteString(" -T")
	}
	return b.String()
}

// sealedTokenRe matches the tag that a sealed state shows in place of a value.
var sealedTokenRe = regexp.MustCompile(`<sealed(?::[0-9a-f]+)?>`)

// CommandText is the command line of the plan as a person is shown it. For a
// summary that is Redacted each sealed value, a tag as <sealed:a1b2c3d4>, is a
// placeholder, <value>, as in ApplyCommand: the tag is not a value that a shell
// takes, and the key before it stays.
func (m *PlanMeta) CommandText() string {
	if !m.Redacted {
		return m.Command
	}
	return sealedTokenRe.ReplaceAllString(m.Command, "<value>")
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
	// Committed is set on the claim of a collector in a write that only succeeds while
	// the claim is its own and no apply runs, just before it deletes. An apply from the
	// plan is refused from then on, whatever the clocks say: the plan is going. A
	// claim that is not committed lapses after claimTimeout.
	Committed bool `json:"committed,omitempty"`
}

// holds reports whether the claim of a collector keeps an apply from using the plan at
// now: a committed claim always does, the others for claimTimeout.
func (c *PlanClaim) holds(now time.Time) bool {
	return c != nil && (c.Committed || now.Sub(c.At) < claimTimeout)
}

// adoptable reports whether a collector may finish the removal that another began: the
// claim is committed, and has not been renewed for claimTimeout, so that collector is
// gone.
func (c *PlanClaim) adoptable(now time.Time) bool {
	return c != nil && c.Committed && now.Sub(c.At) >= claimTimeout
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
	// Cache, when set, keeps the summaries that a listing reads (see
	// PlanMetaCache). The summaries it returns are shared: they are not to be changed.
	Cache *PlanMetaCache
	// Seal seals and opens the plans, their command lines and their tags. If it is
	// nil, the store uses the sealer of the state that it was made for, which is nil
	// for a state that is not sealed: nothing is then sealed, and every object is
	// written and read as it always was.
	Seal state.Sealer

	st *state.Store
}

// NewPlanStore returns the plan store of a state backend. It seals nothing: use
// NewPlanStoreFor for a state that may be sealed.
func NewPlanStore(b state.Backend) *PlanStore { return &PlanStore{B: b, Now: time.Now} }

// NewPlanStoreFor returns the plan store of a state. If the state is sealed, the
// plans that the store writes are sealed to the readers of the state, and a store
// whose key does not open them lists them from their summaries and fails with
// state.ErrNotReader when asked for what a plan holds.
func NewPlanStoreFor(st *state.Store) *PlanStore {
	return &PlanStore{B: st.Backend(), Now: time.Now, st: st}
}

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
			m.Changes = append(m.Changes, PlanTouch{Addr: sc.Addr, From: sc.MovedFrom, Mode: sc.Mode, Type: sc.Type, Action: sc.Action})
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
	// A plan is written to the backend and not through the store, so the refusal
	// that the store gives every other writer while a seal is in progress is made
	// here: a plan written now could be missed by the seal.
	if ps.st != nil && ps.st.Protection().State == "migrating" {
		return state.ErrSealing
	}
	// A command that opened the store before it was sealed does not seal the plan.
	if ps.st != nil {
		if err := ps.st.CheckCurrent(); err != nil {
			return err
		}
	}
	sl := ps.sealer()
	var id string
	for n := planIDHex; n <= 24; n += 4 {
		id = digestID(data, n)
		stored := data
		if sl != nil {
			var err error
			if stored, err = sealPlan(sl, id, data); err != nil {
				return fmt.Errorf("storing the plan: %w", err)
			}
		}
		ok, err := ps.B.PutIfAbsent(ctx, planKey(id), stored)
		// A write of unknown outcome is read like a refusal: the plan that is there
		// is the same plan, if the write was made.
		unknown := errors.Is(err, state.ErrUnknownOutcome)
		if err != nil && !unknown {
			return fmt.Errorf("storing the plan: %w", err)
		}
		if ok {
			break
		}
		old, gerr := ps.B.Get(ctx, planKey(id))
		if gerr != nil {
			if unknown {
				return fmt.Errorf("storing the plan: %w", state.Advise(err))
			}
			return fmt.Errorf("storing the plan: %w", gerr)
		}
		if ps.samePlan(id, old, data) {
			break
		}
		id = ""
	}
	if id == "" {
		return errors.New("storing the plan: no free id")
	}
	meta.ID, meta.Size = id, len(data)
	toStore := meta
	if sl != nil {
		var err error
		if toStore, err = redactMeta(sl, meta); err != nil {
			return fmt.Errorf("storing the plan: %w", err)
		}
	}
	enc, err := indentJSON(toStore)
	if err != nil {
		return err
	}
	if _, err := ps.B.PutIfAbsent(ctx, metaKey2(id), enc); err != nil {
		return fmt.Errorf("storing the plan: %w", state.Advise(err))
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
	m, err := decodeMeta(id, data)
	if err != nil {
		return nil, err
	}
	if err := ps.openMeta(m); err != nil {
		return nil, err
	}
	return m, nil
}

// decodeMeta decodes and checks the summary of a plan. A summary of a sealed state
// is returned as it is stored, with its box: see PlanStore.openMeta.
func decodeMeta(id string, data []byte) (*PlanMeta, error) {
	if len(data) > MaxPlanMetaBytes {
		return nil, tooLarge("summary", id, len(data), MaxPlanMetaBytes)
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
	if len(data) > MaxPlanProgressBytes {
		return nil, tooLarge("progress", id, len(data), MaxPlanProgressBytes)
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
	if len(data) > MaxPlanFileBytes {
		return nil, nil, tooLarge("plan", id, len(data), MaxPlanFileBytes)
	}
	plain, err := ps.openPlan(id, data)
	if err != nil {
		return nil, nil, err
	}
	_, wasSealed := asSealedPlan(data)
	if ps.sealer() != nil && digestID(plain, len(id)-len("plan-")) != id {
		// The id of a plan is the digest of its plan file. In a sealed state only a
		// reader can seal a plan, so a plan that is not what its name says it is was
		// put there by someone else: a plan file in the clear in place of the sealed one.
		if wasSealed {
			clear(plain)
		}
		return nil, nil, fmt.Errorf("the plan %s is not the plan that its name says: %w", id, state.ErrUntrusted)
	}
	pf, err := parsePlanFile(id, plain)
	if wasSealed {
		clear(plain)
	}
	if err != nil {
		return nil, nil, err
	}
	pf.Unsealed = !wasSealed && ps.sealer() != nil
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
				return state.Advise(err)
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
			return state.Advise(err)
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
		if p.Collecting.holds(now) {
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

// applyRenewEvery is how often a running apply renews its claim: a claim that is not renewed
// lapses after applyingKeep, and an apply may run for longer than that.
var applyRenewEvery = applyingKeep / 48

// RenewApply moves the time of the claim of the apply of owner to now, so that an apply that
// runs for a long time stays in use. It does nothing when the claim is gone.
func (ps *PlanStore) RenewApply(ctx context.Context, id, owner string) {
	now := ps.now()
	_ = ps.updateProgress(ctx, id, func(p *PlanProgress) error {
		for i := range p.Applying {
			if p.Applying[i].By == owner {
				p.Applying[i].At = now
				return nil
			}
		}
		return errSkip
	})
}

// KeepApply renews the claim of the apply of owner while the apply runs, until the function
// that it returns is called.
func (ps *PlanStore) KeepApply(ctx context.Context, id, owner string) (stop func()) {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		t := time.NewTicker(applyRenewEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				ps.RenewApply(ctx, id, owner)
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }); <-finished }
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

// cachedMeta is Meta through the cache of the store, if it has one.
func (ps *PlanStore) cachedMeta(ctx context.Context, id string) (*PlanMeta, error) {
	if ps.Cache == nil {
		return ps.Meta(ctx, id)
	}
	if r, ok := ps.Cache.get(id); ok {
		return r.meta, r.err
	}
	data, err := ps.B.Get(ctx, metaKey2(id))
	if errors.Is(err, state.ErrNotFound) {
		return nil, fmt.Errorf("%w: %s", ErrNoPlan, id)
	}
	if err != nil {
		return nil, err // not kept: the next listing asks again
	}
	m, err := decodeMeta(id, data)
	if err == nil {
		err = ps.openMeta(m)
	}
	if err != nil {
		m = nil
	}
	ps.Cache.put(id, metaResult{m, err})
	return m, err
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
			r.meta, r.metaErr = ps.cachedMeta(ctx, id)
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

// ErrForeignPlan is why a listing cannot read a plan object that is not what its
// name says it is and has no summary: it was planted or damaged.
var ErrForeignPlan = errors.New("the plan file is not the plan that its name says (its digest is not the id) and has no summary; pravo plan gc removes it")

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
	l, err := ps.List(ctx, check)
	if err != nil {
		return nil, nil, err
	}
	return l.Entries, l.Skipped, nil
}

// HiddenPlan is a plan that a listing does not show because a collector holds a
// claim on it, and until when the claim holds.
type HiddenPlan struct {
	ID    string
	Until time.Time
}

// PlanListing is what a listing of the stored plans found.
type PlanListing struct {
	// Entries are the plans that are listed, newest first.
	Entries []*PlanEntry
	// Skipped are the plans that could not be read.
	Skipped []SkippedPlan
	// Hidden are the plans that a collector is removing.
	Hidden []HiddenPlan
	// Unsealed are the plans of a sealed state that were stored before it was
	// sealed, and are as readable as the state was then: pravo protect rewrap --all
	// seals them once nothing uses them.
	Unsealed []string
	// At is the time that the listing was judged at.
	At time.Time
}

// NextLapse is the first moment after the listing at which a claim lapses: the
// claim of a collector on a hidden plan, which then shows again, or a claim of an
// apply or a collector on a listed one, which then no longer counts. The clock
// alone changes what a listing shows at that moment, and nothing is written. It
// is the zero time when no claim is held.
func (l *PlanListing) NextLapse() time.Time {
	var next time.Time
	for _, h := range l.Hidden {
		if h.Until.After(l.At) && (next.IsZero() || h.Until.Before(next)) {
			next = h.Until
		}
	}
	for _, e := range l.Entries {
		if t := e.Progress.NextLapse(l.At); !t.IsZero() && (next.IsZero() || t.Before(next)) {
			next = t
		}
	}
	return next
}

// NextLapse is the first moment after now at which a claim in the progress
// lapses, or the zero time when none is held.
func (p *PlanProgress) NextLapse(now time.Time) time.Time {
	var next time.Time
	consider := func(t time.Time) {
		if t.After(now) && (next.IsZero() || t.Before(next)) {
			next = t
		}
	}
	if p.Collecting != nil && !p.Collecting.Committed {
		consider(p.Collecting.At.Add(claimTimeout))
	}
	for _, c := range p.Applying {
		consider(c.At.Add(applyingKeep))
	}
	return next
}

// List reads every stored plan as Entries does, and says which plans it could
// not read and which a collector's claim hides.
func (ps *PlanStore) List(ctx context.Context, check *PlanCheck) (*PlanListing, error) {
	all, err := ps.ids(ctx)
	if err != nil {
		return nil, err
	}
	if ps.Cache != nil {
		ps.Cache.retain(all)
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
	l := &PlanListing{At: now}
	for _, id := range whole {
		r := reads[id]
		if r.metaErr != nil || r.progErr != nil {
			l.Skipped = append(l.Skipped, SkippedPlan{ID: id, Err: errors.Join(r.metaErr, r.progErr)})
			continue
		}
		if r.progress.Collecting.holds(now) {
			h := HiddenPlan{ID: id}
			if !r.progress.Collecting.Committed {
				h.Until = r.progress.Collecting.At.Add(claimTimeout)
			}
			l.Hidden = append(l.Hidden, h)
			continue
		}
		l.Entries = append(l.Entries, newPlanEntry(r.meta, r.progress, check, now))
		if r.meta.Unsealed {
			l.Unsealed = append(l.Unsealed, id)
		}
	}
	// A plan object that is not the plan its name says it is, and has no summary,
	// is not a write that did not finish: nobody's write makes one.
	var foreign []string
	for id, kinds := range all {
		if kinds["plan"] && !kinds["meta"] && ps.foreignPlan(ctx, id) {
			foreign = append(foreign, id)
		}
	}
	sort.Strings(foreign)
	for _, id := range foreign {
		l.Skipped = append(l.Skipped, SkippedPlan{ID: id, Err: ErrForeignPlan})
	}
	out := l.Entries
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Meta.Created.Equal(out[j].Meta.Created) {
			return out[i].Meta.Created.After(out[j].Meta.Created)
		}
		return out[i].Meta.ID < out[j].Meta.ID
	})
	return l, nil
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
	// Changes are the resources that the other plan changes and this plan reads: applying the other makes this plan stale.
	Changes []string
	// ReadBy are the resources that this plan changes and the other plan reads: applying this makes the other stale.
	ReadBy []string
}

// touchedBy returns the addresses that the plan of e still changes: those of the
// summary that were not applied, once each.
func (e *PlanEntry) touchedBy() []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range e.Meta.Changes {
		if _, done := e.Progress.Applied[t.Addr]; done {
			continue
		}
		// a move changes the resource that it takes the record from, too
		for _, a := range []string{t.Addr, t.From} {
			if a != "" && !seen[a] {
				seen[a] = true
				out = append(out, a)
			}
		}
	}
	return out
}

// readsOf returns the addresses of the records that the plan of e reads and does not write, and that it has
// not applied.
func (e *PlanEntry) readsOf() []string {
	var out []string
	for a, b := range e.Meta.Bases {
		if _, done := e.Progress.Applied[a]; b.Reads && !done {
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
}

// OverlapsOf is Overlaps for a plan that is known by its summary, and also finds the plans that read what this
// plan changes, and change what it reads: a move takes its record from an address, which counts as changed, and a
// plan that reads a record is made stale by the plan that changes it, though it changes nothing there itself.
func OverlapsOf(entries []*PlanEntry, meta *PlanMeta) []Overlap {
	writes, reads := map[string]bool{}, map[string]bool{}
	for _, t := range meta.Changes {
		writes[t.Addr] = true
		if t.From != "" {
			writes[t.From] = true
		}
	}
	for a, b := range meta.Bases {
		if b.Reads && !writes[a] {
			reads[a] = true
		}
	}
	var out []Overlap
	for _, e := range entries {
		if e.Meta.ID == meta.ID || !e.Status.Open() {
			continue
		}
		var o Overlap
		for _, a := range e.changing() {
			switch {
			case writes[a]:
				o.Addrs = append(o.Addrs, a)
			case reads[a]:
				o.Changes = append(o.Changes, a)
			}
		}
		for _, a := range e.readsOf() {
			if writes[a] {
				o.ReadBy = append(o.ReadBy, a)
			}
		}
		if len(o.Addrs)+len(o.Changes)+len(o.ReadBy) > 0 {
			sort.Strings(o.Addrs)
			sort.Strings(o.Changes)
			sort.Strings(o.ReadBy)
			o.Plan = e.Meta
			out = append(out, o)
		}
	}
	return out
}

// BeingCollected reports whether a collector holds a claim on the plan at now: it cannot be applied, and it is about
// to be removed.
func (p *PlanProgress) BeingCollected(now time.Time) bool {
	return p.Collecting.holds(now)
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
		if t.From != "" {
			mine[t.From] = true
		}
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
	if len(e.Opts.Args) > 0 {
		meta.shownCommand = e.Opts.Command
	}
	if err := NewPlanStoreFor(e.State).Put(ctx, data, meta); err != nil {
		return nil, err
	}
	return meta, nil
}

// SummaryFile builds a plan file from the summary of a stored plan: the address,
// the type and the action of each change, and which of them were applied. It
// holds no value. A reader that cannot open the plan lays this over a graph.
func (e *PlanEntry) SummaryFile() *PlanFile {
	pf := &PlanFile{Created: e.Meta.Created, Actor: e.Meta.Author, Destroy: e.Meta.Destroy}
	for _, t := range e.Meta.Changes {
		done := false
		if e.Progress != nil {
			_, done = e.Progress.Applied[t.Addr]
		}
		pf.Changes = append(pf.Changes, savedChange{Addr: t.Addr, MovedFrom: t.From, Mode: t.Mode, Type: t.Type, Action: t.Action, Applied: done})
	}
	return pf
}
