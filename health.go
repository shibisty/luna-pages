package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// healthcheck asks this service's own /healthz and returns the exit code
// for `app healthcheck`. The runtime image has no shell or curl, so Docker
// and Kubernetes probes run the binary itself. It reads HTTP_ADDR from the
// environment only, so it never needs the rest of the configuration.
func healthcheck() int {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck: HTTP_ADDR:", err)
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := http.Client{Timeout: 3 * time.Second}
	res, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck:", res.Status)
		return 1
	}
	return 0
}
