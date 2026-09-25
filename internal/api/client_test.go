package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRejectUnsafeOrigins(t *testing.T) {
	for _, origin := range []string{"http://runivo.example", "https://user:pass@example.com", "https://example.com/api", "https://example.com?token=x", "file:///tmp/api"} {
		if _, e := New(origin, "secret", "test"); e == nil {
			t.Errorf("accepted %q", origin)
		}
	}
	for _, origin := range []string{"https://example.com", "http://127.0.0.1:8000", "http://[::1]:8000"} {
		if _, e := New(origin, "", "test"); e != nil {
			t.Fatal(e)
		}
	}
}
func TestRedirectNeverReceivesBearer(t *testing.T) {
	called := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 302) }))
	defer origin.Close()
	client, _ := New(origin.URL, "rnv_secret", "test")
	e := client.Do(context.Background(), "GET", "/api/v1/catalog", nil, nil, "")
	if e == nil || called {
		t.Fatal("followed credential-bearing redirect")
	}
}
func TestHeadersBodyAndAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer rnv_test" || r.Header.Get("Idempotency-Key") != "request1" {
			t.Error("missing headers")
		}
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"errors":[{"message":"Upgrade required","code":"plan"}]}`))
	}))
	defer server.Close()
	client, _ := New(server.URL, "rnv_test", "test")
	e := client.Do(context.Background(), "POST", "/api/v1/catalog", map[string]any{}, nil, "request1")
	var ae *Error
	if !errors.As(e, &ae) || ae.Status != 403 || ae.Code != "plan" || ae.Message != "Upgrade required" {
		t.Fatalf("wrong error: %v", e)
	}
}
func TestBoundedResponsesAndInvalidJSON(t *testing.T) {
	for _, payload := range []string{"<html>error</html>", strings.Repeat("a", MaxResponse+1)} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(payload)) }))
		client, _ := New(server.URL, "", "test")
		var result any
		if e := client.Do(context.Background(), "GET", "/api/v1/catalog", nil, &result, ""); e == nil {
			t.Fatal("accepted invalid/oversized response")
		}
		server.Close()
	}
}
func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client, _ := New("https://example.test", "", "test")
	if e := client.Do(ctx, "GET", "/api/v1/catalog", nil, nil, ""); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
