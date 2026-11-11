// Package controllers holds the HTTP handlers of luna-pages.
package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"fullstack"
	"fullstack/web"
	"luna-keys"

	"luna-pages/app/store"
)

// MaxBody is the largest request body: a project (2 MiB) with its HTML,
// CSS and values.
const MaxBody = 4 << 20

// API serves /v1 and, with the same handlers, the dashboard's internal API.
type API struct {
	App   *fullstack.App
	cache *cache
}

// NewAPI makes the handlers (one rendered-page cache for both groups).
func NewAPI(app *fullstack.App) API { return API{App: app, cache: newCache()} }

func (h API) store() (*store.Store, error) { return fullstack.Make[*store.Store](h.App) }

// app is the application of the key (or of the internal path).
func app(c *web.Ctx) (string, string) {
	k, _ := keys.FromContext(c.Context())
	by := "dashboard"
	if k.ID != "" {
		by = "key:" + k.ID
	}
	return k.AppID, by
}

// Ping tells a client which application its key belongs to.
func (h API) Ping(c *web.Ctx) error {
	k, _ := keys.FromContext(c.Context())
	return c.JSON(http.StatusOK, map[string]any{"app_id": k.AppID, "app_name": k.AppName, "scopes": k.Scopes})
}

// storeError maps the store's errors to HTTP answers.
func storeError(err error) error {
	var fe *store.FieldError
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &fe):
		return invalid(fe.Field, fe.Message)
	case errors.Is(err, store.ErrNotFound):
		return web.Error(http.StatusNotFound, "Not found.")
	case errors.Is(err, store.ErrVersion):
		return web.Error(http.StatusPreconditionFailed, "Changed since you loaded it (If-Match does not match the current version).")
	case errors.Is(err, store.ErrInUse):
		return web.Error(http.StatusConflict, "Pages still use this template.")
	case errors.As(err, &tooBig):
		return web.Error(http.StatusRequestEntityTooLarge, "The request body is too large.")
	}
	return err
}

// invalid is fullstack-validation's 422 shape: {"error", "errors": {field: [message]}}.
func invalid(field, message string) error {
	return &web.HTTPError{Status: http.StatusUnprocessableEntity, Message: "The given data was invalid.",
		Fields: map[string]any{"errors": map[string][]string{field: {message}}}}
}

func decode(c *web.Ctx, v any) error {
	c.R.Body = http.MaxBytesReader(c.W, c.R.Body, MaxBody)
	if err := json.NewDecoder(c.R.Body).Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return storeError(err)
		}
		return web.Error(http.StatusBadRequest, "The body must be JSON.")
	}
	return nil
}

func ifMatch(c *web.Ctx) (*int64, error) {
	v, ok := store.ParseIfMatch(c.R.Header.Get("If-Match"))
	if !ok {
		return nil, web.Error(http.StatusBadRequest, `If-Match must be a version, e.g. "7".`)
	}
	return v, nil
}

func etag(c *web.Ctx, version int64) {
	c.W.Header().Set("ETag", `"`+strconv.FormatInt(version, 10)+`"`)
	c.W.Header().Set("Cache-Control", "no-store")
}

func kindOf(c *web.Ctx) (store.Kind, error) {
	k, ok := store.KindOf(c.Param("kind"))
	if !ok {
		return "", web.Error(http.StatusNotFound, "Not found.")
	}
	return k, nil
}

// ---- the editor contract v2 ----

// List is GET …/editor/{layouts|templates|pages}: {"items": [headers]}.
func (h API) List(c *web.Ctx) error {
	k, err := kindOf(c)
	if err != nil {
		return err
	}
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	items, err := st.List(c.Context(), a, k)
	if err != nil {
		return err
	}
	c.W.Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// Get is GET …/editor/{kind}/{id}: the header, the draft project in
// "data" and, for pages, the draft values.
func (h API) Get(c *web.Ctx) error {
	k, err := kindOf(c)
	if err != nil {
		return err
	}
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	rec, err := st.Get(c.Context(), a, k, c.Param("id"))
	if err != nil {
		return storeError(err)
	}
	etag(c, rec.Version)
	return c.JSON(http.StatusOK, rec)
}

// Put is PUT …/editor/{kind}/{id}: creates (201) or replaces (200);
// If-Match makes it conditional (412 when the version moved on).
func (h API) Put(c *web.Ctx) error {
	k, err := kindOf(c)
	if err != nil {
		return err
	}
	cond, err := ifMatch(c)
	if err != nil {
		return err
	}
	var in store.Input
	if err := decode(c, &in); err != nil {
		return err
	}
	st, err := h.store()
	if err != nil {
		return err
	}
	a, by := app(c)
	rec, created, err := st.Put(c.Context(), a, k, c.Param("id"), in, cond, by)
	if err != nil {
		return storeError(err)
	}
	etag(c, rec.Version)
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	return c.JSON(status, rec)
}

// Delete is DELETE …/editor/{kind}/{id}: 204, 404 when already gone,
// 409 for a template with entries.
func (h API) Delete(c *web.Ctx) error {
	k, err := kindOf(c)
	if err != nil {
		return err
	}
	cond, err := ifMatch(c)
	if err != nil {
		return err
	}
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	if err := st.Delete(c.Context(), a, k, c.Param("id"), cond); err != nil {
		return storeError(err)
	}
	return c.NoContent()
}

// GetSite is GET …/editor/site.
func (h API) GetSite(c *web.Ctx) error {
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	rec, err := st.GetSite(c.Context(), a)
	if err != nil {
		return storeError(err)
	}
	etag(c, rec.Version)
	return c.JSON(http.StatusOK, rec)
}

// PutSite is PUT …/editor/site.
func (h API) PutSite(c *web.Ctx) error {
	cond, err := ifMatch(c)
	if err != nil {
		return err
	}
	var in store.SiteInput
	if err := decode(c, &in); err != nil {
		return err
	}
	st, err := h.store()
	if err != nil {
		return err
	}
	a, by := app(c)
	rec, err := st.PutSite(c.Context(), a, in, cond, by)
	if err != nil {
		return storeError(err)
	}
	etag(c, rec.Version)
	return c.JSON(http.StatusOK, rec)
}
