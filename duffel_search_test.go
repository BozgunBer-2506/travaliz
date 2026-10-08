package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"travel-proxy-service/internal/duffel"
	"travel-proxy-service/internal/handlers"
)

type flightSearchTimeout struct{}

func (flightSearchTimeout) Error() string   { return "mock timeout" }
func (flightSearchTimeout) Timeout() bool   { return true }
func (flightSearchTimeout) Temporary() bool { return true }

func TestDuffelFlightSearch(t *testing.T) {
	t.Setenv("DUFFEL_API_TOKEN", "duffel_test_offline")
	tmpl := template.Must(template.ParseFS(templateFiles, "templates/*.html"))
	oldTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
	calls := 0
	http.DefaultTransport = searchTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodPost || r.URL.Host != "api.duffel.com" ||
			r.URL.Path != "/air/offer_requests" || r.URL.Query().Get("return_offers") != "true" {
			t.Fatalf("unexpected provider request: %s %s", r.Method, r.URL)
		}
		var request struct {
			Data struct {
				Slices     []duffel.SearchSlice     `json:"slices"`
				Passengers []duffel.SearchPassenger `json:"passengers"`
				CabinClass string                   `json:"cabin_class"`
			} `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		data := request.Data
		if len(data.Slices) != 1 || len(data.Passengers) != 1 ||
			data.Passengers[0].Type != "adult" || data.Passengers[0].Age != nil ||
			data.Slices[0].DepartureDate != "2099-11-10" || data.CabinClass != "premium_economy" {
			t.Fatalf("unexpected search parameters: %+v", data)
		}
		slice := data.Slices[0]
		if slice.Origin == "STN" && slice.Destination == "LHR" {
			return nil, flightSearchTimeout{}
		}
		offers := []duffel.Offer{}
		switch {
		case slice.Origin == "PVD" && slice.Destination == "RAI":
		case slice.Origin == "LHR" && slice.Destination == "DXB":
			for i := 65; i > 0; i-- {
				offers = append(offers, duffel.Offer{
					ID: fmt.Sprintf("off_%02d", i), TotalAmount: fmt.Sprintf("%d.25", i),
					TotalCurrency: "GBP",
					Slices: []duffel.OfferSlice{{Duration: "PT5H30M", Segments: []duffel.Segment{
						{Origin: duffel.Airport{IATACode: "LHR"}, Destination: duffel.Airport{IATACode: "AMS"},
							DepartingAt:      "2099-11-10T08:00:00",
							MarketingCarrier: duffel.Carrier{Name: "Test Airline", LogoSymbolURL: "https://example.com/logo.svg"}},
						{Origin: duffel.Airport{IATACode: "AMS"}, Destination: duffel.Airport{IATACode: "DXB"},
							ArrivingAt: "2099-11-10T16:30:00"},
					}}},
				})
			}
			// Malformed offers must not panic or become misleading price cards.
			offers = append(offers, duffel.Offer{ID: "off_empty"}, offers[0])
			offers[len(offers)-1].ID = "off_bad_price"
			offers[len(offers)-1].TotalAmount = "NaN"
		default:
			t.Fatalf("unexpected route: %+v", slice)
		}
		body, err := json.Marshal(map[string]any{"data": map[string]any{"offers": offers}})
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})
	for _, external := range []bool{true, false} {
		mux := newMux(&handlers.TravelHandler{Templates: tmpl, ExternalSearch: external})
		get := func(path string) string {
			t.Helper()
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("status %d", w.Code)
			}
			return w.Body.String()
		}
		body := get("/flights?fromSky=lhr&toSky=dxb&date=2099-11-10&cabinClass=premium_economy")
		if strings.Count(body, `data-offer-id="off_`) != 20 ||
			strings.Contains(body, `data-offer-id="off_21"`) ||
			strings.Contains(body, `data-offer-id="off_bad_price"`) {
			t.Fatal("offer limit or malformed-offer handling failed")
		}
		if strings.Index(body, `data-offer-id="off_01"`) >= strings.Index(body, `data-offer-id="off_20"`) {
			t.Fatal("offers are not sorted by price")
		}
		for _, want := range []string{
			`data-from="LHR" data-to="DXB" data-airline="Test Airline"`,
			`data-price="1.25" data-currency="GBP"`,
			`data-depart="2099-11-10T08:00:00" data-arrive="2099-11-10T16:30:00" data-depart-date="2099-11-10"`,
			`data-duration="5h 30m" data-stops="1"`,
			"GBP 1.25", "https://example.com/logo.svg",
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("missing mapped field: %s", want)
			}
		}
		for _, tc := range []struct{ route, want string }{
			{"PVD&toSky=RAI", "Uçuş bulunamadı"},
			{"STN&toSky=LHR", "Arama zaman aşımına uğradı, tekrar deneyin"},
		} {
			body := get("/flights?fromSky=" + tc.route + "&date=2099-11-10&cabinClass=premium_economy")
			if !strings.Contains(body, tc.want) || strings.Contains(body, `data-offer-id="off_`) {
				t.Fatalf("unexpected error result for %s", tc.route)
			}
		}
		before := calls
		for _, query := range []string{
			"tripType=round", "tripType=multi", "returnDate=2099-11-11",
			"adults=2", "children=1", "children_ages=8", "cabinClass=invalid",
			"fromSky=BADCODE", "date=invalid",
		} {
			params := url.Values{"fromSky": {"LHR"}, "toSky": {"DXB"}, "date": {"2099-11-10"}}
			pair := strings.SplitN(query, "=", 2)
			params.Set(pair[0], pair[1])
			body := get("/flights?" + params.Encode())
			if strings.Contains(body, `data-offer-id="off_`) {
				t.Fatalf("invalid request returned flights: %s", query)
			}
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("HEAD", "/flights?fromSky=LHR&toSky=DXB", nil))
		if calls != before {
			t.Fatal("unsupported/invalid/HEAD request reached Duffel")
		}
	}
}
