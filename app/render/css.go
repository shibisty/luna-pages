package render

import (
	"regexp"
	"strings"
)

// The CSS the editor sends (editor.getCss()) and style attributes are
// cleaned by removing what can run code or load from elsewhere unasked:
// expression(), behavior / -moz-binding, javascript: and vbscript: URLs,
// @import (except fonts from the allowed font hosts), and anything that
// would close a <style> element. Everything else is the site's design.

// FontHosts are the origins @import may load from.
var FontHosts = []string{"https://fonts.googleapis.com/", "https://fonts.bunny.net/", "https://use.typekit.net/"}

var (
	cssComment    = regexp.MustCompile(`(?s)/\*.*?\*/`)
	cssEscape     = regexp.MustCompile(`\\[0-9a-fA-F]{1,6}\s?|\\(.)`)
	cssExpression = regexp.MustCompile(`(?i)expression\s*\(`)
	cssBinding    = regexp.MustCompile(`(?i)(-moz-binding|behavior)\s*:`)
	cssURLStart   = regexp.MustCompile(`(?i)url\s*\(`)
	cssImport     = regexp.MustCompile(`(?i)@import\s+(?:url\s*\(\s*)?['"]?([^'")\s;]*)['"]?\s*\)?[^;]*;?`)
	cssCharset    = regexp.MustCompile(`(?i)@charset\s+[^;]*;`)
)

// SanitizeCSS cleans a stylesheet.
func SanitizeCSS(src string) string {
	s := cssComment.ReplaceAllString(src, "")
	// "</style" and "<!--" would end or confuse the <style> element the
	// site puts the CSS into.
	s = strings.NewReplacer("</", "<\\/", "<!--", "", "-->", "").Replace(s)
	s = cssCharset.ReplaceAllString(s, "")
	s = cssImport.ReplaceAllStringFunc(s, func(m string) string {
		u := cssImport.FindStringSubmatch(m)[1]
		for _, h := range FontHosts {
			if strings.HasPrefix(strings.ToLower(u), h) {
				return m
			}
		}
		return ""
	})
	return cleanDeclarations(s)
}

// SanitizeStyle cleans a style attribute.
func SanitizeStyle(src string) string {
	s := cssComment.ReplaceAllString(src, "")
	if strings.ContainsAny(s, "<>") || strings.Contains(strings.ToLower(s), "@import") {
		return ""
	}
	return strings.TrimSpace(cleanDeclarations(s))
}

func cleanDeclarations(s string) string {
	// Judge escaped text by what it means: "\65 xpression(" is expression(.
	probe := strings.ToLower(cssEscape.ReplaceAllStringFunc(s, unescapeCSS))
	if cssExpression.MatchString(probe) || cssBinding.MatchString(probe) {
		s = cssExpression.ReplaceAllString(cssEscape.ReplaceAllStringFunc(s, unescapeCSS), "x(")
		s = cssBinding.ReplaceAllString(s, "x-removed:")
	}
	return rewriteURLs(s)
}

// rewriteURLs empties every url(…) whose target is not a safe URL. The
// argument is read as CSS does: a quoted string, or up to the closing
// parenthesis (counting nested ones, which make it a bad URL anyway).
func rewriteURLs(s string) string {
	var b strings.Builder
	for {
		loc := cssURLStart.FindStringIndex(s)
		if loc == nil {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:loc[1]])
		rest := s[loc[1]:]
		arg, end := urlArg(rest)
		u := strings.TrimSpace(cssEscape.ReplaceAllStringFunc(arg, unescapeCSS))
		if q := len(u); q >= 2 && (u[0] == '"' || u[0] == '\'') && u[q-1] == u[0] {
			u = u[1 : q-1]
		}
		quoted := strings.HasPrefix(strings.TrimSpace(arg), `"`) || strings.HasPrefix(strings.TrimSpace(arg), `'`)
		lower := strings.ToLower(u)
		_, safe := cleanURL(u, true)
		switch {
		case !safe, !quoted && strings.Contains(arg, "("), // a bad URL token
			strings.HasPrefix(lower, "data:") && !strings.HasPrefix(lower, "data:image/"):
			b.WriteString(")")
		default:
			b.WriteString(arg + ")")
		}
		s = rest[end:]
	}
}

// urlArg is the argument of url( up to its closing parenthesis, and the
// index just past it.
func urlArg(s string) (string, int) {
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '\\':
			i++
		case c == '(':
			depth++
		case c == ')':
			if depth == 0 {
				return s[:i], i + 1
			}
			depth--
		}
	}
	return s, len(s)
}

func unescapeCSS(m string) string {
	if len(m) == 2 && m[0] == '\\' {
		return m[1:]
	}
	hex := strings.TrimSpace(m[1:])
	var r rune
	for _, c := range hex {
		r <<= 4
		switch {
		case c >= '0' && c <= '9':
			r |= c - '0'
		case c >= 'a' && c <= 'f':
			r |= c - 'a' + 10
		case c >= 'A' && c <= 'F':
			r |= c - 'A' + 10
		}
	}
	if r == 0 || r > 0x10FFFF {
		return ""
	}
	return string(r)
}
