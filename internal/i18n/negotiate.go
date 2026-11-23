package i18n

import (
	"sort"
	"strconv"
	"strings"
)

// Negotiate picks the language of a request from its Accept-Language header: the supported language the client
// prefers most (by quality value, then header order); a region tag such as "en-US" falls back to its base "en".
// No header, or no supported language in it, gives Vietnamese.
func Negotiate(acceptLanguage string) string {
	supported := []string{Vietnamese, English}
	preferred := parseAcceptLanguage(acceptLanguage)
	if len(preferred) == 0 {
		return supported[0]
	}
	// Each "xx_YY" is also tried as plain "xx", right after itself, unless "xx" is listed on its own.
	listed := make(map[string]bool, len(preferred))
	for _, lang := range preferred {
		listed[lang] = true
	}
	var extended []string
	for _, lang := range preferred {
		extended = append(extended, lang)
		if base, _, found := strings.Cut(lang, "_"); found && !listed[base] {
			extended = append(extended, base)
		}
	}
	for _, lang := range extended {
		for _, s := range supported {
			if lang == s {
				return s
			}
		}
	}
	return supported[0]
}

// parseAcceptLanguage returns the languages of the header, best quality first (header order breaks ties),
// normalised so that "en-us" becomes "en_US".
func parseAcceptLanguage(header string) []string {
	type entry struct {
		lang    string
		quality float64
	}
	var entries []entry
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(part, ";")
		lang := strings.TrimSpace(fields[0])
		if lang == "" {
			continue
		}
		quality := 1.0
		for _, param := range fields[1:] {
			if k, v, ok := strings.Cut(strings.TrimSpace(param), "="); ok && strings.TrimSpace(k) == "q" {
				if q, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					quality = q
				}
			}
		}
		entries = append(entries, entry{normalise(lang), quality})
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].quality > entries[j].quality })
	langs := make([]string, len(entries))
	for i, e := range entries {
		langs[i] = e.lang
	}
	return langs
}

func normalise(lang string) string {
	codes := strings.Split(lang, "-")
	if len(codes) == 1 {
		return lang
	}
	if codes[0] == "i" { // grandfathered tags such as "i-klingon"
		return codes[1]
	}
	out := strings.ToLower(codes[0])
	for _, c := range codes[1:] {
		out += "_" + strings.ToUpper(c)
	}
	return out
}
