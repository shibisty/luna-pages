// Command luna-pages is the application: with no arguments it serves HTTP,
// otherwise it runs a console command (go run . list); `healthcheck` probes
// the running server's /healthz (for Docker and Kubernetes).
package main

import (
	"context"
	"log"
	"os"

	"luna-pages/bootstrap"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	app, err := bootstrap.NewApp()
	if err != nil {
		log.Fatal(err)
	}
	if code, ok := app.Console(os.Args[1:]); ok {
		os.Exit(code)
	}
	if err := app.Run(context.Background()); err != nil {
		app.Log.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
