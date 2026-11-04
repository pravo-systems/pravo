package registry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"

	"github.com/pravo-systems/pravo/internal/env"
)

func armoredPublic(t *testing.T, e *openpgp.Entity) string {
	t.Helper()
	var b bytes.Buffer
	w, err := armor.Encode(&b, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Serialize(w); err != nil {
		t.Fatal(err)
	}
	w.Close()
	return b.String()
}

type sigCase struct {
	keyOf    *openpgp.Entity // key the registry publishes
	signer   *openpgp.Entity // key that signs; nil: no signature published
	sumsHash string          // hash the signed list gives for the archive; "" = the real one
	sumsName string
}

// signedServer serves one archive and a checksum list shaped by c.
func signedServer(t *testing.T, c sigCase, environ ...string) (*Client, Source, string) {
	t.Helper()
	archive := []byte("not really a zip")
	h := sha256.Sum256(archive)
	real := hex.EncodeToString(h[:])
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/terraform.json", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"providers.v1": "/v1/providers/"}`) })
	sums := func() []byte {
		hh, name := c.sumsHash, c.sumsName
		if hh == "" {
			hh = real
		}
		if name == "" {
			name = "f.zip"
		}
		return []byte(hh + "  " + name + "\n")
	}
	mux.HandleFunc("/v1/providers/", func(w http.ResponseWriter, r *http.Request) {
		dl := map[string]any{"protocols": []string{"6.0"}, "os": CurrentPlatform().OS, "arch": CurrentPlatform().Arch, "filename": "f.zip", "download_url": srv.URL + "/f.zip", "shasum": real}
		if c.signer != nil {
			dl["shasums_url"] = srv.URL + "/sums"
			dl["shasums_signature_url"] = srv.URL + "/sums.sig"
			dl["signing_keys"] = map[string]any{"gpg_public_keys": []map[string]string{{"key_id": "K", "ascii_armor": armoredPublic(t, c.keyOf)}}}
		}
		json.NewEncoder(w).Encode(dl)
	})
	mux.HandleFunc("/f.zip", func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) { w.Write(sums()) })
	mux.HandleFunc("/sums.sig", func(w http.ResponseWriter, r *http.Request) {
		var b bytes.Buffer
		// the list that was signed is the list for the real archive
		signed := []byte(real + "  f.zip\n")
		if c.sumsHash != "" || c.sumsName != "" {
			signed = sums()
		}
		if err := openpgp.DetachSign(&b, c.signer, bytes.NewReader(signed), nil); err != nil {
			t.Error(err)
		}
		w.Write(b.Bytes())
	})
	srv = httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	cl := &Client{HTTP: srv.Client(), Home: t.TempDir(), Env: env.New(environ)}
	return cl, Source{Host: strings.TrimPrefix(srv.URL, "https://"), Namespace: "a", Type: "b"}, real
}

func newKey(t *testing.T) *openpgp.Entity {
	t.Helper()
	e, err := openpgp.NewEntity("registry", "", "r@example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestSignedChecksumListVerifies(t *testing.T) {
	k := newKey(t)
	c, src, _ := signedServer(t, sigCase{keyOf: k, signer: k})
	_, _, signer, err := c.archive(context.Background(), src, "1.0.0", CurrentPlatform(), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.ToUpper(hex.EncodeToString(k.PrimaryKey.Fingerprint)); signer != want {
		t.Errorf("signer = %s, want %s", signer, want)
	}
}

func TestSignatureFromAnotherKeyIsRefused(t *testing.T) {
	c, src, _ := signedServer(t, sigCase{keyOf: newKey(t), signer: newKey(t)})
	dir := t.TempDir()
	_, _, _, err := c.archive(context.Background(), src, "1.0.0", CurrentPlatform(), dir, nil)
	if err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("err = %v", err)
	}
	if m, _ := filepathGlob(dir); len(m) != 0 {
		t.Errorf("left behind: %v", m)
	}
}

func TestSignedListThatNamesAnotherHashIsRefused(t *testing.T) {
	k := newKey(t)
	c, src, _ := signedServer(t, sigCase{keyOf: k, signer: k, sumsHash: strings.Repeat("1", 64)})
	_, _, _, err := c.archive(context.Background(), src, "1.0.0", CurrentPlatform(), t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "signed checksum list gives") {
		t.Errorf("err = %v", err)
	}
}

func TestSignedListThatDoesNotNameTheFileIsRefused(t *testing.T) {
	k := newKey(t)
	c, src, _ := signedServer(t, sigCase{keyOf: k, signer: k, sumsName: "other.zip"})
	_, _, _, err := c.archive(context.Background(), src, "1.0.0", CurrentPlatform(), t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "does not name") {
		t.Errorf("err = %v", err)
	}
}

func TestUnsignedIsAllowedUnlessRequired(t *testing.T) {
	c, src, _ := signedServer(t, sigCase{})
	_, _, signer, err := c.archive(context.Background(), src, "1.0.0", CurrentPlatform(), t.TempDir(), nil)
	if err != nil || signer != "" {
		t.Fatalf("signer %q err %v", signer, err)
	}
	c, src, _ = signedServer(t, sigCase{}, "PRAVO_REQUIRE_SIGNED=1")
	_, _, _, err = c.archive(context.Background(), src, "1.0.0", CurrentPlatform(), t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "PRAVO_REQUIRE_SIGNED") {
		t.Errorf("err = %v", err)
	}
}

func filepathGlob(dir string) ([]string, error) { return filepath.Glob(filepath.Join(dir, "*")) }
