// Package tests holds the application's HTTP tests. The dashboard is faked
// with luna-keys' own introspection handler. Tests that touch the database
// need TEST_DATABASE_DSN (Postgres; each test rolls back its transaction)
// and are skipped without it.
package tests

import (
	"context"
	"io"
	"net/http/httptest"
	"os"
	"testing"

	"fullstack"
	database "fullstack-orm"
	apptest "fullstack-testing"
	"luna-keys"

	"luna-pages/bootstrap"
)

const (
	serviceToken = "pages-service-token"
	shopID       = "11111111-1111-4111-8111-111111111111"
	blogID       = "22222222-2222-4222-8222-222222222222"
)

// fakeDashboard knows the keys it is given and nothing else.
func fakeDashboard(t *testing.T, known map[string]*keys.Key) string {
	srv := httptest.NewServer(keys.Handler(func(_ context.Context, hash string) (*keys.Key, error) {
		return known[hash], nil
	}, serviceToken))
	t.Cleanup(srv.Close)
	return srv.URL
}

func env(t *testing.T, dashboardURL string, extra map[string]string) {
	t.Helper()
	vars := map[string]string{
		"APP_NAME": "luna-pages", "APP_ENV": "testing",
		"DB_DRIVER": "postgres", "DATABASE_DSN": "postgres://unused@127.0.0.1:1/none", "DB_PING": "false", "DB_MIGRATE": "false",
		"LUNA_DASHBOARD_URL": dashboardURL, "LUNA_DASHBOARD_TOKEN": serviceToken,
	}
	for k, v := range extra {
		vars[k] = v
	}
	for k, v := range vars {
		t.Setenv(k, v)
	}
}

// newApp is the application without a database.
func newApp(t *testing.T, dashboardURL string) *fullstack.App {
	t.Helper()
	env(t, dashboardURL, nil)
	app, err := bootstrap.NewApp(fullstack.Options{EnvFiles: []string{}, LogOutput: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

// newDBApp is the application on TEST_DATABASE_DSN, migrated, inside a
// transaction.
func newDBApp(t *testing.T, dashboardURL string) *fullstack.App {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN is not set (a Postgres database for the tests)")
	}
	env(t, dashboardURL, map[string]string{"DATABASE_DSN": dsn, "DB_PING": "true", "DB_MIGRATE": "true"})
	app, err := bootstrap.NewApp(fullstack.Options{EnvFiles: []string{}, LogOutput: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.Handler(); err != nil { // boot: migrations run before the transaction starts
		t.Fatal(err)
	}
	database.UseTransaction(t, app)
	return app
}

// world is the shop with a writer key, a server reader, a browser reader,
// and the blog with a writer of its own.
type world struct {
	c                                        *apptest.Client
	writer, reader, browser, blog, authOnly string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{}
	var wh, rh, bh, blh, ah string
	w.writer, wh, _ = keys.Generate(keys.Server)
	w.reader, rh, _ = keys.Generate(keys.Server)
	w.browser, bh, _ = keys.Generate(keys.Browser)
	w.blog, blh, _ = keys.Generate(keys.Server)
	w.authOnly, ah, _ = keys.Generate(keys.Server)
	url := fakeDashboard(t, map[string]*keys.Key{
		wh:  {ID: "k1", AppID: shopID, AppName: "Shop", Kind: keys.Server, Scopes: []keys.Scope{keys.PagesRead, keys.PagesWrite}},
		rh:  {ID: "k2", AppID: shopID, Kind: keys.Server, Scopes: []keys.Scope{keys.PagesRead}},
		bh:  {ID: "k3", AppID: shopID, Kind: keys.Browser, Scopes: []keys.Scope{keys.PagesRead}, Origins: []string{"https://shop.example"}},
		blh: {ID: "k4", AppID: blogID, Kind: keys.Server, Scopes: []keys.Scope{keys.PagesWrite}},
		ah:  {ID: "k5", AppID: shopID, Kind: keys.Server, Scopes: []keys.Scope{keys.AuthLogin}},
	})
	w.c = apptest.New(t, newDBApp(t, url))
	return w
}

func (w *world) as(key string) *apptest.Client { return w.c.WithHeader(keys.Header, key) }

func TestHealth(t *testing.T) {
	apptest.New(t, newApp(t, fakeDashboard(t, nil))).GetJSON("/healthz").AssertOK().AssertJSONPath("status", "ok")
}
