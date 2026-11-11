package tests

import (
	"strings"
	"testing"

	apptest "fullstack-testing"
	"luna-keys"
)

const (
	catalogID = "aaaaaaaa-0000-4000-8000-000000000005"
	grinderID = "aaaaaaaa-0000-4000-8000-000000000006"
	kettleID  = "aaaaaaaa-0000-4000-8000-000000000007"
)

var productFields = []map[string]any{
	{"name": "title", "type": "text", "required": true},
	{"name": "photo", "type": "image"},
	{"name": "price", "type": "number", "format": "0.00"},
	{"name": "sale", "type": "boolean"},
	{"name": "specs", "type": "list", "fields": []map[string]any{{"name": "name", "type": "text", "required": true}, {"name": "value", "type": "text"}}},
}

const productHTML = `<article class="product">` +
	`<h1 data-field="title">Product name</h1>` +
	`<img data-field="photo" src="/placeholder.png" onerror="steal()">` +
	`<p>Price: <span data-field="price">0.00</span></p>` +
	`<span data-field="sale" class="badge">Sale</span>` +
	`<ul data-field="specs"><li><b data-field="name">Name</b>: <i data-field="value">Value</i></li></ul>` +
	`<a href="{{entry.url}}" title="{{ title }}">{{entry.name}}: {{price}}</a>` +
	`<script>alert(1)</script></article>`

// htmlHas checks the rendered HTML (the body escapes < as \u003c).
func htmlHas(t *testing.T, r *apptest.Response, want []string, not ...string) {
	t.Helper()
	v, _ := r.JSONPath("html")
	html, _ := v.(string)
	for _, w := range want {
		if !strings.Contains(html, w) {
			t.Errorf("the HTML lacks %q:\n%s", w, html)
		}
	}
	for _, n := range not {
		if strings.Contains(html, n) {
			t.Errorf("the HTML has %q:\n%s", n, html)
		}
	}
}

// shop is a site with a layout, the Product template, a free home page
// and two product entries, all saved as drafts.
func shop(t *testing.T, w *world) *apptest.Client {
	t.Helper()
	c := w.as(w.writer)
	c.PutJSON("/v1/editor/site", map[string]any{"name": "Shop", "settings": map[string]any{"lang": "en"}}).AssertOK()
	c.PutJSON("/v1/editor/layouts/"+layoutID, map[string]any{"name": "Main", "data": project}).AssertCreated()
	c.PutJSON("/v1/editor/templates/"+templateID, map[string]any{"name": "Product", "fields": productFields, "data": project}).AssertCreated()
	c.PutJSON("/v1/editor/pages/"+homeID, map[string]any{"name": "Home", "slug": "/", "layout_id": layoutID, "data": project,
		"values": map[string]any{"in_menu": true, "seo.title": "Shop: home"}}).AssertCreated()
	c.PutJSON("/v1/editor/pages/"+grinderID, map[string]any{"name": "Coffee grinder", "slug": "/products/coffee-grinder",
		"template_id": templateID, "layout_id": layoutID, "position": 1,
		"values": map[string]any{
			"field.title": "Coffee grinder", "field.price": 49.5, "field.sale": false,
			"field.photo":     map[string]any{"url": "https://cdn.example.com/grinder.jpg", "alt": "A grinder"},
			"field.specs":     []map[string]any{{"name": "Burrs", "value": "Steel"}, {"name": "Weight", "value": "1 kg"}},
			"seo.description": "Grinds coffee", "sku": "G-1", "in_menu": true,
		}}).AssertCreated()
	c.PutJSON("/v1/editor/pages/"+kettleID, map[string]any{"name": "Kettle", "slug": "/products/kettle", "template_id": templateID, "position": 2,
		"values": map[string]any{"field.title": "Kettle", "field.price": "1200", "field.sale": true}}).AssertCreated()
	return c
}

func TestPublishLayoutTemplatePage(t *testing.T) {
	w := newWorld(t)
	c := shop(t, w)

	// A layout needs its slot; the HTML is required.
	c.PostJSON("/v1/editor/layouts/"+layoutID+"/publish", map[string]any{"html": "<header>Shop</header>"}).
		AssertUnprocessable().AssertJSONValidationErrors("html")
	c.PostJSON("/v1/editor/layouts/"+layoutID+"/publish", map[string]any{}).AssertUnprocessable().AssertJSONValidationErrors("html")
	c.WithHeader("If-Match", `"7"`).PostJSON("/v1/editor/layouts/"+layoutID+"/publish", map[string]any{"html": "<luna-slot></luna-slot>"}).AssertStatus(412)
	c.WithHeader("If-Match", `"1"`).PostJSON("/v1/editor/layouts/"+layoutID+"/publish", map[string]any{
		"html": `<header onclick="x()">Shop</header><main><luna-slot></luna-slot></main><footer>©</footer>`,
		"css":  `header{color:navy}@import url(https://evil.example/x.css);`,
	}).AssertOK().AssertHeader("ETag", `"2"`).AssertJSONPath("revision", 1).AssertJSONPath("record.version", 2)

	// An entry cannot go live before its template.
	c.PostJSON("/v1/editor/pages/"+grinderID+"/publish", nil).AssertUnprocessable().AssertJSONValidationErrors("template_id")

	// The template: warnings for fields without an element.
	c.PostJSON("/v1/editor/templates/"+templateID+"/publish", map[string]any{"html": strings.Replace(productHTML, `<span data-field="sale" class="badge">Sale</span>`, "", 1), "css": ".product{display:grid}"}).
		AssertOK().AssertJSONCount("warnings", 1).AssertSee("The field sale has no element")
	c.PostJSON("/v1/editor/templates/"+templateID+"/publish", map[string]any{"html": productHTML, "css": ".product{display:grid}"}).
		AssertOK().AssertJSONPath("revision", 2).AssertJSONCount("warnings", 0)

	// Required fields are checked on publish; the entry's draft values go live.
	c.PutJSON("/v1/editor/pages/"+kettleID, map[string]any{"name": "Kettle", "values": map[string]any{"field.title": ""}}).AssertOK()
	c.PostJSON("/v1/editor/pages/"+kettleID+"/publish", nil).AssertUnprocessable().AssertJSONValidationErrors("field.title")
	c.PutJSON("/v1/editor/pages/"+kettleID, map[string]any{"name": "Kettle", "values": map[string]any{"field.title": "Kettle", "field.price": "cheap"}}).AssertOK()
	c.PostJSON("/v1/editor/pages/"+kettleID+"/publish", nil).AssertUnprocessable().AssertJSONValidationErrors("field.price")
	c.PutJSON("/v1/editor/pages/"+kettleID, map[string]any{"name": "Kettle", "values": map[string]any{"field.price": 1200,
		"field.specs": []map[string]any{{"value": "no name"}}}}).AssertOK()
	c.PostJSON("/v1/editor/pages/"+kettleID+"/publish", nil).AssertUnprocessable().AssertJSONValidationErrors("field.specs.0.name")
	c.PutJSON("/v1/editor/pages/"+kettleID, map[string]any{"name": "Kettle", "values": map[string]any{"field.specs": nil}}).AssertOK()
	c.PostJSON("/v1/editor/pages/"+kettleID+"/publish", nil).AssertOK().AssertJSONPath("record.status", "published")
	c.PostJSON("/v1/editor/pages/"+grinderID+"/publish", nil).AssertOK()

	// A free page publishes its HTML.
	c.PostJSON("/v1/editor/pages/"+homeID+"/publish", map[string]any{"html": `<h1>Welcome</h1><a href="javascript:alert(1)">x</a>`, "css": "h1{font-size:3rem}", "data": project}).
		AssertOK().AssertJSONPath("record.status", "published")
	c.PostJSON("/v1/editor/site/publish", map[string]any{"css": "body{margin:0}"}).AssertOK().AssertJSONPath("revision", 1)
	c.PostJSON("/v1/editor/site/publish", map[string]any{}).AssertUnprocessable().AssertJSONValidationErrors("css")

	// Rendered, sanitized, filled, in the layout, CSS in order.
	r := w.as(w.reader).GetJSON("/v1/render?slug=/products/coffee-grinder").AssertOK().
		AssertHeader("Cache-Control", "public, max-age=60").
		AssertJSONPath("title", "Coffee grinder").AssertJSONPath("seo.description", "Grinds coffee").AssertJSONPath("meta.sku", "G-1").
		AssertJSONPath("css", "body{margin:0}\nheader{color:navy}\n.product{display:grid}")
	html, _ := r.JSONPath("html")
	want := `<header>Shop</header><main><article class="product">` +
		`<h1 data-field="title">Coffee grinder</h1>` +
		`<img data-field="photo" src="https://cdn.example.com/grinder.jpg" alt="A grinder"/>` +
		`<p>Price: <span data-field="price">49.50</span></p>` +
		`<ul data-field="specs"><li><b data-field="name">Burrs</b>: <i data-field="value">Steel</i></li><li><b data-field="name">Weight</b>: <i data-field="value">1 kg</i></li></ul>` +
		`<a href="/products/coffee-grinder" title="Coffee grinder">Coffee grinder: 49.50</a>` +
		`</article></main><footer>©</footer>`
	if html != want {
		t.Errorf("rendered:\n got: %v\nwant: %s", html, want)
	}

	// ETag and 304; the cache follows publishing.
	tag := r.Header().Get("ETag")
	w.as(w.reader).WithHeader("If-None-Match", tag).Get("/v1/render?slug=/products/coffee-grinder").AssertStatus(304)
	w.as(w.reader).WithHeader("If-None-Match", tag).Get("/v1/render?id=" + grinderID).AssertStatus(304)
	c.PostJSON("/v1/editor/layouts/"+layoutID+"/publish", map[string]any{"html": `<nav>New</nav><luna-slot></luna-slot>`}).AssertOK()
	r2 := w.as(w.reader).WithHeader("If-None-Match", tag).GetJSON("/v1/render?slug=/products/coffee-grinder").AssertOK()
	htmlHas(t, r2, []string{"<nav>New</nav>"})
	if r2.Header().Get("ETag") == tag {
		t.Error("the ETag did not change with the layout")
	}

	// Without a layout; the free page; a draft value change does not show until published.
	htmlHas(t, w.as(w.reader).GetJSON("/v1/render?slug=products/kettle").AssertOK(),
		[]string{`<span data-field="price">1200.00</span>`, `class="badge">Sale</span>`}, "<nav>", "<script>", "onerror")
	htmlHas(t, w.as(w.reader).GetJSON("/v1/render?slug=/").AssertOK().AssertJSONPath("seo.title", "Shop: home"),
		[]string{"<nav>New</nav><h1>Welcome</h1><a>x</a>"})
	c.PutJSON("/v1/editor/pages/"+grinderID, map[string]any{"name": "Coffee grinder", "values": map[string]any{"field.title": "Grinder 2"}}).AssertOK()
	w.as(w.reader).GetJSON("/v1/render?slug=/products/coffee-grinder").AssertOK().AssertDontSee("Grinder 2")

	// Drafts: pages:write only, never cached.
	w.as(w.reader).GetJSON("/v1/render?slug=/products/coffee-grinder&draft=1").AssertForbidden()
	htmlHas(t, c.GetJSON("/v1/render?slug=/products/coffee-grinder&draft=1").AssertOK().
		AssertHeader("Cache-Control", "no-store").AssertJSONPath("draft", true), []string{"Grinder 2"})

	// Not live, unknown, no slug, another application.
	c.PostJSON("/v1/editor/pages/"+kettleID+"/unpublish", nil).AssertOK().AssertJSONPath("status", "draft")
	w.as(w.reader).GetJSON("/v1/render?slug=/products/kettle").AssertNotFound()
	c.GetJSON("/v1/render?slug=/products/kettle&draft=1").AssertOK()
	w.as(w.reader).GetJSON("/v1/render?slug=/nope").AssertNotFound()
	w.as(w.reader).GetJSON("/v1/render?slug=Not%20A%20Slug!").AssertNotFound()
	w.as(w.reader).GetJSON("/v1/render").AssertUnprocessable().AssertJSONValidationErrors("slug")
	w.as(w.blog).GetJSON("/v1/render?slug=/").AssertForbidden() // the blog key has no pages:read
}

func TestReadAPI(t *testing.T) {
	w := newWorld(t)
	c := shop(t, w)
	c.PostJSON("/v1/editor/templates/"+templateID+"/publish", map[string]any{"html": productHTML}).AssertOK()
	c.PostJSON("/v1/editor/pages/"+grinderID+"/publish", nil).AssertOK()
	c.PostJSON("/v1/editor/pages/"+kettleID+"/publish", nil).AssertOK()
	c.PostJSON("/v1/editor/pages/"+homeID+"/publish", map[string]any{"html": "<h1>Home</h1>"}).AssertOK()

	// A browser key from its origin reads published pages.
	b := w.as(w.browser).WithHeader("Origin", "https://shop.example")
	// (Within the test's one transaction every published_at is the same, so not by date.)
	b.GetJSON("/v1/pages?template=product&sort=-title").AssertOK().AssertHeader("Access-Control-Allow-Origin", "https://shop.example").
		AssertJSONPath("total", 2).AssertJSONCount("items", 2).
		AssertJSONPath("items.0.slug", "/products/kettle").
		AssertJSONPath("items.0.template", "Product").AssertJSONPath("items.0.fields.price", "1200").
		AssertJSONPath("items.1.fields.specs.1.value", "1 kg").AssertJSONPath("items.1.meta.sku", "G-1").
		AssertJSONPath("items.1.seo.description", "Grinds coffee")
	b.GetJSON("/v1/pages?template="+templateID+"&sort=position&per_page=1").AssertOK().
		AssertJSONPath("total", 2).AssertJSONCount("items", 1).AssertJSONPath("items.0.title", "Coffee grinder")
	b.GetJSON("/v1/pages?template=product&sort=position&per_page=1&page=2").AssertOK().AssertJSONPath("items.0.title", "Kettle")
	b.GetJSON("/v1/pages?sort=-title").AssertOK().AssertJSONPath("total", 3).AssertJSONPath("items.0.title", "Kettle")
	b.GetJSON("/v1/pages?template=nothing").AssertOK().AssertJSONPath("total", 0)
	b.GetJSON("/v1/pages?sort=price").AssertUnprocessable().AssertJSONValidationErrors("sort")
	b.GetJSON("/v1/pages?per_page=500").AssertUnprocessable().AssertJSONValidationErrors("per_page")
	b.GetJSON("/v1/pages/"+grinderID).AssertOK().AssertJSONPath("fields.title", "Coffee grinder").AssertJSONMissing("html")
	b.GetJSON("/v1/pages/" + "aaaaaaaa-0000-4000-8000-0000000000ff").AssertNotFound()
	b.GetJSON("/v1/render?slug=/products/coffee-grinder").AssertOK()

	// The site: menu from in_menu, CSS once published.
	s := b.GetJSON("/v1/site").AssertOK().AssertJSONPath("name", "Shop").AssertJSONPath("settings.lang", "en").AssertJSONPath("css", "").
		AssertJSONCount("menu", 2).AssertJSONPath("menu.0.slug", "/")
	tag := s.Header().Get("ETag")
	b.WithHeader("If-None-Match", tag).Get("/v1/site").AssertStatus(304)
	c.PostJSON("/v1/editor/site/publish", map[string]any{"css": "body{margin:0}"}).AssertOK()
	b.WithHeader("If-None-Match", tag).GetJSON("/v1/site").AssertOK().AssertJSONPath("css", "body{margin:0}")

	// Drafts are never listed; the editor routes stay closed to readers.
	c.PostJSON("/v1/editor/pages/"+kettleID+"/unpublish", nil).AssertOK()
	b.GetJSON("/v1/pages").AssertOK().AssertJSONPath("total", 2)
	b.GetJSON("/v1/pages/" + kettleID).AssertNotFound()
	b.PostJSON("/v1/editor/pages/"+kettleID+"/publish", nil).AssertForbidden()
	w.as(w.authOnly).GetJSON("/v1/pages").AssertForbidden()
	w.as(w.blog).GetJSON("/v1/editor/pages/" + grinderID).AssertNotFound() // another application's ids do not exist
}

func TestRevisionsAndRestore(t *testing.T) {
	w := newWorld(t)
	c := w.as(w.writer)
	v1 := map[string]any{"pages": []any{map[string]any{"component": "<h1>One</h1>"}}}
	v2 := map[string]any{"pages": []any{map[string]any{"component": "<h1>Two</h1>"}}}
	c.PutJSON("/v1/editor/pages/"+homeID, map[string]any{"name": "Home", "slug": "/", "data": v1}).AssertCreated()
	c.PostJSON("/v1/editor/pages/"+homeID+"/publish", map[string]any{"html": "<h1>One</h1>"}).AssertOK().AssertJSONPath("revision", 1)
	c.PostJSON("/v1/editor/pages/"+homeID+"/publish", map[string]any{"html": "<h1>Two</h1>", "data": v2}).AssertOK().AssertJSONPath("revision", 2)

	c.GetJSON("/v1/editor/pages/"+homeID+"/revisions").AssertOK().AssertJSONCount("items", 2).
		AssertJSONPath("items.0.revision", 2).AssertJSONPath("items.0.live", true).AssertJSONPath("items.1.live", false).
		AssertJSONPath("items.0.created_by", "key:k1")
	rec := c.GetJSON("/v1/editor/pages/"+homeID).AssertOK().AssertJSONPath("data.pages.0.component", "<h1>Two</h1>")
	version := rec.Header().Get("ETag")

	c.WithHeader("If-Match", `"1"`).PostJSON("/v1/editor/pages/"+homeID+"/revisions/1/restore", nil).AssertStatus(412)
	c.WithHeader("If-Match", version).PostJSON("/v1/editor/pages/"+homeID+"/revisions/1/restore", nil).AssertOK().
		AssertJSONPath("data.pages.0.component", "<h1>One</h1>")
	c.GetJSON("/v1/editor/pages/"+homeID).AssertOK().AssertJSONPath("data.pages.0.component", "<h1>One</h1>")
	htmlHas(t, w.as(w.reader).GetJSON("/v1/render?slug=/").AssertOK(), []string{"<h1>Two</h1>"}) // restoring is not publishing
	c.PostJSON("/v1/editor/pages/"+homeID+"/revisions/9/restore", nil).AssertNotFound()
	c.PostJSON("/v1/editor/pages/"+homeID+"/revisions/x/restore", nil).AssertNotFound()
	c.GetJSON("/v1/editor/pages/" + layoutID + "/revisions").AssertNotFound()

	c.PostJSON("/v1/editor/site/publish", map[string]any{"css": "a{}"}).AssertOK()
	c.GetJSON("/v1/editor/site/revisions").AssertOK().AssertJSONCount("items", 1)

	// Only the newest 20 revisions stay.
	for i := 0; i < 22; i++ {
		c.PostJSON("/v1/editor/pages/"+homeID+"/publish", map[string]any{"html": "<p>x</p>"}).AssertOK()
	}
	c.GetJSON("/v1/editor/pages/"+homeID+"/revisions").AssertOK().AssertJSONCount("items", 20).AssertJSONPath("items.0.revision", 24)
}

func TestSitePolicy(t *testing.T) {
	w := newWorld(t)
	c := w.as(w.writer)
	page := `<iframe src="https://maps.example.com/embed"></iframe><iframe src="https://www.youtube.com/embed/x"></iframe><script>track()</script>`
	c.PutJSON("/v1/editor/pages/"+homeID, map[string]any{"name": "Home", "slug": "/"}).AssertCreated()
	c.PostJSON("/v1/editor/pages/"+homeID+"/publish", map[string]any{"html": page}).AssertOK()
	htmlHas(t, w.as(w.reader).GetJSON("/v1/render?slug=/").AssertOK(), []string{"youtube.com"}, "maps.example.com", "<script>")

	c.PutJSON("/v1/editor/site", map[string]any{"settings": map[string]any{"custom_code": true, "iframe_hosts": []string{"maps.example.com"}}}).AssertOK()
	c.PostJSON("/v1/editor/pages/"+homeID+"/publish", map[string]any{"html": page}).AssertOK()
	htmlHas(t, w.as(w.reader).GetJSON("/v1/render?slug=/").AssertOK(), []string{"maps.example.com", "youtube.com", "<script>track()</script>"})
}

func TestInternalReadAndPublish(t *testing.T) {
	w := newWorld(t)
	c := shop(t, w)
	c.PostJSON("/v1/editor/templates/"+templateID+"/publish", map[string]any{"html": productHTML}).AssertOK()

	in := w.c.WithHeader("Authorization", "Bearer "+serviceToken)
	base := "/internal/v1/apps/" + shopID
	in.PostJSON(base+"/editor/pages/"+grinderID+"/publish", nil).AssertOK()
	htmlHas(t, in.GetJSON(base+"/render?slug=/products/coffee-grinder").AssertOK(), []string{"Coffee grinder"})
	in.GetJSON(base+"/render?slug=/products/kettle&draft=1").AssertOK().AssertJSONPath("draft", true) // the dashboard previews drafts
	in.GetJSON(base+"/pages").AssertOK().AssertJSONPath("total", 1)
	in.GetJSON(base+"/site").AssertOK().AssertJSONPath("name", "Shop")
	in.GetJSON("/internal/v1/apps/"+blogID+"/pages").AssertOK().AssertJSONPath("total", 0)
	w.c.WithHeader(keys.Header, w.writer).GetJSON(base + "/pages").AssertUnauthorized()
}
