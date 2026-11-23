package propertyhttp

import "strings"

// mediaETag is the validator of a stored picture: its file name, quoted. A key is written once and never
// overwritten, so the name alone says whether the client's copy is current.
func mediaETag(key string) string {
	return `"` + key[strings.LastIndex(key, "/")+1:] + `"`
}

// etagMatches reports whether an If-None-Match header lists etag (or is "*"). The weak prefix is ignored: for
// If-None-Match a weak comparison is the one the specification asks for.
func etagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == "*" || candidate == etag {
			return true
		}
	}
	return false
}
