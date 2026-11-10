package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/pravo"
)

func idCmd(rt *runContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "id",
		Short: "Make and show the identity that reads a sealed state",
		Long: `An identity is a name, a signing key and a reader key. The reader key is what
opens a sealed state. The identity is a file in the identity directory, by
default ~/.config/pravo/id, with the reader key sealed under a passphrase. It
is kept apart from the provider cache in ~/.pravo.

An identity touches no state. Its public line goes into
.pravo/access/readers.txt of the repository, which lists who may read.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	cmd.AddCommand(idNewCmd(rt), idShowCmd(rt))
	return cmd
}

func idNewCmd(rt *runContext) *cobra.Command {
	var name, sshFile, outFile, unlock, agentKey string
	var ci bool
	cmd := &cobra.Command{
		Use:   "new",
		Short: "Make an identity and print the line for the readers file",
		Long: `Makes a reader key and a signing key, writes them to the identity file named by --name
(default: the user name) in the identity directory, and prints the public line for .pravo/access/readers.txt.
The reader key is sealed under a passphrase, which is asked for twice on the
terminal, or read from $PRAVO_PASSPHRASE when stdin is not a terminal.
An identity that exists is never overwritten.

--unlock agent seals it under a key that the ssh-agent derives instead: opening
the identity asks the agent to sign a fixed challenge, so whatever the agent asks
for before it signs (in 1Password, an approval that Touch ID can give) is what
unlocks it, and no passphrase is set. The agent must hold an ssh-ed25519 key; --agent-key picks one
when it holds several, by its fingerprint or by part of its comment. The key has
to stay in the agent: the identity file cannot be opened without it.

--ssh FILE uses the ssh-ed25519 public key FILE (or FILE.pub) as the signing
key. Without it a signing key is made and kept in the identity file. Signing is
not used while the state is sealed and has no roles.

--ci makes an identity for a job, which has no person to type a passphrase. It
needs --name, the name of the job, and never takes the name of the user. The
file holds both private keys in the clear, with mode 0600, in the file given by
--out. Store its contents as the CI secret PRAVO_IDENTITY and delete the file.
Nothing is written to the identity directory.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return fmt.Errorf("pravo id new takes no argument, but got %q; the name is a flag: pravo id new --name %s", args[0], args[0])
			}
			return noArgs(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIDNew(rt, name, sshFile, outFile, ci, unlock, agentKey)
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "name of the identity (default: the user name; required with --ci)")
	f.StringVar(&sshFile, "ssh", "", "use this ssh-ed25519 public key (FILE or FILE.pub) as the signing key")
	f.BoolVar(&ci, "ci", false, "make an identity for a CI job: no passphrase, written to --out")
	f.StringVar(&outFile, "out", "", "with --ci: the file to write the identity to")
	f.StringVar(&unlock, "unlock", "passphrase", "what opens the identity: passphrase, or agent (a key of the ssh-agent)")
	f.StringVar(&agentKey, "agent-key", "", "with --unlock agent: the key of the ssh-agent, by fingerprint or part of its comment")
	return cmd
}

func idShowCmd(rt *runContext) *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print an identity's keys and the line for the readers file",
		Long: `Prints the signing key, the reader key and the public line of the identity. It
reads only the public parts of the file and never asks for the passphrase.
The identity is found as for every command: --name (or --identity),
$PRAVO_IDENTITY, then the identity directory ($PRAVO_IDENTITY_DIR, default ~/.config/pravo/id).`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				name = rt.flags.Identity // the global flag means the same
			}
			return runIDShow(rt, name)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "name of the identity (same as --identity)")
	return cmd
}

func runIDNew(rt *runContext, name, sshFile, outFile string, ci bool, unlock, agentKey string) error {
	if err := checkIDNewFlags(name, outFile, ci, unlock, agentKey); err != nil {
		return err
	}
	if name == "" {
		name = osUser()
		if name == "" {
			return errors.New("no name for the identity: pass --name NAME")
		}
	}
	var sshLine, sshFrom string
	if sshFile != "" {
		var err error
		if sshLine, sshFrom, err = readSSHPublicKey(sshFile); err != nil {
			return err
		}
	}
	// An identity that exists is found before anything is asked for.
	path, err := newIdentityPath(name, outFile, ci)
	if err != nil {
		return err
	}
	var unlocker protect.AgentKey
	if unlock == "agent" {
		if unlocker, err = agentUnlocker(agentKey); err != nil {
			return err
		}
	}
	id, err := protect.NewIdentity(name, sshLine)
	if err != nil {
		return err
	}
	defer id.Close()
	var data []byte
	switch {
	case ci:
		data, err = id.MarshalUnsealed()
	case unlock == "agent":
		data, err = id.MarshalAgentSealed(unlocker.Pub)
	default:
		var pass []byte
		if pass, err = newPassphrase(name); err != nil {
			return err
		}
		data, err = id.MarshalSealed(pass)
		clear(pass)
	}
	if err != nil {
		return err
	}
	defer clear(data)
	if ci {
		err = protect.WritePrivateFile(path, data, false)
	} else {
		path, err = protect.SaveIdentity(environment(), name, data, false)
		if err != nil && strings.Contains(err.Error(), "already exists") {
			return fmt.Errorf("identity %s already exists at %s", name, tilde(path))
		}
	}
	if err != nil {
		return err
	}
	sum := identitySummary(id.Name, id.SignPub, id.SignVia, id.Reader.Public())
	if sshFrom != "" {
		sum.Source = "from " + sshFrom
	}
	keeping := "sealed with a passphrase"
	unlocked := map[string]any{"by": "passphrase"}
	if ci {
		unlocked = map[string]any{"by": "none"}
	}
	if unlock == "agent" {
		fp := protect.SSHFingerprint(unlocker.Pub)
		keeping = "sealed under ssh-agent key " + fp
		unlocked = map[string]any{"by": "ssh-agent", "key": fp, "comment": unlocker.Comment}
	}
	if rt.flags.JSON {
		return emitJSON(map[string]any{"name": id.Name, "path": path, "ci": ci, "unlock": unlocked,
			"signing": map[string]string{"alg": "ssh-ed25519", "fingerprint": sum.Fingerprint, "source": sum.Source},
			"reader":  sum.Reader, "line": sum.Line})
	}
	if ci {
		rt.out.Printf("Identity %s created for a job. It has no passphrase: protect the file.\n", id.Name)
		rt.out.Printf("  private  %s   (store it as the CI secret PRAVO_IDENTITY, then delete this file)\n", path)
		rt.out.Printf("  public   %s\n", sum.Line)
		if runtime.GOOS == "windows" {
			rt.out.Println("  Check who can read the file with: icacls " + path)
		}
		return nil
	}
	rt.out.Printf("Identity %s created\n", id.Name)
	rt.out.Printf("  signing  ssh-ed25519 %s  %s\n", sum.Fingerprint, sum.Source)
	rt.out.Printf("  reader   %s  %s in %s\n", sum.Reader, keeping, tilde(path))
	rt.out.Println()
	rt.out.Println(readersFileAsk(rt))
	rt.out.Println("They add the role as its last word: role:reader (the default), role:writer or role:admin.")
	rt.out.Println()
	fmt.Println("  " + sum.Line)
	return nil
}

// newPassphrase asks for the passphrase that seals a new identity: twice on the
// terminal, or from $PRAVO_PASSPHRASE when stdin is not a terminal.
func newPassphrase(name string) ([]byte, error) {
	if stdinIsTerminal() {
		return askNewPassphrase("Passphrase for " + name + ": ")
	}
	for _, v := range []string{"PRAVO_PASSPHRASE", "PRAVO_IDENTITY_PASSPHRASE"} {
		if s := os.Getenv(v); s != "" {
			return []byte(s), nil
		}
	}
	return nil, fmt.Errorf("refusing to ask for the passphrase of %s: stdin is not a terminal; set PRAVO_PASSPHRASE, or use --ci for an identity without a passphrase", name)
}

var askNewPassphrase = func(prompt string) ([]byte, error) { return protect.PassphraseFromTerminal(prompt, true) }

// readSSHPublicKey reads the public key named by --ssh: FILE.pub, or FILE if it
// is itself a public key. A private key is never read.
func readSSHPublicKey(file string) (line, from string, err error) {
	candidates := []string{file + ".pub", file}
	if strings.HasSuffix(file, ".pub") {
		candidates = []string{file}
	}
	var last error
	for _, c := range candidates {
		data, err := readSmallFile(c, 16<<10)
		if err != nil {
			last = err
			continue
		}
		text := strings.TrimSpace(string(data))
		if !strings.HasPrefix(text, "ssh-") && !strings.HasPrefix(text, "sk-") && !strings.HasPrefix(text, "ecdsa-") {
			last = fmt.Errorf("%s is not an ssh public key; pass the .pub file", c)
			continue
		}
		first, _, _ := strings.Cut(text, "\n")
		if _, err := protect.ParseSSHPublicLine(first); err != nil {
			return "", "", fmt.Errorf("%s: %w", c, err)
		}
		return first, tilde(c), nil
	}
	if last == nil {
		last = errors.New("no public key found")
	}
	return "", "", fmt.Errorf("--ssh %s: %w", file, last)
}

// readSmallFile reads at most limit bytes of a file and fails on a longer one.
func readSmallFile(path string, limit int64) ([]byte, error) {
	// Opening a named pipe waits for a writer: look before opening.
	if st, err := os.Stat(path); err == nil && !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, limit)
	}
	return data, nil
}

type idSummary struct {
	Name, Fingerprint, Source, Reader, Line string
}

func identitySummary(name string, sign []byte, via string, reader protect.ReaderPublic) idSummary {
	r := pravo.Reader{Name: name, Sign: sign, Key: reader}
	s := idSummary{Name: name, Fingerprint: protect.SSHFingerprint(sign), Reader: reader.String(), Line: r.String()}
	if via == "ssh-agent" {
		s.Source = "through ssh-agent"
	} else {
		s.Source = "new key, kept in the identity file"
	}
	return s
}

// publicIdentity is what an identity file shows without a passphrase.
type publicIdentity struct {
	Name    string `json:"name"`
	Signing struct {
		Alg string `json:"alg"`
		Pub string `json:"pub"`
		Via string `json:"via"`
	} `json:"signing"`
	Reader struct {
		Pub string `json:"pub"`
	} `json:"reader"`
	Seal json.RawMessage `json:"seal"`
}

// unlockedBy says what opens an identity file, from its public parts: "passphrase",
// "ssh-agent" with the fingerprint of the key, or "none" for the file of a job.
func (p publicIdentity) unlockedBy() (by, key string) {
	if len(p.Seal) == 0 || string(p.Seal) == "null" {
		return "none", ""
	}
	var seal struct {
		KDF   string `json:"kdf"`
		Agent string `json:"agent"`
	}
	if json.Unmarshal(p.Seal, &seal) != nil || seal.Agent == "" {
		return "passphrase", ""
	}
	pub, err := protect.ParseSSHBlob(seal.Agent)
	if err != nil {
		return "ssh-agent", ""
	}
	return "ssh-agent", protect.SSHFingerprint(pub)
}

func runIDShow(rt *runContext, name string) error {
	taken.mu.Lock()
	src, err := findIdentity(identityOptions{Name: name})
	taken.mu.Unlock()
	if err != nil {
		return err
	}
	data, where := src.data, "PRAVO_IDENTITY"
	if data == nil {
		where = tilde(src.path)
		if data, err = readSmallFile(src.path, 64<<10); err != nil {
			if src.fromEnv {
				return errors.New("PRAVO_IDENTITY is neither the contents of an identity file nor the path of one that can be read")
			}
			return err
		}
	}
	var p publicIdentity
	if err := json.Unmarshal(data, &p); err != nil || p.Name == "" {
		return fmt.Errorf("%s is not an identity file", where)
	}
	sign, err := protect.ParseSSHBlob(p.Signing.Pub)
	if err != nil {
		return fmt.Errorf("%s: signing key: %w", where, err)
	}
	reader, err := protect.ParsePublic(p.Reader.Pub)
	if err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	sum := identitySummary(p.Name, sign, p.Signing.Via, reader)
	by, agentKey := p.unlockedBy()
	if rt.flags.JSON {
		unlocked := map[string]any{"by": by}
		if agentKey != "" {
			unlocked["key"] = agentKey
		}
		return emitJSON(map[string]any{"name": p.Name, "path": where, "sealed": by != "none", "unlock": unlocked,
			"signing": map[string]string{"alg": "ssh-ed25519", "fingerprint": sum.Fingerprint, "source": sum.Source},
			"reader":  sum.Reader, "line": sum.Line})
	}
	keeping := "no passphrase"
	switch by {
	case "passphrase":
		keeping = "sealed with a passphrase"
	case "ssh-agent":
		keeping = "sealed under ssh-agent key " + agentKey
	}
	rt.out.Printf("Identity %s (%s)\n", p.Name, where)
	rt.out.Printf("  signing  ssh-ed25519 %s  %s\n", sum.Fingerprint, sum.Source)
	rt.out.Printf("  reader   %s  %s\n", sum.Reader, keeping)
	rt.out.Println()
	rt.out.Println(readersFileAsk(rt))
	rt.out.Println("They add the role as its last word: role:reader (the default), role:writer or role:admin.")
	rt.out.Println()
	fmt.Println("  " + sum.Line)
	return nil
}

// readersFileAsk is the sentence that asks for the line to go to the readers file. It names the
// file the settings give, one for each environment when they differ, and .pravo/access/readers.txt
// outside a project.
func readersFileAsk(rt *runContext) string {
	const tail = " this line, and nothing else."
	list, err := config.ReadEnvironmentSettings(".", config.LoadOptions{Tags: rt.flags.Tags, SysTags: rt.flags.SysTags, Env: environment()})
	if err == nil && len(list) > 0 {
		var parts []string
		seen := map[string]bool{}
		for _, es := range list {
			if es.Err != nil {
				continue
			}
			path := displayPath(filepath.Join(accessDirsFrom(".", es.AccessDir)[0], readersFileName))
			if !seen[path] {
				seen[path] = true
				parts = append(parts, path)
			}
		}
		switch {
		case len(parts) == 1:
			return "Give whoever keeps " + parts[0] + tail
		case len(parts) > 1:
			return "Give whoever keeps the readers file of each environment you are to read (" + strings.Join(parts, ", ") + ")" + tail
		}
	}
	if root, err := loadRoot(rt); err == nil {
		return "Give whoever keeps " + readersOf(rt, root).shown() + tail
	}
	return "Give whoever keeps .pravo/access/readers.txt" + tail
}

// checkIDNewFlags refuses the flags of "id new" that do not go together.
func checkIDNewFlags(name, outFile string, ci bool, unlock, agentKey string) error {
	switch unlock {
	case "passphrase", "agent":
	default:
		return fmt.Errorf("--unlock %s: use passphrase or agent", unlock)
	}
	if ci && unlock == "agent" {
		return errors.New("--unlock agent is for a person; an identity for a job has no passphrase and no agent to ask")
	}
	if agentKey != "" && unlock != "agent" {
		return errors.New("--agent-key goes with --unlock agent")
	}
	if ci && name == "" {
		return errors.New("--ci needs --name NAME: name the identity after the job, such as deploy-bot. A name taken from the user would be the name of a person in the readers file")
	}
	if ci && outFile == "" {
		return errors.New("--ci needs --out FILE: the identity of a job is written to a file you store as a CI secret")
	}
	if !ci && outFile != "" {
		return errors.New("--out is for --ci; an identity without it is written to the identity directory")
	}
	return nil
}

// newIdentityPath is where the new identity is written, which must not exist yet: the
// file that --out names for a job, and the identity directory otherwise.
func newIdentityPath(name, outFile string, ci bool) (string, error) {
	if ci {
		if _, err := os.Lstat(outFile); err == nil {
			return "", fmt.Errorf("%s already exists", outFile)
		}
		return outFile, nil
	}
	dir, err := identityDir()
	if err != nil {
		return "", err
	}
	if _, err := protect.IdentityPath(environment(), name); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if _, err := os.Lstat(path); err == nil {
		return "", fmt.Errorf("identity %s already exists at %s", name, tilde(path))
	}
	return path, nil
}

// agentUnlocker picks the key of the ssh-agent that the new identity is sealed under.
func agentUnlocker(agentKey string) (protect.AgentKey, error) {
	keys, err := protect.AgentKeys()
	if err != nil {
		return protect.AgentKey{}, err
	}
	if len(keys) == 0 {
		where := ""
		if a := protect.AgentAddress(); a != "" {
			where = " at " + tilde(a)
		}
		return protect.AgentKey{}, fmt.Errorf("the ssh-agent%s holds no ssh-ed25519 key (is it locked, or is the key an RSA or ECDSA one?). pravo looks for the agent in $PRAVO_SSH_AUTH_SOCK, then the IdentityAgent of ssh_config, then $SSH_AUTH_SOCK", where)
	}
	return protect.PickAgentKey(keys, agentKey)
}
