package protect

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"io"
)

// A minimal ssh-agent client (draft-miller-ssh-agent): list the Ed25519 keys and
// sign with one. About 80 lines of standard library, so signing through ssh-agent
// needs no golang.org/x/crypto. It is NOT used in step 2 (nothing signs yet);
// `pravo id new --agent` may use AgentList to pick the public key.
//
// The agent never reveals a private key and cannot decrypt, which is why an
// identity also has a separate reader key. Ed25519 signatures are deterministic,
// so an agent could in principle be used as a KDF for the reader key; that is a
// design choice for later, with caveats (hardware keys and agents that ask for
// approval per use make it unusable).

const (
	agentFailure       = 5
	agentRequestIDs    = 11
	agentIDsAnswer     = 12
	agentSignRequest   = 13
	agentSignResponse  = 14
	maxAgentMessageLen = 256 * 1024
)

// AgentKey is a key an agent holds.
type AgentKey struct {
	Pub     ed25519.PublicKey
	Comment string
}

func agentCall(rw io.ReadWriter, typ byte, payload []byte) (byte, []byte, error) {
	msg := binary.BigEndian.AppendUint32(nil, uint32(1+len(payload)))
	msg = append(append(msg, typ), payload...)
	if _, err := rw.Write(msg); err != nil {
		return 0, nil, err
	}
	var hdr [4]byte
	if _, err := io.ReadFull(rw, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n < 1 || n > maxAgentMessageLen {
		return 0, nil, errors.New("ssh-agent sent an invalid message")
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(rw, body); err != nil {
		return 0, nil, err
	}
	return body[0], body[1:], nil
}

// AgentList returns the ssh-ed25519 keys the agent holds; other types are skipped.
func AgentList(rw io.ReadWriter) ([]AgentKey, error) {
	t, b, err := agentCall(rw, agentRequestIDs, nil)
	if err != nil {
		return nil, err
	}
	if t != agentIDsAnswer || len(b) < 4 {
		return nil, errors.New("ssh-agent refused to list its keys")
	}
	n := binary.BigEndian.Uint32(b)
	b = b[4:]
	var out []AgentKey
	for i := uint32(0); i < n; i++ {
		blob, rest, ok := sshString(b)
		if !ok {
			return nil, errors.New("ssh-agent sent a truncated list")
		}
		comment, rest, ok := sshString(rest)
		if !ok {
			return nil, errors.New("ssh-agent sent a truncated list")
		}
		b = rest
		if pub, err := ParseSSHBlob(b64std(blob)); err == nil {
			out = append(out, AgentKey{pub, string(comment)})
		}
	}
	return out, nil
}

// AgentSign asks the agent to sign data with pub and returns the 64-byte Ed25519
// signature (what ed25519.Verify takes). The agent may ask its user first.
func AgentSign(rw io.ReadWriter, pub ed25519.PublicKey, data []byte) ([]byte, error) {
	blob := sshWire(sshEd25519, string(pub))
	req := binary.BigEndian.AppendUint32(nil, uint32(len(blob)))
	req = append(req, blob...)
	req = binary.BigEndian.AppendUint32(req, uint32(len(data)))
	req = append(req, data...)
	req = binary.BigEndian.AppendUint32(req, 0) // flags: none (Ed25519 has no variants)
	t, b, err := agentCall(rw, agentSignRequest, req)
	if err != nil {
		return nil, err
	}
	if t == agentFailure {
		return nil, errors.New("ssh-agent refused to sign")
	}
	if t != agentSignResponse {
		return nil, errors.New("ssh-agent sent an unexpected reply")
	}
	sig, _, ok := sshString(b)
	if !ok {
		return nil, errors.New("ssh-agent sent a truncated signature")
	}
	format, rest, ok := sshString(sig)
	if !ok || string(format) != sshEd25519 {
		return nil, errors.New("ssh-agent signed with another algorithm")
	}
	raw, _, ok := sshString(rest)
	if !ok || len(raw) != ed25519.SignatureSize {
		return nil, errors.New("ssh-agent sent a malformed signature")
	}
	return append([]byte(nil), raw...), nil
}

// sshWire encodes strings as uint32 length || bytes, one after the other.
func sshWire(parts ...string) []byte {
	var b []byte
	for _, p := range parts {
		b = binary.BigEndian.AppendUint32(b, uint32(len(p)))
		b = append(b, p...)
	}
	return b
}
