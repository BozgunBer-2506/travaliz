package main

import (
	"encoding/xml"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"travel-proxy-service/internal/handlers"
	"travel-proxy-service/internal/proxy"
)

func TestPublicSEO(t *testing.T) {
	tmpl := template.Must(template.ParseFS(templateFiles, "templates/*.html"))
	server := httptest.NewServer(newMux(&handlers.TravelHandler{
		Templates: tmpl, ProxyClient: proxy.NewProxyClient(""),
	}))
	defer server.Close()

	get := func(path, contentType string) string {
		t.Helper()
		resp, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), contentType) {
			t.Fatalf("%s: status %d, content type %q", path, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		if strings.Contains(strings.ToLower(resp.Header.Get("X-Robots-Tag")), "noindex") {
			t.Fatalf("%s has a noindex header", path)
		}
		return string(body)
	}

	var sitemap struct {
		XMLName xml.Name `xml:"http://www.sitemaps.org/schemas/sitemap/0.9 urlset"`
		URLs    []struct {
			Location     string `xml:"loc"`
			LastModified string `xml:"lastmod"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal([]byte(get("/sitemap.xml", "application/xml")), &sitemap); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"https://travaliz.com/":        "/",
		"https://travaliz.com/flights": "/flights",
		"https://travaliz.com/cars":    "/cars",
	}
	if len(sitemap.URLs) != len(want) {
		t.Fatalf("expected %d sitemap URLs, got %d", len(want), len(sitemap.URLs))
	}
	for _, entry := range sitemap.URLs {
		path, ok := want[entry.Location]
		if !ok {
			t.Fatalf("unexpected or duplicate URL: %s", entry.Location)
		}
		delete(want, entry.Location)
		if entry.LastModified != "" {
			t.Fatal("lastmod requires a trustworthy update date")
		}
		body := get(path, "text/html")
		if strings.Contains(strings.ToLower(body), "noindex") || !strings.Contains(body, `rel="canonical" href="`+entry.Location+`"`) {
			t.Fatalf("%s: missing canonical or unexpected noindex", path)
		}
	}
	robots := get("/robots.txt", "text/plain")
	if !strings.Contains(robots, "User-agent: *\nAllow: /\n") || !strings.Contains(robots, "Sitemap: https://travaliz.com/sitemap.xml\n") {
		t.Fatalf("unexpected robots.txt: %s", robots)
	}
}
