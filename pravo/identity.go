package pravo

import "github.com/pravo-systems/pravo/internal/protect"

// ParseIdentity reads an identity file as "pravo id new" writes it, for
// Config.Identity. The form that "pravo id new --ci" writes holds its keys in
// the clear and needs no passphrase. For the form that a passphrase seals,
// passphrase is called once; a program that has none to give passes nil and
// gets an error for such a file. The identity holds a private key: close it
// when the workspace is closed.
func ParseIdentity(data []byte, passphrase func() ([]byte, error)) (*Identity, error) {
	return protect.ParseIdentity(data, passphrase)
}
