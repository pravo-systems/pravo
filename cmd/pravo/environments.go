package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/ui"
	"github.com/pravo-systems/pravo/pravo"
)

// environmentsOf reads the settings of the environments of a root, once.
type environmentsOf struct {
	root *config.Root
	once sync.Once
	list []config.EnvironmentSettings
	err  error
}

func (e *environmentsOf) all() ([]config.EnvironmentSettings, error) {
	e.once.Do(func() {
		e.list, e.err = config.ReadEnvironmentSettings(e.root.Dir, config.LoadOptions{Tags: e.root.Tags, SysTags: flags.SysTags, Env: environment()})
	})
	return e.list, e.err
}

// settings are those of the environment name, or why they cannot be read here.
func (e *environmentsOf) settings(name string) (config.EnvironmentSettings, error) {
	all, err := e.all()
	if err != nil {
		return config.EnvironmentSettings{}, err
	}
	for _, es := range all {
		if es.Name != name {
			continue
		}
		if es.Err != nil {
			return es, fmt.Errorf("the settings of %s cannot be read here: %w", name, es.Err)
		}
		return es, nil
	}
	return config.EnvironmentSettings{}, fmt.Errorf("%s is not an environment of this specification", name)
}

// readersFile is the readers file of the environment name. The environment of the run reads
// the file the run reads (--access-dir, $PRAVO_ACCESS_DIR, then its settings); another reads
// the one its own settings name, else the one found by default.
func (e *environmentsOf) readersFile(name string) (readersFile, error) {
	rf := readersOf(e.root)
	if name != e.root.Environment {
		es, err := e.settings(name)
		if err != nil {
			return readersFile{}, err
		}
		rf = readersOfEnvironment(e.root, es)
	}
	switch {
	case !rf.Found:
		return rf, fmt.Errorf("no readers file was found for %s (looked in %s)", name, rf.shownDir())
	case rf.Err != nil:
		return rf, fmt.Errorf("%s: %s", rf.shown(), rf.problem())
	}
	return rf, nil
}

// readers are the readers that the notes of the environment name are checked against: those of
// its readers file, with the roles that its state holds when the file pins them. A sealed state
// whose record pins its roles is refused when the file does not pin those roles, so that an
// edit of its access directory does not make anyone a writer there.
func (e *environmentsOf) readers(name string) ([]protect.Reader, error) {
	s := e.open(context.Background(), name)
	defer s.close()
	switch {
	case s.Err != nil:
		return nil, fmt.Errorf("%s: %w", name, s.Err)
	case s.Sealed:
		return s.rf.Readers, nil
	}
	rf, err := e.readersFile(name)
	if err != nil {
		return nil, err
	}
	return rf.Readers, nil
}

// rootFor is a root that names the state of the environment es, enough to open it.
func (e *environmentsOf) rootFor(es config.EnvironmentSettings) *config.Root {
	if es.Name == e.root.Environment {
		return e.root
	}
	return &config.Root{Dir: e.root.Dir, State: es.State, StatePath: es.StatePath, AccessDir: es.AccessDir,
		Environments: e.root.Environments, Environment: es.Name, Tags: e.root.Tags}
}

// envState is what can be read of the state of one environment without a key: where it is,
// the record of the environment, the newest note of what it runs and whether that note
// verifies.
type envState struct {
	es config.EnvironmentSettings
	st *state.Store
	rf readersFile
	// Err says why the state cannot be read here; Missing that there is none yet.
	Err     error
	Missing bool
	Sealed  bool
	Record  *state.EnvironmentRecord
	Note    *state.RunNote
	// NoteBy is who signed the note; NoteProblem says why it does not show what the state
	// runs.
	NoteBy      string
	NoteProblem string
	// Destroyed is set when the newest transaction is a complete destroy that came after
	// the note: the note says what the state ran before it was destroyed.
	Destroyed bool
}

func (s *envState) close() {
	if s.st != nil {
		s.st.Close()
	}
}

// openEnvironment opens the state of the environment name for reading, without a key.
func (e *environmentsOf) open(ctx context.Context, name string) *envState {
	s := &envState{}
	es, err := e.settings(name)
	s.es = es
	if err != nil {
		s.Err = err
		return s
	}
	root := e.rootFor(es)
	if stateMissing(ctx, root) {
		s.Missing = true
		return s
	}
	st, err := pravo.OpenStore(ctx, root, pravo.StoreOptions{Env: environment()})
	if err != nil {
		s.Err = err
		return s
	}
	s.st = st
	s.Sealed = st.Sealer() != nil
	pin := ""
	if s.Sealed {
		rf, err := e.readersFile(name)
		var rs state.RolesStatus
		if err == nil {
			rf, rs, err = rf.withRoles(st)
		}
		if rs.Source == "state" {
			pin = rf.Pin
		}
		if err != nil {
			s.Err = err
			return s
		}
		s.rf = rf
	}
	if s.Record, err = st.EnvironmentRecord(ctx, s.rf.Readers); err != nil {
		s.Err = err
		return s
	}
	if s.Sealed {
		if p := engine.PinProblem(s.Record, pin, s.rf.shown(), true); p != "" {
			s.Err = fmt.Errorf("%w: %s", state.ErrUntrusted, p)
			return s
		}
	}
	note, _, err := st.LatestRun(ctx, time.Now())
	if err != nil {
		s.Err = err
		return s
	}
	s.Note = note
	if note != nil && s.Sealed {
		s.NoteProblem = engine.NoteProblem(note, name, st.Lineage(), s.rf.Readers, time.Now())
		if _, who := note.Verify(st.Lineage(), s.rf.Readers); who != "" {
			s.NoteBy = who
		}
	}
	if note != nil {
		if t, err := st.LatestTransaction(); err == nil && t != nil && t.Kind == "destroy" && t.Status == "applied" && t.Started.After(note.At) {
			s.Destroyed = true
		}
	}
	return s
}

// after is what the environment follows: what its record says, else what the specification
// says.
func (s *envState) after() []string {
	if s.Record != nil && s.Record.Name != "" {
		return s.Record.AfterNames()
	}
	return s.es.After
}

// firstLine is the first line of an error, made printable.
func firstLine(err error) string {
	line, _, _ := strings.Cut(err.Error(), "\n")
	return printable(line)
}

// ---------- promote ----------

type promoteOpts struct {
	note, waive, revoke, refresh, ttl  string
	signOnly, autoApprove, full, store bool
}

func promoteCmd() *cobra.Command {
	var o promoteOpts
	cmd := &cobra.Command{
		Use:   "promote <environment>",
		Short: "Let the release that the environments before one run into it",
		Long: `An environment whose row in $environments names others in $after takes only a release that
was promoted into it. promote reads the newest note of each environment it follows, which needs no
key and no provider: the note says which specification and tags the state there runs, and is signed
there by whoever applied it. When every one runs the release of this directory, promote signs a
promotion that carries those notes into the state of the environment, and then applies it there as
"pravo apply" does. The other tags (-t) are those of the release:

  pravo apply   --env staging -t image=1.4
  pravo promote prod -t image=1.4

The apply checks the promotion against the readers of the state it applies, and each note against
the readers file of the environment it comes from. Whoever promotes needs to be a writer of the
state they promote into. --sign-only stops after signing, and --store stores the plan of the
apply in the state instead, for anyone who may write it to apply by its id; so does a promotion into an environment
whose policies ask for approvals of a stored plan.

An admin who must release while an environment before cannot be read gives --waive "reason": the
promotion is signed without the notes and holds for this one release, and the apply keeps the
reason in its note. --revoke "reason" withdraws the promotions of the release signed before it.
"pravo environments" shows what each environment runs and the next command to type.`,
		Example: `  pravo promote prod -t image=1.4
  pravo promote prod -t image=1.4 --sign-only --note "smoke tests passed"
  pravo promote prod -t image=1.4 --store
  pravo promote prod -t image=1.4 --waive "staging bucket is down, hotfix for the outage"
  pravo promote prod -t image=1.4 --revoke "1.4 corrupts uploads"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return promoteInto(cmd, args[0], o)
		},
	}
	f := cmd.Flags()
	addTagFlags(cmd)
	f.StringVar(&flags.AccessDir, "access-dir", "", accessDirUsage)
	f.StringVar(&o.note, "note", "", "a line of text to keep with the promotion")
	f.StringVar(&o.waive, "waive", "", "promote without the notes of the environments before; an admin gives the reason")
	f.StringVar(&o.revoke, "revoke", "", "withdraw the promotions of this release; an admin gives the reason")
	f.BoolVar(&o.signOnly, "sign-only", false, "sign the promotion and stop; apply it with pravo apply")
	f.BoolVar(&o.store, "store", false, "sign the promotion and store the plan of the apply in the state instead of applying, to be applied later with \"pravo apply <plan-id>\"")
	f.StringVar(&o.ttl, "ttl", "", "with --store: keep the plan this long if it is not applied (90m, 36h, 7d; the default is 7d)")
	f.BoolVar(&o.autoApprove, "auto-approve", false, "apply without asking")
	addRunFlags(cmd, &o.refresh, &o.full)
	return cmd
}

// promotionJSON is a promotion in the JSON of promote.
type promotionJSON struct {
	Kind        string                `json:"kind"`
	Environment string                `json:"environment"`
	Fingerprint string                `json:"fingerprint"`
	Tags        map[string]string     `json:"tags,omitempty"`
	From        []promotionSourceJSON `json:"from,omitempty"`
	At          time.Time             `json:"at"`
	Signer      string                `json:"signer"`
	Key         string                `json:"key"`
	Note        string                `json:"note,omitempty"`
}

type promotionSourceJSON struct {
	Environment string    `json:"environment"`
	Lineage     string    `json:"lineage"`
	Fingerprint string    `json:"fingerprint"`
	GitRevision string    `json:"git_revision,omitempty"`
	Txn         string    `json:"txn,omitempty"`
	At          time.Time `json:"at"`
}

func promoteInto(cmd *cobra.Command, env string, o promoteOpts) error {
	ctx := cmd.Context()
	if o.waive != "" && o.revoke != "" {
		return engine.WithCode(engine.ExitInvalid, errors.New("--waive and --revoke do not go together"))
	}
	if o.store && (o.signOnly || o.revoke != "") {
		return engine.WithCode(engine.ExitInvalid, errors.New("--store goes with neither --sign-only nor --revoke"))
	}
	if o.ttl != "" && !o.store {
		return engine.WithCode(engine.ExitInvalid, errors.New("--ttl goes with --store"))
	}
	if given, ok := config.EnvironmentOf(flags.Tags); ok && given != env {
		return engine.WithCode(engine.ExitInvalid, fmt.Errorf("the environment is the argument of promote: pravo promote %s, without --env %s", env, given))
	}
	flags.Tags = config.WithEnvironment(flags.Tags, env)
	if _, err := parseRefresh(o.refresh); err != nil {
		return err
	}
	root, err := loadRoot()
	if err != nil {
		return err
	}
	if root.Environment == "" {
		return engine.WithCode(engine.ExitInvalid, errors.New("this specification declares no environments, so there is nothing to promote into. Declare them in $environments, and say in $after which one each follows"))
	}
	if stateMissing(ctx, root) {
		return engine.WithCode(engine.ExitRefused, fmt.Errorf("%s has no state yet, and a promotion is kept in it, signed. Seal one first: pravo protect init --encrypt --env %s", env, env))
	}
	envs := &environmentsOf{root: root}
	var line, already string
	err = func() error {
		st, err := openStateStore(ctx, root)
		if err != nil {
			return err
		}
		defer st.Close()
		st.SetActor(flags.Actor, flags.Reason, pravo.GitRevision(root.Dir))
		if st.Sealer() == nil {
			return engine.WithCode(engine.ExitRefused, fmt.Errorf("the state of %s is not sealed, and a promotion is a signature. Seal it: pravo protect init --encrypt --env %s", env, env))
		}
		rf := readersOf(root)
		eff, _, err := rf.withRoles(st)
		if err != nil {
			return engine.WithCode(engine.ExitUntrusted, errors.New(printable(err.Error())))
		}
		rec, err := st.EnvironmentRecord(ctx, eff.Readers)
		if err != nil {
			return secretErr(err)
		}
		if p := engine.RecordProblem(root, rec); p != "" {
			return engine.WithCode(engine.ExitRefused, fmt.Errorf("%s.\n\nIf the specification is right, an admin makes the record say so:\n\n    %s", p, engine.RecordCommand(root)))
		}
		gate := (&envState{es: config.EnvironmentSettings{Environment: mustCurrent(root)}, Record: rec}).after()
		if len(gate) == 0 {
			return engine.WithCode(engine.ExitRefused, fmt.Errorf("%s follows no environment, so it takes any release and there is nothing to promote into it. Apply it:\n\n    %s", env, engine.ApplyCommand(root, env)))
		}
		need, what := protect.RoleWriter, "promote into "+env
		switch {
		case o.waive != "":
			need, what = protect.RoleAdmin, "waive the check of "+joinAnd(gate)+" for "+env
		case o.revoke != "":
			need, what = protect.RoleAdmin, "withdraw a promotion into "+env
		}
		if msg, code := roleProblem(rf, st, need, what); msg != "" {
			return engine.WithCode(code, errors.New(msg))
		}
		rel, err := engine.ReleaseOf(root)
		if err != nil {
			return err
		}
		if o.revoke == "" && o.waive == "" {
			if msg := alreadyRuns(ctx, st, envs, env, gate, rec, rel, eff.Readers); msg != "" {
				already = msg
				return nil
			}
		}
		p := &state.Promotion{Kind: state.PromotionPromoted, Environment: env, Fingerprint: rel.Fingerprint, Tags: rel.Tags, Note: o.note}
		switch {
		case o.revoke != "":
			p.Kind, p.Note = state.PromotionRevoked, o.revoke
			line = fmt.Sprintf("Withdrawn: %s takes this release again only after a new promotion. The reason is kept in its state.", env)
		case o.waive != "":
			p.Kind, p.Note = state.PromotionWaived, o.waive
			line = fmt.Sprintf("Waived: %s may take this release although %s was not checked. The reason is kept with the promotion and with the note of the apply.", env, joinAnd(gate))
		default:
			var ran []string
			next := state.EnvironmentRecord{Name: env}
			if rec != nil {
				next.RolesPin = rec.RolesPin
			}
			fills := false
			for _, up := range gate {
				src, by, err := readUpstream(ctx, envs, root, up, env, rel, rec)
				if err != nil {
					return err
				}
				p.From = append(p.From, *src)
				u := state.Upstream{Name: up, Lineage: src.Lineage}
				if rec == nil || rec.Lineage(up) == "" {
					fills = true
				}
				next.After = append(next.After, u)
				ran = append(ran, fmt.Sprintf("%s runs this release, applied by %s %s ago", up, engine.PlainText(by), engine.RoundAge(time.Since(src.Run.At))))
			}
			// The first promotion records the state of each environment before, so that a
			// promotion that read another one is refused.
			if fills {
				seq := 1
				if rec != nil {
					seq = rec.Seq + 1
				}
				if err := st.PutEnvironmentRecord(ctx, seq, next); err != nil {
					return secretErr(fmt.Errorf("recording the state %s follows: %w", env, err))
				}
			}
			line = fmt.Sprintf("Promoted to %s: %s.", env, strings.Join(ran, "; "))
			// A withdrawal ends the promotions signed before it, and any writer's new
			// promotion counts again. Say so, with who withdrew it and why.
			g := engine.GateReader{State: st, Environment: env, Readers: eff.Readers, Now: time.Now()}
			if by, ago, why, ok := g.Withdrawal(ctx, rel); ok {
				line += fmt.Sprintf("\nNote: %s withdrew this release from %s %s ago (%s). This promotion lifts that; an admin withdraws it again with --revoke.", by, env, engine.RoundAge(ago), why)
			}
			if shared := sharedReaders(envs, env, gate); shared != "" {
				line += "\n" + out.C(ui.Dim, shared)
			}
		}
		if err := st.PutPromotion(ctx, p); err != nil {
			return secretErr(err)
		}
		if flags.JSON {
			j := promotionJSON{Kind: p.Kind, Environment: env, Fingerprint: p.Fingerprint, At: p.At, Signer: p.Signature.Signer, Key: p.Signature.Key, Note: p.Note, Tags: p.Tags}
			for _, src := range p.From {
				j.From = append(j.From, promotionSourceJSON{Environment: src.Environment, Lineage: src.Lineage, Fingerprint: src.Run.Fingerprint,
					GitRevision: src.Run.GitRevision, Txn: src.Run.Txn, At: src.Run.At})
			}
			emitLine(map[string]any{"kind": "promotion", "promotion": j})
		}
		return nil
	}()
	if err != nil {
		return err
	}
	if already != "" {
		if flags.JSON {
			rel, _ := engine.ReleaseOf(root)
			emitLine(map[string]any{"kind": "already_running", "environment": env, "fingerprint": rel.Fingerprint})
		} else {
			out.Println(already)
			if cmd.Flags().Changed("refresh") {
				out.Println("\n--refresh was ignored: nothing was promoted, so no plan ran.")
			}
		}
		return nil
	}
	if o.revoke != "" || o.signOnly {
		if !flags.JSON {
			out.Println(line)
			if o.signOnly {
				out.Printf("Apply it there with:\n\n    %s\n", engine.ApplyCommand(root, env))
			}
		}
		return nil
	}
	if !flags.JSON {
		out.Println(line)
		out.Println()
	}
	if o.store {
		return storePromotedPlan(cmd, o)
	}
	// The apply is the one "pravo apply" makes. Where the policies of the environment ask for
	// approvals it is refused, and says how to store the plan.
	return runApply(cmd, o.refresh, o.full, o.autoApprove, false, nil, nil)
}

// storePromotedPlan stores the plan of the apply in the state, as "pravo plan --store" does.
// Its apply by id checks the promotion again.
func storePromotedPlan(cmd *cobra.Command, o promoteOpts) error {
	// Defining the flags of plan resets the globals they write, so they are put back.
	tags, env, sys := flags.Tags, flags.Env, flags.SysTags
	pc := planCmd()
	flags.Tags, flags.Env, flags.SysTags = tags, env, sys
	pc.SetContext(cmd.Context())
	set := map[string]string{"store": "true", "refresh": o.refresh}
	if o.ttl != "" {
		set["ttl"] = o.ttl
	}
	if o.full {
		set["full"] = "true"
	}
	for k, v := range set {
		if err := pc.Flags().Set(k, v); err != nil {
			return err
		}
	}
	return pc.RunE(pc, nil)
}

// alreadyRuns says that env already runs the release, or "" when it does not: its newest
// note, signed there by a writer, names the release, and a promotion of the release still
// counts. Promoting again would sign a second promotion and apply nothing new.
func alreadyRuns(ctx context.Context, st *state.Store, envs *environmentsOf, env string, gate []string, rec *state.EnvironmentRecord, rel engine.Release, readers []protect.Reader) string {
	now := time.Now()
	note, _, err := st.LatestRun(ctx, now)
	if err != nil || note == nil || !rel.Same(note) || note.Deferred > 0 || engine.NoteProblem(note, env, st.Lineage(), readers, now) != "" {
		return ""
	}
	c := engine.GateReader{State: st, Environment: env, Readers: readers, Upstream: envs.readers, Now: now}.Check(ctx, gate, rec, rel)
	if !c.Met || c.Admission == nil {
		return ""
	}
	_, by := note.Verify(st.Lineage(), readers)
	how := "promoted"
	if c.Admission.Kind == state.PromotionWaived {
		how = "let in by a waiver"
	}
	return fmt.Sprintf("%s already runs this release (%s): applied by %s %s ago, %s by %s. Nothing to promote.\nTo bring %s back in line with it, apply it again:\n\n    %s",
		env, engine.ShortID(rel.Fingerprint), engine.PlainText(by), engine.RoundAge(now.Sub(note.At)), how, engine.PlainText(c.Admission.By), env, engine.ApplyCommand(envs.root, env))
}

func mustCurrent(root *config.Root) config.Environment {
	cur, _ := root.Current()
	return cur
}

// readUpstream reads the note of the environment up and checks that it runs the release: it is
// signed there by a writer, names the release, and comes from the state the record of env
// follows.
func readUpstream(ctx context.Context, envs *environmentsOf, root *config.Root, up, env string, rel engine.Release, rec *state.EnvironmentRecord) (*state.PromotionSource, string, error) {
	s := envs.open(ctx, up)
	defer s.close()
	steps := func(why string) error {
		return engine.WithCode(engine.ExitRefused, fmt.Errorf("%s does not run this release: %s.\n\nRun these, skipping a step that is already done:\n\n%s", up, why, engine.Steps(root, env)))
	}
	switch {
	case s.Err != nil:
		return nil, "", engine.WithCode(engine.ExitRefused, fmt.Errorf("the state of %s, which %s follows, cannot be read here: %s\nAn admin can promote without it: pravo promote %s --waive \"reason\"", up, env, firstLine(s.Err), env))
	case s.Missing || s.Note == nil:
		return nil, "", steps("nothing has been applied there yet")
	case !s.Sealed:
		return nil, "", engine.WithCode(engine.ExitRefused, fmt.Errorf("the state of %s is not sealed, so it keeps no signed note of what it runs. Seal it: pravo protect init --encrypt --env %s\nAn admin can promote without it: pravo promote %s --waive \"reason\"", up, up, env))
	case s.NoteProblem != "":
		return nil, "", engine.WithCode(engine.ExitRefused, fmt.Errorf("%s, so it does not show what %s runs. The key may belong to someone who left: a current writer of %s applies the release again, and the promotion follows.\n\nRun these, skipping a step that is already done:\n\n%s", s.NoteProblem, up, up, engine.Steps(root, env)))
	case s.Destroyed:
		return nil, "", engine.WithCode(engine.ExitRefused, fmt.Errorf("%s was destroyed after it ran this release, so there is nothing to promote from.\n\nRun these, skipping a step that is already done:\n\n%s", up, engine.Steps(root, env)))
	}
	if want := rec.Lineage(up); want != "" && want != s.st.Lineage() {
		return nil, "", engine.WithCode(engine.ExitRefused, fmt.Errorf("the state of %s is another than the one %s follows: its settings point at a state that its record does not name.\nIf the settings are right, an admin makes the record of %s say so:\n\n    pravo environments record --env %s", up, env, env, env))
	}
	if why := rel.Differs(s.Note); why != "" {
		return nil, "", steps(why)
	}
	if n := s.Note.Deferred; n > 0 {
		return nil, "", engine.WithCode(engine.ExitRefused, fmt.Errorf("%s runs this release only in part: its apply deferred %s to the next plan. Apply it there again, and promote once nothing is deferred:\n\n    %s",
			up, deferredChanges(n), engine.ApplyCommand(root, up)))
	}
	return &state.PromotionSource{Environment: up, Lineage: s.st.Lineage(), Run: s.Note}, s.NoteBy, nil
}

// sharedReaders says, when the environment and one it follows read one readers file, that the
// writers of the one before may promote into it.
func sharedReaders(envs *environmentsOf, env string, gate []string) string {
	mine, err := envs.readersFile(env)
	if err != nil {
		return ""
	}
	for _, up := range gate {
		if rf, err := envs.readersFile(up); err == nil && rf.Path == mine.Path {
			return fmt.Sprintf("Note: %s and %s read one readers file, so a writer of %s may promote into %s. $settings.access.dir gives %s a file of its own.", up, env, up, env, env)
		}
	}
	return ""
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

// refuseOtherEnvironment refuses a command that changes the state outside a plan and an apply,
// such as state rm, import or secret set, when the record of the state says that it serves
// another environment than the one the specification was loaded for: the check that plan and
// apply make, so that a state that a setting points at by mistake is not written to.
func refuseOtherEnvironment(ctx context.Context, root *config.Root, st *state.Store) error {
	eff, _, err := readersOf(root).withRoles(st)
	if err != nil {
		return engine.WithCode(engine.ExitUntrusted, errors.New(printable(err.Error())))
	}
	return refuseOtherEnvironmentFor(ctx, root, st, eff.Readers)
}

// refuseOtherEnvironmentFor is refuseOtherEnvironment with the readers already resolved.
func refuseOtherEnvironmentFor(ctx context.Context, root *config.Root, st *state.Store, readers []protect.Reader) error {
	rec, err := st.EnvironmentRecord(ctx, readers)
	if err != nil {
		return secretErr(err)
	}
	if p := engine.RecordProblem(root, rec); p != "" {
		return engine.WithCode(engine.ExitRefused, fmt.Errorf("%s.\n\nIf the specification is right, an admin makes the record say so:\n\n    %s", p, engine.RecordCommand(root)))
	}
	return nil
}
