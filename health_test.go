package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthcheck(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	t.Setenv("HTTP_ADDR", ":"+port)
	if code := healthcheck(); code != 0 {
		t.Fatalf("healthy: exit %d", code)
	}
	status = http.StatusServiceUnavailable
	if code := healthcheck(); code != 1 {
		t.Fatalf("unhealthy: exit %d", code)
	}
	t.Setenv("HTTP_ADDR", "bad")
	if code := healthcheck(); code != 1 {
		t.Fatalf("bad addr: exit %d", code)
	}
}
