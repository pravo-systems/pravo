// Command fakehttp serves an in-memory http state server on localhost, for
// trying the http state backend without a real server:
//
//	go run ./tools/fakehttp -addr 127.0.0.1:9000
//
//	$settings: state: {backend: "http", url: "http://127.0.0.1:9000/demo"}
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

	"github.com/pravo-systems/pravo/internal/fakehttp"
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
	flags := flag.NewFlagSet("fakehttp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	addr := flags.String("addr", "127.0.0.1:9000", "listen address; port 0 picks a free port")
	exitOnEOF := flags.Bool("exit-on-eof", false, "exit when standard input reaches end of file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: fakehttp.New()}
	failed := make(chan error, 1)
	go func() { failed <- srv.Serve(ln) }()

	fmt.Fprintf(stderr, "fakehttp: serving an in-memory http state server on %s (contents are lost on exit)\n", ln.Addr())
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
