package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"orm"
)

// View is everything a page's rendering needs: its live design (a free
// page's HTML, or its template's with the field list), its layout, the
// CSS in order (site, layout, page or template) and its values. Tag
// changes whenever any of them can change the result.
type View struct {
	Page        Record
	PublishedAt *time.Time
	Entry       bool
	Body        string
	Layout      string
	CSS         []string
	Fields      []Field
	Values      map[string]json.RawMessage
	Settings    json.RawMessage
	Tag         string
}

// Summary is a published page as the read API lists it: its header and
// values split into field values, SEO and the client's own keys.
type Summary struct {
	ID          string                     `json:"id"`
	Slug        string                     `json:"slug"`
	Title       string                     `json:"title"`
	Kind        string                     `json:"kind"`
	TemplateID  *string                    `json:"template_id"`
	Template    *string                    `json:"template"`
	ParentID    *string                    `json:"parent_id"`
	Position    int                        `json:"position"`
	PublishedAt *time.Time                 `json:"published_at"`
	Fields      map[string]json.RawMessage `json:"fields"`
	SEO         map[string]json.RawMessage `json:"seo"`
	Meta        map[string]json.RawMessage `json:"meta"`
}

// Split sorts a page's values: field.<name> into fields, seo.<name> into
// seo, the rest into meta.
func Split(vals map[string]json.RawMessage) (fields, seo, meta map[string]json.RawMessage) {
	fields, seo, meta = map[string]json.RawMessage{}, map[string]json.RawMessage{}, map[string]json.RawMessage{}
	for k, v := range vals {
		switch {
		case strings.HasPrefix(k, "field."):
			fields[k[len("field."):]] = v
		case strings.HasPrefix(k, "seo."):
			seo[k[len("seo."):]] = v
		default:
			meta[k] = v
		}
	}
	return fields, seo, meta
}

// PageView is the view of the page at slug (or id): published only, or
// with draft values when draft is set (designs are always the published
// ones: the editor previews its own drafts). ErrNotFound for a page that
// is not live or never had a design published.
func (s *Store) PageView(ctx context.Context, app, slugOrID string, draft bool) (View, error) {
	db, err := s.db()
	if err != nil {
		return View{}, err
	}
	var v View
	var publishedAt sql.NullTime
	if UUIDLike(strings.ToLower(slugOrID)) {
		v.Page, publishedAt, err = pageBy(ctx, db, app, "id", strings.ToLower(slugOrID))
	} else {
		slug, nerr := NormalizeSlug(slugOrID)
		if nerr != nil {
			return View{}, ErrNotFound
		}
		v.Page, publishedAt, err = pageBy(ctx, db, app, "slug", slug)
	}
	if err != nil {
		return View{}, err
	}
	if !draft && v.Page.Status != "published" {
		return View{}, ErrNotFound
	}
	if publishedAt.Valid {
		t := publishedAt.Time.UTC()
		v.PublishedAt = &t
	}
	tag := []string{v.Page.ID, fmt.Sprint(publishedAt.Time.UnixNano())}
	var bodyCSS string
	if v.Page.TemplateID != nil {
		v.Entry = true
		t, err := get(ctx, db, app, Template, *v.Page.TemplateID)
		if err != nil {
			return View{}, err
		}
		body, err := latest(ctx, db, app, Template, t.ID)
		if err != nil {
			return View{}, err
		}
		v.Fields, v.Body, bodyCSS = t.Fields, body.HTML, body.CSS
		// The fields change without the version (the content API): in the tag too.
		fields, _ := json.Marshal(t.Fields)
		tag = append(tag, fmt.Sprintf("t%d.%d.%x", t.Version, body.Revision, sha256.Sum256(fields)))
	} else {
		body, err := latest(ctx, db, app, Page, v.Page.ID)
		if err != nil {
			return View{}, err
		}
		v.Body, bodyCSS = body.HTML, body.CSS
		tag = append(tag, fmt.Sprintf("p%d", body.Revision))
	}
	if site, err := latest(ctx, db, app, Site, app); err == nil {
		v.CSS = append(v.CSS, site.CSS)
		tag = append(tag, fmt.Sprintf("s%d", site.Revision))
	} else if !errors.Is(err, ErrNotFound) {
		return View{}, err
	}
	if v.Page.LayoutID != nil {
		if l, err := latest(ctx, db, app, Layout, *v.Page.LayoutID); err == nil {
			v.Layout = l.HTML
			v.CSS = append(v.CSS, l.CSS)
			tag = append(tag, fmt.Sprintf("l%d", l.Revision))
		} else if !errors.Is(err, ErrNotFound) {
			return View{}, err
		}
	}
	v.CSS = append(v.CSS, bodyCSS)
	site, err := site(ctx, db, app, false)
	switch {
	case err == nil:
		v.Settings = site.Settings
		tag = append(tag, fmt.Sprintf("v%d", site.Version))
	case !errors.Is(err, ErrNotFound):
		return View{}, err
	}
	state := "published"
	if draft {
		state = "draft"
		tag = append(tag, "draft", fmt.Sprint(v.Page.Version))
	}
	if v.Values, err = values(ctx, db, app, v.Page.ID, state); err != nil {
		return View{}, err
	}
	sum := sha256.Sum256([]byte(app + "|" + strings.Join(tag, "|")))
	v.Tag = hex.EncodeToString(sum[:12])
	return v, nil
}

func pageBy(ctx context.Context, db orm.SQLExecutor, app, column, value string) (Record, sql.NullTime, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+columns(Page)+` FROM pages WHERE app_id = $1 AND `+column+` = $2`, app, value)
	if err != nil {
		return Record{}, sql.NullTime{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Record{}, sql.NullTime{}, err
		}
		return Record{}, sql.NullTime{}, ErrNotFound
	}
	r, err := scanRecord(Page, rows)
	var at sql.NullTime
	if r.PublishedAt != nil {
		at = sql.NullTime{Time: *r.PublishedAt, Valid: true}
	}
	return r, at, err
}

// ListQuery filters and pages the published pages.
type ListQuery struct {
	Template string // a template's id or name; "" any page
	Parent   string // a parent page's id; "" any
	Page     int    // from 1
	PerPage  int    // 1 to 100
	Sort     string // published_at, title, slug or position; "-" first for descending
}

var sorts = map[string]string{
	"published_at": "p.published_at", "title": "p.title", "slug": "p.slug", "position": "p.position",
}

// ParseSort checks a sort key: "-published_at" is the default.
func ParseSort(s string) (string, error) {
	if s == "" {
		return "-published_at", nil
	}
	if _, ok := sorts[strings.TrimPrefix(s, "-")]; !ok {
		return "", invalid("sort", "Sort by published_at, title, slug or position (a leading - for descending).")
	}
	return s, nil
}

// ListPublished is a page of the published pages and how many there are.
func (s *Store) ListPublished(ctx context.Context, app string, q ListQuery) ([]Summary, int, error) {
	db, err := s.db()
	if err != nil {
		return nil, 0, err
	}
	sort, err := ParseSort(q.Sort)
	if err != nil {
		return nil, 0, err
	}
	dir := "ASC"
	if strings.HasPrefix(sort, "-") {
		dir, sort = "DESC", sort[1:]
	}
	where := []string{"p.app_id = $1", "p.status = 'published'"}
	args := []any{app}
	if q.Template != "" {
		args = append(args, strings.ToLower(q.Template))
		if UUIDLike(strings.ToLower(q.Template)) {
			where = append(where, fmt.Sprintf("p.template_id = $%d", len(args)))
		} else {
			where = append(where, fmt.Sprintf("lower(t.name) = $%d", len(args)))
		}
	}
	if q.Parent != "" {
		if !UUIDLike(strings.ToLower(q.Parent)) {
			return []Summary{}, 0, nil
		}
		args = append(args, strings.ToLower(q.Parent))
		where = append(where, fmt.Sprintf("p.parent_id = $%d", len(args)))
	}
	from := ` FROM pages p LEFT JOIN templates t ON t.app_id = p.app_id AND t.id = p.template_id WHERE ` + strings.Join(where, " AND ")

	var total int
	rows, err := db.QueryContext(ctx, `SELECT count(*)`+from, args...)
	if err != nil {
		return nil, 0, err
	}
	if rows.Next() {
		err = rows.Scan(&total)
	}
	rows.Close()
	if err != nil {
		return nil, 0, err
	}

	args = append(args, q.PerPage, (q.Page-1)*q.PerPage)
	rows, err = db.QueryContext(ctx, `SELECT p.id, p.slug, p.title, p.kind, p.template_id, t.name, p.parent_id, p.position, p.published_at`+from+
		fmt.Sprintf(` ORDER BY %s %s NULLS LAST, p.id LIMIT $%d OFFSET $%d`, sorts[sort], dir, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	items, err := scanSummaries(rows)
	if err != nil {
		return nil, 0, err
	}
	if err := withValues(ctx, db, app, items); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// PublishedPage is one published page's header and values.
func (s *Store) PublishedPage(ctx context.Context, app, id string) (Summary, error) {
	db, err := s.db()
	if err != nil {
		return Summary{}, err
	}
	id = strings.ToLower(id)
	if !UUIDLike(id) {
		return Summary{}, ErrNotFound
	}
	rows, err := db.QueryContext(ctx, `SELECT p.id, p.slug, p.title, p.kind, p.template_id, t.name, p.parent_id, p.position, p.published_at
		FROM pages p LEFT JOIN templates t ON t.app_id = p.app_id AND t.id = p.template_id
		WHERE p.app_id = $1 AND p.id = $2 AND p.status = 'published'`, app, id)
	if err != nil {
		return Summary{}, err
	}
	items, err := scanSummaries(rows)
	if err != nil {
		return Summary{}, err
	}
	if len(items) == 0 {
		return Summary{}, ErrNotFound
	}
	if err := withValues(ctx, db, app, items); err != nil {
		return Summary{}, err
	}
	return items[0], nil
}

func scanSummaries(rows orm.Rows) ([]Summary, error) {
	defer rows.Close()
	out := []Summary{}
	for rows.Next() {
		var p Summary
		var template, name, parent sql.NullString
		var at sql.NullTime
		if err := rows.Scan(&p.ID, &p.Slug, &p.Title, &p.Kind, &template, &name, &parent, &p.Position, &at); err != nil {
			return nil, err
		}
		p.TemplateID, p.Template, p.ParentID = nullable(template), nullable(name), nullable(parent)
		if at.Valid {
			t := at.Time.UTC()
			p.PublishedAt = &t
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// withValues loads the published values of the pages in one query.
func withValues(ctx context.Context, db orm.SQLExecutor, app string, items []Summary) error {
	if len(items) == 0 {
		return nil
	}
	byID := make(map[string]*Summary, len(items))
	ids := make([]string, 0, len(items))
	for i := range items {
		items[i].Fields, items[i].SEO, items[i].Meta = Split(nil)
		byID[items[i].ID] = &items[i]
		ids = append(ids, items[i].ID)
	}
	rows, err := db.QueryContext(ctx, `SELECT page_id, key, value FROM metadata
		WHERE app_id = $1 AND state = 'published' AND page_id::text = ANY(string_to_array($2, ','))`, app, strings.Join(ids, ","))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, key string
		var value []byte
		if err := rows.Scan(&id, &key, &value); err != nil {
			return err
		}
		p := byID[id]
		if p == nil {
			continue
		}
		v := json.RawMessage(value)
		switch {
		case strings.HasPrefix(key, "field."):
			p.Fields[key[len("field."):]] = v
		case strings.HasPrefix(key, "seo."):
			p.SEO[key[len("seo."):]] = v
		default:
			p.Meta[key] = v
		}
	}
	return rows.Err()
}

// PublicSite is the site as the read API shows it.
type PublicSite struct {
	Name     string          `json:"name"`
	Settings json.RawMessage `json:"settings"`
	CSS      string          `json:"css"`
	Menu     []MenuItem      `json:"menu"`
	Version  string          `json:"version"`
}

// MenuItem is a published page with the value in_menu = true.
type MenuItem struct {
	ID       string  `json:"id"`
	Slug     string  `json:"slug"`
	Title    string  `json:"title"`
	ParentID *string `json:"parent_id"`
	Position int     `json:"position"`
}

// Site is the public site: name, settings, published global CSS and menu.
func (s *Store) Site(ctx context.Context, app string) (PublicSite, error) {
	var out PublicSite
	err := s.tx(ctx, func(tx orm.SQLExecutor) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO sites (app_id) VALUES ($1) ON CONFLICT DO NOTHING`, app); err != nil {
			return err
		}
		st, err := site(ctx, tx, app, false)
		if err != nil {
			return err
		}
		out.Name, out.Settings = st.Name, st.Settings
		rev := 0
		if p, err := latest(ctx, tx, app, Site, app); err == nil {
			out.CSS, rev = p.CSS, p.Revision
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT p.id, p.slug, p.title, p.parent_id, p.position, p.published_at FROM pages p
			JOIN metadata m ON m.app_id = p.app_id AND m.page_id = p.id AND m.state = 'published' AND m.key = 'in_menu' AND m.value = 'true'::jsonb
			WHERE p.app_id = $1 AND p.status = 'published' ORDER BY p.position, p.title, p.id`, app)
		if err != nil {
			return err
		}
		defer rows.Close()
		out.Menu = []MenuItem{}
		var last time.Time
		for rows.Next() {
			var m MenuItem
			var parent sql.NullString
			var at sql.NullTime
			if err := rows.Scan(&m.ID, &m.Slug, &m.Title, &parent, &m.Position, &at); err != nil {
				return err
			}
			m.ParentID = nullable(parent)
			if at.Time.After(last) {
				last = at.Time
			}
			out.Menu = append(out.Menu, m)
		}
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d|%d|%d", app, st.Version, rev, len(out.Menu), last.UnixNano())))
		out.Version = hex.EncodeToString(sum[:12])
		return rows.Err()
	})
	return out, err
}
