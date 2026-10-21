// Command fakeoci serves an in-memory OCI registry on localhost, for trying
// module publishing without a real registry:
//
//	go run ./tools/fakeoci -addr 127.0.0.1:5000
//
//	export CUE_REGISTRY='example.com=localhost:5000+insecure'
//	pravo mod publish v0.1.0
//
// Its contents are lost when it stops.
//
// Once it accepts connections it prints "listening on ADDR" on standard
// output, with the port that was bound, so that a parent process can start it
// on port 0 and read the port back. With -exit-on-eof it exits when standard
// input reaches end of file, so that a parent that dies cannot leave it
// behind. tools/capture starts it this way.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"

	"cuelabs.dev/go/oci/ociregistry/ocimem"
	"cuelabs.dev/go/oci/ociregistry/ociserver"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		log.Fatal(err)
	}
}

// run serves until the server fails or, with -exit-on-eof, until stdin ends.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("fakeoci", flag.ContinueOnError)
	flags.SetOutput(stderr)
	addr := flags.String("addr", "127.0.0.1:5000", "listen address; port 0 picks a free port")
	exitOnEOF := flags.Bool("exit-on-eof", false, "exit when standard input reaches end of file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: ociserver.New(ocimem.New(), nil)}
	failed := make(chan error, 1)
	go func() { failed <- srv.Serve(ln) }()

	fmt.Fprintf(stderr, "fakeoci: serving an in-memory OCI registry on %s (contents are lost on exit)\n", ln.Addr())
	fmt.Fprintf(stdout, "listening on %s\n", ln.Addr())

	eof := make(chan struct{})
	if *exitOnEOF {
		go func() {
			io.Copy(io.Discard, stdin)
			close(eof)
		}()
	}
	select {
	case err := <-failed:
		return err
	case <-eof:
		return srv.Close()
	}
}
