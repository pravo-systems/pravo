// Package shamir implements Shamir secret sharing over GF(2^8) (AES polynomial 0x11b).
package shamir

import (
	"crypto/rand"
	"errors"
)

var exp, log [256]byte

func init() { // generator 3
	x := byte(1)
	for i := range 255 {
		exp[i] = x
		log[x] = byte(i)
		x ^= xtime(x) // x*3
	}
}
func xtime(a byte) byte {
	if a&0x80 != 0 {
		return a<<1 ^ 0x1b
	}
	return a << 1
}
func mul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return exp[(int(log[a])+int(log[b]))%255]
}
func div(a, b byte) byte { // b != 0
	if a == 0 {
		return 0
	}
	return exp[(int(log[a])-int(log[b])+255)%255]
}

// Split makes n shares, any k of which recover secret. A share is x(1) || y(len(secret)).
func Split(secret []byte, k, n int) ([][]byte, error) {
	if k < 2 || n < k || n > 255 || len(secret) == 0 {
		return nil, errors.New("shamir: need 2 <= k <= n <= 255")
	}
	shares := make([][]byte, n)
	for i := range shares {
		shares[i] = make([]byte, 1+len(secret))
		shares[i][0] = byte(i + 1)
	}
	coef := make([]byte, k)
	for pos, s := range secret {
		coef[0] = s
		rand.Read(coef[1:])
		for i := range shares {
			x := shares[i][0]
			y := byte(0)
			for j := k - 1; j >= 0; j-- { // Horner
				y = mul(y, x) ^ coef[j]
			}
			shares[i][1+pos] = y
		}
	}
	return shares, nil
}

// Combine interpolates at x=0 with Lagrange. It cannot tell whether the shares are right.
func Combine(shares [][]byte) ([]byte, error) {
	if len(shares) < 2 {
		return nil, errors.New("shamir: need shares")
	}
	l := len(shares[0])
	seen := map[byte]bool{}
	for _, s := range shares {
		if len(s) != l || seen[s[0]] || s[0] == 0 {
			return nil, errors.New("shamir: bad or duplicate share")
		}
		seen[s[0]] = true
	}
	out := make([]byte, l-1)
	for pos := range out {
		var acc byte
		for i, si := range shares {
			num, den := byte(1), byte(1)
			for j, sj := range shares {
				if i == j {
					continue
				}
				num = mul(num, sj[0])
				den = mul(den, si[0]^sj[0])
			}
			acc ^= mul(si[1+pos], div(num, den))
		}
		out[pos] = acc
	}
	return out, nil
}
