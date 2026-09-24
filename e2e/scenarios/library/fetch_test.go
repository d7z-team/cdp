package library

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gopkg.d7z.net/cdp"
)

func TestBrowserFetchMethods(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	session := acquireSession(t)
	page := must(session.Open("/home"))
	for _, test := range []struct {
		method, path, body, key string
		want                    any
	}{
		{"GET", "/api/hello", "", "message", "hello"},
		{"POST", "/api/echo", `{"key":"value"}`, "key", "value"},
		{"PUT", "/api/data", `{"name":"updated"}`, "updated", true},
		{"DELETE", "/api/data", "", "updated", true},
	} {
		response := must(page.Fetch(context.Background(), cdp.FetchRequest{URL: session.FixtureURL(test.path), Method: test.method, Body: []byte(test.body), Headers: http.Header{"Content-Type": {"application/json"}, "X-Custom": {"test-value"}}}))
		var body map[string]any
		mustOK(response.JSON(&body))
		if response.Status != 200 || response.URL == "" || response.StatusText == "" || response.Headers.Get("Content-Type") == "" || body[test.key] != test.want {
			t.Fatalf("%s: %+v body=%v", test.method, response, body)
		}
	}
	for _, body := range [][]byte{[]byte("raw-string-body"), {0, 1, 255}} {
		response := must(page.Fetch(context.Background(), cdp.FetchRequest{URL: session.FixtureURL("/api/echo"), Method: "POST", Body: body}))
		if response.Status != 200 {
			t.Fatalf("binary POST: %+v", response)
		}
	}
}

func TestFetchCancellationAbortsBrowserRequest(t *testing.T) {
	if !browserEnabled {
		t.Skip("set CDP_E2E_BROWSER=1")
	}
	started, aborted := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		if r.URL.Path == "/slow" {
			close(started)
			<-r.Context().Done()
			close(aborted)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer server.Close()
	page := openFixture(t, "/home")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := page.Fetch(ctx, cdp.FetchRequest{URL: server.URL + "/slow"}); result <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request not started")
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	select {
	case <-aborted:
	case <-time.After(5 * time.Second):
		t.Fatal("browser fetch was not aborted")
	}
	if evalValue(page, "return 1+1") != "2" {
		t.Fatal("connection lost after cancellation")
	}
}
