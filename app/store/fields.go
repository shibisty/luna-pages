package store

import (
	"encoding/json"
	"strings"
)

// FieldType is the type of a template field. The element marked with
// data-field="<name>" data-field-type="<type>" in the template is filled
// with the entry's value of that type (grapesjs-storage, the renderer).
type FieldType string

// The field types.
const (
	Text     FieldType = "text"
	RichText FieldType = "richtext"
	Image    FieldType = "image"
	Link     FieldType = "link"
	Number   FieldType = "number"
	Boolean  FieldType = "boolean"
	Date     FieldType = "date"
	Select   FieldType = "select"
	List     FieldType = "list"
)

var fieldTypes = map[FieldType]bool{Text: true, RichText: true, Image: true, Link: true, Number: true, Boolean: true, Date: true, Select: true, List: true}

// Field is one field of a template.
type Field struct {
	Name     string          `json:"name"`
	Type     FieldType       `json:"type"`
	Label    string          `json:"label,omitempty"`
	Required bool            `json:"required,omitempty"`
	Default  json.RawMessage `json:"default,omitempty"`
	Options  []string        `json:"options,omitempty"` // select
	Format   string          `json:"format,omitempty"`  // number, date
	Fields   []Field         `json:"fields,omitempty"`  // the item of a list
}

// checkFields validates a template's field list: names unique per level,
// known types, options for selects, item fields for lists (one level).
func checkFields(fields []Field, nested bool) error {
	if len(fields) > MaxFields {
		return invalid("fields", "A template may have at most %d fields.", MaxFields)
	}
	seen := map[string]bool{}
	for i := range fields {
		f := &fields[i]
		f.Name = strings.TrimSpace(f.Name)
		if !nameRe.MatchString(f.Name) {
			return invalid("fields", "%q is not a field name: a lower-case letter, then letters, digits or _ (up to 64).", f.Name)
		}
		if seen[f.Name] {
			return invalid("fields", "The field %q is defined twice.", f.Name)
		}
		seen[f.Name] = true
		if !fieldTypes[f.Type] {
			return invalid("fields", "%q: unknown type %q.", f.Name, f.Type)
		}
		if len(f.Label) > 200 {
			return invalid("fields", "%q: the label may not be longer than 200 characters.", f.Name)
		}
		if len(f.Default) > 0 && !json.Valid(f.Default) {
			return invalid("fields", "%q: the default is not JSON.", f.Name)
		}
		switch f.Type {
		case Select:
			if len(f.Options) == 0 {
				return invalid("fields", "%q: a select needs options.", f.Name)
			}
		case List:
			if nested {
				return invalid("fields", "%q: lists inside lists are not supported.", f.Name)
			}
			if len(f.Fields) == 0 {
				return invalid("fields", "%q: a list needs the fields of its item.", f.Name)
			}
			if err := checkFields(f.Fields, true); err != nil {
				return err
			}
		default:
			if len(f.Fields) > 0 {
				return invalid("fields", "%q: only a list has item fields.", f.Name)
			}
		}
	}
	return nil
}
