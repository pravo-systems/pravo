package serve

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
)

// The secrets fixtures of the interface (web/dev/fixtures/secrets.*.json) are what the server sends for a
// sealed state that was made for real, so the page is built against the real sentences and commands. Only
// what changes from one run to the next is fixed: the revision, the times, and the ids of transactions.
// PRAVO_UPDATE_FIXTURES=1 writes them again; otherwise the test compares.

// platformSecrets are the nodes that the platform fixture of the interface (web/dev/gen-fixtures.js) has for
// the secrets: the addresses must be the same there and here.
const platformSecrets = specCUE + `
api: {
	db_secret:         {$kind: "secret", name: "prod/db_password", value: string}
	db_secret_version: {$kind: "secret_version", name: "prod/db_password", version: int}
	api_token:         {$kind: "secret", name: "prod/api_token", value: string}
}
edge: origin_secret: {$kind: "secret", name: "prod/origin_secret", value: string}
deploy_key: {$kind: "secret", name: "ci/deploy_key", value: string}
`

var txnIDRE = regexp.MustCompile(`\d{8}T\d{6}Z-[0-9a-f]{6}`)

// fixtureTimes are when the versions of the fixtures were written: before the time at which the other
// fixtures of the platform were read.
var fixtureTimes = map[string]time.Time{
	"ci/deploy_key":      time.Date(2026, 10, 14, 8, 20, 0, 0, time.UTC),
	"prod/api_token":     time.Date(2026, 10, 19, 10, 5, 0, 0, time.UTC),
	"prod/db_password":   time.Date(2026, 10, 20, 16, 41, 0, 0, time.UTC),
	"prod/signing_key":   time.Date(2026, 10, 9, 9, 30, 0, 0, time.UTC),
	"prod/origin_secret": time.Date(2026, 10, 18, 13, 12, 0, 0, time.UTC),
}

func fixSecrets(d Secrets) Secrets {
	d.Revision = "r-7c41e9b2"
	for i := range d.Secrets {
		if at, ok := fixtureTimes[d.Secrets[i].Name]; ok {
			d.Secrets[i].At = at
		}
	}
	for i := range d.Issues {
		for j := range d.Issues[i].Items {
			d.Issues[i].Items[j].Why = txnIDRE.ReplaceAllString(d.Issues[i].Items[j].Why, "20261020T164100Z-9c41ae")
		}
	}
	return d
}

func TestSecretsFixtures(t *testing.T) {
	// A team's vault with the readers file: every secret is set, and every version was written by someone who may.
	allClear := func(t *testing.T) Secrets {
		v := newVault(t, platformSecrets)
		v.set("dan", "prod/db_password", vaultDB)
		v.set("dan", "prod/db_password", vaultDB+"-2")
		v.set("dan", "prod/api_token", vaultToken)
		v.set("dan", "prod/origin_secret", vaultToken+"-origin")
		v.set("ada", "ci/deploy_key", vaultKey)
		v.set("ada", "prod/signing_key", vaultKey+"-sign")
		return v.serve(Options{}).secrets()
	}
	// The same, with a reader's version of the password, a secret that was removed and one that was never set.
	problems := func(t *testing.T) Secrets {
		v := newVault(t, platformSecrets)
		v.set("dan", "prod/db_password", vaultDB)
		v.set("eve", "prod/db_password", vaultEvil)
		v.set("dan", "prod/api_token", vaultToken)
		v.set("ada", "ci/deploy_key", vaultKey)
		v.remove("ada", "ci/deploy_key")
		v.set("ada", "prod/signing_key", vaultKey+"-sign")
		return v.serve(Options{}).secrets()
	}
	// A sealed state with no secret and a specification that reads none.
	empty := func(t *testing.T) Secrets { return newVault(t, specCUE).serve(Options{}).secrets() }
	// No readers file: the versions are listed, and nobody is judged.
	unchecked := func(t *testing.T) Secrets {
		v := newVault(t, platformSecrets)
		v.set("dan", "prod/db_password", vaultDB)
		v.set("dan", "prod/api_token", vaultToken)
		v.set("dan", "prod/origin_secret", vaultToken+"-origin")
		v.set("ada", "ci/deploy_key", vaultKey)
		return v.serve(Options{SecretRules: func(*state.Store) (state.RecordRules, bool, string) { return state.RecordRules{}, false, "" }}).secrets()
	}
	// A state that is not sealed, whose specification reads secrets, and one whose specification does not.
	plain := func(cue string) func(t *testing.T) Secrets {
		return func(t *testing.T) Secrets {
			dir := t.TempDir()
			writeSpec(t, dir, cue)
			st, err := state.Open(filepath.Join(dir, "pravo.state"))
			if err != nil {
				t.Fatal(err)
			}
			seed(t, st)
			return start(t, dir, st, Options{}).secrets()
		}
	}
	cases := map[string]func(t *testing.T) Secrets{
		"clear":     allClear,
		"platform":  problems,
		"empty":     empty,
		"unchecked": unchecked,
		"unsealed":  plain(platformSecrets),
		"off":       plain(specCUE),
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			doc, err := json.MarshalIndent(fixSecrets(build(t)), "", " ")
			if err != nil {
				t.Fatal(err)
			}
			doc = append(doc, '\n')
			path := filepath.Join("web", "dev", "fixtures", "secrets."+name+".json")
			if os.Getenv("PRAVO_UPDATE_FIXTURES") != "" {
				if err := os.WriteFile(path, doc, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			have, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%s: %v (PRAVO_UPDATE_FIXTURES=1 writes it)", path, err)
			}
			// A checkout on Windows may turn the line ends of the file into CR LF.
			if strings.ReplaceAll(string(have), "\r\n", "\n") != string(doc) {
				t.Errorf("%s is not what the server sends; PRAVO_UPDATE_FIXTURES=1 go test ./internal/serve -run SecretsFixtures writes it again", path)
			}
		})
	}
}
