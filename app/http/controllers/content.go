package controllers

import (
	"net/http"
	"strings"

	"fullstack/web"

	"luna-pages/app/store"
)

// The content API: grapesjs-storage 1.x's REST contract (its openapi.yaml).
// Templates are groups of fields; an entry is the data of a page (the
// page's id) or a standalone entry; the site has languages.

// ContentTemplates is GET …/content/templates: without their data.
func (h API) ContentTemplates(c *web.Ctx) error {
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	list, err := st.DataTemplates(c.Context(), a)
	if err != nil {
		return storeError(err)
	}
	c.W.Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, map[string]any{"items": list})
}

// ContentTemplate is GET …/content/templates/{id}: with its data.
func (h API) ContentTemplate(c *web.Ctx) error {
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	t, err := st.DataTemplate(c.Context(), a, c.Param("id"))
	if err != nil {
		return storeError(err)
	}
	c.W.Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, t)
}

// PutContentTemplate is PUT …/content/templates/{id}: name, fields (and data).
func (h API) PutContentTemplate(c *web.Ctx) error {
	var in store.DataTemplateInput
	if err := decode(c, &in); err != nil {
		return err
	}
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	t, created, err := st.PutDataTemplate(c.Context(), a, c.Param("id"), in)
	if err != nil {
		return storeError(err)
	}
	h.cache.clear(a)
	t.Data = nil
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	c.W.Header().Set("Cache-Control", "no-store")
	return c.JSON(status, t)
}

// DeleteContentTemplate is DELETE …/content/templates/{id}; its entries stay.
func (h API) DeleteContentTemplate(c *web.Ctx) error {
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	if err := st.DeleteDataTemplate(c.Context(), a, c.Param("id")); err != nil {
		return storeError(err)
	}
	h.cache.clear(a)
	c.W.WriteHeader(http.StatusNoContent)
	return nil
}

// ContentEntries is GET …/content/entries?template=&values=: without
// values, or with the draft or published ones (values=draft|published).
func (h API) ContentEntries(c *web.Ctx) error {
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	q := c.R.URL.Query()
	list, err := st.DataEntries(c.Context(), a, strings.TrimSpace(q.Get("template")), q.Get("values"))
	if err != nil {
		return storeError(err)
	}
	c.W.Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, map[string]any{"items": list})
}

// ContentEntry is GET …/content/entries/{id}.
func (h API) ContentEntry(c *web.Ctx) error {
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	e, err := st.DataEntry(c.Context(), a, c.Param("id"))
	if err != nil {
		return storeError(err)
	}
	etag(c, e.Version)
	return c.JSON(http.StatusOK, e)
}

// PutContentEntry is PUT …/content/entries/{id} (If-Match): values merge, null deletes a key.
func (h API) PutContentEntry(c *web.Ctx) error {
	cond, err := ifMatch(c)
	if err != nil {
		return err
	}
	var in store.DataEntryInput
	if err := decode(c, &in); err != nil {
		return err
	}
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	e, created, err := st.PutDataEntry(c.Context(), a, c.Param("id"), in, cond)
	if err != nil {
		return storeError(err)
	}
	etag(c, e.Version)
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	return c.JSON(status, e)
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
	if err := st.DeleteDataEntry(c.Context(), a, c.Param("id"), cond); err != nil {
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
	a, _ := app(c)
	e, err := st.PublishDataEntry(c.Context(), a, c.Param("id"), cond)
	if err != nil {
		return storeError(err)
	}
	h.cache.clear(a)
	etag(c, e.Version)
	return c.JSON(http.StatusOK, e)
}

// ContentLanguages is GET …/content/languages (404: none yet).
func (h API) ContentLanguages(c *web.Ctx) error {
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	l, err := st.DataLanguages(c.Context(), a)
	if err != nil {
		return storeError(err)
	}
	c.W.Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, l)
}

// PutContentLanguages is PUT …/content/languages.
func (h API) PutContentLanguages(c *web.Ctx) error {
	var in store.Languages
	if err := decode(c, &in); err != nil {
		return err
	}
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	l, err := st.PutDataLanguages(c.Context(), a, in)
	if err != nil {
		return storeError(err)
	}
	h.cache.clear(a)
	c.W.Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, l)
}

// LegacyEntries is GET …/content/legacy-entries?template=: the entries of
// the content model before grapesjs-storage 1.x, for clients moving to
// data records (read only, temporary).
func (h API) LegacyEntries(c *web.Ctx) error {
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	t := strings.TrimSpace(c.R.URL.Query().Get("template"))
	if t == "" {
		return invalid("template", "Give the template's name or id.")
	}
	list, err := st.LegacyEntries(c.Context(), a, t)
	if err != nil {
		return storeError(err)
	}
	c.W.Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, map[string]any{"items": list})
}
