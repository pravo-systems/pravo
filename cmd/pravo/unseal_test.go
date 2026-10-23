package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sealedEnv is a bucket with two records sealed to ada and dan, a recovery of 2 of 3.
func TestProtectUnseal(t *testing.T) {
	r := recoveredBucket(t)
	if strings.Contains(r.bucketText(), secretA) {
		t.Fatal("the secret is in the bucket after the seal")
	}
	t.Run("it asks first, and without a terminal it refuses", func(t *testing.T) {
		before := r.bucketText()
		text, code := runPravo(t, r.dir, "protect", "unseal")
		if code != 1 || !strings.Contains(text, "pass --yes") || r.bucketText() != before {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("it takes the seal off and says what the repository still expects", func(t *testing.T) {
		text, code := runPravo(t, r.dir, "protect", "unseal", "--yes")
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, text)
		}
		for _, want := range []string{
			"Afterwards anyone who can read the bucket can read every value.",
			"2 records unsealed in ",
			"outputs unsealed · the sealed part of meta.json is gone",
			"The state is not sealed any more (format 3).",
			"The repository still expects a sealed state, and writers refuse this one (exit code 5) until these are removed:",
			"  .pravo/access/readers.txt\n  .pravo/access/recovery.txt\n",
			"Destroy the recovery shares",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("the output lacks %q:\n%s", want, text)
			}
		}
		if !strings.Contains(r.bucketText(), secretA) {
			t.Error("the secret is not in the bucket in the open")
		}
		for k, data := range r.f.Objects {
			if (strings.Contains(k, "rec/") || strings.HasSuffix(k, "outputs.json")) && strings.Contains(string(data), `"sealed"`) {
				t.Errorf("%s is still sealed", k)
			}
		}
		if strings.Contains(string(r.f.Objects["prod/meta.json"]), `"sealed": {`) {
			t.Error("meta.json still has the sealed part")
		}
	})
	t.Run("a writer refuses the state until the files are gone", func(t *testing.T) {
		text, code := runPravo(t, r.dir, "state", "rm", "net.vpc")
		if code != 5 || !strings.Contains(text, "the state is not sealed") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		text, code = runPravo(t, r.dir, "protect", "status")
		if code != 0 || !strings.Contains(text, "off · format 3 · immutable records") {
			t.Errorf("status: exit %d\n%s", code, text)
		}
	})
	t.Run("--remove-files finishes it", func(t *testing.T) {
		text, code := runPravo(t, r.dir, "protect", "unseal", "--remove-files")
		if code != 0 || !strings.Contains(text, "The state is not sealed.") ||
			!strings.Contains(text, "Removed .pravo/access/readers.txt; commit the deletion.") ||
			!strings.Contains(text, "Removed .pravo/access/recovery.txt; commit the deletion.") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		for _, f := range []string{"readers.txt", "recovery.txt"} {
			if _, err := os.Stat(filepath.Join(r.dir, ".pravo", "access", f)); err == nil {
				t.Errorf("%s stayed", f)
			}
		}
		if text, code = runPravo(t, r.dir, "state", "list"); code != 0 || !strings.Contains(text, "net.vpc") {
			t.Errorf("list: exit %d\n%s", code, text)
		}
	})
}

func TestProtectUnsealJSONAndSealAgain(t *testing.T) {
	r := recoveredBucket(t)
	text, code := runPravo(t, r.dir, "--json", "protect", "unseal", "--yes", "--remove-files")
	if code != 0 || !strings.Contains(text, `"unsealed": true`) || !strings.Contains(text, `"records": 2`) ||
		!strings.Contains(text, ".pravo/access/readers.txt") {
		t.Fatalf("exit %d\n%s", code, text)
	}
	// The same state is sealed again, to the same people, in a group of its own.
	r.readers(t, r.adaLine, r.danLine)
	if text, code = runPravo(t, r.dir, "protect", "init", "--encrypt", "--yes"); code != 0 {
		t.Fatalf("seal again: exit %d\n%s", code, text)
	}
	if strings.Contains(r.bucketText(), secretA) {
		t.Error("the secret is in the bucket after the second seal")
	}
	text, code = runPravo(t, r.dir, "--json", "protect", "status")
	if code != 0 || !strings.Contains(text, `"state": "sealed"`) {
		t.Errorf("status: exit %d\n%s", code, text)
	}
	if !strings.Contains(r.bucketText(), `"current": "g2"`) {
		t.Errorf("the second seal did not number its group after the first:\n%s", r.f.Objects["prod/meta.json"])
	}
}
