package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"orm"
)

// The content API (grapesjs-storage's contract): templates as their
// fields, entries as pages of a template with plain field names as value
// keys (stored as field.<name>).

// SaveTemplateFields writes a template's fields, and its draft project
// when data is given (grapesjs-storage owning the design). The version
// does not move: the fields follow from the design, which grapesjs-pages
// writes with its own versions. name is required to create a template.
func (s *Store) SaveTemplateFields(ctx context.Context, app, id, name string, fields []Field, data json.RawMessage, by string) (Record, bool, error) {
	id = strings.ToLower(id)
	if !UUIDLike(id) {
		return Record{}, false, invalid("id", "The id must be a UUID the editor generates.")
	}
	if err := checkFields(fields, false); err != nil {
		return Record{}, false, err
	}
	if err := checkProject(data); err != nil {
		return Record{}, false, err
	}
	created := false
	var rec Record
	err := s.tx(ctx, func(tx orm.SQLExecutor) error {
		_, err := lock(ctx, tx, app, Template, id)
		list, _ := json.Marshal(nonNil(fields))
		switch {
		case errors.Is(err, ErrNotFound):
			clean, err := cleanName(name)
			if err != nil {
				return err
			}
			if err := underLimit(ctx, tx, app, Template); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO templates (app_id, id, name, fields) VALUES ($1, $2, $3, $4)`, app, id, clean, string(list)); err != nil {
				return err
			}
			created = true
		case err != nil:
			return err
		default:
			if _, err := tx.ExecContext(ctx, `UPDATE templates SET fields = $3, updated_at = now() AT TIME ZONE 'UTC' WHERE app_id = $1 AND id = $2`,
				app, id, string(list)); err != nil {
				return err
			}
		}
		if len(data) > 0 && string(data) != "null" {
			if err := saveDraft(ctx, tx, app, Template, id, data, by); err != nil {
				return err
			}
		}
		rec, err = get(ctx, tx, app, Template, id)
		return err
	})
	return rec, created, err
}

// ListEntries is the entries (pages with a template) of a template, or
// of every template when templateID is "".
func (s *Store) ListEntries(ctx context.Context, app, templateID string) ([]Record, error) {
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	q := `SELECT ` + columns(Page) + ` FROM pages WHERE app_id = $1 AND template_id IS NOT NULL`
	args := []any{app}
	if templateID != "" {
		templateID = strings.ToLower(templateID)
		if !UUIDLike(templateID) {
			return []Record{}, nil
		}
		q += ` AND template_id = $2`
		args = append(args, templateID)
	}
	rows, err := db.QueryContext(ctx, q+` ORDER BY title, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		r, err := scanRecord(Page, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetEntry is an entry with its draft values (ErrNotFound for a free page).
func (s *Store) GetEntry(ctx context.Context, app, id string) (Record, error) {
	r, err := s.Get(ctx, app, Page, id)
	if err != nil {
		return Record{}, err
	}
	if r.TemplateID == nil {
		return Record{}, ErrNotFound
	}
	return r, nil
}

// EntryInput is a write of an entry: values by plain field name.
type EntryInput struct {
	TemplateID string                     `json:"template_id"`
	Name       string                     `json:"name"`
	Slug       *string                    `json:"slug"`
	Values     map[string]json.RawMessage `json:"values"`
}

// PutEntry creates or replaces an entry. A free page with the id is not an
// entry: ErrNotFound.
func (s *Store) PutEntry(ctx context.Context, app, id string, in EntryInput, ifMatch *int64, by string) (Record, bool, error) {
	db, err := s.db()
	if err != nil {
		return Record{}, false, err
	}
	cur, err := s.GetEntry(ctx, app, id)
	switch {
	case err == nil:
		if in.TemplateID == "" {
			in.TemplateID = *cur.TemplateID
		}
	case errors.Is(err, ErrNotFound):
		if _, err := get(ctx, db, app, Page, id); err == nil {
			return Record{}, false, ErrNotFound // a free page
		}
		if in.TemplateID == "" {
			return Record{}, false, invalid("template_id", "An entry needs its template.")
		}
	default:
		return Record{}, false, err
	}
	values := make(map[string]json.RawMessage, len(in.Values))
	for k, v := range in.Values {
		if !strings.Contains(k, ".") {
			k = FieldKey(k)
		}
		values[k] = v
	}
	tid := in.TemplateID
	return s.Put(ctx, app, Page, id, Input{Name: in.Name, Slug: in.Slug, TemplateID: &tid, Values: values}, ifMatch, by)
}

// DeleteEntry deletes an entry (not a free page).
func (s *Store) DeleteEntry(ctx context.Context, app, id string, ifMatch *int64) error {
	if _, err := s.GetEntry(ctx, app, id); err != nil {
		return err
	}
	return s.Delete(ctx, app, Page, id, ifMatch)
}

// PublishEntry publishes an entry's draft values.
func (s *Store) PublishEntry(ctx context.Context, app, id string, ifMatch *int64, by string) (Record, error) {
	if _, err := s.GetEntry(ctx, app, id); err != nil {
		return Record{}, err
	}
	out, err := s.Publish(ctx, app, Page, id, PublishInput{}, ifMatch, by)
	if err != nil {
		return Record{}, err
	}
	return s.GetEntry(ctx, app, out.Record.(Record).ID)
}
