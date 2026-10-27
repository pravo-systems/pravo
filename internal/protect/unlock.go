package protect

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// An identity file can be sealed under the ssh-agent instead of a passphrase. The
// agent never reveals its private key and cannot decrypt, but an Ed25519 signature
// is deterministic (RFC 8032), so the signature of a fixed challenge is a secret
// that only the holder of the key can make, and it is the same every time. The
// wrapping key is HKDF-SHA256 over that signature.
//
// The challenge names the file (its name, its reader key, the key that signs and a
// random salt that is stored in it), so a signature made for one file opens no
// other, and it starts with a tag that the signatures of events (signedBytes) can
// never start with, so that signing an event never yields a wrapping key.
//
// What opens the file is therefore whatever makes the agent sign: a touch in
// 1Password, a confirmation from ssh-add -c, nothing from an agent that signs for any
// process of the user. The agent decides, and this file is as safe as it is.
//
// The seal lives in the "seal" object of the identity file with kdf
// "ssh-agent-ed25519" and the agent key in "agent". A pravo that does not know the
// function refuses the file ("identity uses kdf ...").

const (
	unlockTag  = "pravo/identity/unlock/v1"
	unlockInfo = "pravo/identity/unlock-key/v1"
	unlockSalt = 32
)

// unlockSign is how the signature of the challenge is had. A test replaces it.
var unlockSign = agentUnlockSignature

// unlockChallenge is what the agent signs.
func (s *fileSeal) unlockChallenge(f *identityFile) []byte {
	return pae([]byte(unlockTag), []byte(f.Name), []byte(f.Reader.Pub.String()), []byte(s.Agent), s.Salt)
}

// agentKey derives the wrapping key of an agent seal: it asks the agent for the
// signature of the challenge.
func (s *fileSeal) agentKey(f *identityFile) ([]byte, error) {
	if len(s.Salt) < unlockSalt {
		return nil, errors.New("identity file has unacceptable key derivation parameters")
	}
	pub, err := ParseSSHBlob(s.Agent)
	if err != nil {
		return nil, fmt.Errorf("identity unlock key: %w", err)
	}
	sig, err := unlockSign(f.Name, pub, s.unlockChallenge(f))
	if err != nil {
		return nil, err
	}
	defer clear(sig)
	return hkdf.Key(sha256.New, sig, s.Salt, unlockInfo, 32)
}

// agentUnlockSignature asks the ssh-agent for the signature of challenge by pub. It
// lists the agent's keys first, so that a key the agent does not hold (a locked
// vault, a key that was removed) is told from a signature the agent refused.
func agentUnlockSignature(name string, pub ed25519.PublicKey, challenge []byte) ([]byte, error) {
	var sig []byte
	err := callAgent(func(rw io.ReadWriter) (err error) {
		keys, err := AgentList(rw)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(keys, func(k AgentKey) bool { return k.Pub.Equal(pub) }) {
			return errAgentLacksKey
		}
		sig, err = AgentSign(rw, pub, challenge)
		return err
	})
	var down agentUnreachable
	var silent agentSilent
	switch {
	case errors.As(err, &down):
		return nil, fmt.Errorf("identity %s is unlocked by the ssh-agent, which is not reachable: %w", name, err)
	case errors.As(err, &silent):
		return nil, err
	case errors.Is(err, errAgentLacksKey):
		return nil, fmt.Errorf("identity %s is unlocked by the ssh-agent key %s, which the agent does not hold (is it locked, or is the key gone?)", name, SSHFingerprint(pub))
	case err != nil:
		return nil, fmt.Errorf("identity %s is unlocked by the ssh-agent: %w", name, err)
	}
	if !ed25519.Verify(pub, challenge, sig) {
		return nil, fmt.Errorf("identity %s: the signature that the ssh-agent made does not verify against its key %s", name, SSHFingerprint(pub))
	}
	return sig, nil
}

var errAgentLacksKey = errors.New("the ssh-agent does not hold the key")

// MarshalAgentSealed is the interactive form for a person whose ssh-agent keeps a
// key: the secrets are sealed under a key derived from the agent's signature, and
// opening the file takes whatever makes the agent sign. The agent is asked for the
// signature twice, so that one that signs differently each time (a randomised signer
// would seal the file under a key that can never be derived again) is found now.
func (id *Identity) MarshalAgentSealed(agentKey ed25519.PublicKey) ([]byte, error) {
	f := id.file()
	s := &fileSeal{KDF: kdfAgent, Salt: randBytes(unlockSalt), Agent: SSHBlob(agentKey)}
	key, err := s.agentKey(f)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	again, err := s.agentKey(f)
	if err != nil {
		return nil, err
	}
	defer clear(again)
	if !bytes.Equal(key, again) {
		return nil, fmt.Errorf("the ssh-agent signs differently each time, so a key derived from its signature could not be derived again: use another key (%s)", SSHFingerprint(agentKey))
	}
	secret := append(id.Reader.Bytes(), id.seed...)
	defer clear(secret)
	ct, err := s.aead(key, f, secret, nil)
	if err != nil {
		return nil, err
	}
	s.CT = ct
	f.Seal = s
	return json.MarshalIndent(f, "", "  ")
}

// AgentKeys lists the ssh-ed25519 keys that the ssh-agent holds.
func AgentKeys() ([]AgentKey, error) {
	var keys []AgentKey
	err := callAgent(func(rw io.ReadWriter) (err error) {
		keys, err = AgentList(rw)
		return err
	})
	var down agentUnreachable
	switch {
	case errors.As(err, &down):
		return nil, fmt.Errorf("the ssh-agent is not reachable: %w", err)
	case err != nil:
		return nil, err
	}
	return keys, nil
}

// PickAgentKey chooses the key that unlocks an identity among the keys of the
// agent. With no selector there has to be exactly one. A selector is the
// fingerprint of the key (SHA256:...) or part of its comment, which is the title
// of the item in 1Password; it has to match exactly one.
func PickAgentKey(keys []AgentKey, selector string) (AgentKey, error) {
	if len(keys) == 0 {
		return AgentKey{}, errors.New("the ssh-agent holds no ssh-ed25519 key (is it locked, or is the key an RSA or ECDSA one?)")
	}
	var match []AgentKey
	switch selector = strings.TrimSpace(selector); selector {
	case "":
		match = keys
	default:
		for _, k := range keys {
			if SSHFingerprint(k.Pub) == selector {
				match = []AgentKey{k}
				break
			}
			if strings.Contains(strings.ToLower(k.Comment), strings.ToLower(selector)) {
				match = append(match, k)
			}
		}
	}
	if len(match) == 1 {
		return match[0], nil
	}
	var b strings.Builder
	for _, k := range match {
		fmt.Fprintf(&b, "\n  %s  %s", SSHFingerprint(k.Pub), k.Comment)
	}
	switch {
	case selector == "":
		return AgentKey{}, fmt.Errorf("the ssh-agent holds several ssh-ed25519 keys; choose one with --agent-key (a fingerprint, or part of the comment):%s", b.String())
	case len(match) == 0:
		for _, k := range keys {
			fmt.Fprintf(&b, "\n  %s  %s", SSHFingerprint(k.Pub), k.Comment)
		}
		return AgentKey{}, fmt.Errorf("no key of the ssh-agent matches %q; it holds:%s", selector, b.String())
	}
	return AgentKey{}, fmt.Errorf("%q matches several keys of the ssh-agent; use the fingerprint:%s", selector, b.String())
}
