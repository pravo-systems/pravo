package protect

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// A readers line is "NAME ssh-ed25519 BASE64 x25519:BASE64URL", four fields, and
// a fifth, "role:reader", "role:writer" or "role:admin", when the repository gives
// roles (see Role). This is the whole of .pravo/access/readers.txt:
//
//	ada ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIQx7mT… x25519:7Hc3Kp…
//
// Blank lines and lines that start with # are skipped. A # that follows the four
// fields starts a comment. CRLF, tabs, repeated spaces and a UTF-8 byte order
// mark (what Windows editors add) are accepted. A key comment such as
// "ada@laptop" in the SSH field is NOT accepted, because it would make the line
// five fields and `pravo id new` never prints one.

// Reader is one parsed line.
type Reader struct {
	Name string
	Sign ed25519.PublicKey // the signing key; unused for sealing in step 2
	Key  ReaderPublic      // the key data keys are locked for
	Line int               // 1-based line number, 0 if not from a file
	// Role is what the key may do. It is RoleNone when the line has no role field.
	Role Role
}

// Limits keep a hostile or damaged file from costing anything.
const (
	maxReadersFile = 1 << 20
	maxReaderLine  = 4096
	maxReaders     = 200
)

var digestRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ParseReaderLine parses one line, without its newline.
func ParseReaderLine(line string) (Reader, error) {
	body := line
	if i := strings.Index(body, "#"); i >= 0 {
		body = body[:i]
	}
	f := strings.Fields(body)
	if len(f) != 4 && len(f) != 5 {
		return Reader{}, fmt.Errorf("want 4 fields: NAME ssh-ed25519 BASE64 x25519:BASE64URL (got %d)", len(f))
	}
	role := RoleNone
	if len(f) == 5 {
		var err error
		if role, err = ParseRole(f[4]); err != nil {
			return Reader{}, err
		}
	}
	if !nameRE.MatchString(f[0]) {
		return Reader{}, fmt.Errorf("name %q: use letters, digits, . _ - (at most 64, starting with a letter or digit)", f[0])
	}
	if strings.EqualFold(f[0], RecoveryName) {
		return Reader{}, fmt.Errorf("name %q is reserved for the recovery recipient of a state", f[0])
	}
	if f[1] != sshEd25519 {
		return Reader{}, fmt.Errorf("signing key type %q: want ssh-ed25519", f[1])
	}
	sign, err := ParseSSHBlob(f[2])
	if err != nil {
		return Reader{}, fmt.Errorf("signing key: %w", err)
	}
	key, err := ParsePublic(f[3])
	if err != nil {
		return Reader{}, err
	}
	return Reader{Name: f[0], Sign: sign, Key: key, Role: role}, nil
}

// RolesGenesisKey is the first field of the line that pins the first version of the roles
// stored in the state: "roles-genesis DIGEST", the SHA-256 of its text in hex. A key that is
// named like this and has two fields is the pin and not a reader.
const RolesGenesisKey = "roles-genesis"

// ParseReaders parses a whole file. It stops at the first bad line and names it.
// The result is in file order. It fails on a repeated name (case-insensitive) or
// a repeated reader key, which are almost always a paste error.
func ParseReaders(r io.Reader) ([]Reader, error) {
	rs, _, err := ParseReadersPin(r)
	return rs, err
}

// ParseReadersPin is ParseReaders for a file that may hold a pin of the roles in the state:
// the line "roles-genesis DIGEST". pin is the digest, empty without the line.
func ParseReadersPin(r io.Reader) (rs []Reader, pin string, err error) {
	data, err := io.ReadAll(io.LimitReader(r, maxReadersFile+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxReadersFile {
		return nil, "", fmt.Errorf("readers file is larger than %d bytes", maxReadersFile)
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, "", errors.New("readers file contains a NUL byte")
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 4096), maxReaderLine+2)
	var out []Reader
	names := map[string]int{}
	keys := map[string]int{}
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimRight(sc.Text(), "\r")
		if t := strings.TrimSpace(line); t == "" || t[0] == '#' {
			continue
		}
		if f := strings.Fields(strings.SplitN(line, "#", 2)[0]); len(f) == 2 && f[0] == RolesGenesisKey {
			if pin != "" || !digestRE.MatchString(f[1]) {
				return nil, "", fmt.Errorf("readers file, line %d: want one %s line with the SHA-256 of the first roles in hex", n, RolesGenesisKey)
			}
			pin = f[1]
			continue
		}
		rd, err := ParseReaderLine(line)
		if err != nil {
			return nil, "", fmt.Errorf("readers file, line %d: %w", n, err)
		}
		rd.Line = n
		if p, dup := names[strings.ToLower(rd.Name)]; dup {
			return nil, "", fmt.Errorf("readers file, line %d: name %q is already used on line %d", n, rd.Name, p)
		}
		if p, dup := keys[rd.Key.String()]; dup {
			return nil, "", fmt.Errorf("readers file, line %d: reader key of %q is already listed on line %d", n, rd.Name, p)
		}
		names[strings.ToLower(rd.Name)] = n
		keys[rd.Key.String()] = n
		if out = append(out, rd); len(out) > maxReaders {
			return nil, "", fmt.Errorf("readers file, line %d: more than %d readers", n, maxReaders)
		}
	}
	if err := sc.Err(); err != nil { // includes a line over maxReaderLine
		return nil, "", fmt.Errorf("readers file: %w", err)
	}
	return out, pin, nil
}

// String is the canonical line, with no comment.
func (r Reader) String() string {
	line := r.Name + " " + sshEd25519 + " " + SSHBlob(r.Sign) + " " + r.Key.String()
	if r.Role != RoleNone {
		line += " role:" + r.Role.String()
	}
	return line
}

// FormatReaders writes the canonical file: one line per reader, in the order given.
func FormatReaders(rs []Reader) []byte {
	var b bytes.Buffer
	for _, r := range rs {
		b.WriteString(r.String())
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// SetID is a digest of the set of readers that does not depend on line order,
// comments or spacing. `protect status` compares the digest of the readers file
// with the one recorded in the store ("readers.txt matches the store").
// It covers the name, signing key and reader key of each reader, and not the role:
// the role is read from the repository and the state holds none.
func SetID(rs []Reader) string {
	sorted := append([]Reader(nil), rs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	parts := [][]byte{[]byte("pravo/readers/v1")}
	for _, r := range sorted {
		parts = append(parts, []byte(r.Name), r.Sign, r.Key.Bytes())
	}
	sum := sha256.Sum256(pae(parts...))
	return hex.EncodeToString(sum[:])
}
