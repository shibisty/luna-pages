package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"orm"
)

// grapesjs-storage 1.x's content (its openapi.yaml): templates are groups
// of fields, an entry is the data of a page (its id is the page's id) or a
// standalone entry, with draft and published values; the site has
// languages. Fields and values are kept as the editor sends them — the
// editor checks them; a published page already carries its default
// language's values (the editor fills the data-storage-field elements).

// DataIDRe: ids of templates and entries (UUIDs, or names a client picks).
var DataIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// LangRe: a language code with its region (uk-ua, en-gb, pt-br).
var LangRe = regexp.MustCompile(`^[a-z]{2,3}-[a-z0-9]{2,4}$`)

// Limits of the content.
const (
	MaxDataTemplates = 500
	MaxDataEntries   = 20000
	MaxFieldsBytes   = 512 << 10
	MaxDataBytes     = 1 << 20
)

// DataTemplate is a template of grapesjs-storage 1.x.
type DataTemplate struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Fields    json.RawMessage `json:"fields"`
	Data      json.RawMessage `json:"data,omitempty"`
	Version   int64           `json:"version"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// DataEntry is an entry: values by field name (a translatable field's value
// is an object by language code).
type DataEntry struct {
	ID          string                     `json:"id"`
	TemplateID  string                     `json:"template_id"`
	Name        string                     `json:"name"`
	Slug        string                     `json:"slug"`
	Status      string                     `json:"status"`
	Values      map[string]json.RawMessage `json:"values,omitempty"`
	Version     int64                      `json:"version"`
	UpdatedAt   time.Time                  `json:"updated_at"`
	PublishedAt *time.Time                 `json:"published_at"`
	// Published values (the public read API); not in the editor's JSON.
	Published map[string]json.RawMessage `json:"-"`
}

// DataTemplateInput is PUT …/templates/{id}.
type DataTemplateInput struct {
	Name   *string         `json:"name"`
	Fields json.RawMessage `json:"fields"`
	Data   json.RawMessage `json:"data"`
}

// DataEntryInput is PUT …/entries/{id}: values merge, null deletes a key.
type DataEntryInput struct {
	TemplateID *string                    `json:"template_id"`
	Name       *string                    `json:"name"`
	Slug       *string                    `json:"slug"`
	Values     map[string]json.RawMessage `json:"values"`
}

// Languages are the site's languages, the default first.
type Languages struct {
	Default string   `json:"default"`
	Enabled []string `json:"enabled"`
}

/* ---------------- templates ---------------- */

const dataTemplateCols = `id, name, fields, data, version, updated_at`

func scanDataTemplate(row scanner, withData bool) (DataTemplate, error) {
	var t DataTemplate
	var fields, data []byte
	if err := row.Scan(&t.ID, &t.Name, &fields, &data, &t.Version, &t.UpdatedAt); err != nil {
		return t, err
	}
	t.Fields = json.RawMessage(fields)
	if withData {
		t.Data = json.RawMessage(data)
		if len(data) == 0 {
			t.Data = json.RawMessage("null")
		}
	}
	t.UpdatedAt = t.UpdatedAt.UTC()
	return t, nil
}

// DataTemplates lists the templates, without their data.
func (s *Store) DataTemplates(ctx context.Context, app string) ([]DataTemplate, error) {
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT `+dataTemplateCols+` FROM data_templates WHERE app_id = $1 ORDER BY created_at, id`, app)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DataTemplate{}
	for rows.Next() {
		t, err := scanDataTemplate(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DataTemplate is one template with its data.
func (s *Store) DataTemplate(ctx context.Context, app, id string) (DataTemplate, error) {
	db, err := s.db()
	if err != nil {
		return DataTemplate{}, err
	}
	return getDataTemplate(ctx, db, app, id, "")
}

func getDataTemplate(ctx context.Context, db orm.SQLExecutor, app, id, lock string) (DataTemplate, error) {
	if !DataIDRe.MatchString(id) {
		return DataTemplate{}, ErrNotFound
	}
	rows, err := db.QueryContext(ctx, `SELECT `+dataTemplateCols+` FROM data_templates WHERE app_id = $1 AND id = $2`+lock, app, id)
	if err != nil {
		return DataTemplate{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return DataTemplate{}, err
		}
		return DataTemplate{}, ErrNotFound
	}
	return scanDataTemplate(rows, true)
}

// PutDataTemplate creates or replaces a template (fields as the editor
// sends them: a JSON array).
func (s *Store) PutDataTemplate(ctx context.Context, app, id string, in DataTemplateInput) (t DataTemplate, created bool, err error) {
	if !DataIDRe.MatchString(id) {
		return t, false, invalid("id", "An id: 1 to 64 letters, digits, - and _.")
	}
	var fields []json.RawMessage
	if len(in.Fields) == 0 || json.Unmarshal(in.Fields, &fields) != nil {
		return t, false, invalid("fields", "A list of fields.")
	}
	if len(in.Fields) > MaxFieldsBytes {
		return t, false, invalid("fields", "The fields may not be larger than %d bytes.", MaxFieldsBytes)
	}
	if len(in.Data) > MaxDataBytes || (len(in.Data) > 0 && !json.Valid(in.Data)) {
		return t, false, invalid("data", "Not JSON, or larger than %d bytes.", MaxDataBytes)
	}
	err = s.tx(ctx, func(tx orm.SQLExecutor) error {
		cur, err := getDataTemplate(ctx, tx, app, id, " FOR UPDATE")
		switch {
		case errors.Is(err, ErrNotFound):
			created = true
			if err := countUnder(ctx, tx, `SELECT count(*) FROM data_templates WHERE app_id = $1`, app, MaxDataTemplates, "templates"); err != nil {
				return err
			}
		case err != nil:
			return err
		}
		name := cur.Name
		if in.Name != nil {
			if name, err = cleanName(*in.Name); err != nil {
				return err
			}
		}
		if name == "" {
			name = "Template"
		}
		var data any
		switch {
		case len(in.Data) > 0 && string(in.Data) != "null":
			data = string(in.Data)
		case in.Data == nil && !created && len(cur.Data) > 0 && string(cur.Data) != "null":
			data = string(cur.Data) // omitted: kept
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO data_templates (app_id, id, name, fields, data) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (app_id, id) DO UPDATE SET name = EXCLUDED.name, fields = EXCLUDED.fields, data = EXCLUDED.data,
				version = data_templates.version + 1, updated_at = now() AT TIME ZONE 'UTC'`,
			app, id, name, string(in.Fields), data); err != nil {
			return err
		}
		t, err = getDataTemplate(ctx, tx, app, id, "")
		return err
	})
	return t, created, err
}

// DeleteDataTemplate deletes a template; its entries stay (the editor
// shows them without a template).
func (s *Store) DeleteDataTemplate(ctx context.Context, app, id string) error {
	db, err := s.db()
	if err != nil {
		return err
	}
	if !DataIDRe.MatchString(id) {
		return ErrNotFound
	}
	res, err := db.ExecContext(ctx, `DELETE FROM data_templates WHERE app_id = $1 AND id = $2`, app, id)
	if err != nil {
		return err
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

/* ---------------- entries ---------------- */

const dataEntryCols = `id, template_id, name, slug, draft, published, version, updated_at, published_at`

func scanDataEntry(row scanner, withValues bool) (DataEntry, error) {
	var e DataEntry
	var draft, published []byte
	var at sql.NullTime
	if err := row.Scan(&e.ID, &e.TemplateID, &e.Name, &e.Slug, &draft, &published, &e.Version, &e.UpdatedAt, &at); err != nil {
		return e, err
	}
	e.UpdatedAt = e.UpdatedAt.UTC()
	e.Status = "draft"
	if at.Valid {
		t := at.Time.UTC()
		e.PublishedAt = &t
		e.Status = "published"
	}
	if withValues {
		e.Values = map[string]json.RawMessage{}
		_ = json.Unmarshal(draft, &e.Values)
		if len(published) > 0 {
			_ = json.Unmarshal(published, &e.Published)
		}
	}
	return e, nil
}

// DataEntries lists entries (of one template when given), without
// values, or with the "draft" or "published" ones (with) as values: the
// published ones of an entry never published are none.
func (s *Store) DataEntries(ctx context.Context, app, template, with string) ([]DataEntry, error) {
	if with != "" && with != "draft" && with != "published" {
		return nil, invalid("values", `"draft" or "published".`)
	}
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	q, args := `SELECT `+dataEntryCols+` FROM data_entries WHERE app_id = $1`, []any{app}
	if template != "" {
		q += ` AND template_id = $2`
		args = append(args, template)
	}
	rows, err := db.QueryContext(ctx, q+` ORDER BY created_at, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DataEntry{}
	for rows.Next() {
		e, err := scanDataEntry(rows, with != "")
		if err != nil {
			return nil, err
		}
		if with == "published" {
			e.Values = e.Published
			if e.Values == nil {
				e.Values = map[string]json.RawMessage{}
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DataEntry is one entry with its draft (and published) values.
func (s *Store) DataEntry(ctx context.Context, app, id string) (DataEntry, error) {
	db, err := s.db()
	if err != nil {
		return DataEntry{}, err
	}
	return getDataEntry(ctx, db, app, id, "")
}

func getDataEntry(ctx context.Context, db orm.SQLExecutor, app, id, lock string) (DataEntry, error) {
	if !DataIDRe.MatchString(id) {
		return DataEntry{}, ErrNotFound
	}
	rows, err := db.QueryContext(ctx, `SELECT `+dataEntryCols+` FROM data_entries WHERE app_id = $1 AND id = $2`+lock, app, strings.ToLower(id))
	if err != nil {
		return DataEntry{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return DataEntry{}, err
		}
		return DataEntry{}, ErrNotFound
	}
	return scanDataEntry(rows, true)
}

// PutDataEntry creates an entry or changes it: values merge, null deletes
// a key. ifMatch: 412 when the entry moved on.
func (s *Store) PutDataEntry(ctx context.Context, app, id string, in DataEntryInput, ifMatch *int64) (e DataEntry, created bool, err error) {
	id = strings.ToLower(id)
	if !DataIDRe.MatchString(id) {
		return e, false, invalid("id", "An id: 1 to 64 letters, digits, - and _.")
	}
	for k, v := range in.Values {
		if !keyRe.MatchString(k) {
			return e, false, invalid("values", "%q is not a field name.", k)
		}
		if !json.Valid(v) {
			return e, false, invalid("values", "%q: not JSON.", k)
		}
	}
	err = s.tx(ctx, func(tx orm.SQLExecutor) error {
		cur, err := getDataEntry(ctx, tx, app, id, " FOR UPDATE")
		switch {
		case errors.Is(err, ErrNotFound):
			if ifMatch != nil {
				return ErrVersion
			}
			created = true
			if err := countUnder(ctx, tx, `SELECT count(*) FROM data_entries WHERE app_id = $1`, app, MaxDataEntries, "entries"); err != nil {
				return err
			}
			cur.Values = map[string]json.RawMessage{}
		case err != nil:
			return err
		case ifMatch != nil && *ifMatch != cur.Version:
			return ErrVersion
		}
		template := cur.TemplateID
		if in.TemplateID != nil {
			template = *in.TemplateID
		}
		if !DataIDRe.MatchString(template) {
			return invalid("template_id", "An entry has a template.")
		}
		name, slug := cur.Name, cur.Slug
		if in.Name != nil {
			if name, err = cleanName(*in.Name); err != nil && *in.Name != "" {
				return err
			}
		}
		if in.Slug != nil {
			slug = strings.TrimSpace(*in.Slug)
			if len(slug) > 255 {
				return invalid("slug", "At most 255 characters.")
			}
		}
		values := cur.Values
		for k, v := range in.Values {
			if string(v) == "null" {
				delete(values, k)
			} else {
				values[k] = v
			}
		}
		raw, _ := json.Marshal(values)
		if len(raw) > MaxDataBytes {
			return invalid("values", "The values may not be larger than %d bytes.", MaxDataBytes)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO data_entries (app_id, id, template_id, name, slug, draft) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (app_id, id) DO UPDATE SET template_id = EXCLUDED.template_id, name = EXCLUDED.name, slug = EXCLUDED.slug,
				draft = EXCLUDED.draft, version = data_entries.version + 1, updated_at = now() AT TIME ZONE 'UTC'`,
			app, id, template, name, slug, string(raw)); err != nil {
			return err
		}
		e, err = getDataEntry(ctx, tx, app, id, "")
		return err
	})
	return e, created, err
}

// DeleteDataEntry deletes an entry.
func (s *Store) DeleteDataEntry(ctx context.Context, app, id string, ifMatch *int64) error {
	return s.tx(ctx, func(tx orm.SQLExecutor) error {
		cur, err := getDataEntry(ctx, tx, app, id, " FOR UPDATE")
		if err != nil {
			if errors.Is(err, ErrNotFound) && ifMatch != nil {
				return ErrVersion
			}
			return err
		}
		if ifMatch != nil && *ifMatch != cur.Version {
			return ErrVersion
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM data_entries WHERE app_id = $1 AND id = $2`, app, cur.ID)
		return err
	})
}

// PublishDataEntry makes the draft values the published ones.
func (s *Store) PublishDataEntry(ctx context.Context, app, id string, ifMatch *int64) (e DataEntry, err error) {
	err = s.tx(ctx, func(tx orm.SQLExecutor) error {
		cur, err := getDataEntry(ctx, tx, app, id, " FOR UPDATE")
		if err != nil {
			if errors.Is(err, ErrNotFound) && ifMatch != nil {
				return ErrVersion
			}
			return err
		}
		if ifMatch != nil && *ifMatch != cur.Version {
			return ErrVersion
		}
		if _, err := tx.ExecContext(ctx, `UPDATE data_entries SET published = draft, published_at = now() AT TIME ZONE 'UTC',
			version = version + 1, updated_at = now() AT TIME ZONE 'UTC' WHERE app_id = $1 AND id = $2`, app, cur.ID); err != nil {
			return err
		}
		e, err = getDataEntry(ctx, tx, app, cur.ID, "")
		return err
	})
	return e, err
}

/* ---------------- languages ---------------- */

// DataLanguages are the site's languages (ErrNotFound: none set).
func (s *Store) DataLanguages(ctx context.Context, app string) (Languages, error) {
	db, err := s.db()
	if err != nil {
		return Languages{}, err
	}
	rows, err := db.QueryContext(ctx, `SELECT languages FROM sites WHERE app_id = $1`, app)
	if err != nil {
		return Languages{}, err
	}
	defer rows.Close()
	var raw []byte
	if rows.Next() {
		if err := rows.Scan(&raw); err != nil {
			return Languages{}, err
		}
	}
	var l Languages
	if len(raw) == 0 || json.Unmarshal(raw, &l) != nil || l.Default == "" {
		return Languages{}, ErrNotFound
	}
	return l, rows.Err()
}

// PutDataLanguages sets the site's languages; the default goes first.
func (s *Store) PutDataLanguages(ctx context.Context, app string, l Languages) (Languages, error) {
	if l.Default == "" && len(l.Enabled) == 0 {
		// {default: "", enabled: []}: the site has no languages (grapesjs-storage clears them so).
		db, err := s.db()
		if err != nil {
			return l, err
		}
		_, err = db.ExecContext(ctx, `UPDATE sites SET languages = NULL WHERE app_id = $1`, app)
		return Languages{Enabled: []string{}}, err
	}
	if !LangRe.MatchString(l.Default) {
		return l, invalid("default", "A language code with its region, e.g. uk-ua.")
	}
	if len(l.Enabled) > 200 {
		return l, invalid("enabled", "At most 200 languages.")
	}
	out := Languages{Default: l.Default, Enabled: []string{l.Default}}
	seen := map[string]bool{l.Default: true}
	for _, c := range l.Enabled {
		if !LangRe.MatchString(c) {
			return l, invalid("enabled", "%q is not a language code with its region.", c)
		}
		if !seen[c] {
			seen[c] = true
			out.Enabled = append(out.Enabled, c)
		}
	}
	db, err := s.db()
	if err != nil {
		return out, err
	}
	raw, _ := json.Marshal(out)
	_, err = db.ExecContext(ctx, `INSERT INTO sites (app_id, languages) VALUES ($1, $2)
		ON CONFLICT (app_id) DO UPDATE SET languages = EXCLUDED.languages`, app, string(raw))
	return out, err
}

func countUnder(ctx context.Context, db orm.SQLExecutor, q, app string, max int, what string) error {
	rows, err := db.QueryContext(ctx, q, app)
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
	if n >= max {
		return invalid("id", "An application may have at most %d %s.", max, what)
	}
	return rows.Err()
}
