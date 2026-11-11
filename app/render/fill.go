package render

import (
	"encoding/json"
	"math"
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
func Fill(template string, fields []Field, values map[string]json.RawMessage, p Policy) (string, error) {
	nodes, err := ParseFragment(template)
	if err != nil {
		return "", err
	}
	nodes = fillNodes(nodes, index(fields), values, p)
	return Render(nodes)
}

func index(fields []Field) map[string]Field {
	m := make(map[string]Field, len(fields))
	for _, f := range fields {
		m[f.Name] = f
	}
	return m
}

// fillNodes fills a list of sibling nodes; a node may disappear (a false
// boolean).
func fillNodes(nodes []*html.Node, fields map[string]Field, values map[string]json.RawMessage, p Policy) []*html.Node {
	out := nodes[:0]
	for _, n := range nodes {
		if fillNode(n, fields, values, p) {
			out = append(out, n)
		}
	}
	return out
}

// fillNode fills n and what is inside it; false: remove n.
func fillNode(n *html.Node, fields map[string]Field, values map[string]json.RawMessage, p Policy) bool {
	if n.Type != html.ElementNode {
		return true
	}
	name := attr(n, "data-field")
	if name == "" {
		kids := fillNodes(children(n), fields, values, p)
		setChildren(n, kids)
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
		return true // the placeholder stays
	}
	switch f.Type {
	case "richtext":
		s := asString(v)
		clean, err := ParseFragment(s)
		if err != nil {
			return true
		}
		setChildren(n, p.clean(clean))
	case "number":
		setText(n, formatNumber(v, f.Format))
	case "date":
		setText(n, formatDate(asString(v), f.Format))
	case "boolean":
		var b bool
		if json.Unmarshal(v, &b) == nil && !b {
			return false
		}
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
		}
	case "list":
		fillList(n, f, v, p)
	default: // text, select and unknown types: text
		setText(n, asString(v))
	}
	return true
}

// fillList repeats the list element's item once per value.
func fillList(n *html.Node, f Field, v json.RawMessage, p Policy) {
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
		if fillNode(c, sub, values, p) {
			kids = append(kids, c)
		}
	}
	setChildren(n, kids)
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
// <luna-slot> element, or inside the element marked data-luna-slot.
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
		if n.Data == SlotTag || hasAttr(n, "data-luna-slot") {
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
	if slot.Data != SlotTag {
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
