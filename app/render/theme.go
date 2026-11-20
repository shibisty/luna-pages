package render

import (
	_ "embed"
	"encoding/json"
	"strings"

	"golang.org/x/net/html"
)

// ThemeAttr marks the settings of grapesjs-darkreader's theme on a page:
// <script type="application/json" data-luna-darkreader>{…}</script>, which
// luna-studio's editor adds on publish. The sanitizer keeps it (it runs
// nothing) and ThemeRuntime is the script that reads it.
const ThemeAttr = "data-luna-darkreader"

// darkreaderBoot is grapesjs-darkreader's bootRuntime (runtime/VERSION
// says which), as an expression: (function(cfg, el){…}).
//
//go:embed runtime/darkreader-boot.js
var darkreaderBoot string

// ThemeRuntime is the site script that starts the theme with the page's
// settings: window.gjsTheme, the toggles ([data-gjs-theme-toggle]), the
// visitor's choice. Dark Reader itself is loaded from src, never from the
// page's settings (a developer without custom code cannot pick a script).
func ThemeRuntime(src string) string {
	s, _ := json.Marshal(src)
	return "(function(boot){var el=document.querySelector('script[" + ThemeAttr + "]');if(!el)return;" +
		"var c;try{c=JSON.parse(el.textContent||'')}catch(e){return}if(!c||typeof c!=='object')return;c.src=" + string(s) + ";boot(c);})(\n" +
		strings.TrimSpace(darkreaderBoot) + "\n);\n"
}

// themeNode cleans a theme settings element: a JSON object, written back
// compact (no "</script>" can survive json.Marshal), without "src".
func themeNode(n *html.Node) (*html.Node, bool) {
	if !strings.EqualFold(strings.TrimSpace(attr(n, "type")), "application/json") {
		return nil, false
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(b.String()), &cfg); err != nil || cfg == nil {
		return nil, false
	}
	delete(cfg, "src")
	out, err := json.Marshal(cfg)
	if err != nil || len(out) > 64<<10 {
		return nil, false
	}
	n.Attr = []html.Attribute{{Key: "type", Val: "application/json"}, {Key: ThemeAttr}}
	setChildren(n, []*html.Node{{Type: html.TextNode, Data: string(out)}})
	return n, true
}
