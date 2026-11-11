package tests

import (
	"encoding/json"
	"strings"
	"testing"

	apptest "fullstack-testing"
)

const (
	layoutID   = "aaaaaaaa-0000-4000-8000-000000000001"
	templateID = "aaaaaaaa-0000-4000-8000-000000000002"
	homeID     = "aaaaaaaa-0000-4000-8000-000000000003"
	productID  = "aaaaaaaa-0000-4000-8000-000000000004"
)

var project = map[string]any{"pages": []any{map[string]any{"component": "<h1>Hello</h1>"}}, "styles": "h1{color:red}"}

func TestKeysAndScopes(t *testing.T) {
	w := newWorld(t)
	w.c.GetJSON("/v1/editor/pages").AssertUnauthorized()
	w.as(w.reader).GetJSON("/v1/editor/pages").AssertForbidden()   // drafts need pages:write
	w.as(w.authOnly).GetJSON("/v1/editor/pages").AssertForbidden() // another service's key
	w.as(w.browser).WithHeader("Origin", "https://shop.example").GetJSON("/v1/editor/pages").AssertForbidden()
	w.as(w.writer).WithHeader("Origin", "https://shop.example").GetJSON("/v1/editor/pages").AssertForbidden() // server key from a browser
	w.as(w.writer).GetJSON("/v1/ping").AssertOK().AssertJSONPath("app_id", shopID).AssertJSONPath("app_name", "Shop")
	w.as(w.writer).GetJSON("/v1/editor/pages").AssertOK().AssertJSONPath("items", []any{})
	w.as(w.writer).GetJSON("/v1/editor/widgets").AssertNotFound()
}

func TestSite(t *testing.T) {
	w := newWorld(t)
	c := w.as(w.writer)
	c.GetJSON("/v1/editor/site").AssertOK().AssertHeader("ETag", `"1"`).AssertJSONPath("name", "").AssertJSONPath("settings", map[string]any{})
	c.WithHeader("If-Match", `"1"`).PutJSON("/v1/editor/site", map[string]any{"name": "Shop", "settings": map[string]any{"lang": "uk"}, "data": project}).
		AssertOK().AssertHeader("ETag", `"2"`).AssertJSONPath("version", 2)
	c.WithHeader("If-Match", `"1"`).PutJSON("/v1/editor/site", map[string]any{"name": "Old"}).AssertStatus(412)
	c.PutJSON("/v1/editor/site", map[string]any{"settings": []int{1}}).AssertUnprocessable().AssertJSONValidationErrors("settings")
	c.GetJSON("/v1/editor/site").AssertOK().AssertJSONPath("name", "Shop").AssertJSONPath("data.styles", "h1{color:red}")
	// Another application has its own site.
	w.as(w.blog).GetJSON("/v1/editor/site").AssertOK().AssertJSONPath("name", "")
}

func TestLayoutsTemplatesPages(t *testing.T) {
	w := newWorld(t)
	c := w.as(w.writer)

	c.PutJSON("/v1/editor/layouts/"+layoutID, map[string]any{"name": "Main", "data": project}).
		AssertCreated().AssertHeader("ETag", `"1"`).AssertJSONPath("kind", "layout").AssertJSONPath("name", "Main")
	c.PutJSON("/v1/editor/layouts/not-a-uuid", map[string]any{"name": "X"}).AssertUnprocessable().AssertJSONValidationErrors("id")
	c.PutJSON("/v1/editor/layouts/"+layoutID, map[string]any{"name": " "}).AssertUnprocessable().AssertJSONValidationErrors("name")

	fields := []map[string]any{
		{"name": "title", "type": "text", "required": true},
		{"name": "photo", "type": "image"},
		{"name": "price", "type": "number", "format": "0.00"},
		{"name": "specs", "type": "list", "fields": []map[string]any{{"name": "name", "type": "text"}, {"name": "value", "type": "text"}}},
	}
	c.PutJSON("/v1/editor/templates/"+templateID, map[string]any{"name": "Product", "fields": fields, "data": project}).
		AssertCreated().AssertJSONCount("fields", 4).AssertJSONPath("fields.3.fields.1.name", "value")
	for _, bad := range []map[string]any{
		{"name": "Title", "type": "text"},
		{"name": "x", "type": "video"},
		{"name": "kind", "type": "select"},
		{"name": "rows", "type": "list"},
	} {
		c.PutJSON("/v1/editor/templates/"+templateID, map[string]any{"name": "Product", "fields": []map[string]any{bad}}).
			AssertUnprocessable().AssertJSONValidationErrors("fields")
	}
	c.PutJSON("/v1/editor/templates/"+templateID, map[string]any{"name": "Product", "fields": []map[string]any{fields[0], fields[0]}}).
		AssertUnprocessable().AssertSee("defined twice")

	// A free page as grapesjs-pages v1 sends it: id, name, data. The slug comes from the name.
	c.PutJSON("/v1/editor/pages/"+homeID, map[string]any{"id": homeID, "name": "Home, sweet home", "data": project, "updatedAt": 1}).
		AssertCreated().AssertJSONPath("slug", "/home-sweet-home").AssertJSONPath("page_kind", "free").AssertJSONPath("status", "draft")
	c.WithHeader("If-Match", `"1"`).PutJSON("/v1/editor/pages/"+homeID, map[string]any{"name": "Home", "slug": "/", "layout_id": layoutID}).
		AssertOK().AssertJSONPath("slug", "/").AssertJSONPath("layout_id", layoutID).AssertJSONPath("version", 2)
	c.WithHeader("If-Match", `"1"`).PutJSON("/v1/editor/pages/"+homeID, map[string]any{"name": "Stale"}).AssertStatus(412)
	c.WithHeader("If-Match", "nonsense").PutJSON("/v1/editor/pages/"+homeID, map[string]any{"name": "X"}).AssertBadRequest()

	// An entry: a template and its values.
	c.PutJSON("/v1/editor/pages/"+productID, map[string]any{
		"name": "Coffee grinder", "slug": "/Products/Coffee-Grinder/", "template_id": templateID, "parent_id": homeID,
		"values": map[string]any{"field.title": "Coffee grinder", "field.price": 129.5, "seo.description": "Burr grinder"},
	}).AssertCreated().AssertJSONPath("slug", "/products/coffee-grinder").AssertJSONPath("page_kind", "entry").AssertJSONPath("template_id", templateID)
	var got struct {
		Values map[string]json.RawMessage `json:"values"`
		Data   json.RawMessage            `json:"data"`
	}
	c.GetJSON("/v1/editor/pages/"+productID).AssertOK().AssertHeader("ETag", `"1"`).DecodeJSON(&got)
	if string(got.Values["field.price"]) != "129.5" || string(got.Values["field.title"]) != `"Coffee grinder"` || len(got.Data) != 0 {
		t.Fatalf("entry: %s %s", got.Values, got.Data)
	}
	// Values: set, null deletes, keys not sent stay; a v1 write keeps the template.
	c.PutJSON("/v1/editor/pages/"+productID, map[string]any{"name": "Coffee grinder", "values": map[string]any{"field.price": 119, "seo.description": nil}}).AssertOK()
	got.Values = nil
	c.GetJSON("/v1/editor/pages/"+productID).AssertOK().AssertJSONPath("template_id", templateID).DecodeJSON(&got)
	if len(got.Values) != 2 || string(got.Values["field.price"]) != "119" {
		t.Fatalf("values: %s", got.Values)
	}
	c.PutJSON("/v1/editor/pages/"+productID, map[string]any{"name": "X", "values": map[string]any{"bad key": 1}}).
		AssertUnprocessable().AssertJSONValidationErrors("values")

	// Slugs are unique and valid; references must exist in this application.
	c.PutJSON("/v1/editor/pages/aaaaaaaa-0000-4000-8000-000000000005", map[string]any{"name": "Dup", "slug": "/products/coffee-grinder"}).
		AssertUnprocessable().AssertSee("already has the slug")
	c.PutJSON("/v1/editor/pages/aaaaaaaa-0000-4000-8000-000000000005", map[string]any{"name": "Bad", "slug": "/a b"}).
		AssertUnprocessable().AssertJSONValidationErrors("slug")
	c.PutJSON("/v1/editor/pages/aaaaaaaa-0000-4000-8000-000000000005", map[string]any{"name": "Orphan", "layout_id": "aaaaaaaa-0000-4000-8000-0000000000ff"}).
		AssertUnprocessable().AssertJSONValidationErrors("layout_id")
	// The same name twice gets a numbered slug.
	c.PutJSON("/v1/editor/pages/aaaaaaaa-0000-4000-8000-000000000006", map[string]any{"name": "Home, sweet home"}).
		AssertCreated().AssertJSONPath("slug", "/home-sweet-home")
	c.PutJSON("/v1/editor/pages/aaaaaaaa-0000-4000-8000-000000000007", map[string]any{"name": "Home, sweet home"}).
		AssertCreated().AssertJSONPath("slug", "/home-sweet-home-2")

	c.GetJSON("/v1/editor/pages").AssertOK().AssertJSONCount("items", 4)

	// Another application sees none of it, and cannot write into it.
	blog := w.as(w.blog)
	blog.GetJSON("/v1/editor/pages").AssertOK().AssertJSONCount("items", 0)
	blog.GetJSON("/v1/editor/pages/" + productID).AssertNotFound()
	blog.PutJSON("/v1/editor/pages/"+productID, map[string]any{"name": "Mine", "template_id": templateID}).
		AssertUnprocessable().AssertJSONValidationErrors("template_id")

	// Deleting: a template with entries is refused; a layout leaves its pages.
	c.Delete("/v1/editor/templates/" + templateID).AssertStatus(409)
	c.Delete("/v1/editor/layouts/" + layoutID).AssertNoContent()
	c.GetJSON("/v1/editor/pages/" + homeID).AssertOK().AssertJSONMissing("layout_id")
	c.WithHeader("If-Match", `"1"`).Delete("/v1/editor/pages/" + productID).AssertStatus(412) // it is at version 2
	c.Delete("/v1/editor/pages/" + productID).AssertNoContent()
	c.Delete("/v1/editor/pages/" + productID).AssertNotFound()
	c.Delete("/v1/editor/pages/not-a-uuid").AssertNotFound()
	c.Delete("/v1/editor/templates/" + templateID).AssertNoContent()
}

func TestInternalEditor(t *testing.T) {
	w := newWorld(t)
	in := w.c.WithToken(serviceToken)
	base := "/internal/v1/apps/" + shopID + "/editor"
	w.c.GetJSON(base + "/pages").AssertUnauthorized()
	in.PutJSON(base+"/pages/"+homeID, map[string]any{"name": "Home"}).AssertCreated()
	// The same store as the shop's keys.
	w.as(w.writer).GetJSON("/v1/editor/pages/"+homeID).AssertOK().AssertJSONPath("name", "Home")
	in.GetJSON("/internal/v1/apps/" + blogID + "/editor/pages/" + homeID).AssertNotFound()
	in.GetJSON("/internal/v1/apps/not-a-uuid/editor/pages").AssertNotFound()
}

func TestBodyLimit(t *testing.T) {
	w := newWorld(t)
	big := map[string]any{"name": "Big", "data": map[string]any{"html": strings.Repeat("x", 5<<20)}}
	w.as(w.writer).PutJSON("/v1/editor/layouts/"+layoutID, big).AssertStatus(413)
	tooBigProject := map[string]any{"name": "Big", "data": map[string]any{"html": strings.Repeat("x", 3<<20)}}
	w.as(w.writer).PutJSON("/v1/editor/layouts/"+layoutID, tooBigProject).AssertUnprocessable().AssertJSONValidationErrors("data")
}

var _ = apptest.New
