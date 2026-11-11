package tests

import (
	"testing"
)

const (
	entryID = "aaaaaaaa-0000-4000-8000-000000000008"
	tmpl2ID = "aaaaaaaa-0000-4000-8000-000000000009"
)

// The content API: grapesjs-storage's REST contract.
func TestContentTemplates(t *testing.T) {
	w := newWorld(t)
	c := w.as(w.writer)
	c.GetJSON("/v1/content/templates").AssertOK().AssertJSONCount("items", 0)

	// Fields of a new template need its name; then they are saved without moving the version.
	c.PutJSON("/v1/content/templates/"+templateID, map[string]any{"fields": productFields}).AssertUnprocessable().AssertJSONValidationErrors("name")
	c.PutJSON("/v1/content/templates/"+templateID, map[string]any{"name": "Product", "fields": productFields, "data": project}).
		AssertCreated().AssertJSONCount("fields", 5).AssertJSONPath("version", 1)
	c.PutJSON("/v1/content/templates/"+templateID, map[string]any{"fields": productFields[:2]}).AssertOK().
		AssertJSONCount("fields", 2).AssertJSONPath("version", 1).AssertJSONPath("name", "Product")
	c.PutJSON("/v1/content/templates/"+templateID, map[string]any{"fields": []map[string]any{{"name": "Bad Name", "type": "text"}}}).AssertUnprocessable()

	// The editor contract sees the same template (its version untouched: no 412 for grapesjs-pages).
	c.GetJSON("/v1/editor/templates/"+templateID).AssertOK().AssertJSONPath("version", 1).AssertJSONCount("fields", 2)
	c.GetJSON("/v1/content/templates").AssertOK().AssertJSONCount("items", 1).AssertJSONMissing("items.0.data")
	c.GetJSON("/v1/content/templates/"+templateID).AssertOK().AssertJSONPath("data.pages.0.component", "<h1>Hello</h1>")
	c.GetJSON("/v1/content/templates/" + tmpl2ID).AssertNotFound()
	w.as(w.reader).GetJSON("/v1/content/templates").AssertForbidden()
}

func TestContentEntries(t *testing.T) {
	w := newWorld(t)
	c := w.as(w.writer)
	c.PutJSON("/v1/content/templates/"+templateID, map[string]any{"name": "Product", "fields": productFields}).AssertCreated()
	c.PutJSON("/v1/editor/pages/"+homeID, map[string]any{"name": "Home", "slug": "/"}).AssertCreated()

	// Create: values by plain names, stored as field.<name>.
	c.PutJSON("/v1/content/entries/"+entryID, map[string]any{"name": "Grinder", "values": map[string]any{"title": "G"}}).
		AssertUnprocessable().AssertJSONValidationErrors("template_id")
	r := c.PutJSON("/v1/content/entries/"+entryID, map[string]any{
		"template_id": templateID, "name": "Grinder", "slug": "/products/grinder",
		"values": map[string]any{"title": "Coffee grinder", "price": 49.5, "specs": []map[string]any{{"name": "Burrs", "value": "Steel"}}},
	}).AssertCreated().AssertJSONPath("template_id", templateID).AssertJSONPath("values.title", "Coffee grinder").AssertJSONPath("status", "draft")
	version := r.Header().Get("ETag")
	c.GetJSON("/v1/editor/pages/"+entryID).AssertOK().AssertSee(`"field.title":"Coffee grinder"`).AssertJSONPath("page_kind", "entry")

	// Update with If-Match; null deletes a value; a stale version is refused.
	c.WithHeader("If-Match", version).PutJSON("/v1/content/entries/"+entryID, map[string]any{"name": "Grinder", "values": map[string]any{"price": nil, "sale": true}}).
		AssertOK().AssertJSONMissing("values.price").AssertJSONPath("values.sale", true).AssertJSONPath("values.title", "Coffee grinder")
	c.WithHeader("If-Match", version).PutJSON("/v1/content/entries/"+entryID, map[string]any{"name": "Grinder", "values": map[string]any{}}).AssertStatus(412)

	// Lists: entries only, headers only.
	c.GetJSON("/v1/content/entries").AssertOK().AssertJSONCount("items", 1).AssertJSONMissing("items.0.values")
	c.GetJSON("/v1/content/entries?template="+templateID).AssertOK().AssertJSONCount("items", 1).AssertJSONPath("items.0.slug", "/products/grinder")
	c.GetJSON("/v1/content/entries?template="+tmpl2ID).AssertOK().AssertJSONCount("items", 0)
	c.GetJSON("/v1/content/entries/" + homeID).AssertNotFound() // a free page is not an entry
	c.PutJSON("/v1/content/entries/"+homeID, map[string]any{"template_id": templateID, "name": "X"}).AssertNotFound()

	// Publish: checked against the template (its design must be live).
	cur := c.GetJSON("/v1/content/entries/" + entryID).AssertOK()
	c.WithHeader("If-Match", cur.Header().Get("ETag")).PostJSON("/v1/content/entries/"+entryID+"/publish", nil).
		AssertUnprocessable().AssertJSONValidationErrors("template_id")
	c.PostJSON("/v1/editor/templates/"+templateID+"/publish", map[string]any{"html": productHTML}).AssertOK()
	cur = c.GetJSON("/v1/content/entries/" + entryID).AssertOK()
	c.WithHeader("If-Match", cur.Header().Get("ETag")).PostJSON("/v1/content/entries/"+entryID+"/publish", nil).AssertOK().AssertJSONPath("status", "published")
	htmlHas(t, w.as(w.reader).GetJSON("/v1/render?slug=/products/grinder").AssertOK(), []string{`<h1 data-field="title">Coffee grinder</h1>`})

	// Fields changed through the content API change the rendering (and its ETag).
	before := w.as(w.reader).GetJSON("/v1/render?slug=/products/grinder").AssertOK().Header().Get("ETag")
	c.PutJSON("/v1/content/templates/"+templateID, map[string]any{"fields": productFields[:1]}).AssertOK()
	after := w.as(w.reader).GetJSON("/v1/render?slug=/products/grinder").AssertOK().Header().Get("ETag")
	if before == after {
		t.Error("the render ETag did not follow the fields")
	}

	// Delete.
	cur = c.GetJSON("/v1/content/entries/" + entryID).AssertOK()
	c.WithHeader("If-Match", `"1"`).Delete("/v1/content/entries/" + entryID).AssertStatus(412)
	c.WithHeader("If-Match", cur.Header().Get("ETag")).Delete("/v1/content/entries/" + entryID).AssertNoContent()
	c.GetJSON("/v1/content/entries/" + entryID).AssertNotFound()
	c.Delete("/v1/content/entries/" + homeID).AssertNotFound()
}

func TestContentInternal(t *testing.T) {
	w := newWorld(t)
	in := w.c.WithHeader("Authorization", "Bearer "+serviceToken)
	base := "/internal/v1/apps/" + shopID + "/content"
	in.PutJSON(base+"/templates/"+templateID, map[string]any{"name": "Product", "fields": productFields}).AssertCreated()
	in.PutJSON(base+"/entries/"+entryID, map[string]any{"template_id": templateID, "name": "E", "values": map[string]any{"title": "T"}}).AssertCreated()
	in.GetJSON(base + "/entries").AssertOK().AssertJSONCount("items", 1)
	w.as(w.writer).GetJSON("/v1/content/entries").AssertOK().AssertJSONCount("items", 1)
	w.as(w.blog).GetJSON("/v1/content/entries").AssertOK().AssertJSONCount("items", 0) // another application
}
