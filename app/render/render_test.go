package render

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type fixture struct {
	Name     string                     `json:"name"`
	Fields   []fixtureField             `json:"fields"`
	Template string                     `json:"template"`
	Values   map[string]json.RawMessage `json:"values"`
	HTML     string                     `json:"html"`
}

type fixtureField struct {
	Name   string         `json:"name"`
	Type   string         `json:"type"`
	Format string         `json:"format"`
	Fields []fixtureField `json:"fields"`
}

func (f fixtureField) field() Field {
	out := Field{Name: f.Name, Type: f.Type, Format: f.Format}
	for _, s := range f.Fields {
		out.Fields = append(out.Fields, s.field())
	}
	return out
}

// TestFillFixtures runs the fixtures shared with grapesjs-storage's
// renderer (testdata/fill.json).
func TestFillFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/fill.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct{ Cases []fixture }
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var fields []Field
			for _, f := range c.Fields {
				fields = append(fields, f.field())
			}
			got, err := Fill(c.Template, fields, c.Values, Policy{})
			if err != nil {
				t.Fatal(err)
			}
			if got != c.HTML {
				t.Errorf("\n got: %s\nwant: %s", got, c.HTML)
			}
		})
	}
}

func TestSanitizeHTML(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"script dropped", `<p>a</p><script>alert(1)</script><p>b</p>`, `<p>a</p><p>b</p>`},
		{"style element dropped", `<style>body{}</style><p>x</p>`, `<p>x</p>`},
		{"event handlers removed", `<div onclick="x()" onMouseOver="y()" class="c">t</div>`, `<div class="c">t</div>`},
		{"javascript href removed", `<a href="javascript:alert(1)">x</a>`, `<a>x</a>`},
		{"obfuscated scheme removed", `<a href=" jav&#x09;ascript:alert(1)">x</a>`, `<a>x</a>`},
		{"vbscript and data links removed", `<a href="vbscript:x">a</a><a href="data:text/html,x">b</a>`, `<a>a</a><a>b</a>`},
		{"safe links kept", `<a href="https://e.com/a?b=1">a</a><a href="/p">b</a><a href="#top">c</a><a href="mailto:a@b.c">d</a>`,
			`<a href="https://e.com/a?b=1">a</a><a href="/p">b</a><a href="#top">c</a><a href="mailto:a@b.c">d</a>`},
		{"blank target gets noopener", `<a href="/x" target="_blank">x</a>`, `<a href="/x" target="_blank" rel="noopener noreferrer">x</a>`},
		{"unknown element unwrapped", `<blink><b>x</b></blink>`, `<b>x</b>`},
		{"form unwrapped, inputs dropped", `<form action="/x"><p>Name</p><input name="n"/></form>`, `<p>Name</p>`},
		{"comments dropped", `<p>a<!-- secret --></p>`, `<p>a</p>`},
		{"data and aria attributes kept", `<div data-field="title" data-gjs-type="text" aria-label="t">x</div>`,
			`<div data-field="title" data-gjs-type="text" aria-label="t">x</div>`},
		{"unknown attributes removed", `<div formaction="x" srcdoc="y">x</div>`, `<div>x</div>`},
		{"image data URL kept", `<img src="data:image/png;base64,AAAA"/>`, `<img src="data:image/png;base64,AAAA"/>`},
		{"svg data URL removed", `<img src="data:image/svg+xml,&lt;svg onload=alert(1)&gt;"/>`, `<img/>`},
		{"iframe from youtube kept", `<iframe src="https://www.youtube.com/embed/x" allowfullscreen=""></iframe>`,
			`<iframe src="https://www.youtube.com/embed/x" allowfullscreen=""></iframe>`},
		{"iframe from elsewhere dropped", `<iframe src="https://evil.example/x"></iframe><p>x</p>`, `<p>x</p>`},
		{"iframe over http dropped", `<iframe src="http://www.youtube.com/embed/x"></iframe>`, ``},
		{"style attribute cleaned", `<div style="color: red; background: url(javascript:alert(1))">x</div>`,
			`<div style="color: red; background: url()">x</div>`},
		{"expression in style removed", `<div style="width: expression(alert(1))">x</div>`, `<div style="width: x(alert(1))">x</div>`},
		{"slot kept", `<header>h</header><luna-slot></luna-slot>`, `<header>h</header><luna-slot></luna-slot>`},
		{"page-slot kept", `<page-slot id="i3"></page-slot>`, `<page-slot id="i3"></page-slot>`},
		{"svg kept", `<svg viewBox="0 0 10 10"><path d="M0 0L10 10" onclick="x()"></path></svg>`,
			`<svg viewBox="0 0 10 10"><path d="M0 0L10 10"></path></svg>`},
		{"svg script dropped", `<svg><script>alert(1)</script><a href="javascript:x"><text>t</text></a></svg>`, `<svg><a><text>t</text></a></svg>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := SanitizeHTML(c.in, Policy{})
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("\n got: %s\nwant: %s", got, c.want)
			}
		})
	}
}

func TestSanitizePolicy(t *testing.T) {
	in := `<script src="https://cdn.example.com/a.js" onload="x()"></script><script>track()</script><iframe src="https://maps.example.com/m"></iframe>`
	got, _ := SanitizeHTML(in, Policy{})
	if got != "" {
		t.Errorf("strict: %s", got)
	}
	got, _ = SanitizeHTML(in, Policy{CustomCode: true, IframeHosts: []string{"maps.example.com"}})
	want := `<script src="https://cdn.example.com/a.js"></script><script>track()</script><iframe src="https://maps.example.com/m"></iframe>`
	if got != want {
		t.Errorf("custom code:\n got: %s\nwant: %s", got, want)
	}
	got, _ = SanitizeHTML(`<script src="javascript:alert(1)"></script>`, Policy{CustomCode: true})
	if got != `<script></script>` {
		t.Errorf("javascript: src kept: %s", got)
	}
}

func TestSanitizeCSS(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain kept", `.a{color:red}@media (max-width:600px){.b{display:none}}`, `.a{color:red}@media (max-width:600px){.b{display:none}}`},
		{"comments dropped", `.a{/* x */color:red}`, `.a{color:red}`},
		{"closing style escaped", `.a{content:"</style><script>x</script>"}`, `.a{content:"<\/style><script>x<\/script>"}`},
		{"import dropped", `@import url("https://evil.example/x.css");.a{}`, `.a{}`},
		{"font import kept", `@import url('https://fonts.googleapis.com/css2?family=Inter');.a{}`, `@import url('https://fonts.googleapis.com/css2?family=Inter');.a{}`},
		{"expression removed", `.a{width:expression(alert(1))}`, `.a{width:x(alert(1))}`},
		{"escaped expression removed", `.a{width:\65 xpression(alert(1))}`, `.a{width:x(alert(1))}`},
		{"behavior removed", `.a{behavior:url(x.htc);-moz-binding:url(x.xml)}`, `.a{x-removed:url(x.htc);x-removed:url(x.xml)}`},
		{"javascript url removed", `.a{background:url(javascript:alert(1))}`, `.a{background:url()}`},
		{"escaped javascript url removed", `.a{background:url("java\73 cript:alert(1)")}`, `.a{background:url()}`},
		{"image urls kept", `.a{background:url(/img/a.png)}.b{background:url("https://cdn.e.com/b.jpg")}`, `.a{background:url(/img/a.png)}.b{background:url("https://cdn.e.com/b.jpg")}`},
		{"charset dropped", `@charset "utf-8";.a{}`, `.a{}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SanitizeCSS(c.in); got != c.want {
				t.Errorf("\n got: %s\nwant: %s", got, c.want)
			}
		})
	}
	if got := SanitizeStyle(`color:red;</style>`); got != "" {
		t.Errorf("style with markup: %q", got)
	}
}

func TestCompose(t *testing.T) {
	cases := []struct{ name, layout, body, want string }{
		{"no layout", ``, `<p>x</p>`, `<p>x</p>`},
		{"slot element", `<header>H</header><main><luna-slot></luna-slot></main><footer>F</footer>`, `<p>x</p><p>y</p>`,
			`<header>H</header><main><p>x</p><p>y</p></main><footer>F</footer>`},
		{"top-level slot", `<header>H</header><luna-slot></luna-slot><footer>F</footer>`, `<p>x</p>`, `<header>H</header><p>x</p><footer>F</footer>`},
		{"slot attribute", `<main data-luna-slot="">placeholder</main>`, `<p>x</p>`, `<main data-luna-slot=""><p>x</p></main>`},
		{"no slot: appended", `<header>H</header>`, `<p>x</p>`, `<header>H</header><p>x</p>`},
		{"grapesjs-pages slot", `<header>H</header><page-slot id="i3"></page-slot>`, `<p>x</p>`, `<header>H</header><p>x</p>`},
		{"grapesjs-pages slot attribute", `<main data-page-slot="">placeholder</main>`, `<p>x</p>`, `<main data-page-slot=""><p>x</p></main>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Compose(c.layout, c.body)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("\n got: %s\nwant: %s", got, c.want)
			}
		})
	}
	if !HasSlot(`<div><luna-slot></luna-slot></div>`) || !HasSlot(`<div data-luna-slot></div>`) || !HasSlot(`<page-slot></page-slot>`) ||
		!HasSlot(`<div data-page-slot></div>`) || HasSlot(`<div></div>`) {
		t.Error("HasSlot")
	}
}

func TestFormatting(t *testing.T) {
	num := func(v, f string) string { return formatNumber(json.RawMessage(v), f) }
	for _, c := range [][3]string{
		{"12", "", "12"}, {"12.5", "0", "13"}, {"-1234.5", "#,##0.00", "-1,234.50"}, {"999", "#,##0", "999"},
		{`"abc"`, "0.00", "abc"}, {"-0.001", "0.00", "0.00"}, {"1000000", "#,##0", "1,000,000"},
	} {
		if got := num(c[0], c[1]); got != c[2] {
			t.Errorf("formatNumber(%s, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
	if got := formatDate("2026-11-11T10:00:00Z", "YYYY/MM/DD"); got != "2026/11/11" {
		t.Errorf("date-time: %q", got)
	}
	if got := formatDate("soon", "DD.MM.YYYY"); got != "soon" {
		t.Errorf("not a date: %q", got)
	}
	if !strings.Contains(asString(json.RawMessage(`true`)), "true") {
		t.Error("asString bool")
	}
}
