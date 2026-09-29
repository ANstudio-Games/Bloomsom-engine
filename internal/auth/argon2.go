package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argon2Time    = 1
	argon2Memory  = 64 * 1024 // 64 MB = 65536 KB
	argon2Threads = 4
	argon2KeyLen  = 32
	saltLen       = 16
)

// ErrInvalidHash indicates that an encoded hash string does not match the expected PHC format.
var ErrInvalidHash = errors.New("auth: invalid hash format")

// HashPassword generates a 16-byte random salt and hashes the password using Argon2id
// with time=1, memory=64MB, threads=4, keyLen=32. It returns a standard PHC-formatted string:
// $argon2id$v=19$m=65536,t=1,p=4$<base64-salt>$<base64-hash>
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", fmt.Errorf("auth: generate salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)

	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)

	encoded := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argon2Memory, argon2Time, argon2Threads, b64Salt, b64Hash)

	return encoded, nil
}

// VerifyPassword parses a PHC-formatted argon2id hash string, extracts the parameters and salt,
// computes the argon2id hash for the given password, and compares it in constant time.
func VerifyPassword(password, encodedHash string) (bool, error) {
	parts := strings.Split(encodedHash, "$")
	// Standard PHC string starts with "$", so parts[0] is ""
	// Expect: ["", "argon2id", "v=19", "m=65536,t=1,p=4", "<salt>", "<hash>"]
	if len(parts) != 6 || parts[0] != "" {
		return false, ErrInvalidHash
	}

	if parts[1] != "argon2id" {
		return false, fmt.Errorf("%w: unsupported algorithm %q", ErrInvalidHash, parts[1])
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("%w: invalid version string %q", ErrInvalidHash, parts[2])
	}
	if version != argon2.Version {
		return false, fmt.Errorf("%w: incompatible argon2 version %d", ErrInvalidHash, version)
	}

	var memory, timeCost uint32
	var threads uint8
	params := strings.Split(parts[3], ",")
	for _, param := range params {
		kv := strings.SplitN(param, "=", 2)
		if len(kv) != 2 {
			return false, fmt.Errorf("%w: invalid parameter %q", ErrInvalidHash, param)
		}
		val, err := strconv.ParseUint(kv[1], 10, 32)
		if err != nil {
			return false, fmt.Errorf("%w: invalid parameter value %q", ErrInvalidHash, kv[1])
		}
		switch kv[0] {
		case "m":
			memory = uint32(val)
		case "t":
			timeCost = uint32(val)
		case "p":
			if val > 255 {
				return false, fmt.Errorf("%w: thread count out of range %d", ErrInvalidHash, val)
			}
			threads = uint8(val)
		default:
			return false, fmt.Errorf("%w: unknown parameter %q", ErrInvalidHash, kv[0])
		}
	}

	if memory == 0 || timeCost == 0 || threads == 0 {
		return false, fmt.Errorf("%w: missing required parameters", ErrInvalidHash)
	}

	salt, err := decodeBase64(parts[4])
	if err != nil {
		return false, fmt.Errorf("%w: invalid salt base64: %w", ErrInvalidHash, err)
	}

	expectedHash, err := decodeBase64(parts[5])
	if err != nil {
		return false, fmt.Errorf("%w: invalid hash base64: %w", ErrInvalidHash, err)
	}

	keyLen := uint32(len(expectedHash))
	if keyLen == 0 {
		return false, fmt.Errorf("%w: empty hash", ErrInvalidHash)
	}

	computedHash := argon2.IDKey([]byte(password), salt, timeCost, memory, threads, keyLen)

	if subtle.ConstantTimeCompare(computedHash, expectedHash) == 1 {
		return true, nil
	}
	return false, nil
}

func decodeBase64(s string) ([]byte, error) {
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.StdEncoding.DecodeString(s)
}
