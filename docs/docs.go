// Package docs is the API description: the OpenAPI documents of the public
// API (served at /openapi.yaml, Swagger UI at /docs) and of the dashboard's
// internal API (/internal/openapi.yaml, /internal/docs; nginx never routes
// /internal/). Swagger UI only when API_DOCS=true.
package docs

import (
	_ "embed"
	"html/template"
	"net/http"

	"fullstack/web"
)

// OpenAPI is the OpenAPI 3.1 document of the API.
//
//go:embed openapi.yaml
var OpenAPI []byte

// Internal is the OpenAPI 3.1 document of the internal API.
//
//go:embed internal.yaml
var Internal []byte

// Register serves doc at specPath and, when ui is true, Swagger UI at
// uiPath; name prefixes the route names. Swagger UI's scripts come from the
// jsDelivr CDN, so the page needs the internet; the document does not.
func Register(r *web.Router, doc []byte, name, specPath, uiPath, title string, ui bool) {
	r.Get(specPath, func(c *web.Ctx) error {
		h := c.W.Header()
		h.Set("Content-Type", "application/yaml; charset=utf-8")
		h.Set("Cache-Control", "no-cache")
		h.Set("Access-Control-Allow-Origin", "*") // tools like editor.swagger.io may read it
		_, err := c.W.Write(doc)
		return err
	}).Name(name + ".openapi")
	if !ui {
		return
	}
	r.Get(uiPath, func(c *web.Ctx) error {
		c.W.Header().Set("Content-Type", "text/html; charset=utf-8")
		c.W.WriteHeader(http.StatusOK)
		return page.Execute(c.W, map[string]string{"Title": title, "Spec": specPath})
	}).Name(name + ".ui")
}

var page = template.Must(template.New("docs").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}}</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js" crossorigin></script>
  <script>
    window.ui = SwaggerUIBundle({ url: {{.Spec}}, dom_id: "#swagger-ui", persistAuthorization: true });
  </script>
</body>
</html>
`))
