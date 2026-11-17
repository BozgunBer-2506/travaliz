package main

import (
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"travel-proxy-service/internal/duffel"
	"travel-proxy-service/internal/handlers"
)

func multiSliceSegment(from, to, depart, arrive, airline string) duffel.Segment {
	return duffel.Segment{
		Origin: duffel.Airport{IATACode: from, CityName: from + " City"}, Destination: duffel.Airport{IATACode: to, CityName: to + " City"},
		DepartingAt: depart, ArrivingAt: arrive, MarketingCarrier: duffel.Carrier{Name: airline},
	}
}

func TestDuffelRoundTripAndMultiCitySearch(t *testing.T) {
	t.Setenv("DUFFEL_API_TOKEN", "duffel_test_offline")
	tmpl := template.Must(template.ParseFS(templateFiles, "templates/*.html"))
	oldTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
	var got []duffel.SearchSlice
	var gotPassengers []duffel.SearchPassenger
	calls := 0
	respond := func(v any) (*http.Response, error) {
		body, _ := json.Marshal(map[string]any{"data": v})
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	}
	var pending []duffel.Offer
	http.DefaultTransport = searchTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method == http.MethodGet {
			q := r.URL.Query()
			if r.URL.Path != "/air/offers" || q.Get("offer_request_id") != "orq_test" || q.Get("sort") != "total_amount" || q.Get("limit") != "50" {
				t.Fatalf("unexpected list offers request: %s", r.URL)
			}
			return respond(pending)
		}
		if r.URL.Path != "/air/offer_requests" {
			t.Fatalf("unexpected request: %s", r.URL)
		}
		var request struct {
			Data struct {
				Slices     []duffel.SearchSlice     `json:"slices"`
				Passengers []duffel.SearchPassenger `json:"passengers"`
			} `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		got, gotPassengers = request.Data.Slices, request.Data.Passengers
		var offerSlices []duffel.OfferSlice
		for i, s := range got {
			airline := "Leg Air"
			segments := []duffel.Segment{multiSliceSegment(s.Origin, s.Destination, s.DepartureDate+"T09:00:00", s.DepartureDate+"T11:15:00", airline)}
			if i == 1 {
				segments = []duffel.Segment{
					multiSliceSegment(s.Origin, "AMS", s.DepartureDate+"T18:00:00", s.DepartureDate+"T19:00:00", "Second Air"),
					multiSliceSegment("AMS", s.Destination, s.DepartureDate+"T20:00:00", "2099-12-31T01:30:00", "Second Air"),
				}
			}
			offerSlices = append(offerSlices, duffel.OfferSlice{Duration: "PT2H15M", Segments: segments})
		}
		offers := []duffel.Offer{
			{ID: "off_full", TotalAmount: "640.50", TotalCurrency: "EUR", Slices: offerSlices},
			// An offer missing a requested slice must never be shown.
			{ID: "off_partial", TotalAmount: "1.00", TotalCurrency: "EUR", Slices: offerSlices[:1]},
		}
		if len(got) > 1 {
			// Multi-slice searches create the request without inline offers
			// and read the cheapest page through List Offers.
			if r.URL.Query().Get("return_offers") != "false" {
				t.Fatalf("multi-slice search must not inline offers: %s", r.URL)
			}
			pending = offers
			return respond(map[string]any{"id": "orq_test"})
		}
		if r.URL.Query().Get("return_offers") != "true" {
			t.Fatalf("one-way search changed: %s", r.URL)
		}
		return respond(map[string]any{"offers": offers})
	})
	mux := newMux(&handlers.TravelHandler{Templates: tmpl, ExternalSearch: true})
	get := func(path string) string {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status %d", w.Code)
		}
		return w.Body.String()
	}

	body := get("/flights?tripType=round&fromSky=ber&toSky=lhr&date=2099-12-01&returnDate=2099-12-08&adults=2&children=1&children_ages=7")
	if len(got) != 2 || got[0] != (duffel.SearchSlice{Origin: "BER", Destination: "LHR", DepartureDate: "2099-12-01"}) ||
		got[1] != (duffel.SearchSlice{Origin: "LHR", Destination: "BER", DepartureDate: "2099-12-08"}) || len(gotPassengers) != 3 {
		t.Fatalf("round trip slices/passengers wrong: %+v %+v", got, gotPassengers)
	}
	if strings.Contains(body, "off_partial") || !strings.Contains(body, `data-offer-id="off_full"`) {
		t.Fatal("offer slice count was not enforced")
	}
	for _, want := range []string{
		"EUR 640.50", "Itinerary total for 2 adults · 1 child",
		`data-route="BER → LHR → BER"`, `data-adults="2" data-child-ages="7"`,
		`class="flight-leg`, "Leg Air", "Second Air", "1 stop", "Direct", "(2099-12-31)",
		`data-stops="1"`, `data-from="BER" data-to="BER"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("round trip result missing %q", want)
		}
	}
	if !strings.Contains(body, "&#34;from_code&#34;:&#34;LHR&#34;") {
		t.Fatal("legs JSON for booking verification is missing")
	}
	// Each slice row carries its own times, airline and stops; the price
	// appears once, as the offer total, never per slice.
	card := body[strings.Index(body, `class="flight-card`):]
	card = card[:strings.Index(card, "Request booking")]
	// The price/CTA column is the only element using sm:items-end.
	rows := strings.Split(card[:strings.Index(card, "sm:items-end")], `class="flight-leg`)[1:]
	if len(rows) != 2 {
		t.Fatalf("expected 2 slice rows, got %d", len(rows))
	}
	for i, want := range [][]string{
		{"Leg Air", ">BER<", ">LHR<", "09:00", "11:15", "2h 15m", "Direct"},
		{"Second Air", ">LHR<", ">BER<", "18:00", "01:30", "(2099-12-31)", "1 stop"},
	} {
		for _, w := range want {
			if !strings.Contains(rows[i], w) {
				t.Fatalf("slice %d missing %q", i+1, w)
			}
		}
		if strings.Contains(rows[i], "EUR") || strings.Contains(rows[i], "640.50") {
			t.Fatalf("slice %d shows a price; only the offer total may be shown", i+1)
		}
	}
	if strings.Count(card, "EUR 640.50") != 1 || !strings.Contains(card, "Itinerary total for 2 adults · 1 child") {
		t.Fatal("card must show exactly one itinerary total")
	}

	body = get("/flights?tripType=multi&leg0from=BER&leg0to=LHR&leg0date=2099-12-01&leg1from=CDG&leg1to=IST&leg1date=2099-12-05&leg2from=IST&leg2to=BER&leg2date=2099-12-09")
	if len(got) != 3 || got[1].Origin != "CDG" || got[2].DepartureDate != "2099-12-09" {
		t.Fatalf("multi-city slices wrong: %+v", got)
	}
	if !strings.Contains(body, `data-route="BER → LHR · CDG → IST → BER"`) || !strings.Contains(body, "Multi-city · 3 flights") {
		t.Fatal("multi-city route not rendered")
	}
	// The searched legs are kept for refilling the multi-city form.
	for _, want := range []string{
		`<span data-from="BER" data-to="LHR" data-date="2099-12-01"></span>`,
		`<span data-from="CDG" data-to="IST" data-date="2099-12-05"></span>`,
		`<span data-from="IST" data-to="BER" data-date="2099-12-09"></span>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("multi-city form state missing %s", want)
		}
	}
	// Also after a validation error, without calling Duffel.
	callsBeforeError := calls
	body = get("/flights?tripType=multi&leg0from=BER&leg0to=LHR&leg0date=2099-12-05&leg1from=LHR&leg1to=CDG&leg1date=2099-12-01")
	if calls != callsBeforeError || !strings.Contains(body, `<span data-from="LHR" data-to="CDG" data-date="2099-12-01"></span>`) {
		t.Fatal("multi-city legs not kept after a validation error")
	}

	// One-way keeps a single slice and the single-route card.
	body = get("/flights?fromSky=BER&toSky=LHR&date=2099-12-01&returnDate=2099-12-08")
	if len(got) != 1 || strings.Contains(body, `class="flight-leg`) || !strings.Contains(body, "Total for 1 adult") {
		t.Fatalf("one-way search changed: %+v", got)
	}

	before := calls
	for _, query := range []string{
		"tripType=round&fromSky=BER&toSky=LHR&date=2099-12-08&returnDate=2099-12-01",
		"tripType=round&fromSky=BER&toSky=LHR&date=2099-12-01&returnDate=invalid",
		"tripType=round&fromSky=BER&toSky=BER&date=2099-12-01&returnDate=2099-12-08",
		"tripType=multi&leg0from=BER&leg0to=LHR&leg0date=2099-12-01",
		"tripType=multi&leg0from=BER&leg0to=LHR&leg0date=2099-12-05&leg1from=LHR&leg1to=CDG&leg1date=2099-12-01",
		"tripType=multi&leg0from=BER&leg0to=LHR&leg0date=2099-12-01&leg1from=LHR&leg1to=LHR&leg1date=2099-12-03",
		"tripType=multi&leg0from=BER&leg0to=LHR&leg0date=2099-12-01&leg1from=LONDON&leg1to=CDG&leg1date=2099-12-03",
		"tripType=multi&leg0from=BER&leg0to=LHR&leg0date=2000-01-01&leg1from=LHR&leg1to=CDG&leg1date=2099-12-03",
		"tripType=multi&leg0from=BER&leg0to=LHR&leg0date=2099-12-01&leg1from=LHR&leg1to=CDG&leg1date=",
		"tripType=round&fromSky=BER&toSky=LHR&date=2099-12-01&returnDate=2099-12-08&adults=9&children=1&children_ages=5",
	} {
		if body := get("/flights?" + query); strings.Contains(body, `data-offer-id="off_`) {
			t.Fatalf("invalid itinerary returned flights: %s", query)
		}
	}
	if calls != before {
		t.Fatal("invalid itinerary reached Duffel")
	}
}
