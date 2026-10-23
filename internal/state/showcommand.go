package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/pravo-systems/pravo/internal/protect"
)

// A command line is written into the history of the state, into the files of the
// locks and into the summaries of the stored plans, where anyone who can read the
// state sees it. The one flag that carries a secret today is -t key=value, which
// injects a value into the configuration; --webhook and -e carry an address and an
// expression. In a sealed state those values are replaced by a tag that tells
// equal values apart and nothing else, and the original line is kept sealed.

// Tagger is implemented by the Sealer of a sealed state. Tag returns the short
// keyed tag of value (see protect.Keys.Tag), or "" when the sealer holds no key.
type Tagger interface {
	Tag(value string) string
}

// sealedFlags are the flags whose value is always replaced. The canonical name is
// the long one.
var sealedFlags = map[string]string{
	"t": "tag", "tag": "tag",
	"webhook": "webhook",
	"e":       "expression", "expression": "expression",
}

// shortValueFlags are the short flags that take a value and are not sealed. A
// cluster of short flags ends at the first of them. Without LookupFlag, they are
// the only short flags that are known to take a value.
const shortValueFlags = "C"

// FlagKind says what the command tree knows of a flag.
type FlagKind struct {
	// Long is the long name of the flag, without the dashes.
	Long string
	// TakesValue is set for a flag that reads the argument after it as its value
	// (--flag value, -f value), which a boolean flag does not.
	TakesValue bool
}

// LookupFlag, when the program sets it, says what the flags of the command that is
// running are. name is a long name without the dashes, or with short set the letter
// of a shorthand; ok is false for a flag that the command does not have. The
// state package cannot tell a boolean flag from one that takes a value, and the
// command tree can, so the program sets the function when it starts, as it sets
// PravoVersion. Without it only -t, --webhook and -e, and the flags that are
// written --flag=value, have their values sealed.
var LookupFlag func(name string, short bool) (kind FlagKind, ok bool)

// valueFlag reports whether the flag takes a value, and its long name. It is false
// for a flag that is not known to.
func valueFlag(name string, short bool) (long string, takes bool) {
	if LookupFlag == nil {
		return "", false
	}
	if k, ok := LookupFlag(name, short); ok && k.TakesValue {
		return k.Long, true
	}
	return "", false
}

// ShowCommand returns what the history of the state and the lock files show of
// a command line. When the state is sealed, the values of -t/--tag, --webhook
// and -e/--expression are replaced by a short tag, <sealed:a1b2c3d4>, and the
// original line is returned sealed in box for the readers. Otherwise shown is
// the arguments joined by spaces, as before, and box is nil. args is os.Args
// without the program name.
func (s *Store) ShowCommand(args []string) (shown string, box *protect.Sealed) {
	var sl Sealer
	if s != nil {
		sl = s.Sealer()
	}
	if sl == nil {
		return "pravo " + joinArgs(args), nil
	}
	shown = RedactArgs(args, TagFunc(sl))
	if sealed, err := sl.Seal(protect.KindCommand, "", []byte("pravo "+joinArgs(args))); err == nil {
		box = &sealed
	}
	return shown, box
}

// TagFunc returns the function that tags a value for the state that sl seals, or
// one that tags with nothing when sl cannot (a sealer that holds no key, or does
// not implement Tagger). RedactArgs then writes <sealed> without a tag.
func TagFunc(sl Sealer) func(value string) string {
	if t, ok := sl.(Tagger); ok {
		return t.Tag
	}
	return func(string) string { return "" }
}

// RedactArgs returns the command line of args as the history shows it in a sealed
// state. tag returns the tag of a value, or "" for none.
//
// The values of -t/--tag (in all of the spellings -t k=v, -tk=v, -t=k=v, --tag
// k=v and --tag=k=v: the key stays readable), --webhook and -e/--expression are
// replaced. So is the value of any other flag that takes a value and is not one of
// the flags whose value is public (see publicFlags): written with an equals sign,
// or in the next argument when LookupFlag says that the flag takes one. The
// positional arguments stay as they are: an address, a plan id, and the argument
// after a boolean flag.
func RedactArgs(args []string, tag func(value string) string) string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			out = append(out, args[i:]...)
			i = len(args)
		case strings.HasPrefix(a, "--") && len(a) > 2:
			name, value, hasValue := strings.Cut(a[2:], "=")
			canon, sealed := sealedFlags[name]
			switch {
			case sealed && hasValue:
				out = append(out, "--"+name+"="+sealedValue(canon, value, tag))
			case sealed:
				out = append(out, a)
				if i+1 < len(args) {
					i++
					out = append(out, sealedValue(canon, args[i], tag))
				}
			case hasValue && !publicFlags[name] && value != "true" && value != "false":
				out = append(out, "--"+name+"="+token(name, value, tag))
			case !hasValue:
				// A flag that takes a value reads the next argument as it, whatever it
				// looks like; a boolean flag does not, and the next argument is its own.
				long, takes := valueFlag(name, false)
				out = append(out, a)
				if takes && i+1 < len(args) {
					i++
					out = append(out, valueWord(long, args[i], tag))
				}
			default:
				out = append(out, a)
			}
		case strings.HasPrefix(a, "-") && len(a) > 1:
			word, canon, takesNext := redactShort(a, tag)
			out = append(out, word)
			if takesNext && i+1 < len(args) {
				i++
				out = append(out, valueWord(canon, args[i], tag))
			}
		default:
			out = append(out, a)
		}
	}
	return "pravo " + strings.Join(out, " ")
}

// RedactLine is RedactArgs for a command line that was joined by spaces, which
// has lost where one argument ends and the next begins: a stored plan of an older
// pravo, a transaction recorded before the seal, or a caller that has only the
// string. It errs on the side of sealing: after the first flag, every word that is
// not itself a flag or the id of a plan is replaced, so that a value with a space
// in it does not leak its second half. The words before the first flag stay, as
// does the program name.
func RedactLine(line string, tag func(value string) string) string {
	words := strings.Fields(line)
	if len(words) > 0 && words[0] == "pravo" {
		words = words[1:]
	}
	var out []string
	seenFlag := false
	for _, w := range words {
		isFlag := strings.HasPrefix(w, "-") && len(w) > 1
		switch {
		case !seenFlag && !isFlag:
			out = append(out, w)
		case isFlag:
			seenFlag = true
			if name, value, ok := strings.Cut(w, "="); ok {
				out = append(out, name+"="+token(name, value, tag))
			} else {
				out = append(out, w)
			}
		case planIDWordRe.MatchString(w):
			out = append(out, w)
		default:
			out = append(out, sealedWord(w, tag))
		}
	}
	return "pravo " + strings.Join(out, " ")
}

// planIDWordRe matches the id of a stored plan, which is never a secret and is
// what a reader of the history looks for.
var planIDWordRe = regexp.MustCompile(`^plan-[0-9a-f]{6,64}$`)

// sealedWord replaces a word of a command line that is not known to be an
// argument of its own. A word that has the form of a tag, key=value, keeps its key.
func sealedWord(w string, tag func(string) string) string {
	if k, _, ok := strings.Cut(w, "="); ok && tagKeyRe.MatchString(k) {
		return k + "=" + token("tag", w, tag)
	}
	return token("word", w, tag)
}

var tagKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// RedactTag returns the form of a -t tag, key=value, that a sealed state shows:
// the key and the tag of the value.
func RedactTag(t string, tag func(value string) string) string {
	return sealedValue("tag", t, tag)
}

// publicFlags are the flags whose value is not a secret. Their value stays
// readable in the history, and tells a reader what the command was about.
var publicFlags = map[string]bool{
	"ttl": true, "reserve": true, "refresh": true, "limit": true, "older-than": true,
	"platforms": true, "port": true, "replace": true, "only": true, "touching": true,
	"out": true, "policy": true, "plan": true, "store": true, "chdir": true,
}

// valueWord is the value of the flag canon as the history shows it: sealed, unless
// the flag is one whose value is public.
func valueWord(canon, value string, tag func(string) string) string {
	if publicFlags[canon] {
		return value
	}
	return sealedValue(canon, value, tag)
}

// sealedValue replaces the value of the flag canon. For a tag the key stays.
func sealedValue(canon, value string, tag func(string) string) string {
	if canon == "tag" {
		if k, v, ok := strings.Cut(value, "="); ok {
			return k + "=" + token(canon, k+"="+v, tag)
		}
	}
	return token(canon, value, tag)
}

// redactShort handles one argument that is a cluster of short flags, such as -T,
// -t, -tk=v, -t=k=v or -Tt. It returns the argument as it is shown, and when the
// cluster ends in a flag whose value is the next argument, the canonical name of
// that flag and takesNext. The caller seals that value unless the flag is one whose
// value is public.
func redactShort(a string, tag func(string) string) (word, canon string, takesNext bool) {
	for j := 1; j < len(a); j++ {
		c := string(a[j])
		if strings.Contains(shortValueFlags, c) {
			return a, "", false
		}
		canon, ok := sealedFlags[c]
		if !ok {
			// A flag that takes a value ends the cluster: the rest of it, or the next
			// argument, is the value. Any other is a boolean flag, and the cluster goes on.
			long, takes := valueFlag(c, true)
			if !takes {
				continue
			}
			rest := a[j+1:]
			if rest == "" {
				return a, long, true
			}
			eq := ""
			if strings.HasPrefix(rest, "=") {
				eq, rest = "=", rest[1:]
			}
			return a[:j+1] + eq + valueWord(long, rest, tag), "", false
		}
		rest := a[j+1:]
		if rest == "" {
			return a, canon, true
		}
		eq := ""
		if strings.HasPrefix(rest, "=") {
			eq, rest = "=", rest[1:]
		}
		return a[:j+1] + eq + sealedValue(canon, rest, tag), "", false
	}
	return a, "", false
}

// token is <sealed:a1b2c3d4>, or <sealed> when there is no tag. What is tagged is
// the canonical name of the flag and the value, so that the same value given to
// two flags has two tags.
func token(canon, value string, tag func(string) string) string {
	if t := tag(canon + "\x00" + value); t != "" {
		return "<sealed:" + t + ">"
	}
	return "<sealed>"
}

func joinArgs(args []string) string { return strings.Join(args, " ") }

// Tag tags value with the keys of the store: the same value has the same tag
// until the group changes. A locked store has no key and no tag.
func (z storeSealer) Tag(value string) string {
	z.s.recheckSeal()
	return z.s.ring.tag(value)
}

var _ Tagger = storeSealer{}

// redactHistory rewrites the command line of each transaction that was recorded
// before the seal, and of each lock file that a command left behind, in the form
// that a sealed state shows, so that the values of -t that earlier commands were
// given do not stay in the history. A line that shows nothing to hide is not
// written, and nor is one that already shows a tag. The text of the error of a
// transaction that failed is sealed in the same write. It is called by the upgrade
// to a sealed state, which needs the state at rest, and may be called again
// after a crash.
func (s *Store) redactHistory(ctx context.Context) error {
	tag := TagFunc(s.Sealer())
	for _, kind := range []struct {
		prefix, suffix string
		// redact returns the object with its command line redacted, or nil if
		// there is nothing to change.
		redact func(data []byte) ([]byte, error)
	}{
		{"txn", ".json", func(data []byte) ([]byte, error) {
			var t Transaction
			if json.Unmarshal(data, &t) != nil {
				return nil, nil
			}
			changed := false
			// The command of a signed transaction is what was signed.
			if t.StartSig == nil && redactable(t.Command) {
				t.Command = RedactLine(t.Command, tag)
				changed = true
			}
			if s.sealErrorText(&t) {
				changed = true
			}
			if !changed {
				return nil, nil
			}
			return indentJSON(t)
		}},
		{"locks", ".lock", func(data []byte) ([]byte, error) {
			var l Lock
			if json.Unmarshal(data, &l) != nil || !redactable(l.Command) {
				return nil, nil
			}
			l.Command = RedactLine(l.Command, tag)
			return indentJSON(l)
		}},
	} {
		keys, err := s.b.List(ctx, kind.prefix)
		if err != nil {
			return err
		}
		for _, key := range keys {
			if !strings.HasSuffix(key, kind.suffix) {
				continue
			}
			data, err := s.b.Get(ctx, key)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			next, err := kind.redact(data)
			if err != nil {
				return err
			}
			if next == nil {
				continue
			}
			if _, err := s.b.ReplaceIf(ctx, key, func(cur []byte) ([]byte, bool) { return next, bytes.Equal(cur, data) }); err != nil && !errors.Is(err, ErrNotFound) {
				return Advise(err)
			}
		}
	}
	return nil
}

// sealErrorText moves the text of the error of a transaction that failed or was
// rejected before the seal into a sealed box, and puts SealedErrorText in its place.
// It reports whether it changed t. The summary of an interrupted run names
// addresses and actions, and stays.
func (s *Store) sealErrorText(t *Transaction) bool {
	if (t.Status != "failed" && t.Status != "rejected") || t.Summary == "" || t.SummarySealed != nil ||
		strings.HasPrefix(t.Summary, SealedErrorText) || strings.HasPrefix(t.Summary, "interrupted; ") {
		return false
	}
	sl := s.Sealer()
	if sl == nil {
		return false
	}
	box, err := sl.Seal(protect.KindSummary, t.ID, []byte(t.Summary))
	if err != nil {
		return false
	}
	t.SummarySealed, t.Summary = &box, SealedErrorText
	return true
}

// redactable reports whether a command line recorded before the seal has
// something to hide: it has a flag, and shows no tag yet.
func redactable(command string) bool {
	if command != "pravo" && !strings.HasPrefix(command, "pravo ") {
		return false
	}
	return RedactLine(command, func(string) string { return "" }) != command && !strings.Contains(command, "<sealed")
}
