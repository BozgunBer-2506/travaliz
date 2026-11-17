package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"travel-proxy-service/internal/duffel"
)

// fixtureSlices gives single-slice offer fixtures a departure date in the
// same offset-free format Duffel really sends (airport-local time). Tomorrow
// keeps fixtures whose birthdates are relative to today in the same age.
var fixtureSlices = `"slices":[{"segments":[{"departing_at":"` +
	time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02T15:04:05") + `"}]}],`

func offerDeparting(at string) duffel.Offer {
	return duffel.Offer{Slices: []duffel.OfferSlice{{Segments: []duffel.Segment{{DepartingAt: at}}}}}
}

func TestTravelReferenceDateParsesDuffelFormats(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"2026-12-18T13:09:00", "2026-12-18"},       // Duffel's real offset-free format
		{"2026-12-18T23:59:59", "2026-12-18"},       // late local departure keeps its day
		{"2099-06-20T10:00:00Z", "2099-06-20"},      // RFC3339 UTC
		{"2026-12-18T01:30:00+03:00", "2026-12-18"}, // RFC3339 offset: local day, not the UTC day
	} {
		got, err := travelReferenceDate(offerDeparting(tc.in))
		if err != nil || got.Format("2006-01-02") != tc.want {
			t.Fatalf("%q: got %v, %v; want %s", tc.in, got, err, tc.want)
		}
	}
	for _, offer := range []duffel.Offer{{}, {Slices: []duffel.OfferSlice{{}}}, offerDeparting(""), offerDeparting("18.12.2026 13:09")} {
		if got, err := travelReferenceDate(offer); err == nil {
			t.Fatalf("unusable departure date accepted as %v; must not fall back to today", got)
		}
	}
	// The first slice decides, even when a later slice departs first in UTC.
	multi := duffel.Offer{Slices: []duffel.OfferSlice{
		{Segments: []duffel.Segment{{DepartingAt: "2026-12-18T08:00:00"}}},
		{Segments: []duffel.Segment{{DepartingAt: "2026-12-25T08:00:00"}}},
	}}
	if got, _ := travelReferenceDate(multi); got.Format("2006-01-02") != "2026-12-18" {
		t.Fatalf("reference date not taken from the first slice: %v", got)
	}
}

// bookChildAgainstDeparture books 1 adult + 1 child (searched at age 7) on an
// offer departing at departingAt, returning the HTTP status and the number of
// CreateOrder calls.
func bookChildAgainstDeparture(t *testing.T, departingAt, childBornOn string) (int, int32, *TravelHandler) {
	t.Helper()
	var orders atomic.Int32
	slices := `"slices":[{"segments":[{"departing_at":"` + departingAt + `"}]}],`
	if departingAt == "" {
		slices = ""
	}
	h, token := bookingTestHandler(t, func(r *http.Request) string {
		switch r.Method + " " + r.URL.Host + r.URL.Path {
		case "GET api.duffel.com/air/offers/offer-child":
			return `{"data":{"id":"offer-child","total_amount":"300.00","total_currency":"EUR",` + slices +
				`"passengers":[{"id":"pax-adult","type":"adult"},{"id":"pax-child","age":7}]}}`
		case "POST api.duffel.com/air/orders":
			orders.Add(1)
			return `{"data":{"id":"order-child","booking_reference":"PNRCHD"}}`
		case "POST supabase.test/rest/v1/bookings":
			return `[]`
		default:
			t.Errorf("unexpected flight operation: %s %s", r.Method, r.URL)
			return `{}`
		}
	})
	t.Setenv("DUFFEL_API_TOKEN", "duffel_test_offline")
	r := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(fmt.Sprintf(`{"type":"flight","offerId":"offer-child",
		"email":"other@example.com","phone":"+4915112345678","passengers":[
		{"firstName":"Adult","lastName":"Guest","bornOn":"1990-01-01","gender":"m","title":"mr"},
		{"firstName":"Kid","lastName":"Guest","bornOn":"%s","gender":"f","title":"ms"}]}`, childBornOn)))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.BookHandler(w, r)
	return w.Code, orders.Load(), h
}

// Reproduces the live sandbox failure: Duffel's offset-free departing_at was
// unreadable as RFC3339, ages fell back to today, and a child who turns 7
// between booking and departure was rejected for a 7-year-old fare.
func TestBookFlightChildAgeUsesOffsetFreeDepartureDate(t *testing.T) {
	departure := time.Now().UTC().AddDate(0, 0, 31)
	bornOn := departure.AddDate(-7, 0, -30).Format("2006-01-02") // 6 today, 7 at departure
	code, orders, _ := bookChildAgainstDeparture(t, departure.Format("2006-01-02")+"T13:09:00", bornOn)
	if code != http.StatusOK || orders != 1 {
		t.Fatalf("child aged 7 at departure rejected: HTTP %d, %d orders", code, orders)
	}
}

func TestBookFlightChildBirthdayBoundaryAtDeparture(t *testing.T) {
	for _, tc := range []struct {
		name, departingAt, bornOn string
		ok                        bool
	}{
		{"birthday on departure day (offset-free)", "2099-12-18T00:05:00", "2092-12-18", true},
		{"birthday on departure day (RFC3339)", "2099-12-18T23:30:00+02:00", "2092-12-18", true},
		{"birthday the day before departure", "2099-12-18T13:09:00", "2092-12-17", true},
		{"birthday the day after departure", "2099-12-18T13:09:00", "2092-12-19", false},
		{"turns 8 on departure day", "2099-12-18T13:09:00", "2091-12-18", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, orders, _ := bookChildAgainstDeparture(t, tc.departingAt, tc.bornOn)
			if tc.ok && (code != http.StatusOK || orders != 1) {
				t.Fatalf("valid age rejected: HTTP %d, %d orders", code, orders)
			}
			if !tc.ok && (code != http.StatusUnprocessableEntity || orders != 0) {
				t.Fatalf("wrong age accepted: HTTP %d, %d orders", code, orders)
			}
		})
	}
}

// Without a readable departure date the booking stops before CreateOrder and
// releases the lock, instead of assessing ages as of today.
func TestBookFlightRejectsOfferWithoutReadableDepartureDate(t *testing.T) {
	for _, departingAt := range []string{"", "not-a-date"} {
		bornOn := time.Now().UTC().AddDate(-7, 0, -1).Format("2006-01-02") // 7 today
		code, orders, h := bookChildAgainstDeparture(t, departingAt, bornOn)
		if code != http.StatusConflict || orders != 0 {
			t.Fatalf("departing_at %q: HTTP %d, %d orders; want 409 and no order", departingAt, code, orders)
		}
		store := h.FlightStore.(*fakeFlightStore)
		for _, row := range store.rows {
			if row.Status != "pending" {
				t.Fatalf("lock not released: %s", row.Status)
			}
		}
	}
}
