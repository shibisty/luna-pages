package tests

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"fullstack"

	"luna-pages/app/store"
)

const (
	entryID = "aaaaaaaa-0000-4000-8000-000000000008"
	tmpl2ID = "aaaaaaaa-0000-4000-8000-000000000009"
	dataID  = "aaaaaaaa-0000-4000-8000-00000000000a"
)

var groupFields = []map[string]any{
	{"id": "f1", "name": "title", "type": "text", "translatable": true, "required": true},
	{"id": "f2", "name": "price", "type": "number"},
	{"id": "f3", "name": "specs", "type": "repeater", "fields": []map[string]any{{"id": "f4", "name": "key", "type": "text"}}},
}

// The content API: grapesjs-storage 1.x's REST contract.
func TestContentTemplates(t *testing.T) {
	w := newWorld(t)
	c := w.as(w.writer)
	c.GetJSON("/v1/content/templates").AssertOK().AssertJSONCount("items", 0)

	c.PutJSON("/v1/content/templates/product", map[string]any{"name": "Product"}).AssertUnprocessable().AssertJSONValidationErrors("fields")
	c.PutJSON("/v1/content/templates/bad.id", map[string]any{"fields": groupFields}).AssertUnprocessable()
	c.PutJSON("/v1/content/templates/product", map[string]any{"name": "Product", "fields": groupFields, "data": map[string]any{"title": "Sample"}}).
		AssertCreated().AssertJSONCount("fields", 3).AssertJSONPath("version", 1).AssertJSONPath("fields.2.fields.0.name", "key")
	c.PutJSON("/v1/content/templates/product", map[string]any{"fields": groupFields[:2]}).AssertOK().
		AssertJSONCount("fields", 2).AssertJSONPath("version", 2).AssertJSONPath("name", "Product")
	c.GetJSON("/v1/content/templates").AssertOK().AssertJSONCount("items", 1).AssertJSONMissing("items.0.data")
	c.GetJSON("/v1/content/templates/product").AssertOK().AssertJSONPath("data.title", "Sample")
	// Not the editor's templates (designs): a template of 1.x is not a record of grapesjs-pages.
	c.GetJSON("/v1/editor/templates").AssertOK().AssertJSONCount("items", 0)

	c.Delete("/v1/content/templates/product").AssertNoContent()
	c.GetJSON("/v1/content/templates/product").AssertNotFound()
	c.Delete("/v1/content/templates/product").AssertNotFound()
	w.as(w.reader).GetJSON("/v1/content/templates").AssertForbidden()
}

func TestContentEntries(t *testing.T) {
	w := newWorld(t)
	c := w.as(w.writer)
	c.PutJSON("/v1/content/templates/product", map[string]any{"name": "Product", "fields": groupFields}).AssertCreated()
	// A page with the template (site-pages keeps it in the page's values), and its data: an entry with the page's id.
	c.PutJSON("/v1/editor/pages/"+homeID, map[string]any{"name": "Grinder", "slug": "/grinder", "values": map[string]any{"storage.template": "product"}}).AssertCreated()

	c.PutJSON("/v1/content/entries/"+homeID, map[string]any{"name": "Grinder"}).AssertUnprocessable().AssertJSONValidationErrors("template_id")
	r := c.PutJSON("/v1/content/entries/"+homeID, map[string]any{
		"template_id": "product", "name": "Grinder", "slug": "/grinder",
		"values": map[string]any{"title": map[string]any{"uk-ua": "Кавомолка", "en-gb": "Grinder"}, "price": 1200, "specs": []map[string]any{{"key": "Burrs"}}},
	}).AssertCreated().AssertJSONPath("values.title.uk-ua", "Кавомолка").AssertJSONPath("status", "draft")
	version := r.Header().Get("ETag")

	// Values merge; null deletes; a stale version is refused.
	c.WithHeader("If-Match", version).PutJSON("/v1/content/entries/"+homeID, map[string]any{"values": map[string]any{"price": nil, "sale": true}}).
		AssertOK().AssertJSONMissing("values.price").AssertJSONPath("values.sale", true).AssertJSONPath("values.title.en-gb", "Grinder")
	c.WithHeader("If-Match", version).PutJSON("/v1/content/entries/"+homeID, map[string]any{"values": map[string]any{}}).AssertStatus(412)

	// A standalone entry too (any id).
	c.PutJSON("/v1/content/entries/promo-1", map[string]any{"template_id": "product", "name": "Promo"}).AssertCreated()
	c.GetJSON("/v1/content/entries").AssertOK().AssertJSONCount("items", 2).AssertJSONMissing("items.0.values")
	c.GetJSON("/v1/content/entries?template=product").AssertOK().AssertJSONCount("items", 2)
	c.GetJSON("/v1/content/entries?template=other").AssertOK().AssertJSONCount("items", 0)

	cur := c.GetJSON("/v1/content/entries/" + homeID).AssertOK()
	c.WithHeader("If-Match", cur.Header().Get("ETag")).PostJSON("/v1/content/entries/"+homeID+"/publish", nil).
		AssertOK().AssertJSONPath("status", "published")

	// A data record of the page (data_of); deleting the page deletes it and the data.
	c.PutJSON("/v1/editor/pages/"+dataID, map[string]any{"name": "Grinder 2", "slug": "/grinder/2", "data_of": homeID}).AssertCreated().AssertJSONPath("data_of", homeID)
	c.PutJSON("/v1/editor/pages/"+entryID, map[string]any{"name": "X", "data_of": tmpl2ID}).AssertUnprocessable().AssertJSONValidationErrors("data_of")
	c.PutJSON("/v1/content/entries/"+dataID, map[string]any{"template_id": "product", "name": "Grinder 2", "values": map[string]any{"price": 900}}).AssertCreated()
	c.GetJSON("/v1/editor/pages").AssertOK().AssertJSONCount("items", 2)
	c.Delete("/v1/editor/pages/" + homeID).AssertNoContent()
	c.GetJSON("/v1/editor/pages/" + dataID).AssertNotFound()
	c.GetJSON("/v1/content/entries/" + homeID).AssertNotFound()
	c.GetJSON("/v1/content/entries/" + dataID).AssertNotFound()

	// Delete with If-Match.
	cur = c.GetJSON("/v1/content/entries/promo-1").AssertOK()
	c.WithHeader("If-Match", `"9"`).Delete("/v1/content/entries/promo-1").AssertStatus(412)
	c.WithHeader("If-Match", cur.Header().Get("ETag")).Delete("/v1/content/entries/promo-1").AssertNoContent()
}

func TestContentLanguages(t *testing.T) {
	w := newWorld(t)
	c := w.as(w.writer)
	c.GetJSON("/v1/content/languages").AssertNotFound()
	c.PutJSON("/v1/content/languages", map[string]any{"default": "uk", "enabled": []string{}}).AssertUnprocessable()
	c.PutJSON("/v1/content/languages", map[string]any{"default": "uk-ua", "enabled": []string{"en-gb", "uk-ua", "en-gb"}}).AssertOK().
		AssertJSONPath("enabled.0", "uk-ua").AssertJSONCount("enabled", 2)
	c.GetJSON("/v1/content/languages").AssertOK().AssertJSONPath("default", "uk-ua")
	c.PutJSON("/v1/content/languages", map[string]any{"default": "", "enabled": []string{}}).AssertOK().AssertJSONCount("enabled", 0)
	c.GetJSON("/v1/content/languages").AssertNotFound()
	w.as(w.blog).GetJSON("/v1/content/languages").AssertNotFound() // another application
}

// Linked components (contract 2.1): records like layouts.
func TestComponents(t *testing.T) {
	w := newWorld(t)
	c := w.as(w.writer)
	c.GetJSON("/v1/editor/components").AssertOK().AssertJSONCount("items", 0)
	c.PutJSON("/v1/editor/components/"+entryID, map[string]any{"name": "Header", "data": project}).AssertCreated().AssertJSONPath("kind", "component")
	c.PostJSON("/v1/editor/components/"+entryID+"/publish", map[string]any{"html": "<header>H</header>", "css": "header{color:red}"}).AssertOK()
	c.GetJSON("/v1/editor/components").AssertOK().AssertJSONCount("items", 1).AssertJSONPath("items.0.name", "Header")
	c.GetJSON("/v1/editor/components/"+entryID).AssertOK().AssertJSONPath("data.pages.0.component", "<h1>Hello</h1>")
	c.Delete("/v1/editor/components/" + entryID).AssertNoContent()
	c.GetJSON("/v1/editor/components/" + entryID).AssertNotFound()
}

func TestContentInternal(t *testing.T) {
	w := newWorld(t)
	in := w.c.WithHeader("Authorization", "Bearer "+serviceToken)
	base := "/internal/v1/apps/" + shopID + "/content"
	in.PutJSON(base+"/templates/product", map[string]any{"name": "Product", "fields": groupFields}).AssertCreated()
	in.PutJSON(base+"/entries/"+entryID, map[string]any{"template_id": "product", "name": "E", "values": map[string]any{"price": 1}}).AssertCreated()
	in.GetJSON(base + "/entries").AssertOK().AssertJSONCount("items", 1)
	w.as(w.writer).GetJSON("/v1/content/entries").AssertOK().AssertJSONCount("items", 1)
	w.as(w.blog).GetJSON("/v1/content/entries").AssertOK().AssertJSONCount("items", 0) // another application
}

// A data record shows its owner page's design with its own values,
// filled in by the server (a record a client wrote without the editor).
func TestDataRecordRender(t *testing.T) {
	w := newWorld(t)
	c := w.as(w.writer)
	fields := []map[string]any{
		{"name": "name", "type": "text"},
		{"name": "repository", "type": "url"},
		{"name": "versions", "type": "repeater", "fields": []map[string]any{{"name": "version", "type": "text"}}},
	}
	c.PutJSON("/v1/content/templates/package", map[string]any{"name": "Package", "fields": fields}).AssertCreated()
	c.PutJSON("/v1/editor/pages/"+homeID, map[string]any{"name": "Package", "slug": "/packages"}).AssertCreated()
	c.PutJSON("/v1/content/entries/"+homeID, map[string]any{"template_id": "package", "values": map[string]any{
		"name": "sample", "versions": []map[string]any{{"version": "1.0.0"}}}}).AssertCreated()
	design := `<h1 data-storage-field="name" data-storage-type="text">sample</h1>` +
		`<a data-storage-field="repository" data-storage-type="url" hidden=""></a>` +
		`<ul data-storage-field="versions" data-storage-type="repeater"><li><span data-storage-field="version" data-storage-type="text">1.0.0</span></li></ul>`
	c.PostJSON("/v1/editor/pages/"+homeID+"/publish", map[string]any{"html": design, "css": "h1{color:red}"}).AssertOK()
	// publishing the page published its data
	c.GetJSON("/v1/content/entries?template=package&values=published").AssertOK().AssertJSONPath("items.0.values.name", "sample")

	// The record: no HTML of its own.
	c.PutJSON("/v1/editor/pages/"+dataID, map[string]any{"name": "orm", "slug": "/packages/orm", "data_of": homeID,
		"values": map[string]any{"seo.description": "An ORM"}}).AssertCreated()
	c.PutJSON("/v1/content/entries/"+dataID, map[string]any{"template_id": "package", "name": "orm", "values": map[string]any{
		"name": "orm", "repository": "https://github.com/shibisty/orm.go",
		"versions": []map[string]any{{"version": "0.2.2"}, {"version": "0.2.1"}}}}).AssertCreated()
	w.as(w.reader).GetJSON("/v1/render?slug=/packages/orm").AssertNotFound() // not published yet
	c.PostJSON("/v1/editor/pages/"+dataID+"/publish", nil).AssertOK()
	r := w.as(w.reader).GetJSON("/v1/render?slug=/packages/orm").AssertOK().AssertJSONPath("title", "orm").AssertJSONPath("seo.description", "An ORM")
	htmlHas(t, r, []string{">orm</h1>", `href="https://github.com/shibisty/orm.go">https://github.com/shibisty/orm.go</a>`,
		`<li><span data-storage-field="version" data-storage-type="text">0.2.2</span></li><li><span data-storage-field="version" data-storage-type="text">0.2.1</span></li>`},
		"1.0.0", "hidden")
	if css, _ := r.JSONPath("css"); !strings.Contains(css.(string), "color:red") {
		t.Errorf("the owner's CSS: %v", css)
	}

	// New values show once published (the draft is not live).
	e := c.GetJSON("/v1/content/entries/" + dataID).AssertOK()
	c.WithHeader("If-Match", e.Header().Get("ETag")).PutJSON("/v1/content/entries/"+dataID, map[string]any{"values": map[string]any{"name": "orm2"}}).AssertOK()
	htmlHas(t, w.as(w.reader).GetJSON("/v1/render?slug=/packages/orm").AssertOK(), []string{">orm</h1>"})
	c.PostJSON("/v1/content/entries/"+dataID+"/publish", nil).AssertOK()
	htmlHas(t, w.as(w.reader).GetJSON("/v1/render?slug=/packages/orm").AssertOK(), []string{">orm2</h1>"})
	c.GetJSON("/v1/content/entries?values=draft&template=package").AssertOK().AssertJSONCount("items", 2)
	c.GetJSON("/v1/content/entries?values=all").AssertUnprocessable()

	// The owner unpublished: its records are not live either.
	c.PostJSON("/v1/editor/pages/"+homeID+"/unpublish", nil).AssertOK()
	w.as(w.reader).GetJSON("/v1/render?slug=/packages/orm").AssertNotFound()
}

// The old model's entries, for clients moving to data records.
func TestLegacyEntries(t *testing.T) {
	w := newWorld(t)
	c := w.as(w.writer)
	c.GetJSON("/v1/content/legacy-entries").AssertUnprocessable()
	c.GetJSON("/v1/content/legacy-entries?template=Package").AssertOK().AssertJSONCount("items", 0)
	st, err := fullstack.Make[*store.Store](w.app)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, _, err := st.SaveTemplateFields(ctx, shopID, tmpl2ID, "Package", []store.Field{{Name: "name", Type: "text"}}, nil, "test"); err != nil {
		t.Fatal(err)
	}
	slug := "/orm"
	if _, _, err := st.PutEntry(ctx, shopID, entryID, store.EntryInput{TemplateID: tmpl2ID, Name: "orm", Slug: &slug,
		Values: map[string]json.RawMessage{"name": json.RawMessage(`"orm"`), "versions": json.RawMessage(`[{"version":"0.2.2"}]`)}}, nil, "test"); err != nil {
		t.Fatal(err)
	}
	c.GetJSON("/v1/content/legacy-entries?template=package").AssertOK().AssertJSONCount("items", 1).
		AssertJSONPath("items.0.slug", "/orm").AssertJSONPath("items.0.values.name", "orm").AssertJSONPath("items.0.values.versions.0.version", "0.2.2")
	c.GetJSON("/v1/content/legacy-entries?template=" + tmpl2ID).AssertOK().AssertJSONCount("items", 1)
	w.as(w.reader).GetJSON("/v1/content/legacy-entries?template=Package").AssertForbidden()
}
