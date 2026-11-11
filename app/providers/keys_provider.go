package providers

import (
	"fmt"
	"time"

	"fullstack"
	"fullstack/env"
	"luna-keys"
)

// KeysConfig is how luna-pages reaches the dashboard to check API keys.
type KeysConfig struct {
	DashboardURL string        `env:"LUNA_DASHBOARD_URL,required"` // internal address, e.g. http://luna-dashboard:8080
	Token        string        `env:"LUNA_DASHBOARD_TOKEN,required"`
	ValidTTL     time.Duration `env:"LUNA_KEYS_CACHE_TTL" default:"60s"`
}

// KeysProvider puts a keys.Verifier (the dashboard's introspection client)
// into the container.
type KeysProvider struct{}

// Register implements fullstack.Provider.
func (KeysProvider) Register(app *fullstack.App) error {
	var cfg KeysConfig
	if err := env.Decode(&cfg); err != nil {
		return fmt.Errorf("keys: %w", err)
	}
	client := keys.NewClient(cfg.DashboardURL, cfg.Token)
	client.ValidTTL = cfg.ValidTTL
	fullstack.Set[keys.Verifier](app, client)
	fullstack.Set(app, cfg) // the same token guards the dashboard's calls to /internal/
	return nil
}
