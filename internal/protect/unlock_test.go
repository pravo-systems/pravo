package protect

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// keyAgent is an ssh-agent that holds keys: it lists them and signs with them. It
// counts what it is asked.
type keyAgent struct {
	mu     sync.Mutex
	keys   []ed25519.PrivateKey
	names  []string
	refuse bool               // answers every signature request with a failure
	forge  ed25519.PrivateKey // signs with this key whichever key is asked for
	lists  int
	signs  int
}

func (a *keyAgent) add(t *testing.T, comment string) ed25519.PublicKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	a.addKey(priv, comment)
	return pub
}

func (a *keyAgent) addKey(priv ed25519.PrivateKey, comment string) {
	a.keys, a.names = append(a.keys, priv), append(a.names, comment)
}

func (a *keyAgent) handle(body []byte) []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch body[0] {
	case agentRequestIDs:
		a.lists++
		reply := binary.BigEndian.AppendUint32([]byte{agentIDsAnswer}, uint32(len(a.keys)))
		for i, k := range a.keys {
			blob := sshWire(sshEd25519, string(k.Public().(ed25519.PublicKey)))
			reply = append(reply, sshStringBytes(blob)...)
			reply = append(reply, sshStringBytes([]byte(a.names[i]))...)
		}
		return reply
	case agentSignRequest:
		a.signs++
		blob, rest, _ := sshString(body[1:])
		data, _, _ := sshString(rest)
		for _, k := range a.keys {
			if a.refuse || !bytes.Equal(blob, sshWire(sshEd25519, string(k.Public().(ed25519.PublicKey)))) {
				continue
			}
			if a.forge != nil {
				k = a.forge
			}
			raw := ed25519.Sign(k, data)
			return append([]byte{agentSignResponse}, sshStringBytes(append(sshStringBytes([]byte(sshEd25519)), sshStringBytes(raw)...))...)
		}
	}
	return []byte{agentFailure}
}

// use makes the agent the one that protect talks to, until the test ends.
func (a *keyAgent) use(t *testing.T) {
	t.Helper()
	old := DialAgentFunc
	t.Cleanup(func() { DialAgentFunc = old })
	DialAgentFunc = func() (io.ReadWriteCloser, error) {
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
				reply := a.handle(body)
				if _, err := s.Write(append(binary.BigEndian.AppendUint32(nil, uint32(len(reply))), reply...)); err != nil {
					return
				}
			}
		}()
		return c, nil
	}
}

func (a *keyAgent) count() (lists, signs int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lists, a.signs
}

// neverAsked is the passphrase callback of a file that must not ask for one.
func neverAsked(t *testing.T) func() ([]byte, error) {
	return func() ([]byte, error) {
		t.Error("a passphrase was asked for")
		return nil, errors.New("no passphrase")
	}
}

func sealedByAgent(t *testing.T, name string, agentKey ed25519.PublicKey) (*Identity, []byte) {
	t.Helper()
	id := signer(t, name)
	file, err := id.MarshalAgentSealed(agentKey)
	if err != nil {
		t.Fatal(err)
	}
	return id, file
}

func TestAnIdentitySealedByTheAgentOpensThroughIt(t *testing.T) {
	var ag keyAgent
	ag.add(t, "some other key")
	pub := ag.add(t, "pravo")
	ag.use(t)
	id, file := sealedByAgent(t, "ada-mac", pub)

	// Two signatures to seal it: the second proves that the agent signs the same way.
	if _, signs := ag.count(); signs != 2 {
		t.Errorf("the agent signed %d times to seal the file, want 2", signs)
	}
	for _, secret := range [][]byte{id.Reader.Bytes(), id.seed} {
		if bytes.Contains(file, secret) || bytes.Contains(file, []byte(b64std(secret))) {
			t.Fatal("a secret is in the identity file in the clear")
		}
	}
	for _, key := range []string{`"seed"`, `"key"`} {
		if bytes.Contains(file, []byte(key)) {
			t.Errorf("the sealed file has a %s field", key)
		}
	}
	var f struct {
		Seal map[string]any `json:"seal"`
	}
	if err := json.Unmarshal(file, &f); err != nil {
		t.Fatal(err)
	}
	if f.Seal["kdf"] != "ssh-agent-ed25519" || f.Seal["agent"] != SSHBlob(pub) || f.Seal["iter"] != nil {
		t.Errorf("seal = %v", f.Seal)
	}
	if n := len(f.Seal["salt"].(string)); n < 40 { // 32 bytes in base64
		t.Errorf("salt is %d characters", n)
	}

	_, before := ag.count()
	got, err := ParseIdentity(file, neverAsked(t))
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()
	if _, signs := ag.count(); signs != before+1 {
		t.Errorf("opening the file took %d signatures, want 1", signs-before)
	}
	if got.Line() != id.Line() || !got.Reader.Public().Equal(id.Reader.Public()) {
		t.Error("the identity changed")
	}
	sig, err := got.Sign([]byte("m"))
	if err != nil || !ed25519.Verify(got.SignPub, []byte("m"), sig) {
		t.Errorf("the opened identity does not sign: %v", err)
	}
	// With no way to ask for a passphrase the file still opens.
	if g, err := ParseIdentity(file, nil); err != nil {
		t.Errorf("without a passphrase callback: %v", err)
	} else {
		g.Close()
	}
}

func TestAnIdentityThatSignsThroughTheAgentCanBeSealedByIt(t *testing.T) {
	var ag keyAgent
	pub := ag.add(t, "pravo")
	ag.use(t)
	id, err := NewIdentity("ada-mac", sshEd25519+" "+SSHBlob(pub))
	if err != nil {
		t.Fatal(err)
	}
	defer id.Close()
	file, err := id.MarshalAgentSealed(pub)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseIdentity(file, neverAsked(t))
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()
	if got.SignVia != "ssh-agent" || got.seed != nil {
		t.Errorf("signing via %q, seed held: %v", got.SignVia, got.seed != nil)
	}
	if _, err := got.SignMessage(DomainPlan, "L1", []byte("p")); err != nil {
		t.Errorf("signing through the agent: %v", err)
	}
}

func TestAnAgentThatCannotOpenAnIdentityIsNamedInTheError(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var maker keyAgent
	maker.addKey(priv, "pravo")
	maker.use(t)
	_, file := sealedByAgent(t, "ada-mac", pub)
	fp := SSHFingerprint(pub)

	for name, tc := range map[string]struct {
		agent func(t *testing.T) // makes the agent that the file is opened with
		want  []string
	}{
		"a locked agent holds no keys": {
			func(t *testing.T) { (&keyAgent{}).use(t) },
			[]string{"identity ada-mac", fp, "does not hold", "locked"},
		},
		"an agent that holds other keys": {
			func(t *testing.T) { var ag keyAgent; ag.add(t, "other"); ag.use(t) },
			[]string{fp, "does not hold"},
		},
		"an agent that refuses to sign": {
			func(t *testing.T) { ag := keyAgent{refuse: true}; ag.addKey(priv, "pravo"); ag.use(t) },
			[]string{"identity ada-mac", "refused to sign"},
		},
		"no agent": {
			func(t *testing.T) {
				old := DialAgentFunc
				t.Cleanup(func() { DialAgentFunc = old })
				DialAgentFunc = func() (io.ReadWriteCloser, error) { return nil, errors.New("no ssh-agent") }
			},
			[]string{"identity ada-mac", "not reachable", "no ssh-agent"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			tc.agent(t)
			_, err := ParseIdentity(file, neverAsked(t))
			if err == nil {
				t.Fatal("the file opened")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("%q lacks %q", err, w)
				}
			}
			if strings.Contains(err.Error(), "wrong passphrase") {
				t.Errorf("%q talks of a passphrase", err)
			}
		})
	}
}

func TestAnAgentThatDoesNotAnswerAnUnlockIsGivenUp(t *testing.T) {
	var ag keyAgent
	pub := ag.add(t, "pravo")
	ag.use(t)
	_, file := sealedByAgent(t, "ada-mac", pub)
	oldDial, oldWait := DialAgentFunc, AgentTimeout
	defer func() { DialAgentFunc, AgentTimeout = oldDial, oldWait }()
	AgentTimeout = 50 * time.Millisecond
	DialAgentFunc = func() (io.ReadWriteCloser, error) {
		c, s := net.Pipe()
		go io.Copy(io.Discard, s)
		return c, nil
	}
	if _, err := ParseIdentity(file, neverAsked(t)); err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("a silent agent: %v", err)
	}
}

func TestAnAgentSealOpensOnlyForTheFileItWasMadeFor(t *testing.T) {
	var ag keyAgent
	pub := ag.add(t, "pravo")
	other := ag.add(t, "other")
	ag.use(t)
	_, file := sealedByAgent(t, "ada-mac", pub)
	_, second := sealedByAgent(t, "dan-mac", pub)

	var a, b map[string]json.RawMessage
	json.Unmarshal(file, &a)
	json.Unmarshal(second, &b)

	with := func(m map[string]json.RawMessage, edit func(map[string]json.RawMessage), seal func(map[string]any)) []byte {
		cp := map[string]json.RawMessage{}
		for k, v := range m {
			cp[k] = v
		}
		if edit != nil {
			edit(cp)
		}
		if seal != nil {
			var s map[string]any
			json.Unmarshal(cp["seal"], &s)
			seal(s)
			cp["seal"], _ = json.Marshal(s)
		}
		out, _ := json.Marshal(cp)
		return out
	}
	otherBlob := SSHBlob(other)
	for name, data := range map[string][]byte{
		"the seal of another identity": with(a, func(m map[string]json.RawMessage) { m["seal"] = b["seal"] }, nil),
		"another name":                 with(a, func(m map[string]json.RawMessage) { m["name"] = []byte(`"eve"`) }, nil),
		"another agent key":            with(a, nil, func(s map[string]any) { s["agent"] = otherBlob }),
		"another salt":                 with(a, nil, func(s map[string]any) { s["salt"] = b64std(randBytes(32)) }),
		"a short salt":                 with(a, nil, func(s map[string]any) { s["salt"] = b64std(randBytes(16)) }),
		"an iteration count":           with(a, nil, func(s map[string]any) { s["iter"] = 1_000_000 }),
		"another function":             with(a, nil, func(s map[string]any) { s["kdf"] = "pbkdf2-sha256" }),
		"a damaged ciphertext": with(a, nil, func(s map[string]any) {
			ct, _ := base64.StdEncoding.DecodeString(s["ct"].(string))
			ct[len(ct)/2] ^= 1
			s["ct"] = b64std(ct)
		}),
	} {
		_, err := ParseIdentity(data, func() ([]byte, error) { return []byte("whatever it is"), nil })
		if err == nil {
			t.Errorf("%s: the file opened", name)
		}
	}
	// The untouched files open.
	for _, data := range [][]byte{file, second} {
		id, err := ParseIdentity(data, neverAsked(t))
		if err != nil {
			t.Fatal(err)
		}
		id.Close()
	}
}

func TestAPravoThatDoesNotKnowTheFunctionRefusesTheFile(t *testing.T) {
	var ag keyAgent
	pub := ag.add(t, "pravo")
	ag.use(t)
	_, file := sealedByAgent(t, "ada-mac", pub)
	var m map[string]json.RawMessage
	json.Unmarshal(file, &m)
	var s map[string]any
	json.Unmarshal(m["seal"], &s)
	s["kdf"] = "ssh-agent-p256"
	m["seal"], _ = json.Marshal(s)
	data, _ := json.Marshal(m)
	_, err := ParseIdentity(data, func() ([]byte, error) { return []byte("whatever it is"), nil })
	if err == nil || !strings.Contains(err.Error(), `kdf "ssh-agent-p256", which this pravo does not know`) {
		t.Errorf("an unknown function: %v", err)
	}
}

func TestAnAgentThatSignsDifferentlyEachTimeIsRefused(t *testing.T) {
	var ag keyAgent
	pub := ag.add(t, "pravo")
	ag.use(t)
	old := unlockSign
	defer func() { unlockSign = old }()
	unlockSign = func(string, ed25519.PublicKey, []byte) ([]byte, error) { return randBytes(64), nil }
	id := signer(t, "ada-mac")
	_, err := id.MarshalAgentSealed(pub)
	if err == nil || !strings.Contains(err.Error(), "signs differently each time") {
		t.Errorf("a randomised signer: %v", err)
	}
}

func TestASignatureThatDoesNotVerifyIsNotAKey(t *testing.T) {
	// The agent lists the key and signs with another one.
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	ag := keyAgent{}
	ag.addKey(priv, "pravo")
	ag.use(t)
	_, file := sealedByAgent(t, "ada-mac", pub)
	ag.forge = other
	if _, err := ParseIdentity(file, neverAsked(t)); err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Errorf("a signature by another key: %v", err)
	}
}

func TestTheUnlockChallengeIsNotASignedEvent(t *testing.T) {
	// The agent key of an identity may also be its signing key. What it signs for an
	// event starts with a fixed tag, and the challenge must never start with it.
	var ag keyAgent
	pub := ag.add(t, "pravo")
	ag.use(t)
	_, file := sealedByAgent(t, "ada-mac", pub)
	var f identityFile
	if err := json.Unmarshal(file, &f); err != nil {
		t.Fatal(err)
	}
	challenge := f.Seal.unlockChallenge(&f)
	if bytes.HasPrefix(challenge, signedBytes(DomainPlan, "L1")[:len("pravo/v1/signature\x00")]) {
		t.Error("the challenge looks like a signed event")
	}
	// It binds the file: the name, the reader key, the agent key and the salt.
	for name, edit := range map[string]func(*identityFile, *fileSeal){
		"name":   func(f *identityFile, _ *fileSeal) { f.Name = "eve" },
		"reader": func(f *identityFile, _ *fileSeal) { f.Reader.Pub = signer(t, "x").Reader.Public() },
		"agent":  func(_ *identityFile, s *fileSeal) { s.Agent = SSHBlob(signer(t, "x").SignPub) },
		"salt":   func(_ *identityFile, s *fileSeal) { s.Salt = randBytes(32) },
	} {
		g := f
		s := *f.Seal
		edit(&g, &s)
		if bytes.Equal(s.unlockChallenge(&g), challenge) {
			t.Errorf("the challenge does not bind the %s", name)
		}
	}
}

func TestAPassphraseSealIsWrittenAsBefore(t *testing.T) {
	id := signer(t, "ada")
	file, err := id.MarshalSealed([]byte("correct horse battery"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Seal map[string]any `json:"seal"`
	}
	json.Unmarshal(file, &f)
	if f.Seal["kdf"] != "pbkdf2-sha256" || f.Seal["iter"] != float64(1_000_000) || f.Seal["agent"] != nil {
		t.Errorf("seal = %v", f.Seal)
	}
	// An agent that is not there changes nothing for a passphrase.
	old := DialAgentFunc
	defer func() { DialAgentFunc = old }()
	DialAgentFunc = func() (io.ReadWriteCloser, error) { return nil, errors.New("no ssh-agent") }
	got, err := ParseIdentity(file, func() ([]byte, error) { return []byte("correct horse battery"), nil })
	if err != nil {
		t.Fatal(err)
	}
	got.Close()
}

func TestPickAgentKey(t *testing.T) {
	var ag keyAgent
	a := ag.add(t, "GitHub key")
	b := ag.add(t, "pravo ada")
	c := ag.add(t, "Pravo dan")
	keyOf := func(i ...int) []AgentKey {
		all := []AgentKey{{a, "GitHub key"}, {b, "pravo ada"}, {c, "Pravo dan"}}
		var out []AgentKey
		for _, j := range i {
			out = append(out, all[j])
		}
		return out
	}
	for name, tc := range map[string]struct {
		keys     []AgentKey
		selector string
		want     ed25519.PublicKey
		err      []string
	}{
		"no keys":                {nil, "", nil, []string{"holds no ssh-ed25519 key", "locked"}},
		"the only key":           {keyOf(0), "", a, nil},
		"one of several, no sel": {keyOf(0, 1), "", nil, []string{"several", "--agent-key", SSHFingerprint(a), "GitHub key", SSHFingerprint(b)}},
		"a fingerprint":          {keyOf(0, 1, 2), SSHFingerprint(b), b, nil},
		"a comment":              {keyOf(0, 1, 2), "github", a, nil},
		"part of a comment":      {keyOf(0, 1, 2), "ada", b, nil},
		"an ambiguous comment":   {keyOf(0, 1, 2), "pravo", nil, []string{`"pravo" matches several`, SSHFingerprint(b), SSHFingerprint(c)}},
		"nothing matches":        {keyOf(0, 1), "work", nil, []string{`matches "work"`, "it holds", SSHFingerprint(a)}},
		"the only key, named":    {keyOf(0), "GitHub", a, nil},
		"the only key, wrong":    {keyOf(0), "nope", nil, []string{`matches "nope"`}},
	} {
		got, err := PickAgentKey(tc.keys, tc.selector)
		if tc.err != nil {
			if err == nil {
				t.Errorf("%s: picked %s", name, got.Comment)
				continue
			}
			for _, w := range tc.err {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("%s: %q lacks %q", name, err, w)
				}
			}
			continue
		}
		if err != nil || !got.Pub.Equal(tc.want) {
			t.Errorf("%s: %v %v", name, got.Comment, err)
		}
	}
}

func TestAgentKeysListsTheEd25519KeysOfTheAgent(t *testing.T) {
	var ag keyAgent
	pub := ag.add(t, "pravo")
	ag.use(t)
	keys, err := AgentKeys()
	if err != nil || len(keys) != 1 || !keys[0].Pub.Equal(pub) || keys[0].Comment != "pravo" {
		t.Fatalf("%v %v", keys, err)
	}
	if lists, signs := ag.count(); lists != 1 || signs != 0 {
		t.Errorf("listing took %d list and %d sign requests", lists, signs)
	}
	DialAgentFunc = func() (io.ReadWriteCloser, error) { return nil, errors.New("no ssh-agent: SSH_AUTH_SOCK is not set") }
	if _, err := AgentKeys(); err == nil || !strings.Contains(err.Error(), "not reachable") || !strings.Contains(err.Error(), "SSH_AUTH_SOCK") {
		t.Errorf("no agent: %v", err)
	}
}
