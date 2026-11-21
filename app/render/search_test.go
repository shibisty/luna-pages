package render

import (
	"strings"
	"testing"
)

// A search form (GET to a path of the site) keeps its inputs; other forms
// that are not grapesjs-forms' are unwrapped as before.
func TestSearchForm(t *testing.T) {
	cases := []struct{ in, want, not string }{
		{`<form class="s" action="/" method="get"><input type="search" name="q" value="{{query}}" placeholder="Search"><button type="submit">Search</button></form>`,
			`<form class="s" action="/" method="get"><input type="search" name="q" value="{{query}}" placeholder="Search"/><button type="submit">Search</button></form>`, ""},
		{`<form action="/search" method="GET"><input name="q"><input type="password" name="p"><input type="file" name="f"></form>`,
			`<form action="/search" method="get"><input name="q"/></form>`, "password"},
		{`<form action="/" method="post"><input name="q"></form>`, ``, "<input"},
		{`<form action="/"><input name="q"></form>`, ``, "<input"}, // no method: not said to be a search
		{`<form action="https://evil.example/" method="get"><input name="q"></form>`, ``, "<form"},
		{`<form action="//evil.example/"><input name="q"></form>`, ``, "<form"},
	}
	for _, c := range cases {
		out, err := SanitizeHTML(c.in, Policy{})
		if err != nil {
			t.Fatal(err)
		}
		if c.want != "" && !strings.Contains(out, c.want) {
			t.Errorf("%s:\n got %s\nwant %s", c.in, out, c.want)
		}
		if c.not != "" && strings.Contains(out, c.not) {
			t.Errorf("%s: %s kept in %s", c.in, c.not, out)
		}
	}
}
