package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"orm"

	"luna-pages/app/render"
)

// Publishing limits.
const (
	MaxHTMLBytes = 2 << 20
	MaxCSSBytes  = 1 << 20
	// MaxRevisions published rows are kept per record; older ones go.
	MaxRevisions = 20
)

// PublishInput is what the editor sends to publish: editor.getHtml() and
// editor.getCss(), and optionally the project (saved as the draft first,
// so what is published is also what the editor reopens). An entry sends
// nothing: its template is the design and its draft values are published.
type PublishInput struct {
	HTML *string         `json:"html"`
	CSS  *string         `json:"css"`
	Data json.RawMessage `json:"data"`
}

// Published is the answer to a publish: the record and its new revision.
type Published struct {
	Record   any      `json:"record"`
	Revision int      `json:"revision"`
	Warnings []string `json:"warnings"`
}

// Revision is one published version of a record.
type Revision struct {
	Revision  int       `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
	Live      bool      `json:"live"` // the one the site shows
}

// SitePolicy is the sanitizer's policy from the site's settings:
// "custom_code" (bool) keeps <script>, "iframe_hosts" (strings) adds to the
// hosts an <iframe> may show.
func SitePolicy(settings json.RawMessage) render.Policy {
	var s struct {
		CustomCode  bool     `json:"custom_code"`
		IframeHosts []string `json:"iframe_hosts"`
	}
	_ = json.Unmarshal(settings, &s)
	p := render.Policy{CustomCode: s.CustomCode}
	if len(s.IframeHosts) > 0 {
		p.IframeHosts = append(append([]string{}, render.DefaultIframeHosts...), s.IframeHosts...)
	}
	return p
}

func (s *Store) policy(ctx context.Context, db orm.SQLExecutor, app string) (render.Policy, error) {
	if _, err := db.ExecContext(ctx, `INSERT INTO sites (app_id) VALUES ($1) ON CONFLICT DO NOTHING`, app); err != nil {
		return render.Policy{}, err
	}
	site, err := site(ctx, db, app, false)
	if err != nil {
		return render.Policy{}, err
	}
	return SitePolicy(site.Settings), nil
}

// clean sanitizes what the editor sent.
func clean(in PublishInput, p render.Policy) (htmlOut, cssOut string, err error) {
	if in.HTML != nil {
		if len(*in.HTML) > MaxHTMLBytes {
			return "", "", invalid("html", "The HTML may not be larger than %d bytes.", MaxHTMLBytes)
		}
		if htmlOut, err = render.SanitizeHTML(*in.HTML, p); err != nil {
			return "", "", invalid("html", "The HTML cannot be read.")
		}
	}
	if in.CSS != nil {
		if len(*in.CSS) > MaxCSSBytes {
			return "", "", invalid("css", "The CSS may not be larger than %d bytes.", MaxCSSBytes)
		}
		cssOut = render.SanitizeCSS(*in.CSS)
	}
	return htmlOut, cssOut, nil
}

// Publish makes a layout, template or page live. Layouts and templates
// and free pages publish the HTML and CSS sent (sanitized); an entry
// publishes its draft values, checked against its template's fields
// (the template must be published). ifMatch as in Put; the version moves
// on.
func (s *Store) Publish(ctx context.Context, app string, k Kind, id string, in PublishInput, ifMatch *int64, by string) (Published, error) {
	id = strings.ToLower(id)
	if !UUIDLike(id) {
		return Published{}, ErrNotFound
	}
	if err := checkProject(in.Data); err != nil {
		return Published{}, err
	}
	var out Published
	err := s.tx(ctx, func(tx orm.SQLExecutor) error {
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
		p, err := s.policy(ctx, tx, app)
		if err != nil {
			return err
		}
		if len(in.Data) > 0 {
			if err := saveDraft(ctx, tx, app, k, id, in.Data, by); err != nil {
				return err
			}
		}
		entry := k == Page && cur.TemplateID != nil
		var htmlOut, cssOut string
		if !entry {
			if in.HTML == nil {
				return invalid("html", "Send the HTML to publish (editor.getHtml()).")
			}
			if htmlOut, cssOut, err = clean(in, p); err != nil {
				return err
			}
		}
		switch {
		case k == Layout && !render.HasSlot(htmlOut):
			return invalid("html", "A layout needs a <page-slot></page-slot> or <luna-slot></luna-slot> (or an element with data-page-slot or data-luna-slot) where the page goes.")
		case k == Template:
			fields, err := templateFields(ctx, tx, app, id)
			if err != nil {
				return err
			}
			out.Warnings = missingFields(htmlOut, fields)
		case entry:
			if _, err := latest(ctx, tx, app, Template, *cur.TemplateID); errors.Is(err, ErrNotFound) {
				return invalid("template_id", "Publish the template first.")
			} else if err != nil {
				return err
			}
			fields, err := templateFields(ctx, tx, app, *cur.TemplateID)
			if err != nil {
				return err
			}
			vals, err := values(ctx, tx, app, id, "draft")
			if err != nil {
				return err
			}
			if err := CheckEntry(fields, vals); err != nil {
				return err
			}
		}
		if !entry {
			if out.Revision, err = addRevision(ctx, tx, app, k, id, htmlOut, cssOut, by); err != nil {
				return err
			}
		}
		if k == Page {
			if _, err := tx.ExecContext(ctx, `DELETE FROM metadata WHERE app_id = $1 AND page_id = $2 AND state = 'published'`, app, id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO metadata (app_id, page_id, state, key, value)
				SELECT app_id, page_id, 'published', key, value FROM metadata WHERE app_id = $1 AND page_id = $2 AND state = 'draft'`, app, id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE pages SET status = 'published', published_at = now() AT TIME ZONE 'UTC',
				version = version + 1, updated_at = now() AT TIME ZONE 'UTC' WHERE app_id = $1 AND id = $2`, app, id); err != nil {
				return err
			}
		} else if _, err := tx.ExecContext(ctx, `UPDATE `+table(k)+` SET version = version + 1, updated_at = now() AT TIME ZONE 'UTC'
			WHERE app_id = $1 AND id = $2`, app, id); err != nil {
			return err
		}
		rec, err := get(ctx, tx, app, k, id)
		out.Record = rec
		return err
	})
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	return out, err
}

// PublishSite makes the site's global CSS live.
func (s *Store) PublishSite(ctx context.Context, app string, in PublishInput, ifMatch *int64, by string) (Published, error) {
	if err := checkProject(in.Data); err != nil {
		return Published{}, err
	}
	var out Published
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
		if len(in.Data) > 0 {
			if err := saveDraft(ctx, tx, app, Site, app, in.Data, by); err != nil {
				return err
			}
		}
		if in.CSS == nil {
			return invalid("css", "Send the site's CSS to publish (editor.getCss()).")
		}
		_, cssOut, err := clean(PublishInput{CSS: in.CSS}, render.Policy{})
		if err != nil {
			return err
		}
		if out.Revision, err = addRevision(ctx, tx, app, Site, app, "", cssOut, by); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sites SET version = version + 1, updated_at = now() AT TIME ZONE 'UTC' WHERE app_id = $1`, app); err != nil {
			return err
		}
		rec, err := site(ctx, tx, app, false)
		out.Record = rec
		return err
	})
	out.Warnings = []string{}
	return out, err
}

// Unpublish takes a page off the site (its revisions stay).
func (s *Store) Unpublish(ctx context.Context, app, id string, ifMatch *int64) (Record, error) {
	id = strings.ToLower(id)
	if !UUIDLike(id) {
		return Record{}, ErrNotFound
	}
	var rec Record
	err := s.tx(ctx, func(tx orm.SQLExecutor) error {
		cur, err := lock(ctx, tx, app, Page, id)
		if err != nil {
			if errors.Is(err, ErrNotFound) && ifMatch != nil {
				return ErrVersion
			}
			return err
		}
		if ifMatch != nil && *ifMatch != cur.Version {
			return ErrVersion
		}
		if _, err := tx.ExecContext(ctx, `UPDATE pages SET status = 'draft', published_at = NULL, version = version + 1,
			updated_at = now() AT TIME ZONE 'UTC' WHERE app_id = $1 AND id = $2`, app, id); err != nil {
			return err
		}
		rec, err = get(ctx, tx, app, Page, id)
		return err
	})
	return rec, err
}

// addRevision appends a published row and keeps the newest MaxRevisions.
func addRevision(ctx context.Context, db orm.SQLExecutor, app string, k Kind, owner, htmlOut, cssOut, by string) (int, error) {
	rows, err := db.QueryContext(ctx, `SELECT coalesce(max(revision), 0) + 1 FROM content
		WHERE app_id = $1 AND owner_kind = $2 AND owner_id = $3 AND state = 'published'`, app, string(k), owner)
	if err != nil {
		return 0, err
	}
	rev := 1
	if rows.Next() {
		err = rows.Scan(&rev)
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO content (app_id, owner_kind, owner_id, state, revision, project, html, css, created_by)
		SELECT $1::uuid, $2::varchar, $3::uuid, 'published', $4::integer,
			(SELECT project FROM content WHERE app_id = $1::uuid AND owner_kind = $2::varchar AND owner_id = $3::uuid AND state = 'draft'),
			$5::text, $6::text, $7::varchar`,
		app, string(k), owner, rev, htmlOut, cssOut, by); err != nil {
		return 0, err
	}
	_, err = db.ExecContext(ctx, `DELETE FROM content WHERE app_id = $1 AND owner_kind = $2 AND owner_id = $3 AND state = 'published' AND revision <= $4`,
		app, string(k), owner, rev-MaxRevisions)
	return rev, err
}

// published is a record's live revision.
type published struct {
	Revision int
	HTML     string
	CSS      string
}

// latest is the newest published revision of a record (ErrNotFound: never
// published).
func latest(ctx context.Context, db orm.SQLExecutor, app string, k Kind, owner string) (published, error) {
	rows, err := db.QueryContext(ctx, `SELECT revision, coalesce(html, ''), coalesce(css, '') FROM content
		WHERE app_id = $1 AND owner_kind = $2 AND owner_id = $3 AND state = 'published' ORDER BY revision DESC LIMIT 1`, app, string(k), owner)
	if err != nil {
		return published{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return published{}, err
		}
		return published{}, ErrNotFound
	}
	var p published
	err = rows.Scan(&p.Revision, &p.HTML, &p.CSS)
	return p, err
}

// Revisions lists a record's published revisions, newest first.
func (s *Store) Revisions(ctx context.Context, app string, k Kind, id string) ([]Revision, error) {
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	id = strings.ToLower(id)
	if k == Site {
		id = app
	} else if _, err := get(ctx, db, app, k, id); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT revision, created_at, created_by FROM content
		WHERE app_id = $1 AND owner_kind = $2 AND owner_id = $3 AND state = 'published' ORDER BY revision DESC`, app, string(k), id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Revision{}
	for rows.Next() {
		var r Revision
		if err := rows.Scan(&r.Revision, &r.CreatedAt, &r.CreatedBy); err != nil {
			return nil, err
		}
		r.CreatedAt = r.CreatedAt.UTC()
		r.Live = len(out) == 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// Restore copies a revision's project into the draft (it is not
// published: the editor reopens it and publishes when it is ready). The
// version moves on.
func (s *Store) Restore(ctx context.Context, app string, k Kind, id string, rev int, ifMatch *int64, by string) (Record, error) {
	id = strings.ToLower(id)
	if !UUIDLike(id) {
		return Record{}, ErrNotFound
	}
	var rec Record
	err := s.tx(ctx, func(tx orm.SQLExecutor) error {
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
		rows, err := tx.QueryContext(ctx, `SELECT project FROM content WHERE app_id = $1 AND owner_kind = $2 AND owner_id = $3
			AND state = 'published' AND revision = $4`, app, string(k), id, rev)
		if err != nil {
			return err
		}
		found := rows.Next()
		var project []byte
		if found {
			err = rows.Scan(&project)
		}
		rows.Close()
		if err != nil {
			return err
		}
		if !found {
			return ErrNotFound
		}
		data := json.RawMessage(project)
		if len(project) == 0 {
			data = json.RawMessage("null")
		}
		if err := saveDraft(ctx, tx, app, k, id, data, by); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE `+table(k)+` SET version = version + 1, updated_at = now() AT TIME ZONE 'UTC'
			WHERE app_id = $1 AND id = $2`, app, id); err != nil {
			return err
		}
		if rec, err = get(ctx, tx, app, k, id); err != nil {
			return err
		}
		rec.Data = data
		return nil
	})
	return rec, err
}

func templateFields(ctx context.Context, db orm.SQLExecutor, app, id string) ([]Field, error) {
	t, err := get(ctx, db, app, Template, id)
	if err != nil {
		return nil, err
	}
	return t.Fields, nil
}

// missingFields names the fields of the list without an element in the
// template's HTML: they can be filled in but never show.
func missingFields(htmlOut string, fields []Field) []string {
	warnings := []string{}
	for _, f := range fields {
		if !strings.Contains(htmlOut, `data-field="`+f.Name+`"`) {
			warnings = append(warnings, "The field "+f.Name+" has no element in the template (data-field=\""+f.Name+"\").")
		}
	}
	return warnings
}
