package protect

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
)

// An identity is a name, a signing key (Ed25519, shown as an ssh-ed25519 line)
// and a reader key (X25519). It lives in one JSON file:
//
//   - ~/.config/pravo/id/NAME, made by `pravo id new`: the secrets (reader key and,
//     if pravo made the signing key, its seed) are sealed under a passphrase;
//   - NAME.identity, made by `pravo id new --ci --out`: the secrets are in the file in
//     the clear, for a CI secret. Mode 0600.
//
// Step 2 never signs, so a signing key that is only a public key (`--ssh FILE`,
// `via: "ssh-agent"`) is enough; the agent protocol comes in a later step.

const identityVersion = 1

type identityFile struct {
	V      int    `json:"v"`
	Name   string `json:"name"`
	Signer struct {
		Alg  string `json:"alg"`            // "ssh-ed25519"
		Pub  string `json:"pub"`            // the BASE64 blob of the ssh public key
		Via  string `json:"via"`            // "file": pravo holds the seed; "ssh-agent": the agent does
		Seed []byte `json:"seed,omitempty"` // CI files only
	} `json:"signing"`
	Reader struct {
		Pub ReaderPublic `json:"pub"`
		Key []byte       `json:"key,omitempty"` // CI files only
	} `json:"reader"`
	Seal *passSeal `json:"seal,omitempty"` // interactive files only
}

// passSeal holds reader key || seed (64 bytes, or 32 without a seed) under a key
// derived from a passphrase. The parameters are stored so that a later version can
// raise the cost or switch to scrypt/argon2 ("kdf" names the function).
type passSeal struct {
	KDF  string `json:"kdf"`  // "pbkdf2-sha256"
	Iter int    `json:"iter"` // 1000000 when written
	Salt []byte `json:"salt"` // 16 random bytes
	CT   []byte `json:"ct"`   // nonce (12) || ciphertext || tag
}

// PBKDF2 cost. OWASP's floor for PBKDF2-HMAC-SHA256 is 600,000; 1,000,000 takes 100-250 ms. Load refuses
// less than minIter (a weak file is a downgrade) and more than maxIter (a hang).
const (
	kdfName     = "pbkdf2-sha256"
	defaultIter = 1_000_000
	minIter     = 100_000
	maxIter     = 20_000_000
	MinPassLen  = 8
)

// Identity is a loaded identity. Close clears the secrets it holds.
type Identity struct {
	Name    string
	SignPub ed25519.PublicKey
	SignVia string // "file" or "ssh-agent"
	Reader  *ReaderKey

	seed []byte // Ed25519 seed when SignVia == "file"
}

// NewIdentity makes a fresh reader key and either a fresh Ed25519 signing key
// (sshPublicLine == "") or a reference to an existing one: the content of an
// OpenSSH .pub file. Only the public key of an existing SSH key is read.
func NewIdentity(name, sshPublicLine string) (*Identity, error) {
	if !nameRE.MatchString(name) {
		return nil, fmt.Errorf("identity name %q: use letters, digits, . _ - (at most 64, starting with a letter or digit)", name)
	}
	rk, err := GenerateReaderKey()
	if err != nil {
		return nil, err
	}
	id := &Identity{Name: name, Reader: rk}
	if sshPublicLine != "" {
		if id.SignPub, err = ParseSSHPublicLine(sshPublicLine); err != nil {
			return nil, fmt.Errorf("ssh public key: %w", err)
		}
		id.SignVia = "ssh-agent"
		return id, nil
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	id.SignPub, id.SignVia, id.seed = pub, "file", priv.Seed()
	return id, nil
}

// Line is the public line for .pravo/access/readers.txt.
func (id *Identity) Line() string { return id.AsReader().String() }

// AsReader returns the identity as a Reader (the parsed form of its line).
func (id *Identity) AsReader() Reader {
	return Reader{Name: id.Name, Sign: id.SignPub, Key: id.Reader.Public()}
}

// Sign signs with the Ed25519 key: the seed in the identity file, or the ssh-agent for
// an identity made with --ssh.
func (id *Identity) Sign(msg []byte) ([]byte, error) {
	if id.seed == nil {
		if id.SignVia == "ssh-agent" {
			return id.signViaAgent(msg)
		}
		return nil, errors.New("protect: this identity holds no signing key")
	}
	return ed25519.Sign(ed25519.NewKeyFromSeed(id.seed), msg), nil
}

// Close clears the secret bytes the identity holds (best effort).
func (id *Identity) Close() {
	clear(id.seed)
	id.seed = nil
}

func (id *Identity) file() *identityFile {
	f := &identityFile{V: identityVersion, Name: id.Name}
	f.Signer.Alg, f.Signer.Pub, f.Signer.Via = sshEd25519, SSHBlob(id.SignPub), id.SignVia
	f.Reader.Pub = id.Reader.Public()
	return f
}

// MarshalUnsealed is the CI form: no passphrase, secrets in the clear. The caller
// writes it with WritePrivateFile (mode 0600).
func (id *Identity) MarshalUnsealed() ([]byte, error) {
	f := id.file()
	f.Signer.Seed = id.seed
	f.Reader.Key = id.Reader.Bytes()
	defer clear(f.Reader.Key)
	return json.MarshalIndent(f, "", "  ")
}

// MarshalSealed is the interactive form. The passphrase must be at least MinPassLen
// bytes; an empty one is what --ci is for.
func (id *Identity) MarshalSealed(passphrase []byte) ([]byte, error) {
	if len(passphrase) < MinPassLen {
		return nil, fmt.Errorf("passphrase must be at least %d characters (use --ci for an identity without one)", MinPassLen)
	}
	f := id.file()
	s := &passSeal{KDF: kdfName, Iter: defaultIter, Salt: randBytes(16)}
	secret := append(id.Reader.Bytes(), id.seed...)
	defer clear(secret)
	ct, err := s.crypt(passphrase, f, secret, nil)
	if err != nil {
		return nil, err
	}
	s.CT = ct
	f.Seal = s
	return json.MarshalIndent(f, "", "  ")
}

// aad binds the parameters and the public parts, so none can be changed without
// the passphrase.
func (s *passSeal) aad(f *identityFile) []byte {
	return pae([]byte("pravo/identity/v1"), []byte(f.Name), []byte(f.Reader.Pub.String()),
		[]byte(f.Signer.Alg), []byte(f.Signer.Pub), []byte(f.Signer.Via),
		[]byte(s.KDF), u64(uint64(s.Iter)), s.Salt)
}

// crypt seals secret (when ct == nil) or opens ct.
func (s *passSeal) crypt(pass []byte, f *identityFile, secret, ct []byte) ([]byte, error) {
	if s.KDF != kdfName {
		return nil, fmt.Errorf("identity uses kdf %q, which this pravo does not know", s.KDF)
	}
	if s.Iter < minIter || s.Iter > maxIter || len(s.Salt) < 16 {
		return nil, errors.New("identity file has unacceptable key derivation parameters")
	}
	// pbkdf2.Key takes the password as a string: a copy that cannot be cleared.
	key, err := pbkdf2.Key(sha256.New, string(pass), s.Salt, s.Iter, 32)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	blk, _ := aes.NewCipher(key)
	g, err := cipher.NewGCMWithRandomNonce(blk)
	if err != nil {
		return nil, err
	}
	if ct == nil {
		return g.Seal(nil, nil, secret, s.aad(f)), nil
	}
	pt, err := g.Open(nil, nil, ct, s.aad(f))
	if err != nil {
		return nil, errors.New("wrong passphrase, or the identity file is damaged")
	}
	return pt, nil
}

// ParseIdentity reads either form. For a sealed file it calls passphrase, once.
func ParseIdentity(data []byte, passphrase func() ([]byte, error)) (*Identity, error) {
	var f identityFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, errors.New("not an identity file")
	}
	if f.V != identityVersion {
		return nil, fmt.Errorf("identity file version %d: this pravo reads version %d", f.V, identityVersion)
	}
	if !nameRE.MatchString(f.Name) || f.Signer.Alg != sshEd25519 || f.Reader.Pub.IsZero() {
		return nil, errors.New("identity file is incomplete")
	}
	sign, err := ParseSSHBlob(f.Signer.Pub)
	if err != nil {
		return nil, fmt.Errorf("identity signing key: %w", err)
	}
	id := &Identity{Name: f.Name, SignPub: sign, SignVia: f.Signer.Via}
	var readerKey, seed []byte
	switch {
	case f.Seal != nil:
		if passphrase == nil {
			return nil, errors.New("identity is sealed with a passphrase and none can be asked for")
		}
		pass, err := passphrase()
		if err != nil {
			return nil, err
		}
		defer clear(pass)
		pt, err := f.Seal.crypt(pass, &f, nil, f.Seal.CT)
		if err != nil {
			return nil, err
		}
		defer clear(pt)
		if len(pt) != 32 && len(pt) != 64 {
			return nil, errors.New("identity file is damaged")
		}
		readerKey = append([]byte(nil), pt[:32]...)
		if len(pt) == 64 {
			seed = append([]byte(nil), pt[32:]...)
		}
	case len(f.Reader.Key) == 32:
		readerKey, seed = f.Reader.Key, f.Signer.Seed
	default:
		return nil, errors.New("identity file has no secret")
	}
	rk, err := ReaderKeyFromBytes(readerKey)
	clear(readerKey)
	if err != nil {
		return nil, err
	}
	if !rk.Public().Equal(f.Reader.Pub) {
		return nil, errors.New("identity file: reader key does not match its public key")
	}
	id.Reader = rk
	if len(seed) == ed25519.SeedSize {
		if !ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey).Equal(sign) {
			return nil, errors.New("identity file: signing seed does not match its public key")
		}
		id.seed = append([]byte(nil), seed...)
		clear(seed)
	}
	return id, nil
}
