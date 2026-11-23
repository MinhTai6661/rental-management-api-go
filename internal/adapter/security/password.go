// Package security holds the cryptography behind the identity ports: argon2id password hashes, JWT access
// tokens and the opaque random tokens. It is the only code that imports golang.org/x/crypto and the JWT library.
package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (the OWASP minimum for argon2id: 19 MiB of memory, 2 passes, 1 lane).
const (
	argonMemoryKiB = 19456
	argonTime      = 2
	argonThreads   = 1
	argonSaltBytes = 16
	argonKeyBytes  = 32

	// Bounds for the parameters read back from a stored hash: a corrupt row must not make a login allocate gigabytes.
	argonMaxMemoryKiB = 1 << 20
	argonMaxTime      = 16
	argonMaxThreads   = 16
)

// errBadHash is returned for a stored hash that is not in the format HashPassword writes.
var errBadHash = errors.New("password hash is not in argon2id PHC format")

// PasswordHasher is the argon2id implementation of the password hasher port.
type PasswordHasher struct{}

// Hash returns the argon2id hash of the password.
func (PasswordHasher) Hash(password string) (string, error) { return HashPassword(password) }

// Verify reports whether the password matches the stored hash.
func (PasswordHasher) Verify(password, encoded string) (bool, error) {
	return VerifyPassword(password, encoded)
}

// HashPassword returns the argon2id hash of the password in PHC format:
// $argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("make password salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyBytes)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword reports whether the password matches the stored hash. The comparison takes the same time
// whatever the first differing byte is. An error means the stored hash is unusable (not that the password is wrong).
func VerifyPassword(password, encoded string) (bool, error) {
	p, err := parseHash(encoded)
	if err != nil {
		return false, err
	}
	key := argon2.IDKey([]byte(password), p.salt, p.time, p.memory, p.threads, uint32(len(p.key))) //nolint:gosec // key is 32 bytes
	return subtle.ConstantTimeCompare(key, p.key) == 1, nil
}

type phcHash struct {
	memory  uint32
	time    uint32
	threads uint8
	salt    []byte
	key     []byte
}

// parseHash reads a PHC string: "$argon2id$v=19$m=..,t=..,p=..$salt$hash".
func parseHash(encoded string) (phcHash, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return phcHash{}, errBadHash
	}
	if parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return phcHash{}, fmt.Errorf("%w: unsupported version %q", errBadHash, parts[2])
	}

	var p phcHash
	for _, kv := range strings.Split(parts[3], ",") {
		name, value, ok := strings.Cut(kv, "=")
		n, err := strconv.ParseUint(value, 10, 32)
		if !ok || err != nil {
			return phcHash{}, errBadHash
		}
		switch name {
		case "m":
			p.memory = uint32(n)
		case "t":
			p.time = uint32(n)
		case "p":
			if n > argonMaxThreads {
				return phcHash{}, fmt.Errorf("%w: too many lanes", errBadHash)
			}
			p.threads = uint8(n) //nolint:gosec // bounded above
		default:
			return phcHash{}, errBadHash
		}
	}
	if p.memory == 0 || p.memory > argonMaxMemoryKiB || p.time == 0 || p.time > argonMaxTime || p.threads == 0 {
		return phcHash{}, fmt.Errorf("%w: parameters out of range", errBadHash)
	}

	b64 := base64.RawStdEncoding
	var err error
	if p.salt, err = b64.DecodeString(parts[4]); err != nil || len(p.salt) == 0 {
		return phcHash{}, errBadHash
	}
	if p.key, err = b64.DecodeString(parts[5]); err != nil || len(p.key) == 0 {
		return phcHash{}, errBadHash
	}
	return p, nil
}
