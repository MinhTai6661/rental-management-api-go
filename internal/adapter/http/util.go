package httpadapter

import (
	"strings"
	"time"
)

// blankToNil turns an empty or whitespace-only optional text into "not given".
func blankToNil(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

func retryAfterSeconds(d time.Duration) int {
	if d <= 0 {
		return 1
	}
	return int((d + time.Second - 1) / time.Second)
}
