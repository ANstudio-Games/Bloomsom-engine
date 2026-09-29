package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

// GenerateToken generates 32 cryptographically random bytes, returning them hex-encoded
// as rawToken (64 chars), and computes the SHA-256 hash of rawToken as tokenHash.
func GenerateToken() (rawToken string, tokenHash string, err error) {
	var b [32]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return "", "", fmt.Errorf("auth: generate token: %w", err)
	}
	rawToken = hex.EncodeToString(b[:])
	tokenHash = HashToken(rawToken)
	return rawToken, tokenHash, nil
}

// HashToken computes the SHA-256 hex string of rawToken.
func HashToken(rawToken string) string {
	h := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(h[:])
}
