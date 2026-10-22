//go:build !windows

package protect

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// readNoEcho reads a line from /dev/tty with ECHO cleared, and restores the
// terminal on every path, including a signal handled by the caller (the caller
// should not trap SIGINT around this; the default kills the process with the
// terminal in the state we set, so we also restore in a deferred call and the
// shell's own `stty sane` covers the rest).
func readNoEcho(prompt string) ([]byte, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, errors.New("no terminal to ask for a passphrase: use --passphrase-file")
	}
	defer tty.Close()
	fd := int(tty.Fd())
	old, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		return nil, errors.New("no terminal to ask for a passphrase: use --passphrase-file")
	}
	noecho := *old
	noecho.Lflag &^= unix.ECHO
	noecho.Lflag |= unix.ICANON | unix.ISIG
	if err := unix.IoctlSetTermios(fd, ioctlWriteTermios, &noecho); err != nil {
		return nil, err
	}
	defer unix.IoctlSetTermios(fd, ioctlWriteTermios, old)
	fmt.Fprint(tty, prompt)
	line, err := bufio.NewReader(tty).ReadString('\n')
	fmt.Fprintln(tty)
	if err != nil && line == "" {
		return nil, errors.New("no passphrase entered")
	}
	return []byte(strings.TrimRight(line, "\r\n")), nil
}
