package render

import (
	"encoding/json"
	"html"
	"net/url"
	"regexp"
	"strings"

	xhtml "golang.org/x/net/html"
)

// StorageField is a field of a grapesjs-storage 1.x template: the parts
// the server renders by (its schema.ts SchemaField).
type StorageField struct {
	Name     string          `json:"name"`
	Type     string          `json:"type"`
	Format   string          `json:"format,omitempty"`
	Multiple bool            `json:"multiple,omitempty"`
	OnText   string          `json:"onText,omitempty"`
	OffText  string          `json:"offText,omitempty"`
	Choices  []StorageChoice `json:"choices,omitempty"`
	Fields   []StorageField  `json:"fields,omitempty"`
}

// StorageChoice is a choice of a select, checkbox, radio or button group.
type StorageChoice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// PageRef is a page a page_link field points at.
type PageRef struct {
	Name string
	Slug string
}

// StorageData is what FillStorage needs: a data record's values, the
// values its design was published with (the owner page's own data: how
// many rows of a repeater the design holds), the template's fields and the
// language to show.
type StorageData struct {
	Fields []StorageField
	Values map[string]json.RawMessage
	Owner  map[string]json.RawMessage
	// Lang and Fallback pick a translated value ({"uk-ua": …, "en-gb": …});
	// empty: the first filled language.
	Lang, Fallback string
	// Pages resolves a page_link value (a page id); nil: such links stay empty.
	Pages func(id string) (PageRef, bool)
}

// The attributes grapesjs-storage 1.x keeps on a field element in the
// published HTML (FieldComponents.ts FIELD_ATTR).
const (
	StorageFieldAttr = "data-storage-field"
	StorageTypeAttr  = "data-storage-type"
)

// FillStorage writes a data record's values into the design of the page
// it belongs to: the elements marked data-storage-field (grapesjs-storage
// 1.x), with the same HTML the editor gives each type (schema.ts
// valueHtml). An image field is the <img> itself and a link-like field
// (link, url, email, file, one page_link) the <a> itself: their src / href
// come from the value, empty ones get hidden. A group's fields take their
// values from the group's object. A repeater's element holds its rows: the
// design was published with the owner page's rows, so the first
// len(children) / len(owner rows) elements are the design of one row,
// repeated per row of the record. Body is the owner's published (already
// sanitized) HTML; the values' HTML (wysiwyg) is cleaned by p.
func FillStorage(body string, d StorageData, p Policy) (string, error) {
	nodes, err := ParseFragment(body)
	if err != nil {
		return "", err
	}
	s := storageFiller{d: d, p: p}
	nodes = s.nodes(nodes, storageIndex(d.Fields), d.Values, d.Owner)
	return Render(nodes)
}

type storageFiller struct {
	d StorageData
	p Policy
}

func storageIndex(fields []StorageField) map[string]StorageField {
	out := map[string]StorageField{}
	for _, f := range fields {
		switch f.Type {
		case "tab", "accordion", "message":
			// layout only; an accordion's fields are its own siblings in the values
			for k, v := range storageIndex(f.Fields) {
				out[k] = v
			}
			continue
		}
		out[f.Name] = f
	}
	return out
}

func (s storageFiller) nodes(nodes []*xhtml.Node, fields map[string]StorageField, values, owner map[string]json.RawMessage) []*xhtml.Node {
	out := nodes[:0]
	for _, n := range nodes {
		if s.node(n, fields, values, owner) {
			out = append(out, n)
		}
	}
	return out
}

func (s storageFiller) children(n *xhtml.Node, fields map[string]StorageField, values, owner map[string]json.RawMessage) {
	setChildren(n, s.nodes(children(n), fields, values, owner))
}

// node fills one node (false: drop it).
func (s storageFiller) node(n *xhtml.Node, fields map[string]StorageField, values, owner map[string]json.RawMessage) bool {
	if n.Type != xhtml.ElementNode {
		return true
	}
	if n.Data == "script" || n.Data == "style" {
		return true
	}
	name := attr(n, StorageFieldAttr)
	if name == "" {
		s.children(n, fields, values, owner)
		return true
	}
	f, ok := fields[name]
	if !ok {
		f = StorageField{Name: name, Type: attr(n, StorageTypeAttr)}
	}
	if f.Type == "" {
		f.Type = "text"
	}
	v := s.pick(values[name])
	switch f.Type {
	case "group":
		var sub map[string]json.RawMessage
		_ = json.Unmarshal(v, &sub)
		var own map[string]json.RawMessage
		_ = json.Unmarshal(s.pick(owner[name]), &own)
		s.children(n, storageIndex(f.Fields), sub, own)
		return true
	case "repeater", "gallery":
		s.rows(n, f, v, s.pick(owner[name]))
		return true
	}
	switch n.Data {
	case "img":
		u, alt := imageValue(v)
		if clean, ok := cleanURL(u, true); ok && clean != "" {
			n.Attr = setAttr(n.Attr, "src", clean)
			n.Attr = setAttr(n.Attr, "alt", alt)
			n.Attr = removeAttr(n.Attr, "srcset")
			n.Attr = removeAttr(n.Attr, "hidden")
		} else {
			n.Attr = removeAttr(removeAttr(n.Attr, "src"), "srcset")
			n.Attr = setAttr(n.Attr, "hidden", "")
		}
		return true
	case "a":
		href, text, extra := s.linkOf(f, v)
		if clean, ok := cleanURL(href, false); ok && clean != "" {
			n.Attr = removeAttr(removeAttr(removeAttr(n.Attr, "target"), "rel"), "download")
			n.Attr = setAttr(n.Attr, "href", clean)
			for _, a := range extra {
				n.Attr = setAttr(n.Attr, a.Key, a.Val)
			}
			n.Attr = removeAttr(n.Attr, "hidden")
		} else {
			n.Attr = removeAttr(n.Attr, "href")
			n.Attr = setAttr(n.Attr, "hidden", "")
			text = ""
		}
		setText(n, text)
		return true
	}
	inner := s.valueHTML(f, v)
	kids, err := ParseFragment(inner)
	if err != nil {
		kids = nil
	}
	setChildren(n, s.p.clean(kids))
	return true
}

// rows repeats a repeater's row design once per row of the record.
func (s storageFiller) rows(n *xhtml.Node, f StorageField, v, ownerV json.RawMessage) {
	var els []*xhtml.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == xhtml.ElementNode {
			els = append(els, c)
		}
	}
	var ownerRows []json.RawMessage
	_ = json.Unmarshal(ownerV, &ownerRows)
	k := len(els)
	if len(ownerRows) > 0 {
		if len(els)%len(ownerRows) != 0 {
			return // the design does not match its data: left as published
		}
		k = len(els) / len(ownerRows)
	}
	if k == 0 {
		return // no row design published (the owner had no rows)
	}
	design := els[:k]
	var rows []json.RawMessage
	_ = json.Unmarshal(v, &rows)
	var own map[string]json.RawMessage
	if len(ownerRows) > 0 {
		own = rowValues(f, ownerRows[0])
	}
	sub := storageIndex(f.Fields)
	if f.Type == "gallery" {
		sub = map[string]StorageField{".": {Name: ".", Type: "image"}}
	}
	var kids []*xhtml.Node
	for _, row := range rows {
		vals := rowValues(f, row)
		for _, d := range design {
			c := clone(d)
			if s.node(c, sub, vals, own) {
				kids = append(kids, c)
			}
		}
	}
	setChildren(n, kids)
}

// rowValues are the values a row's fields are looked up in; a gallery's
// item is the picture itself, named ".".
func rowValues(f StorageField, row json.RawMessage) map[string]json.RawMessage {
	if f.Type == "gallery" {
		return map[string]json.RawMessage{".": row}
	}
	var m map[string]json.RawMessage
	_ = json.Unmarshal(row, &m)
	if m == nil {
		m = map[string]json.RawMessage{}
	}
	return m
}

var langKeyRe = regexp.MustCompile(`^[a-z]{2,3}-[a-z0-9]{2,4}$`)

// pick is a translated value in the language (grapesjs-storage's
// pickLang): its own, else the fallback's, else the first filled one. A
// plain value is the same in every language.
func (s storageFiller) pick(v json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if len(v) == 0 || v[0] != '{' || json.Unmarshal(v, &m) != nil || len(m) == 0 {
		return v
	}
	for k := range m {
		if !langKeyRe.MatchString(k) {
			return v
		}
	}
	empty := func(x json.RawMessage) bool {
		t := strings.TrimSpace(string(x))
		return t == "" || t == "null" || t == `""` || t == "[]"
	}
	if x, ok := m[s.d.Lang]; ok && !empty(x) {
		return x
	}
	if x, ok := m[s.d.Fallback]; ok && !empty(x) {
		return x
	}
	for _, x := range m {
		if !empty(x) {
			return x
		}
	}
	return nil
}

func emptyValue(v json.RawMessage) bool {
	t := strings.TrimSpace(string(v))
	switch t {
	case "", "null", "[]", "{}":
		return true
	}
	var str string
	if json.Unmarshal(v, &str) == nil {
		if mediaRe.MatchString(str) {
			return false
		}
		return strings.TrimSpace(tagRe.ReplaceAllString(str, "")) == ""
	}
	return false
}

var (
	tagRe   = regexp.MustCompile(`<[^>]*>`)
	mediaRe = regexp.MustCompile(`(?i)<(img|iframe|video|hr)\b`)
)

func esc(s string) string { return html.EscapeString(s) }

// valueHTML is a value as an element holding it shows it (schema.ts valueHtml).
func (s storageFiller) valueHTML(f StorageField, v json.RawMessage) string {
	if f.Type == "true_false" {
		switch strings.TrimSpace(string(v)) {
		case "true":
			return esc(f.OnText)
		case "false":
			return esc(f.OffText)
		}
		return ""
	}
	if emptyValue(v) {
		return ""
	}
	str := asString(v)
	switch f.Type {
	case "wysiwyg":
		return str // cleaned by the policy after parsing
	case "textarea":
		return strings.ReplaceAll(strings.ReplaceAll(esc(str), "\r\n", "<br>"), "\n", "<br>")
	case "email":
		return `<a href="mailto:` + esc(str) + `">` + esc(str) + `</a>`
	case "url":
		return `<a href="` + esc(str) + `">` + esc(str) + `</a>`
	case "select", "checkbox", "radio", "button_group":
		var list []json.RawMessage
		if json.Unmarshal(v, &list) != nil {
			list = []json.RawMessage{v}
		}
		labels := make([]string, 0, len(list))
		for _, x := range list {
			labels = append(labels, choiceLabel(f, asString(x)))
		}
		return esc(strings.Join(labels, ", "))
	case "image":
		u, alt := imageValue(v)
		return `<img src="` + esc(u) + `" alt="` + esc(alt) + `">`
	case "gallery":
		var list []json.RawMessage
		_ = json.Unmarshal(v, &list)
		var b strings.Builder
		for _, x := range list {
			u, alt := imageValue(x)
			b.WriteString(`<img src="` + esc(u) + `" alt="` + esc(alt) + `">`)
		}
		return b.String()
	case "file":
		u, name := fileValue(v)
		return `<a href="` + esc(u) + `" download>` + esc(name) + `</a>`
	case "link":
		u, title, target := linkValue(v)
		blank := ""
		if target == "_blank" {
			blank = ` target="_blank" rel="noopener"`
		}
		if title == "" {
			title = u
		}
		return `<a href="` + esc(u) + `"` + blank + `>` + esc(title) + `</a>`
	case "page_link":
		var ids []json.RawMessage
		if json.Unmarshal(v, &ids) != nil {
			ids = []json.RawMessage{v}
		}
		var parts []string
		for _, id := range ids {
			if p, ok := s.page(asString(id)); ok {
				parts = append(parts, `<a href="`+esc(p.Slug)+`">`+esc(p.Name)+`</a>`)
			}
		}
		return strings.Join(parts, ", ")
	case "date", "datetime", "time":
		return esc(FormatDateValue(str, f.Format))
	case "group", "repeater":
		return ""
	}
	return esc(str)
}

// linkOf is an <a> field's href, text and extra attributes.
func (s storageFiller) linkOf(f StorageField, v json.RawMessage) (href, text string, extra []xhtml.Attribute) {
	if emptyValue(v) {
		return "", "", nil
	}
	switch f.Type {
	case "link":
		u, title, target := linkValue(v)
		if target == "_blank" {
			extra = []xhtml.Attribute{{Key: "target", Val: "_blank"}, {Key: "rel", Val: "noopener"}}
		}
		if title == "" {
			title = u
		}
		return u, title, extra
	case "email":
		str := asString(v)
		return "mailto:" + str, str, nil
	case "file":
		u, name := fileValue(v)
		return u, name, []xhtml.Attribute{{Key: "download", Val: ""}}
	case "page_link":
		var ids []json.RawMessage
		if json.Unmarshal(v, &ids) != nil {
			ids = []json.RawMessage{v}
		}
		if len(ids) > 0 {
			if p, ok := s.page(asString(ids[0])); ok {
				return p.Slug, p.Name, nil
			}
		}
		return "", "", nil
	}
	str := asString(v)
	return str, str, nil
}

func (s storageFiller) page(id string) (PageRef, bool) {
	if s.d.Pages == nil || id == "" {
		return PageRef{}, false
	}
	p, ok := s.d.Pages(id)
	if ok && p.Slug == "" {
		p.Slug = "#"
	}
	return p, ok
}

func choiceLabel(f StorageField, v string) string {
	for _, c := range f.Choices {
		if c.Value == v {
			return c.Label
		}
	}
	return v
}

func imageValue(v json.RawMessage) (u, alt string) {
	var o struct {
		URL string `json:"url"`
		Alt string `json:"alt"`
	}
	if json.Unmarshal(v, &o) == nil && o.URL != "" {
		return o.URL, o.Alt
	}
	return asString(v), ""
}

func fileValue(v json.RawMessage) (u, name string) {
	var o struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if json.Unmarshal(v, &o) != nil || o.URL == "" {
		o.URL = asString(v)
	}
	name = o.Name
	if name == "" {
		last := o.URL[strings.LastIndex(o.URL, "/")+1:]
		if d, err := url.PathUnescape(last); err == nil {
			last = d
		}
		name = last
	}
	return o.URL, name
}

func linkValue(v json.RawMessage) (u, title, target string) {
	var o struct {
		URL    string `json:"url"`
		Title  string `json:"title"`
		Target string `json:"target"`
	}
	if json.Unmarshal(v, &o) == nil && o.URL != "" {
		return o.URL, o.Title, o.Target
	}
	return asString(v), "", ""
}

var dateValueRe = regexp.MustCompile(`^(?:(\d{4})-(\d{2})-(\d{2}))?[T ]?(?:(\d{2}):(\d{2})(?::(\d{2}))?)?`)
var dateTokenRe = regexp.MustCompile(`YYYY|YY|MM|M|DD|D|HH|H|mm|ss`)

// FormatDateValue shows a date, date-time or time value by a format of
// YYYY YY MM M DD D HH H mm ss (schema.ts formatDateValue); no format: as
// it is.
func FormatDateValue(v, format string) string {
	if format == "" {
		return v
	}
	t := strings.TrimSpace(v)
	m := dateValueRe.FindStringSubmatch(t)
	if m == nil || len(m[0]) != len(t) {
		// a full ISO time (seconds' fractions, a zone): its date and time parts
		if len(t) < 16 || t[4] != '-' || t[10] != 'T' {
			return v
		}
		m = dateValueRe.FindStringSubmatch(t[:16])
	}
	num := func(s string) string {
		s = strings.TrimLeft(s, "0")
		if s == "" {
			return "0"
		}
		return s
	}
	y, mo, d, h, mi, sec := m[1], m[2], m[3], m[4], m[5], m[6]
	if sec == "" {
		sec = "00"
	}
	return dateTokenRe.ReplaceAllStringFunc(format, func(k string) string {
		switch k {
		case "YYYY":
			return y
		case "YY":
			if len(y) == 4 {
				return y[2:]
			}
			return ""
		case "MM":
			return mo
		case "M":
			if mo == "" {
				return ""
			}
			return num(mo)
		case "DD":
			return d
		case "D":
			if d == "" {
				return ""
			}
			return num(d)
		case "HH":
			return h
		case "H":
			if h == "" {
				return ""
			}
			return num(h)
		case "mm":
			return mi
		case "ss":
			return sec
		}
		return k
	})
}
