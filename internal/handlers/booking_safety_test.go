package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"travel-proxy-service/internal/db"
)

func cancelWith(t *testing.T, row string) (int, int) {
	t.Helper()
	deletes := 0
	h, token := bookingTestHandler(t, func(r *http.Request) string {
		switch r.Method + " " + r.URL.Host + r.URL.Path {
		case "GET supabase.test/rest/v1/bookings":
			return row
		case "DELETE supabase.test/rest/v1/bookings":
			deletes++
			return `[]`
		default:
			t.Errorf("unexpected operation: %s %s", r.Method, r.URL)
			return `{}`
		}
	})
	r := httptest.NewRequest(http.MethodPost, "/cancel-booking", strings.NewReader(`{"ref":"TM-ABC123"}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.CancelBookingHandler(w, r)
	return w.Code, deletes
}

func TestCancelNeverDeletesProviderBackedBookings(t *testing.T) {
	for name, row := range map[string]string{
		"confirmed sandbox flight": `[{"ref":"TM-ABC123","type":"flight","email":"guest@example.com","status":"confirmed_sandbox","duffel_order_id":"ord_1","booking_reference":"PNR1"}]`,
		"unknown outcome flight":   `[{"ref":"TM-ABC123","type":"flight","email":"guest@example.com","status":"unknown"}]`,
		"order id without status":  `[{"ref":"TM-ABC123","type":"flight","email":"guest@example.com","duffel_order_id":"ord_1"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			if code, deletes := cancelWith(t, row); code != http.StatusConflict || deletes != 0 {
				t.Fatalf("HTTP %d, %d deletes; provider-backed row must not be removed", code, deletes)
			}
		})
	}
	t.Run("another user's booking", func(t *testing.T) {
		row := `[{"ref":"TM-ABC123","type":"car","email":"someone-else@example.com"}]`
		if code, deletes := cancelWith(t, row); code != http.StatusNotFound || deletes != 0 {
			t.Fatalf("HTTP %d, %d deletes", code, deletes)
		}
	})
	t.Run("missing booking", func(t *testing.T) {
		if code, deletes := cancelWith(t, `[]`); code != http.StatusNotFound || deletes != 0 {
			t.Fatalf("HTTP %d, %d deletes", code, deletes)
		}
	})
	t.Run("own placeholder request", func(t *testing.T) {
		row := `[{"ref":"TM-ABC123","type":"car","email":"guest@example.com","status":""}]`
		if code, deletes := cancelWith(t, row); code != http.StatusOK || deletes != 1 {
			t.Fatalf("placeholder removal broken: HTTP %d, %d deletes", code, deletes)
		}
	})
}

func TestGenericBookingPathAcceptsOnlyBoundedCarRequests(t *testing.T) {
	writes := 0
	h, token := bookingTestHandler(t, func(r *http.Request) string {
		if r.Method+" "+r.URL.Host+r.URL.Path == "POST supabase.test/rest/v1/bookings" {
			writes++
			return `[]`
		}
		t.Errorf("unexpected operation: %s %s", r.Method, r.URL)
		return `{}`
	})
	post := func(body string) int {
		r := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.BookHandler(w, r)
		return w.Code
	}
	base := `"firstName":"Test","lastName":"Guest","phone":"+49123456789","price":120,"currency":"EUR"`
	for _, body := range []string{
		`{"type":"giftcard",` + base + `}`,
		`{"type":"",` + base + `}`,
		`{"type":"car",` + base + `,"hotelName":"` + strings.Repeat("x", 201) + `"}`,
		`{"type":"car","firstName":"Test","lastName":"Guest","phone":"+49123456789","price":1e12,"currency":"EUR"}`,
		`{"type":"car",` + strings.Replace(base, `"EUR"`, `"<b>"`, 1) + `}`,
	} {
		if code := post(body); code != http.StatusUnprocessableEntity {
			t.Fatalf("HTTP %d for %s", code, body[:40])
		}
	}
	if writes != 0 {
		t.Fatalf("rejected requests wrote %d rows", writes)
	}
	if code := post(`{"type":"car",` + base + `,"hotelName":"Compact car","checkin":"2099-01-01","checkout":"2099-01-05"}`); code != http.StatusOK || writes != 1 {
		t.Fatalf("valid car request: HTTP %d, %d writes", code, writes)
	}
}

func TestOneWayBookingRowUsesOfferNotClientRoute(t *testing.T) {
	var stored db.Booking
	h, token := bookingTestHandler(t, func(r *http.Request) string {
		switch r.Method + " " + r.URL.Host + r.URL.Path {
		case "GET api.duffel.com/air/offers/offer-ow":
			return `{"data":{"id":"offer-ow","total_amount":"150.00","total_currency":"EUR","passengers":[{"id":"pax-1","type":"adult"}],
				"slices":[{"duration":"PT3H30M","segments":[
				  {"origin":{"iata_code":"BER"},"destination":{"iata_code":"AMS"},"departing_at":"2099-12-01T09:00:00","arriving_at":"2099-12-01T10:30:00","marketing_carrier":{"name":"Real Air"}},
				  {"origin":{"iata_code":"AMS"},"destination":{"iata_code":"LHR"},"departing_at":"2099-12-01T11:30:00","arriving_at":"2099-12-01T12:30:00","marketing_carrier":{"name":"Real Air"}}]}]}}`
		case "POST api.duffel.com/air/orders":
			return `{"data":{"id":"ord_ow","booking_reference":"PNROW"}}`
		case "POST supabase.test/rest/v1/bookings":
			return `[]`
		default:
			t.Errorf("unexpected operation: %s %s", r.Method, r.URL)
			return `{}`
		}
	})
	t.Setenv("DUFFEL_API_TOKEN", "duffel_test_offline")
	store := newFakeFlightStore()
	h.FlightStore = store
	// Capture the row written to bookings.
	previous := http.DefaultTransport
	http.DefaultTransport = bookingTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost && r.URL.Path == "/rest/v1/bookings" {
			json.NewDecoder(r.Body).Decode(&stored)
			r.Body = http.NoBody
		}
		return previous.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	r := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(`{"type":"flight","offerId":"offer-ow",
		"phone":"+4915112345678","firstName":"Ada","lastName":"Guest","bornOn":"1990-01-01","gender":"f","title":"ms",
		"fromCode":"XXX","toCode":"YYY","airline":"Forged Air","departTime":"2000-01-01T00:00:00","duration":"99h 0m","stops":7}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.BookHandler(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("one-way booking failed: HTTP %d %s", w.Code, w.Body)
	}
	if stored.FromCode != "BER" || stored.ToCode != "LHR" || stored.Airline != "Real Air" || stored.DepartTime != "2099-12-01T09:00:00" ||
		stored.ArriveTime != "2099-12-01T12:30:00" || stored.Duration != "3h 30m" || stored.Stops != 1 || stored.Itinerary != nil || stored.Price != 150 {
		t.Fatalf("booking row not taken from the offer: %+v", stored)
	}
}
