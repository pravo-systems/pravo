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
	addReaderTitle = "Add a member"
	addBackupTitle = "Add a backup member"
)

const (
	maxAccessReaders = 200 // readers listed
	maxAccessErrors  = 20  // lines of the readers file listed as not loading
	maxRemovals      = 50  // "remove a member" suggestions
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
	// backend is "local", "s3" or "http".
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
		Readers: []AccessReader{}, Suggestions: []Suggestion{}, Issues: []AccessIssue{}}
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
	if protected && f.Roles != nil {
		a.Roles = rolesOf(f.Roles, a.Readers)
	}
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
		if mg.Unseal && a.Records.Plain > 0 && a.Records.Unreadable == 0 {
			// The plain records of an unseal are the ones it has reached.
			a.Records.Text = fmt.Sprintf("%s of %s records are unsealed so far.", commas(a.Records.Plain), commas(max(a.Records.Total, a.Records.Plain)))
		}
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
	a.Suggestions = suggestions(a, in.dir, in.backend == "" || in.backend == "local", blobs)
	a.Issues, a.AllClear = issuesOf(a, f)
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
		for i := range rows {
			ar := &rows[i]
			switch {
			case ar.InFile && !ar.InStore:
				added = append(added, ar.Name)
				ar.Status, ar.StatusText = "to-add", "On the list, not let in yet"
			case ar.InStore && !ar.InFile:
				removed = append(removed, ar.Name)
				ar.Status, ar.StatusText = "to-remove", "Off the list, still has a key"
			default:
				ar.Status, ar.StatusText = "active", "Has access"
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
			af.Text = "The repository has an access list (" + shortPath(dir, af.Path) + "), but the state is not sealed. It has not been sealed yet, or someone changed its meta.json."
		case "invalid":
			af.Text = "The access list can't be read" + firstError(af.Errors) + "."
		default:
			af.Text = "The state is not sealed, so there is no access list to compare."
		}
	case f.State == "missing":
		af.Verdict = "missing"
		af.Text = "No access list (readers.txt) was found in " + where + ". Changes to a sealed state need it, so this checkout can't change the state."
	case f.State == "invalid":
		af.Verdict = "invalid"
		af.Text = "The access list can't be read" + firstError(af.Errors) + "."
	case f.State == "ok":
		if len(added) == 0 && len(removed) == 0 {
			af.Verdict = "match"
			af.Text = "The access list (readers.txt) matches the state."
			break
		}
		af.Verdict = "differs"
		af.Added, af.Removed = added, removed
		var parts []string
		if len(added) > 0 {
			parts = append(parts, nameList(added)+" on the list and not let in yet")
		}
		if len(removed) > 0 {
			parts = append(parts, nameList(removed)+" off the list and still in the state")
		}
		af.Text = "The access list and the state disagree: " + strings.Join(parts, "; ") + ". Changes to the state are refused until they match."
	default:
		af.Text = "The access list was not read."
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
			you.Text = "You can open this state."
		} else {
			you.Text = "You are " + you.Name + " and can open this state."
		}
	case "not-reader":
		you.Text = "This key is not on the access list, so secret values are hidden. The resources are still listed."
	case "locked":
		you.Text = "Your key is protected by a passphrase and none was available, so secret values are hidden. The resources are still listed."
	default:
		you.State = "no-identity"
		you.Text = "No key was given, so secret values are hidden. The resources are still listed."
	}
	return you
}

// groupsText says why a state has more than one group and what sealing the records
// again does. A group is a data key; a removal of a reader makes a new one.
const groupsText = "Removing a member starts a new group of keys. Records stay under the old group until they are written again or resealed with rewrap --all."

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
		return fmt.Sprintf("This state is sealed: its secret values are locked, and only the %d %s listed below can open them. Names, types and dependencies stay visible, so the graph and the plans keep working.",
			readers, plural(readers, "member", "members"))
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
		what = "members changed"
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
	rec.Text = fmt.Sprintf("If everyone loses their key, any %d of %d shares rebuild the recovery key (%s) and open the state.", r.K, r.N, rec.KeyShort)
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
	text := "Only one member can open this state. If they lose their key or its passphrase, nobody can, and there is no recovery key."
	if you != nil && you.State == "reader" {
		text = "You are the only member who can open this state. If you lose your key or its passphrase, nobody can, and there is no recovery key."
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

// suggester holds what the suggestion cards share. Each method makes one card,
// or none when the state does not call for it.
type suggester struct {
	a         *Access
	local     bool
	blobs     map[string]string
	file      string // the readers file, as the commands name it
	where     string // the --access-dir flag, when the file is not where the commands look
	protected bool
	notReader bool
	initCmd   string
	addTitle  string
}

func suggestions(a *Access, dir string, local bool, blobs map[string]string) []Suggestion {
	g := &suggester{a: a, local: local, blobs: blobs, file: shortPath(dir, a.File.Path)}
	// A readers file that is not where the commands look for it (.pravo/access, here
	// or in a parent) is named on the commands that read it, so that a paste reads
	// the file this page shows.
	if !strings.HasSuffix(g.file, ".pravo/access/"+readersFile) {
		g.where = " --access-dir " + engine.ShellWord(shortPath(dir, filepath.Dir(a.File.Path)))
	}
	g.protected = a.State == "sealed" || a.State == "migrating"
	g.notReader = a.You != nil && a.You.State != "reader"
	g.addTitle = addReaderTitle
	if a.SingleReader != nil {
		g.addTitle = addBackupTitle
	}
	g.initCmd = "init --encrypt"
	if local {
		g.initCmd += " --allow-local"
	}

	var list []Suggestion
	add := func(s ...Suggestion) { list = append(list, s...) }
	if s, ok := g.finish(); ok {
		add(s)
	}
	if s, ok := g.checkDowngrade(); ok {
		add(s)
	}
	if s, ok := g.seal(); ok {
		add(s)
	}
	if s, ok := g.identity(); ok {
		add(s)
	}
	if s, ok := g.pinRoles(); ok {
		add(s)
	}
	if s, ok := g.rewrap(); ok {
		add(s)
	}
	if a.State == "sealed" {
		add(g.addReader())
		add(g.removals()...)
		if s, ok := g.enableRoles(); ok {
			add(s)
		}
		if s, ok := g.recovery(); ok {
			add(s)
		}
		if s, ok := g.reseal(); ok {
			add(s)
		}
		if s, ok := g.prune(); ok {
			add(s)
		}
		add(Suggestion{ID: "status", Title: "Check the state", Why: "The check hashes every record against its name and prints who can read the state.",
			Steps: []SuggestionStep{g.protectCmd("status")}})
	}
	primary := g.primary()
	for i := range list {
		if list[i].ID == primary {
			list[i].Primary = true
			list = append([]Suggestion{list[i]}, append(list[:i:i], list[i+1:]...)...)
			break
		}
	}
	return list
}

func (g *suggester) protectCmd(rest string) SuggestionStep {
	return command("pravo protect " + rest + g.where)
}

// primary is the id of the card that leads the list.
func (g *suggester) primary() string {
	a := g.a
	switch {
	case a.State == "migrating":
		return "finish"
	case a.State == "sealed" && a.File.Verdict == "differs":
		return "rewrap"
	case g.protected && g.notReader:
		return "identity"
	case a.File.Verdict == "expected-sealed":
		return "check-downgrade"
	case !g.protected:
		return "seal"
	}
	return ""
}

func (g *suggester) finish() (Suggestion, bool) {
	a := g.a
	switch {
	case a.State == "migrating" && a.Migration != nil && a.Migration.Unseal:
		return Suggestion{ID: "finish", Title: "Finish unsealing the state", Why: "Unsealing started and stopped before it finished. Running it again finishes the records that are left.",
			Who: "any member", Steps: []SuggestionStep{g.protectCmd("unseal")}}, true
	case a.State == "migrating":
		return Suggestion{ID: "finish", Title: "Finish sealing the state", Why: "Sealing started and stopped before it finished. Running it again finishes the records that are left.",
			Who: "any member", Steps: []SuggestionStep{g.protectCmd(g.initCmd)}}, true
	}
	return Suggestion{}, false
}

func (g *suggester) checkDowngrade() (Suggestion, bool) {
	if g.a.File.Verdict != "expected-sealed" {
		return Suggestion{}, false
	}
	return Suggestion{ID: "check-downgrade", Title: "Check why the state is not sealed",
		Why: "The access list names members, so the state was expected to be sealed, and it is not. It has not been sealed yet, or someone who can write to the store changed its meta.json.",
		Who: "any member",
		Steps: []SuggestionStep{g.protectCmd("status"),
			note("If you did not mean to seal this state, remove the access list and the recovery file. To seal it: pravo protect " + g.initCmd),
			g.protectCmd(g.initCmd)}}, true
}

func (g *suggester) seal() (Suggestion, bool) {
	if g.protected {
		return Suggestion{}, false
	}
	a, file := g.a, g.file
	s := Suggestion{ID: "seal", Title: "Seal the state",
		Why: "Sealing locks attributes, outputs and stored plans so that only the members on the access list can open them. It needs the store at rest, so announce a short freeze.",
		Who: "every member, then one of them"}
	s.Steps = append(s.Steps, note("On the machine of each member:"), fill("pravo id new --name <name>"))
	if a.File.Count == 0 {
		s.Steps = append(s.Steps, note("On the machine of the member who seals, add the line each member sent, one for each, to the access list:"),
			SuggestionStep{Kind: "add-line", File: file, Text: "<the line pravo id new printed>", Placeholder: true})
	} else {
		s.Steps = append(s.Steps, note("The access list names "+strconv.Itoa(a.File.Count)+" "+plural(a.File.Count, "member", "members")+". A member who is not listed sends their line, and it is added to "+file+"."))
	}
	if g.local {
		s.Steps = append(s.Steps, note("This state is a local directory. pravo protect refuses it unless --allow-local is given, because two users on one directory would not be excluded."), g.protectCmd(g.initCmd))
	} else {
		s.Steps = append(s.Steps, g.protectCmd("check"), g.protectCmd(g.initCmd))
		s.Steps = append(s.Steps, gitSteps(file, "Seal the state")...)
	}
	return s, true
}

func (g *suggester) identity() (Suggestion, bool) {
	if !g.protected || !g.notReader {
		return Suggestion{}, false
	}
	a := g.a
	s := Suggestion{ID: "identity", Title: "Read the state with your own key",
		Why: "This key is not on the access list. Print your line and send it to a member, who adds it as described under " + g.addTitle + ".",
		Who: "you"}
	switch a.You.State {
	case "locked":
		// The identity exists and may well be a reader: what is missing is its passphrase.
		s.Title = "Give the identity its passphrase"
		s.Why = "The identity is sealed with a passphrase and none was available. The viewer asks for it on the terminal before it starts, or reads PRAVO_PASSPHRASE from its environment."
		s.Steps = append(s.Steps, note("Start the viewer again from a terminal:"), command("pravo serve"))
	default:
		if a.You.State == "no-identity" {
			s.Why = "No identity was given, so there is no key to open the state with. Make one, send its line to a member, who adds it as described under " + g.addTitle + "."
			s.Steps = append(s.Steps, fill("pravo id new --name <name>"))
		}
		s.Steps = append(s.Steps, command("pravo id show"), note("When a member has added your line and rewrapped, start the viewer again:"), fill("pravo serve --identity <name>"))
	}
	if a.Recovery != nil {
		s.Steps = append(s.Steps, note(fmt.Sprintf("If no member can open the state any more, put your line in the access list in place of theirs, and open the state with %d of its %d recovery shares:", a.Recovery.K, a.Recovery.N)),
			fill("pravo protect recover --share <share-file> --share <share-file>"+g.where))
	}
	return s, true
}

// pinRoles is the card that saves the roles in the state, when they are only
// in the readers file or the file asks for others.
func (g *suggester) pinRoles() (Suggestion, bool) {
	a := g.a
	if a.State != "sealed" || a.Roles == nil || (a.Roles.State != "file" && !a.Roles.Differs) {
		return Suggestion{}, false
	}
	title, why := "Lock in the roles", "The roles are only written in the access list, so anyone with a copy of the repository can edit their own role for their own runs. An admin saves them in the state, signed, and adds one line to the list that remembers the first version. From then on editing the list changes nothing until an admin signs it."
	if a.Roles.State != "file" {
		title, why = "Save the new roles", "The access list asks for other roles than the state holds. An admin saves them as the next version, signed by an admin of the one before. When a key leaves or is demoted, the admin also vouches for the changes it already made, so those stay valid."
	}
	return Suggestion{ID: "pin-roles", Title: title, Why: why, Who: "an admin",
		Steps: append([]SuggestionStep{g.protectCmd("roles"), note("It lists what changes and asks first.")}, gitSteps(g.file, "Store the roles")...)}, true
}

// enableRoles is the card that turns roles on, when the readers file gives none.
func (g *suggester) enableRoles() (Suggestion, bool) {
	if g.a.Roles == nil || g.a.Roles.State != "off" {
		return Suggestion{}, false
	}
	return Suggestion{ID: "enable-roles", Title: "Give members roles",
		Why: "Roles are off, so every member may change the state, sign, and remove a policy. A role limits that: a CI job can be a writer, so a leaked CI key cannot change who has access or remove a policy. Keep at least one admin.",
		Who: "any member",
		Steps: append(append([]SuggestionStep{note("In " + g.file + ", add role:admin, role:writer or role:reader as the last word of every line, and keep yourself as an admin.")},
			gitSteps(g.file, "Give members roles")...), g.protectCmd("roles"), note("It lists the roles and asks first. It saves them in the state, signed, so editing the list alone changes nothing later."))}, true
}

func (g *suggester) rewrap() (Suggestion, bool) {
	if g.a.State != "sealed" || g.a.File.Verdict != "differs" {
		return Suggestion{}, false
	}
	return Suggestion{ID: "rewrap", Title: "Make the state match the access list",
		Why: "The list and the state disagree about who has access. Changes to the state are refused until they match.", Who: "any member",
		Steps: []SuggestionStep{g.protectCmd("rewrap"), note("If you did not edit the file, something else changed it. Compare with git:"),
			command("git log -p -- " + engine.ShellWord(g.file))}}, true
}

func (g *suggester) addReader() Suggestion {
	addWhy := "The new member makes a key and sends the line it prints (a CI job is a member too, with a key of its own). A current member adds the line, commits it and rewraps."
	if g.a.SingleReader != nil {
		// With one reader the card is the answer to the risk the page has just told of, so it leads with it.
		addWhy = "With one member and no recovery key, nobody can open the state again if that member loses their key or its passphrase. A backup member makes a key and sends the line it prints. A current member adds the line, commits it and rewraps."
	}
	who := "the new member, then any member"
	line := "<the line pravo id new printed>"
	member := "On the machine of a current member:"
	steps := []SuggestionStep{note("On the machine of the new member:"), fill("pravo id new --name <name>")}
	roles := g.a.Roles != nil && g.a.Roles.State != "off"
	if roles {
		// With roles, rewrap needs an admin, a line without a role is a reader, and a role is the
		// state's only once an admin has stored it.
		addWhy += " Roles are on, so an admin does this: the line needs a role, and the role counts once the admin has saved the roles in the state."
		who = "the new member, then an admin"
		line += " role:writer"
		member = "On the machine of an admin:"
	}
	steps = append(steps, note(member), SuggestionStep{Kind: "add-line", File: g.file, Text: line, Placeholder: true})
	if roles {
		steps = append(steps, note("Make the last word role:reader, role:writer or role:admin, whatever the new member needs. A CI job that applies is a writer."))
	}
	steps = append(append(steps, gitSteps(g.file, "Add a member")...), g.protectCmd("rewrap"))
	if roles {
		steps = append(steps, note("Then save the role in the state, signed. It lists what changes and asks first:"), g.protectCmd("roles"))
	}
	return Suggestion{ID: "add-reader", Title: g.addTitle, Why: addWhy,
		Who: who, Kind: "member-add", Steps: steps}
}

// removals makes a card for each reader in the file who can be removed, up to
// maxRemovals. A reader who is the only one cannot be removed: the readers file
// would list nobody, and pravo protect rewrap refuses to remove every reader. A
// second reader is added first.
func (g *suggester) removals() []Suggestion {
	file := g.file
	inFile := 0
	for _, r := range g.a.Readers {
		if r.InFile {
			inFile++
		}
	}
	var list []Suggestion
	removals := 0
	for _, r := range g.a.Readers {
		if removals == maxRemovals || inFile < 2 {
			break
		}
		if !r.InFile || !r.NameOK {
			continue
		}
		line, ok := readerLine(r, g.blobs[r.Key])
		if !ok {
			continue
		}
		removals++
		steps := []SuggestionStep{{Kind: "remove-line", File: file, Text: line}}
		steps = append(steps, gitSteps(file, r.Name+" leaves")...)
		steps = append(steps, g.protectCmd("rewrap"))
		if r.You {
			// The first rewrap takes away the key of the person who runs it: rewrap --all needs a reader.
			steps = append(steps, note("You can no longer open the state once this has run. A member who is left runs the rest, which seals every existing record again under the new key:"))
		} else {
			steps = append(steps, note("rewrap --all seals every existing record again under the new key."))
		}
		steps = append(steps, g.protectCmd("rewrap --all"))
		why := "Use this when " + r.Name + " no longer needs the state. It stops new records being locked to their key. They keep what they have already read, and an old copy of the bucket still opens with their key, so replace any real secrets they could read where those secrets live."
		if r.You {
			why = "This removes you: once it has run you cannot open the state, and another member has to finish it. " + why
		}
		list = append(list, Suggestion{ID: "remove-reader:" + r.Name, Title: "Remove access for " + r.Name, Kind: "member-remove", Member: r.Name,
			Why: why,
			Who: "any member", Steps: steps})
		if g.a.Roles != nil && g.a.Roles.State != "off" {
			list = append(list, Suggestion{ID: "change-role:" + r.Name, Title: "Change the role of " + r.Name, Kind: "member-role", Member: r.Name,
				Why: "A role says what a member may do: an admin manages who has access and the roles, a writer can change the state, a reader can look and plan.",
				Who: "an admin",
				Steps: append(append([]SuggestionStep{note("In " + file + ", change the last word of the line that starts with " + r.Name + " to role:admin, role:writer or role:reader.")},
					gitSteps(file, "Change the role of "+r.Name)...), g.protectCmd("roles"), note("It lists what changes and asks first."))})
		}
	}
	return list
}

// recovery is the card that sets a recovery key, or the one that brings a
// recovery that does not match in line.
func (g *suggester) recovery() (Suggestion, bool) {
	a := g.a
	recoveryFile := path.Join(path.Dir(g.file), "recovery.txt")
	if a.Recovery == nil {
		// A recovery recipient answers the risk that every reader loses their key.
		return Suggestion{ID: "set-recovery", Title: "Set a recovery key",
			Why: "A recovery key is split into shares, any K of which rebuild it, so that the state can still be opened when every member has lost their key. Give each share to a different person, offline. Whoever holds K shares can open the state, so K is a decision about whom to trust together.",
			Who: "any member",
			Steps: append(append([]SuggestionStep{note("Make the key and the shares. This writes recovery-shares/ and " + recoveryFile + ":"), fill("pravo protect rewrap --recovery <K>/<N>" + g.where)},
				gitSteps(recoveryFile, "Set a recovery key")...), note("Do not commit recovery-shares/. Give each file to a different person, offline."))}, true
	}
	if a.Recovery.Warning != "" {
		return Suggestion{ID: "fix-recovery", Title: "Bring the recovery in line",
			Why: a.Recovery.Warning, Who: "any member",
			Steps: []SuggestionStep{g.protectCmd("rewrap"), note("The recovery file is " + recoveryFile + ". If it was not committed, commit it:")}}, true
	}
	return Suggestion{}, false
}

func (g *suggester) reseal() (Suggestion, bool) {
	a := g.a
	// Records out of reach of a reader are under a group that an old reader list made; with no key every
	// record is out of reach, which says nothing about the groups.
	unreadable := a.Records.Unreadable > 0 && a.You != nil && a.You.State == "reader"
	if !g.underOldGroup() && !unreadable {
		return Suggestion{}, false
	}
	return Suggestion{ID: "reseal", Title: "Seal every record again",
		Why: "Records stay under an old group until they are written again.", Who: "any member",
		Steps: []SuggestionStep{g.protectCmd("rewrap --all")}}, true
}

// underOldGroup reports whether a record is still sealed under a group other
// than the current one.
func (g *suggester) underOldGroup() bool {
	for _, grp := range g.a.Groups {
		if !grp.Current && grp.Records > 0 {
			return true
		}
	}
	return false
}

// prune is the card for a group other than the current one: a data key that a
// reader who was removed may still hold. It is dropped when nothing is sealed in
// it; the garbage collection deletes the records that no pointer names as well.
func (g *suggester) prune() (Suggestion, bool) {
	if len(g.a.Groups) <= 1 {
		return Suggestion{}, false
	}
	var steps []SuggestionStep
	if g.underOldGroup() {
		steps = append(steps, note("Records are still under an old group. Seal them again first:"), g.protectCmd("rewrap --all"))
	}
	steps = append(steps, note("Say what would be marked, deleted and dropped, and then do it. gc marks the records that no pointer names and deletes them on a run at least 15 minutes later:"), g.protectCmd("gc --dry-run"), g.protectCmd("gc"))
	return Suggestion{ID: "prune", Title: "Drop the groups nothing is sealed in",
		Why: "A member who was removed keeps the data key of the groups they were in. A group that no record, output or stored plan is sealed in is dropped from meta.json, and the key opens nothing that is left. The same command marks the records that no pointer names once they are a day old, and deletes them on a run at least 15 minutes later.",
		Who: "any member", Steps: steps}, true
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

const maxIssueItems = 20 // records listed under an issue about records

// rolesOf makes the document of the roles from their facts, and gives the reader rows the role
// of their signing key. A key is matched by the fingerprint of its signing key.
func rolesOf(f *RolesFacts, rows []AccessReader) *AccessRoles {
	r := &AccessRoles{Differs: f.Differs && f.Source == "state", You: roleName(f.You)}
	switch {
	case f.Error != "":
		r.State = "untrusted"
		r.Text = "The saved roles fail their check: " + cleanReason(f.Error) + " Commands that change the state refuse to run until this is fixed."
	case f.Source == "state":
		r.State = "pinned"
		r.Text = "The roles are locked in. Only an admin can change them, and every change is signed, so editing your own copy of the access list gives nobody extra rights."
		if f.Differs {
			r.Text += " The access list asks for other roles than the ones saved."
		}
	case f.Source == "file":
		r.State = "file"
		r.Text = "The roles are only written in the access list, which anyone with a copy of the repository can edit for their own runs."
		if f.Unpinned {
			r.Text = "Roles are saved in the state, but the access list does not point to them yet, so it still decides, and anyone with a copy of the repository can edit it for their own runs."
		}
		r.Text += " Locking them in closes that."
	default:
		r.State = "off"
		r.Text = "Roles are off, so every member may change the state, sign, and remove a policy. Give the lines of the access list a role to turn them on."
	}
	if r.State == "off" {
		return r
	}
	byFP := map[string]string{}
	for _, k := range f.Keys {
		if k.Fingerprint != "" {
			byFP[k.Fingerprint] = k.Role
		}
	}
	for i := range rows {
		if role, ok := byFP[rows[i].Fingerprint]; ok && rows[i].Fingerprint != "" {
			rows[i].Role = roleName(role)
		}
	}
	return r
}

// roleName is the role as the page knows it, "" for any other word.
func roleName(s string) string {
	switch s {
	case "admin", "writer", "reader":
		return s
	}
	return ""
}

// issuesOf lists what needs a person's attention, worst first, and, for a sealed state with none,
// the sentence that says so. Each issue points to the card that says what to do when there is one.
func issuesOf(a *Access, f Facts) (issues []AccessIssue, allClear string) {
	has := func(id string) string {
		for _, s := range a.Suggestions {
			if s.ID == id {
				return id
			}
		}
		return ""
	}
	add := func(level, title, text, card string) *AccessIssue {
		issues = append(issues, AccessIssue{Level: level, Title: title, Text: text, Suggestion: has(card)})
		return &issues[len(issues)-1]
	}
	if a.State == "migrating" {
		title := "Sealing has not finished"
		if a.Migration != nil && a.Migration.Unseal {
			title = "Unsealing has not finished"
		}
		add("chg", title, "Changes to the state are refused until it does.", "finish")
	}
	switch a.File.Verdict {
	case "expected-sealed":
		add("bad", "The state is not sealed", a.File.Text, "check-downgrade")
	case "differs":
		add("chg", "The access list and the state disagree", strings.TrimPrefix(a.File.Text, "The access list and the state disagree: "), "rewrap")
	case "missing":
		add("chg", "The access list is missing", a.File.Text, "")
	case "invalid":
		add("bad", "The access list can't be read", strings.TrimPrefix(a.File.Text, "The access list can't be read: "), "")
	}
	if r := a.Roles; r != nil {
		switch {
		case r.State == "untrusted" && f.Roles != nil:
			add("bad", "The saved roles fail their check", cleanReason(f.Roles.Error)+" Commands that change the state refuse to run until this is fixed. Run pravo protect status for the details.", "")
		case r.State == "file":
			add("chg", "Roles are not locked in", r.Text, "pin-roles")
		case r.Differs:
			add("chg", "The access list asks for different roles", "The access list asks for other roles than the ones saved. An admin saves them to make them count.", "pin-roles")
		}
	}
	if rc := f.Roles; rc != nil && rc.Records != nil && len(rc.Records.Problems) > 0 && a.Roles != nil && a.Roles.State != "off" {
		n := len(rc.Records.Problems)
		is := add("bad", fmt.Sprintf("%s %s written by someone who may not change the state", commas(n), plural(n, "record was", "records were")),
			"These records were written by someone who may not change the state, or whom nobody approved. Look at who wrote them, and remove them with pravo state rm if they are not right.", "")
		for i, p := range rc.Records.Problems {
			if i == maxIssueItems {
				is.More = n - maxIssueItems
				break
			}
			is.Items = append(is.Items, AccessIssueItem{Addr: display(p.Addr), Why: cleanReason(p.Why)})
		}
	}
	if a.State != "sealed" || len(issues) > 0 || a.SingleReader != nil || (a.Recovery != nil && a.Recovery.Warning != "") {
		return issues, ""
	}
	allClear = "The access list matches the state"
	if a.Roles != nil && a.Roles.State == "pinned" {
		allClear += ", the roles are locked in"
	}
	if rc := f.Roles; rc != nil && rc.Records != nil && rc.Records.Skipped == "" && a.Roles != nil && a.Roles.State != "off" {
		allClear += " and every record was written by someone who may change the state."
	} else {
		allClear += "."
	}
	return issues, allClear
}
