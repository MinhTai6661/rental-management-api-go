package i18n

import "golang.org/x/text/language"

// matcher picks among the supported languages. It is built once and never changed.
var matcher = newMatcher()

func newMatcher() language.Matcher {
	tags := make([]language.Tag, len(languages))
	for i, lang := range languages {
		tags[i] = lang.tag
	}
	return language.NewMatcher(tags)
}

// Negotiate picks the language of a request from its Accept-Language header: the supported language the client
// prefers most (by quality value, then header order); a region tag such as "en-US" falls back to its base "en".
// No header, or no supported language in it, gives Vietnamese.
func Negotiate(acceptLanguage string) string {
	_, index := language.MatchStrings(matcher, acceptLanguage)
	return languages[index].code
}
