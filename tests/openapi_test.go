package tests

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	apptest "fullstack-testing"

	"luna-pages/docs"
)

// specOperations reads "METHOD /path" of every operation in the OpenAPI
// document (paths at two spaces, methods at four).
func specOperations(t *testing.T, spec string) map[string]bool {
	t.Helper()
	pathRe := regexp.MustCompile(`^  (/\S*):\s*$`)
	methodRe := regexp.MustCompile(`^    (get|put|post|patch|delete):\s*$`)
	ops := map[string]bool{}
	inPaths, current := false, ""
	for _, line := range strings.Split(spec, "\n") {
		switch {
		case line == "paths:":
			inPaths = true
		case inPaths && line != "" && !strings.HasPrefix(line, " "):
			inPaths = false
		case inPaths:
			if m := pathRe.FindStringSubmatch(line); m != nil {
				current = m[1]
			} else if m := methodRe.FindStringSubmatch(line); m != nil && current != "" {
				ops[strings.ToUpper(m[1])+" "+current] = true
			}
		}
	}
	if len(ops) == 0 {
		t.Fatal("no operations found in the OpenAPI document")
	}
	return ops
}

// TestOpenAPIMatchesRoutes keeps the OpenAPI documents and the routes in
// step: every route is documented, the /internal/ ones in docs/internal.yaml,
// the rest in docs/openapi.yaml, and every documented operation exists.
func TestOpenAPIMatchesRoutes(t *testing.T) {
	app := newApp(t, fakeDashboard(t, nil))
	if _, err := app.Handler(); err != nil {
		t.Fatal(err)
	}
	public, internal := map[string]bool{}, map[string]bool{}
	for _, r := range app.Router.Routes() {
		switch r.Path {
		case "/openapi.yaml", "/docs", "/internal/openapi.yaml", "/internal/docs":
			continue
		}
		if r.Method == "HEAD" {
			continue
		}
		if strings.HasPrefix(r.Path, "/internal/") {
			internal[r.Method+" "+r.Path] = true
		} else {
			public[r.Method+" "+r.Path] = true
		}
	}
	compare(t, "docs/openapi.yaml", specOperations(t, string(docs.OpenAPI)), public)
	compare(t, "docs/internal.yaml", specOperations(t, string(docs.Internal)), internal)
}

func compare(t *testing.T, name string, documented, routes map[string]bool) {
	t.Helper()
	var missing, extra []string
	for op := range routes {
		if !documented[op] {
			missing = append(missing, op)
		}
	}
	for op := range documented {
		if !routes[op] {
			extra = append(extra, op)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Errorf("%s is out of step with the routes:\n  routes not documented: %v\n  documented, no route: %v", name, missing, extra)
	}
}

func TestDocsServed(t *testing.T) {
	c := apptest.New(t, newApp(t, fakeDashboard(t, nil)))
	c.Get("/openapi.yaml").AssertOK().AssertHeader("Content-Type", "application/yaml; charset=utf-8").AssertSee("openapi: 3.1.0", "/v1/editor/{kind}/{id}")
	c.Get("/docs").AssertNotFound() // API_DOCS is off

	t.Setenv("API_DOCS", "true")
	c.Get("/internal/openapi.yaml").AssertOK().AssertSee("openapi: 3.1.0", "/internal/v1/apps/{app}/editor/{kind}")
	c = apptest.New(t, newApp(t, fakeDashboard(t, nil)))
	c.Get("/docs").AssertOK().AssertSee("swagger-ui", "/openapi.yaml")
	c.Get("/internal/docs").AssertOK().AssertSee("swagger-ui", "/internal/openapi.yaml")
}
