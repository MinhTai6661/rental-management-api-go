package invoice

import (
	"fmt"
	"strings"
	"unicode"
)

// The transfer text of a bill: the words people read (the label) and a random tail that keeps the whole unique.
const (
	// MaxLabelLength is how long the words may be: a bank's transfer text takes plain characters and the QR code at
	// most 25 characters in all.
	MaxLabelLength = 14
	// TailLength is the length of the random tail.
	TailLength = 5
	// TailAlphabet has no look-alikes (no O, I, L, 0 or 1).
	TailAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	// MaxCodeLength is the longest whole transfer text: the words, a space and the tail.
	MaxCodeLength = MaxLabelLength + 1 + TailLength
)

// ValidLabel says whether the words are plain characters (letters, digits and ` ._-/`) and at most MaxLabelLength.
func ValidLabel(s string) bool {
	if s == "" || len(s) > MaxLabelLength {
		return false
	}
	for _, r := range s {
		ok := r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(" ._-/", r))
		if !ok {
			return false
		}
	}
	return true
}

// DefaultLabel writes the words of a transfer text nobody wrote: "HD T11-P101" (month and room). They stay within
// what the text allows, so the room's name loses its accents and anything else, and is cut to the room left; a name
// with nothing usable falls back to the number of the bill in its month.
func DefaultLabel(month int, room string, number int) string {
	prefix := fmt.Sprintf("HD T%02d-", month)
	tail := plain(room)
	if tail == "" {
		tail = fmt.Sprintf("%03d", number)
	}
	label := prefix + tail
	if len(label) > MaxLabelLength {
		label = label[:MaxLabelLength]
	}
	return label
}

// vietnamese groups the letters with marks by the plain letter they stand for.
var vietnamese = [...]struct {
	plain    rune
	variants string
}{
	{'a', "àáạảãâầấậẩẫăằắặẳẵ"}, {'e', "èéẹẻẽêềếệểễ"}, {'i', "ìíịỉĩ"}, {'o', "òóọỏõôồốộổỗơờớợởỡ"},
	{'u', "ùúụủũưừứựửữ"}, {'y', "ỳýỵỷỹ"}, {'d', "đ"},
}

// fold turns a letter with a Vietnamese mark into its plain letter (capitals too); anything else stays as it is.
func fold(r rune) rune {
	lower := unicode.ToLower(r)
	for _, g := range vietnamese {
		if strings.ContainsRune(g.variants, lower) {
			if lower != r {
				return unicode.ToUpper(g.plain)
			}
			return g.plain
		}
	}
	return r
}

// plain is the name in capital ASCII letters, digits and ._-/ only (accents are dropped, Đ becomes D).
func plain(name string) string {
	var b strings.Builder
	for _, r := range name {
		r = unicode.ToUpper(fold(r))
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._-/", r)) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ComposeCode is the whole transfer text: the words, a space and the tail.
func ComposeCode(label, tail string) string { return label + " " + tail }

// TailOf returns the random tail of a transfer text (ok is false when it has none), so a bill keeps its tail when only
// its words change.
func TailOf(code string) (string, bool) {
	i := strings.LastIndexByte(code, ' ')
	if i < 0 || len(code)-i-1 != TailLength {
		return "", false
	}
	tail := code[i+1:]
	for _, r := range tail {
		if !strings.ContainsRune(TailAlphabet, r) {
			return "", false
		}
	}
	return tail, true
}
