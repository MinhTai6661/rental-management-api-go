package service

import "github.com/google/uuid"

// deref returns the text a pointer points at, or "" for nil.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}
