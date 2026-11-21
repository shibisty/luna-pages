package render

import (
	"encoding/json"
	"strings"
	"testing"
)

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func TestFillStorage(t *testing.T) {
	design := `<article>` +
		`<h1 data-storage-field="name" data-storage-type="text">orm</h1>` +
		`<div data-storage-field="readme" data-storage-type="wysiwyg"><p>old</p></div>` +
		`<a data-storage-field="repository" data-storage-type="url" href="https://old">https://old</a>` +
		`<img data-storage-field="logo" data-storage-type="image" src="/old.png" alt="">` +
		`<span data-storage-field="downloads" data-storage-type="number">5</span>` +
		`<time data-storage-field="updated" data-storage-type="date">2026-01-01</time>` +
		`<ul data-storage-field="versions" data-storage-type="repeater">` +
		`<li><b data-storage-field="version" data-storage-type="text">1.0.0</b></li><li class="sep">·</li>` +
		`<li><b data-storage-field="version" data-storage-type="text">0.9.0</b></li><li class="sep">·</li>` +
		`</ul>` +
		`<div data-storage-field="meta" data-storage-type="group"><i data-storage-field="license" data-storage-type="text">MIT</i></div>` +
		`<p data-storage-field="yanked" data-storage-type="true_false">no</p>` +
		`</article>`
	fields := []StorageField{
		{Name: "name", Type: "text"}, {Name: "readme", Type: "wysiwyg"}, {Name: "repository", Type: "url"},
		{Name: "logo", Type: "image"}, {Name: "downloads", Type: "number"}, {Name: "updated", Type: "date", Format: "DD.MM.YYYY"},
		{Name: "versions", Type: "repeater", Fields: []StorageField{{Name: "version", Type: "text"}}},
		{Name: "meta", Type: "group", Fields: []StorageField{{Name: "license", Type: "text"}}},
		{Name: "yanked", Type: "true_false", OnText: "yes", OffText: "no"},
	}
	out, err := FillStorage(design, StorageData{
		Fields: fields,
		Values: map[string]json.RawMessage{
			"name":       raw(`{"uk-ua":"","en-gb":"passport"}`),
			"readme":     raw(`"<h2>Hi</h2><script>x()</script><p onclick=\"y\">there</p>"`),
			"repository": raw(`"https://github.com/shibisty/passport.go"`),
			"downloads":  raw(`42`),
			"updated":    raw(`"2026-11-21T05:00:00Z"`),
			"versions":   raw(`[{"version":"0.3.0"},{"version":"0.2.1"},{"version":"0.2.0"}]`),
			"meta":       raw(`{"license":"Apache-2.0"}`),
			"yanked":     raw(`true`),
		},
		Owner: map[string]json.RawMessage{"versions": raw(`[{"version":"1.0.0"},{"version":"0.9.0"}]`)},
		Lang:  "uk-ua",
	}, Policy{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<h1 data-storage-field="name" data-storage-type="text">passport</h1>`,
		`<h2>Hi</h2><p>there</p>`,
		`href="https://github.com/shibisty/passport.go">https://github.com/shibisty/passport.go</a>`,
		`data-storage-type="image" alt="" hidden=""`,
		`>42</span>`,
		`>21.11.2026</time>`,
		`<b data-storage-field="version" data-storage-type="text">0.3.0</b></li><li class="sep">·</li><li><b data-storage-field="version" data-storage-type="text">0.2.1</b></li><li class="sep">·</li><li><b data-storage-field="version" data-storage-type="text">0.2.0</b>`,
		`>Apache-2.0</i>`,
		`>yes</p>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
	if strings.Contains(out, "script") || strings.Contains(out, "onclick") || strings.Contains(out, "1.0.0") || strings.Contains(out, "/old.png") {
		t.Errorf("leftovers in %s", out)
	}
}

func TestFillStorageLinksAndGallery(t *testing.T) {
	design := `<a data-storage-field="home" data-storage-type="link" href="#">x</a>` +
		`<a data-storage-field="site" data-storage-type="url" href="#">x</a>` +
		`<a data-storage-field="bad" data-storage-type="url" href="#">x</a>` +
		`<div data-storage-field="pics" data-storage-type="gallery"><img data-storage-field="." data-storage-type="image" src="/a.png"></div>` +
		`<div data-storage-field="tags" data-storage-type="checkbox">a</div>`
	out, err := FillStorage(design, StorageData{
		Fields: []StorageField{{Name: "tags", Type: "checkbox", Choices: []StorageChoice{{Value: "go", Label: "Go"}, {Value: "db", Label: "Databases"}}},
			{Name: "pics", Type: "gallery"}},
		Values: map[string]json.RawMessage{
			"home": raw(`{"url":"https://shibisty.com","title":"Author","target":"_blank"}`),
			"bad":  raw(`"javascript:alert(1)"`),
			"pics": raw(`[{"url":"/1.png","alt":"one"},{"url":"/2.png"}]`),
			"tags": raw(`["go","db"]`),
		},
		Owner: map[string]json.RawMessage{"pics": raw(`[{"url":"/a.png"}]`)},
	}, Policy{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`href="https://shibisty.com" target="_blank" rel="noopener">Author</a>`,
		`data-storage-type="url" hidden="">`,
		`src="/1.png" alt="one"`, `src="/2.png" alt=""`,
		`>Go, Databases</div>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
	if strings.Contains(out, "javascript") || strings.Contains(out, "/a.png") {
		t.Errorf("leftovers in %s", out)
	}
}

func TestFormatDateValue(t *testing.T) {
	for _, c := range [][3]string{
		{"2026-03-05", "D.M.YYYY", "5.3.2026"},
		{"2026-03-05T07:08", "DD.MM.YY HH:mm", "05.03.26 07:08"},
		{"09:30", "H:mm", "9:30"},
		{"2026-03-05", "", "2026-03-05"},
		{"soon", "YYYY", "soon"},
	} {
		if got := FormatDateValue(c[0], c[1]); got != c[2] {
			t.Errorf("%s %s: %s, want %s", c[0], c[1], got, c[2])
		}
	}
}
