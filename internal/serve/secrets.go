package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"cuelang.org/go/cue"

	"github.com/pravo-systems/pravo/internal/state"
)

const (
	// maxSecretVersions bounds the versions whose transactions are checked one by one. A state
	// with more says so, and the page judges the latest version of each secret only.
	maxSecretVersions = 2000
	// maxSecretItems bounds the items listed under an issue.
	maxSecretItems = 20
	// maxSecretUsers bounds the nodes listed under a secret or an item.
	maxSecretUsers = 40
)

// secretRead is a node of the specification that reads a secret.
type secretRead struct {
	addr   string
	name   string
	pin    int
	number bool
}

// secretSpec reads from the specification the name of the secret a node reads and the version it
// asks for, 0 for the latest. known is false when the name depends on something that a command
// settles. The caller holds the lock of the CUE values.
func (m *model) secretSpec(mn *mnode) (name string, pin int, known bool) {
	ss := m.spec
	if ss.root == nil || mn.res == nil {
		return "", 0, false
	}
	v := ss.root.Value.LookupPath(mn.res.Path)
	if !v.Exists() {
		return "", 0, false
	}
	name, err := v.LookupPath(cue.ParsePath("name")).String()
	if err != nil {
		return "", 0, false
	}
	if mn.res.Type == "secret" {
		if p := v.LookupPath(cue.ParsePath("version")); p.Exists() {
			// A version that is not known yet reads the latest, as a run does until it is.
			if n, err := p.Int64(); err == nil && n > 0 && n < 1<<31 {
				pin = int(n)
			}
		}
	}
	return name, pin, true
}

// isSecretNode reports whether a node is a pravo.#Secret or a pravo.#SecretVersion.
func isSecretNode(mn *mnode) bool {
	return mn.res != nil && mn.res.Builtin && (mn.res.Type == "secret" || mn.res.Type == "secret_version")
}

// secretReads lists the nodes of the specification that are a pravo.#Secret or a
// pravo.#SecretVersion, with the secret each names. A node whose name is not known before a
// command runs is listed apart. The caller holds the lock of the CUE values, as the build of a
// model does.
func (m *model) secretReads() (reads []secretRead, unresolved []string) {
	for _, addr := range m.order {
		mn := m.nodes[addr]
		if !isSecretNode(mn) {
			continue
		}
		name, pin, known := m.secretSpec(mn)
		if !known {
			unresolved = append(unresolved, addr)
			continue
		}
		reads = append(reads, secretRead{addr: addr, name: name, pin: pin, number: mn.res.Type == "secret_version"})
	}
	sort.SliceStable(reads, func(i, j int) bool { return reads[i].name < reads[j].name })
	return reads, unresolved
}

// shownSecret is a secret name fit to be shown, and whether it is a valid name for a secret. A
// valid name is shown as it is, since the page offers commands that name it. The name of an
// object in a bucket that is not valid has been put there by something other than pravo: it is
// cleaned up, and no command names it.
func shownSecret(name string) (string, bool) {
	if state.ValidSecretName(name) == nil {
		return name, true
	}
	return display(name), false
}

// buildSecrets sets the secrets of the state against the nodes of the specification that read
// them, and judges what it finds. It lists the clear parts of the versions and the transactions
// that wrote them: no value is opened, no key is needed, and nothing is written. The caller
// holds the lock of the CUE values.
func (m *model) buildSecrets(rev string) {
	doc := &Secrets{Version: APIVersion, Revision: rev, SpecOK: m.spec.root != nil, Secrets: []SecretEntry{}, Issues: []SecretIssue{}}
	defer func() {
		doc.Problems = len(doc.Issues)
		m.secs = doc
		m.secsJSON, _ = json.Marshal(doc)
	}()
	ctx := m.store.Context()
	doc.Sealed = m.store.Protection().Sealed != nil

	var rules state.RecordRules
	var rolesProblem string
	if f := m.srv.opts.SecretRules; f != nil {
		rules, doc.Checked, rolesProblem = f(m.store)
	}
	reads, unresolved := m.secretReads()
	doc.Unresolved = unresolved

	if !doc.Sealed {
		if n := len(reads); n > 0 {
			doc.Issues = append(doc.Issues, SecretIssue{Kind: "unsealed", Level: "bad", Title: "Secrets need a sealed state",
				Text: fmt.Sprintf("%s of the specification %s a secret, and the state is not sealed, so it cannot keep one. A command that reads a secret stops. The State page says how to seal the state.",
					countOf(n, "node", "nodes"), plural(n, "reads", "read"))})
		}
		return
	}

	list, err := m.store.Secrets(ctx, rules)
	if err != nil {
		doc.Issues = append(doc.Issues, SecretIssue{Kind: "unreadable", Level: "bad", Title: "The secrets cannot be read as trusted", Text: m.scrubText(cleanReason(err.Error()))})
		return
	}
	if rolesProblem != "" {
		doc.Issues = append(doc.Issues, SecretIssue{Kind: "roles", Level: "bad", Title: "The saved roles fail their check",
			Text: m.scrubText(cleanReason(rolesProblem)) + " No version is vouched for until this is fixed. Run pravo protect status for the details."})
	}

	byName := map[string]state.SecretSummary{}
	used := map[string][]SecretUse{}
	for _, s := range list {
		byName[s.Name] = s
	}
	for _, r := range reads {
		used[r.name] = append(used[r.name], SecretUse{Addr: r.addr, Pin: r.pin, Number: r.number})
	}
	versions := 0
	for _, s := range list {
		versions += s.Versions
	}

	// The versions are judged by the transactions that wrote them: all of them, or the latest of
	// each secret when there are too many to read.
	var problems []state.SecretProblem
	unchecked := 0
	if doc.Checked {
		if versions <= maxSecretVersions {
			v, err := m.store.VerifySecrets(ctx, m.transactions(), rules)
			if err != nil {
				doc.Issues = append(doc.Issues, SecretIssue{Kind: "unreadable", Level: "bad", Title: "The secrets cannot be read as trusted", Text: m.scrubText(cleanReason(err.Error()))})
				return
			}
			problems, unchecked = v.Problems, v.Unchecked
		} else {
			for _, s := range list {
				switch s.Head.Trust {
				case state.TrustProblem, state.TrustUnconfirmed:
					problems = append(problems, state.SecretProblem{Name: s.Name, Version: s.Head.Seq, Why: s.Head.Problem})
				case state.TrustUnchecked:
					unchecked++
				}
			}
			doc.Note = fmt.Sprintf("The state holds %s secret versions. Only the latest of each secret was checked here; pravo protect status checks them all.", commas(versions))
		}
	}
	bad := map[string]int{}
	for _, p := range problems {
		bad[p.Name]++
	}

	for _, s := range list {
		shown, _ := shownSecret(s.Name)
		e := SecretEntry{Name: shown, Status: "set", Version: s.Head.Seq, Versions: s.Versions, At: s.Head.At, Bad: bad[s.Name], UsedBy: []SecretUse{}}
		if !s.Live() {
			e.Status = "removed"
		}
		if doc.Checked {
			e.Trust = string(s.Head.Trust)
		}
		e.By = display(s.Head.Signer())
		if u := used[s.Name]; len(u) > maxSecretUsers {
			e.UsedBy, e.UsedMore = u[:maxSecretUsers], len(u)-maxSecretUsers
		} else if u != nil {
			e.UsedBy = u
		}
		doc.Secrets = append(doc.Secrets, e)
	}
	sort.SliceStable(doc.Secrets, func(i, j int) bool { return doc.Secrets[i].Name < doc.Secrets[j].Name })

	if is := m.untrustedSecrets(problems, byName, used); is != nil {
		doc.Issues = append(doc.Issues, *is)
	}
	if is := m.unsetSecrets(reads, byName, rules); is != nil {
		doc.Issues = append(doc.Issues, *is)
	}
	if unchecked > 0 {
		doc.Issues = append(doc.Issues, SecretIssue{Kind: "unchecked", Level: "chg",
			Title: countOf(unchecked, "secret version was", "secret versions were") + " signed by a key the access list does not list",
			Text:  "It could be a member who has left, or a key someone made. When the roles are locked in the state, a command refuses such a key."})
	}
	if !doc.Checked && rolesProblem == "" && len(list) > 0 {
		doc.Issues = append(doc.Issues, SecretIssue{Kind: "unchecked-all", Level: "chg", Title: "Who set each version is not checked",
			Text: "There is no access list in this repository to check the signatures against. The State page says how to add one."})
	}
	if len(doc.Issues) == 0 && len(list) > 0 {
		doc.AllClear = allClearSecrets(doc, len(reads) > 0)
	}
}

// allClearSecrets is the sentence for secrets that have no issue.
func allClearSecrets(doc *Secrets, reads bool) string {
	s := "Every version was written by someone who may set secrets"
	if doc.Note != "" {
		s = "The latest version of each secret was written by someone who may set secrets"
	}
	if reads && doc.SpecOK {
		return s + ", and every secret the specification reads is set."
	}
	return s + "."
}

// countOf is "1 node" or "3 nodes".
func countOf(n int, one, many string) string {
	return fmt.Sprintf("%s %s", commas(n), plural(n, one, many))
}

// userAddrs lists the addresses of the nodes that read a secret, up to the bound.
func userAddrs(uses []SecretUse) []string {
	var out []string
	for _, u := range uses {
		if len(out) == maxSecretUsers {
			break
		}
		out = append(out, u.Addr)
	}
	return out
}

// untrustedSecrets is the issue about versions that no transaction of someone who may write
// accounts for. Its commands are the ones that mend the secret when its latest version is the
// one at fault, and the one that shows its history otherwise.
func (m *model) untrustedSecrets(problems []state.SecretProblem, byName map[string]state.SecretSummary, used map[string][]SecretUse) *SecretIssue {
	if len(problems) == 0 {
		return nil
	}
	is := &SecretIssue{Kind: "untrusted", Level: "bad",
		Title: countOf(len(problems), "secret version", "secret versions") + " no signed record of a writer accounts for",
		Text:  "A command that reads the secret stops when its latest version is one of these. Set the secret again to replace it. The history of the secret says who wrote each version."}
	for i, p := range problems {
		if i == maxSecretItems {
			is.More = len(problems) - maxSecretItems
			break
		}
		shown, valid := shownSecret(p.Name)
		it := SecretIssueItem{Name: shown, Version: p.Version, Why: m.scrubText(cleanReason(p.Why)), UsedBy: userAddrs(used[p.Name])}
		if valid {
			if s, ok := byName[p.Name]; ok && p.Version == s.Versions {
				it.Command = "pravo secret set " + p.Name
			} else {
				it.Command = "pravo secret history " + p.Name
			}
		}
		is.Items = append(is.Items, it)
	}
	return is
}

// pinProblem says why version p of a secret cannot be read, and is empty when it can.
func (m *model) pinProblem(s state.SecretSummary, p int, rules state.RecordRules) string {
	if p > s.Versions {
		return fmt.Sprintf("version %d is asked for, and the latest is %d", p, s.Versions)
	}
	_, err := m.store.SecretHead(m.store.Context(), s.Name, p, rules)
	switch {
	case err == nil:
		return ""
	case errors.Is(err, state.ErrSecretPurged):
		return fmt.Sprintf("version %d was purged: its value is gone", p)
	}
	return m.scrubText(cleanReason(err.Error()))
}

// unsetSecrets is the issue about secrets that the specification reads and the state cannot
// give: not set, removed, a version that is not there or was purged, or a name that is not one.
func (m *model) unsetSecrets(reads []secretRead, byName map[string]state.SecretSummary, rules state.RecordRules) *SecretIssue {
	type want struct {
		name  string
		uses  []SecretUse
		pins  map[int]bool
		order int
	}
	var wants []*want
	index := map[string]*want{}
	for _, r := range reads {
		w := index[r.name]
		if w == nil {
			w = &want{name: r.name, pins: map[int]bool{}, order: len(wants)}
			index[r.name] = w
			wants = append(wants, w)
		}
		w.uses = append(w.uses, SecretUse{Addr: r.addr, Pin: r.pin, Number: r.number})
		if r.pin > 0 {
			w.pins[r.pin] = true
		}
	}
	var items []SecretIssueItem
	for _, w := range wants {
		shown, valid := shownSecret(w.name)
		it := SecretIssueItem{Name: shown, UsedBy: userAddrs(w.uses)}
		s, held := byName[w.name]
		switch {
		case !valid:
			it.Why = cleanReason(state.ValidSecretName(w.name).Error())
			it.Name = display(w.name)
		case !held:
			it.Why = "not set"
			it.Command = "pravo secret set " + w.name
		case !s.Live():
			it.Why = "was removed"
			it.Command = "pravo secret set " + w.name
		default:
			pins := make([]int, 0, len(w.pins))
			for p := range w.pins {
				pins = append(pins, p)
			}
			sort.Ints(pins)
			for _, p := range pins {
				if why := m.pinProblem(s, p, rules); why != "" {
					it.Version, it.Why = p, why
					break
				}
			}
		}
		if it.Why != "" {
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		return nil
	}
	is := &SecretIssue{Kind: "unset", Level: "bad",
		Title: countOf(len(items), "secret", "secrets") + " the specification reads cannot be read",
		Text:  "A command that reads one stops before it changes anything."}
	if len(items) > maxSecretItems {
		is.More = len(items) - maxSecretItems
		items = items[:maxSecretItems]
	}
	is.Items = items
	return is
}

// secretSchema is the declaration of pravo.#Secret and pravo.#SecretVersion, which the built-in
// package gives.
func secretSchema(number bool) []Attr {
	if number {
		return []Attr{
			{Name: "name", Type: "string", Kind: "required", Description: "The name of the secret."},
			{Name: "version", Type: "number", Kind: "computed", Description: "The number of the version that a command reads. It is not secret: it is an ordinary value."},
		}
	}
	return []Attr{
		{Name: "name", Type: "string", Kind: "required", Description: "The name of the secret, such as prod/db_password."},
		{Name: "version", Type: "number", Kind: "optional", Description: "The version to read. Without it, the latest one."},
		{Name: "value", Type: "string", Kind: "computed", Sensitive: true, Description: "What the secret holds."},
	}
}

// secretEntry finds a secret of the state in the document of the secrets, by the name the
// specification gives it.
func (m *model) secretEntry(name string) *SecretEntry {
	if m.secs == nil {
		return nil
	}
	shown, _ := shownSecret(name)
	for i := range m.secs.Secrets {
		if m.secs.Secrets[i].Name == shown {
			return &m.secs.Secrets[i]
		}
	}
	return nil
}

// secretDetail fills the detail of a pravo.#Secret or a pravo.#SecretVersion. The value of a
// secret is ephemeral and shows nowhere: it is kept out of the state, out of a saved plan and
// out of this page, so the node says which version the state holds, which is no secret, and what
// to run when the state holds none.
func (m *model) secretDetail(mn *mnode, d *Detail) {
	number := mn.res.Type == "secret_version"
	d.Schema = secretSchema(number)
	ss := m.spec
	ss.cueMu.Lock()
	name, pin, known := m.secretSpec(mn)
	ss.cueMu.Unlock()

	nameAt := Attr{Name: "name", Type: "string", Kind: "required"}
	if known {
		nameAt.Value = display(name)
	} else {
		nameAt.Unknown = true
	}
	d.Attrs = append(d.Attrs, nameAt)

	e := (*SecretEntry)(nil)
	if known {
		e = m.secretEntry(name)
	}
	live := e != nil && e.Status == "set"
	switch {
	case number:
		at := Attr{Name: "version", Type: "number", Kind: "computed"}
		if live {
			at.Value = e.Version
		} else {
			at.Unknown = true
		}
		d.Attrs = append(d.Attrs, at)
	default:
		if pin > 0 {
			d.Attrs = append(d.Attrs, Attr{Name: "version", Type: "number", Kind: "optional", Value: pin})
		}
		d.Attrs = append(d.Attrs, Attr{Name: "value", Type: "string", Kind: "computed", Sensitive: true, Redacted: "sensitive"})
		d.Notes = append(d.Notes, "A secret shows no value. It is read from the sealed state each time a command runs, and it is kept out of the state, out of a saved plan and out of this page.")
	}

	shown, valid := shownSecret(name)
	switch {
	case !known:
		d.Notes = append(d.Notes, "The name of the secret depends on something that is not known before a command runs.")
	case m.secs == nil || !m.secs.Sealed:
		d.Notes = append(d.Notes, "The state is not sealed, so it cannot keep secrets, and a command that reads this one stops.")
	case !valid:
		d.Notes = append(d.Notes, "This is not a name that a secret can have: "+cleanReason(state.ValidSecretName(name).Error())+".")
	case e == nil:
		d.Notes = append(d.Notes, fmt.Sprintf("The state holds no secret named %s, so a command that reads this one stops. Set it with \"pravo secret set %s\".", shown, shown))
	case e.Status == "removed":
		d.Notes = append(d.Notes, fmt.Sprintf("The secret %s was removed, so a command that reads this one stops. Set it again with \"pravo secret set %s\".", shown, shown))
	case pin > 0:
		d.Notes = append(d.Notes, fmt.Sprintf("A command reads version %d, as this node asks. The latest is version %d.", pin, e.Version))
	default:
		d.Notes = append(d.Notes, fmt.Sprintf("A command reads version %d, the latest of %s.", e.Version, countOf(e.Versions, "version", "versions")))
	}
}
