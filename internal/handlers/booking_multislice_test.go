package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"travel-proxy-service/internal/db"
)

const roundTripOffer = `{"data":{"id":"offer-rt","total_amount":"480.00","total_currency":"EUR",
	"passengers":[{"id":"pax-1","type":"adult"}],
	"slices":[
	 {"duration":"PT2H","segments":[{"origin":{"iata_code":"BER"},"destination":{"iata_code":"LHR"},
	   "departing_at":"2099-12-01T09:00:00","arriving_at":"2099-12-01T10:00:00","marketing_carrier":{"name":"Out Air"}}]},
	 {"duration":"PT3H30M","segments":[
	   {"origin":{"iata_code":"LHR"},"destination":{"iata_code":"AMS"},"departing_at":"2099-12-08T18:00:00","arriving_at":"2099-12-08T20:00:00","marketing_carrier":{"name":"Back Air"}},
	   {"origin":{"iata_code":"AMS"},"destination":{"iata_code":"BER"},"departing_at":"2099-12-08T21:00:00","arriving_at":"2099-12-08T22:30:00","marketing_carrier":{"name":"Back Air"}}]}]}}`

const roundTripShown = `[{"origin":"BER","destination":"LHR","departingAt":"2099-12-01T09:00:00"},
	{"origin":"LHR","destination":"BER","departingAt":"2099-12-08T18:00:00"}]`

func roundTripRequest(t *testing.T, h *TravelHandler, token, slices string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"type":"flight","offerId":"offer-rt","email":"x@example.com","phone":"+4915112345678",
		"firstName":"Ada","lastName":"Guest","bornOn":"1990-01-01","gender":"f","title":"ms",
		"fromCode":"BER","toCode":"LHR","departTime":"2099-12-01T09:00:00"`
	if slices != "" {
		body += `,"slices":` + slices
	}
	r := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(body+"}"))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.BookHandler(w, r)
	return w
}

// A round trip is one offer with two slices and must become exactly one
// Duffel order, with the stored booking describing both legs from the offer.
func TestBookFlightRoundTripCreatesOneOrderAndStoresItinerary(t *testing.T) {
	var stored db.Booking
	var raw map[string]json.RawMessage
	orders := 0
	h, token := bookingTestHandler(t, func(r *http.Request) string {
		switch r.Method + " " + r.URL.Host + r.URL.Path {
		case "GET api.duffel.com/air/offers/offer-rt":
			return roundTripOffer
		case "POST api.duffel.com/air/orders":
			orders++
			var order struct {
				Data struct {
					SelectedOffers []string `json:"selected_offers"`
					Payments       []struct{ Amount, Currency string }
				} `json:"data"`
			}
			json.NewDecoder(r.Body).Decode(&order)
			if len(order.Data.SelectedOffers) != 1 || order.Data.SelectedOffers[0] != "offer-rt" ||
				order.Data.Payments[0].Amount != "480.00" || order.Data.Payments[0].Currency != "EUR" {
				t.Fatalf("round trip order payload wrong: %+v", order.Data)
			}
			return `{"data":{"id":"order-rt","booking_reference":"RTPNR1"}}`
		case "POST supabase.test/rest/v1/bookings":
			json.NewDecoder(r.Body).Decode(&raw)
			b, _ := json.Marshal(raw)
			json.Unmarshal(b, &stored)
			return `[]`
		default:
			t.Errorf("unexpected operation: %s %s", r.Method, r.URL)
			return `{}`
		}
	})
	t.Setenv("DUFFEL_API_TOKEN", "duffel_test_offline")
	w := roundTripRequest(t, h, token, roundTripShown)
	if w.Code != http.StatusOK || orders != 1 {
		t.Fatalf("round trip booking failed: HTTP %d, orders %d, %s", w.Code, orders, w.Body.String())
	}
	if len(stored.Itinerary) != 2 || stored.Itinerary[1].FromCode != "LHR" || stored.Itinerary[1].ToCode != "BER" ||
		stored.Itinerary[1].Stops != 1 || stored.Itinerary[1].Duration != "3h 30m" || stored.Itinerary[1].Airline != "Back Air" ||
		stored.FromCode != "BER" || stored.ToCode != "BER" || stored.ArriveTime != "2099-12-08T22:30:00" ||
		stored.Stops != 1 || stored.Price != 480 || stored.DuffelOrderID != "order-rt" {
		t.Fatalf("stored itinerary is incomplete or client-derived: %+v", stored)
	}

	// Repeating the request reuses the lock and never orders again.
	w = roundTripRequest(t, h, token, roundTripShown)
	if w.Code != http.StatusOK || orders != 1 {
		t.Fatalf("repeat round trip request reordered: HTTP %d, orders %d", w.Code, orders)
	}
}

func TestBookFlightRejectsItineraryNotMatchingOffer(t *testing.T) {
	for name, shown := range map[string]string{
		"legacy client without slices": "",
		"only outbound shown":          `[{"origin":"BER","destination":"LHR","departingAt":"2099-12-01T09:00:00"}]`,
		"different return time":        strings.Replace(roundTripShown, "2099-12-08T18:00:00", "2099-12-08T07:00:00", 1),
		"different return airport":     strings.Replace(roundTripShown, `"destination":"BER"`, `"destination":"TXL"`, 1),
		"too many slices":              `[{},{},{},{},{},{},{}]`,
	} {
		t.Run(name, func(t *testing.T) {
			h, token := bookingTestHandler(t, func(r *http.Request) string {
				if r.Method+" "+r.URL.Host+r.URL.Path == "GET api.duffel.com/air/offers/offer-rt" {
					return roundTripOffer
				}
				t.Errorf("no order or booking may be written: %s %s", r.Method, r.URL)
				return `{}`
			})
			t.Setenv("DUFFEL_API_TOKEN", "duffel_test_offline")
			w := roundTripRequest(t, h, token, shown)
			if w.Code != http.StatusConflict && w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("mismatched itinerary accepted: HTTP %d %s", w.Code, w.Body.String())
			}
			if store := h.FlightStore.(*fakeFlightStore); len(store.rows) > 0 {
				for _, row := range store.rows {
					if row.Status != "pending" {
						t.Fatalf("lock left in %q after a rejected itinerary", row.Status)
					}
				}
			}
		})
	}
}

// The itinerary check runs inside the pending -> ordering CAS window: a
// rejected itinerary must release the lock to "pending" (no order exists),
// and concurrent correct retries afterwards must still produce one order.
func TestBookFlightItineraryCheckKeepsSingleOrderUnderCAS(t *testing.T) {
	var mu sync.Mutex
	orders, rows := 0, 0
	h, token := bookingTestHandler(t, func(r *http.Request) string {
		switch r.Method + " " + r.URL.Host + r.URL.Path {
		case "GET api.duffel.com/air/offers/offer-rt":
			return roundTripOffer
		case "POST api.duffel.com/air/orders":
			mu.Lock()
			orders++
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			return `{"data":{"id":"order-rt","booking_reference":"RTPNR1"}}`
		case "POST supabase.test/rest/v1/bookings":
			mu.Lock()
			rows++
			mu.Unlock()
			return `[]`
		default:
			t.Errorf("unexpected operation: %s %s", r.Method, r.URL)
			return `{}`
		}
	})
	t.Setenv("DUFFEL_API_TOKEN", "duffel_test_offline")
	store := h.FlightStore.(*fakeFlightStore)

	if w := roundTripRequest(t, h, token, `[{"origin":"BER","destination":"LHR","departingAt":"2099-12-01T09:00:00"}]`); w.Code != http.StatusConflict {
		t.Fatalf("partial itinerary accepted: HTTP %d", w.Code)
	}
	for _, row := range store.rows {
		if row.Status != "pending" || row.OrderID != "" {
			t.Fatalf("rejected itinerary left lock %+v", row)
		}
	}

	var wg sync.WaitGroup
	codes := make([]int, 5)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = roundTripRequest(t, h, token, roundTripShown).Code
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, code := range codes {
		if code == http.StatusOK {
			ok++
		} else if code != http.StatusConflict {
			t.Fatalf("unexpected status %d", code)
		}
	}
	if orders != 1 || rows != 1 || ok < 1 {
		t.Fatalf("expected exactly one order and one booking row, got orders=%d rows=%d ok=%d", orders, rows, ok)
	}
	for _, row := range store.rows {
		if row.Status != "confirmed_sandbox" {
			t.Fatalf("lock not confirmed: %+v", row)
		}
	}
}
