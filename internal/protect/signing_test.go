package protect

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func signer(t *testing.T, name string) *Identity {
	t.Helper()
	id, err := NewIdentity(name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(id.Close)
	return id
}

func TestSignAndVerify(t *testing.T) {
	ada, dan := signer(t, "ada"), signer(t, "dan")
	readers := []Reader{ada.AsReader(), dan.AsReader()}
	sig, err := ada.SignMessage(DomainTxnFinish, "L1", []byte("t-1"), []byte("apply"))
	if err != nil {
		t.Fatal(err)
	}
	if st, name := VerifySignature(&sig, readers, DomainTxnFinish, "L1", []byte("t-1"), []byte("apply")); st != SigVerified || name != "ada" {
		t.Fatalf("verify = %v %q", st, name)
	}
	// Every part of the message is bound: the domain, the state, each field, and where
	// one field ends and the next begins.
	for name, f := range map[string]func() (SigStatus, string){
		"domain": func() (SigStatus, string) {
			return VerifySignature(&sig, readers, DomainTxnStart, "L1", []byte("t-1"), []byte("apply"))
		},
		"lineage": func() (SigStatus, string) {
			return VerifySignature(&sig, readers, DomainTxnFinish, "L2", []byte("t-1"), []byte("apply"))
		},
		"field": func() (SigStatus, string) {
			return VerifySignature(&sig, readers, DomainTxnFinish, "L1", []byte("t-1"), []byte("apply!"))
		},
		"framing": func() (SigStatus, string) {
			return VerifySignature(&sig, readers, DomainTxnFinish, "L1", []byte("t-1apply"))
		},
		"order": func() (SigStatus, string) {
			return VerifySignature(&sig, readers, DomainTxnFinish, "L1", []byte("apply"), []byte("t-1"))
		},
	} {
		if st, _ := f(); st != SigInvalid {
			t.Errorf("%s: %v, want invalid", name, st)
		}
	}
	// A signature that names another key, or a key nobody lists, or none.
	forged := sig
	forged.Sig = append([]byte(nil), sig.Sig...)
	forged.Sig[0] ^= 1
	if st, _ := VerifySignature(&forged, readers, DomainTxnFinish, "L1", []byte("t-1"), []byte("apply")); st != SigInvalid {
		t.Errorf("a changed signature: %v", st)
	}
	if st, _ := VerifySignature(&sig, []Reader{dan.AsReader()}, DomainTxnFinish, "L1", []byte("t-1"), []byte("apply")); st != SigUnknownKey {
		t.Errorf("a key that is not listed: %v", st)
	}
	if st, _ := VerifySignature(nil, readers, DomainTxnFinish, "L1"); st != SigUnsigned {
		t.Errorf("no signature: %v", st)
	}
	// The name in the signature is a label: dan's key under ada's name verifies as dan.
	d, _ := dan.SignMessage(DomainPlan, "L1", []byte("x"))
	d.Signer = "ada"
	if st, name := VerifySignature(&d, readers, DomainPlan, "L1", []byte("x")); st != SigVerified || name != "dan" {
		t.Errorf("a borrowed name: %v %q", st, name)
	}
}

// fakeAgent answers the sign requests of the ssh-agent protocol with priv.
func fakeAgent(t *testing.T, priv ed25519.PrivateKey) func() (io.ReadWriteCloser, error) {
	t.Helper()
	return func() (io.ReadWriteCloser, error) {
		c, s := net.Pipe()
		go func() {
			defer s.Close()
			for {
				var hdr [4]byte
				if _, err := io.ReadFull(s, hdr[:]); err != nil {
					return
				}
				body := make([]byte, binary.BigEndian.Uint32(hdr[:]))
				if _, err := io.ReadFull(s, body); err != nil {
					return
				}
				reply := []byte{agentFailure}
				if body[0] == agentSignRequest {
					_, rest, _ := sshString(body[1:])
					data, _, _ := sshString(rest)
					raw := ed25519.Sign(priv, data)
					reply = append([]byte{agentSignResponse}, sshStringBytes(append(sshStringBytes([]byte(sshEd25519)), sshStringBytes(raw)...))...)
				}
				out := binary.BigEndian.AppendUint32(nil, uint32(len(reply)))
				if _, err := s.Write(append(out, reply...)); err != nil {
					return
				}
			}
		}()
		return c, nil
	}
}

func sshStringBytes(b []byte) []byte {
	return append(binary.BigEndian.AppendUint32(nil, uint32(len(b))), b...)
}

func TestAnIdentityThatSignsThroughTheAgent(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	id, err := NewIdentity("ben", sshEd25519+" "+SSHBlob(pub))
	if err != nil {
		t.Fatal(err)
	}
	defer id.Close()
	old := DialAgentFunc
	defer func() { DialAgentFunc = old }()

	DialAgentFunc = fakeAgent(t, priv)
	sig, err := id.SignMessage(DomainTxnStart, "L1", []byte("t-9"))
	if err != nil {
		t.Fatal(err)
	}
	if st, name := VerifySignature(&sig, []Reader{id.AsReader()}, DomainTxnStart, "L1", []byte("t-9")); st != SigVerified || name != "ben" {
		t.Errorf("verify = %v %q", st, name)
	}
	// An agent that does not hold the key refuses, and so does one that is not there.
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	DialAgentFunc = func() (io.ReadWriteCloser, error) {
		c, s := net.Pipe()
		go func() {
			defer s.Close()
			var hdr [4]byte
			io.ReadFull(s, hdr[:])
			io.ReadFull(s, make([]byte, binary.BigEndian.Uint32(hdr[:])))
			s.Write([]byte{0, 0, 0, 1, agentFailure})
		}()
		return c, nil
	}
	_ = other
	if _, err := id.SignMessage(DomainTxnStart, "L1", []byte("t-9")); err == nil || !strings.Contains(err.Error(), "refused to sign") {
		t.Errorf("an agent that refuses: %v", err)
	}
	DialAgentFunc = func() (io.ReadWriteCloser, error) { return nil, io.ErrClosedPipe }
	if _, err := id.SignMessage(DomainTxnStart, "L1", []byte("t-9")); err == nil || !strings.Contains(err.Error(), "not reachable") {
		t.Errorf("no agent: %v", err)
	}
}

func TestAnAgentThatSignsWithAnotherKeyIsRefused(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	id, err := NewIdentity("ben", sshEd25519+" "+SSHBlob(pub))
	if err != nil {
		t.Fatal(err)
	}
	defer id.Close()
	old := DialAgentFunc
	defer func() { DialAgentFunc = old }()
	DialAgentFunc = fakeAgent(t, other)
	if _, err := id.SignMessage(DomainTxnStart, "L1", []byte("t")); err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Errorf("an agent with another key: %v", err)
	}
}

func TestAnAgentThatDoesNotAnswerIsGivenUp(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	id, _ := NewIdentity("ben", sshEd25519+" "+SSHBlob(pub))
	defer id.Close()
	oldDial, oldWait := DialAgentFunc, AgentTimeout
	defer func() { DialAgentFunc, AgentTimeout = oldDial, oldWait }()
	AgentTimeout = 50 * time.Millisecond
	DialAgentFunc = func() (io.ReadWriteCloser, error) {
		c, s := net.Pipe()
		go io.Copy(io.Discard, s) // reads the request and never answers
		return c, nil
	}
	if _, err := id.SignMessage(DomainTxnStart, "L1", []byte("t")); err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("a silent agent: %v", err)
	}
}

func TestAMessageOfAnySizeGoesToTheAgentAsADigest(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	id, _ := NewIdentity("ben", sshEd25519+" "+SSHBlob(pub))
	defer id.Close()
	old := DialAgentFunc
	defer func() { DialAgentFunc = old }()
	DialAgentFunc = fakeAgent(t, priv)
	big := make([]byte, 1<<20)
	sig, err := id.SignMessage(DomainTxnFinish, "L1", big)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := VerifySignature(&sig, []Reader{id.AsReader()}, DomainTxnFinish, "L1", big); st != SigVerified {
		t.Errorf("verdict %v", st)
	}
}
