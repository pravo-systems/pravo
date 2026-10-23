package serve

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/state"
)

// readersFile is the name of the file of readers, in the access directory.
const readersFile = "readers.txt"

// The access document is built on the server, once for each revision, from the
// facts of the protection (see Protection) and fixed templates. The page renders
// it and runs nothing. Everything that comes from the store or the file is
// untrusted: a name can be anything a bucket holds, so a name reaches a command
// only if it matches the rule for names, and every word of a command is quoted
// as the shell needs.

var (
	// readerNameRE is the rule for the name of a reader, the one the readers file
	// uses. It is also what makes a name safe in a command.
	readerNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	// readerKeyRE is the text of a reader key: x25519: and the unpadded base64url
	// of 32 bytes.
	readerKeyRE = regexp.MustCompile(`^x25519:[A-Za-z0-9_-]{43}$`)
	// sshBlobRE is the base64 of an ssh-ed25519 public key as the readers file
	// writes it.
	sshBlobRE = regexp.MustCompile(`^[A-Za-z0-9+/]{16,1000}={0,2}$`)
	// quotedRE finds a quoted piece of a reason. The readers file parser quotes the
	// token it rejects, and a token is the text of the line.
	quotedRE = regexp.MustCompile(`"[^"]*"`)
)

// The add-reader suggestion has two titles: a state that one reader can open asks
// for a backup, and the warning under the readers opens it by its ID.
const (
	addReaderTitle = "Add a reader"
	addBackupTitle = "Add a backup reader"
)

const (
	maxAccessReaders = 200 // readers listed
	maxAccessErrors  = 20  // lines of the readers file listed as not loading
	maxRemovals      = 50  // "remove a reader" suggestions
	maxDisplay       = 64  // runes of a name shown
	maxLineReason    = 200 // bytes of a reason
)

// defaultAccessDir is where the readers file is looked for when no directory is
// given: .pravo/access in the project directory, else in the nearest parent up to
// the one that holds .git, since the repository can be above the project. When
// there is none, the project directory's.
func defaultAccessDir(dir string) string {
	here := filepath.Join(dir, ".pravo", "access")
	for d := dir; ; {
		cand := filepath.Join(d, ".pravo", "access")
		if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
			return cand
		}
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return here
}

// accessPath is the readers file the viewer reads and the watcher signs.
func (s *Server) accessPath() string {
	dir := s.opts.AccessDir
	switch {
	case dir == "":
		dir = defaultAccessDir(s.opts.Dir)
	case !filepath.IsAbs(dir):
		dir = filepath.Join(s.opts.Dir, dir)
	}
	return filepath.Join(dir, readersFile)
}

// protection asks for the facts of the protection of the store. Without a
// Protection in the options they are what the format of the store says, with no
// identity. A Protection that panics does not take the model down: the facts are
// then the default ones, with a problem to tell on the page.
func (s *Server) protection(ctx context.Context, store *state.Store, path string) (f Facts) {
	if s.opts.Protection != nil {
		defer func() {
			if recover() != nil {
				f = defaultFacts(store, path)
				f.Problem = "The protection of the state could not be read."
			}
		}()
		return s.opts.Protection(ctx, store, path)
	}
	return defaultFacts(store, path)
}

// defaultFacts are the facts of a store from its format alone.
func defaultFacts(store *state.Store, path string) Facts {
	f := Facts{File: FileFacts{Path: path}}
	f.Store.Format = store.Format()
	f.Store.State = stateOfStore(store)
	f.Store.Records = store.Len()
	if f.Store.State == "sealed" {
		f.Store.Locked = true
		f.You = &YouFacts{State: "no-identity"}
	}
	return f
}

// stateOfStore is the state the store says it is in. An unseal in progress is shown as
// a change in progress, as the viewer has one name for both.
func stateOfStore(store *state.Store) string {
	if s := store.Protection().State; s == "unsealing" {
		return "migrating"
	} else if s != "" {
		return s
	}
	return stateOfFormat(store.Format())
}

func stateOfFormat(format int) string {
	switch {
	case format >= state.FormatSealed:
		return "sealed"
	case format >= state.FormatImmutable:
		return "immutable"
	}
	return "off"
}

// accessModel is what the model keeps of the facts: whether the state is sealed,
// and whether this process can open the values.
type accessModel struct {
	facts     Facts
	protected bool // the state is sealed or migrating
	locked    bool // the values cannot be opened
	// unreadable is set when the store counts records sealed under a group that this
	// key holds no lock for: those come without attributes.
	unreadable bool
}

func newAccessModel(f Facts, store *state.Store) accessModel {
	switch f.Store.State {
	case "off", "immutable", "sealed", "migrating":
	default:
		// A state that the facts do not name is the one that the format says, and the page says that it could not be told.
		if f.Store.State != "" && f.Problem == "" {
			f.Problem = "The protection of the state could not be told."
		}
		f.Store.State = stateOfStore(store)
	}
	a := accessModel{facts: f}
	a.protected = f.Store.State == "sealed" || f.Store.State == "migrating"
	a.locked = a.protected && (f.Store.Locked || store.Locked())
	a.unreadable = a.protected && f.Store.Unreadable > 0
	return a
}

// sealedRec reports whether a record is sealed and cannot be opened: the state is
// sealed, and either no key was given, or the store says that some records are
// sealed under a group this key cannot open and this one came without attributes.
func (a accessModel) sealedRec(rec *state.Resource) bool {
	if rec == nil || !a.protected {
		return false
	}
	return a.locked || (a.unreadable && len(rec.Attributes) == 0)
}

// accessInput is what the document of one revision is made from.
type accessInput struct {
	rev      string
	location string
	// backend is "local" or "s3".
	backend string
	// format and records are the format and the number of records the store reports.
	format, records int
	facts           Facts
	// path is the readers file; dir the project directory, which paths are shown
	// relative to.
	path, dir string
	// locked is set when the values cannot be opened.
	locked bool
}

// accessDoc builds the document of one revision.
func (m *model) accessDoc(path string) *Access {
	in := accessInput{rev: m.rev, location: m.store.Dir(), backend: "local", format: m.store.Format(), records: m.store.Len(),
		facts: m.acc.facts, path: path, dir: m.srv.opts.Dir, locked: m.acc.locked}
	if m.root != nil && m.root.State.Backend != "" {
		in.backend = m.root.State.Backend
	}
	return buildAccess(in)
}

// buildAccess makes the document from the facts. It reads nothing.
func buildAccess(in accessInput) *Access {
	f := in.facts
	st := f.Store
	a := &Access{Version: APIVersion, Revision: in.rev, Format: in.format, Location: in.location, Backend: in.backend,
		Readers: []AccessReader{}, Suggestions: []Suggestion{}}
	a.State = st.State
	switch a.State {
	case "off", "immutable", "sealed", "migrating":
	default:
		a.State = stateOfFormat(a.Format)
	}
	if f.Problem != "" {
		a.Problems = append(a.Problems, f.Problem)
	}
	if st.Format > 0 {
		a.Format = st.Format
	}
	protected := a.State == "sealed" || a.State == "migrating"

	rows, added, removed := compareReaders(st.Readers, f.File.Readers, protected, f.You)
	if len(rows) > maxAccessReaders {
		rows = rows[:maxAccessReaders]
	}
	a.Readers = rows
	a.File = fileVerdict(a.State, f.File, in.path, in.dir, added, removed)
	storeReaders := 0
	for _, r := range rows {
		if r.InStore {
			storeReaders++
		}
	}

	a.Records = AccessRecords{Total: st.Records, Sealed: st.Sealed, Plain: st.Plain, Unreadable: st.Unreadable}
	if a.Records.Total == 0 {
		a.Records.Total = in.records
	}
	noKey := false
	if protected {
		if (in.locked || st.Locked) && a.Records.Unreadable == 0 {
			// With no key every record that is sealed is out of reach.
			a.Records.Unreadable = max(a.Records.Total-a.Records.Plain, 0)
		}
		// A key that is not a reader of the state was given: what it cannot open is
		// out of reach of this key, not for want of one.
		noKey = (in.locked || st.Locked) && (f.You == nil || f.You.State != "not-reader")
		// A single group is the ordinary state and says nothing that a person decides on.
		if len(st.Groups) > 1 {
			for _, g := range st.Groups {
				a.Groups = append(a.Groups, AccessGroup{ID: display(g.ID), Current: g.Current, Locks: g.Locks, Records: g.Records})
			}
			a.GroupsText = groupsText
		}
		a.You = youOf(f.You)
	}
	a.Records.Text = recordsText(a.Records, protected, noKey)
	if st.Migration != nil && a.State == "migrating" {
		mg := st.Migration
		a.Migration = &AccessMigration{Started: mg.Started.UTC(), By: display(mg.By), Done: mg.Done, Total: mg.Total, Unseal: mg.Unseal}
	}
	a.Summary = summaryOf(a, storeReaders)
	if protected && st.Recovery != nil {
		a.Recovery = recoveryOf(st.Recovery)
	}
	// One reader is a risk that a recovery answers.
	if a.State == "sealed" && storeReaders == 1 && st.Recovery == nil {
		a.SingleReader = singleReader(a.You)
	}
	if n := len(st.History); n > 0 && protected {
		a.LastChange = changeOf(st.History[n-1])
	}
	blobs := map[string]string{}
	for _, r := range f.File.Readers {
		blobs[r.Key] = r.SSH
	}
	a.Suggestions = suggestions(a, in.dir, in.backend != "s3", blobs)
	return a
}

// display makes a name from the store or the file fit to be shown: a valid
// string of at most maxDisplay characters with no control or direction
// character, which can hide what a line says.
func display(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "?")
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			continue
		}
		if n++; n > maxDisplay {
			b.WriteString("…")
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

// cleanReason makes the reason a parser gave for a line fit to be shown: no
// quoted token (it is a piece of the line), no control character, clipped.
func cleanReason(s string) string {
	s = quotedRE.ReplaceAllString(s, "…")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, "?"))
	return clip(strings.TrimSpace(s), maxLineReason)
}

// shortKey is the reader key as it is shown: the start of it.
func shortKey(key string) string {
	if len(key) > len("x25519:")+6 {
		return key[:len("x25519:")+6] + "…"
	}
	return key
}

// fingerprint is the SHA256 fingerprint of the signing key of a readers line, as
// ssh-keygen -l prints it, "" for a blob that does not decode.
func fingerprint(blob string) string {
	raw, err := base64.StdEncoding.Strict().DecodeString(blob)
	if err != nil || len(raw) == 0 {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// readerLine is the line of the readers file for a reader, made from its parsed
// parts, and false if a part is not what it should be. The text of the file is
// never used.
func readerLine(r AccessReader, blob string) (string, bool) {
	if !r.NameOK || !readerKeyRE.MatchString(r.Key) || !sshBlobRE.MatchString(blob) {
		return "", false
	}
	return r.Name + " ssh-ed25519 " + blob + " " + r.Key, true
}

// compareReaders lists the readers of the file and of the store, matched by
// reader key as the locks of the store are: the file's first, in file order, then
// those only the store lists. added are the names in the file and not in the
// store, removed the ones in the store and not in the file.
func compareReaders(store, file []ReaderFacts, protected bool, you *YouFacts) (rows []AccessReader, added, removed []string) {
	rows = []AccessReader{}
	at := map[string]int{}
	row := func(r ReaderFacts, i int, side string) *AccessReader {
		key := r.Key
		if !readerKeyRE.MatchString(key) {
			key = ""
		}
		id := key
		if id == "" {
			id = side + strconv.Itoa(i) // a reader without a key matches nobody
		}
		if j, ok := at[id]; ok {
			return &rows[j]
		}
		ar := AccessReader{Name: display(r.Name), NameOK: readerNameRE.MatchString(r.Name), Key: key, KeyShort: shortKey(key)}
		if you != nil && key != "" && you.Key == key {
			ar.You = true
		}
		rows = append(rows, ar)
		at[id] = len(rows) - 1
		return &rows[len(rows)-1]
	}
	for i, r := range file {
		ar := row(r, i, "f")
		ar.InFile, ar.Line = true, r.Line
		ar.Name, ar.NameOK = display(r.Name), readerNameRE.MatchString(r.Name)
		ar.Fingerprint = fingerprint(r.SSH)
	}
	for i, r := range store {
		ar := row(r, i, "s")
		ar.InStore = true
		if !ar.InFile {
			ar.Name, ar.NameOK = display(r.Name), readerNameRE.MatchString(r.Name)
		}
	}
	if protected {
		for _, ar := range rows {
			switch {
			case ar.InFile && !ar.InStore:
				added = append(added, ar.Name)
			case ar.InStore && !ar.InFile:
				removed = append(removed, ar.Name)
			}
		}
	}
	return rows, added, removed
}

// fileVerdict says what the readers file holds in relation to the store.
func fileVerdict(stateName string, f FileFacts, path, dir string, added, removed []string) AccessFile {
	af := AccessFile{Path: path, Verdict: "none"}
	if f.Path != "" {
		af.Path = f.Path
	}
	for i, e := range f.Errors {
		if i == maxAccessErrors {
			break
		}
		af.Errors = append(af.Errors, AccessLineError{Line: e.Line, Reason: cleanReason(e.Reason)})
	}
	af.Count = len(f.Readers)
	where := shortPath(dir, filepath.Dir(af.Path))
	switch {
	case stateName != "sealed" && stateName != "migrating":
		af.Verdict = "none"
		switch f.State {
		case "ok":
			// The repository says who may read, so a state that is not sealed has not
			// been sealed yet, or was sealed once and its meta.json was changed.
			af.Verdict = "expected-sealed"
			af.Text = "The repository lists readers in " + shortPath(dir, af.Path) + ", but the state is not sealed. It has not been sealed yet, or its meta.json was changed."
		case "invalid":
			af.Text = "The readers file does not load" + firstError(af.Errors) + "."
		default:
			af.Text = "The state is not sealed, so there is no list of readers to compare."
		}
	case f.State == "missing":
		af.Verdict = "missing"
		af.Text = "No readers file was found in " + where + ". Writers take the list of readers from the repository, so a sealed state cannot be written from this checkout."
	case f.State == "invalid":
		af.Verdict = "invalid"
		af.Text = "The readers file does not load" + firstError(af.Errors) + "."
	case f.State == "ok":
		if len(added) == 0 && len(removed) == 0 {
			af.Verdict = "match"
			af.Text = "The readers file matches the store."
			break
		}
		af.Verdict = "differs"
		af.Added, af.Removed = added, removed
		var parts []string
		if len(added) > 0 {
			parts = append(parts, nameList(added)+" in the file and not in the store")
		}
		if len(removed) > 0 {
			parts = append(parts, nameList(removed)+" in the store and not in the file")
		}
		af.Text = "The readers file and the store differ: " + strings.Join(parts, "; ") + ". Writers refuse to write until they match."
	default:
		af.Text = "The readers file was not read."
	}
	return af
}

func firstError(errs []AccessLineError) string {
	if len(errs) == 0 {
		return ""
	}
	if errs[0].Line == 0 {
		return ": " + errs[0].Reason
	}
	return fmt.Sprintf(": line %d: %s", errs[0].Line, errs[0].Reason)
}

// nameList joins names for a sentence: "dan", "dan and eve", "dan, eve and fay"
// with "is" or "are" after it.
func nameList(names []string) string {
	const most = 5
	shown := names
	if len(shown) > most {
		shown = shown[:most]
	}
	var s string
	switch len(shown) {
	case 1:
		s = shown[0]
	default:
		s = strings.Join(shown[:len(shown)-1], ", ") + " and " + shown[len(shown)-1]
	}
	if more := len(names) - len(shown); more > 0 {
		s = strings.Join(shown, ", ") + " and " + strconv.Itoa(more) + " more"
	}
	if len(names) == 1 {
		return s + " is"
	}
	return s + " are"
}

// shortPath shows a path relative to the project directory when it is near it.
func shortPath(dir, path string) string {
	if rel, err := filepath.Rel(dir, path); err == nil && !strings.HasPrefix(rel, "../../../") {
		return filepath.ToSlash(rel)
	}
	return path
}

func youOf(y *YouFacts) *AccessYou {
	if y == nil {
		y = &YouFacts{State: "no-identity"}
	}
	you := &AccessYou{State: y.State, Name: display(y.Name)}
	switch y.State {
	case "reader":
		if you.Name == "" {
			you.Text = "You are a reader."
		} else {
			you.Text = "You are " + you.Name + ", a reader."
		}
	case "not-reader":
		you.Text = "This key is not a reader of the state. Resources are listed and no value is shown."
	case "locked":
		you.Text = "The identity is sealed with a passphrase and none was available. Resources are listed and no value is shown."
	default:
		you.State = "no-identity"
		you.Text = "No identity was given. Resources are listed and no value is shown."
	}
	return you
}

// groupsText says why a state has more than one group and what sealing the records
// again does. A group is a data key; a removal of a reader makes a new one.
const groupsText = "A removal starts a new group, and records stay under the old group until they are written again or resealed with rewrap --all."

// recordsText is the one sentence about the records that the page shows. For a
// state that is sealed it says that all are sealed, or what keeps a record from
// being: that this key cannot open it (noKey: that none was given), or that a seal
// did not reach it. It is empty for a state that holds no record.
func recordsText(r AccessRecords, protected, noKey bool) string {
	total := max(r.Total, r.Plain+r.Unreadable)
	switch {
	case total == 0:
		return ""
	case !protected:
		return fmt.Sprintf("%s %s, not sealed.", commas(total), plural(total, "record", "records"))
	case r.Unreadable > 0 && noKey:
		return fmt.Sprintf("%s %s sealed and no key was given to open them.", commas(r.Unreadable), plural(r.Unreadable, "record is", "records are"))
	case r.Unreadable > 0:
		return fmt.Sprintf("%s %s sealed under a group that this key cannot open.", commas(r.Unreadable), plural(r.Unreadable, "record is", "records are"))
	case r.Plain > 0 && total == 1:
		// Only a seal that is not finished leaves records in the clear, and writers are
		// refused until it is.
		return "1 record is not sealed yet."
	case r.Plain > 0:
		return fmt.Sprintf("%s of %s records %s not sealed yet.", commas(r.Plain), commas(total), plural(r.Plain, "is", "are"))
	case total == 1:
		return "The only record is sealed."
	}
	return fmt.Sprintf("All %s records are sealed.", commas(total))
}

func summaryOf(a *Access, readers int) string {
	switch a.State {
	case "immutable":
		return "Records are stored as immutable objects. They are not encrypted."
	case "sealed":
		return fmt.Sprintf("The state is sealed to %d %s. Attributes, outputs and stored plans are encrypted. Addresses, types, names, dependencies and revisions stay readable, so the graph and the plans keep working.",
			readers, plural(readers, "reader", "readers"))
	case "migrating":
		s := "Sealing started"
		if mg := a.Migration; mg != nil && mg.Unseal {
			s = "Unsealing started"
		}
		if mg := a.Migration; mg != nil {
			if mg.By != "" {
				s += " by " + mg.By
			}
			s += " on " + mg.Started.Format("2006-01-02 15:04") + " UTC and has not finished."
		} else {
			s += " and has not finished."
		}
		return s + " Writers are refused until it finishes."
	}
	return "The state is not sealed. Attributes, outputs and stored plans are stored as plain text, and anyone who can read " + a.Location + " can read every value."
}

// changeOf makes the last change of the seal. Its text is the item of the banner:
// the time in UTC, then what was done and the label that the machine of the change
// gave itself, such as "2026-10-23 08:03 UTC · sealed by marcinw".
func changeOf(h ChangeFacts) *AccessChange {
	c := &AccessChange{At: h.At.UTC(), By: display(h.By), What: display(h.What)}
	for _, n := range h.Added {
		c.Added = append(c.Added, display(n))
	}
	for _, n := range h.Removed {
		c.Removed = append(c.Removed, display(n))
	}
	what := "changed"
	switch h.What {
	case "sealed":
		what = "sealed"
	case "rewrap":
		what = "readers rewrapped"
	case "rewrap --all":
		what = "records sealed again"
	}
	if c.By != "" {
		what += " by " + c.By
	}
	c.Text = what
	if !h.At.IsZero() {
		c.Text = h.At.UTC().Format("2006-01-02 15:04") + " UTC · " + what
	}
	return c
}

// recoveryOf is the sentence about the recovery recipient of the state, and the
// warning when it does not hold.
func recoveryOf(r *RecoveryFacts) *AccessRecovery {
	rec := &AccessRecovery{K: r.K, N: r.N, Key: r.Key, KeyShort: shortKey(r.Key)}
	rec.Text = fmt.Sprintf("Recovery: any %d of %d shares rebuild the recovery key (%s). If every reader loses their key, the holders of %d shares can open the state.", r.K, r.N, rec.KeyShort, r.K)
	var w []string
	if !r.Locked {
		ids := make([]string, 0, len(r.Groups))
		for _, g := range r.Groups {
			ids = append(ids, display(g))
		}
		w = append(w, "The lock of the recovery key is missing from "+strings.Join(ids, ", ")+".")
	}
	if len(r.Unverified) > 0 {
		ids := make([]string, 0, len(r.Unverified))
		for _, g := range r.Unverified {
			ids = append(ids, display(g))
		}
		w = append(w, "The lock of the recovery key in "+strings.Join(ids, ", ")+" does not hold its check: it was changed in the bucket.")
	}
	switch r.File {
	case "missing":
		w = append(w, "The repository has no recovery file, so a writer cannot tell that the recovery was taken out of the bucket.")
	case "differs":
		w = append(w, "The recovery file of the repository and the state name different recovery keys.")
	case "invalid":
		w = append(w, "The recovery file of the repository does not load.")
	}
	rec.Warning = strings.Join(w, " ")
	return rec
}

// singleReader is the warning for a state that one reader can open. The sentence
// is about the reader who looks when that is the identity of this process, and
// about the reader otherwise, whose name the table above it gives.
func singleReader(you *AccessYou) *AccessWarning {
	text := "There is only one reader. If they lose their key or its passphrase, nobody can read this state, and there is no recovery key."
	if you != nil && you.State == "reader" {
		text = "You are the only reader. If you lose your key or its passphrase, nobody can read this state, and there is no recovery key."
	}
	return &AccessWarning{Text: text, Action: addBackupTitle, Suggestion: "add-reader"}
}

// commas writes n with a comma between thousands.
func commas(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// The suggestions. Every command is built here from a template: the words that
// vary are a path, which is quoted, and the name of a reader, which must match the
// rule for names and is quoted as well. A part that the person fills in is an
// angle-bracket word, left unquoted, so a paste that was not filled in fails in the
// shell and does nothing.

func command(text string) SuggestionStep { return SuggestionStep{Kind: "command", Text: text} }

func fill(text string) SuggestionStep {
	return SuggestionStep{Kind: "command", Text: text, Placeholder: true}
}

func note(text string) SuggestionStep { return SuggestionStep{Kind: "note", Text: text} }

func gitSteps(file, message string) []SuggestionStep {
	return []SuggestionStep{
		command("git add " + engine.ShellWord(file)),
		command("git commit -m " + engine.ShellWord(message)),
		command("git push"),
	}
}

func suggestions(a *Access, dir string, local bool, blobs map[string]string) []Suggestion {
	file := shortPath(dir, a.File.Path)
	// A readers file that is not where the commands look for it (.pravo/access, here
	// or in a parent) is named on the commands that read it, so that a paste reads
	// the file this page shows.
	where := ""
	if !strings.HasSuffix(file, ".pravo/access/"+readersFile) {
		where = " --access-dir " + engine.ShellWord(shortPath(dir, filepath.Dir(a.File.Path)))
	}
	protectCmd := func(rest string) SuggestionStep { return command("pravo protect " + rest + where) }
	protected := a.State == "sealed" || a.State == "migrating"
	var list []Suggestion
	add := func(s Suggestion) { list = append(list, s) }
	primary := ""

	notReader := a.You != nil && a.You.State != "reader"
	addTitle := addReaderTitle
	if a.SingleReader != nil {
		addTitle = addBackupTitle
	}
	switch {
	case a.State == "migrating":
		primary = "finish"
	case a.State == "sealed" && a.File.Verdict == "differs":
		primary = "rewrap"
	case protected && notReader:
		primary = "identity"
	case a.File.Verdict == "expected-sealed":
		primary = "check-downgrade"
	case !protected:
		primary = "seal"
	}

	initCmd := "init --encrypt"
	if local {
		initCmd += " --allow-local"
	}
	if a.State == "migrating" && a.Migration != nil && a.Migration.Unseal {
		add(Suggestion{ID: "finish", Title: "Finish unsealing the state", Why: "Unsealing started and stopped before it finished. Running it again finishes the records that are left.",
			Who: "a reader", Steps: []SuggestionStep{protectCmd("unseal")}})
	} else if a.State == "migrating" {
		add(Suggestion{ID: "finish", Title: "Finish sealing the state", Why: "Sealing started and stopped before it finished. Running it again finishes the records that are left.",
			Who: "a reader", Steps: []SuggestionStep{protectCmd(initCmd)}})
	}
	if a.File.Verdict == "expected-sealed" {
		add(Suggestion{ID: "check-downgrade", Title: "Check why the state is not sealed",
			Why: "The readers file lists readers, so the state was expected to be sealed, and it is not. It has not been sealed yet, or someone who can write to the store changed its meta.json.",
			Who: "a reader",
			Steps: []SuggestionStep{protectCmd("status"),
				note("If you did not mean to seal this state, remove the readers file. To seal it: pravo protect " + initCmd),
				protectCmd(initCmd)}})
	}
	if !protected {
		s := Suggestion{ID: "seal", Title: "Seal the state",
			Why: "Sealing encrypts attributes, outputs and stored plans to the readers in the file. It needs the store at rest, so announce a short freeze.",
			Who: "each reader, then one of them"}
		s.Steps = append(s.Steps, note("On the machine of each reader:"), fill("pravo id new --name <name>"))
		if a.File.Count == 0 {
			s.Steps = append(s.Steps, note("On the machine of the reader who seals, add the line each reader sent, one for each, to the readers file:"),
				SuggestionStep{Kind: "add-line", File: file, Text: "<the line pravo id new printed>", Placeholder: true})
		} else {
			s.Steps = append(s.Steps, note("The readers file lists "+strconv.Itoa(a.File.Count)+" "+plural(a.File.Count, "reader", "readers")+". A reader who is not listed sends their line, and it is added to "+file+"."))
		}
		if local {
			s.Steps = append(s.Steps, note("This state is a local directory. pravo protect refuses it unless --allow-local is given, because two users on one directory would not be excluded."), protectCmd(initCmd))
		} else {
			s.Steps = append(s.Steps, protectCmd("check"), protectCmd(initCmd))
			s.Steps = append(s.Steps, gitSteps(file, "Seal the state")...)
		}
		add(s)
	}
	if protected && notReader {
		s := Suggestion{ID: "identity", Title: "Read the state with your own key",
			Why: "This key is not a reader. Print your line and send it to a reader, who adds it as described under " + addTitle + ".",
			Who: "you"}
		switch a.You.State {
		case "locked":
			// The identity exists and may well be a reader: what is missing is its passphrase.
			s.Title = "Give the identity its passphrase"
			s.Why = "The identity is sealed with a passphrase and none was available. The viewer asks for it on the terminal before it starts, or reads PRAVO_PASSPHRASE from its environment."
			s.Steps = append(s.Steps, note("Start the viewer again from a terminal:"), command("pravo serve"))
		default:
			if a.You.State == "no-identity" {
				s.Why = "No identity was given, so there is no key to open the state with. Make one, send its line to a reader, who adds it as described under " + addTitle + "."
				s.Steps = append(s.Steps, fill("pravo id new --name <name>"))
			}
			s.Steps = append(s.Steps, command("pravo id show"), note("When a reader has added your line and rewrapped, start the viewer again:"), fill("pravo serve --identity <name>"))
		}
		if a.Recovery != nil {
			s.Steps = append(s.Steps, note(fmt.Sprintf("If no reader can open the state any more, put your line in the readers file in place of theirs, and open the state with %d of its %d recovery shares:", a.Recovery.K, a.Recovery.N)),
				fill("pravo protect recover --share <share-file> --share <share-file>"+where))
		}
		add(s)
	}
	if a.State == "sealed" && a.File.Verdict == "differs" {
		add(Suggestion{ID: "rewrap", Title: "Make the store match the readers file",
			Why: "The file and the store list different readers. Writers refuse to write until they match.", Who: "a current reader",
			Steps: []SuggestionStep{protectCmd("rewrap"), note("If you did not edit the file, something else changed it. Compare with git:"),
				command("git log -p -- " + engine.ShellWord(file))}})
	}
	if a.State == "sealed" {
		addWhy := "The new reader makes an identity and sends the line it prints. A current reader adds the line, commits it and rewraps."
		if a.SingleReader != nil {
			// With one reader the card is the answer to the risk the page has just told of, so it leads with it.
			addWhy = "With one reader and no recovery key, nobody can read the state again if that reader loses their key or its passphrase. A backup reader makes an identity and sends the line it prints. A current reader adds the line, commits it and rewraps."
		}
		add(Suggestion{ID: "add-reader", Title: addTitle, Why: addWhy,
			Who: "the new reader, then a current reader",
			Steps: append(append([]SuggestionStep{note("On the machine of the new reader:"), fill("pravo id new --name <name>"),
				note("On the machine of a current reader:"),
				{Kind: "add-line", File: file, Text: "<the line pravo id new printed>", Placeholder: true}},
				gitSteps(file, "Add a reader")...), protectCmd("rewrap"))})
		removals := 0
		for _, r := range a.Readers {
			if removals == maxRemovals {
				break
			}
			if !r.InFile || !r.NameOK {
				continue
			}
			line, ok := readerLine(r, blobs[r.Key])
			if !ok {
				continue
			}
			removals++
			steps := []SuggestionStep{{Kind: "remove-line", File: file, Text: line}}
			steps = append(steps, gitSteps(file, r.Name+" leaves")...)
			steps = append(steps, protectCmd("rewrap"))
			if r.You {
				// The first rewrap takes away the key of the person who runs it: rewrap --all needs a reader.
				steps = append(steps, note("You are not a reader once this has run. A reader who is left runs the rest, which seals every existing record again under the new key:"))
			} else {
				steps = append(steps, note("rewrap --all seals every existing record again under the new key."))
			}
			steps = append(steps, protectCmd("rewrap --all"))
			add(Suggestion{ID: "remove-reader:" + r.Name, Title: "Remove " + r.Name,
				Why: "Removing the line stops new records being sealed to " + r.Name + ". " + r.Name + " keeps what they already read, and an old copy of the bucket still opens with their key. Replace the real secrets " + r.Name + " could read where they live.",
				Who: "a current reader", Steps: steps})
		}
		// Records out of reach of a reader are under a group that an old reader list made; with no key every
		// record is out of reach, which says nothing about the groups.
		underOld := false
		for _, g := range a.Groups {
			underOld = underOld || (!g.Current && g.Records > 0)
		}
		recoveryFile := path.Join(path.Dir(file), "recovery.txt")
		if a.Recovery == nil {
			// A recovery recipient answers the risk that every reader loses their key.
			add(Suggestion{ID: "set-recovery", Title: "Set a recovery key",
				Why: "A recovery key is split into shares, any K of which rebuild it, so that the state can be opened when every reader has lost their key. Give each share to a different person, offline. Whoever holds K shares can read the state, so K is a decision about whom to trust together.",
				Who: "a current reader",
				Steps: append(append([]SuggestionStep{note("Make the key and the shares. This writes recovery-shares/ and " + recoveryFile + ":"), fill("pravo protect rewrap --recovery <K>/<N>" + where)},
					gitSteps(recoveryFile, "Set a recovery key")...), note("Do not commit recovery-shares/. Give each file to a different person, offline."))})
		} else if a.Recovery.Warning != "" {
			add(Suggestion{ID: "fix-recovery", Title: "Bring the recovery in line",
				Why: a.Recovery.Warning, Who: "a current reader",
				Steps: []SuggestionStep{protectCmd("rewrap"), note("The recovery file is " + recoveryFile + ". If it was not committed, commit it:")}})
		}
		if underOld || (a.Records.Unreadable > 0 && a.You != nil && a.You.State == "reader") {
			add(Suggestion{ID: "reseal", Title: "Seal every record again",
				Why: "Records stay under an old group until they are written again.", Who: "a current reader",
				Steps: []SuggestionStep{protectCmd("rewrap --all")}})
		}
		// A group other than the current one is a data key that a reader who was removed may still hold.
		// It is dropped when nothing is sealed in it; the garbage collection deletes the records that no
		// pointer names as well.
		if len(a.Groups) > 1 {
			var steps []SuggestionStep
			for _, g := range a.Groups {
				if !g.Current && g.Records > 0 {
					steps = append(steps, note("Records are still under an old group. Seal them again first:"), protectCmd("rewrap --all"))
					break
				}
			}
			steps = append(steps, note("Say what would be marked, deleted and dropped, and then do it. gc marks the records that no pointer names and deletes them on a run at least 15 minutes later:"), protectCmd("gc --dry-run"), protectCmd("gc"))
			add(Suggestion{ID: "prune", Title: "Drop the groups nothing is sealed in",
				Why: "A reader who was removed keeps the data key of the groups they were in. A group that no record, output or stored plan is sealed in is dropped from meta.json, and the key opens nothing that is left. The same command marks the records that no pointer names once they are a day old, and deletes them on a run at least 15 minutes later.",
				Who: "a current reader", Steps: steps})
		}
		add(Suggestion{ID: "status", Title: "Check the state", Why: "The check hashes every record against its name and prints who can read the state.",
			Steps: []SuggestionStep{protectCmd("status")}})
	}
	for i := range list {
		if list[i].ID == primary {
			list[i].Primary = true
			list = append([]Suggestion{list[i]}, append(list[:i:i], list[i+1:]...)...)
			break
		}
	}
	return list
}

// sealedReason says why a sealed record is not opened, for the note on a record.
func (m *model) sealedReason() string {
	y := youOf(m.acc.facts.You)
	switch y.State {
	case "reader":
		return "this key holds no lock for the group the record is sealed in."
	case "not-reader":
		return "this key is not a reader of the state."
	case "locked":
		return "the identity is sealed with a passphrase and none was available."
	}
	return "no identity was given. Start the viewer with pravo serve --identity NAME."
}
