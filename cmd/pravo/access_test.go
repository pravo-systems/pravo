package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/protect"
)

// readerLines makes n identities and returns their lines.
func readerLines(t *testing.T, names ...string) []string {
	t.Helper()
	var lines []string
	for _, n := range names {
		id, err := protect.NewIdentity(n, "")
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, id.Line())
	}
	return lines
}

func writeReaders(t *testing.T, accessDir string, lines ...string) string {
	t.Helper()
	if err := os.MkdirAll(accessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(accessDir, "readers.txt")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadReadersFileFindsTheFileInTheRoot(t *testing.T) {
	isolateIdentities(t)
	root := t.TempDir()
	lines := readerLines(t, "ada", "dan")
	p := writeReaders(t, filepath.Join(root, ".pravo", "access"), lines[0], "# the second reader", lines[1]+" # on call", "")
	rf := loadReadersFile(root, "")
	if !rf.Found || rf.Err != nil || rf.Path != p || rf.Dir != filepath.Dir(p) {
		t.Fatalf("load = %+v", rf)
	}
	if got := strings.Join(readerNames(rf.Readers), " "); got != "ada dan" {
		t.Errorf("readers = %q, want ada dan", got)
	}
}

func TestLoadReadersFileSearchesTheParentsUpToTheRepository(t *testing.T) {
	isolateIdentities(t)
	top := t.TempDir()
	if err := os.Mkdir(filepath.Join(top, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(top, "infra", "prod")
	if err := os.MkdirAll(spec, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Run("a file in the repository root", func(t *testing.T) {
		p := writeReaders(t, filepath.Join(top, ".pravo", "access"), readerLines(t, "ada")...)
		rf := loadReadersFile(spec, "")
		if !rf.Found || rf.Path != p || len(rf.Readers) != 1 {
			t.Fatalf("load = %+v", rf)
		}
	})
	t.Run("the nearest directory wins", func(t *testing.T) {
		p := writeReaders(t, filepath.Join(top, "infra", ".pravo", "access"), readerLines(t, "dan")...)
		if rf := loadReadersFile(spec, ""); rf.Path != p {
			t.Errorf("path = %s, want %s", rf.Path, p)
		}
	})
	t.Run("the search stops at the repository", func(t *testing.T) {
		outer := t.TempDir()
		writeReaders(t, filepath.Join(outer, ".pravo", "access"), readerLines(t, "eve")...)
		repo := filepath.Join(outer, "repo")
		if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		rf := loadReadersFile(repo, "")
		if rf.Found {
			t.Errorf("found %s above the repository", rf.Path)
		}
		if want := filepath.Join(repo, ".pravo", "access"); rf.Dir != want {
			t.Errorf("looked in %s, want %s first", rf.Dir, want)
		}
	})
	t.Run("outside a repository only the root is searched", func(t *testing.T) {
		outer := t.TempDir()
		writeReaders(t, filepath.Join(outer, ".pravo", "access"), readerLines(t, "eve")...)
		sub := filepath.Join(outer, "sub")
		if err := os.Mkdir(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if rf := loadReadersFile(sub, ""); rf.Found {
			t.Errorf("found %s although no .git is above", rf.Path)
		}
	})
}

func TestLoadReadersFileDirectoryOrder(t *testing.T) {
	isolateIdentities(t)
	root := t.TempDir()
	byDefault := writeReaders(t, filepath.Join(root, ".pravo", "access"), readerLines(t, "ada")...)
	byEnv := writeReaders(t, filepath.Join(root, "from-env"), readerLines(t, "bob")...)
	byFlag := writeReaders(t, filepath.Join(root, "from-flag"), readerLines(t, "cy")...)
	if rf := loadReadersFile(root, ""); rf.Path != byDefault {
		t.Errorf("default: %s", rf.Path)
	}
	t.Setenv("PRAVO_ACCESS_DIR", byEnv[:len(byEnv)-len("/readers.txt")])
	if rf := loadReadersFile(root, ""); rf.Path != byEnv {
		t.Errorf("environment: %s", rf.Path)
	}
	if rf := loadReadersFile(root, "from-flag"); rf.Path != byFlag {
		t.Errorf("flag, relative to the root: %s", rf.Path)
	}
	if rf := loadReadersFile(root, filepath.Join(root, "from-flag")); rf.Path != byFlag {
		t.Errorf("flag, absolute: %s", rf.Path)
	}
	if rf := loadReadersFile(root, filepath.Join(root, "nothing-here")); rf.Found || rf.Dir != filepath.Join(root, "nothing-here") {
		t.Errorf("a flag that names an empty directory: %+v", rf)
	}
}

func TestReadersFileErrorsCarryALineAndAReasonAndNeverTheLine(t *testing.T) {
	isolateIdentities(t)
	good := readerLines(t, "ada", "dan")
	fields := strings.Fields(good[0])
	// The first line is good and the second is the one that is wrong.
	cases := []struct {
		name string
		line string
		want string
	}{
		{"too few fields", "SECRETWORD ssh-ed25519", "want 4 fields"},
		{"too many fields", good[0] + " role:admin SECRETWORD", "want 4 fields"},
		{"a fifth field that is not a role", good[0] + " SECRETWORD", "fifth field"},
		{"a role that is not known", good[0] + " role:SECRETWORD", "role"},
		{"a name with a character that is not allowed", "SECRET!WORD " + strings.Join(fields[1:], " "), "name"},
		{"another key type", fields[0] + " ssh-rsa " + fields[2] + " " + fields[3], "ssh-ed25519"},
		{"a key type that is not supported", fields[0] + " ssh-ed25519 SECRETWORDSECRET " + fields[3], "signing key"},
		{"a reader key that is not a key", fields[0] + " " + fields[1] + " " + fields[2] + " x25519:SECRETWORD", "reader key"},
		{"a reader key with no prefix", fields[0] + " " + fields[1] + " " + fields[2] + " SECRETWORD", "reader key"},
		{"a name that is used twice", strings.Replace(good[1], "dan ", "ada ", 1), "already used on line 1"},
		{"a reader key that is used twice", "dan " + strings.Join(fields[1:], " "), "already listed on line 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			writeReaders(t, filepath.Join(root, ".pravo", "access"), good[0], c.line)
			rf := loadReadersFile(root, "")
			var fe *readersFileError
			if !rf.Found || !errors.As(rf.Err, &fe) {
				t.Fatalf("load = %+v, want a *readersFileError", rf)
			}
			if fe.Line != 2 {
				t.Errorf("line = %d, want 2 (%v)", fe.Line, rf.Err)
			}
			if !strings.Contains(rf.Err.Error(), c.want) {
				t.Errorf("error %q does not say %q", rf.Err, c.want)
			}
			for _, secret := range []string{"SECRET", "SECRETWORD", fields[3], fields[2]} {
				if strings.Contains(rf.Err.Error(), secret) {
					t.Errorf("the error repeats part of the line (%q): %v", secret, rf.Err)
				}
			}
			if rf.Readers != nil {
				t.Error("readers were returned with an error")
			}
		})
	}
}

func TestReadersFileSizeBound(t *testing.T) {
	isolateIdentities(t)
	root := t.TempDir()
	dir := filepath.Join(root, ".pravo", "access")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("# padding padding padding padding padding padding padding\n", 1500)
	if len(big) <= 64<<10 {
		t.Fatal("the test file is too small")
	}
	if err := os.WriteFile(filepath.Join(dir, "readers.txt"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	rf := loadReadersFile(root, "")
	if !rf.Found || rf.Err == nil || !strings.Contains(rf.Err.Error(), "64 KiB") {
		t.Fatalf("load = %+v, want a file larger than 64 KiB refused", rf)
	}
}

func TestReadersFileThroughALink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("links need privileges on Windows")
	}
	isolateIdentities(t)
	root := t.TempDir()
	dir := filepath.Join(root, ".pravo", "access")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Run("to a file outside the access directory", func(t *testing.T) {
		secret := filepath.Join(t.TempDir(), "secret.txt")
		if err := os.WriteFile(secret, []byte("TOPSECRETVALUE\nTOPSECRETVALUE2\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "readers.txt")
		os.Remove(link)
		if err := os.Symlink(secret, link); err != nil {
			t.Fatal(err)
		}
		rf := loadReadersFile(root, "")
		if !rf.Found || rf.Err == nil || !strings.Contains(rf.Err.Error(), "leaves the access directory") {
			t.Fatalf("load = %+v, want the link refused", rf)
		}
		if strings.Contains(rf.Err.Error(), "TOPSECRET") {
			t.Errorf("the error shows the target: %v", rf.Err)
		}
	})
	t.Run("to a file in the access directory", func(t *testing.T) {
		real := filepath.Join(dir, "real.txt")
		if err := os.WriteFile(real, []byte(strings.Join(readerLines(t, "ada"), "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "readers.txt")
		os.Remove(link)
		if err := os.Symlink("real.txt", link); err != nil {
			t.Fatal(err)
		}
		rf := loadReadersFile(root, "")
		if rf.Err != nil || len(rf.Readers) != 1 {
			t.Fatalf("load = %+v, want the link followed", rf)
		}
	})
	t.Run("to a directory", func(t *testing.T) {
		link := filepath.Join(dir, "readers.txt")
		os.Remove(link)
		if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("sub", link); err != nil {
			t.Fatal(err)
		}
		rf := loadReadersFile(root, "")
		if rf.Err == nil || !strings.Contains(rf.Err.Error(), "not a regular file") {
			t.Fatalf("load = %+v, want a refusal", rf)
		}
	})
}
