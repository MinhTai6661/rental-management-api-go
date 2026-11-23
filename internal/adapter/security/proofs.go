package security

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// proofInfo separates the key of proof links from every other use of the master key.
const proofInfo = "payment-proof-url/v1"

// ProofLinks makes and checks the signed, expiring links to the proof pictures of payments: a proof is private, so a
// link is the only way to read one, and only the two sides of a payment are given one. The signing key is derived from
// the master data key (HKDF-SHA256), never the key itself.
type ProofLinks struct {
	key     []byte
	baseURL string
}

// NewProofLinks derives the signing key from the 32-byte master key. baseURL is the public origin of the API
// ("https://api.example.vn"): the links are absolute.
func NewProofLinks(master []byte, baseURL string) (*ProofLinks, error) {
	if len(master) != 32 {
		return nil, fmt.Errorf("proof links: the master key must be 32 bytes, got %d", len(master))
	}
	key, err := hkdf.Key(sha256.New, master, nil, proofInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("proof links: derive key: %w", err)
	}
	return &ProofLinks{key: key, baseURL: strings.TrimRight(baseURL, "/")}, nil
}

// sign is the signature of a file of a payment that works until expires (unix seconds).
func (p *ProofLinks) sign(paymentID uuid.UUID, file string, expires int64) []byte {
	mac := hmac.New(sha256.New, p.key)
	mac.Write([]byte(paymentID.String() + "\n" + file + "\n" + strconv.FormatInt(expires, 10)))
	return mac.Sum(nil)
}

// Link returns the absolute address of a proof file of a payment that works until expires.
func (p *ProofLinks) Link(paymentID uuid.UUID, file string, expires time.Time) string {
	at := expires.Unix()
	q := url.Values{}
	q.Set("expires", strconv.FormatInt(at, 10))
	q.Set("signature", hex.EncodeToString(p.sign(paymentID, file, at)))
	return p.baseURL + "/v1/payments/" + paymentID.String() + "/proofs/" + url.PathEscape(file) + "?" + q.Encode()
}

// Valid says whether the signature belongs to the file of the payment and the link has not expired at now. A signature
// that is not 64 hex characters is simply false.
func (p *ProofLinks) Valid(paymentID uuid.UUID, file string, expires int64, signature string, now time.Time) bool {
	got, err := hex.DecodeString(signature)
	if err != nil || len(got) != sha256.Size {
		return false
	}
	return hmac.Equal(got, p.sign(paymentID, file, expires)) && expires > now.Unix()
}
