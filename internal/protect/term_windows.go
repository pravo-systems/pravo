//go:build windows

package protect

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func readNoEcho(prompt string) ([]byte, error) {
	in, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		return nil, errors.New("no console to ask for a passphrase: use --passphrase-file")
	}
	defer in.Close()
	h := windows.Handle(in.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return nil, errors.New("no console to ask for a passphrase: use --passphrase-file")
	}
	if err := windows.SetConsoleMode(h, mode&^windows.ENABLE_ECHO_INPUT); err != nil {
		return nil, err
	}
	defer windows.SetConsoleMode(h, mode)
	fmt.Fprint(os.Stderr, prompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	fmt.Fprintln(os.Stderr)
	if err != nil && line == "" {
		return nil, errors.New("no passphrase entered")
	}
	return []byte(strings.TrimRight(line, "\r\n")), nil
}
