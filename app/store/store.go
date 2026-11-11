// Package store keeps luna-pages' records: the site, layouts, templates and
// pages of each application, their GrapesJS projects (content) and the
// pages' values (metadata). It is the editor contract v2: ids come from the
// editor (UUIDs), every record has a version that changes on every write,
// and writes may be conditional on it (If-Match), as grapesjs-pages expects.
//
// PostgreSQL-specific SQL (JSONB, upserts, row locks); every query is
// scoped by the application id.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"fullstack"
	"orm"
)

// Kind is the kind of a record.
type Kind string

// The kinds the editor edits one by one. The site is a single record per
// application with its own calls.
const (
	Layout   Kind = "layout"
	Template Kind = "template"
	Page     Kind = "page"
	Site     Kind = "site"
)

// KindOf maps an API path segment (layouts, templates, pages) to its kind.
func KindOf(segment string) (Kind, bool) {
	switch segment {
	case "layouts":
		return Layout, true
	case "templates":
		return Template, true
	case "pages":
		return Page, true
	}
	return "", false
}

// Errors of the store.
var (
	ErrNotFound = errors.New("store: not found")
	// ErrVersion: If-Match did not match the record's version (or the
	// record is gone). The editor answers it with its conflict dialog.
	ErrVersion = errors.New("store: version mismatch")
	// ErrInUse: a template still has entries.
	ErrInUse = errors.New("store: in use")
)

// FieldError is a validation error of one input field (422).
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Message }

func invalid(field, format string, a ...any) error {
	return &FieldError{Field: field, Message: fmt.Sprintf(format, a...)}
}

// Limits.
const (
	MaxProjectBytes = 2 << 20 // one GrapesJS project
	MaxFields       = 100     // fields of a template
	MaxValues       = 200     // metadata keys of a page
	MaxValueBytes   = 64 << 10
	MaxRecords      = 1000 // records of one kind per application
)

// Record is a layout, template or page as the editor sees it.
type Record struct {
	Kind      Kind      `json:"kind"`
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Pages.
	Slug       string  `json:"slug,omitempty"`
	PageKind   string  `json:"page_kind,omitempty"` // free, entry
	Status     string  `json:"status,omitempty"`
	LayoutID   *string `json:"layout_id,omitempty"`
	TemplateID *string `json:"template_id,omitempty"`
	ParentID   *string `json:"parent_id,omitempty"`
	Position   int     `json:"position,omitempty"`

	// Templates.
	Fields []Field `json:"fields,omitempty"`

	// Only on Get: the GrapesJS project (draft) and, for pages, the draft
	// values (field.<name>, seo.<name>, the client's own keys).
	Data   json.RawMessage            `json:"data,omitempty"`
	Values map[string]json.RawMessage `json:"values,omitempty"`
}

// Input is a write of a layout, template or page (PUT). Name and Data are
// what grapesjs-pages v1 sends; the rest is v2 and optional.
type Input struct {
	Name string          `json:"name"`
	Data json.RawMessage `json:"data"`

	// Pages: the title is Name.
	Slug       *string                    `json:"slug"`
	LayoutID   *string                    `json:"layout_id"`
	TemplateID *string                    `json:"template_id"`
	ParentID   *string                    `json:"parent_id"`
	Position   *int                       `json:"position"`
	Values     map[string]json.RawMessage `json:"values"`

	// Templates.
	Fields []Field `json:"fields"`
}

// Store is in the container (Provider).
type Store struct{ app *fullstack.App }

// Provider registers *store.Store.
type Provider struct{}

// Register implements fullstack.Provider.
func (Provider) Register(app *fullstack.App) error {
	fullstack.Provide(app, func(app *fullstack.App) (*Store, error) { return &Store{app: app}, nil })
	return nil
}

func (s *Store) db() (orm.SQLExecutor, error) { return fullstack.Make[orm.SQLExecutor](s.app) }

// tx runs fn in a transaction, or in the one already open (tests run in
// database.UseTransaction; orm-postgres has no nested transactions).
func (s *Store) tx(ctx context.Context, fn func(tx orm.SQLExecutor) error) (err error) {
	db, err := s.db()
	if err != nil {
		return err
	}
	if tx, ok := db.(orm.Tx); ok {
		return fn(tx)
	}
	tx, err := db.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// ---- validation ----

var (
	nameRe  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	keyRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
	segment = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

// UUIDLike reports a canonical lower-case UUID.
func UUIDLike(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if c != '-' {
				return false
			}
		case !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f'):
			return false
		}
	}
	return true
}

// NormalizeSlug makes a page path: "/" for the home page, otherwise
// "/a/b-c" of lower-case letters, digits and single dashes, at most 255.
func NormalizeSlug(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.Trim(s, "/")
	if s == "" {
		return "/", nil
	}
	parts := strings.Split(s, "/")
	for _, p := range parts {
		if !segment.MatchString(p) {
			return "", invalid("slug", "Use lower-case letters, digits and dashes between slashes, e.g. /products/coffee-grinder.")
		}
	}
	out := "/" + strings.Join(parts, "/")
	if len(out) > 255 {
		return "", invalid("slug", "The slug may not be longer than 255 characters.")
	}
	return out, nil
}

// Slugify makes a slug from a title: "Coffee grinder, 2nd" → "/coffee-grinder-2nd".
func Slugify(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.TrimSuffix(b.String(), "-")
	if len(s) > 200 {
		s = strings.TrimSuffix(s[:200], "-")
	}
	if s == "" {
		s = "page"
	}
	return "/" + s
}

func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", invalid("name", "The name is required.")
	case len(name) > 200:
		return "", invalid("name", "The name may not be longer than 200 characters.")
	}
	return name, nil
}

func checkProject(data json.RawMessage) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if len(data) > MaxProjectBytes {
		return invalid("data", "The project may not be larger than %d bytes.", MaxProjectBytes)
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		return invalid("data", "The project must be a JSON object (editor.getProjectData()).")
	}
	return nil
}

func optionalID(field string, id *string) (*string, error) {
	if id == nil || *id == "" {
		return nil, nil
	}
	v := strings.ToLower(*id)
	if !UUIDLike(v) {
		return nil, invalid(field, "Not an id.")
	}
	return &v, nil
}

func checkValues(values map[string]json.RawMessage) error {
	if len(values) > MaxValues {
		return invalid("values", "A page may have at most %d values.", MaxValues)
	}
	for k, v := range values {
		if !keyRe.MatchString(k) {
			return invalid("values", "%q is not a key: 1 to 128 of letters, digits, _ . : - starting with a letter or digit.", k)
		}
		if len(v) > MaxValueBytes {
			return invalid("values", "%q: a value may not be larger than %d bytes.", k, MaxValueBytes)
		}
		if !json.Valid(v) {
			return invalid("values", "%q: not JSON.", k)
		}
	}
	return nil
}
