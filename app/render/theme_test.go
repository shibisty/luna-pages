package render

import (
	"strings"
	"testing"
)

func TestThemeSettingsKept(t *testing.T) {
	in := `<p>Hi</p><script type="application/json" data-luna-darkreader onload="x()">{"on":true,"src":"https://evil.example/x.js","scheme":"light"}</script>` +
		`<script type="application/json" data-luna-darkreader>{"on":true,"css":"a{}</script><script>alert(1)</script>"}</script>` +
		`<script type="application/json" data-luna-darkreader>not json</script>` +
		`<script type="text/javascript" data-luna-darkreader>{"on":true}</script>`
	out, err := SanitizeHTML(in, Policy{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "<script") != 1 || strings.Contains(out, "evil.example") || strings.Contains(out, "alert(1)") || strings.Contains(out, "onload") {
		t.Fatalf("kept: %s", out)
	}
	if !strings.Contains(out, `<script type="application/json" data-luna-darkreader="">{"on":true,"scheme":"light"}</script>`) {
		t.Fatalf("settings: %s", out)
	}
	// "<" in the settings stays escaped, so nothing ends the element early.
	out, _ = SanitizeHTML(`<script type="application/json" data-luna-darkreader>{"on":true,"css":"a{color:red}</style>"}</script>`, Policy{})
	if !strings.Contains(out, `"css":"a{color:red}\u003c/style\u003e"`) {
		t.Fatalf("escaped: %s", out)
	}
}

func TestThemeRuntime(t *testing.T) {
	js := ThemeRuntime("https://cdn.test/darkreader.js")
	for _, want := range []string{`script[data-luna-darkreader]`, `c.src="https://cdn.test/darkreader.js"`, "gjsTheme", "prefers-color-scheme"} {
		if !strings.Contains(js, want) {
			t.Fatalf("no %q in %s", want, js[:200])
		}
	}
}
