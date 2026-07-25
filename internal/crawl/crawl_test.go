package crawl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Masralai/web-gobbler/internal/scraper"
)

func TestMain(m *testing.M) {
	os.Setenv("SCRAPER_ALLOW_PRIVATE_IPS", "1")
	os.Exit(m.Run())
}

func TestCrawlBFS_DepthAndPages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><a href="/a">A</a><a href="https://other.com/x">X</a></body></html>`))
	})
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><h1>A</h1><a href="/b">B</a></body></html>`))
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><h1>B</h1></body></html>`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	res, err := CrawlBFS(context.Background(), ts.URL+"/", Options{
		MaxPages: 10,
		MaxDepth: 2,
		Extract:  []string{"markdown", "links"},
		Scrape:   *scraper.DefaultOptions(),
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.PagesCrawled < 2 {
		t.Fatalf("expected >=2 pages, got %d %#v", res.PagesCrawled, res.Pages)
	}
	for _, p := range res.Pages {
		if strings.Contains(p.URL, "other.com") {
			t.Fatalf("fetched off-origin: %s", p.URL)
		}
	}
}

func TestCrawlBFS_MaxPages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><a href="/a">A</a><a href="/b">B</a><a href="/c">C</a></body></html>`))
	})
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`<html><body>a</body></html>`)) })
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`<html><body>b</body></html>`)) })
	mux.HandleFunc("/c", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`<html><body>c</body></html>`)) })
	ts := httptest.NewServer(mux)
	defer ts.Close()

	res, err := CrawlBFS(context.Background(), ts.URL+"/", Options{
		MaxPages: 2,
		MaxDepth: 5,
		Extract:  []string{"links"},
		Scrape:   *scraper.DefaultOptions(),
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.PagesCrawled != 2 {
		t.Fatalf("expected 2 pages, got %d", res.PagesCrawled)
	}
}

func TestMapBFS(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><a href="/a">A</a></body></html>`))
	})
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><a href="/b">B</a></body></html>`))
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body>end</body></html>`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	res, err := MapBFS(context.Background(), ts.URL+"/", Options{
		MaxURLs:  10,
		MaxDepth: 2,
		Scrape:   *scraper.DefaultOptions(),
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.URLs) < 2 {
		t.Fatalf("expected urls, got %v", res.URLs)
	}
}

func TestNormalizeURL(t *testing.T) {
	got, err := NormalizeURL("https://ex.com/a#frag")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "#") {
		t.Fatalf("fragment not stripped: %s", got)
	}
}
