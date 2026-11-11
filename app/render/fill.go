package render

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Field is what the filler needs to know about a template field (the
// store's field list, or the element's own data-field-type when the list
// does not name it).
type Field struct {
	Name   string
	Type   string
	Format string
	Fields []Field // the item of a list
}

// Entry is what the {{entry.…}} placeholders show.
type Entry struct {
	Name        string
	Slug        string
	PublishedAt *time.Time
}

// Fill writes an entry's values into a template's field elements, the
// elements marked data-field="<name>":
//
//	text, select   the element's text (escaped)
//	richtext       the element's children (the value is HTML, sanitized by p)
//	number         the text, by the field's format ("0.00", "#,##0") or data-field-format
//	date           the text: an ISO date, or the format's YYYY MM DD
//	boolean        false removes the element, true keeps it
//	image          src and alt ({"url", "alt"} or a URL)
//	link           href and, given a label, the text ({"url", "label"} or a URL)
//	list           the element's first child (or its data-field-item child)
//	               repeated once per item, the item's own fields filled
//
// A field without a value keeps the template's placeholder. values are by
// field name (the store keeps them under field.<name>).
//
// Placeholders in text and in href, src, alt, title and aria-label are
// replaced too (see Placeholder): {{title}} is a field's value as text,
// {{entry.slug}} the entry's own. Inside a list's item they name the
// item's fields.
func Fill(template string, fields []Field, values map[string]json.RawMessage, entry Entry, p Policy) (string, error) {
	nodes, err := ParseFragment(template)
	if err != nil {
		return "", err
	}
	f := filler{p: p, entry: entry}
	nodes = f.nodes(nodes, index(fields), values)
	return Render(nodes)
}

func index(fields []Field) map[string]Field {
	m := make(map[string]Field, len(fields))
	for _, f := range fields {
		m[f.Name] = f
	}
	return m
}

type filler struct {
	p     Policy
	entry Entry
}

// nodes fills a list of sibling nodes; a node may disappear (a false
// boolean).
func (fl filler) nodes(nodes []*html.Node, fields map[string]Field, values map[string]json.RawMessage) []*html.Node {
	out := nodes[:0]
	for _, n := range nodes {
		if fl.node(n, fields, values) {
			out = append(out, n)
		}
	}
	return out
}

// node fills n and what is inside it; false: remove n.
func (fl filler) node(n *html.Node, fields map[string]Field, values map[string]json.RawMessage) bool {
	switch n.Type {
	case html.TextNode:
		if strings.Contains(n.Data, "{{") {
			n.Data = fl.replace(n.Data, "", fields, values)
		}
		return true
	case html.ElementNode:
	default:
		return true
	}
	fl.attrs(n, fields, values)
	if n.Data == "script" || n.Data == "style" {
		return true // their text stays as it is: what a placeholder put there would not be escaped
	}
	name := attr(n, "data-field")
	if name == "" {
		fl.children(n, fields, values)
		return true
	}
	f, ok := fields[name]
	if !ok {
		f = Field{Name: name, Type: attr(n, "data-field-type")}
	}
	if f.Format == "" {
		f.Format = attr(n, "data-field-format")
	}
	v, has := values[name]
	if !has || string(v) == "null" {
		fl.children(n, fields, values) // the placeholder stays, its own placeholders filled
		return true
	}
	switch f.Type {
	case "richtext":
		s := asString(v)
		clean, err := ParseFragment(s)
		if err != nil {
			return true
		}
		setChildren(n, fl.p.clean(clean))
	case "number":
		setText(n, formatNumber(v, f.Format))
	case "date":
		setText(n, formatDate(asString(v), f.Format))
	case "boolean":
		var b bool
		if json.Unmarshal(v, &b) == nil && !b {
			return false
		}
		fl.children(n, fields, values)
	case "image":
		url, alt := image(v)
		if u, ok := cleanURL(url, true); ok && u != "" {
			n.Attr = setAttr(n.Attr, "src", u)
			n.Attr = removeAttr(n.Attr, "srcset") // the placeholder's
		}
		if alt != "" || url != "" {
			n.Attr = setAttr(n.Attr, "alt", alt)
		}
	case "link":
		url, label := link(v)
		if u, ok := cleanURL(url, false); ok && u != "" {
			n.Attr = setAttr(n.Attr, "href", u)
		}
		if label != "" {
			setText(n, label)
		} else {
			fl.children(n, fields, values)
		}
	case "list":
		fl.list(n, f, v)
	default: // text, select and unknown types: text
		setText(n, asString(v))
	}
	return true
}

func (fl filler) children(n *html.Node, fields map[string]Field, values map[string]json.RawMessage) {
	setChildren(n, fl.nodes(children(n), fields, values))
}

// list repeats the list element's item once per value.
func (fl filler) list(n *html.Node, f Field, v json.RawMessage) {
	var items []map[string]json.RawMessage
	if json.Unmarshal(v, &items) != nil {
		return
	}
	var item *html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && hasAttr(c, "data-field-item") {
			item = c
			break
		}
	}
	if item == nil {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode {
				item = c
				break
			}
		}
	}
	if item == nil {
		return
	}
	sub := index(f.Fields)
	var kids []*html.Node
	for _, values := range items {
		c := clone(item)
		if fl.node(c, sub, values) {
			kids = append(kids, c)
		}
	}
	setChildren(n, kids)
}

/* ---------------- placeholders ---------------- */

// Placeholder matches {{name}} (a field: a-z, digits, _) and
// {{entry.name}}; spaces inside the braces are allowed. Anything else in
// braces is left as it is.
var Placeholder = regexp.MustCompile(`\{\{\s*((?:entry\.)?[a-z][a-z0-9_]{0,63})\s*\}\}`)

// placeholderAttrs are the attributes whose placeholders are filled; href
// and src are checked as URLs afterwards.
var placeholderAttrs = map[string]bool{"href": true, "src": true, "alt": true, "title": true, "aria-label": true}

func (fl filler) attrs(n *html.Node, fields map[string]Field, values map[string]json.RawMessage) {
	out := n.Attr[:0]
	for _, a := range n.Attr {
		key := strings.ToLower(a.Key)
		if a.Namespace == "" && placeholderAttrs[key] && strings.Contains(a.Val, "{{") {
			a.Val = fl.replace(a.Val, key, fields, values)
			if key == "href" || key == "src" {
				u, ok := cleanURL(a.Val, key == "src")
				if !ok || u == "" {
					continue // an unsafe or empty address: no attribute
				}
				a.Val = u
			}
		}
		out = append(out, a)
	}
	n.Attr = out
}

func (fl filler) replace(s, attr string, fields map[string]Field, values map[string]json.RawMessage) string {
	return Placeholder.ReplaceAllStringFunc(s, func(m string) string {
		name := Placeholder.FindStringSubmatch(m)[1]
		if key, ok := strings.CutPrefix(name, "entry."); ok {
			return fl.entryValue(key)
		}
		f, ok := fields[name]
		if !ok {
			f = Field{Name: name, Type: "text"}
		}
		v, has := values[name]
		if !has || string(v) == "null" {
			return ""
		}
		return valueText(f, v, attr)
	})
}

func (fl filler) entryValue(key string) string {
	switch key {
	case "name":
		return fl.entry.Name
	case "slug", "url":
		return fl.entry.Slug
	case "published_at":
		if fl.entry.PublishedAt != nil {
			return fl.entry.PublishedAt.UTC().Format("2006-01-02")
		}
	}
	return ""
}

// valueText is a value as a placeholder shows it: text by the field's
// type; an image is its URL in src and its alt text elsewhere, a link its
// URL in href and its label (or URL) elsewhere; yes/no and lists show
// nothing (they are elements' fields).
func valueText(f Field, v json.RawMessage, attr string) string {
	switch f.Type {
	case "number":
		return formatNumber(v, f.Format)
	case "date":
		return formatDate(asString(v), f.Format)
	case "richtext":
		nodes, err := ParseFragment(asString(v))
		if err != nil {
			return ""
		}
		var b strings.Builder
		for _, n := range nodes {
			textOf(n, &b)
		}
		return b.String()
	case "image":
		url, alt := image(v)
		if attr == "src" {
			return url
		}
		return alt
	case "link":
		url, label := link(v)
		if attr == "href" || label == "" {
			return url
		}
		return label
	case "boolean", "list":
		return ""
	}
	return asString(v)
}

func textOf(n *html.Node, b *strings.Builder) {
	if n.Type == html.TextNode {
		b.WriteString(n.Data)
		return
	}
	if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		textOf(c, b)
	}
}

func clone(n *html.Node) *html.Node {
	c := &html.Node{Type: n.Type, DataAtom: n.DataAtom, Data: n.Data, Namespace: n.Namespace,
		Attr: append([]html.Attribute(nil), n.Attr...)}
	for k := n.FirstChild; k != nil; k = k.NextSibling {
		c.AppendChild(clone(k))
	}
	return c
}

func setText(n *html.Node, s string) {
	setChildren(n, []*html.Node{{Type: html.TextNode, Data: s}})
}

// asString is a JSON string's value, or the JSON of a number or boolean.
func asString(v json.RawMessage) string {
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(v))
}

func image(v json.RawMessage) (url, alt string) {
	var obj struct {
		URL string `json:"url"`
		Alt string `json:"alt"`
	}
	if json.Unmarshal(v, &obj) == nil {
		return obj.URL, obj.Alt
	}
	return asString(v), ""
}

func link(v json.RawMessage) (url, label string) {
	var obj struct {
		URL   string `json:"url"`
		Label string `json:"label"`
	}
	if json.Unmarshal(v, &obj) == nil {
		return obj.URL, obj.Label
	}
	return asString(v), ""
}

// formatNumber: "0" → 12, "0.00" → 12.50, "#,##0.00" → 1,234.50 (the
// digits after the point in the format, a comma before it for thousands).
func formatNumber(v json.RawMessage, format string) string {
	f, err := strconv.ParseFloat(asString(v), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return asString(v)
	}
	if format == "" {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	decimals := 0
	if i := strings.IndexByte(format, '.'); i >= 0 {
		decimals = len(format) - i - 1
	}
	scale := math.Pow(10, float64(decimals))
	f = math.Round(f*scale) / scale
	s := strconv.FormatFloat(math.Abs(f), 'f', decimals, 64)
	if strings.Contains(format, ",") {
		intPart, frac, _ := strings.Cut(s, ".")
		var b strings.Builder
		for i, d := range intPart {
			if i > 0 && (len(intPart)-i)%3 == 0 {
				b.WriteByte(',')
			}
			b.WriteRune(d)
		}
		s = b.String()
		if frac != "" {
			s += "." + frac
		}
	}
	if f < 0 && strings.Trim(s, "0.,") != "" {
		s = "-" + s
	}
	return s
}

// formatDate: an ISO date (or date-time) by YYYY, MM, DD in the format;
// without a format the date as given.
func formatDate(s, format string) string {
	var t time.Time
	var err error
	if t, err = time.Parse("2006-01-02", s); err != nil {
		if t, err = time.Parse(time.RFC3339, s); err != nil {
			return s
		}
	}
	if format == "" {
		return s
	}
	return strings.NewReplacer("YYYY", t.Format("2006"), "MM", t.Format("01"), "DD", t.Format("02")).Replace(format)
}

// Compose puts a page's HTML into its layout: in place of the layout's
// <luna-slot> (or <page-slot>) element, or inside the element marked
// data-luna-slot (or data-page-slot).
// Without a slot the page follows the layout.
func Compose(layout, body string) (string, error) {
	if strings.TrimSpace(layout) == "" {
		return body, nil
	}
	lnodes, err := ParseFragment(layout)
	if err != nil {
		return "", err
	}
	bnodes, err := ParseFragment(body)
	if err != nil {
		return "", err
	}
	lnodes, ok := placeSlot(lnodes, bnodes)
	if !ok {
		lnodes = append(lnodes, bnodes...)
	}
	return Render(lnodes)
}

// HasSlot reports whether a layout marks the page's place.
func HasSlot(layout string) bool {
	nodes, err := ParseFragment(layout)
	if err != nil {
		return false
	}
	return findSlot(nodes) != nil
}

func findSlot(nodes []*html.Node) *html.Node {
	for _, n := range nodes {
		if n.Type != html.ElementNode {
			continue
		}
		if isSlot(n) {
			return n
		}
		var kids []*html.Node
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			kids = append(kids, c)
		}
		if s := findSlot(kids); s != nil {
			return s
		}
	}
	return nil
}

// placeSlot returns the layout with the body in the slot's place.
func placeSlot(layout, body []*html.Node) ([]*html.Node, bool) {
	slot := findSlot(layout)
	if slot == nil {
		return layout, false
	}
	if !isSlotTag(slot.Data) {
		setChildren(slot, body)
		return layout, true
	}
	if slot.Parent == nil { // a top-level <luna-slot>
		out := make([]*html.Node, 0, len(layout)+len(body))
		for _, n := range layout {
			if n == slot {
				out = append(out, body...)
			} else {
				out = append(out, n)
			}
		}
		return out, true
	}
	for _, b := range body {
		slot.Parent.InsertBefore(b, slot)
	}
	slot.Parent.RemoveChild(slot)
	return layout, true
}
