package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/pravo"
)

// Seams for tests.
var (
	stdoutIsTerminal = func() bool {
		fd := os.Stdout.Fd()
		return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
	}
	// askSecretValue asks for a value on the terminal, twice, with no echo.
	askSecretValue = func(prompt string) ([]byte, error) { return protect.PassphraseFromTerminal(prompt, true) }
)

func secretCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Keep secrets in the sealed state and use them in runs",
		Long: `A secret is a named value that the sealed state keeps for its readers, as a
series of versions: every set is a new one. A run reads a secret as pravo.#Secret, which
feeds the configuration of a provider and write-only attributes, and is never written to the
state, to a plan or to a log. pravo.#SecretVersion is the number of the version, which is not
secret: wire it to the version of a write-only attribute and a new version rotates the value.

Everyone who can read the state can read every secret in it; a secret for other readers
belongs in another state. A writer sets a secret, an admin removes one and forgets its old
values, and a reader reads. Each of these is a signed transaction (pravo state log), so
"secret history" says who set which version, and a version that no key allowed to write
accounts for is refused by the commands that read it.

The value is never an argument, so that the shell history and the process list do not
hold it: "set" reads it from standard input, from a file, from an environment variable
or, on a terminal, from a prompt that does not show it. The state has to be sealed
(pravo protect init --encrypt).

Names are words of letters, digits, dots, dashes and underscores, with a slash between
parts, such as prod/db_password.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	pf := cmd.PersistentFlags()
	pf.StringVar(&flags.AccessDir, "access-dir", "", accessDirUsage)
	// The tags select the state, so the commands that open it take them too.
	pf.StringArrayVarP(&flags.Tags, "tag", "t", nil, tagUsage)
	pf.BoolVarP(&flags.SysTags, "inject-vars", "T", false, sysTagsUsage)
	cmd.AddCommand(secretSetCmd(), secretGetCmd(), secretListCmd(), secretHistoryCmd(), secretRmCmd(), secretPurgeCmd(), secretExecCmd())
	return cmd
}

// secretAccess is what a secret command does with the state.
type secretAccess int

const (
	// secretLook lists names, versions and signers. It opens no value, so it needs no key.
	secretLook secretAccess = iota
	// secretRead opens values.
	secretRead
	// secretWrite adds versions.
	secretWrite
)

// secretRun is what the secret commands share: the open state and the rules that judge
// the signatures of its versions.
type secretRun struct {
	ctx   context.Context
	root  *config.Root
	st    *state.Store
	rf    readersFile
	rules state.RecordRules
	// readers are the readers the roles of the state leave, the keys that judge a signature.
	readers []protect.Reader
}

func openSecrets(cmd *cobra.Command, access secretAccess) (*secretRun, error) {
	ctx := cmd.Context()
	root, err := loadRoot()
	if err != nil {
		return nil, err
	}
	rf := loadReadersFile(root.Dir, flags.AccessDir)
	if access == secretWrite && stateMissing(ctx, root) {
		// Opening a store for writing would make one, and a first writer makes it in the clear.
		if rf.Found {
			return nil, engine.WithCode(engine.ExitRefused, errors.New(missingStateProblem(rf)))
		}
		return nil, engine.WithCode(engine.ExitRefused, fmt.Errorf("%w: there is no state yet. Seal one first: list the readers in %s (pravo id new prints a line) and run pravo protect init --encrypt", state.ErrSecretsNeedSeal, rf.shownDir()))
	}
	var st *state.Store
	switch access {
	case secretWrite:
		st, err = openStateStore(ctx, root)
	case secretRead:
		st, err = openStateValues(ctx, root)
	default:
		st, err = openStateReadOnly(ctx, root)
	}
	if err != nil {
		return nil, secretErr(err)
	}
	st.SetActor(flags.Actor, flags.Reason, pravo.GitRevision(root.Dir))
	eff, rs, err := rf.withRoles(st)
	if err != nil {
		st.Close()
		return nil, engine.WithCode(engine.ExitUntrusted, errors.New(printable(err.Error())))
	}
	return &secretRun{ctx: ctx, root: root, st: st, rf: rf, rules: recordRules(eff.Readers, rs), readers: eff.Readers}, nil
}

func (r *secretRun) close() { r.st.Close() }

// need refuses an identity whose role is lower than the role that the command needs. A readers
// file without roles lets every key do everything.
func (r *secretRun) need(role protect.Role, what string) error {
	if r.st.Locked() {
		return nil
	}
	if msg, code := roleProblem(r.rf, r.st, role, what); msg != "" {
		return engine.WithCode(code, errors.New(msg))
	}
	return nil
}

// secretErr gives the errors of the secret store the exit codes of the commands, and says what to
// do about them. Texts that came from the bucket are made printable.
func secretErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, state.ErrSecretsNeedSeal):
		return engine.WithCode(engine.ExitRefused, err)
	case errors.Is(err, state.ErrSecretChanged):
		return engine.WithCode(engine.ExitStale, fmt.Errorf("%w; run the command again", err))
	case errors.Is(err, state.ErrNoSecret):
		return fmt.Errorf("%w; pravo secret list shows the secrets of the state", err)
	case errors.Is(err, state.ErrSecretRemoved):
		return fmt.Errorf("%w; set it again with pravo secret set", err)
	case errors.Is(err, state.ErrUntrusted):
		return engine.WithCode(engine.ExitUntrusted, errors.New(printable(err.Error())))
	}
	return err
}

// secretCommandLine is the command that the transaction of a change shows. It holds the name and
// never the value, the note or the place the value came from.
func secretCommandLine(parts ...string) string {
	return "pravo secret " + strings.Join(parts, " ")
}

// ---------- secret set ----------

func secretSetCmd() *cobra.Command {
	var file, fromEnv, note string
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Set a secret: a new version with the value from standard input, a file or the terminal",
		Long: `Writes a new version of the secret NAME, creating the secret if the state has none of
that name. The value is sealed to the readers of the state and the change is signed.

The value comes from standard input unless it is a terminal, from a file (--file, and
- for standard input), from an environment variable (--from-env), or from a prompt on
the terminal that does not show it and asks twice. It is never an argument.

From standard input or a file, one line break after a value of one line is dropped, so that
"echo" and the terminal give the same secret. A value of several lines, such as a key, is kept
as it is. A value is at most 1 MiB and may be binary. --note keeps a line of text with the
version, sealed with it, that "history" shows to the readers.

It needs an identity that is a writer when the readers file gives roles.`,
		Example: `  printf %s "$PASSWORD" | pravo secret set prod/db_password
  pravo secret set prod/tls_key --file key.pem --note "reissued after the audit"
  pravo secret set prod/api_token --from-env API_TOKEN`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.ValidSecretName(args[0]); err != nil {
				return err
			}
			if file != "" && fromEnv != "" {
				return errors.New("name one source for the value: --file or --from-env")
			}
			r, err := openSecrets(cmd, secretWrite)
			if err != nil {
				return err
			}
			defer r.close()
			value, err := readSecretValue(args[0], file, fromEnv)
			if err != nil {
				return err
			}
			defer clear(value)
			info, err := r.st.SetSecret(r.ctx, args[0], value, note, secretCommandLine("set", args[0]))
			if err != nil {
				return secretErr(err)
			}
			if flags.JSON {
				return emitJSON(info)
			}
			out.Printf("Set %s, version %d.\n", printable(info.Name), info.Seq)
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "read the value from this file; - is standard input")
	cmd.Flags().StringVar(&fromEnv, "from-env", "", "read the value from this environment variable")
	cmd.Flags().StringVar(&note, "note", "", "a line of text to keep, sealed, with this version")
	return cmd
}

// readSecretValue reads the value of a secret from the source that the flags name.
func readSecretValue(name, file, fromEnv string) ([]byte, error) {
	switch {
	case fromEnv != "":
		v, ok := environment().Lookup(fromEnv)
		if !ok || v == "" {
			return nil, fmt.Errorf("the environment variable %s is not set, or it is empty", fromEnv)
		}
		return limitedValue([]byte(v))
	case file != "" && file != "-":
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return readValue(f)
	case file == "" && stdinIsTerminal():
		v, err := askSecretValue("Value for " + name + " (not shown): ")
		if err != nil {
			return nil, err
		}
		return limitedValue(v)
	}
	return readValue(os.Stdin)
}

// readValue reads a value from a stream, and drops the line break after a value of one line.
func readValue(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, state.MaxSecretValue+1))
	if err != nil {
		return nil, err
	}
	if len(b) > state.MaxSecretValue {
		clear(b)
		return nil, fmt.Errorf("a secret holds at most %d bytes", state.MaxSecretValue)
	}
	if bytes.Count(b, []byte("\n")) == 1 {
		b = bytes.TrimSuffix(bytes.TrimSuffix(b, []byte("\n")), []byte("\r"))
	}
	return b, nil
}

func limitedValue(b []byte) ([]byte, error) {
	if len(b) > state.MaxSecretValue {
		clear(b)
		return nil, fmt.Errorf("a secret holds at most %d bytes", state.MaxSecretValue)
	}
	return b, nil
}

// ---------- secret get ----------

// secretValueJSON is the JSON form of a version with its value.
type secretValueJSON struct {
	state.SecretInfo
	Value       *string `json:"value,omitempty"`
	ValueBase64 string  `json:"value_base64,omitempty"`
	Note        string  `json:"note,omitempty"`
}

func secretGetCmd() *cobra.Command {
	var version int
	cmd := &cobra.Command{
		Use:   "get <name>",
		Short: "Print the value of a secret",
		Long: `Prints the latest value of the secret NAME, or the value of one version (--version).
It prints the bytes of the value as they are, with a line break after them only when the
output is a terminal and the value has none. A value that is not text is not printed to a
terminal: redirect it to a file. With --json the value is "value", or "value_base64" when it is
not valid UTF-8, next to the version, the signer and the note.

It needs an identity that is a reader of the state. In a repository whose readers file gives
roles, a version that no transaction of a writer accounts for is refused with exit code 7.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.ValidSecretName(args[0]); err != nil {
				return err
			}
			if version < 0 {
				return errors.New("--version is a number from 1")
			}
			r, err := openSecrets(cmd, secretRead)
			if err != nil {
				return err
			}
			defer r.close()
			v, err := r.st.ReadSecret(r.ctx, args[0], version, r.rules)
			if err != nil {
				return secretErr(err)
			}
			defer clear(v.Value)
			r.warnAbout(v.SecretInfo)
			if flags.JSON {
				doc := secretValueJSON{SecretInfo: v.SecretInfo, Note: v.Note}
				if utf8.Valid(v.Value) {
					s := string(v.Value)
					doc.Value = &s
				} else {
					doc.ValueBase64 = base64.StdEncoding.EncodeToString(v.Value)
				}
				return emitJSON(doc)
			}
			terminal := stdoutIsTerminal()
			if terminal && !printableValue(v.Value) {
				return errors.New("the value is not text, and is not printed to a terminal: redirect it to a file, or use --json")
			}
			if _, err := os.Stdout.Write(v.Value); err != nil {
				return err
			}
			if terminal && !bytes.HasSuffix(v.Value, []byte("\n")) {
				_, err = os.Stdout.WriteString("\n")
			}
			return err
		},
	}
	cmd.Flags().IntVar(&version, "version", 0, "the version to print (default the latest)")
	return cmd
}

// printableValue reports whether a terminal can show the value: it is valid UTF-8 without control
// characters other than line breaks and tabs.
func printableValue(b []byte) bool {
	if !utf8.Valid(b) {
		return false
	}
	for _, c := range string(b) {
		if c < 0x20 && c != '\n' && c != '\r' && c != '\t' || c == 0x7f || c >= 0x80 && c < 0xa0 {
			return false
		}
	}
	return true
}

// warnAbout says on standard error what is doubtful about a version that was read.
func (r *secretRun) warnAbout(info state.SecretInfo) {
	switch info.Trust {
	case state.TrustUnchecked:
		out.Warning(fmt.Sprintf("version %d of %s was signed by %s, whose key is not in the readers file", info.Seq, printable(info.Name), safeName(info.Signer())))
	case state.TrustProblem, state.TrustUnconfirmed:
		out.Warning(fmt.Sprintf("version %d of %s: %s. The readers file gives no roles, so it is read anyway", info.Seq, printable(info.Name), printable(info.Problem)))
	}
}

// ---------- secret rm and purge ----------

func secretRmCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a secret: runs that read it fail until it is set again",
		Long: `Ends the secret NAME with a removal, as the next version. The values it held stay in the
state until pravo secret purge forgets them, so that the history shows what was there. Setting the
name again continues the numbering.

It needs an identity that is an admin when the readers file gives roles.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := state.ValidSecretName(name); err != nil {
				return err
			}
			r, err := openSecrets(cmd, secretWrite)
			if err != nil {
				return err
			}
			defer r.close()
			if err := r.need(protect.RoleAdmin, "remove a secret"); err != nil {
				return err
			}
			hist, err := r.st.SecretHistory(r.ctx, name, r.rules)
			if err != nil {
				return secretErr(err)
			}
			if hist[len(hist)-1].Kind == state.SecretKindRemoved {
				return secretErr(fmt.Errorf("%s: %w", name, state.ErrSecretRemoved))
			}
			ok, err := confirm(fmt.Sprintf("Remove the secret %s? Runs that read it will fail until it is set again.", name), yes)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("secret rm cancelled; nothing was changed")
			}
			info, err := r.st.RemoveSecret(r.ctx, name, secretCommandLine("rm", name))
			if err != nil {
				return secretErr(err)
			}
			if flags.JSON {
				return emitJSON(info)
			}
			out.Printf("Removed %s, version %d. Its values stay in the state until pravo secret purge %s --keep 0.\n", printable(info.Name), info.Seq, printable(info.Name))
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask before the secret is removed")
	return cmd
}

func secretPurgeCmd() *cobra.Command {
	var (
		yes  bool
		keep int
	)
	cmd := &cobra.Command{
		Use:   "purge <name>",
		Short: "Forget the old values of a secret",
		Long: `Forgets the values of the old versions of the secret NAME and keeps the newest --keep of
them (1 unless you say). A version that is purged stays in the history, without its value, with the
transaction that purged it. A secret that is live keeps at least its latest value; one that was removed
may keep none (--keep 0).

The values are gone from the state. A bucket that keeps the earlier versions of its objects keeps these
too, and anyone who could read the secret has read it: a value that leaked needs a new one, not a purge.

It needs an identity that is an admin when the readers file gives roles.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := state.ValidSecretName(name); err != nil {
				return err
			}
			if keep < 0 {
				return errors.New("--keep is a number of versions, 0 or more")
			}
			r, err := openSecrets(cmd, secretWrite)
			if err != nil {
				return err
			}
			defer r.close()
			if err := r.need(protect.RoleAdmin, "forget the values of a secret"); err != nil {
				return err
			}
			hist, err := r.st.SecretHistory(r.ctx, name, r.rules)
			if err != nil {
				return secretErr(err)
			}
			values := 0
			for _, h := range hist {
				if h.Kind == state.SecretKindValue {
					values++
				}
			}
			if hist[len(hist)-1].Kind != state.SecretKindRemoved && keep < 1 {
				return fmt.Errorf("%s is live: keep at least its latest value, or remove the secret first with pravo secret rm", name)
			}
			doomed := max(values-keep, 0)
			if doomed == 0 {
				if flags.JSON {
					return emitJSON(map[string]any{"name": name, "purged": 0})
				}
				out.Printf("Nothing to forget: %s holds %s.\n", printable(name), plural(values, "value", "values"))
				return nil
			}
			ok, err := confirm(fmt.Sprintf("Forget %s of %s, keeping the newest %d?", plural(doomed, "old value", "old values"), name, keep), yes)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("secret purge cancelled; nothing was changed")
			}
			n, err := r.st.PurgeSecret(r.ctx, name, keep, secretCommandLine("purge", name, "--keep", strconv.Itoa(keep)))
			if err != nil {
				return secretErr(err)
			}
			if flags.JSON {
				return emitJSON(map[string]any{"name": name, "purged": n})
			}
			out.Printf("Forgot %s of %s.\n", plural(n, "value", "values"), printable(name))
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask before the values are forgotten")
	cmd.Flags().IntVar(&keep, "keep", 1, "how many of the newest values to keep")
	return cmd
}

// ---------- secret exec ----------

var envVarName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// secretBinding is one --env VAR=NAME[@VERSION] of exec.
type secretBinding struct {
	variable, name string
	version        int
}

func parseSecretBindings(specs []string) ([]secretBinding, error) {
	if len(specs) == 0 {
		return nil, errors.New("name at least one secret to put in the environment: --env VAR=NAME")
	}
	seen := map[string]bool{}
	var out []secretBinding
	for _, spec := range specs {
		variable, ref, ok := strings.Cut(spec, "=")
		if !ok || !envVarName.MatchString(variable) {
			return nil, fmt.Errorf("--env %q: want VAR=NAME, with a variable name of letters, digits and underscores", spec)
		}
		if seen[variable] {
			return nil, fmt.Errorf("--env: the variable %s is set twice", variable)
		}
		seen[variable] = true
		name, vs, hasVersion := strings.Cut(ref, "@")
		b := secretBinding{variable: variable, name: name}
		if err := state.ValidSecretName(name); err != nil {
			return nil, fmt.Errorf("--env %q: %w", spec, err)
		}
		if hasVersion {
			n, err := strconv.Atoi(vs)
			if err != nil || n < 1 {
				return nil, fmt.Errorf("--env %q: a version is a number from 1, as in %s@3", spec, name)
			}
			b.version = n
		}
		out = append(out, b)
	}
	return out, nil
}

func secretExecCmd() *cobra.Command {
	var bindings []string
	cmd := &cobra.Command{
		Use:   "exec --env VAR=NAME... -- COMMAND [ARGUMENTS]",
		Short: "Run a command with secrets in its environment",
		Long: `Reads the secrets, puts each in the environment variable that --env names (--env
VAR=NAME, or VAR=NAME@3 for version 3 of it) and runs the command with that environment. The
values are not written to a file, and only the command sees them. Everything before the command
is read by pravo and everything after it belongs to the command. If a secret cannot be read, the
command does not run. The exit code is that of the command.

It needs an identity that is a reader of the state. Secrets with a null byte cannot be put in
an environment variable.`,
		Example: `  pravo secret exec --env PGPASSWORD=prod/db_password -- psql -h db.internal -U app`,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			parsed, err := parseSecretBindings(bindings)
			if err != nil {
				return err
			}
			r, err := openSecrets(cmd, secretRead)
			if err != nil {
				return err
			}
			defer r.close()
			e := environment()
			var values [][]byte
			defer func() {
				for _, v := range values {
					clear(v)
				}
			}()
			for _, b := range parsed {
				v, err := r.st.ReadSecret(r.ctx, b.name, b.version, r.rules)
				if err != nil {
					return secretErr(err)
				}
				values = append(values, v.Value)
				r.warnAbout(v.SecretInfo)
				if bytes.IndexByte(v.Value, 0) >= 0 {
					return fmt.Errorf("%s holds a null byte, which an environment variable cannot", b.name)
				}
				e = e.With(b.variable, string(v.Value))
			}
			r.close() // the command does not need the state
			c := exec.CommandContext(cmd.Context(), args[0], args[1:]...)
			c.Env = e.Environ()
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			err = c.Run()
			var exit *exec.ExitError
			switch {
			case err == nil:
				return nil
			case errors.As(err, &exit):
				code := exit.ExitCode()
				if code < 0 { // killed by a signal
					code = 1
				}
				return engine.WithCode(engine.ExitCode(code), errSilent)
			}
			return err
		},
	}
	cmd.Flags().StringArrayVar(&bindings, "env", nil, "put a secret in an environment variable: --env VAR=NAME, or VAR=NAME@3 for a version")
	// What follows the first word that is not a flag belongs to the command.
	cmd.Flags().SetInterspersed(false)
	return cmd
}
