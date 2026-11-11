// Package render turns what the editor publishes into what a site shows:
// it cleans the editor's HTML and CSS (an allow-list: everything the
// editor sends is untrusted), fills a template's field elements with an
// entry's values, and puts a page into its layout. Pure functions over
// golang.org/x/net/html; the store keeps the results.
package render

import (
	"bytes"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Policy is what the sanitizer lets through beyond the fixed allow-list.
// Its zero value is the strict default.
type Policy struct {
	// IframeHosts are the hosts an <iframe src> may point at (exact host
	// names, https only). nil means DefaultIframeHosts.
	IframeHosts []string
	// CustomCode keeps <script> elements (the site's own decision, a site
	// setting). Event handler attributes and javascript: URLs are removed
	// either way.
	CustomCode bool
}

// DefaultIframeHosts: video players and maps.
var DefaultIframeHosts = []string{
	"www.youtube.com", "youtube.com", "www.youtube-nocookie.com",
	"player.vimeo.com",
	"www.google.com", "maps.google.com",
	"open.spotify.com",
}

// SlotTag is the element a layout marks the page's place with; an element
// with the attribute data-luna-slot works too, and so do grapesjs-pages'
// own <page-slot> and data-page-slot (SlotTags, SlotAttrs).
const SlotTag = "luna-slot"

// SlotTags and SlotAttrs: every way a layout may mark the slot.
var (
	SlotTags  = []string{SlotTag, "page-slot"}
	SlotAttrs = []string{"data-luna-slot", "data-page-slot"}
)

func isSlot(n *html.Node) bool {
	for _, t := range SlotTags {
		if n.Data == t {
			return true
		}
	}
	for _, a := range SlotAttrs {
		if hasAttr(n, a) {
			return true
		}
	}
	return false
}

func isSlotTag(name string) bool {
	for _, t := range SlotTags {
		if name == t {
			return true
		}
	}
	return false
}

// Elements kept as they are. Anything else that is not dropped (below) is
// unwrapped: its children stay, the element goes (a <form> keeps its text).
var allowedElements = map[string]bool{}

func init() {
	for _, e := range strings.Fields(`
		a abbr address article aside b bdi bdo blockquote br caption cite code col colgroup
		data dd del details dfn div dl dt em figcaption figure footer h1 h2 h3 h4 h5 h6
		header hgroup hr i img ins kbd li main mark nav ol p picture pre q rp rt ruby s samp
		section small source span strong sub summary sup table tbody td tfoot th thead time
		tr u ul var wbr audio video track iframe button label
		svg g path circle ellipse line polyline polygon rect text tspan defs use symbol
		lineargradient radialgradient stop clippath mask title desc ` + strings.Join(SlotTags, " ")) {
		allowedElements[e] = true
	}
}

// Elements dropped with everything inside them.
var droppedElements = map[string]bool{
	"script": true, "style": true, "noscript": true, "template": true, "object": true,
	"embed": true, "applet": true, "frame": true, "frameset": true, "base": true,
	"meta": true, "link": true, "head": true, "input": true,
	"select": true, "textarea": true, "option": true, "math": true, "foreignobject": true,
	"portal": true, "dialog": true, "param": true,
}

// Attributes any element may have; data-* and aria-* are allowed too.
var globalAttrs = map[string]bool{
	"id": true, "class": true, "style": true, "title": true, "lang": true, "dir": true,
	"role": true, "hidden": true, "tabindex": true, "translate": true,
}

// Attributes per element (on top of the global ones).
var elementAttrs = map[string]map[string]bool{
	"a":          set("href", "target", "rel", "download", "hreflang", "name"),
	"img":        set("src", "alt", "width", "height", "srcset", "sizes", "loading", "decoding"),
	"source":     set("src", "srcset", "sizes", "type", "media", "width", "height"),
	"video":      set("src", "poster", "controls", "autoplay", "muted", "loop", "playsinline", "preload", "width", "height"),
	"audio":      set("src", "controls", "autoplay", "muted", "loop", "preload"),
	"track":      set("src", "kind", "srclang", "label", "default"),
	"iframe":     set("src", "width", "height", "allow", "allowfullscreen", "frameborder", "loading", "referrerpolicy"),
	"td":         set("colspan", "rowspan", "headers"),
	"th":         set("colspan", "rowspan", "headers", "scope", "abbr"),
	"col":        set("span", "width"),
	"colgroup":   set("span"),
	"ol":         set("start", "type", "reversed"),
	"li":         set("value"),
	"time":       set("datetime"),
	"data":       set("value"),
	"del":        set("datetime", "cite"),
	"ins":        set("datetime", "cite"),
	"q":          set("cite"),
	"blockquote": set("cite"),
	"details":    set("open"),
	"button":     set("type", "disabled", "name", "value"),
	"label":      set("for"),
	"svg":        set("viewbox", "width", "height", "fill", "stroke", "xmlns", "preserveaspectratio", "version"),
}

// SVG presentation attributes, on every SVG element.
var svgAttrs = set("d", "fill", "fill-rule", "clip-rule", "fill-opacity", "stroke", "stroke-width",
	"stroke-linecap", "stroke-linejoin", "stroke-opacity", "stroke-dasharray", "opacity", "transform",
	"cx", "cy", "r", "rx", "ry", "x", "y", "x1", "y1", "x2", "y2", "points", "width", "height",
	"viewbox", "offset", "stop-color", "stop-opacity", "gradientunits", "gradienttransform",
	"text-anchor", "font-size", "font-family", "font-weight", "clip-path", "mask", "href", "xlink:href", "xmlns:xlink")

// Attributes whose value is a URL.
var urlAttrs = set("href", "src", "poster", "cite", "xlink:href")

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// SanitizeHTML cleans an HTML fragment (the editor's getHtml(): a body's
// content) by the allow-list and the policy, and returns it serialized.
func SanitizeHTML(src string, p Policy) (string, error) {
	nodes, err := ParseFragment(src)
	if err != nil {
		return "", err
	}
	nodes = p.clean(nodes)
	return Render(nodes)
}

// ParseFragment parses HTML as the content of a <body>.
func ParseFragment(src string) ([]*html.Node, error) {
	body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	return html.ParseFragment(strings.NewReader(src), body)
}

// Render serializes nodes.
func Render(nodes []*html.Node) (string, error) {
	var b bytes.Buffer
	for _, n := range nodes {
		if err := html.Render(&b, n); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

// clean returns the nodes that survive, cleaned; unwrapped elements put
// their children in their place.
func (p Policy) clean(nodes []*html.Node) []*html.Node {
	var out []*html.Node
	for _, n := range nodes {
		out = append(out, p.cleanNode(n)...)
	}
	return out
}

func (p Policy) cleanNode(n *html.Node) []*html.Node {
	switch n.Type {
	case html.TextNode:
		return []*html.Node{n}
	case html.ElementNode:
	default: // comments, doctypes
		return nil
	}
	name := strings.ToLower(n.Data)
	if name == "script" && p.CustomCode {
		n.Attr = p.scriptAttrs(n.Attr)
		return []*html.Node{n}
	}
	if droppedElements[name] {
		return nil
	}
	kids := p.clean(children(n))
	if !allowedElements[name] {
		for _, k := range kids {
			k.Parent, k.PrevSibling, k.NextSibling = nil, nil, nil
		}
		return kids
	}
	if name == "iframe" && !p.iframeAllowed(attr(n, "src")) {
		return nil
	}
	n.Attr = p.cleanAttrs(name, n.Namespace == "svg" || svgElement(name), n.Attr)
	setChildren(n, kids)
	return []*html.Node{n}
}

func svgElement(name string) bool {
	switch name {
	case "svg", "g", "path", "circle", "ellipse", "line", "polyline", "polygon", "rect", "text",
		"tspan", "defs", "use", "symbol", "lineargradient", "radialgradient", "stop", "clippath", "mask":
		return true
	}
	return false
}

func (p Policy) cleanAttrs(el string, svg bool, attrs []html.Attribute) []html.Attribute {
	out := attrs[:0]
	for _, a := range attrs {
		key := strings.ToLower(a.Key)
		if a.Namespace != "" {
			key = a.Namespace + ":" + key
		}
		ok := globalAttrs[key] || elementAttrs[el][key] || (svg && svgAttrs[key]) ||
			validDataAttr(key) || strings.HasPrefix(key, "aria-")
		if !ok || strings.HasPrefix(key, "on") {
			continue
		}
		switch {
		case urlAttrs[key]:
			v, ok := cleanURL(a.Val, el == "img" && key == "src")
			if !ok {
				continue
			}
			a.Val = v
		case key == "srcset":
			v, ok := cleanSrcset(a.Val)
			if !ok {
				continue
			}
			a.Val = v
		case key == "style":
			a.Val = SanitizeStyle(a.Val)
			if a.Val == "" {
				continue
			}
		case key == "target":
			if a.Val != "_blank" && a.Val != "_self" && a.Val != "_parent" && a.Val != "_top" {
				continue
			}
		}
		out = append(out, a)
	}
	// A link that opens a new tab may not reach back to the page.
	if el == "a" && attrValue(out, "target") == "_blank" {
		out = setAttr(out, "rel", addRel(attrValue(out, "rel"), "noopener", "noreferrer"))
	}
	return out
}

var dataAttrRe = regexp.MustCompile(`^data-[a-z0-9_.:-]+$`)

func validDataAttr(key string) bool { return dataAttrRe.MatchString(key) }

func (p Policy) scriptAttrs(attrs []html.Attribute) []html.Attribute {
	out := attrs[:0]
	for _, a := range attrs {
		key := strings.ToLower(a.Key)
		switch key {
		case "src":
			if v, ok := cleanURL(a.Val, false); ok && !strings.HasPrefix(v, "#") {
				a.Val = v
				out = append(out, a)
			}
		case "type", "async", "defer", "crossorigin", "integrity", "nomodule", "referrerpolicy":
			out = append(out, a)
		}
	}
	return out
}

func (p Policy) iframeAllowed(src string) bool {
	u, err := url.Parse(strings.TrimSpace(src))
	if err != nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	hosts := p.IframeHosts
	if hosts == nil {
		hosts = DefaultIframeHosts
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range hosts {
		if host == strings.ToLower(h) {
			return true
		}
	}
	return false
}

// cleanURL lets through http(s), mailto, tel, relative URLs and
// fragments; image sources may also be data:image/… (not SVG, which can
// carry script).
func cleanURL(raw string, image bool) (string, bool) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", true
	}
	// Browsers ignore control characters and whitespace inside a scheme
	// ("java\tscript:"): judge the URL without them.
	compact := strings.Map(func(r rune) rune {
		if r <= ' ' || r == 0x7f {
			return -1
		}
		return r
	}, v)
	lower := strings.ToLower(compact)
	if image && strings.HasPrefix(lower, "data:image/") {
		for _, t := range []string{"png", "jpeg", "jpg", "gif", "webp", "avif"} {
			if strings.HasPrefix(lower, "data:image/"+t+";") || strings.HasPrefix(lower, "data:image/"+t+",") {
				return v, true
			}
		}
		return "", false
	}
	colon := strings.IndexByte(lower, ':')
	if colon < 0 || strings.ContainsAny(lower[:colon], "/?#") {
		return v, true // relative
	}
	switch lower[:colon] {
	case "http", "https", "mailto", "tel":
		return v, true
	}
	return "", false
}

func cleanSrcset(raw string) (string, bool) {
	parts := strings.Split(raw, ",")
	var out []string
	for _, p := range parts {
		f := strings.Fields(p)
		if len(f) == 0 {
			continue
		}
		u, ok := cleanURL(f[0], true)
		if !ok || u == "" {
			return "", false
		}
		f[0] = u
		out = append(out, strings.Join(f, " "))
	}
	return strings.Join(out, ", "), len(out) > 0
}

func addRel(rel string, add ...string) string {
	have := map[string]bool{}
	f := strings.Fields(strings.ToLower(rel))
	for _, r := range f {
		have[r] = true
	}
	for _, a := range add {
		if !have[a] {
			f = append(f, a)
		}
	}
	return strings.Join(f, " ")
}

// ---- node helpers ----

func children(n *html.Node) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		n.RemoveChild(c)
		out = append(out, c)
		c = next
	}
	return out
}

func setChildren(n *html.Node, kids []*html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		n.RemoveChild(c)
		c = next
	}
	for _, k := range kids {
		if k.Parent != nil {
			k.Parent.RemoveChild(k)
		}
		k.PrevSibling, k.NextSibling = nil, nil
		n.AppendChild(k)
	}
}

func attr(n *html.Node, key string) string { return attrValue(n.Attr, key) }

func attrValue(attrs []html.Attribute, key string) string {
	for _, a := range attrs {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return true
		}
	}
	return false
}

func setAttr(attrs []html.Attribute, key, val string) []html.Attribute {
	for i, a := range attrs {
		if strings.EqualFold(a.Key, key) {
			attrs[i].Val = val
			return attrs
		}
	}
	return append(attrs, html.Attribute{Key: key, Val: val})
}

func removeAttr(attrs []html.Attribute, key string) []html.Attribute {
	out := attrs[:0]
	for _, a := range attrs {
		if !strings.EqualFold(a.Key, key) {
			out = append(out, a)
		}
	}
	return out
}
