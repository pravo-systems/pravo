package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/serve"
)

func serveCmd() *cobra.Command {
	var port int
	var noOpen bool
	var planFile string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Show the specification, the state and a saved plan in a browser, read-only",
		Long: `Serve a read-only viewer on the loopback interface and open it in the default browser.

The viewer shows the specification and the state as a graph of modules and resources, with the attributes of each (sensitive ones withheld), the CUE that writes it, its provider schema and its history. With --plan it shows a saved plan on top. It updates when the state, the specification or the plan file changes.

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
			opts := serve.Options{Dir: dir, Tags: flagTags, SysTags: flagSysTags, Version: Version, OpenState: openStateReadOnly}
			if planFile != "" {
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
	cmd.Flags().StringVar(&planFile, "plan", "", "a saved plan to show on top of the graph")
	addTagFlags(cmd)
	return cmd
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
