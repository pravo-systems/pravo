package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// The readers file is .pravo/access/readers.txt in the repository: one line per
// person who may read a sealed state. Writers take the list from it, not from
// the bucket.

// readersFileName is the name of the file in the access directory.
const readersFileName = "readers.txt"

// maxReadersFileSize is the most that is read of a readers file.
const maxReadersFileSize = 64 << 10

// flagAccessDir is the --access-dir flag of the commands that read the readers
// file.
var flagAccessDir string

func addAccessDirFlag(cmd *cobra.Command) {
	cmd.Flags().StringVar(&flagAccessDir, "access-dir", "", "directory of the readers file (default .pravo/access); $PRAVO_ACCESS_DIR")
}

// readersFile is what was found of the readers file.
type readersFile struct {
	// Dir is the access directory the file was found in, or the first one that was
	// looked in when it was not found.
	Dir string
	// Path is Dir/readers.txt.
	Path string
	// Found is set when the file exists, whether or not it could be read.
	Found bool
	// Readers is the parsed list, in file order. Nil unless Err is nil and Found
	// is set.
	Readers []protect.Reader
	// Pin is the digest of the first roles of the state that the file pins, empty without
	// the roles-genesis line.
	Pin string
	// Err is why a file that exists could not be used. It is a *readersFileError
	// and never holds the text of a line.
	Err error
}

// readersFileError says why the readers file cannot be used: the line (0 when the
// problem is not on a line) and a reason that does not quote the file.
type readersFileError struct {
	Line   int
	Reason string
}

func (e *readersFileError) Error() string {
	if e.Line > 0 {
		return "readers file, line " + strconv.Itoa(e.Line) + ": " + e.Reason
	}
	return "readers file: " + e.Reason
}

// problem is the error without its leading words: "line 3: want 4 fields".
func (rf readersFile) problem() string {
	var fe *readersFileError
	if !errors.As(rf.Err, &fe) {
		if rf.Err == nil {
			return ""
		}
		return rf.Err.Error()
	}
	if fe.Line > 0 {
		return "line " + strconv.Itoa(fe.Line) + ": " + fe.Reason
	}
	return fe.Reason
}

// loadReadersFile finds and parses the readers file of the repository that
// holds the specification in rootDir. The access directory is, in this order:
// flagDir, $PRAVO_ACCESS_DIR, rootDir/.pravo/access and then the same below each
// parent of rootDir up to the directory that holds .git. A relative directory
// is relative to rootDir. The file is read up to 64 KiB, and not through a
// symbolic link that leaves the access directory. An error never holds a line of
// the file.
func loadReadersFile(rootDir, flagDir string) readersFile {
	dirs := accessDirs(rootDir, flagDir)
	rf := readersFile{Dir: dirs[0], Path: filepath.Join(dirs[0], readersFileName)}
	for _, d := range dirs {
		p := filepath.Join(d, readersFileName)
		if _, err := os.Lstat(p); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				rf.Dir, rf.Path, rf.Found = d, p, true
				rf.Err = &readersFileError{Reason: "cannot be read: " + errorReason(err)}
				return rf
			}
			continue
		}
		rf.Dir, rf.Path, rf.Found = d, p, true
		data, err := readInside(d, p)
		if err != nil {
			rf.Err = err
			return rf
		}
		readers, pin, err := protect.ParseReadersPin(strings.NewReader(string(data)))
		if err != nil {
			rf.Err = sanitiseReadersError(err)
			return rf
		}
		rf.Readers, rf.Pin = readers, pin
		return rf
	}
	return rf
}

// accessDirs lists the directories the readers file is looked for in.
func accessDirs(rootDir, flagDir string) []string {
	explicit := flagDir
	if explicit == "" {
		explicit = os.Getenv("PRAVO_ACCESS_DIR")
	}
	if explicit != "" {
		if !filepath.IsAbs(explicit) {
			explicit = filepath.Join(rootDir, explicit)
		}
		return []string{filepath.Clean(explicit)}
	}
	dirs := []string{filepath.Join(rootDir, ".pravo", "access")}
	top := gitTop(rootDir)
	if top == "" {
		return dirs
	}
	for d := rootDir; d != top; {
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
		dirs = append(dirs, filepath.Join(d, ".pravo", "access"))
	}
	return dirs
}

// gitTop is the nearest directory at or above dir that holds .git, or "".
func gitTop(dir string) string {
	for d := dir; ; {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// readInside reads the regular file at path, which is in dir, up to the size
// bound. A symbolic link is followed only when what it points to is also in dir.
func readInside(dir, path string) ([]byte, error) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, &readersFileError{Reason: "cannot be read: " + errorReason(err)}
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, &readersFileError{Reason: "cannot be read: " + errorReason(err)}
	}
	if rel, err := filepath.Rel(realDir, real); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, &readersFileError{Reason: "is a symbolic link that leaves the access directory"}
	}
	// A named pipe or a device blocks in open, so look before opening.
	if info, err := os.Stat(real); err == nil && !info.Mode().IsRegular() {
		return nil, &readersFileError{Reason: "is not a regular file"}
	}
	f, err := os.Open(real)
	if err != nil {
		return nil, &readersFileError{Reason: "cannot be read: " + errorReason(err)}
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, &readersFileError{Reason: "cannot be read: " + errorReason(err)}
	}
	if !st.Mode().IsRegular() {
		return nil, &readersFileError{Reason: "is not a regular file"}
	}
	data, err := io.ReadAll(io.LimitReader(f, maxReadersFileSize+1))
	if err != nil {
		return nil, &readersFileError{Reason: "cannot be read: " + errorReason(err)}
	}
	if len(data) > maxReadersFileSize {
		return nil, &readersFileError{Reason: "is larger than 64 KiB"}
	}
	return data, nil
}

// errorReason is the reason of a file system error, without the path.
func errorReason(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	return err.Error()
}

var (
	readersLineErr = regexp.MustCompile(`^readers file, line (\d+): (.*)$`)
	quotedText     = regexp.MustCompile(`\s?"(?:[^"\\]|\\.)*"`)
)

// sanitiseReadersError turns an error of protect.ParseReaders into one with a
// line number and a reason. Reasons that quote part of the line (a name, a key)
// are cut, so that a readers file that is a link to something else leaks
// nothing through the message.
func sanitiseReadersError(err error) error {
	msg := err.Error()
	e := &readersFileError{}
	if m := readersLineErr.FindStringSubmatch(msg); m != nil {
		e.Line, _ = strconv.Atoi(m[1])
		msg = m[2]
	} else {
		msg = strings.TrimPrefix(msg, "readers file: ")
	}
	msg = quotedText.ReplaceAllString(msg, "")
	msg = strings.Replace(msg, "reader key of is already listed", "reader key is already listed", 1)
	e.Reason = msg
	return e
}

// withRoles is the readers file with the roles that decide for the state: the roles stored
// in it when the file pins them (see state.Store.RolesFor), the roles of the file otherwise.
// An error is a chain that does not verify, or is not the one the file pins.
func (rf readersFile) withRoles(st *state.Store) (readersFile, state.RolesStatus, error) {
	if !rf.Found || rf.Err != nil || st == nil {
		return rf, state.RolesStatus{Source: "off"}, nil
	}
	readers, status, err := st.RolesFor(st.Context(), rf.Readers, rf.Pin)
	if err != nil {
		return rf, status, err
	}
	rf.Readers = readers
	return rf, status, nil
}
