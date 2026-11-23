// Package i18n holds the vi/en message catalog and picks the language of a request. Messages live in embedded JSON
// files (key -> text) and are rendered by go-i18n, so a text may use templates such as "{{.attribute}}".
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"

	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

// Supported languages. Vietnamese is the default.
const (
	Vietnamese = "vi"
	English    = "en"
)

// languages lists the supported languages, default first. The order matters for the language matcher.
var languages = []struct {
	code string
	tag  language.Tag
}{
	{Vietnamese, language.Vietnamese},
	{English, language.English},
}

//go:embed locales/*.json
var localeFiles embed.FS

// Catalog is the set of messages of every supported language, loaded once at start-up.
type Catalog struct {
	localizers map[string]*goi18n.Localizer // by language code
	tags       map[string]language.Tag
}

// New loads the embedded locale files.
func New() (*Catalog, error) {
	bundle := goi18n.NewBundle(language.Vietnamese)
	bundle.RegisterUnmarshalFunc("json", json.Unmarshal)
	c := &Catalog{localizers: map[string]*goi18n.Localizer{}, tags: map[string]language.Tag{}}
	for _, lang := range languages {
		if _, err := bundle.LoadMessageFileFS(localeFiles, "locales/"+lang.code+".json"); err != nil {
			return nil, fmt.Errorf("load %s locale: %w", lang.code, err)
		}
		c.localizers[lang.code] = goi18n.NewLocalizer(bundle, lang.code)
		c.tags[lang.code] = lang.tag
	}
	return c, nil
}

// Localizer returns the translator of one language; an unknown language falls back to Vietnamese.
func (c *Catalog) Localizer(locale string) Localizer {
	if _, ok := c.localizers[locale]; !ok {
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

// Has reports whether key exists in this language itself (a key found only in the fallback language does not count).
func (l Localizer) Has(key string) bool {
	_, ok := l.render(key, nil)
	return ok
}

// T returns the message of key with its {{.placeholders}} filled from args. A missing key is returned as is, so a
// gap shows up in the response instead of hiding.
func (l Localizer) T(key string, args map[string]string) string {
	if text, ok := l.render(key, args); ok {
		return text
	}
	return key
}

func (l Localizer) render(key string, args map[string]string) (string, bool) {
	if l.catalog == nil {
		return "", false
	}
	var data any
	if len(args) > 0 {
		data = args
	}
	text, tag, err := l.catalog.localizers[l.locale].LocalizeWithTag(&goi18n.LocalizeConfig{MessageID: key, TemplateData: data})
	if err != nil || tag != l.catalog.tags[l.locale] {
		return "", false
	}
	return text, true
}
