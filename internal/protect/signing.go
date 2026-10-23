package protect

import (
	"crypto/ed25519"
	"errors"
	"fmt"
)

// Signing attributes an event to a key. What is signed is a message built from a
// domain tag, the lineage of the state and the fields of the event, each framed by
// its length (see pae), so that a signature made for one kind of event, or in one
// state, verifies nowhere else.
//
// The key that signs is the signing key of an identity, which is also the second field
// of its line in the readers file. A signature is trusted when its key is in the
// readers file of the repository, which a writer of the bucket cannot change.

// Domains of the signatures. They are part of the message and never change: a
// signature that was made under one is not valid under another.
const (
	DomainTxnStart  = "pravo/v1/txn-start"
	DomainTxnFinish = "pravo/v1/txn-finish"
	DomainPlan      = "pravo/v1/plan"
)

// Signature is a signature as it is stored beside what it signs. Signer is the
// name the signer gave, a label: the key decides.
type Signature struct {
	Signer string `json:"signer"`
	Key    string `json:"key"`
	Sig    []byte `json:"sig"`
}

// SigStatus is what a verifier makes of a signature.
type SigStatus int

const (
	// SigUnsigned means there is no signature.
	SigUnsigned SigStatus = iota
	// SigVerified means a key in the readers file made the signature.
	SigVerified
	// SigUnknownKey means the key is not in the readers file: a member who has left, or
	// a key that never was. Nothing says the signature is wrong.
	SigUnknownKey
	// SigInvalid means the key is in the readers file and the signature is not its
	// signature of this message: the event was changed, or the signature was made up.
	SigInvalid
)

func (s SigStatus) String() string {
	return [...]string{"unsigned", "verified", "key not in the file", "invalid"}[s]
}

// SigningMessage is the message that a signature covers.
func SigningMessage(domain, lineage string, parts ...[]byte) []byte {
	all := make([][]byte, 0, 2+len(parts))
	all = append(all, []byte(domain), []byte(lineage))
	return pae(append(all, parts...)...)
}

// SignMessage signs a message of domain, lineage and parts with the signing key of
// the identity: from the identity file, or through the ssh-agent for an identity made
// with --ssh. A signature that cannot be made is an error and never an empty
// signature.
func (id *Identity) SignMessage(domain, lineage string, parts ...[]byte) (Signature, error) {
	if len(id.SignPub) != ed25519.PublicKeySize {
		return Signature{}, errors.New("protect: this identity has no signing key")
	}
	sig, err := id.Sign(SigningMessage(domain, lineage, parts...))
	if err != nil {
		return Signature{}, err
	}
	return Signature{Signer: id.Name, Key: SSHFingerprint(id.SignPub), Sig: sig}, nil
}

// VerifySignature checks sig against the readers: the one whose signing key has the
// fingerprint the signature names. It returns the name the readers file gives that
// key, empty unless the signature verified.
func VerifySignature(sig *Signature, readers []Reader, domain, lineage string, parts ...[]byte) (SigStatus, string) {
	if sig == nil {
		return SigUnsigned, ""
	}
	for _, r := range readers {
		if len(r.Sign) != ed25519.PublicKeySize || SSHFingerprint(r.Sign) != sig.Key {
			continue
		}
		if len(sig.Sig) == ed25519.SignatureSize && ed25519.Verify(r.Sign, SigningMessage(domain, lineage, parts...), sig.Sig) {
			return SigVerified, r.Name
		}
		return SigInvalid, ""
	}
	return SigUnknownKey, ""
}

// DialAgentFunc connects to the ssh-agent. A test replaces it.
var DialAgentFunc = DialAgent

// signViaAgent asks the ssh-agent to sign msg with the signing key of the identity.
func (id *Identity) signViaAgent(msg []byte) ([]byte, error) {
	rw, err := DialAgentFunc()
	if err != nil {
		return nil, fmt.Errorf("protect: identity %s signs through the ssh-agent, which is not reachable: %w", id.Name, err)
	}
	defer rw.Close()
	sig, err := AgentSign(rw, id.SignPub, msg)
	if err != nil {
		return nil, fmt.Errorf("protect: identity %s signs through the ssh-agent: %w", id.Name, err)
	}
	return sig, nil
}
