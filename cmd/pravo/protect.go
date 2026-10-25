package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/ui"
)

const accessDirUsage = "directory of the readers file (default .pravo/access); $PRAVO_ACCESS_DIR"

// identityPrompt says whether a command may ask for a passphrase on the
// terminal. pravo mcp turns it off: it has no terminal, and the identity of a
// server comes from the environment.
var identityPrompt = true

func protectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "protect",
		Short: "Seal the state to a list of readers and check that it holds",
		Long: `Protection is off by default, and a state that has it off is unchanged. These
commands turn it on, show where it stands and keep it in line with the list of
readers.

The list of readers is .pravo/access/readers.txt in the repository, one line
for each person, as "pravo id new" prints it. Writers take the list from the
repository and never from the state. Whoever can merge a change to the file
decides who may read.

"protect check" reads the store and says whether it can be sealed. "protect
init --encrypt" stores every record as an immutable object and encrypts the
attributes, the outputs, the stored plans and the values in command lines to
the readers. "protect status" says where things stand. "protect rewrap" brings
the keys in line with the readers file after a reader is added or removed, and sets
the recovery recipient. "protect recover" opens a state with the shares of its
recovery key when no reader can. "protect unseal" takes the seal off.
"protect gc" deletes the records that no pointer names, and the reader groups
that nothing is sealed in any more.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	pf := cmd.PersistentFlags()
	pf.StringVar(&flagAccessDir, "access-dir", "", accessDirUsage)
	cmd.AddCommand(protectCheckCmd(), protectInitCmd(), protectStatusCmd(), protectRewrapCmd(), protectRecoverCmd(), protectUnsealCmd(), protectGCCmd())
	return cmd
}

// protectRun is what the protect commands share: the specification, the
// readers file and the context of the command.
type protectRun struct {
	ctx  context.Context
	root *config.Root
	rf   readersFile
}

func newProtectRun(cmd *cobra.Command) (*protectRun, error) {
	root, err := loadRoot()
	if err != nil {
		return nil, err
	}
	return &protectRun{ctx: cmd.Context(), root: root, rf: loadReadersFile(root.Dir, flagAccessDir)}, nil
}

func (p *protectRun) local() bool { return !p.root.State.Remote() }

// backend returns the backend of the state. A local directory that does not
// exist is created only when write is set.
func (p *protectRun) backend(write bool) (state.Backend, error) {
	if !p.local() {
		return remoteBackend(p.ctx, p.root)
	}
	path := p.root.StateFile()
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		_, err := state.Open(path) // says that the path is a file
		return nil, err
	}
	if write {
		return state.NewLocalBackend(path)
	}
	return &state.LocalBackend{Dir: path}, nil
}

// refuseLocal is the refusal of check and init for a state in a directory,
// unless allow is set. Its compare-and-swap is a lock of one user, so two users
// of one directory would not be excluded.
func (p *protectRun) refuseLocal(allow bool) error {
	if !p.local() || allow {
		return nil
	}
	return fmt.Errorf("the state at %s is a local directory. Its compare-and-swap is a per-user mutex, so two users on one directory would not be excluded. Move the state to a bucket, or pass --allow-local for a directory that only you use.", p.location())
}

// location is where the state is, as the commands print it.
func (p *protectRun) location() string {
	if !p.local() {
		if p.root.State.Backend == "http" {
			return strings.TrimRight(p.root.State.URL, "/")
		}
		return "s3://" + path3(p.root.State.Bucket, p.root.State.Prefix)
	}
	loc := displayPath(p.root.StateFile())
	if !strings.HasPrefix(loc, ".") && !filepath.IsAbs(loc) {
		loc = "./" + loc
	}
	return loc
}

func path3(bucket, prefix string) string {
	if prefix == "" {
		return bucket
	}
	return bucket + "/" + strings.Trim(prefix, "/")
}

func (p *protectRun) storeLine() string {
	if !p.local() {
		if r := p.root.State.Region; r != "" {
			return p.location() + "  (" + r + ")"
		}
		return p.location()
	}
	return p.location() + "  (local directory)"
}

// open opens the store. When the state is sealed and needKey is set it finds
// the identity of the user and opens the store again with it. A state that is
// sealed and cannot be opened with a key is returned locked, with the reason in
// idErr.
func (p *protectRun) open(b state.Backend, write, needKey bool) (st *state.Store, id *protect.Identity, idErr error, err error) {
	mk := func(id *protect.Identity) (*state.Store, error) {
		bb := b
		if !write {
			bb = &readOnlyBackend{Backend: b}
		}
		return state.OpenBackendWith(p.ctx, bb, state.Options{Identity: id})
	}
	if st, err = mk(nil); err != nil || !st.Locked() || !needKey {
		return st, nil, nil, err
	}
	id, idErr = resolveIdentity(identityOptions{Name: flagIdentity, Prompt: identityPrompt, Readers: currentReaders(st.Protection())})
	if idErr != nil {
		return st, nil, idErr, nil
	}
	keyed, err := mk(id)
	if errors.Is(err, state.ErrNotReader) {
		return st, id, nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	return keyed, id, nil, nil
}

// displayPath writes a path relative to the working directory, with forward
// slashes. A path that cannot be made relative stays as it is, and so does one
// whose relative form climbs more than two levels: ../../../../x says less than
// the path.
func displayPath(p string) string {
	wd, err := os.Getwd()
	if err != nil {
		return p
	}
	rel, err := filepath.Rel(wd, p)
	if err != nil {
		return p
	}
	rel = filepath.ToSlash(rel)
	climbs := 0
	for rest := rel; strings.HasPrefix(rest, "../"); rest = rest[3:] {
		climbs++
	}
	if climbs > 2 {
		return p
	}
	return rel
}

// shown is the readers file as the messages name it.
func (rf readersFile) shown() string { return displayPath(rf.Path) }

// shownDir is the directory the readers file was looked for in.
func (rf readersFile) shownDir() string { return displayPath(rf.Dir) }

// require is the error for a readers file that is missing or cannot be
// used, with exit code 6.
func (rf readersFile) require() error {
	switch {
	case !rf.Found:
		return engine.WithCode(engine.ExitInvalid, fmt.Errorf("the readers file was not found (looked in %s). It lists who may read the state, one line for each, as pravo id new prints it.", rf.shownDir()))
	case rf.Err != nil:
		return engine.WithCode(engine.ExitInvalid, fmt.Errorf("%s: %s", rf.shown(), rf.problem()))
	case len(rf.Readers) == 0:
		return engine.WithCode(engine.ExitInvalid, fmt.Errorf("%s lists no readers", rf.shown()))
	}
	return nil
}

// notReaderError is the refusal to open the values of a sealed state: exit 5.
func notReaderError(rf readersFile, id *protect.Identity, idErr error) error {
	why := "no identity found"
	switch {
	case id != nil:
		why = "identity " + id.Name + " is not a reader"
	case idErr != nil && !errors.Is(idErr, errNoIdentity):
		// The identity exists and cannot be opened (a passphrase that is
		// missing or wrong): asking to be added to the list would mislead.
		return fmt.Errorf("%w (%s)", state.ErrNotReader, strings.TrimSuffix(idErr.Error(), "."))
	case idErr != nil && strings.HasPrefix(idErr.Error(), "no identity found: "):
		// Several identity files and none named like the user, or a name that
		// does not exist: the reader may well be on the list, so the advice is not
		// to ask to be added.
		return fmt.Errorf("%w (%s)", state.ErrNotReader, strings.TrimSuffix(idErr.Error(), "."))
	}
	return fmt.Errorf("%w (%s).\n  Readers are listed in %s. Ask one of them to add yours.", state.ErrNotReader, why, rf.shown())
}

var nameShape = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// safeName is a reader name from the state as the terminal may show it. The
// names in meta.json come from the bucket, so a name that is not shaped like the
// names of the readers file is not printed.
func safeName(s string) string {
	if nameShape.MatchString(s) {
		return s
	}
	return "(invalid name)"
}

func safeNames(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = safeName(n)
	}
	return out
}

// printable drops the control characters of a text that came from the bucket, so
// that it cannot move the cursor or recolour the terminal.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)
}

// counted writes a number of things: "1 record", "2,001 records".
func counted(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return commas(n) + " " + many
}

// commas writes n with a comma between the thousands.
func commas(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func readerNames(rs []protect.Reader) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Name
	}
	return out
}

func nameList(names []string) string { return strings.Join(names, " ") }

// ---------- readers file against the store ----------

// readersDiff compares the readers file with the readers of a sealed state.
// Added are in the file and not in the store; removed are the other way round.
func readersDiff(prot state.Protection, rf readersFile) (added, removed []string) {
	if prot.Sealed == nil {
		return nil, nil
	}
	st := prot.Sealed.Status(rf.Readers)
	return st.Missing, safeNames(st.Extra)
}

// listedNames is how many names a line of the readers file against the store
// lists before it says how many more there are.
const listedNames = 5

// capNames joins at most listedNames names and says how many it left out.
func capNames(names []string) string {
	if len(names) <= listedNames {
		return strings.Join(names, " ")
	}
	return strings.Join(names[:listedNames], " ") + fmt.Sprintf(" and %d more", len(names)-listedNames)
}

func diffText(added, removed []string) string {
	var parts []string
	if len(added) > 0 {
		parts = append(parts, capNames(added)+" added")
	}
	if len(removed) > 0 {
		parts = append(parts, capNames(removed)+" removed")
	}
	return strings.Join(parts, ", ")
}

// differsText is the text of the readers line of protect status when the file and
// the store differ: one clause per reader, the first listedNames of them.
func differsText(added, removed []string) string {
	var parts []string
	for _, n := range added {
		parts = append(parts, n+" is in the file and not in the store")
	}
	for _, n := range removed {
		parts = append(parts, n+" is in the store and not in the file")
	}
	if more := len(parts) - listedNames; more > 0 {
		parts = append(parts[:listedNames], fmt.Sprintf("and %d more", more))
	}
	return "differs: " + strings.Join(parts, "; ")
}

// isSealed reports whether the state is sealed, or being sealed.
func isSealed(prot state.Protection) bool {
	return (prot.State == "sealed" || prot.State == "migrating" || prot.State == "unsealing") && prot.Sealed != nil
}

// downgradeProblem is what a command says about a state that is not sealed in a
// repository that lists readers. The repository is the anchor that a bucket
// writer cannot move: whoever can write to the bucket can lower the format in
// meta.json, and the writers that follow would write in the clear.
func downgradeProblem(rf readersFile) string {
	return fmt.Sprintf("the repository lists readers in %s, but the state is not sealed: it has not been sealed yet, or its meta.json was changed. Run pravo protect status; to seal it, pravo protect init --encrypt; if the state is not meant to be sealed, remove the readers file and the recovery file.", rf.shown())
}

// missingStateProblem is what a command says about a repository that lists
// readers when the state does not exist yet. The state is not created: a first
// writer would make it in format 1, in the clear, and the guard would then have
// nothing to say but that it is not sealed.
func missingStateProblem(rf readersFile) string {
	return fmt.Sprintf("the repository lists readers in %s, but the state does not exist yet. Create it sealed with: pravo protect init --encrypt", rf.shown())
}

// expectsSealed reports whether the repository expects the state to be sealed:
// it has a readers file, whether or not the file can be read, and the state is
// not sealed.
func expectsSealed(prot state.Protection, rf readersFile) bool {
	return rf.Found && !isSealed(prot)
}

// checkReadersFile compares the readers file with the sealed state st. A writer
// refuses to go on when they differ or the file cannot be used; a reader only
// warns. A state that is not sealed in a repository that has a readers file
// is refused to a writer with exit code 5 and warned of to a reader: it was not
// sealed yet, or it was downgraded in the bucket.
func checkReadersFile(root *config.Root, st *state.Store, write bool) error {
	prot := st.Protection()
	rf := loadReadersFile(root.Dir, flagAccessDir)
	if !isSealed(prot) {
		if !expectsSealed(prot, rf) {
			return nil
		}
		problem := downgradeProblem(rf)
		if write {
			return engine.WithCode(engine.ExitRefused, errors.New(problem))
		}
		fmt.Fprintln(os.Stderr, "Warning: "+problem)
		return nil
	}
	problem, code := "", engine.ExitRefused
	switch {
	case !rf.Found:
		problem = fmt.Sprintf("the readers file was not found (looked in %s). A sealed state is written with the list of readers from the repository.", rf.shownDir())
	case rf.Err != nil:
		problem, code = fmt.Sprintf("%s: %s", rf.shown(), rf.problem()), engine.ExitInvalid
	default:
		added, removed := readersDiff(prot, rf)
		if len(added) == 0 && len(removed) == 0 {
			// The readers agree; the recovery recipient of the repository must too.
			problem, code = recoveryProblem(prot, rf)
			break
		}
		problem = fmt.Sprintf("%s and the state differ (%s). Run pravo protect rewrap.", rf.shown(), diffText(added, removed))
	}
	if problem == "" {
		return nil
	}
	if write {
		return engine.WithCode(code, errors.New(problem))
	}
	fmt.Fprintln(os.Stderr, "Warning: "+problem)
	return nil
}

// ---------- protect check ----------

func protectCheckCmd() *cobra.Command {
	var allowLocal bool
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check that the store can be sealed",
		Long: `Reads the store and says whether it can be sealed. It tests the store harder
than the probe that every command makes: eight writers race to replace one
object and exactly one must win, and a key that has just been written must be in
the listing. It lists what is running now, reads the readers file, and writes
nothing to the state.

A state in a local directory is refused, because its compare-and-swap is a lock
of one user. --allow-local accepts a directory that only you use.

Exit codes: 4 when a command is running or a lock is held, 6 when the readers
file is missing or invalid.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newProtectRun(cmd)
			if err != nil {
				return err
			}
			return p.check(allowLocal)
		},
	}
	cmd.Flags().BoolVar(&allowLocal, "allow-local", false, "accept a state in a local directory that only you use")
	return cmd
}

// checkResult is the JSON form of protect check.
type checkResult struct {
	Store     string   `json:"store"`
	Sealed    bool     `json:"sealed"`
	Format    int      `json:"format"`
	Records   int      `json:"records"`
	Readers   []string `json:"readers,omitempty"`
	Running   int      `json:"running"`
	Locks     int      `json:"locks"`
	Ready     bool     `json:"ready"`
	NotReady  string   `json:"not_ready,omitempty"`
	LastWrite string   `json:"last_written_by,omitempty"`
}

func (p *protectRun) check(allowLocal bool) error {
	if err := p.refuseLocal(allowLocal); err != nil {
		return err
	}
	b, err := p.backend(false)
	if err != nil {
		return err
	}
	st, _, _, err := p.open(b, false, false)
	if err != nil {
		return err
	}
	prot := st.Protection()
	res := checkResult{Store: p.location(), Format: prot.Format, Records: prot.Records}
	if prot.State == "sealed" {
		res.Sealed = true
		if flagJSON {
			return emitJSON(res)
		}
		out.Println("Already sealed. Use pravo protect status.")
		return nil
	}
	if prot.State == "unsealing" {
		if flagJSON {
			return emitJSON(res)
		}
		out.Println("The state is being unsealed: it was started and did not finish. Finish it with pravo protect unseal.")
		return nil
	}
	out.Printf("%-16s %s\n", "Store", p.storeLine())
	cond, err := p.probeLines(b)
	out.Printf("%-16s %s\n", "Conditional", cond[0])
	if cond[1] != "" {
		out.Printf("%-16s %s\n", "", cond[1])
	}
	if err != nil {
		return err
	}
	txns, err := st.Transactions()
	if err != nil {
		return err
	}
	line := fmt.Sprintf("%s · format %d", counted(prot.Records, "record", "records"), prot.Format)
	if v := lastWriter(txns); v != "" {
		line += " · last written by pravo " + v
		res.LastWrite = v
	}
	if prot.State == "migrating" && prot.Migration != nil {
		line += fmt.Sprintf(" · sealing began, %s of %s records done", commas(prot.Migration.Done), commas(prot.Migration.Total))
	}
	if prot.State == "unsealing" && prot.Migration != nil {
		line += fmt.Sprintf(" · unsealing began, %s of %s records done", commas(prot.Migration.Done), commas(prot.Migration.Total))
	}
	out.Printf("%-16s %s\n", "State", line)
	rf := p.rf
	switch {
	case !rf.Found:
		out.Printf("%-16s not found (looked in %s)\n", "Readers file", rf.shownDir())
	case rf.Err != nil:
		out.Printf("%-16s %s · invalid: %s\n", "Readers file", rf.shown(), rf.problem())
	default:
		out.Printf("%-16s %s · %s (%s)\n", "Readers file", rf.shown(), plural(len(rf.Readers), "reader", "readers"), nameList(readerNames(rf.Readers)))
		res.Readers = readerNames(rf.Readers)
	}
	running, locks, err := activity(st, txns)
	if err != nil {
		return err
	}
	res.Running, res.Locks = running, locks
	out.Printf("%-16s %s · %s held\n", "Running", plural(running, "transaction", "transactions"), plural(locks, "lock", "locks"))
	out.Printf("%-16s %s\n", "Machines", machines(txns, time.Now()))
	switch {
	case !rf.Found || rf.Err != nil || len(rf.Readers) == 0:
		res.NotReady = "readers file"
		if flagJSON {
			if e := emitJSON(res); e != nil {
				return e
			}
			return engine.WithCode(engine.ExitInvalid, errSilent)
		}
		return engine.WithCode(engine.ExitInvalid, errSilent)
	case running > 0 || locks > 0:
		res.NotReady = "running"
		out.Println(out.C(ui.Red, "Not ready: wait for them, or release stale locks with pravo state unlock --stale."))
		if flagJSON {
			if e := emitJSON(res); e != nil {
				return e
			}
		}
		return engine.WithCode(engine.ExitLocked, errSilent)
	}
	res.Ready = true
	out.Println(out.C(ui.Green, "Ready to seal. Next: pravo protect init --encrypt"))
	if flagJSON {
		return emitJSON(res)
	}
	return nil
}

// probeLines runs the probes of an S3 or http store and returns the two lines of
// "Conditional", and the error of the first check that failed.
func (p *protectRun) probeLines(b state.Backend) ([2]string, error) {
	sb, ok := b.(state.Prober)
	if !ok {
		return [2]string{"a local directory: compare-and-swap is a lock of one user (--allow-local)"}, nil
	}
	short := map[string]string{
		state.ProbeChecks[0]: "create-only", state.ProbeChecks[1]: "replace", state.ProbeChecks[2]: "delete", state.ProbeChecks[3]: "concurrent creates",
	}
	var first []string
	failed := false
	err := sb.ProbeSteps(p.ctx, func(check string, err error) {
		switch {
		case err != nil:
			failed = true
			first = append(first, short[check]+" "+out.C(ui.Red, "FAILED"))
		case check != state.ProbeChecks[3]:
			first = append(first, short[check]+" ok")
		}
	})
	if err != nil {
		if len(first) == 0 {
			first = append(first, "not tested")
		}
		_ = failed
		return [2]string{strings.Join(first, " · ")}, err
	}
	var second []string
	err = sb.ProbeStrict(p.ctx, func(check string, err error) {
		switch {
		case check == state.StrictChecks[0] && err == nil:
			second = append(second, fmt.Sprintf("8 concurrent replaces: 1 winner"))
		case check == state.StrictChecks[0]:
			second = append(second, "8 concurrent replaces: "+out.C(ui.Red, "FAILED"))
		case err == nil:
			second = append(second, "listing sees a new key at once")
		default:
			second = append(second, "listing after a put: "+out.C(ui.Red, "FAILED"))
		}
	})
	return [2]string{strings.Join(first, " · "), strings.Join(second, " · ")}, err
}

// lastWriter is the version of pravo that wrote the newest transaction that
// records one.
func lastWriter(txns []state.Transaction) string {
	for _, t := range txns { // newest first
		if t.Version != "" {
			return t.Version
		}
	}
	return ""
}

// activity counts the transactions that are running and not explained by a
// command that was killed, and the locks that are held and not stale.
func activity(st *state.Store, txns []state.Transaction) (running, locks int, err error) {
	interrupted, err := st.Interrupted(txns)
	if err != nil {
		return 0, 0, err
	}
	for _, t := range txns {
		if t.Status == "running" && !interrupted[t.ID] {
			running++
		}
	}
	ls, err := st.Locks()
	if err != nil {
		return 0, 0, err
	}
	for _, l := range ls {
		if !l.Stale {
			locks++
		}
	}
	return running, locks, nil
}

// machines says which versions of pravo wrote in the last 30 days.
func machines(txns []state.Transaction, now time.Time) string {
	since := now.AddDate(0, 0, -30)
	var recent, versioned int
	oldest := ""
	for _, t := range txns {
		if t.Started.Before(since) {
			continue
		}
		recent++
		if t.Version == "" {
			continue
		}
		versioned++
		if oldest == "" || versionLess(t.Version, oldest) {
			oldest = t.Version
		}
	}
	switch {
	case recent == 0:
		return "no writer in the last 30 days"
	case versioned < recent:
		return fmt.Sprintf("versions are not recorded by this state's older writers (%d of %d transactions in 30 days)", recent-versioned, recent)
	}
	return "every writer in the last 30 days used pravo >= " + oldest
}

// versionLess orders versions such as 0.1.0, 0.10.2 and 0.1.0-dev: numbers by
// value, and a version with a suffix before the same one without.
func versionLess(a, b string) bool {
	an, as, _ := strings.Cut(strings.TrimPrefix(a, "v"), "-")
	bn, bs, _ := strings.Cut(strings.TrimPrefix(b, "v"), "-")
	ap, bp := strings.Split(an, "."), strings.Split(bn, ".")
	for i := 0; i < len(ap) || i < len(bp); i++ {
		var x, y int
		if i < len(ap) {
			x, _ = strconv.Atoi(ap[i])
		}
		if i < len(bp) {
			y, _ = strconv.Atoi(bp[i])
		}
		if x != y {
			return x < y
		}
	}
	if (as == "") != (bs == "") {
		return as != ""
	}
	return as < bs
}

// ---------- protect init ----------

func protectInitCmd() *cobra.Command {
	var encrypt, yes, allowLocal bool
	var recovery string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Seal the state to the readers in the readers file",
		Long: `Seals the state. It stores every record once as an immutable object,
rec/<sha256>, and encrypts the attributes and the private data of each record,
the outputs, the stored plans and the values in command lines to the readers in
the readers file. Addresses, types, names, dependencies and revisions stay
readable, so the graph and the plans keep working. The state is set to format 3,
which earlier versions of pravo refuse to open.

It shows what it will do and asks for a yes. --yes answers it. Without a
terminal and without --yes it refuses to ask. It does not run while a command is
running or a lock is held. It needs an identity that is on the readers file,
because sealing to a list that you are not on would lock you out.

--recovery K/N also sets a recovery recipient: a key that is split into N shares,
any K of which rebuild it, so that the state can be opened when every reader has
lost their key. The shares are written to recovery-shares/ and the public key to
the recovery file next to the readers file. Give each share to a different person,
offline, and commit the recovery file; never commit the shares.

A run that is interrupted leaves the state sealing; run it again to finish.

Exit codes: 4 when a command is running or a lock is held, 5 when your identity
is not on the readers file, 6 when the readers file is missing or invalid.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !encrypt {
				return errors.New("protect init needs --encrypt: it is the only protection there is")
			}
			p, err := newProtectRun(cmd)
			if err != nil {
				return err
			}
			return p.init(yes, allowLocal, recovery)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&encrypt, "encrypt", false, "seal the attributes, outputs and stored plans to the readers")
	f.BoolVar(&yes, "yes", false, "do not ask for confirmation")
	f.BoolVar(&allowLocal, "allow-local", false, "accept a state in a local directory that only you use")
	f.StringVar(&recovery, "recovery", "", "also set a recovery recipient, as K/N: N shares, any K rebuild its key")
	return cmd
}

// atRest refuses, with the exit code of a locked state, while a command is
// running or a lock is held. The commands that rewrite every record of the state
// run only then: a writer that is half way through a write would lose it.
func atRest(st *state.Store) error {
	txns, err := st.Transactions()
	if err != nil {
		return err
	}
	running, locks, err := activity(st, txns)
	if err != nil {
		return err
	}
	if running > 0 || locks > 0 {
		var busy []string
		if running > 0 {
			busy = append(busy, plural(running, "transaction is", "transactions are")+" running")
		}
		if locks > 0 {
			busy = append(busy, plural(locks, "lock is", "locks are")+" held")
		}
		return engine.WithCode(engine.ExitLocked, fmt.Errorf("not ready: %s. Wait for them, or release stale locks with pravo state unlock --stale.", strings.Join(busy, " and ")))
	}
	return nil
}

func (p *protectRun) init(yes, allowLocal bool, recoverySpec string) error {
	if err := p.refuseLocal(allowLocal); err != nil {
		return err
	}
	var rec *protect.Recovery
	var shares []protect.Share
	if recoverySpec != "" {
		var err error
		if rec, shares, err = newRecovery(recoverySpec); err != nil {
			return err
		}
	}
	if err := p.rf.require(); err != nil {
		return err
	}
	// Several identity files and none named like the user: the one on the list.
	var listed []protect.ReaderPublic
	for _, r := range p.rf.Readers {
		listed = append(listed, r.Key)
	}
	id, err := resolveIdentity(identityOptions{Name: flagIdentity, Prompt: identityPrompt, Readers: listed})
	if err != nil {
		if errors.Is(err, errNoIdentity) {
			return engine.WithCode(engine.ExitRefused, fmt.Errorf("%w; sealing to a list you are not on would lock you out", err))
		}
		return err
	}
	if !readersInclude(p.rf.Readers, id) {
		return engine.WithCode(engine.ExitRefused, fmt.Errorf("your identity %s is not in %s. Sealing to a list you are not on would lock you out.", id.Name, p.rf.shown()))
	}
	// Everything before the yes only reads.
	b, err := p.backend(false)
	if err != nil {
		return err
	}
	st, _, _, err := p.open(b, false, false)
	if err != nil {
		return err
	}
	prot := st.Protection()
	if prot.State == "sealed" {
		out.Printf("Already sealed (format %d). Use pravo protect rewrap to change the readers.\n", prot.Format)
		if rec != nil {
			out.Println(out.C(ui.Yellow, "--recovery was not applied. To set a recovery recipient on a sealed state: pravo protect rewrap --recovery "+fmt.Sprintf("%d/%d", rec.K, rec.N)))
		}
		return nil
	}
	if prot.State == "unsealing" {
		return errors.New("an unseal was started and did not finish; finish it with pravo protect unseal before the state is sealed again")
	}
	if rec != nil && prot.State == "migrating" {
		return errors.New("a seal was started and did not finish, and a run that finishes it does not set a recovery. Finish it with pravo protect init --encrypt, then run pravo protect rewrap --recovery " + fmt.Sprintf("%d/%d", rec.K, rec.N))
	}
	if err := atRest(st); err != nil {
		return err
	}
	names := nameList(readerNames(p.rf.Readers))
	out.Printf("%-8s %s  (%s)\n", "Readers", p.rf.shown(), names)
	if m := prot.Migration; prot.State == "migrating" && m != nil {
		out.Printf("A seal was started by %s and did not finish: %s of %s records are done.\n", safeName(m.By), commas(m.Done), commas(m.Total))
	}
	out.Println("This will:")
	out.Printf("  1  write %s as immutable objects  rec/<sha256>\n", counted(prot.Records, "record", "records"))
	out.Printf("  2  encrypt their attributes and private data to %s (%s)\n", plural(len(p.rf.Readers), "reader", "readers"), names)
	out.Println("  3  seal outputs.json, stored-plan values and the values in transaction command lines the same way")
	step := 4
	if rec != nil {
		out.Printf("  %d  add a recovery recipient: %d shares, any %d rebuild the key\n", step, rec.N, rec.K)
		step++
	}
	out.Printf("  %d  set the state to format %d. Earlier versions of pravo will refuse it\n", step, state.FormatSealed)
	if ok, err := confirm("Proceed?", yes); err != nil {
		return err
	} else if !ok {
		return errors.New("protect init cancelled; nothing was written")
	}
	if sb, ok := b.(state.Prober); ok {
		if err := sb.ProbeSteps(p.ctx, nil); err != nil {
			return err
		}
		if err := sb.ProbeStrict(p.ctx, nil); err != nil {
			return err
		}
	}
	out.Println(out.C(ui.Green, "store check passed"))
	if b, err = p.backend(true); err != nil {
		return err
	}
	if st, _, _, err = p.open(b, true, true); err != nil {
		return err
	}
	// The shares are written before the state is touched: a seal that lost them would
	// leave a recovery lock that nobody can open.
	var removeShares func()
	if rec != nil {
		if removeShares, err = writeShares(shares); err != nil {
			return err
		}
	}
	began := time.Now()
	res, err := st.Upgrade(p.ctx, state.UpgradeOptions{
		To: state.FormatSealed, Readers: p.rf.Readers, Identity: id, By: id.Name, Recovery: rec,
		Plans: engine.NewPlanStoreFor(st).SealAll,
	})
	if err != nil {
		if rec != nil {
			p.afterFailure(rec, removeShares, loadRecoveryFile(p.rf))
		}
		return err
	}
	out.Println(out.C(ui.Green, fmt.Sprintf("%s sealed in %s", counted(res.Records, "record", "records"), elapsed(time.Since(began)))))
	out.Println(out.C(ui.Green, "outputs sealed · recipients written to meta.json"))
	if res.PlansSkipped > 0 {
		out.Println(plansInUseLine(res.PlansSkipped, false))
	}
	out.Println()
	out.Printf("The state is sealed to %s.\n", names)
	out.Println()
	out.Println("Commit the readers file. Writers read the list from git:")
	out.Printf("  %s\n", p.rf.shown())
	rcf := loadRecoveryFile(p.rf)
	if rec == nil {
		// A run that was cut short after it set a recovery leaves it in the state.
		if stored, ok := p.storedRecovery(); ok && stored != nil {
			if !rcf.Found {
				if err := writeRecoveryFile(rcf, stored); err != nil {
					return fmt.Errorf("the state has a recovery recipient, but %s could not be written: %w. Write this line to it:\n%s", rcf.shown(), err, stored.Line())
				}
			}
			out.Printf("The state has a recovery recipient from an earlier run: any %d of %d shares rebuild its key. Commit %s.\n", stored.K, stored.N, rcf.shown())
			return nil
		}
		out.Println(out.C(ui.Yellow, "No recovery recipient. If every reader loses their key, the state cannot be read."))
		out.Println("Add one with: pravo protect rewrap --recovery 3/5")
		return nil
	}
	if err := writeRecoveryFile(rcf, rec); err != nil {
		return fmt.Errorf("the recovery is in the state, but %s could not be written: %w. Write this line to it:\n%s", rcf.shown(), err, rec.Line())
	}
	printShares(rec, shares, rcf)
	return nil
}

func readersInclude(rs []protect.Reader, id *protect.Identity) bool {
	me := id.Reader.Public()
	for _, r := range rs {
		if r.Key.Equal(me) {
			return true
		}
	}
	return false
}

// elapsed writes a duration as 41 s, or 0.4 s when it is short.
func elapsed(d time.Duration) string {
	if d < 10*time.Second {
		return fmt.Sprintf("%.1f s", d.Seconds())
	}
	return fmt.Sprintf("%d s", int(d.Round(time.Second).Seconds()))
}

// confirm asks a question that is answered with y or yes. With assume set it
// asks nothing. It refuses to ask when stdin is not a terminal, or when the
// output is JSON, because then nobody sees the question.
func confirm(prompt string, assume bool) (bool, error) {
	if assume {
		return true, nil
	}
	if flagJSON {
		return false, errors.New("refusing to ask with --json; pass --yes")
	}
	if !stdinIsTerminal() {
		return false, errors.New("refusing to ask, stdin is not a terminal; pass --yes")
	}
	out.Printf("%s [y/N] ", prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

// ---------- protect status ----------

func protectStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether the state is sealed, to whom, and whether it verifies",
		Long: `Opens the state with your identity, checks every immutable record against its
name and every pointer against its record, and prints one screen: whether the
state is sealed and to how many readers, whether the readers file matches the
store, who you are in relation to it, and whether anything was left unfinished.
It writes nothing.

It exits with 7 if a record does not match its name.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newProtectRun(cmd)
			if err != nil {
				return err
			}
			return p.status()
		},
	}
}

func (p *protectRun) status() error {
	b, err := p.backend(false)
	if err != nil {
		return err
	}
	st, id, idErr, err := p.open(b, false, true)
	if errors.Is(err, state.ErrUntrusted) {
		return reportUntrusted(err)
	}
	if err != nil {
		return err
	}
	prot := st.Protection()
	rows := [][2]string{}
	add := func(k, v string) { rows = append(rows, [2]string{k, v}) }
	sealed := prot.State == "sealed" || prot.State == "migrating" || prot.State == "unsealing"
	// A state that does not exist yet is read as an empty one of format 1, and
	// nothing is created. What is true of it is that there is none.
	_, metaErr := b.Get(p.ctx, metaKey)
	none := errors.Is(metaErr, state.ErrNotFound) && prot.Records == 0 && !sealed
	switch {
	case none:
		if p.rf.Found {
			add("Sealed", "no state yet · create it sealed with: pravo protect init --encrypt")
		} else {
			add("Sealed", "no state yet")
		}
	case prot.State == "sealed":
		names := protectedNames(prot)
		recov := "recovery none"
		if r := prot.Sealed.Recovery; r != nil {
			recov = fmt.Sprintf("recovery %d of %d", r.K, r.N)
		}
		add("Sealed", fmt.Sprintf("on · format %d · %s (%s) · %s", prot.Format, plural(len(names), "reader", "readers"), nameList(names), recov))
	case prot.State == "migrating":
		done, total := 0, prot.Records
		if m := prot.Migration; m != nil {
			done, total = m.Done, m.Total
		}
		add("Sealed", fmt.Sprintf("migrating · %s of %s records · run pravo protect init --encrypt to finish", commas(done), commas(total)))
	case prot.State == "unsealing":
		done, total := 0, prot.Records
		if m := prot.Migration; m != nil {
			done, total = m.Done, m.Total
		}
		add("Sealed", fmt.Sprintf("unsealing · %s of %s records · run pravo protect unseal to finish", commas(done), commas(total)))
	case prot.State == "immutable":
		line := fmt.Sprintf("off · format %d · immutable records", prot.Format)
		if n := len(prot.StraySealed); n > 0 {
			line += fmt.Sprintf(" · %s sealed, and meta.json says the state is not: it was changed", plural(n, "record is", "records are"))
		}
		add("Sealed", line)
	default:
		add("Sealed", fmt.Sprintf("off · format %d", prot.Format))
	}
	rf := p.rf
	expects := expectsSealed(prot, rf)
	if expects && !none {
		rows[len(rows)-1][1] += " · the repository expects a sealed state"
	}
	add("Store", p.location())
	switch {
	case !rf.Found:
		add("Readers", fmt.Sprintf("file not found (looked in %s)", rf.shownDir()))
	case rf.Err != nil:
		add("Readers", "file is invalid: "+rf.problem())
	case !sealed:
		add("Readers", fmt.Sprintf("%s · %s", rf.shown(), plural(len(rf.Readers), "reader", "readers")))
	default:
		added, removed := readersDiff(prot, rf)
		if len(added) == 0 && len(removed) == 0 {
			add("Readers", rf.shown()+" matches the store")
		} else {
			add("Readers", differsText(added, removed))
		}
	}
	if sealed {
		switch {
		case prot.You.State == "reader":
			add("You", fmt.Sprintf("%s · reader key %s", prot.You.Name, prot.You.Key))
		case id != nil:
			add("You", "identity "+id.Name+" is not a reader")
		case idErr != nil && !errors.Is(idErr, errNoIdentity):
			add("You", "not a reader of this state ("+strings.TrimSuffix(idErr.Error(), ".")+")")
		default:
			add("You", "not a reader of this state (no identity found)")
		}
	}
	recoveryFileState := ""
	if prot.State == "sealed" && (prot.Sealed.Recovery != nil || loadRecoveryFile(rf).Found) {
		var text string
		text, recoveryFileState = recoveryStatusText(prot, rf)
		add("Recovery", text)
	}
	add("Roles", "off")
	txns, err := st.Transactions()
	if err != nil {
		return err
	}
	var sigs *sigCounts
	if prot.Sealed != nil {
		if readers, ok := attributionReaders(rf); ok {
			c := countSignatures(st.Lineage(), txns, readers)
			sigs = &c
			add("Signatures", c.text(len(txns)))
		} else {
			add("Signatures", notChecked(rf))
		}
	}
	interrupted, err := st.Interrupted(txns)
	if err != nil {
		return err
	}
	if n := len(interrupted); n == 0 {
		add("Unsettled", "none")
	} else {
		add("Unsettled", plural(n, "interrupted transaction", "interrupted transactions")+" (pravo state log)")
	}
	ver, err := st.VerifyAll(p.ctx)
	if err != nil {
		return err
	}
	if flagJSON {
		doc := map[string]any{"state": prot.State, "format": prot.Format, "store": p.location(), "records": prot.Records,
			"readers": protectedNames(prot), "checked": ver.Checked, "untrusted": ver.Bad, "unsettled": len(interrupted)}
		if none {
			doc["state"] = "none"
			delete(doc, "format")
		}
		if expects {
			doc["expects_sealed"] = true
		}
		if sigs != nil {
			doc["signatures"] = sigs
		}
		if sealed {
			doc["you"] = prot.You.State
			added, removed := readersDiff(prot, rf)
			if r := prot.Sealed.Recovery; r != nil {
				doc["recovery"] = map[string]any{"k": r.K, "n": r.N, "key": r.Key.String(), "file": recoveryFileState,
					"locked": prot.Sealed.Status(nil).Recovery.Locked}
			}
			doc["readers_file"] = map[string]any{"path": rf.shown(), "found": rf.Found, "valid": rf.Found && rf.Err == nil, "matches": rf.Found && rf.Err == nil && len(added) == 0 && len(removed) == 0, "added": added, "removed": removed}
		}
		if err := emitJSON(doc); err != nil {
			return err
		}
	} else {
		for _, r := range rows {
			out.Printf("%-12s %s\n", r[0], r[1])
		}
		for _, bad := range ver.Bad {
			out.Println(out.C(ui.Red, "Untrusted: "+printable(bad)+" does not match its name"))
		}
		if sigs != nil {
			for _, id := range sigs.Bad {
				out.Println(out.C(ui.Red, "Untrusted: txn/"+printable(id)+".json has a signature that does not verify"))
			}
		}
	}
	if len(ver.Bad) > 0 {
		return fmt.Errorf("%w: %s", state.ErrUntrusted, plural(len(ver.Bad), "object does", "objects do")+" not match")
	}
	if sigs != nil && sigs.Invalid > 0 {
		return invalidTxns(sigs.Invalid)
	}
	return nil
}

// reportUntrusted prints the object that failed verification when the state
// could not be opened because of it. The error is the one of the state package:
// "rec/SHA256: does not match its name (ADDRESS): the state failed verification".
func reportUntrusted(err error) error {
	msg := printable(strings.TrimSuffix(err.Error(), ": "+state.ErrUntrusted.Error()))
	msg = strings.Replace(msg, ": does not match", " does not match", 1)
	out.Println(out.C(ui.Red, "Untrusted: "+msg))
	return engine.WithCode(engine.ExitUntrusted, errSilent)
}

// protectedNames lists the readers a sealed state has locks for, in order of name.
func protectedNames(prot state.Protection) []string {
	if prot.Sealed == nil {
		return nil
	}
	st := prot.Sealed.Status(nil)
	var names []string
	for _, r := range st.Readers {
		names = append(names, safeName(r.Name))
	}
	sort.Strings(names)
	return names
}

// ---------- protect rewrap ----------

func protectRewrapCmd() *cobra.Command {
	var all, removeRecovery, yes bool
	var recovery string
	cmd := &cobra.Command{
		Use:   "rewrap",
		Short: "Bring the keys of the state in line with the readers file",
		Long: `Compares the readers file with the readers the state is sealed to, and
changes the state to match.

A reader who was added gets the data keys of every group, so that they can read
what is there. This rewrites meta.json and nothing else.

A reader who was removed loses their lock in every group, and the state gets a
new data key. Records are sealed under it as they are next written. They stay
under the old key until then, and a copy of the state that was made before the
removal still opens with the old key. --all seals every record, the outputs and
the stored plans again under the new key now.

--recovery K/N sets the recovery recipient of the state, or replaces it: a new key
is split into N shares, any K of which rebuild it. The shares are written to
recovery-shares/, and the public key to the recovery file next to the readers
file. Give each share to a different person, offline, and commit the recovery
file. A replaced recovery key opens nothing afterwards, and the old shares are
worth nothing, so replacing or removing a recovery asks first (--yes does not).
--remove-recovery takes the recovery out of the state. Without
either, a recovery file that names a key that the state does not have is set in the
state, and a recovery lock that is missing from a group is made again.

It needs an identity that is a reader of the state, and the readers file.
--all does not run while a command is running or a lock is held (exit 4).`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newProtectRun(cmd)
			if err != nil {
				return err
			}
			return p.rewrap(all, recovery, removeRecovery, yes)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "seal every record, the outputs and the stored plans again under the current key")
	cmd.Flags().StringVar(&recovery, "recovery", "", "set or replace the recovery recipient, as K/N: N shares, any K rebuild its key")
	cmd.Flags().BoolVar(&removeRecovery, "remove-recovery", false, "take the recovery recipient out of the state")
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask before a recovery is replaced or removed")
	return cmd
}

func (p *protectRun) rewrap(all bool, recoverySpec string, removeRecovery, yes bool) error {
	if recoverySpec != "" && removeRecovery {
		return errors.New("set the recovery or remove it, not both")
	}
	var newRec *protect.Recovery
	var shares []protect.Share
	if recoverySpec != "" {
		var err error
		if newRec, shares, err = newRecovery(recoverySpec); err != nil {
			return err
		}
	}
	b, err := p.backend(true)
	if err != nil {
		return err
	}
	st, id, idErr, err := p.open(b, true, true)
	if err != nil {
		return err
	}
	prot := st.Protection()
	if prot.State == "unsealing" {
		return errors.New("an unseal is in progress; finish it with pravo protect unseal")
	}
	if prot.State != "sealed" {
		return errors.New("the state is not sealed; see pravo protect check")
	}
	if st.Locked() || prot.You.State != "reader" {
		return engine.WithCode(engine.ExitRefused, notReaderError(p.rf, id, idErr))
	}
	if err := p.rf.require(); err != nil {
		return err
	}
	added, removed := readersDiff(prot, p.rf)
	// Without a flag the repository decides: a recovery file that names a key that
	// the state does not have is set in the state, without new shares.
	rcf := loadRecoveryFile(p.rf)
	setRec, fromFile := newRec, false
	if setRec == nil && !removeRecovery && rcf.Found {
		if rcf.Err != nil {
			return engine.WithCode(engine.ExitInvalid, fmt.Errorf("%s: %s", rcf.shown(), rcf.Err))
		}
		if !rcf.Rec.Equal(prot.Sealed.Recovery) {
			setRec, fromFile = rcf.Rec, true
		}
	}
	removing := removeRecovery && prot.Sealed.Recovery != nil
	// A recovery file that the state has no recovery for is the repository's mistake: it goes.
	fileRemoving := removeRecovery && !removing && rcf.Found
	if len(added) == 0 && len(removed) == 0 && !all && setRec == nil && !removing && !fileRemoving && len(prot.RecoveryUnverified) == 0 {
		if removeRecovery && !flagJSON {
			out.Println("The state has no recovery recipient, and there is no recovery file.")
			return nil
		}
		if flagJSON {
			return emitJSON(map[string]any{"added": nil, "removed": nil, "new_group": "", "resealed": 0})
		}
		out.Println("The store already matches the readers file.")
		return nil
	}
	replacing := newRec != nil && prot.Sealed.Recovery != nil
	if all {
		// --all deletes the records that nothing names; a writer that is half way
		// through a write has made one and not yet named it.
		if err := atRest(st); err != nil {
			return err
		}
	}
	if replacing || removing {
		what := "The recovery key will be replaced"
		if removing {
			what = "The recovery recipient will be removed"
		}
		out.Printf("%s (%d of %d shares, key %s). The shares that were given out open nothing that is written from now on.\n",
			what, prot.Sealed.Recovery.K, prot.Sealed.Recovery.N, prot.Sealed.Recovery.Key.Short())
		if ok, err := confirm("Proceed?", yes); err != nil {
			return err
		} else if !ok {
			return errors.New("protect rewrap cancelled; nothing was written")
		}
	}
	var removeShares func()
	if newRec != nil {
		if removeShares, err = writeShares(shares); err != nil {
			return err
		}
	}
	began := time.Now()
	res, err := st.Rewrap(p.ctx, state.RewrapOptions{
		Readers: p.rf.Readers, All: all, By: id.Name, Recovery: setRec, RemoveRecovery: removing,
		GuardRecovery: setRec != nil || removing, RecoveryWas: prot.Sealed.Recovery,
		Plans: engine.NewPlanStoreFor(st).ResealAll,
	})
	if err != nil && newRec != nil {
		p.afterFailure(newRec, removeShares, rcf)
	}
	if errors.Is(err, state.ErrRecoveryChanged) {
		return engine.WithCode(engine.ExitStale, errors.New("another command changed the recovery of the state while this ran, so nothing was set and any new shares were removed; run it again"))
	}
	if errors.Is(err, state.ErrChanged) {
		return engine.WithCode(engine.ExitStale, errors.New("meta.json changed while rewrapping; run it again"))
	}
	if err != nil {
		return err
	}
	// The files of the recovery are settled before anything is reported.
	var recNote func()
	var recJSON map[string]any
	switch {
	case newRec != nil:
		if err := writeRecoveryFile(rcf, newRec); err != nil {
			return fmt.Errorf("the new recovery is in the state, but %s could not be written: %w. Write this line to it:\n%s", rcf.shown(), err, newRec.Line())
		}
		recJSON = recoveryJSON("set", newRec, rcf, true)
		if replacing {
			recJSON["action"] = "replaced"
		}
		recNote = func() {
			printShares(newRec, shares, rcf)
			if replacing {
				out.Println("The old shares open nothing that is written from now on. Collect them and destroy them.")
			}
		}
	case fromFile:
		recJSON = recoveryJSON("from_file", setRec, rcf, false)
		recNote = func() {
			out.Printf("Recovery recipient set from %s: key %s, any %d of %d shares rebuild its key.\n", rcf.shown(), setRec.Key.Short(), setRec.K, setRec.N)
		}
	case removing || fileRemoving:
		if err := os.Remove(rcf.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		recJSON = recoveryJSON("removed", nil, rcf, false)
		recNote = func() {
			if removing {
				out.Printf("Recovery removed. Commit the deletion of %s, and destroy the shares.\n", rcf.shown())
			} else {
				out.Printf("The state has no recovery recipient. Removed %s; commit the deletion.\n", rcf.shown())
			}
		}
	}
	if flagJSON {
		j := map[string]any{"added": res.Change.Added, "removed": res.Change.Removed, "new_group": res.Change.NewGroup, "resealed": res.Resealed, "plans_skipped": res.PlansSkipped}
		if recJSON != nil {
			j["recovery"] = recJSON
		}
		return emitJSON(j)
	}
	line := "Readers in " + p.rf.shown() + ": " + nameList(readerNames(p.rf.Readers))
	if len(res.Change.Removed) > 0 {
		line += "  (" + nameList(safeNames(res.Change.Removed)) + " removed)"
	}
	out.Println(line)
	if n := len(res.Change.Added); n > 0 {
		groups := len(prot.Sealed.Groups)
		out.Printf("Wrapped the data keys for %s (%s, %s)\n", nameList(res.Change.Added),
			plural(groups, "reader group", "reader groups"), plural(groups*n, "new wrapped key", "new wrapped keys"))
	}
	if res.Change.NewGroup != "" {
		if all {
			out.Println("New data keys for 1 reader group.")
		} else {
			out.Println("New data keys for 1 reader group. Records are sealed again as they are next written.")
		}
	}
	if all {
		out.Printf("Sealed %s again in %s.\n", counted(res.Resealed, "record", "records"), elapsed(time.Since(began)))
		if res.PlansSkipped > 0 {
			out.Println(plansInUseLine(res.PlansSkipped, true))
		}
		// A plan that an apply uses is left alone, and stays as readable as it was.
		if entries, _, err := engine.NewPlanStoreFor(st).EntriesAndSkipped(p.ctx, engine.PlanCheckFor(st)); err == nil {
			warnUnsealed(unsealedPlans(entries))
		}
	} else if len(res.Change.Added) > 0 || len(res.Change.Removed) == 0 {
		out.Println(out.C(ui.Green, "meta.json updated"))
	}
	if recNote != nil {
		recNote()
	}
	return nil
}

// plansInUseLine says that n stored plans were left as they were because
// something uses them, in the number that n takes. again is set for the plans that
// a rewrap leaves under the group they were sealed in, and not for those that an
// upgrade leaves in the clear.
func plansInUseLine(n int, again bool) string {
	sealed := "sealed"
	if again {
		sealed = "sealed again"
	}
	if n == 1 {
		return "1 stored plan in use was not " + sealed + "; run pravo protect rewrap --all when it is done."
	}
	return fmt.Sprintf("%d stored plans in use were not %s; run pravo protect rewrap --all when they are done.", n, sealed)
}
