package protect

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strings"
)

// A passphrase comes from, in this order:
//
//  1. --passphrase-file FILE (or "-" for one line on stdin): for scripts;
//  2. the terminal, with echo off, asked for on /dev/tty (CONIN$ on Windows);
//  3. otherwise an error that names (1). There is deliberately no environment
//     variable: providers run with pravo's environment (internal/plugin/client.go
//     passes os.Environ()), so a secret in the environment reaches every provider.

// PassphraseFromReader reads one line (up to 1 KiB) and trims the newline.
func PassphraseFromReader(r io.Reader) ([]byte, error) {
	line, err := bufio.NewReaderSize(io.LimitReader(r, 1024), 1024).ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || line == "") {
		return nil, errors.New("no passphrase on input")
	}
	return []byte(strings.TrimRight(line, "\r\n")), nil
}

// PassphraseFromFile reads the first line of the file at path.
func PassphraseFromFile(path string) ([]byte, error) {
	if path == "-" {
		return PassphraseFromReader(os.Stdin)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return PassphraseFromReader(f)
}

// PassphraseFromTerminal prints prompt to the terminal and reads a line with
// echo off. When confirm is set it asks twice and requires both to match.
func PassphraseFromTerminal(prompt string, confirm bool) ([]byte, error) {
	first, err := readNoEcho(prompt)
	if err != nil {
		return nil, err
	}
	if !confirm {
		return first, nil
	}
	second, err := readNoEcho("Again: ")
	if err != nil {
		return nil, err
	}
	defer clear(second)
	if string(first) != string(second) { // a typo check, not a secret compare
		clear(first)
		return nil, errors.New("the two passphrases differ")
	}
	return first, nil
}
