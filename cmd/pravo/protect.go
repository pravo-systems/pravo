package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/pravo"
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
	pf.StringVar(&flags.AccessDir, "access-dir", "", accessDirUsage)
	// The tags select the state, so the commands that open it take them too.
	pf.StringArrayVarP(&flags.Tags, "tag", "t", nil, tagUsage)
	pf.BoolVarP(&flags.SysTags, "inject-vars", "T", false, sysTagsUsage)
	cmd.AddCommand(protectCheckCmd(), protectInitCmd(), protectStatusCmd(), protectRewrapCmd(), protectRolesCmd(), protectRecoverCmd(), protectUnsealCmd(), protectGCCmd())
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
	return &protectRun{ctx: cmd.Context(), root: root, rf: loadReadersFile(root.Dir, flags.AccessDir)}, nil
}

// needAdmin refuses an identity that is not an admin in the readers file of the repository.
// It does nothing for a state with no key to judge by, or a readers file without roles.
func (p *protectRun) needAdmin(st *state.Store, what string) error {
	if st.Locked() {
		return nil
	}
	if msg, code := roleProblem(p.rf, st, protect.RoleAdmin, what); msg != "" {
		return engine.WithCode(code, errors.New(msg))
	}
	return nil
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
			bb = pravo.ReadOnlyBackend(b)
		}
		return state.OpenBackendWith(p.ctx, bb, state.Options{Identity: id})
	}
	if st, err = mk(nil); err != nil || !st.Locked() || !needKey {
		return st, nil, nil, err
	}
	id, idErr = resolveIdentity(identityOptions{Name: flags.Identity, Prompt: identityPrompt, Readers: pravo.CurrentReaders(st.Protection())})
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

var nameShape = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ---------- readers file against the store ----------

// listedNames is how many names a line of the readers file against the store
// lists before it says how many more there are.
const listedNames = 5

// ---------- protect check ----------

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

// ---------- protect init ----------

// ---------- protect status ----------

// ---------- protect rewrap ----------
