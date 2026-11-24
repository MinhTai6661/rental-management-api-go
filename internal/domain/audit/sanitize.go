package audit

import (
	"encoding/json"
	"strings"
)

// MaxPayloadBytes is the largest JSON a changes or meta payload may have; a bigger one is replaced by a stub.
const MaxPayloadBytes = 8192

const redacted = "[redacted]"

// secretKeys are removed from every payload: they must never reach the trail.
var secretKeys = map[string]bool{
	"password": true, "password_confirmation": true, "token": true, "access_token": true, "refresh_token": true,
	"secret": true, "authorization": true,
}

// maskedKeys keep only their last four characters.
var maskedKeys = map[string]bool{"account_number": true, "bank_account": true}

// Sanitize makes a payload safe to store: values under secret keys are redacted, account numbers keep their last four
// characters, and a payload over MaxPayloadBytes of JSON becomes {"truncated": true, "bytes": N}. Keys are compared
// without regard to case at every depth. Any value that marshals to a JSON object is accepted; nil and empty payloads
// give nil. The input is never changed.
func Sanitize(payload any) map[string]any {
	obj := normalize(payload)
	if len(obj) == 0 {
		return nil
	}
	clean, _ := scrub(obj, false).(map[string]any)
	raw, err := json.Marshal(clean)
	if err != nil {
		return map[string]any{"truncated": true, "bytes": 0}
	}
	if len(raw) > MaxPayloadBytes {
		return map[string]any{"truncated": true, "bytes": len(raw)}
	}
	return clean
}

// normalize turns any value into plain JSON data (maps, slices, strings, numbers), which is also a deep copy.
func normalize(v any) map[string]any {
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

// scrub walks the data; mask says an ancestor key asked for masking, which applies to everything below it.
func scrub(v any, mask bool) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			key := strings.ToLower(k)
			switch {
			case secretKeys[key]:
				out[k] = redacted
			default:
				out[k] = scrub(val, mask || maskedKeys[key])
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = scrub(val, mask)
		}
		return out
	case nil:
		return nil
	default:
		if !mask {
			return v
		}
		return maskValue(v)
	}
}

// maskValue keeps the last four characters of a value.
func maskValue(v any) string {
	s := ""
	switch x := v.(type) {
	case string:
		s = x
	case float64:
		s = strings.TrimRight(strings.TrimRight(formatNumber(x), "0"), ".")
	default:
		raw, _ := json.Marshal(x)
		s = string(raw)
	}
	runes := []rune(s)
	if len(runes) > 4 {
		runes = runes[len(runes)-4:]
	}
	return "****" + string(runes)
}

func formatNumber(f float64) string {
	raw, _ := json.Marshal(f)
	return string(raw)
}
