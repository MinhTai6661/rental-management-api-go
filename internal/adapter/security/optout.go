package security

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// optOutInfo separates the key of opt-out links from every other use of the master key.
const optOutInfo = "invitation-opt-out/v1"

// OptOutSigner signs the address in the "do not mail me invitations" link of an invitation email, so only whoever
// received the email can use it. The key is derived from the master data key (HKDF-SHA256), never the key itself.
type OptOutSigner struct{ key []byte }

// NewOptOutSigner derives the signing key from the 32-byte master key.
func NewOptOutSigner(master []byte) (*OptOutSigner, error) {
	if len(master) != 32 {
		return nil, fmt.Errorf("opt-out signer: the master key must be 32 bytes, got %d", len(master))
	}
	key, err := hkdf.Key(sha256.New, master, nil, optOutInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("opt-out signer: derive key: %w", err)
	}
	return &OptOutSigner{key: key}, nil
}

// Sign returns the token (hex HMAC-SHA256, 64 characters) of a normalized address.
func (s *OptOutSigner) Sign(normalizedEmail string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(normalizedEmail))
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify says whether the token is the one Sign gives for the address. Anything that is not 64 hex characters is
// simply false.
func (s *OptOutSigner) Verify(normalizedEmail, token string) bool {
	got, err := hex.DecodeString(token)
	if err != nil || len(got) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(normalizedEmail))
	return hmac.Equal(got, mac.Sum(nil))
}
