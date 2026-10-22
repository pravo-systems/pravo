package protect

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// The SSH public key line of an Ed25519 key is "ssh-ed25519 BASE64 [comment]".
// The BASE64 is the wire blob: string "ssh-ed25519", string key (RFC 8709), where
// string is a uint32 length and the bytes. That is small enough to parse here,
// so the standard library is all it takes. Keys of other types are refused with
// a clear message; "sk-ssh-ed25519@openssh.com" (FIDO) has a different blob.

const sshEd25519 = "ssh-ed25519"

// SSHBlob returns the base64 blob of an Ed25519 public key, as it appears on a
// readers line.
func SSHBlob(pub ed25519.PublicKey) string {
	b := make([]byte, 0, 51)
	b = binary.BigEndian.AppendUint32(b, uint32(len(sshEd25519)))
	b = append(b, sshEd25519...)
	b = binary.BigEndian.AppendUint32(b, uint32(len(pub)))
	b = append(b, pub...)
	return base64.StdEncoding.EncodeToString(b)
}

// ParseSSHBlob decodes the base64 blob and returns the Ed25519 public key.
func ParseSSHBlob(s string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.Strict().DecodeString(s)
	if err != nil {
		return nil, errors.New("not valid base64")
	}
	t, rest, ok := sshString(raw)
	if !ok {
		return nil, errors.New("truncated key")
	}
	if string(t) != sshEd25519 {
		return nil, fmt.Errorf("key type %q is not supported: use an ssh-ed25519 key", t)
	}
	k, rest, ok := sshString(rest)
	if !ok || len(k) != ed25519.PublicKeySize || len(rest) != 0 {
		return nil, errors.New("not an ssh-ed25519 public key")
	}
	return ed25519.PublicKey(append([]byte(nil), k...)), nil
}

// ParseSSHPublicLine parses a line of an OpenSSH .pub file or authorized_keys
// ("ssh-ed25519 AAAA… comment"). The comment is ignored.
func ParseSSHPublicLine(line string) (ed25519.PublicKey, error) {
	f := strings.Fields(line)
	if len(f) < 2 {
		return nil, errors.New("want \"ssh-ed25519 BASE64\"")
	}
	if f[0] != sshEd25519 {
		return nil, fmt.Errorf("key type %q is not supported: use an ssh-ed25519 key", f[0])
	}
	return ParseSSHBlob(f[1])
}

// SSHFingerprint is what ssh-keygen -l prints: "SHA256:" and the unpadded
// base64 of the SHA-256 of the blob.
func SSHFingerprint(pub ed25519.PublicKey) string {
	raw, _ := base64.StdEncoding.DecodeString(SSHBlob(pub))
	sum := sha256.Sum256(raw)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

func sshString(b []byte) (s, rest []byte, ok bool) {
	if len(b) < 4 {
		return nil, nil, false
	}
	n := binary.BigEndian.Uint32(b)
	if uint64(n) > uint64(len(b)-4) {
		return nil, nil, false
	}
	return b[4 : 4+n], b[4+n:], true
}

func b64std(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
