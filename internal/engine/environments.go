package engine

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// Environments. A root that declares $environments is applied to one of them at a time, and
// its state remembers which (state/environment.go). An apply checks the record of the state
// against the specification, and refuses a run for another environment, or a specification
// that changed what the environment follows. In an environment that follows another ($after),
// an apply takes only a release that was promoted into it: a promotion in this state, signed
// by a writer of it, that carries the signed note of every environment it follows, saying that
// it runs this specification with these tags. The notes are checked here against the readers
// file of the environment they come from, without opening its state. After a complete apply the
// state writes a note of its own: what the environments after it check.

// EnvironmentNote is what plan and apply report of the environment: the gate and how the
// release stands against it.
type EnvironmentNote struct {
	// Environment is the environment the run is for, and After those it follows.
	Environment string
	After       []string
	// Gate is set when the environment follows another and its state is sealed.
	Gate *GateCheck
	// Unsealed is set when the environment follows another and its state is not sealed, so
	// that nothing holds it to what the one before runs.
	Unsealed bool
}

// GateCheck is how the release of a run stands against the promotions this state holds.
type GateCheck struct {
	// Met is set when a promotion counts; Admission then says which.
	Met       bool
	Admission *state.Admission
	// Reason says, when it is not met, what is missing.
	Reason string
}

// Release is a specification and the digests of the tags it is run with: what a promotion lets
// in and a note says a state runs.
type Release struct {
	Fingerprint string
	Tags        map[string]string
}

// ReleaseOf is the release of a root.
func ReleaseOf(root *config.Root) (Release, error) {
	fp, err := root.Fingerprint()
	if err != nil {
		return Release{}, err
	}
	return Release{Fingerprint: fp, Tags: state.SpecTagDigests(root.Tags)}, nil
}

// Same reports whether the note names this release.
func (r Release) Same(n *state.RunNote) bool {
	return n != nil && n.Fingerprint == r.Fingerprint && maps.Equal(n.Tags, r.Tags)
}

// Differs says why the note does not name this release, or "" when it does.
func (r Release) Differs(n *state.RunNote) string {
	switch {
	case n == nil:
		return "nothing has been applied there yet"
	case n.Fingerprint != r.Fingerprint:
		return fmt.Sprintf("it runs %s, and this directory is %s", ShortID(n.Fingerprint), ShortID(r.Fingerprint))
	}
	if diff := state.TagsDiffer(n.Tags, r.Tags); len(diff) > 0 {
		return "it runs this specification with other tags: " + strings.Join(diff, "; ")
	}
	return ""
}

// ShortID is the first twelve characters of a fingerprint.
func ShortID(fp string) string {
	if len(fp) > 12 {
		return fp[:12]
	}
	return fp
}

// RecordProblem compares the record of the environment that a state holds with the
// specification, and says what differs, or "" when nothing does. A state with no record agrees
// with any specification: its first apply writes one.
func RecordProblem(root *config.Root, rec *state.EnvironmentRecord) string {
	if rec == nil || rec.Name == "" {
		return ""
	}
	if root.Environment == "" {
		return fmt.Sprintf("this state serves the environment %s, and the specification declares no environments", rec.Name)
	}
	if rec.Name != root.Environment {
		return fmt.Sprintf("this state serves the environment %s, and this run is for %s: $settings.state of %s points at the state of %s", rec.Name, root.Environment, root.Environment, rec.Name)
	}
	cur, _ := root.Current()
	want, have := slices.Clone(cur.After), rec.AfterNames()
	sort.Strings(want)
	sort.Strings(have)
	if !slices.Equal(want, have) {
		return fmt.Sprintf("the record of this state says that %s follows %s, and the specification says it follows %s", rec.Name, nobody(have), nobody(want))
	}
	return ""
}

func nobody(names []string) string {
	if len(names) == 0 {
		return "no environment"
	}
	return joinAnd(names)
}

func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// RecordCommand is the command an admin runs to make the record say what the specification
// says.
func RecordCommand(root *config.Root) string {
	if root.Environment == "" {
		return "pravo environments record"
	}
	return "pravo environments record --env " + root.Environment
}

// environmentRecord reads the record of the state with the readers of the engine.
func (e *Engine) environmentRecord(ctx context.Context) (*state.EnvironmentRecord, error) {
	rec, err := e.State.EnvironmentRecord(ctx, e.Opts.Readers)
	if err != nil {
		if errors.Is(err, state.ErrUntrusted) {
			return nil, WithCode(ExitUntrusted, err)
		}
		return nil, err
	}
	return rec, nil
}

// checkEnvironment refuses a run whose specification disagrees with the record of the state:
// another environment, or one that follows others than it did. It returns the record.
func (e *Engine) checkEnvironment(ctx context.Context) (*state.EnvironmentRecord, error) {
	if e.State == nil {
		return nil, nil
	}
	rec, err := e.environmentRecord(ctx)
	if err != nil {
		return nil, err
	}
	if p := RecordProblem(e.Root, rec); p != "" {
		return rec, refused("%s.\n\nIf the specification is right, an admin makes the record say so:\n\n    %s", p, RecordCommand(e.Root))
	}
	if p := PinProblem(rec, e.Opts.RolesPin, e.Opts.ReadersFile, e.State.Sealer() != nil); p != "" {
		return rec, refused("%s.\n\nIf the readers file is right, an admin of the roles the record pins makes the record say so:\n\n    %s", p, RecordCommand(e.Root))
	}
	return rec, nil
}

// PinProblem says why the readers file of a sealed state does not do for the record of its
// environment: the record pins the roles of the state, and the file pins other roles or none,
// so that the roles it gives are not the signed ones. It is empty when the file does.
func PinProblem(rec *state.EnvironmentRecord, pin, file string, sealed bool) string {
	if !sealed || rec == nil || rec.Name == "" || rec.RolesPin == "" || rec.RolesPin == pin {
		return ""
	}
	if file == "" {
		file = "the readers file"
	}
	return fmt.Sprintf("the record of this state pins the roles of %s, and %s does not pin those roles, so the roles it gives are not the signed ones in the state", rec.Name, PlainText(file))
}

// gateOf lists the environments the run's environment follows: those of the record, which is
// what an earlier apply accepted, else those of the specification, which the first apply
// records.
func (e *Engine) gateOf(rec *state.EnvironmentRecord) []string {
	if rec != nil && rec.Name != "" {
		return rec.AfterNames()
	}
	cur, ok := e.Root.Current()
	if !ok {
		return nil
	}
	return cur.After
}

// EnvironmentStatus reads how the release of the run stands in its environment, for plan and
// the viewer. It is nil for a root without environments.
func (e *Engine) EnvironmentStatus(ctx context.Context) (*EnvironmentNote, error) {
	if e.Root.Environment == "" || e.State == nil {
		return nil, nil
	}
	rec, err := e.environmentRecord(ctx)
	if err != nil {
		return nil, err
	}
	note := &EnvironmentNote{Environment: e.Root.Environment, After: e.gateOf(rec)}
	if len(note.After) == 0 {
		return note, nil
	}
	if e.State.Sealer() == nil {
		note.Unsealed = true
		return note, nil
	}
	rel, err := ReleaseOf(e.Root)
	if err != nil {
		return nil, err
	}
	c := e.CheckGate(ctx, rec, rel)
	note.Gate = &c
	return note, nil
}

// upstreamReaders are the readers that the note of the environment env is checked against.
func (e *Engine) upstreamReaders(env string) ([]protect.Reader, error) {
	if e.Opts.UpstreamReaders == nil {
		return e.Opts.Readers, nil
	}
	return e.Opts.UpstreamReaders(env)
}

// gateReader judges the promotions of the state of the run.
func (e *Engine) gateReader() GateReader {
	return GateReader{State: e.State, Environment: e.Root.Environment, Readers: e.Opts.Readers, Upstream: e.upstreamReaders, Now: e.clock()}
}

// CheckGate reads the promotions of the release in this state and says whether one counts.
func (e *Engine) CheckGate(ctx context.Context, rec *state.EnvironmentRecord, rel Release) GateCheck {
	return e.gateReader().Check(ctx, e.gateOf(rec), rec, rel)
}

// GateReader judges the promotions that the state of one environment holds. Plan and apply
// use it for the state of the run, and "pravo environments" for the state of each environment.
type GateReader struct {
	State *state.Store
	// Environment is the environment the state serves, and Readers the readers that decide
	// there.
	Environment string
	Readers     []protect.Reader
	// Upstream gives the readers of an environment this one follows, which its notes are
	// checked against. Nil uses Readers.
	Upstream func(env string) ([]protect.Reader, error)
	Now      time.Time
}

func (g GateReader) upstream(env string) ([]protect.Reader, error) {
	if g.Upstream == nil {
		return g.Readers, nil
	}
	return g.Upstream(env)
}

func (g GateReader) now() time.Time {
	if g.Now.IsZero() {
		return time.Now()
	}
	return g.Now
}

// Withdrawal reads the newest withdrawal of the release by an admin of the state, and says
// who made it, how long ago and why. ok is false where there is none.
func (g GateReader) Withdrawal(ctx context.Context, rel Release) (by string, ago time.Duration, why string, ok bool) {
	all, err := g.State.Promotions(ctx, rel.Fingerprint)
	if err != nil {
		return "", 0, "", false
	}
	var at time.Time
	for _, o := range all {
		p := o.Promotion
		if p == nil || p.Kind != state.PromotionRevoked || p.Fingerprint != rel.Fingerprint || !maps.Equal(p.Tags, rel.Tags) || p.Environment != g.Environment {
			continue
		}
		if st, who := p.Verify(g.State.Lineage(), g.Readers); st == protect.SigVerified {
			if role, _ := protect.RoleOf(g.Readers, p.Signature.Key); role.Allows(protect.RoleAdmin) && p.At.After(at) {
				at, by, why = p.At, who, p.Note
			}
		}
	}
	if at.IsZero() {
		return "", 0, "", false
	}
	return PlainText(by), g.now().Sub(at), PlainText(why), true
}

// Check reads the promotions of the release and says whether one counts. after lists the
// environments this one follows. A state that cannot be read is a reason, not an error: the
// apply is refused and says why.
func (g GateReader) Check(ctx context.Context, after []string, rec *state.EnvironmentRecord, rel Release) GateCheck {
	var c GateCheck
	all, err := g.State.Promotions(ctx, rel.Fingerprint)
	if err != nil {
		c.Reason = "the promotions of this state cannot be read: " + err.Error()
		return c
	}
	lineage := g.State.Lineage()
	// A withdrawal by an admin ends every promotion of the release signed before it.
	var revokedAt time.Time
	var revokedBy, revokedWhy string
	var mine []*state.Promotion
	for _, o := range all {
		p := o.Promotion
		if p == nil || p.Fingerprint != rel.Fingerprint || !maps.Equal(p.Tags, rel.Tags) || p.Environment != g.Environment {
			continue
		}
		if p.Kind == state.PromotionRevoked {
			if st, who := p.Verify(lineage, g.Readers); st == protect.SigVerified {
				if role, _ := protect.RoleOf(g.Readers, p.Signature.Key); role.Allows(protect.RoleAdmin) && p.At.After(revokedAt) {
					revokedAt, revokedBy, revokedWhy = p.At, who, p.Note
				}
			}
			continue
		}
		mine = append(mine, p)
	}
	withdrawn := func() string {
		return fmt.Sprintf("its promotion was withdrawn by %s %s ago: %s", PlainText(revokedBy), RoundAge(g.now().Sub(revokedAt)), PlainText(revokedWhy))
	}
	if len(mine) == 0 {
		c.Reason = "nobody has promoted this release yet"
		if !revokedAt.IsZero() {
			c.Reason = withdrawn()
		}
		return c
	}
	sort.Slice(mine, func(i, j int) bool { return mine[i].At.After(mine[j].At) })
	best := -1
	for _, p := range mine {
		reason, level := g.judge(after, rec, p, rel)
		if reason == "" && !revokedAt.IsZero() && !p.At.After(revokedAt) {
			reason, level = withdrawn(), 9
		}
		if reason == "" {
			who := p.Signature.Signer
			if _, name := p.Verify(lineage, g.Readers); name != "" {
				who = name
			}
			c.Met, c.Reason = true, ""
			c.Admission = &state.Admission{Kind: p.Kind, By: who, Key: p.Signature.Key, At: p.At, Note: p.Note}
			return c
		}
		if level > best {
			best, c.Reason = level, reason
		}
	}
	return c
}

// judge says why a promotion does not count, or "" when it does, with how far it got.
func (g GateReader) judge(after []string, rec *state.EnvironmentRecord, p *state.Promotion, rel Release) (string, int) {
	st, who := p.Verify(g.State.Lineage(), g.Readers)
	if who == "" {
		who = p.Signature.Signer
	}
	who = PlainText(who)
	switch st {
	case protect.SigVerified:
	case protect.SigUnknownKey:
		return fmt.Sprintf("the promotion by %s does not count: its key is not in the readers file", who), 1
	default:
		return fmt.Sprintf("the promotion by %s does not verify in this state", who), 0
	}
	role, _ := protect.RoleOf(g.Readers, p.Signature.Key)
	switch {
	case p.Kind == state.PromotionWaived && !role.Allows(protect.RoleAdmin):
		return fmt.Sprintf("the waiver by %s does not count: %s is %s here, and an admin is needed", who, who, article(role)), 2
	case !role.Allows(protect.RoleWriter):
		return fmt.Sprintf("the promotion by %s does not count: %s is %s here, and a writer is needed", who, who, article(role)), 2
	}
	if p.Kind == state.PromotionWaived {
		return "", 10
	}
	for i, up := range after {
		var src *state.PromotionSource
		for j := range p.From {
			if p.From[j].Environment == up {
				src = &p.From[j]
			}
		}
		if src == nil || src.Run == nil {
			return fmt.Sprintf("the promotion by %s names no note of %s", who, up), 3
		}
		if rec != nil {
			if want := rec.Lineage(up); want != "" && src.Lineage != want {
				return fmt.Sprintf("the promotion by %s read %s from another state than the one this environment follows", who, up), 3
			}
		}
		readers, err := g.upstream(up)
		if err != nil {
			return fmt.Sprintf("the readers of %s cannot be read here: %v", up, err), 4 + i
		}
		if why := NoteProblem(src.Run, up, src.Lineage, readers, g.now()); why != "" {
			return why, 4
		}
		if why := rel.Differs(src.Run); why != "" {
			return fmt.Sprintf("%s did not run this release when it was promoted: %s", up, why), 5
		}
	}
	return "", 10
}

// NoteProblem says why the note n does not show what the state of the environment env, with
// the lineage, runs, or "" when it does: it is signed there by a writer and is not dated
// ahead of now.
func NoteProblem(n *state.RunNote, env, lineage string, readers []protect.Reader, now time.Time) string {
	nst, nwho := n.Verify(lineage, readers)
	switch nst {
	case protect.SigVerified:
	case protect.SigUnknownKey:
		if nwho == "" && n.Signature != nil {
			nwho = n.Signature.Signer
		}
		return fmt.Sprintf("the note of %s was signed by %s, whose key is not in the readers file of %s", env, PlainText(nwho), env)
	case protect.SigUnsigned:
		return fmt.Sprintf("the note of %s is not signed", env)
	default:
		return fmt.Sprintf("the note of %s does not verify against the readers file of %s", env, env)
	}
	if r, _ := protect.RoleOf(readers, n.Signature.Key); !r.Allows(protect.RoleWriter) {
		return fmt.Sprintf("the note of %s was signed by %s, who is %s there, and a writer is needed", env, PlainText(nwho), article(r))
	}
	if n.Environment != env {
		return fmt.Sprintf("the note of %s was written in %s", env, PlainText(n.Environment))
	}
	if n.At.After(now.Add(state.FutureSkew)) {
		return fmt.Sprintf("the note of %s is dated in the future (%s)", env, n.At.Format(time.RFC3339))
	}
	return ""
}

func article(r protect.Role) string {
	switch r {
	case protect.RoleAdmin:
		return "an admin"
	case protect.RoleNone:
		return "nobody"
	}
	return "a " + r.String()
}

// unchanging is a plan that changes nothing, in the infrastructure or in the state, and defers
// nothing to the next plan. An environment that follows another takes it without a promotion,
// since there is nothing to hold back, and it does not note that it runs the release.
func unchanging(p *Plan) bool {
	return p != nil && !p.NeedsApply() && p.DeferredCount() == 0
}

// admit checks the environment before the apply of plan: the record of the state, and the
// gate of an environment that follows another. It returns the promotion that let the release
// in, nil where there is no gate, and whether the state may note that it runs the release:
// not when the gate is shut and the plan changes nothing, which is let through.
func (e *Engine) admit(ctx context.Context, plan *Plan) (*state.Admission, bool, error) {
	if e.State == nil {
		return nil, true, nil
	}
	rec, err := e.checkEnvironment(ctx)
	if err != nil || e.Root.Environment == "" || e.Opts.Destroy {
		return nil, err == nil, err
	}
	gate := e.gateOf(rec)
	if len(gate) == 0 {
		return nil, true, nil
	}
	if e.State.Sealer() == nil {
		return nil, true, nil // plan says that nothing holds it
	}
	rel, err := ReleaseOf(e.Root)
	if err != nil {
		return nil, false, err
	}
	c := e.CheckGate(ctx, rec, rel)
	if !c.Met && unchanging(plan) {
		return nil, false, nil
	}
	if !c.Met {
		return nil, false, refused("%s only takes what was promoted from %s, and %s.\n\nRun these, skipping a step that is already done:\n\n%s",
			e.Root.Environment, joinAnd(gate), c.Reason, Steps(e.Root, e.Root.Environment))
	}
	return c.Admission, true, nil
}

// preflightRecord runs before an apply that changes something, in an environment whose state
// has no record yet or a record from before the pin of the roles: the apply writes the record
// at its end, and that raises the format of the state, which only an admin may do in a bucket
// with a policy by role. Raising it first means a writer is refused before anything is created,
// with the command an admin runs, and not after.
func (e *Engine) preflightRecord(ctx context.Context) error {
	if e.State == nil || e.Root.Environment == "" || e.Opts.Destroy || e.State.Sealer() == nil {
		return nil
	}
	rec, err := e.environmentRecord(ctx)
	if err != nil {
		return err
	}
	pin := e.Opts.RolesPin
	if rec != nil && (rec.Name == "" || rec.RolesPin != "" || pin == "") {
		return nil
	}
	if err := e.State.EnsureEnvironmentFormat(pin != ""); err != nil {
		return refused("this state does not yet record that it serves %s, and your key cannot write that (%v).\n\nNothing was changed. An admin records it once:\n\n    %s", e.Root.Environment, err, RecordCommand(e.Root))
	}
	return nil
}

func unsealedLine(env string, gate []string) string {
	return fmt.Sprintf("Environment: %s follows %s, and its state is not sealed, so it takes any release. Seal it to hold it to what %s runs: pravo protect init --encrypt --env %s", env, joinAnd(gate), joinAnd(gate), env)
}

// CheckAdmission refuses, before an apply asks for a yes, a run of plan that the apply would
// refuse for its environment: a state that serves another, or a release that was not promoted.
func (e *Engine) CheckAdmission(ctx context.Context, plan *Plan) error {
	_, _, err := e.admit(ctx, plan)
	return err
}

// EnvironmentLine is the line that plan prints about the environment of the run: whether
// the release was promoted into it. It is empty where there is nothing to say, as for a
// plan that changes nothing in an environment that the release was not promoted into.
func (e *Engine) EnvironmentLine(ctx context.Context, plan *Plan) (string, error) {
	n, err := e.EnvironmentStatus(ctx)
	if err != nil || n == nil {
		return "", err
	}
	if n.Gate != nil && !n.Gate.Met && unchanging(plan) {
		return "", nil
	}
	return n.Line(e.Root), nil
}

// Line is the status as plan reports it.
func (n *EnvironmentNote) Line(root *config.Root) string {
	switch {
	case n == nil || len(n.After) == 0:
		return ""
	case n.Unsealed:
		return unsealedLine(n.Environment, n.After)
	case n.Gate.Met && n.Gate.Admission.Kind == state.PromotionWaived:
		a := n.Gate.Admission
		return fmt.Sprintf("Promotion: waived by %s %s ago, without a check of %s: %s", PlainText(a.By), RoundAge(time.Since(a.At)), joinAnd(n.After), PlainText(a.Note))
	case n.Gate.Met:
		a := n.Gate.Admission
		return fmt.Sprintf("Promotion: %s ran this release, and %s promoted it %s ago.", joinAnd(n.After), PlainText(a.By), RoundAge(time.Since(a.At)))
	}
	return fmt.Sprintf("Promotion: none yet. %s only takes what was promoted from %s, and %s. An apply is refused until these run, skipping a step that is already done:\n%s",
		n.Environment, joinAnd(n.After), n.Gate.Reason, Steps(root, n.Environment))
}

// noteRun writes, after a complete apply, the record of the environment if the state has none
// and a note of what the state now runs. txn is the transaction of the apply, empty for one that
// found nothing to change; such an apply writes a note only when the newest one names another
// release. deferred is the number of changes the plan deferred to the next plan: the note says
// that the state runs the release in part, and promote refuses to take it further.
func (e *Engine) noteRun(ctx context.Context, admitted *state.Admission, txn string, deferred int) error {
	if e.Root.Environment == "" || e.Opts.Destroy {
		return nil
	}
	rec, err := e.environmentRecord(ctx)
	if err != nil {
		return err
	}
	pin := ""
	if e.State.Sealer() != nil {
		pin = e.Opts.RolesPin
	}
	switch {
	case rec == nil:
		cur, _ := e.Root.Current()
		next := state.EnvironmentRecord{Name: cur.Name, RolesPin: pin}
		for _, a := range cur.After {
			next.After = append(next.After, state.Upstream{Name: a})
		}
		if err := e.State.PutEnvironmentRecord(ctx, 1, next); err != nil && !errors.Is(err, state.ErrEnvironmentChanged) {
			return fmt.Errorf("recording that this state serves %s: %w", cur.Name, err)
		}
	case rec.Name != "" && rec.RolesPin == "" && pin != "":
		// A record from before the pin takes the roles that the readers file pins and the
		// state holds, so that from now on another readers file does not give other roles.
		next := *rec
		next.RolesPin, next.At, next.Signature = pin, time.Time{}, nil
		if err := e.State.PutEnvironmentRecord(ctx, rec.Seq+1, next); err != nil && !errors.Is(err, state.ErrEnvironmentChanged) {
			return fmt.Errorf("recording the roles of %s: %w", rec.Name, err)
		}
	}
	rel, err := ReleaseOf(e.Root)
	if err != nil {
		return err
	}
	if txn == "" {
		last, _, err := e.State.LatestRun(ctx, e.clock())
		if err == nil && rel.Same(last) && last.GitRevision == e.Opts.GitRevision && last.Deferred == deferred {
			if st, _ := last.Verify(e.State.Lineage(), e.Opts.Readers); st == protect.SigVerified || last.Signature == nil && e.State.Sealer() == nil {
				return nil
			}
		}
	}
	n := &state.RunNote{Environment: e.Root.Environment, Fingerprint: rel.Fingerprint, Tags: rel.Tags, GitRevision: e.Opts.GitRevision, Txn: txn, Admitted: admitted, Deferred: deferred}
	if err := e.State.PutRunNote(ctx, n); err != nil {
		if errors.Is(err, state.ErrNotReader) {
			return nil // a reader plans in memory and notes nothing
		}
		return fmt.Errorf("noting what %s runs: %w", e.Root.Environment, err)
	}
	switch {
	case deferred > 0:
		e.Opts.Progress("Noted that %s runs this release in part: %s deferred to the next plan.", e.Root.Environment, countOf(deferred, "change is", "changes are"))
	case txn == "":
		e.Opts.Progress("Noted that %s runs this release.", e.Root.Environment)
	}
	return nil
}

// RoundAge is an age for a sentence: "2 hours".
func RoundAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < 2*time.Minute:
		return "1 minute"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d < 2*time.Hour:
		return "1 hour"
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}

// TagFlags writes tags as the flags that give them: --env prod -t image=1.4. The selector
// of an environment comes first, as --env.
func TagFlags(tags []string) string {
	var parts []string
	if name, ok := config.EnvironmentOf(tags); ok {
		parts = append(parts, "--env "+name)
	}
	for _, t := range config.WithoutEnvironment(tags) {
		parts = append(parts, "-t "+t)
	}
	return strings.Join(parts, " ")
}

// PromoteCommand is the command that promotes the release of the root into env.
func PromoteCommand(root *config.Root, env string) string {
	return strings.TrimSpace("pravo promote " + env + " " + TagFlags(root.Tags))
}

// ApplyCommand is the command that applies the release of the root in env.
func ApplyCommand(root *config.Root, env string) string {
	return "pravo apply " + TagFlags(append([]string{config.EnvTag + "=" + env}, config.WithoutEnvironment(root.Tags)...))
}

// Steps are the commands that bring the release of the root to the environment to, in the
// order of the promotion flow: apply it in each first environment that leads there, then promote it
// into each one after and apply it there. The last step is the promotion into to.
func Steps(root *config.Root, to string) string {
	need := map[string]bool{to: true}
	for i := len(root.Environments) - 1; i >= 0; i-- {
		env := root.Environments[i]
		if need[env.Name] {
			for _, a := range env.After {
				need[a] = true
			}
		}
	}
	var lines []string
	for _, env := range root.Environments {
		if !need[env.Name] {
			continue
		}
		if len(env.After) > 0 {
			lines = append(lines, "    "+PromoteCommand(root, env.Name))
		}
		if env.Name != to {
			lines = append(lines, "    "+ApplyCommand(root, env.Name))
		}
	}
	return strings.Join(lines, "\n")
}

// countOf is n with the noun that fits it: "1 change is", "2 changes are".
func countOf(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
