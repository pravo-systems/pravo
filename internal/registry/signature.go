package registry

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
)

// maxSums and maxSig bound the checksum list and its signature.
var (
	maxSums int64 = 1 << 20
	maxSig  int64 = 1 << 16
)

// signingKeys is the "signing_keys" object of a provider download answer.
type signingKeys struct {
	GPGPublicKeys []struct {
		KeyID      string `json:"key_id"`
		ASCIIArmor string `json:"ascii_armor"`
	} `json:"gpg_public_keys"`
}

func (dl *downloadInfo) signed() bool {
	return dl.ShasumsURL != "" && dl.ShasumsSignatureURL != "" && len(dl.SigningKeys.GPGPublicKeys) > 0
}

// fetchLimited reads a small document from the registry.
func (c *Client) fetchLimited(ctx context.Context, u string, limit int64) ([]byte, error) {
	if !secureURL(u) {
		return nil, fmt.Errorf("the registry gave the address %s; it must be https", clean(u))
	}
	resp, err := c.get(ctx, u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", clean(u), clean(resp.Status))
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", clean(u), limit)
	}
	return b, nil
}

// verifySignature checks the detached signature of the checksum list against the
// keys the registry published, and that the list names the archive with the
// checksum that was computed. It returns the fingerprint of the signing key.
func (c *Client) verifySignature(ctx context.Context, dl *downloadInfo, sum string) (string, error) {
	var ring openpgp.EntityList
	for _, k := range dl.SigningKeys.GPGPublicKeys {
		l, err := openpgp.ReadArmoredKeyRing(strings.NewReader(k.ASCIIArmor))
		if err == nil {
			ring = append(ring, l...)
		}
	}
	if len(ring) == 0 {
		return "", errors.New("the registry published signing keys that cannot be read")
	}
	sums, err := c.fetchLimited(ctx, dl.ShasumsURL, maxSums)
	if err != nil {
		return "", fmt.Errorf("fetching the checksum list: %w", err)
	}
	sig, err := c.fetchLimited(ctx, dl.ShasumsSignatureURL, maxSig)
	if err != nil {
		return "", fmt.Errorf("fetching the signature: %w", err)
	}
	var signer *openpgp.Entity
	if bytes.HasPrefix(bytes.TrimSpace(sig), []byte("-----BEGIN")) {
		signer, err = openpgp.CheckArmoredDetachedSignature(ring, bytes.NewReader(sums), bytes.NewReader(sig), nil)
	} else {
		signer, err = openpgp.CheckDetachedSignature(ring, bytes.NewReader(sums), bytes.NewReader(sig), nil)
	}
	if err != nil {
		return "", fmt.Errorf("the signature of the checksum list does not verify with any of the registry's keys: %w", err)
	}
	found := false
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == dl.Filename {
			found = true
			if !strings.EqualFold(f[0], sum) {
				return "", fmt.Errorf("the signed checksum list gives %s for %s, but the archive is %s", clean(f[0]), clean(dl.Filename), sum)
			}
		}
	}
	if !found {
		return "", fmt.Errorf("the signed checksum list does not name %s", clean(dl.Filename))
	}
	return strings.ToUpper(hex.EncodeToString(signer.PrimaryKey.Fingerprint)), nil
}
