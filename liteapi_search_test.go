package main

import (
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

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
		body, status := `{"data":[{"id":"hotel","name":"API Hotel","stars":4}]}`, 200
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
	for _, want := range []string{`data-price="33.34"`, `data-total="100.01"`, `data-currency="GBP"`, `GBP 33.34`, `GBP 100.01`, `/night average`, `value="TR"`} {
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

// A category search (no city) renders through a different template branch
// gated on CategoryTitle; results must not be silently hidden there.
func TestLiteAPIHotelCategorySearchRendersResults(t *testing.T) {
	t.Setenv("LITEAPI_API_KEY", "sandbox-test")
	client := liteapi.NewClient()
	client.HTTPClient.Transport = searchTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"data":[{"id":"hotel","name":"Category Hotel","stars":4}]}`
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
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Category Hotel") {
		t.Fatalf("category results were not rendered (status %d): %s", w.Code, w.Body.String())
	}
}
