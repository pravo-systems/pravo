package serve

import (
	"slices"
	"strings"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
)

// maxLapse bounds the runs of GET /api/timelapse: the newest ones, so that a project with a long
// history still answers at once. Nodes that the runs left out show as there from the start.
const maxLapse = 400

// maxLapseChanges bounds the changes sent for one run; the rest is counted in More.
const maxLapseChanges = 5000

// TimeLapse is GET /api/timelapse: the runs that wrote nodes, oldest first, each with what it
// did to which node. The page replays them over a fixed drawing of the estate. A plan writes
// nothing, so it is not a step; a run that stopped halfway is, with what it had done.
type TimeLapse struct {
	Version  int    `json:"version"`
	Revision string `json:"revision"`
	// Steps are the runs, oldest first.
	Steps []LapseStep `json:"steps"`
	// Older counts the runs before the first of Steps that were left out for room.
	Older int `json:"older,omitempty"`
}

// LapseStep is one run.
type LapseStep struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"`
	Status  string    `json:"status"`
	Time    time.Time `json:"time"`
	User    string    `json:"user,omitempty"`
	Actor   string    `json:"actor,omitempty"`
	Command string    `json:"command,omitempty"`
	Reason  string    `json:"reason,omitempty"`
	// GitRevision is the revision of the specification the run ran against.
	GitRevision string `json:"git_revision,omitempty"`
	// Signature is what the signatures of the run come to, as in a history entry.
	Signature string        `json:"signature,omitempty"`
	Signer    string        `json:"signer,omitempty"`
	Changes   []LapseChange `json:"changes"`
	// More counts the changes of the run that were left out for room.
	More int `json:"more,omitempty"`
}

// LapseChange is what a run did to a node: Action is "add" (a create or an import), "change", "replace",
// "remove" (a delete, a forget or the old address of a move) or "move" (the new address of a move). Namespace
// is the path of the namespace the node sits in, as in a graph node.
type LapseChange struct {
	Addr      string `json:"addr"`
	Namespace string `json:"namespace"`
	Action    string `json:"action"`
}

// lapseAction names what a recorded action did to a node, or "" when it did not write the node: a read, a
// drift, a refresh, a deposed copy or an unsettled apply is not a change of the estate.
func lapseAction(a string) string {
	switch a {
	case "create", "partial-create", "import":
		return "add"
	case "update", "partial", "provider-move", "relink":
		return "change"
	case "replace":
		return "replace"
	case "delete", "forget", "move-from":
		return "remove"
	case "move", "move-to":
		return "move"
	}
	return ""
}

// timelapse lists the runs that wrote nodes, built once for the model from the recorded transactions.
func (m *model) timelapse() *TimeLapse {
	m.lapseOnce.Do(func() {
		out := &TimeLapse{Version: APIVersion, Revision: m.rev, Steps: []LapseStep{}}
		secret := state.SecretAddr("")
		txns := m.transactions() // newest first
		for _, t := range slices.Backward(txns) {
			if t.Kind == "plan" {
				continue
			}
			step := LapseStep{ID: t.ID, Kind: t.Kind, Status: t.Status, Time: t.Started, User: t.User, Actor: t.Actor,
				Command: scrubCommand(t.Command), Reason: t.Reason, GitRevision: t.GitRevision, Changes: []LapseChange{}}
			for _, c := range t.Changes {
				act := lapseAction(c.Action)
				if act == "" || strings.HasPrefix(c.Addr, secret) || c.Addr == state.EnvironmentAddr {
					continue
				}
				if len(step.Changes) >= maxLapseChanges {
					step.More++
					continue
				}
				ns := ""
				if p := splitAddr(c.Addr); len(p.names) > 1 {
					ns = p.prefix(len(p.names) - 1)
				}
				step.Changes = append(step.Changes, LapseChange{Addr: c.Addr, Namespace: ns, Action: act})
			}
			if len(step.Changes) == 0 {
				continue
			}
			if verify := m.srv.opts.VerifyTxn; verify != nil {
				t := t
				step.Signature, step.Signer = verify(m.store, &t)
			}
			out.Steps = append(out.Steps, step)
		}
		if n := len(out.Steps); n > maxLapse {
			out.Older = n - maxLapse
			out.Steps = out.Steps[n-maxLapse:]
		}
		m.lapse = out
	})
	return m.lapse
}
