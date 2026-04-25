package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/MemaxLabs/memax-go-agent-sdk/web"
)

func TestHTTPFetcherFetchesBoundedContentAndTitle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "memax-code/0" {
			t.Fatalf("User-Agent = %q, want memax-code/0", got)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><head><title> Example Page </title></head><body>" + strings.Repeat("x", 128) + "</body></html>"))
	}))
	defer server.Close()

	fetcher := NewHTTPFetcher(HTTPFetcherConfig{
		MaxBytes:            64,
		AllowPrivateNetwork: true,
	})
	result, err := fetcher.FetchURL(context.Background(), web.FetchRequest{URL: server.URL, MaxBytes: 1024})
	if err != nil {
		t.Fatalf("FetchURL() error = %v", err)
	}
	if result.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", result.StatusCode)
	}
	if result.Title != "Example Page" {
		t.Fatalf("Title = %q, want Example Page", result.Title)
	}
	if len(result.Content) != 64 {
		t.Fatalf("Content length = %d, want 64", len(result.Content))
	}
	if result.Metadata["truncated"] != true {
		t.Fatalf("Metadata = %#v, want truncated=true", result.Metadata)
	}
	if result.FetchedAt.IsZero() {
		t.Fatal("FetchedAt is zero")
	}
}

func TestHTTPFetcherStripsURLUserInfo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	parsed.User = url.UserPassword("user", "secret")

	fetcher := NewHTTPFetcher(HTTPFetcherConfig{AllowPrivateNetwork: true})
	result, err := fetcher.FetchURL(context.Background(), web.FetchRequest{URL: parsed.String()})
	if err != nil {
		t.Fatalf("FetchURL() error = %v", err)
	}
	for _, value := range []string{result.URL, result.FinalURL} {
		if strings.Contains(value, "user") || strings.Contains(value, "secret") {
			t.Fatalf("fetched URL leaked credentials: %#v", result)
		}
	}
}

func TestHTTPFetcherStripsRedirectURLUserInfo(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization = %q, want empty", got)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer target.Close()

	targetURL, err := url.Parse(target.URL)
	if err != nil {
		t.Fatalf("parse target URL: %v", err)
	}
	targetURL.User = url.UserPassword("redirect-user", "redirect-secret")
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetURL.String(), http.StatusFound)
	}))
	defer redirector.Close()

	fetcher := NewHTTPFetcher(HTTPFetcherConfig{AllowPrivateNetwork: true})
	result, err := fetcher.FetchURL(context.Background(), web.FetchRequest{URL: redirector.URL})
	if err != nil {
		t.Fatalf("FetchURL() error = %v", err)
	}
	if strings.Contains(result.FinalURL, "redirect-user") || strings.Contains(result.FinalURL, "redirect-secret") {
		t.Fatalf("FinalURL leaked redirect credentials: %#v", result)
	}
}

func TestHTTPFetcherRedirectPolicy(t *testing.T) {
	t.Run("caps redirects", func(t *testing.T) {
		var server *httptest.Server
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, server.URL, http.StatusFound)
		}))
		defer server.Close()

		fetcher := NewHTTPFetcher(HTTPFetcherConfig{AllowPrivateNetwork: true, MaxRedirects: 2})
		_, err := fetcher.FetchURL(context.Background(), web.FetchRequest{URL: server.URL})
		if err == nil || !strings.Contains(err.Error(), "stopped after 2 redirects") {
			t.Fatalf("FetchURL() error = %v, want redirect cap", err)
		}
	})

	t.Run("rejects non-http redirects", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
		}))
		defer server.Close()

		fetcher := NewHTTPFetcher(HTTPFetcherConfig{AllowPrivateNetwork: true})
		_, err := fetcher.FetchURL(context.Background(), web.FetchRequest{URL: server.URL})
		if err == nil || !strings.Contains(err.Error(), "scheme must be http or https") {
			t.Fatalf("FetchURL() error = %v, want redirect scheme rejection", err)
		}
	})
}

func TestHTTPFetcherRejectsPrivateNetworkByDefault(t *testing.T) {
	fetcher := NewHTTPFetcher(HTTPFetcherConfig{})
	_, err := fetcher.FetchURL(context.Background(), web.FetchRequest{URL: "http://127.0.0.1:8080/"})
	if err == nil || !strings.Contains(err.Error(), "blocked private address") {
		t.Fatalf("FetchURL() error = %v, want private address rejection", err)
	}

	_, err = fetcher.FetchURL(context.Background(), web.FetchRequest{URL: "http://localhost:8080/"})
	if err == nil || !strings.Contains(err.Error(), "blocked private address") {
		t.Fatalf("FetchURL(localhost) error = %v, want private host rejection", err)
	}
}

func TestHTTPFetcherRejectsNonHTTPURLs(t *testing.T) {
	fetcher := NewHTTPFetcher(HTTPFetcherConfig{})
	_, err := fetcher.FetchURL(context.Background(), web.FetchRequest{URL: "file:///etc/passwd"})
	if err == nil || !strings.Contains(err.Error(), "scheme must be http or https") {
		t.Fatalf("FetchURL() error = %v, want scheme rejection", err)
	}
}
