package payment

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ProofKeyPrefix is the start of every storage key this context writes. The files are private: the public picture
// route serves only the keys of the property context, and a proof is reached through a signed link.
const ProofKeyPrefix = "payments/"

// NewProofKey is the storage key of a proof picture: the month it was sent and a random name, with the extension of
// what is stored ("webp" or "jpg"). The key is never reused, so a link made from it never shows another file.
func NewProofKey(now time.Time, id uuid.UUID, ext string) string {
	return fmt.Sprintf("%s%04d/%02d/%x.%s", ProofKeyPrefix, now.Year(), int(now.Month()), id[:], ext)
}

// ThumbKey is the storage key of the small copy (WebP) of a proof picture, next to it.
func ThumbKey(key string) string { return thumbBase(key) + "_thumb.webp" }

// LegacyThumbKey is where the small copy of a proof stored before thumbnails became WebP is (a JPEG).
func LegacyThumbKey(key string) string { return thumbBase(key) + "_thumb.jpg" }

func thumbBase(key string) string {
	if i := strings.LastIndexByte(key, '.'); i >= 0 {
		return key[:i]
	}
	return key
}

var proofFilePattern = regexp.MustCompile(`^([0-9]{1,3})(_thumb)?\.webp$`)

// ProofFileName is the name a proof has in a link: its place in the payment, ".webp", and "_thumb" for the small copy.
func ProofFileName(index int, thumb bool) string {
	if thumb {
		return strconv.Itoa(index) + "_thumb.webp"
	}
	return strconv.Itoa(index) + ".webp"
}

// ParseProofFile reads the name of a proof in a link.
func ParseProofFile(name string) (index int, thumb, ok bool) {
	m := proofFilePattern.FindStringSubmatch(name)
	if m == nil {
		return 0, false, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false, false
	}
	return n, m[2] != "", true
}
