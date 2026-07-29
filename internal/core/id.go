package core

import (
	"crypto/rand"
	"fmt"
)

const (
	idAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	idLength   = 6 // characters in a session ID
)

// NewID returns a 6-char lowercase [a-z0-9] ID from crypto/rand.
// Bytes >= 252 (the largest multiple of 36 below 256) are rejected so
// every character is uniform. Callers retry on DB collision.
func NewID() (string, error) {
	const (
		limit       = 252 // largest multiple of len(idAlphabet) below 256
		randBufSize = 16  // bytes drawn per rand.Read
	)
	id := make([]byte, 0, idLength)
	buf := make([]byte, randBufSize)
	for len(id) < idLength {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("generate id: %w", err)
		}
		for _, b := range buf {
			if b < limit && len(id) < idLength {
				id = append(id, idAlphabet[int(b)%len(idAlphabet)])
			}
		}
	}
	return string(id), nil
}

// SuffixName returns base if unused, else base-2, base-3, ...
// taken() reports whether a candidate collides with a live session.
func SuffixName(base string, taken func(string) bool) string {
	if !taken(base) {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !taken(candidate) {
			return candidate
		}
	}
}
