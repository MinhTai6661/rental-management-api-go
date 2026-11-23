// Package i18n holds the vi/en message catalog and picks the language of a request.
// Messages use :placeholder syntax, e.g. "The :attribute field is required.".
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Supported languages. Vietnamese is the default.
const (
	Vietnamese = "vi"
	English    = "en"
)

//go:embed locales/*.json
var localeFiles embed.FS

// Catalog is the set of messages of every supported language, loaded once at start-up.
type Catalog struct {
	messages map[string]map[string]string // locale -> key -> text
}

// New loads the embedded locale files.
func New() (*Catalog, error) {
	c := &Catalog{messages: map[string]map[string]string{}}
	for _, locale := range []string{Vietnamese, English} {
		raw, err := localeFiles.ReadFile("locales/" + locale + ".json")
		if err != nil {
			return nil, fmt.Errorf("read %s locale: %w", locale, err)
		}
		var msgs map[string]string
		if err := json.Unmarshal(raw, &msgs); err != nil {
			return nil, fmt.Errorf("parse %s locale: %w", locale, err)
		}
		c.messages[locale] = msgs
	}
	return c, nil
}

// Localizer returns the translator of one language; an unknown language falls back to Vietnamese.
func (c *Catalog) Localizer(locale string) Localizer {
	if _, ok := c.messages[locale]; !ok {
		locale = Vietnamese
	}
	return Localizer{catalog: c, locale: locale}
}

// Localizer translates keys into one language.
type Localizer struct {
	catalog *Catalog
	locale  string
}

// Locale is the language code this localizer speaks.
func (l Localizer) Locale() string { return l.locale }

// Has reports whether key exists in this language.
func (l Localizer) Has(key string) bool {
	if l.catalog == nil {
		return false
	}
	_, ok := l.catalog.messages[l.locale][key]
	return ok
}

// T returns the message of key with :placeholders replaced from args. A missing key is returned as is, so a gap shows up in the response instead of hiding.
func (l Localizer) T(key string, args map[string]string) string {
	if !l.Has(key) {
		return key
	}
	return replace(l.catalog.messages[l.locale][key], args)
}

// replace swaps ":name" for args["name"], longest names first so ":attributes" is not clipped by ":attribute".
func replace(text string, args map[string]string) string {
	if len(args) == 0 {
		return text
	}
	names := make([]string, 0, len(args))
	for name := range args {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	pairs := make([]string, 0, len(names)*2)
	for _, name := range names {
		pairs = append(pairs, ":"+name, args[name])
	}
	return strings.NewReplacer(pairs...).Replace(text)
}
