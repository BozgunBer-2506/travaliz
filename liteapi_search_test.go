package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"travel-proxy-service/internal/handlers"
	"travel-proxy-service/internal/liteapi"
)

func TestLiteAPIHotelSearch(t *testing.T) {
	t.Setenv("LITEAPI_API_KEY", "sandbox-test")
	client := liteapi.NewClient()
	calls, ratesStatus := 0, 200
	expectedCoords := [2]string{"52.52", "13.405"}
	ratesBody := `{"data":[{"hotelId":"hotel","roomTypes":[{"offerId":"offer","rates":[{"rateId":"rate"}],"offerRetailRate":{"amount":100.01,"currency":"GBP"}}]}]}`
	client.HTTPClient.Transport = searchTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		body, status := `{"data":[{"id":"hotel","name":"API Hotel","stars":4.5}]}`, 200
		if r.URL.Host != "api.liteapi.travel" || r.Header.Get("X-API-Key") != "sandbox-test" {
			t.Fatal("wrong provider or authentication")
		}
		switch r.URL.Path {
		case "/v3.0/data/hotels":
			if r.Method != "GET" || r.URL.Query().Get("latitude") != expectedCoords[0] || r.URL.Query().Get("longitude") != expectedCoords[1] {
				t.Fatal("wrong hotel method or city coordinates")
			}
		case "/v3.0/hotels/rates":
			var query struct {
				liteapi.RateQuery
				HotelIDs []string `json:"hotelIds"`
			}
			if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
				t.Fatal(err)
			}
			if r.Method != "POST" || query.Currency != "EUR" || query.GuestNationality != "TR" ||
				query.Checkin != "2099-11-10" || query.Checkout != "2099-11-13" ||
				!reflect.DeepEqual(query.HotelIDs, []string{"hotel"}) ||
				!reflect.DeepEqual(query.Occupancies, []liteapi.Occupancy{{Adults: 2, Children: []int{0, 17}}}) {
				t.Fatalf("incorrect rate request: %+v", query)
			}
			body, status = ratesBody, ratesStatus
		default:
			t.Fatalf("unexpected supplier operation: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	tmpl := template.Must(template.ParseFS(templateFiles, "templates/*.html"))
	mux := newMux(&handlers.TravelHandler{Templates: tmpl, ExternalSearch: true, HotelClient: client})
	path := "/?q=Berlin&checkin=2099-11-10&checkout=2099-11-13&adults=2&children=2&children_ages=0,17&rooms=1&guestNationality=tr&currency=EUR"
	get := func(path string) string {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("HTTP %d", w.Code)
		}
		return w.Body.String()
	}
	body := get(path)
	for _, want := range []string{`data-stars="4.5"`, `data-price="33.34"`, `data-total="100.01"`, `data-currency="GBP"`, `GBP 33.34`, `GBP 100.01`, `/night average`, `value="TR"`, `</html>`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	if calls != 2 || strings.Contains(body, `data-usd="33.34"`) {
		t.Fatal("price converted or extra calls made")
	}
	for _, invalid := range []string{
		strings.Replace(path, "rooms=1", "rooms=2", 1),
		strings.Replace(path, "adults=2", "adults=0", 1),
		strings.Replace(path, "children=2", "children=1", 1),
		strings.Replace(path, "children_ages=0,17", "children_ages=,17", 1),
		strings.Replace(path, "children_ages=0,17", "children_ages=18,17", 1),
		strings.Replace(path, "guestNationality=tr", "guestNationality=ZZ", 1),
		strings.Replace(path, "guestNationality=tr", "guestNationality=", 1),
		strings.Replace(path, "currency=EUR", "currency=ZZZ", 1),
		strings.Replace(path, "q=Berlin", "q=Ber", 1),
		strings.Replace(path, "q=Berlin", "q=Unknown+Town", 1),
		path + "&entityId=Paris",
	} {
		if got := get(invalid); strings.Contains(got, "API Hotel") || !strings.Contains(got, `text-red-700`) || strings.Contains(got, "properties found") {
			t.Fatal("invalid search did not show an error or was displayed as an empty result")
		}
		if calls != 2 {
			t.Fatal("invalid guests reached the supplier")
		}
	}
	for _, tc := range []struct {
		name, city string
		coords     [2]string
	}{
		{"  bErLiN  ", "Berlin", [2]string{"52.52", "13.405"}},
		{"İstanbul", "Istanbul", [2]string{"41.0082", "28.9784"}},
		{"Londra", "London", [2]string{"51.5074", "-0.1278"}},
		{"Roma", "Rome", [2]string{"41.9028", "12.4964"}},
	} {
		expectedCoords = tc.coords
		before := calls
		body := get(strings.Replace(path, "q=Berlin", "q="+url.QueryEscape(tc.name), 1))
		if calls != before+2 || !strings.Contains(body, "API Hotel") || !strings.Contains(body, `id="city-entity" name="entityId" type="hidden" value="`+tc.city+`"`) {
			t.Fatalf("typed city %q was not safely resolved to %s", tc.name, tc.city)
		}
	}
	expectedCoords = [2]string{"52.52", "13.405"}
	ratesStatus = 429
	if got := get(path); !strings.Contains(got, "Hotel prices could not be loaded") || strings.Contains(got, "API Hotel") {
		t.Fatal("supplier failure hidden or fabricated results")
	}
	ratesStatus, ratesBody = 200, `{"data":[]}`
	if got := get(path); strings.Contains(got, "API Hotel") || !strings.Contains(got, "otel bulunamadı") {
		t.Fatal("empty rates fabricated")
	}
}

func TestLiteAPIHotelCurrencyChanges(t *testing.T) {
	t.Setenv("LITEAPI_API_KEY", "sandbox-test")
	client := liteapi.NewClient()
	prices := map[string]float64{"TRY": 14339.01, "USD": 448.09, "EUR": 412.24, "GBP": 354.00}
	var requestedCurrencies []string
	client.HTTPClient.Transport = searchTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"data":[{"id":"hotel","name":"Currency Hotel","stars":4}]}`
		if r.URL.Path == "/v3.0/hotels/rates" {
			var query liteapi.RateQuery
			if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
				t.Fatal(err)
			}
			requestedCurrencies = append(requestedCurrencies, query.Currency)
			body = fmt.Sprintf(`{"data":[{"hotelId":"hotel","roomTypes":[{"offerId":"offer-%s","rates":[{"rateId":"rate"}],"offerRetailRate":{"amount":%.2f,"currency":%q}}]}]}`,
				query.Currency, prices[query.Currency], query.Currency)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	tmpl := template.Must(template.ParseFS(templateFiles, "templates/*.html"))
	mux := newMux(&handlers.TravelHandler{Templates: tmpl, ExternalSearch: true, HotelClient: client})
	wantCurrencies := []string{"TRY", "USD", "EUR", "GBP", "TRY", "USD"}
	for _, currency := range wantCurrencies {
		w := httptest.NewRecorder()
		path := "/?q=Dubai&checkin=2099-11-10&checkout=2099-11-14&guestNationality=TR&currency=" + currency
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		body := w.Body.String()
		for _, want := range []string{
			`data-currency="` + currency + `"`,
			fmt.Sprintf(`data-total="%.2f"`, prices[currency]),
			fmt.Sprintf(`data-price="%.2f"`, prices[currency]/4),
			`value="` + currency + `" selected`,
		} {
			if w.Code != 200 || !strings.Contains(body, want) {
				t.Errorf("currency %s: status %d, missing %s", currency, w.Code, want)
			}
		}
	}
	if !reflect.DeepEqual(requestedCurrencies, wantCurrencies) {
		t.Fatalf("provider currencies = %v, want %v", requestedCurrencies, wantCurrencies)
	}
}

// A category search (no city) renders through a different template branch
// gated on CategoryTitle; results must not be silently hidden there.
func TestLiteAPIHotelCategorySearchRendersResults(t *testing.T) {
	t.Setenv("LITEAPI_API_KEY", "sandbox-test")
	client := liteapi.NewClient()
	client.HTTPClient.Transport = searchTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"data":[{"id":"hotel","name":"Category Hotel","stars":4.5}]}`
		if r.URL.Path == "/v3.0/hotels/rates" {
			body = `{"data":[{"hotelId":"hotel","roomTypes":[{"offerId":"offer","rates":[{"rateId":"rate"}],"offerRetailRate":{"amount":100.01,"currency":"EUR"}}]}]}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	tmpl := template.Must(template.ParseFS(templateFiles, "templates/*.html"))
	mux := newMux(&handlers.TravelHandler{Templates: tmpl, ExternalSearch: true, HotelClient: client})
	w := httptest.NewRecorder()
	path := "/?category=beach&checkin=2099-11-10&checkout=2099-11-13&guestNationality=TR&currency=EUR"
	mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Category Hotel") || !strings.Contains(w.Body.String(), `data-stars="4.5"`) || !strings.Contains(w.Body.String(), `</html>`) {
		t.Fatalf("category results with fractional stars were not rendered (status %d)", w.Code)
	}
}

// Distinguish Go configuration/decoding failures, upstream HTTP failures and
// transport timeouts without contacting providers or exposing sensitive input.
func TestLiteAPIHotelSearchFailureDiagnostics(t *testing.T) {
	const secret = "sandbox-sensitive-test-value"
	tmpl := template.Must(template.ParseFS(templateFiles, "templates/*.html"))
	for _, search := range []string{"q=Berlin", "category=beach"} {
		for _, tc := range []struct {
			name, diagnostic string
			status           int
			transportErr     error
		}{
			{"missing_key", "LITEAPI_API_KEY is not configured", 0, nil},
			{"bad_request", "HTTP 400", 400, nil},
			{"unauthorized", "HTTP 401", 401, nil},
			{"forbidden", "HTTP 403", 403, nil},
			{"not_found", "HTTP 404", 404, nil},
			{"quota", "HTTP 429", 429, nil},
			{"provider_failure", "HTTP 500", 500, nil},
			{"gateway_failure", "HTTP 502", 502, nil},
			{"unavailable", "HTTP 503", 503, nil},
			{"gateway_timeout", "HTTP 504", 504, nil},
			{"timeout", "request timed out", 0, context.DeadlineExceeded},
			{"network", "network request failed", 0, errors.New(secret)},
			{"decode", "could not decode response data", 200, nil},
		} {
			t.Run(search+"/"+tc.name, func(t *testing.T) {
				t.Setenv("LITEAPI_API_KEY", secret)
				if tc.name == "missing_key" {
					t.Setenv("LITEAPI_API_KEY", "")
				}
				client := liteapi.NewClient()
				if client.HTTPClient.Timeout != 30*time.Second {
					t.Fatal("unexpected provider timeout")
				}
				calls := 0
				client.HTTPClient.Transport = searchTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.URL.Path != "/v3.0/data/hotels" {
						t.Fatal("failed metadata search must not request rates")
					}
					if tc.transportErr != nil {
						return nil, tc.transportErr
					}
					return &http.Response{StatusCode: tc.status, Header: make(http.Header),
						Body: io.NopCloser(strings.NewReader(secret))}, nil
				})
				var logs bytes.Buffer
				previous := log.Writer()
				log.SetOutput(&logs)
				t.Cleanup(func() { log.SetOutput(previous) })
				mux := newMux(&handlers.TravelHandler{Templates: tmpl, ExternalSearch: true, HotelClient: client})
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, httptest.NewRequest("GET", "/?"+search+"&checkin=2099-11-10&checkout=2099-11-13&guestNationality=TR", nil))
				message := "Otel araması tamamlanamadı"
				if tc.name == "timeout" {
					message = "Arama zaman aşımına uğradı"
				}
				if w.Code != 200 || !strings.Contains(w.Body.String(), message) {
					t.Fatal("unexpected user-facing failure")
				}
				for _, want := range []string{"method=GET endpoint=/data/hotels", fmt.Sprintf("status=%d", tc.status), "elapsed_ms=", tc.diagnostic} {
					if !strings.Contains(logs.String(), want) {
						t.Errorf("missing safe diagnostic %q", want)
					}
				}
				if strings.Contains(logs.String()+w.Body.String(), secret) || strings.Contains(logs.String(), "latitude=") {
					t.Fatal("sensitive input leaked")
				}
				if (tc.name == "missing_key" && calls != 0) || (tc.name != "missing_key" && calls != 1) {
					t.Fatal("unexpected supplier retry or category fan-out after failure")
				}
			})
		}
	}
}
