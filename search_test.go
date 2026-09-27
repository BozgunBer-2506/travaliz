package main

import (
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"travel-proxy-service/internal/handlers"
	"travel-proxy-service/internal/proxy"
)

type searchTransport func(*http.Request) (*http.Response, error)

func (f searchTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSearchPages(t *testing.T) {
	pc := proxy.NewProxyClient("private-test-key")
	calls := 0
	status := 200
	pc.HTTPClient.Transport = searchTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "serpapi.com" {
			t.Fatal("legacy provider called")
		}
		body := `{"search_metadata":{"status":"Success"},"properties":[{"name":"Test Real Hotel","link":"https://example.com/hotel","rate_per_night":{"extracted_lowest":90},"total_rate":{"extracted_lowest":180}}]}`
		if r.URL.Query().Get("engine") == "google_flights" {
			body = `{"search_metadata":{"status":"Success","google_flights_url":"https://www.google.com/travel/flights"},"best_flights":[{"price":120,"total_duration":120,"flights":[{"departure_airport":{"id":"BER","time":"2030-01-01 08:00"},"arrival_airport":{"id":"LHR","time":"2030-01-01 09:00"},"airline":"Test Airline"}]}]}`
		}
		if status == 429 {
			body = `{"error":"private-test-key quota exceeded"}`
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	tmpl := template.Must(template.ParseFS(templateFiles, "templates/*.html"))
	mux := newMux(&handlers.TravelHandler{Templates: tmpl, ProxyClient: pc})
	get := func(method, path string) string {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		if w.Code != 200 {
			t.Fatalf("%s returned %d", path, w.Code)
		}
		body := w.Body.String()
		if strings.Contains(body, "private-test-key") || strings.Contains(body, "ZgotmplZ") {
			t.Fatal("credential leaked or unsafe template URL")
		}
		return body
	}
	for _, path := range []string{"/", "/flights", "/cars", "/cars?pickup=Berlin", "/suggest?q=Ber", "/suggest-flight?q=Ber", "/travel-data"} {
		body := get("GET", path)
		if strings.HasPrefix(path, "/cars") && (!strings.Contains(body, "Car rental search is not available yet") || strings.Contains(body, "Our trusted rental partners")) {
			t.Fatal("cars page misleading")
		}
	}
	if calls != 0 {
		t.Fatalf("landing/suggestions consumed %d requests", calls)
	}
	date := time.Now().AddDate(0, 0, 30)
	start := date.Format("2006-01-02")
	end := date.AddDate(0, 0, 2).Format("2006-01-02")
	path := "/?q=Berlin&checkin=" + start + "&checkout=" + end
	hotel := get("GET", path)
	if calls != 1 || !strings.Contains(hotel, `href="https://example.com/hotel"`) || !strings.Contains(hotel, "Test Real Hotel") || strings.Contains(hotel, `onclick="openHotelDetail(this)"`) {
		t.Fatal("hotel search not wired correctly")
	}
	flight := get("GET", "/flights?fromSky=BER&toSky=LHR&date="+start)
	if calls != 2 || !strings.Contains(flight, "Test Airline") || !strings.Contains(flight, "View on Google Flights") || strings.Contains(flight, `class="flight-book-btn`) {
		t.Fatal("flight search not wired correctly")
	}
	multi := get("GET", "/flights?tripType=multi&leg0from=BER&leg0to=LHR&leg0date="+start+"&leg1from=LHR&leg1to=JFK&leg1date="+end)
	if calls != 3 || !strings.Contains(multi, "Multi-city itinerary") {
		t.Fatal("multi-city issued multiple searches")
	}
	get("HEAD", path)
	if calls != 3 {
		t.Fatal("HEAD consumed quota")
	}
	get("GET", path+"&rooms=2")
	if calls != 3 {
		t.Fatal("invalid occupancy consumed quota")
	}
	status = 429
	outage := get("GET", path)
	if calls != 4 || !strings.Contains(outage, "search limit has been reached") || strings.Contains(outage, "Test Real Hotel") || strings.Contains(outage, "Grand Palace Hotel") {
		t.Fatal("provider error hidden or mock results shown")
	}
}
