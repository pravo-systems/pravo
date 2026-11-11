package main

import (
	"bufio"
	"os"
	"strings"
	"sync"
)

// stdinLines is the one reader of what a person types in answer to a question. A reader
// made per question buffers more than one line of a piped input and loses the rest, so a
// second question in the same run read the end of the input and was cancelled.
var stdinLines struct {
	mu   sync.Mutex
	file *os.File
	r    *bufio.Reader
}

// readAnswer reads one line of the answer to a question from standard input, with its
// newline cut off, and reports whether anything could be read at all.
func readAnswer() (line string, ok bool) {
	stdinLines.mu.Lock()
	defer stdinLines.mu.Unlock()
	if stdinLines.r == nil || stdinLines.file != os.Stdin {
		stdinLines.file, stdinLines.r = os.Stdin, bufio.NewReader(os.Stdin)
	}
	line, err := stdinLines.r.ReadString('\n')
	return strings.TrimSpace(line), err == nil || strings.TrimSpace(line) != ""
}
