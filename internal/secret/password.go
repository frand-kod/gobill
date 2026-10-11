package secret

import "crypto/rand"

// RandomPassword returns n characters from a set without look-alikes (no 0/O, 1/l/I), for passwords
// a person has to read off a screen or type.
func RandomPassword(n int) string {
	const chars = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	rand.Read(b) // never returns an error in Go 1.24+
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)] // slight modulo bias is fine for 54 chars
	}
	return string(b)
}
