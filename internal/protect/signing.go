package protect

import (
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"time"
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

// Key is the status as JSON and the viewer spell it.
func (s SigStatus) Key() string {
	return [...]string{"unsigned", "verified", "key_not_in_file", "invalid"}[s]
}

func (s SigStatus) String() string {
	return [...]string{"unsigned", "verified", "key not in the file", "invalid"}[s]
}

// SigningMessage is the message that a signature covers: the domain, the lineage and the
// parts, each framed by its length.
func SigningMessage(domain, lineage string, parts ...[]byte) []byte {
	all := make([][]byte, 0, 2+len(parts))
	all = append(all, []byte(domain), []byte(lineage))
	return pae(append(all, parts...)...)
}

// signedBytes are the bytes that the key signs: a fixed prefix and the SHA-256 of the
// message. The message of a transaction with thousands of changes is long, and an ssh-agent
// refuses a request that is too large; the digest is the same size whatever it covers.
func signedBytes(domain, lineage string, parts ...[]byte) []byte {
	sum := sha256.Sum256(SigningMessage(domain, lineage, parts...))
	return append([]byte("pravo/v1/signature\x00"), sum[:]...)
}

// SignMessage signs a message of domain, lineage and parts with the signing key of
// the identity: from the identity file, or through the ssh-agent for an identity made
// with --ssh. A signature that cannot be made is an error and never an empty
// signature.
func (id *Identity) SignMessage(domain, lineage string, parts ...[]byte) (Signature, error) {
	if len(id.SignPub) != ed25519.PublicKeySize {
		return Signature{}, errors.New("protect: this identity has no signing key")
	}
	sig, err := id.Sign(signedBytes(domain, lineage, parts...))
	if err != nil {
		return Signature{}, err
	}
	// Whatever signed, the signature has to be the one of this identity's key: an agent
	// that holds another key, or answers with something else, must not leave a signature
	// in the log that nobody can verify.
	if !ed25519.Verify(id.SignPub, signedBytes(domain, lineage, parts...), sig) {
		return Signature{}, fmt.Errorf("protect: the signature that identity %s got does not verify against its signing key: is the right key loaded in the ssh-agent?", id.Name)
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
		if len(sig.Sig) == ed25519.SignatureSize && ed25519.Verify(r.Sign, signedBytes(domain, lineage, parts...), sig.Sig) {
			return SigVerified, r.Name
		}
		return SigInvalid, ""
	}
	return SigUnknownKey, ""
}

// DialAgentFunc connects to the ssh-agent. A test replaces it.
var DialAgentFunc = DialAgent

// AgentTimeout is how long a signature waits for the ssh-agent, which may be waiting for a
// touch. A test shortens it.
var AgentTimeout = 90 * time.Second

// agentUnreachable is the error of a connection to the ssh-agent that could not be
// made.
type agentUnreachable struct{ err error }

func (e agentUnreachable) Error() string { return e.err.Error() }
func (e agentUnreachable) Unwrap() error { return e.err }

// agentSilent is the error of an ssh-agent that did not answer in time.
type agentSilent struct{ wait time.Duration }

func (e agentSilent) Error() string {
	return fmt.Sprintf("protect: the ssh-agent did not answer in %s (does it wait for a touch?)", e.wait)
}

// callAgent runs f on a connection to the ssh-agent and gives up when the agent
// does not answer in AgentTimeout. A connection that cannot be made is an
// agentUnreachable, and an agent that does not answer is an agentSilent.
func callAgent(f func(rw io.ReadWriter) error) error {
	rw, err := DialAgentFunc()
	if err != nil {
		return agentUnreachable{err}
	}
	defer rw.Close()
	done := make(chan error, 1)
	go func() { done <- f(rw) }()
	select {
	case err := <-done:
		return err
	case <-time.After(AgentTimeout):
		return agentSilent{AgentTimeout}
	}
}

// signViaAgent asks the ssh-agent to sign msg with the signing key of the identity.
func (id *Identity) signViaAgent(msg []byte) ([]byte, error) {
	var sig []byte
	err := callAgent(func(rw io.ReadWriter) (err error) {
		sig, err = AgentSign(rw, id.SignPub, msg)
		return err
	})
	var down agentUnreachable
	var silent agentSilent
	switch {
	case errors.As(err, &down):
		return nil, fmt.Errorf("protect: identity %s signs through the ssh-agent, which is not reachable: %w", id.Name, err)
	case errors.As(err, &silent):
		return nil, err
	case err != nil:
		return nil, fmt.Errorf("protect: identity %s signs through the ssh-agent: %w", id.Name, err)
	}
	return sig, nil
}
