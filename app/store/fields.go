package store

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"luna-pages/app/render"
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

// Value limits of entries.
const (
	MaxTextChars = 1000
	MaxListItems = 200
)

// FieldKey is the metadata key of a field's value.
func FieldKey(name string) string { return "field." + name }

// RenderFields is the field list as the renderer reads it.
func RenderFields(fields []Field) []render.Field {
	out := make([]render.Field, 0, len(fields))
	for _, f := range fields {
		out = append(out, render.Field{Name: f.Name, Type: string(f.Type), Format: f.Format, Fields: RenderFields(f.Fields)})
	}
	return out
}

// FieldValues are an entry's values by field name (the field.<name> keys
// without their prefix).
func FieldValues(vals map[string]json.RawMessage) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range vals {
		if name, ok := strings.CutPrefix(k, "field."); ok {
			out[name] = v
		}
	}
	return out
}

// CheckEntry validates an entry's values (field.<name> keys) against its
// template's fields: required ones present, every value of its type.
// The error names the key, e.g. field.price or field.specs.2.name.
func CheckEntry(fields []Field, vals map[string]json.RawMessage) error {
	return checkGroup("field.", fields, FieldValues(vals))
}

func checkGroup(prefix string, fields []Field, vals map[string]json.RawMessage) error {
	for _, f := range fields {
		key := prefix + f.Name
		v, ok := vals[f.Name]
		if !ok || string(v) == "null" || empty(v) {
			if f.Required {
				return invalid(key, "The %s field is required.", label(f))
			}
			continue
		}
		if err := checkValue(key, f, v); err != nil {
			return err
		}
	}
	return nil
}

func label(f Field) string {
	if f.Label != "" {
		return f.Label
	}
	return f.Name
}

func empty(v json.RawMessage) bool {
	s := strings.TrimSpace(string(v))
	return s == `""` || s == `[]`
}

func checkValue(key string, f Field, v json.RawMessage) error {
	var s string
	isString := json.Unmarshal(v, &s) == nil
	switch f.Type {
	case Text, Select:
		if !isString {
			return invalid(key, "Must be text.")
		}
		if len([]rune(s)) > MaxTextChars {
			return invalid(key, "May not be longer than %d characters.", MaxTextChars)
		}
		if f.Type == Select && !slices.Contains(f.Options, s) {
			return invalid(key, "Must be one of: %s.", strings.Join(f.Options, ", "))
		}
	case RichText:
		if !isString {
			return invalid(key, "Must be HTML text.")
		}
	case Number:
		var n float64
		if json.Unmarshal(v, &n) != nil {
			if _, err := strconv.ParseFloat(strings.TrimSpace(s), 64); !isString || err != nil {
				return invalid(key, "Must be a number.")
			}
		}
	case Boolean:
		var b bool
		if json.Unmarshal(v, &b) != nil {
			return invalid(key, "Must be true or false.")
		}
	case Date:
		if !isString {
			return invalid(key, "Must be a date (YYYY-MM-DD).")
		}
		if _, err := time.Parse("2006-01-02", s); err != nil {
			if _, err := time.Parse(time.RFC3339, s); err != nil {
				return invalid(key, "Must be a date (YYYY-MM-DD).")
			}
		}
	case Image, Link:
		if isString {
			return nil
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(v, &obj) != nil {
			return invalid(key, "Must be a URL or an object with url.")
		}
		for _, k := range []string{"url", "alt", "label", "file"} {
			if raw, ok := obj[k]; ok {
				var str string
				if json.Unmarshal(raw, &str) != nil {
					return invalid(key, "%s must be text.", k)
				}
			}
		}
	case List:
		var items []map[string]json.RawMessage
		if json.Unmarshal(v, &items) != nil {
			return invalid(key, "Must be a list of items (objects).")
		}
		if len(items) > MaxListItems {
			return invalid(key, "May have at most %d items.", MaxListItems)
		}
		for i, item := range items {
			if err := checkGroup(key+"."+strconv.Itoa(i)+".", f.Fields, item); err != nil {
				return err
			}
		}
	}
	return nil
}
