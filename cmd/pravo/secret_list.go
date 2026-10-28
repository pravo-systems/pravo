package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/ui"
)

// ---------- secret list ----------

func secretListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the secrets of the state: names, versions and who set them last",
		Long: `Lists the secrets of the state with their latest version, when it was set and by whom. It
shows no value and needs no key. A version that no transaction of a key allowed to write accounts for is
shown under the table and ends the command with exit code 7.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := openSecrets(cmd, secretLook)
			if err != nil {
				return err
			}
			defer r.close()
			list, err := r.st.Secrets(r.ctx, r.rules)
			if err != nil {
				return secretErr(err)
			}
			var bad []state.SecretSummary
			for _, s := range list {
				if s.Head.Trust == state.TrustProblem {
					bad = append(bad, s)
				}
			}
			if flags.JSON {
				if list == nil {
					list = []state.SecretSummary{}
				}
				if err := emitJSON(list); err != nil {
					return err
				}
				return secretsUntrusted(len(bad))
			}
			if len(list) == 0 {
				out.Println("No secrets. Set one with: pravo secret set NAME")
				return nil
			}
			rows := make([][]string, 0, len(list)+1)
			rows = append(rows, []string{"NAME", "VERSION", "SET", "BY", ""})
			for _, s := range list {
				h := s.Head
				note := ""
				switch {
				case h.Kind == state.SecretKindRemoved:
					note = "removed"
				case h.Trust == state.TrustUnconfirmed:
					note = "not finished"
				case h.Trust == state.TrustUnchecked:
					note = "key not in the readers file"
				}
				rows = append(rows, []string{printable(s.Name), strconv.Itoa(h.Seq), agoAt(h), safeName(h.Signer()), note})
			}
			printTable(rows, 1)
			for _, s := range bad {
				out.Println(out.C(ui.Red, fmt.Sprintf("Untrusted: %s version %d: %s", printable(s.Name), s.Head.Seq, printable(s.Head.Problem))))
			}
			return secretsUntrusted(len(bad))
		},
	}
}

// secretsUntrusted is the error of a listing that held versions that nothing accounts for.
func secretsUntrusted(n int) error {
	if n == 0 {
		return nil
	}
	return engine.WithCode(engine.ExitUntrusted, errSilent)
}

// agoAt is when a version was written, as ago writes it.
func agoAt(i state.SecretInfo) string {
	at := i.At
	return ago(&at)
}

// printTable writes rows with their columns aligned; the columns from right on are right-aligned.
// The last column is not padded.
func printTable(rows [][]string, right int) {
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, cell := range row {
			if n := len([]rune(cell)); n > widths[i] {
				widths[i] = n
			}
		}
	}
	for n, row := range rows {
		var b strings.Builder
		for i, cell := range row {
			pad := strings.Repeat(" ", widths[i]-len([]rune(cell)))
			switch {
			case i == len(row)-1:
				b.WriteString(cell)
			case i == right:
				b.WriteString(pad + cell + "  ")
			default:
				b.WriteString(cell + pad + "  ")
			}
		}
		line := strings.TrimRight(b.String(), " ")
		if n == 0 {
			line = out.C(ui.Dim, line)
		}
		out.Println(line)
	}
}

// ---------- secret history ----------

// secretHistoryJSON is a version in the JSON of history.
type secretHistoryJSON struct {
	state.SecretInfo
	Note string `json:"note,omitempty"`
}

func secretHistoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "history <name>",
		Short: "Show every version of a secret: when, by whom, and the note",
		Long: `Lists the versions of the secret NAME, newest first, with when each was set, who set it and
what became of it: a value, a removal, or a value that was purged. A version that no transaction
of a key allowed to write accounts for is marked, and ends the command with exit code 7. It shows
no value. The notes are sealed with the values, so they are shown to an identity that is a reader.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := state.ValidSecretName(name); err != nil {
				return err
			}
			r, err := openSecrets(cmd, secretLook)
			if err != nil {
				return err
			}
			defer r.close()
			hist, err := r.st.SecretHistory(r.ctx, name, r.rules)
			if err != nil {
				return secretErr(err)
			}
			var notes map[int]string
			if !r.st.Locked() {
				var nerr error
				if notes, nerr = r.st.SecretNotes(r.ctx, name); nerr != nil && !errors.Is(nerr, state.ErrNotReader) {
					out.Warning("the notes cannot be shown: " + printable(nerr.Error()))
				}
			}
			bad := 0
			for _, h := range hist {
				if h.Trust == state.TrustProblem {
					bad++
				}
			}
			if flags.JSON {
				docs := make([]secretHistoryJSON, 0, len(hist))
				for i := len(hist) - 1; i >= 0; i-- {
					docs = append(docs, secretHistoryJSON{SecretInfo: hist[i], Note: notes[hist[i].Seq]})
				}
				if err := emitJSON(docs); err != nil {
					return err
				}
				return secretsUntrusted(bad)
			}
			out.Println(out.C(ui.Bold, printable(name)))
			for i := len(hist) - 1; i >= 0; i-- {
				h := hist[i]
				what := h.Kind
				switch {
				case h.Kind == state.SecretKindPurged:
					what = "purged"
				case h.Trust == state.TrustUnchecked:
					what += ", key not in the readers file"
				case h.Trust == state.TrustUnconfirmed:
					what += ", not finished"
				}
				out.Printf("  v%-4d %s  %-12s %s\n", h.Seq, h.At.Local().Format("2006-01-02 15:04:05"), safeName(h.Signer()), what)
				if n := notes[h.Seq]; n != "" {
					out.Printf("        %s\n", out.C(ui.Dim, printable(n)))
				}
				if h.Trust == state.TrustProblem {
					out.Println(out.C(ui.Red, "        Untrusted: "+printable(h.Problem)))
				}
			}
			return secretsUntrusted(bad)
		},
	}
}
