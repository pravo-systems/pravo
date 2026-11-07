package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/ui"
	"github.com/pravo-systems/pravo/pravo"
)

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

// requireReader refuses (exit 5) when the key that opened the state is not a reader of it.
func (p *protectRun) requireReader(st *state.Store, prot state.Protection, id *protect.Identity, idErr error) error {
	if st.Locked() || prot.You.State != "reader" {
		return engine.WithCode(engine.ExitRefused, notReaderError(p.rf, id, idErr))
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

func readerNames(rs []pravo.Reader) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Name
	}
	return out
}

func nameList(names []string) string { return strings.Join(names, " ") }

// readersDiff compares the readers file with the readers of a sealed state.
// Added are in the file and not in the store; removed are the other way round.
func readersDiff(prot state.Protection, rf readersFile) (added, removed []string) {
	if prot.Sealed == nil {
		return nil, nil
	}
	st := prot.Sealed.Status(rf.Readers)
	return st.Missing, safeNames(st.Extra)
}

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
func missingStateProblem(rt *runContext, rf readersFile) string {
	return fmt.Sprintf("the repository lists readers in %s, but the state does not exist yet. Create it sealed with: %s", rf.shown(), strings.TrimSpace("pravo protect init --encrypt "+engine.TagFlags(rt.flags.Tags)))
}

// lostStateProblem is what a command says when meta.json is missing but the
// state still holds records. Running protect init would write a new data key and
// make those records unreadable.
func lostStateProblem(rf readersFile) string {
	return fmt.Sprintf("the repository lists readers in %s, but the state has no meta.json while its records, transactions or resources are still there. Do not run protect init: it would write a new data key and make those records unreadable. Restore meta.json from a bucket version (versioning keeps the deleted object), or from a copy of the state.", rf.shown())
}

// missingStateProblemOf picks between a state that does not exist yet and one
// that lost its meta.json.
func missingStateProblemOf(ctx context.Context, rt *runContext, root *config.Root, rf readersFile) string {
	if stateLost(ctx, stateBackendIfAny(ctx, root)) {
		return lostStateProblem(rf)
	}
	return missingStateProblem(rt, rf)
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
func checkReadersFile(rt *runContext, root *config.Root, st *state.Store, write bool) error {
	prot := st.Protection()
	rf := readersOf(rt, root)
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
	var problem string
	code := engine.ExitRefused
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
	if problem == "" && write {
		problem, code = roleProblem(rf, st, pravo.RoleWriter, "change the state")
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

func readersInclude(rs []pravo.Reader, id *protect.Identity) bool {
	me := id.Reader.Public()
	for _, r := range rs {
		if r.Key.Equal(me) {
			return true
		}
	}
	return false
}

// reportUntrusted prints the object that failed verification when the state
// could not be opened because of it. The error is the one of the state package:
// "rec/SHA256: does not match its name (ADDRESS): the state failed verification".
func reportUntrusted(rt *runContext, err error) error {
	msg := printable(strings.TrimSuffix(err.Error(), ": "+state.ErrUntrusted.Error()))
	msg = strings.Replace(msg, ": does not match", " does not match", 1)
	rt.out.Println(rt.out.C(ui.Red, "Untrusted: "+msg))
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

// roleProblem says why the identity of the store may not do what need allows, or is
// empty, with the exit code of the refusal. The roles are those that decide for the state:
// the roles in it when the readers file pins them, the roles of the file otherwise. A
// readers file without roles lets every key do everything. This is the check of an honest
// pravo; a state that was written by a key without the role is found by the signatures (see
// sealed-state.md), and only the credentials of the bucket stop a write.
func roleProblem(rf readersFile, st *state.Store, need pravo.Role, what string) (string, engine.ExitCode) {
	rf, rs, err := rf.withRoles(st)
	if err != nil {
		return printable(err.Error()), engine.ExitUntrusted
	}
	key := st.SigningKey()
	// Where the roles come from, as the refusal names it: the roles in the state when they
	// decide, and also when the state holds some that the readers file does not pin, since a
	// local file that dropped the pin line must not turn a reader into an admin.
	source := rf.shown()
	readers := rf.Readers
	switch {
	case rs.Source == "state":
		source = "the roles in the state"
	case rs.Unpinned:
		role := pravo.RoleReader
		if r, ok := pravo.RoleOf(rs.Stored, key); ok {
			role = r
		}
		if role.Allows(need) {
			return "", engine.ExitRefused
		}
		return fmt.Sprintf("your identity is %s in the roles stored in the state, and %s is needed to %s. %s does not pin those roles, so the roles in the state decide; commit the %s line that pravo protect roles printed.", article(role), article(need), what, rf.shown(), protect.RolesGenesisKey), engine.ExitRefused
	}
	if !rf.Found || rf.Err != nil || !pravo.RolesOn(readers) {
		return "", engine.ExitRefused
	}
	role, ok := pravo.RoleOf(readers, key)
	if !ok {
		return fmt.Sprintf("your identity is not in %s, so its role is not known, and %s is needed to %s.", source, article(need), what), engine.ExitRefused
	}
	if role.Allows(need) {
		return "", engine.ExitRefused
	}
	return fmt.Sprintf("your identity is %s in %s, and %s is needed to %s.", article(role), source, article(need), what), engine.ExitRefused
}

// article puts a or an before the name of a role.
func article(r pravo.Role) string {
	if r == pravo.RoleAdmin {
		return "an admin"
	}
	return "a " + r.String()
}

// unnamedNotice is the line printed by plan for a sealed state that holds resources which
// no signed transaction names.
func unnamedNotice(n int) string {
	return fmt.Sprintf("Notice: %s no signed transaction names, so a pointer deleted from the bucket may go unnoticed. Run pravo protect rewrap --all to record them.", plural(n, "resource whose pointer", "resources whose pointers"))
}

// noteUnnamed prints the notice for st when it applies.
func noteUnnamed(ctx context.Context, rt *runContext, root *config.Root, st *state.Store) {
	if readers, ok := attributionReaders(readersOf(rt, root)); ok {
		if n := st.Unnamed(ctx, readers); n > 0 {
			fmt.Fprintln(os.Stderr, unnamedNotice(n))
		}
	}
}

// dotSlash puts "./" before a path that is relative and stays inside the
// working directory, so that it reads as a path and not as a word.
func dotSlash(p string) string {
	if filepath.IsAbs(p) || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "./") {
		return p
	}
	return "./" + p
}
