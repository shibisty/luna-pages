package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"orm"
)

func table(k Kind) string {
	switch k {
	case Layout:
		return "layouts"
	case Template:
		return "templates"
	}
	return "pages"
}

// columns per kind, in scan order after the common ones.
func columns(k Kind) string {
	common := "id, version, created_at, updated_at"
	switch k {
	case Layout:
		return common + ", name"
	case Template:
		return common + ", name, fields"
	}
	return common + ", title, slug, kind, status, layout_id, template_id, parent_id, position, published_at"
}

type scanner interface{ Scan(...any) error }

func scanRecord(k Kind, row scanner) (Record, error) {
	r := Record{Kind: k}
	common := []any{&r.ID, &r.Version, &r.CreatedAt, &r.UpdatedAt}
	var err error
	switch k {
	case Layout:
		err = row.Scan(append(common, &r.Name)...)
	case Template:
		var fields []byte
		err = row.Scan(append(common, &r.Name, &fields)...)
		if err == nil && len(fields) > 0 {
			err = json.Unmarshal(fields, &r.Fields)
		}
	default:
		var layout, template, parent sql.NullString
		var published sql.NullTime
		err = row.Scan(append(common, &r.Name, &r.Slug, &r.PageKind, &r.Status, &layout, &template, &parent, &r.Position, &published)...)
		r.LayoutID, r.TemplateID, r.ParentID = nullable(layout), nullable(template), nullable(parent)
		if published.Valid {
			t := published.Time.UTC()
			r.PublishedAt = &t
		}
	}
	r.CreatedAt, r.UpdatedAt = r.CreatedAt.UTC(), r.UpdatedAt.UTC()
	return r, err
}

func nullable(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}

// List is every record of a kind, without projects, oldest first.
func (s *Store) List(ctx context.Context, app string, k Kind) ([]Record, error) {
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	order := "created_at, id"
	if k == Page {
		order = "position, created_at, id"
	}
	rows, err := db.QueryContext(ctx, `SELECT `+columns(k)+` FROM `+table(k)+` WHERE app_id = $1 ORDER BY `+order, app)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		r, err := scanRecord(k, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Get is one record with its draft project and, for pages, draft values.
func (s *Store) Get(ctx context.Context, app string, k Kind, id string) (Record, error) {
	var r Record
	err := s.tx(ctx, func(tx orm.SQLExecutor) error {
		var err error
		r, err = get(ctx, tx, app, k, id)
		if err != nil {
			return err
		}
		if r.Data, err = draft(ctx, tx, app, k, id); err != nil {
			return err
		}
		if k == Page {
			r.Values, err = values(ctx, tx, app, id, "draft")
		}
		return err
	})
	return r, err
}

func get(ctx context.Context, db orm.SQLExecutor, app string, k Kind, id string) (Record, error) {
	id = strings.ToLower(id)
	if !UUIDLike(id) {
		return Record{}, ErrNotFound
	}
	rows, err := db.QueryContext(ctx, `SELECT `+columns(k)+` FROM `+table(k)+` WHERE app_id = $1 AND id = $2`, app, id)
	if err != nil {
		return Record{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Record{}, err
		}
		return Record{}, ErrNotFound
	}
	return scanRecord(k, rows)
}

func draft(ctx context.Context, db orm.SQLExecutor, app string, k Kind, owner string) (json.RawMessage, error) {
	rows, err := db.QueryContext(ctx, `SELECT project FROM content WHERE app_id = $1 AND owner_kind = $2 AND owner_id = $3 AND state = 'draft'`, app, string(k), owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var p []byte
	if err := rows.Scan(&p); err != nil {
		return nil, err
	}
	return json.RawMessage(p), nil
}

func values(ctx context.Context, db orm.SQLExecutor, app, page, state string) (map[string]json.RawMessage, error) {
	rows, err := db.QueryContext(ctx, `SELECT key, value FROM metadata WHERE app_id = $1 AND page_id = $2 AND state = $3 ORDER BY key`, app, page, state)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var k string
		var v []byte
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = json.RawMessage(v)
	}
	return out, rows.Err()
}

// ParseIfMatch reads an If-Match header: "" → no condition; `"7"`, `7`,
// `W/"7"` → 7.
func ParseIfMatch(h string) (*int64, bool) {
	h = strings.TrimSpace(h)
	if h == "" {
		return nil, true
	}
	h = strings.TrimPrefix(h, "W/")
	h = strings.Trim(h, `"`)
	v, err := strconv.ParseInt(h, 10, 64)
	if err != nil || v < 1 {
		return nil, false
	}
	return &v, true
}

// Put creates the record (when it does not exist and ifMatch is nil) or
// replaces it, and its draft project and values. created reports a new
// record. ifMatch: the version the editor based its edits on; nil writes
// unconditionally (the editor's "keep mine").
func (s *Store) Put(ctx context.Context, app string, k Kind, id string, in Input, ifMatch *int64, by string) (rec Record, created bool, err error) {
	id = strings.ToLower(id)
	if !UUIDLike(id) {
		return Record{}, false, invalid("id", "The id must be a UUID the editor generates.")
	}
	name, err := cleanName(in.Name)
	if err != nil {
		return Record{}, false, err
	}
	if err := checkProject(in.Data); err != nil {
		return Record{}, false, err
	}
	var layout, template, parent *string
	if k == Page {
		if layout, err = optionalID("layout_id", in.LayoutID); err != nil {
			return Record{}, false, err
		}
		if template, err = optionalID("template_id", in.TemplateID); err != nil {
			return Record{}, false, err
		}
		if parent, err = optionalID("parent_id", in.ParentID); err != nil {
			return Record{}, false, err
		}
		if parent != nil && *parent == id {
			return Record{}, false, invalid("parent_id", "A page cannot be its own parent.")
		}
		if err := checkValues(in.Values); err != nil {
			return Record{}, false, err
		}
	}
	if k == Template {
		if err := checkFields(in.Fields, false); err != nil {
			return Record{}, false, err
		}
	}

	err = s.tx(ctx, func(tx orm.SQLExecutor) error {
		cur, err := lock(ctx, tx, app, k, id)
		switch {
		case errors.Is(err, ErrNotFound):
			if ifMatch != nil {
				return ErrVersion
			}
			created = true
		case err != nil:
			return err
		case ifMatch != nil && *ifMatch != cur.Version:
			return ErrVersion
		}
		if created {
			if err := underLimit(ctx, tx, app, k); err != nil {
				return err
			}
		}
		switch k {
		case Layout:
			err = upsert(ctx, tx, `INSERT INTO layouts (app_id, id, name) VALUES ($1, $2, $3)
				ON CONFLICT (app_id, id) DO UPDATE SET name = EXCLUDED.name, version = layouts.version + 1, updated_at = now() AT TIME ZONE 'UTC'`,
				app, id, name)
		case Template:
			// Omitted fields stay (grapesjs-pages saves a template's design
			// without knowing its fields); [] clears them.
			list := in.Fields
			if list == nil && !created {
				list = cur.Fields
			}
			fields, _ := json.Marshal(nonNil(list))
			err = upsert(ctx, tx, `INSERT INTO templates (app_id, id, name, fields) VALUES ($1, $2, $3, $4)
				ON CONFLICT (app_id, id) DO UPDATE SET name = EXCLUDED.name, fields = EXCLUDED.fields, version = templates.version + 1, updated_at = now() AT TIME ZONE 'UTC'`,
				app, id, name, string(fields))
		default:
			err = s.putPage(ctx, tx, app, id, name, in, cur, created, layout, template, parent)
		}
		if err != nil {
			return err
		}
		if len(in.Data) > 0 {
			if err := saveDraft(ctx, tx, app, k, id, in.Data, by); err != nil {
				return err
			}
		}
		if k == Page && in.Values != nil {
			if err := saveValues(ctx, tx, app, id, "draft", in.Values); err != nil {
				return err
			}
		}
		rec, err = get(ctx, tx, app, k, id)
		return err
	})
	return rec, created, err
}

func nonNil(f []Field) []Field {
	if f == nil {
		return []Field{}
	}
	return f
}

func (s *Store) putPage(ctx context.Context, tx orm.SQLExecutor, app, id, title string, in Input, cur Record, created bool, layout, template, parent *string) error {
	// Slug: given, kept, or made from the title (unique within the app).
	var slug string
	switch {
	case in.Slug != nil:
		var err error
		if slug, err = NormalizeSlug(*in.Slug); err != nil {
			return err
		}
	case !created:
		slug = cur.Slug
	default:
		var err error
		if slug, err = freeSlug(ctx, tx, app, Slugify(title)); err != nil {
			return err
		}
	}
	if !created && in.TemplateID == nil {
		template = cur.TemplateID // v1 editors do not send it: keep it
	}
	if !created && in.LayoutID == nil {
		layout = cur.LayoutID
	}
	if !created && in.ParentID == nil {
		parent = cur.ParentID
	}
	position := cur.Position
	if in.Position != nil {
		position = *in.Position
	}
	kind := "free"
	if template != nil {
		kind = "entry"
	}
	for ref, want := range map[*string]Kind{layout: Layout, template: Template, parent: Page} {
		if ref == nil {
			continue
		}
		if _, err := get(ctx, tx, app, want, *ref); errors.Is(err, ErrNotFound) {
			return invalid(string(want)+"_id", "No such %s.", want)
		} else if err != nil {
			return err
		}
	}
	// Checked first: a unique violation would abort a surrounding
	// transaction; the index still guards against a race.
	rows, err := tx.QueryContext(ctx, `SELECT 1 FROM pages WHERE app_id = $1 AND slug = $2 AND id <> $3`, app, slug, id)
	if err != nil {
		return err
	}
	taken := rows.Next()
	rows.Close()
	if taken {
		return invalid("slug", "Another page already has the slug %s.", slug)
	}
	err = upsert(ctx, tx, `INSERT INTO pages (app_id, id, title, slug, kind, layout_id, template_id, parent_id, position)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (app_id, id) DO UPDATE SET title = EXCLUDED.title, slug = EXCLUDED.slug, kind = EXCLUDED.kind,
			layout_id = EXCLUDED.layout_id, template_id = EXCLUDED.template_id, parent_id = EXCLUDED.parent_id,
			position = EXCLUDED.position, version = pages.version + 1, updated_at = now() AT TIME ZONE 'UTC'`,
		app, id, title, slug, kind, layout, template, parent, position)
	if isUnique(err, "slug") {
		return invalid("slug", "Another page already has the slug %s.", slug)
	}
	return err
}

// freeSlug is base, or base-2, base-3… when taken.
func freeSlug(ctx context.Context, db orm.SQLExecutor, app, base string) (string, error) {
	for i := 1; i < 1000; i++ {
		slug := base
		if i > 1 {
			slug = base + "-" + strconv.Itoa(i)
		}
		rows, err := db.QueryContext(ctx, `SELECT 1 FROM pages WHERE app_id = $1 AND slug = $2`, app, slug)
		if err != nil {
			return "", err
		}
		taken := rows.Next()
		rows.Close()
		if !taken {
			return slug, nil
		}
	}
	return "", invalid("slug", "Choose a slug: %s is taken.", base)
}

// lock reads a record FOR UPDATE.
func lock(ctx context.Context, db orm.SQLExecutor, app string, k Kind, id string) (Record, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+columns(k)+` FROM `+table(k)+` WHERE app_id = $1 AND id = $2 FOR UPDATE`, app, id)
	if err != nil {
		return Record{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Record{}, err
		}
		return Record{}, ErrNotFound
	}
	return scanRecord(k, rows)
}

func underLimit(ctx context.Context, db orm.SQLExecutor, app string, k Kind) error {
	rows, err := db.QueryContext(ctx, `SELECT count(*) FROM `+table(k)+` WHERE app_id = $1`, app)
	if err != nil {
		return err
	}
	defer rows.Close()
	var n int
	if rows.Next() {
		if err := rows.Scan(&n); err != nil {
			return err
		}
	}
	if n >= MaxRecords {
		return invalid("id", "An application may have at most %d %ss.", MaxRecords, k)
	}
	return rows.Err()
}

func upsert(ctx context.Context, db orm.SQLExecutor, q string, args ...any) error {
	_, err := db.ExecContext(ctx, q, args...)
	return err
}

func isUnique(err error, column string) bool {
	return err != nil && strings.Contains(err.Error(), "23505") && strings.Contains(err.Error(), column)
}

func saveDraft(ctx context.Context, db orm.SQLExecutor, app string, k Kind, owner string, project json.RawMessage, by string) error {
	var p any = string(project)
	if string(project) == "null" {
		p = nil
	}
	_, err := db.ExecContext(ctx, `INSERT INTO content (app_id, owner_kind, owner_id, state, project, created_by)
		VALUES ($1, $2, $3, 'draft', $4, $5)
		ON CONFLICT (app_id, owner_kind, owner_id) WHERE state = 'draft'
		DO UPDATE SET project = EXCLUDED.project, created_by = EXCLUDED.created_by, created_at = now() AT TIME ZONE 'UTC'`,
		app, string(k), owner, p, by)
	return err
}

// saveValues replaces a page's values in a state: the keys given are set,
// a JSON null deletes a key, keys not given stay.
func saveValues(ctx context.Context, db orm.SQLExecutor, app, page, state string, vals map[string]json.RawMessage) error {
	for k, v := range vals {
		if string(v) == "null" {
			if _, err := db.ExecContext(ctx, `DELETE FROM metadata WHERE app_id = $1 AND page_id = $2 AND state = $3 AND key = $4`, app, page, state, k); err != nil {
				return err
			}
			continue
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO metadata (app_id, page_id, state, key, value) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (app_id, page_id, state, key) DO UPDATE SET value = EXCLUDED.value, updated_at = now() AT TIME ZONE 'UTC'`,
			app, page, state, k, string(v)); err != nil {
			return err
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT count(*) FROM metadata WHERE app_id = $1 AND page_id = $2 AND state = $3`, app, page, state)
	if err != nil {
		return err
	}
	defer rows.Close()
	var n int
	if rows.Next() {
		if err := rows.Scan(&n); err != nil {
			return err
		}
	}
	if n > MaxValues {
		return invalid("values", "A page may have at most %d values.", MaxValues)
	}
	return rows.Err()
}

// Delete removes a record and its content. ifMatch as in Put. A template
// with entries is ErrInUse; a deleted layout leaves its pages without one.
func (s *Store) Delete(ctx context.Context, app string, k Kind, id string, ifMatch *int64) error {
	id = strings.ToLower(id)
	if !UUIDLike(id) {
		return ErrNotFound
	}
	return s.tx(ctx, func(tx orm.SQLExecutor) error {
		cur, err := lock(ctx, tx, app, k, id)
		if err != nil {
			if errors.Is(err, ErrNotFound) && ifMatch != nil {
				return ErrVersion
			}
			return err
		}
		if ifMatch != nil && *ifMatch != cur.Version {
			return ErrVersion
		}
		if k == Template {
			rows, err := tx.QueryContext(ctx, `SELECT 1 FROM pages WHERE app_id = $1 AND template_id = $2 LIMIT 1`, app, id)
			if err != nil {
				return err
			}
			used := rows.Next()
			rows.Close()
			if used {
				return ErrInUse
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table(k)+` WHERE app_id = $1 AND id = $2`, app, id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM content WHERE app_id = $1 AND owner_kind = $2 AND owner_id = $3`, app, string(k), id)
		return err
	})
}

// ---- the site ----

// SiteRecord is an application's site.
type SiteRecord struct {
	Name      string          `json:"name"`
	Settings  json.RawMessage `json:"settings"`
	Version   int64           `json:"version"`
	UpdatedAt time.Time       `json:"updated_at"`
	Data      json.RawMessage `json:"data,omitempty"` // the draft project: global CSS, symbols, assets
}

// SiteInput is a write of the site.
type SiteInput struct {
	Name     string          `json:"name"`
	Settings json.RawMessage `json:"settings"`
	Data     json.RawMessage `json:"data"`
}

// GetSite is the application's site, created empty on first use.
func (s *Store) GetSite(ctx context.Context, app string) (SiteRecord, error) {
	var r SiteRecord
	err := s.tx(ctx, func(tx orm.SQLExecutor) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO sites (app_id) VALUES ($1) ON CONFLICT DO NOTHING`, app); err != nil {
			return err
		}
		var err error
		if r, err = site(ctx, tx, app, false); err != nil {
			return err
		}
		r.Data, err = draft(ctx, tx, app, Site, app)
		return err
	})
	return r, err
}

func site(ctx context.Context, db orm.SQLExecutor, app string, forUpdate bool) (SiteRecord, error) {
	q := `SELECT name, settings, version, updated_at FROM sites WHERE app_id = $1`
	if forUpdate {
		q += ` FOR UPDATE`
	}
	rows, err := db.QueryContext(ctx, q, app)
	if err != nil {
		return SiteRecord{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return SiteRecord{}, err
		}
		return SiteRecord{}, ErrNotFound
	}
	var r SiteRecord
	var settings []byte
	err = rows.Scan(&r.Name, &settings, &r.Version, &r.UpdatedAt)
	r.Settings, r.UpdatedAt = json.RawMessage(settings), r.UpdatedAt.UTC()
	return r, err
}

// PutSite replaces the site's name, settings and draft project.
func (s *Store) PutSite(ctx context.Context, app string, in SiteInput, ifMatch *int64, by string) (SiteRecord, error) {
	if len(in.Name) > 200 {
		return SiteRecord{}, invalid("name", "The name may not be longer than 200 characters.")
	}
	settings := in.Settings
	if len(settings) == 0 || string(settings) == "null" {
		settings = json.RawMessage(`{}`)
	}
	var obj map[string]any
	if json.Unmarshal(settings, &obj) != nil || len(settings) > MaxValueBytes {
		return SiteRecord{}, invalid("settings", "The settings must be a JSON object of at most %d bytes.", MaxValueBytes)
	}
	if err := checkProject(in.Data); err != nil {
		return SiteRecord{}, err
	}
	var r SiteRecord
	err := s.tx(ctx, func(tx orm.SQLExecutor) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO sites (app_id) VALUES ($1) ON CONFLICT DO NOTHING`, app); err != nil {
			return err
		}
		cur, err := site(ctx, tx, app, true)
		if err != nil {
			return err
		}
		if ifMatch != nil && *ifMatch != cur.Version {
			return ErrVersion
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sites SET name = $2, settings = $3, version = version + 1, updated_at = now() AT TIME ZONE 'UTC' WHERE app_id = $1`,
			app, strings.TrimSpace(in.Name), string(settings)); err != nil {
			return err
		}
		if len(in.Data) > 0 {
			if err := saveDraft(ctx, tx, app, Site, app, in.Data, by); err != nil {
				return err
			}
		}
		r, err = site(ctx, tx, app, false)
		return err
	})
	return r, err
}
