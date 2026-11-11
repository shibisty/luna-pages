// Package routes registers the application's routes.
package routes

import (
	"fullstack"
	"fullstack/env"
	"fullstack/web"
	"luna-keys"

	"luna-pages/app/http/controllers"
	"luna-pages/docs"
)

// API registers the public API (/v1, behind API keys from the dashboard in
// X-API-Key) and the dashboard's internal API (/internal/v1/apps/{app},
// behind the service token). Both run the same handlers.
func API(app *fullstack.App, v keys.Verifier, serviceToken string) {
	app.Router.Get("/healthz", func(c *web.Ctx) error {
		return c.JSON(200, map[string]string{"status": "ok", "app": app.Config.Name})
	}).Name("healthz")

	ui := env.Bool("API_DOCS", false)
	docs.Register(app.Router, docs.OpenAPI, "docs", "/openapi.yaml", "/docs", "Luna Pages API", ui)
	docs.Register(app.Router, docs.Internal, "docs.internal", "/internal/openapi.yaml", "/internal/docs", "Luna Pages internal API", ui)

	api := controllers.NewAPI(app)
	anyKey := keys.RequireKey(v)
	write := keys.RequireKey(v, keys.PagesWrite)

	app.Router.Group("/v1", func(r *web.Router) {
		r.Use(keys.CORS())
		r.Get("/ping", api.Ping, anyKey).Name("v1.ping")
		editor(r, api, write)
	})

	// The dashboard's editor, through the dashboard (which checks that the
	// developer owns the application). nginx does not route /internal/.
	app.Router.Group("/internal/v1/apps/{app}", func(r *web.Router) {
		editor(r, api, nil)
	}, web.Middleware(keys.Internal(serviceToken, keys.PagesRead, keys.PagesWrite)))
}

// editor is the editor contract v2 (grapesjs-pages, grapesjs-storage):
// the site, and layouts, templates and pages by client-made ids.
func editor(r *web.Router, api controllers.API, guard web.Middleware) {
	mw := []web.Middleware{}
	if guard != nil {
		mw = append(mw, guard)
	}
	r.Get("/editor/site", api.GetSite, mw...)
	r.Put("/editor/site", api.PutSite, mw...)
	r.Get("/editor/{kind}", api.List, mw...)
	r.Get("/editor/{kind}/{id}", api.Get, mw...)
	r.Put("/editor/{kind}/{id}", api.Put, mw...)
	r.Delete("/editor/{kind}/{id}", api.Delete, mw...)
}
