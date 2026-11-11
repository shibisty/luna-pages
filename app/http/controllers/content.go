package controllers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"fullstack/web"

	"luna-pages/app/store"
)

// The content API: grapesjs-storage's REST contract (its openapi.yaml)
// over templates and entries.

// ContentTemplate is a template as the content API shows it.
type ContentTemplate struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Fields    []store.Field   `json:"fields"`
	Data      json.RawMessage `json:"data,omitempty"`
	Version   int64           `json:"version"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// ContentEntry is an entry as the content API shows it: values by plain field name.
type ContentEntry struct {
	ID         string                     `json:"id"`
	TemplateID string                     `json:"template_id"`
	Name       string                     `json:"name"`
	Slug       string                     `json:"slug"`
	Status     string                     `json:"status"`
	Values     map[string]json.RawMessage `json:"values,omitempty"`
	Version    int64                      `json:"version"`
	UpdatedAt  time.Time                  `json:"updated_at"`
	// When it was last published; {{entry.published_at}} in its template.
	PublishedAt *time.Time `json:"published_at"`
}

func contentTemplate(r store.Record, withData bool) ContentTemplate {
	t := ContentTemplate{ID: r.ID, Name: r.Name, Fields: r.Fields, Version: r.Version, UpdatedAt: r.UpdatedAt}
	if t.Fields == nil {
		t.Fields = []store.Field{}
	}
	if withData {
		t.Data = r.Data
	}
	return t
}

func contentEntry(r store.Record, withValues bool) ContentEntry {
	e := ContentEntry{ID: r.ID, Name: r.Name, Slug: r.Slug, Status: r.Status, Version: r.Version, UpdatedAt: r.UpdatedAt, PublishedAt: r.PublishedAt}
	if r.TemplateID != nil {
		e.TemplateID = *r.TemplateID
	}
	if withValues {
		e.Values = store.FieldValues(r.Values)
	}
	return e
}

// ContentTemplates is GET …/content/templates.
func (h API) ContentTemplates(c *web.Ctx) error {
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	list, err := st.List(c.Context(), a, store.Template)
	if err != nil {
		return storeError(err)
	}
	items := make([]ContentTemplate, 0, len(list))
	for _, r := range list {
		items = append(items, contentTemplate(r, false))
	}
	c.W.Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// ContentTemplate is GET …/content/templates/{id}: with the design.
func (h API) ContentTemplate(c *web.Ctx) error {
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	r, err := st.Get(c.Context(), a, store.Template, c.Param("id"))
	if err != nil {
		return storeError(err)
	}
	c.W.Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, contentTemplate(r, true))
}

// PutContentTemplate is PUT …/content/templates/{id}: the fields (and the design when sent).
func (h API) PutContentTemplate(c *web.Ctx) error {
	var in struct {
		Name   string          `json:"name"`
		Fields []store.Field   `json:"fields"`
		Data   json.RawMessage `json:"data"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	st, err := h.store()
	if err != nil {
		return err
	}
	a, by := app(c)
	r, created, err := st.SaveTemplateFields(c.Context(), a, c.Param("id"), in.Name, in.Fields, in.Data, by)
	if err != nil {
		return storeError(err)
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	return c.JSON(status, contentTemplate(r, false))
}

// ContentEntries is GET …/content/entries?template=: headers, no values.
func (h API) ContentEntries(c *web.Ctx) error {
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	list, err := st.ListEntries(c.Context(), a, strings.TrimSpace(c.R.URL.Query().Get("template")))
	if err != nil {
		return storeError(err)
	}
	items := make([]ContentEntry, 0, len(list))
	for _, r := range list {
		items = append(items, contentEntry(r, false))
	}
	c.W.Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// ContentEntry is GET …/content/entries/{id}.
func (h API) ContentEntry(c *web.Ctx) error {
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	r, err := st.GetEntry(c.Context(), a, c.Param("id"))
	if err != nil {
		return storeError(err)
	}
	etag(c, r.Version)
	return c.JSON(http.StatusOK, contentEntry(r, true))
}

// PutContentEntry is PUT …/content/entries/{id} (If-Match).
func (h API) PutContentEntry(c *web.Ctx) error {
	cond, err := ifMatch(c)
	if err != nil {
		return err
	}
	var in store.EntryInput
	if err := decode(c, &in); err != nil {
		return err
	}
	st, err := h.store()
	if err != nil {
		return err
	}
	a, by := app(c)
	r, created, err := st.PutEntry(c.Context(), a, c.Param("id"), in, cond, by)
	if err != nil {
		return storeError(err)
	}
	h.cache.clear(a)
	full, err := st.GetEntry(c.Context(), a, r.ID)
	if err != nil {
		return storeError(err)
	}
	etag(c, full.Version)
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	return c.JSON(status, contentEntry(full, true))
}

// DeleteContentEntry is DELETE …/content/entries/{id} (If-Match).
func (h API) DeleteContentEntry(c *web.Ctx) error {
	cond, err := ifMatch(c)
	if err != nil {
		return err
	}
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	if err := st.DeleteEntry(c.Context(), a, c.Param("id"), cond); err != nil {
		return storeError(err)
	}
	h.cache.clear(a)
	c.W.WriteHeader(http.StatusNoContent)
	return nil
}

// PublishContentEntry is POST …/content/entries/{id}/publish (If-Match).
func (h API) PublishContentEntry(c *web.Ctx) error {
	cond, err := ifMatch(c)
	if err != nil {
		return err
	}
	st, err := h.store()
	if err != nil {
		return err
	}
	a, by := app(c)
	r, err := st.PublishEntry(c.Context(), a, c.Param("id"), cond, by)
	if err != nil {
		return storeError(err)
	}
	h.cache.clear(a)
	etag(c, r.Version)
	return c.JSON(http.StatusOK, contentEntry(r, true))
}
