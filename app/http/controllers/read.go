package controllers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"fullstack/web"
	"luna-keys"

	"luna-pages/app/render"
	"luna-pages/app/store"
)

// MaxAge is how long a client or CDN may keep a published answer.
const MaxAge = 60

// Rendered is a page as GET …/render answers it.
type Rendered struct {
	ID          string                     `json:"id"`
	Slug        string                     `json:"slug"`
	Title       string                     `json:"title"`
	Kind        string                     `json:"kind"`
	TemplateID  *string                    `json:"template_id"`
	PublishedAt *time.Time                 `json:"published_at"`
	HTML        string                     `json:"html"`
	CSS         string                     `json:"css"`
	SEO         map[string]json.RawMessage `json:"seo"`
	Meta        map[string]json.RawMessage `json:"meta"`
	Version     string                     `json:"version"`
	Draft       bool                       `json:"draft,omitempty"`
}

// cache keeps rendered pages by their tag, which changes with anything
// that changes the result; publishing clears an application's entries.
type cache struct {
	mu      sync.Mutex
	entries map[string]Rendered
}

const cacheSize = 1000

func newCache() *cache { return &cache{entries: map[string]Rendered{}} }

func (c *cache) get(app, tag string) (Rendered, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.entries[app+"|"+tag]
	return r, ok
}

func (c *cache) put(app, tag string, r Rendered) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= cacheSize {
		c.entries = map[string]Rendered{} // simple and bounded: start over
	}
	c.entries[app+"|"+tag] = r
}

func (c *cache) clear(app string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.entries {
		if strings.HasPrefix(k, app+"|") {
			delete(c.entries, k)
		}
	}
}

// draft reports whether the request asks for drafts (?draft=1), which
// needs pages:write (the dashboard's internal API has it).
func draft(c *web.Ctx) (bool, error) {
	v := c.R.URL.Query().Get("draft")
	if v == "" || v == "0" || v == "false" {
		return false, nil
	}
	k, _ := keys.FromContext(c.Context())
	if k == nil || !k.Has(keys.PagesWrite) {
		return false, web.Error(http.StatusForbidden, "Drafts need a key with pages:write.")
	}
	return true, nil
}

// cacheable answers a published resource: its ETag, Cache-Control, and
// 304 when the client has it. true: the 304 is sent.
func cacheable(c *web.Ctx, tag string, isDraft bool) bool {
	h := c.W.Header()
	if isDraft {
		h.Set("Cache-Control", "no-store")
		return false
	}
	etag := `"` + tag + `"`
	h.Set("ETag", etag)
	h.Set("Cache-Control", "public, max-age="+strconv.Itoa(MaxAge))
	for _, m := range strings.Split(c.R.Header.Get("If-None-Match"), ",") {
		m = strings.TrimPrefix(strings.TrimSpace(m), "W/")
		if m == etag || m == "*" {
			c.W.WriteHeader(http.StatusNotModified)
			return true
		}
	}
	return false
}

// Render is GET …/render?slug=/products/coffee-grinder (or ?id=): the
// page's finished HTML in its layout, its CSS (site, layout, page), SEO
// and the client's own values. ?draft=1 fills an entry with its draft
// values (pages:write).
func (h API) Render(c *web.Ctx) error {
	isDraft, err := draft(c)
	if err != nil {
		return err
	}
	q := c.R.URL.Query()
	ref := q.Get("slug")
	if id := q.Get("id"); id != "" {
		ref = id
	}
	if ref == "" {
		return invalid("slug", "Give the page's slug (or id).")
	}
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	v, err := st.PageView(c.Context(), a, ref, isDraft)
	if err != nil {
		return storeError(err)
	}
	if cacheable(c, v.Tag, isDraft) {
		return nil
	}
	if r, ok := h.cache.get(a, v.Tag); ok && !isDraft {
		return c.JSON(http.StatusOK, r)
	}
	r, err := renderView(v)
	if err != nil {
		return err
	}
	r.Draft = isDraft
	if !isDraft {
		h.cache.put(a, v.Tag, r)
	}
	return c.JSON(http.StatusOK, r)
}

func renderView(v store.View) (Rendered, error) {
	body := v.Body
	if v.Entry {
		var err error
		if body, err = render.Fill(v.Body, store.RenderFields(v.Fields), store.FieldValues(v.Values), store.SitePolicy(v.Settings)); err != nil {
			return Rendered{}, err
		}
	}
	page, err := render.Compose(v.Layout, body)
	if err != nil {
		return Rendered{}, err
	}
	var css []string
	for _, s := range v.CSS {
		if strings.TrimSpace(s) != "" {
			css = append(css, s)
		}
	}
	_, seo, meta := store.Split(v.Values)
	return Rendered{
		ID: v.Page.ID, Slug: v.Page.Slug, Title: v.Page.Name, Kind: v.Page.PageKind, TemplateID: v.Page.TemplateID,
		PublishedAt: v.PublishedAt, HTML: page, CSS: strings.Join(css, "\n"), SEO: seo, Meta: meta, Version: v.Tag,
	}, nil
}

// Pages is GET …/pages?template=&parent=&page=&per_page=&sort=: published
// pages with their values, newest first by default (a catalogue).
func (h API) Pages(c *web.Ctx) error {
	q := c.R.URL.Query()
	page, err := intParam(q.Get("page"), 1, 1, 1<<20, "page")
	if err != nil {
		return err
	}
	per, err := intParam(q.Get("per_page"), 20, 1, 100, "per_page")
	if err != nil {
		return err
	}
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	items, total, err := st.ListPublished(c.Context(), a, store.ListQuery{
		Template: q.Get("template"), Parent: q.Get("parent"), Page: page, PerPage: per, Sort: q.Get("sort"),
	})
	if err != nil {
		return storeError(err)
	}
	c.W.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(MaxAge))
	return c.JSON(http.StatusOK, map[string]any{"items": items, "page": page, "per_page": per, "total": total})
}

// Page is GET …/pages/{id}: one published page's header and values.
func (h API) Page(c *web.Ctx) error {
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	p, err := st.PublishedPage(c.Context(), a, c.Param("id"))
	if err != nil {
		return storeError(err)
	}
	c.W.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(MaxAge))
	return c.JSON(http.StatusOK, p)
}

// Site is GET …/site: the site's name, settings, global CSS and menu.
func (h API) Site(c *web.Ctx) error {
	st, err := h.store()
	if err != nil {
		return err
	}
	a, _ := app(c)
	s, err := st.Site(c.Context(), a)
	if err != nil {
		return storeError(err)
	}
	if cacheable(c, s.Version, false) {
		return nil
	}
	return c.JSON(http.StatusOK, s)
}

func intParam(s string, def, lo, hi int, name string) (int, error) {
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < lo || n > hi {
		return 0, invalid(name, "Must be a whole number from "+strconv.Itoa(lo)+" to "+strconv.Itoa(hi)+".")
	}
	return n, nil
}
