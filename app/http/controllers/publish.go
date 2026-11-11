package controllers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"fullstack/web"

	"luna-pages/app/store"
)

// Publish is POST …/editor/{kind}/{id}/publish: the editor's HTML and CSS
// (sanitized here) become the live revision; an entry publishes its draft
// values. If-Match as in Put.
func (h API) Publish(c *web.Ctx) error {
	k, err := kindOf(c)
	if err != nil {
		return err
	}
	cond, err := ifMatch(c)
	if err != nil {
		return err
	}
	var in store.PublishInput
	if err := decodeOptional(c, &in); err != nil {
		return err
	}
	st, err := h.store()
	if err != nil {
		return err
	}
	a, by := app(c)
	out, err := st.Publish(c.Context(), a, k, c.Param("id"), in, cond, by)
	if err != nil {
		return storeError(err)
	}
	h.cache.clear(a)
	etag(c, out.Record.(store.Record).Version)
	return c.JSON(http.StatusOK, out)
}

// PublishSite is POST …/editor/site/publish: the site's global CSS.
func (h API) PublishSite(c *web.Ctx) error {
	cond, err := ifMatch(c)
	if err != nil {
		return err
	}
	var in store.PublishInput
	if err := decodeOptional(c, &in); err != nil {
		return err
	}
	st, err := h.store()
	if err != nil {
		return err
	}
	a, by := app(c)
	out, err := st.PublishSite(c.Context(), a, in, cond, by)
	if err != nil {
		return storeError(err)
	}
	h.cache.clear(a)
	etag(c, out.Record.(store.SiteRecord).Version)
	return c.JSON(http.StatusOK, out)
}

// Unpublish is POST …/editor/pages/{id}/unpublish.
func (h API) Unpublish(c *web.Ctx) error {
	cond, err := ifMatch(c)
	if err != nil {
		return err
	}
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	rec, err := st.Unpublish(c.Context(), a, c.Param("id"), cond)
	if err != nil {
		return storeError(err)
	}
	h.cache.clear(a)
	etag(c, rec.Version)
	return c.JSON(http.StatusOK, rec)
}

// Revisions is GET …/editor/{kind}/{id}/revisions (and …/editor/site/revisions).
func (h API) Revisions(c *web.Ctx) error {
	k := store.Site
	if c.Param("kind") != "" {
		var err error
		if k, err = kindOf(c); err != nil {
			return err
		}
	}
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	items, err := st.Revisions(c.Context(), a, k, c.Param("id"))
	if err != nil {
		return storeError(err)
	}
	c.W.Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// Restore is POST …/editor/{kind}/{id}/revisions/{revision}/restore: the
// revision's project becomes the draft (not published).
func (h API) Restore(c *web.Ctx) error {
	k, err := kindOf(c)
	if err != nil {
		return err
	}
	rev, err := strconv.Atoi(c.Param("revision"))
	if err != nil || rev < 1 {
		return web.Error(http.StatusNotFound, "Not found.")
	}
	cond, err := ifMatch(c)
	if err != nil {
		return err
	}
	st, err := h.store()
	if err != nil {
		return err
	}
	a, by := app(c)
	rec, err := st.Restore(c.Context(), a, k, c.Param("id"), rev, cond, by)
	if err != nil {
		return storeError(err)
	}
	etag(c, rec.Version)
	return c.JSON(http.StatusOK, rec)
}

// decodeOptional decodes a JSON body when there is one.
func decodeOptional(c *web.Ctx, v any) error {
	body, err := io.ReadAll(http.MaxBytesReader(c.W, c.R.Body, MaxBody))
	if err != nil {
		return storeError(err)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if json.Unmarshal(body, v) != nil {
		return web.Error(http.StatusBadRequest, "The body must be JSON.")
	}
	return nil
}
