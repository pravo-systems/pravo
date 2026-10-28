package pravo

import (
	"testing"

	"github.com/pravo-systems/pravo/internal/protect"
)

// ParseIdentity reads both forms of an identity file: the one a job holds in
// the clear, which needs no passphrase, and the one a passphrase seals.
func TestParseIdentityReadsBothFormsOfAnIdentityFile(t *testing.T) {
	id, err := protect.NewIdentity("deploy-bot", "")
	if err != nil {
		t.Fatal(err)
	}
	defer id.Close()

	plain, err := id.MarshalUnsealed()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseIdentity(plain, nil)
	if err != nil {
		t.Fatalf("the unsealed form: %v", err)
	}
	if got.Name != "deploy-bot" {
		t.Errorf("the unsealed form gave the identity %q, want deploy-bot", got.Name)
	}
	got.Close()

	sealed, err := id.MarshalSealed([]byte("an example passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseIdentity(sealed, nil); err == nil {
		t.Error("the sealed form parsed without a passphrase")
	}
	asked := 0
	got, err = ParseIdentity(sealed, func() ([]byte, error) {
		asked++
		return []byte("an example passphrase"), nil
	})
	if err != nil {
		t.Fatalf("the sealed form: %v", err)
	}
	defer got.Close()
	if got.Name != "deploy-bot" || asked != 1 {
		t.Errorf("the sealed form gave the identity %q after %d passphrase calls, want deploy-bot after 1", got.Name, asked)
	}
}
