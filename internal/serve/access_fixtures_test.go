package serve

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The access fixtures of the interface (web/dev/fixtures/access.*.json) are what
// the server sends, so the page is built against the real sentences and commands.
// PRAVO_UPDATE_FIXTURES=1 writes them again; otherwise the test compares.

func fixKey(name string) string {
	sum := sha256.Sum256([]byte("reader/" + name))
	return "x25519:" + base64.RawURLEncoding.EncodeToString(sum[:])
}

func fixBlob(name string) string {
	sum := sha256.Sum256([]byte("signing/" + name))
	raw := append([]byte{0, 0, 0, 11}, []byte("ssh-ed25519")...)
	raw = append(raw, 0, 0, 0, 32)
	raw = append(raw, sum[:]...)
	return base64.StdEncoding.EncodeToString(raw)
}

func fixFile(line int, name string) ReaderFacts {
	return ReaderFacts{Name: name, Key: fixKey(name), SSH: fixBlob(name), Line: line}
}

func fixStore(name string) ReaderFacts { return ReaderFacts{Name: name, Key: fixKey(name)} }

func fixSealed(store, file []string, you *YouFacts) Facts {
	f := Facts{Store: StoreFacts{State: "sealed", Format: 3, Records: 46, Sealed: 46,
		Groups: []GroupFacts{{ID: "g1", Current: true, Locks: len(store), Records: 46}},
		History: []ChangeFacts{
			{At: time.Date(2026, 10, 20, 9, 12, 0, 0, time.UTC), By: "ada", What: "sealed", Added: []string{"ada", "dan"}},
			{At: time.Date(2026, 10, 22, 10, 41, 0, 0, time.UTC), By: "ada", What: "rewrap", Added: []string{"eve"}},
		}}, You: you, File: FileFacts{State: "ok"}}
	for _, n := range store {
		f.Store.Readers = append(f.Store.Readers, fixStore(n))
	}
	for i, n := range file {
		f.File.Readers = append(f.File.Readers, fixFile(i+1, n))
	}
	return f
}

func TestAccessFixtures(t *testing.T) {
	ada := &YouFacts{State: "reader", Name: "ada", Key: fixKey("ada")}
	in := func(f Facts, format int, backend string) accessInput {
		return accessInput{rev: "r-7c41e9b2", location: "s3://acme-state/estate", backend: backend, format: format, records: 46, facts: f,
			path: "/srv/infra/estate/.pravo/access/readers.txt", dir: "/srv/infra/estate"}
	}
	// A state sealed to its maker alone: the page warns under the readers, and the card is "Add a backup reader".
	solo := fixSealed([]string{"ada"}, []string{"ada"}, ada)
	solo.Store.History = []ChangeFacts{{At: time.Date(2026, 10, 23, 8, 3, 0, 0, time.UTC), By: "ada", What: "sealed", Added: []string{"ada"}}}
	drift := fixSealed([]string{"ada", "eve", "fay", "x; rm -rf ~"}, []string{"ada", "dan", "eve"}, ada)
	drift.Store.Groups = []GroupFacts{{ID: "g1", Locks: 3, Records: 12}, {ID: "g2", Current: true, Locks: 4, Records: 34}}
	locked := fixSealed([]string{"ada", "dan", "eve"}, []string{"ada", "dan", "eve"}, &YouFacts{State: "no-identity"})
	locked.Store.Locked, locked.Store.Unreadable = true, 46
	migrating := fixSealed([]string{"ada", "dan"}, []string{"ada", "dan"}, ada)
	migrating.Store.State, migrating.Store.Sealed, migrating.Store.Plain = "migrating", 25, 21
	migrating.Store.Groups[0].Records = 25
	migrating.Store.History = nil
	migrating.Store.Migration = &MigrationFacts{Started: time.Date(2026, 10, 22, 9, 30, 0, 0, time.UTC), By: "ada", Done: 25, Total: 46}
	unsealing := fixSealed([]string{"ada", "dan"}, []string{"ada", "dan"}, ada)
	unsealing.Store.State, unsealing.Store.Sealed, unsealing.Store.Plain = "migrating", 21, 25
	unsealing.Store.Groups[0].Records = 21
	unsealing.Store.History = nil
	unsealing.Store.Migration = &MigrationFacts{Started: time.Date(2026, 10, 22, 9, 30, 0, 0, time.UTC), By: "ada", Done: 25, Total: 46, Unseal: true}
	invalid := fixSealed([]string{"ada", "dan", "eve"}, nil, ada)
	invalid.File = FileFacts{State: "invalid", Errors: []LineError{{Line: 3, Reason: "want 4 fields: NAME ssh-ed25519 BASE64 x25519:BASE64URL (got 3)"}, {Line: 5, Reason: "reader key …: want x25519:BASE64URL"}}}
	missing := fixSealed([]string{"ada", "dan", "eve"}, nil, ada)
	missing.File = FileFacts{State: "missing"}
	notReader := fixSealed([]string{"ada", "dan", "eve"}, []string{"ada", "dan", "eve"}, &YouFacts{State: "not-reader", Name: "gus", Key: fixKey("gus")})
	notReader.Store.Locked = true
	off := Facts{Store: StoreFacts{State: "off", Format: 1, Records: 46}, File: FileFacts{State: "missing"}}
	// The repository lists readers and the state is not sealed: it was sealed and meta.json was changed.
	downgraded := Facts{Store: StoreFacts{State: "off", Format: 1, Records: 46}, File: FileFacts{State: "ok", Readers: []ReaderFacts{fixFile(1, "ada"), fixFile(2, "dan"), fixFile(3, "eve")}}}
	immutable := Facts{Store: StoreFacts{State: "immutable", Format: 2, Records: 46}, File: FileFacts{State: "missing"}}
	// A state with a recovery of 2 of 3 shares, and one whose recovery does not hold.
	withRecovery := fixSealed([]string{"ada"}, []string{"ada"}, ada)
	withRecovery.Store.Recovery = &RecoveryFacts{K: 2, N: 3, Key: fixKey("recovery"), Locked: true, File: "match"}
	brokenRecovery := fixSealed([]string{"ada", "dan"}, []string{"ada", "dan"}, ada)
	brokenRecovery.Store.Recovery = &RecoveryFacts{K: 3, N: 5, Key: fixKey("recovery"), Locked: false, Groups: []string{"g1"}, File: "missing"}
	cases := map[string]accessInput{
		"recovery":    in(withRecovery, 3, "s3"),
		"recoverybad": in(brokenRecovery, 3, "s3"),
		"platform":    in(fixSealed([]string{"ada", "dan", "eve"}, []string{"ada", "dan", "eve"}, ada), 3, "s3"),
		"solo":        in(solo, 3, "s3"),
		"off":         in(off, 1, "s3"),
		"downgraded":  in(downgraded, 1, "s3"),
		"immutable":   in(immutable, 2, "s3"),
		"drift":       in(drift, 3, "s3"),
		"locked":      in(locked, 3, "s3"),
		"migrating":   in(migrating, 3, "s3"),
		"unsealing":   in(unsealing, 3, "s3"),
		"invalid":     in(invalid, 3, "s3"),
		"missing":     in(missing, 3, "s3"),
		"notreader":   in(notReader, 3, "s3"),
	}
	for name, input := range cases {
		doc, err := json.MarshalIndent(buildAccess(input), "", " ")
		if err != nil {
			t.Fatal(err)
		}
		doc = append(doc, '\n')
		path := filepath.Join("web", "dev", "fixtures", "access."+name+".json")
		if os.Getenv("PRAVO_UPDATE_FIXTURES") != "" {
			if err := os.WriteFile(path, doc, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		have, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (PRAVO_UPDATE_FIXTURES=1 writes it)", path, err)
		}
		// A checkout on Windows may turn the line ends of the file into CR LF.
		if strings.ReplaceAll(string(have), "\r\n", "\n") != string(doc) {
			t.Errorf("%s is not what the server sends; PRAVO_UPDATE_FIXTURES=1 go test ./internal/serve -run AccessFixtures writes it again", path)
		}
	}
}
