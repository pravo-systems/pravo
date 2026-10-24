package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/plog"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/serve"
	"github.com/pravo-systems/pravo/internal/state"
)

func serveCmd() *cobra.Command {
	var port int
	var noOpen bool
	var planFile string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Show the specification, the state and a saved plan in a browser, read-only",
		Long: `Serve a read-only viewer on the loopback interface and open it in the default browser.

The viewer shows the specification and the state as a graph of namespaces and resources, with the attributes of each (sensitive ones withheld), the CUE that writes it, its provider schema and its history. With --plan it shows a saved plan on top: a plan file, or the id of a plan stored in the state with "pravo plan --store". The plans stored in the state are listed in the viewer whether or not --plan is given, and any of them can be laid over the graph. It updates when the state, the specification, the plan file or the stored plans change.

A state that is sealed (see "pravo protect") is read with your reader identity, named by --identity or found as the other commands find it, and a sealed identity asks for its passphrase on the terminal before the server starts. Without an identity, or with one that is not a reader, the viewer lists the resources and shows no value; --identity none shows the state as someone with no key sees it. The State page shows who can read the state and suggests commands to copy. It runs none of them.

The server writes nothing: no state, no lock, no lock file, no generated package. It starts no provider and downloads nothing: it reads the provider schemas that are cached already. It listens on 127.0.0.1 only, and every request needs the token in the address that is printed.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if port < 0 || port > 65535 {
				return fmt.Errorf("--port %d is not a port number", port)
			}
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			v := &viewerState{name: flagIdentity, dir: dir, accessDir: loadReadersFile(dir, flagAccessDir).Dir}
			defer v.close()
			opts := serve.Options{Dir: dir, Tags: flagTags, SysTags: flagSysTags, Version: Version, OpenState: v.open, Protection: v.protection, AccessDir: v.accessDir, VerifyTxn: v.verifyTxn, VerifyPlan: v.verifyPlan}
			if isStoredRef(planFile) {
				// The id of a plan in the state (a file of that name wins). The plan is
				// looked up when the state is read, with the tags given here.
				opts.PlanID = planFile
			} else if planFile != "" {
				pf, err := engine.ReadPlanFile(planFile)
				if err != nil {
					return err
				}
				abs, err := filepath.Abs(planFile)
				if err != nil {
					return err
				}
				opts.PlanFile = abs
				if len(flagTags) == 0 && !flagSysTags {
					// The plan was made with these values; the specification is read with them.
					opts.Tags, opts.SysTags = pf.Tags, pf.SysTags
				}
			}
			srv, err := serve.New(opts)
			if err != nil {
				return err
			}
			v.started()
			ln, err := serve.Listen(port)
			if err != nil {
				return err
			}
			url := srv.URL(ln.Addr())
			if d := srv.StateDescription(); d != "" {
				out.Printf("pravo serve: reading state from %s\n", d)
			}
			if flagJSON {
				emitLine(map[string]any{"url": url, "token": srv.Token(), "port": ln.Addr().(*net.TCPAddr).Port})
			} else {
				out.Printf("pravo serve: %s\n", url)
			}
			if !noOpen {
				if err := openBrowser(url); err != nil {
					out.Warning("could not open a browser: " + err.Error() + ". Open the address above.")
				}
			}
			// The first signal cancels the context of the command; the server then
			// ends its event streams, stops its watcher and returns.
			return srv.Serve(cmd.Context(), ln)
		},
	}
	cmd.Flags().IntVar(&port, "port", 0, "the port to listen on (0 picks a free one)")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "do not open a browser")
	cmd.Flags().StringVar(&planFile, "plan", "", "a saved plan to show on top of the graph: a file, or the id of a plan stored in the state (plan-2f9c1a4e)")
	addTagFlags(cmd)
	addAccessDirFlag(cmd)
	return cmd
}

// viewerState opens the state for the viewer and says how it is protected. The
// identity is found once, when the first sealed state is read, which happens
// while the server is made and before it listens: a passphrase is asked for on
// the terminal there and never while a request is served. It is held here, in
// memory, and no part of a response holds it.
type viewerState struct {
	// name is the --identity flag: a name, "none", or empty.
	name string
	dir  string
	// accessDir is the directory of the readers file.
	accessDir string

	mu       sync.Mutex
	resolved bool
	ident    *protect.Identity
	// locked is set when an identity exists and its passphrase was not available.
	locked bool
	// refused is set when the state is sealed and the identity is not a reader of it.
	refused bool
	// listening is set once the server listens. From then on nothing is asked of the
	// person: a state that turns sealed while the viewer runs is read with an
	// identity that needs no passphrase, or with none.
	listening bool
	// openStore is state.OpenBackendWith; a test replaces it.
	openStore func(context.Context, state.Backend, state.Options) (*state.Store, error)
}

// viewerIdentity says what the viewer knows about who it reads as, for the Access
// view. It holds the public part of the identity only.
type viewerIdentity struct {
	// locked: an identity exists and its passphrase was not available.
	locked bool
	// refused: the identity is not a reader of the sealed state. name and key are
	// those of the identity.
	refused   bool
	name, key string
}

// close wipes the identity.
func (v *viewerState) close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.ident != nil {
		v.ident.Close()
		v.ident = nil
	}
}

func (v *viewerState) identity() (id *protect.Identity, locked, resolved bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.ident, v.locked, v.resolved
}

func (v *viewerState) who() viewerIdentity {
	v.mu.Lock()
	defer v.mu.Unlock()
	w := viewerIdentity{locked: v.locked, refused: v.refused && v.ident != nil}
	if v.ident != nil && v.ident.Reader != nil {
		w.name, w.key = v.ident.Name, v.ident.Reader.Public().String()
	}
	return w
}

// setRefused records whether the state turned the identity away, and says whether
// that is a change.
func (v *viewerState) setRefused(on bool) (changed bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	changed = v.refused != on
	v.refused = on
	return changed
}

// openWith opens the store read-only with the identity. An identity that is not a
// reader of the state is not an error for the viewer: the store opens without a
// key, which lists the resources and shows no value.
func (v *viewerState) openWith(ctx context.Context, b state.Backend, id *protect.Identity) (*state.Store, error) {
	open := v.openStore
	if open == nil {
		open = state.OpenBackendWith
	}
	if id != nil {
		st, err := open(ctx, serve.ReadOnly(b), state.Options{Identity: id})
		switch {
		case err == nil:
			v.setRefused(false)
			return st, nil
		case errors.Is(err, state.ErrNotReader):
			if v.setRefused(true) {
				out.Warning("the identity " + id.Name + " is not a reader of the state, so resources are listed and no value is shown.")
			}
		default:
			return nil, err
		}
	}
	return open(ctx, serve.ReadOnly(b), state.Options{})
}

// started says that the server listens: the passphrase is not asked for any more.
func (v *viewerState) started() {
	v.mu.Lock()
	v.listening = true
	v.mu.Unlock()
}

// resolve finds the identity, once. The readers are the reader keys of the
// sealed state, which choose among several identity files. A person who names
// an identity that cannot be used gets an error before the server listens; one
// who names none, and has none, gets a viewer that lists the resources and
// shows no value, and so does anyone once the server listens.
func (v *viewerState) resolve(readers ...protect.ReaderPublic) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.resolved {
		return nil
	}
	v.resolved = true
	if v.name == "none" {
		out.Printf("pravo serve: the state is sealed; reading it with no identity\n")
		return nil
	}
	id, err := resolveIdentity(identityOptions{Name: v.name, Prompt: !v.listening, Readers: readers})
	switch {
	case err == nil:
		v.ident = id
		out.Printf("pravo serve: the state is sealed; using the identity %s\n", id.Name)
	case errors.Is(err, errNoIdentity) && (v.name == "" || v.listening):
		out.Warning("the state is sealed and no identity was found, so resources are listed and no value is shown. Start the viewer with --identity NAME.")
	case (v.name == "" || v.listening) && strings.Contains(err.Error(), "sealed with a passphrase"):
		v.locked = true
		out.Warning("the identity is sealed with a passphrase and none was available, so resources are listed and no value is shown. Run the viewer in a terminal, or set PRAVO_PASSPHRASE.")
	case v.listening:
		out.Warning("the state is sealed and the identity could not be used, so resources are listed and no value is shown: " + err.Error())
	default:
		v.resolved = false
		return err
	}
	return nil
}

// open opens the state for reading. The first look is without a key: only a state
// that turns out to be sealed asks for the identity, so that a plain state never
// asks for a passphrase.
func (v *viewerState) open(ctx context.Context, root *config.Root) (*state.Store, error) {
	var b state.Backend
	switch root.State.Backend {
	case "s3":
		var err error
		if b, err = s3Backend(ctx, root); err != nil {
			return nil, err
		}
	default:
		path := root.StateFile()
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			return state.Open(path) // says that the path is a file
		}
		b = &state.LocalBackend{Dir: path}
	}
	began := time.Now()
	id, _, resolved := v.identity()
	st, err := v.openWith(ctx, b, id)
	if err != nil {
		return nil, err
	}
	if !resolved && isProtected(st.Protection()) {
		if err := v.resolve(currentReaders(st.Protection())...); err != nil {
			return nil, err
		}
		if id, _, _ = v.identity(); id != nil {
			if st, err = v.openWith(ctx, b, id); err != nil {
				return nil, err
			}
		}
	}
	plog.Debug(ctx, "state opened for reading", "backend", backendName(root.State.Backend), "location", st.Dir(), "nodes", st.Len(), "took", took(began))
	return st, nil
}

func isProtected(p state.Protection) bool {
	return p.State == "sealed" || p.State == "migrating" || p.State == "unsealing"
}

// protection is the function the Access view calls for each revision: what the
// store says of itself, what the readers file says, and who the viewer is.
func (v *viewerState) protection(ctx context.Context, st *state.Store, readersFile string) serve.Facts {
	rf := loadReadersFile(v.dir, filepath.Dir(readersFile))
	return accessFacts(st.Protection(), st.Locked(), v.who(), rf)
}

// accessFacts turns what the state says of its protection, and the readers
// file, into the facts the viewer shows. It holds no key.
func accessFacts(p state.Protection, locked bool, id viewerIdentity, rf readersFile) serve.Facts {
	var f serve.Facts
	s := &f.Store
	s.State, s.Format, s.Locked = p.State, p.Format, locked
	if s.State == "unsealing" {
		// The viewer shows a change in progress as one state; the migration says which way.
		s.State = "migrating"
	}
	s.Records, s.Plain, s.Unreadable = p.Records, p.Plain, p.Unreadable
	if m := p.Sealed; m != nil {
		for _, g := range m.Groups {
			n := p.ByGroup[g.ID]
			s.Sealed += n
			locks := 0
			for _, l := range g.Locks {
				if !m.IsRecoveryLock(l) {
					locks++
				}
			}
			s.Groups = append(s.Groups, serve.GroupFacts{ID: g.ID, Current: g.ID == m.Current, Locks: locks, Records: n})
			if g.ID != m.Current {
				continue
			}
			for _, l := range g.Locks {
				if !l.Key.IsZero() && !m.IsRecoveryLock(l) {
					s.Readers = append(s.Readers, serve.ReaderFacts{Name: l.Name, Key: l.Key.String()})
				}
			}
		}
		if rs := m.Status(nil).Recovery; rs != nil {
			_, file := recoveryStatusText(p, rf)
			s.Recovery = &serve.RecoveryFacts{K: rs.K, N: rs.N, Key: rs.Key.String(), Locked: rs.Locked, Groups: rs.Groups, File: file, Unverified: p.RecoveryUnverified}
		}
	}
	if mg := p.Migration; mg != nil {
		s.Migration = &serve.MigrationFacts{Started: mg.Started, By: mg.By, Done: mg.Done, Total: mg.Total, Unseal: mg.Unseal}
	}
	for _, h := range p.History {
		s.History = append(s.History, serve.ChangeFacts{At: h.At, By: h.By, What: h.What, Added: h.Added, Removed: h.Removed})
	}

	f.File = serve.FileFacts{Path: rf.Path}
	switch {
	case !rf.Found:
		f.File.State = "missing"
	case rf.Err != nil:
		f.File.State = "invalid"
		var le *readersFileError
		if errors.As(rf.Err, &le) {
			f.File.Errors = []serve.LineError{{Line: le.Line, Reason: le.Reason}}
		} else {
			f.File.Errors = []serve.LineError{{Reason: "cannot be used"}}
		}
	case len(rf.Readers) == 0:
		// "protect" refuses a file with no readers (exit 6), so the page says that it
		// does not load, and does not suggest a rewrap that would fail.
		f.File.State = "invalid"
		f.File.Errors = []serve.LineError{{Reason: "lists no readers"}}
	default:
		f.File.State = "ok"
		for _, r := range rf.Readers {
			f.File.Readers = append(f.File.Readers, serve.ReaderFacts{Name: r.Name, Key: r.Key.String(), SSH: protect.SSHBlob(r.Sign), Line: r.Line})
		}
	}

	if isProtected(p) {
		y := p.You
		switch y.State {
		case "reader", "not-reader":
			f.You = &serve.YouFacts{State: y.State, Name: y.Name, Key: y.Key}
		default:
			switch {
			case id.refused:
				f.You = &serve.YouFacts{State: "not-reader", Name: id.name, Key: id.key}
			case id.locked:
				f.You = &serve.YouFacts{State: "locked"}
			default:
				f.You = &serve.YouFacts{State: "no-identity"}
			}
		}
	}
	return f
}

// openBrowser asks the system to open the address in the default browser. It
// starts the opener and does not wait for the browser.
func openBrowser(url string) error {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	if err := c.Start(); err != nil {
		return err
	}
	go func() { _ = c.Wait() }()
	return nil
}

// verifyTxn checks the signatures of a transaction against the readers file, for the
// history of a node. A state that is not sealed, or a repository with no readers file, has
// nothing to check against and returns no status.
func (v *viewerState) verifyTxn(st *state.Store, t *state.Transaction) (string, string) {
	if st.Protection().Sealed == nil {
		return "", ""
	}
	readers, ok := attributionReaders(loadReadersFile(v.dir, v.accessDir))
	if !ok {
		return "", ""
	}
	verdict := state.VerifyTransaction(st.Lineage(), t, readers)
	return verdict.Overall().Key(), verdict.Signer
}

// verifyPlan checks the signatures of a stored plan against the readers file, for the plan
// view. It has nothing to check in a state that is not sealed; without a readers file it
// says so.
func (v *viewerState) verifyPlan(ctx context.Context, st *state.Store, id, digest string) (*serve.PlanSignatures, bool) {
	if st.Protection().Sealed == nil {
		return nil, false
	}
	rf := loadReadersFile(v.dir, v.accessDir)
	readers, ok := attributionReaders(rf)
	if !ok {
		return &serve.PlanSignatures{Items: []serve.PlanSignature{}, Note: notChecked(rf)}, true
	}
	verdicts, err := engine.NewPlanStoreFor(st).SignaturesOf(ctx, id, digest, readers)
	if err != nil {
		return &serve.PlanSignatures{Items: []serve.PlanSignature{}, Note: "not checked: the signatures could not be read"}, true
	}
	out := &serve.PlanSignatures{Items: []serve.PlanSignature{}}
	for _, d := range verdicts {
		it := serve.PlanSignature{Status: d.Status.Key(), At: d.At}
		if d.Status == protect.SigVerified {
			it.Signer = d.Signer
		} else {
			it.Claimed = d.Claimed
		}
		out.Items = append(out.Items, it)
	}
	return out, true
}
