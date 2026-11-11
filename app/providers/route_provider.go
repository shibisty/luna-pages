// Package providers holds the application's service providers.
package providers

import (
	"fullstack"
	"fullstack/web"
	"luna-keys"

	"luna-pages/routes"
)

// RouteProvider registers the routes.
type RouteProvider struct{}

// Register implements fullstack.Provider.
func (RouteProvider) Register(*fullstack.App) error { return nil }

// Boot registers the API.
func (RouteProvider) Boot(app *fullstack.App) error {
	v, err := fullstack.Make[keys.Verifier](app)
	if err != nil {
		return err
	}
	cfg, err := fullstack.Make[KeysConfig](app)
	if err != nil {
		return err
	}
	app.Router.Use(web.SecureHeaders(false))
	routes.API(app, v, cfg.Token)
	return nil
}
