package handlers

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"travel-proxy-service/internal/auth"
	"travel-proxy-service/internal/db"
)

// fakeFlightStore is an in-memory stand-in for the real Supabase-backed
// FlightStore, giving tests (including concurrent ones) a real mutex-guarded
// compare-and-swap instead of having to simulate PostgREST's on_conflict and
// conditional-PATCH semantics through string matching in a mock transport.
type fakeFlightStore struct {
	mu   sync.Mutex
	rows map[string]*db.FlightCheckout
}

func newFakeFlightStore() *fakeFlightStore {
	return &fakeFlightStore{rows: map[string]*db.FlightCheckout{}}
}

func (f *fakeFlightStore) ReserveFlight(userID, offerID string) (*db.FlightCheckout, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := userID + "|" + offerID
	if row, ok := f.rows[key]; ok {
		copy := *row
		return &copy, false, nil
	}
	now := time.Now()
	row := &db.FlightCheckout{ID: uuid.NewString(), UserID: userID, OfferID: offerID, Status: "pending", CreatedAt: now, UpdatedAt: now}
	f.rows[key] = row
	copy := *row
	return &copy, true, nil
}

func (f *fakeFlightStore) GetFlight(id, userID string) (*db.FlightCheckout, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.rows {
		if row.ID == id && row.UserID == userID {
			copy := *row
			return &copy, nil
		}
	}
	return nil, nil
}

func (f *fakeFlightStore) UpdateFlight(id, userID, from, to string, fields map[string]any) (*db.FlightCheckout, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.rows {
		if row.ID == id && row.UserID == userID {
			if row.Status != from {
				return nil, false, nil
			}
			row.Status = to
			row.UpdatedAt = time.Now()
			if v, ok := fields["order_id"].(string); ok {
				row.OrderID = v
			}
			if v, ok := fields["booking_reference"].(string); ok {
				row.BookingReference = v
			}
			if v, ok := fields["booking_ref"].(string); ok {
				row.BookingRef = v
			}
			copy := *row
			return &copy, true, nil
		}
	}
	return nil, false, nil
}

// backdateUpdatedAt lets a test simulate a lock that has been stuck at its
// current status for a given duration, e.g. a process that died mid
// CreateOrder before ever reaching a deferred status transition.
func (f *fakeFlightStore) backdateUpdatedAt(userID, offerID string, age time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if row, ok := f.rows[userID+"|"+offerID]; ok {
		row.UpdatedAt = time.Now().Add(-age)
	}
}

type bookingTransport func(*http.Request) (*http.Response, error)

func (f bookingTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Use a locally generated JWT and intercept every HTTP request. These tests
// cannot reach Supabase, Duffel, LiteAPI or an actual B2B webhook.
func bookingTestHandler(t *testing.T, respond func(*http.Request) string) (*TravelHandler, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	coordinate := func(n []byte) string { return base64.RawURLEncoding.EncodeToString(n) }
	jwks := fmt.Sprintf(`{"keys":[{"kty":"EC","crv":"P-256","alg":"ES256","use":"sig","kid":"test","x":%q,"y":%q}]}`,
		coordinate(key.X.FillBytes(make([]byte, 32))), coordinate(key.Y.FillBytes(make([]byte, 32))))
	previous := http.DefaultTransport
	http.DefaultTransport = bookingTransport(func(r *http.Request) (*http.Response, error) {
		body := jwks
		if r.URL.Host != "supabase.test" || r.URL.Path != "/auth/v1/.well-known/jwks.json" {
			body = respond(r)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	t.Setenv("SUPABASE_URL", "https://supabase.test")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := auth.Init(ctx); err != nil {
		t.Fatal(err)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"email": "guest@example.com", "aud": "authenticated", "exp": time.Now().Add(time.Hour).Unix(),
		"sub": "11111111-1111-4111-8111-111111111111",
	})
	token.Header["kid"] = "test"
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	database, err := db.Open("https://supabase.test", "test-service-key")
	if err != nil {
		t.Fatal(err)
	}
	return &TravelHandler{DB: database, FlightStore: newFakeFlightStore()}, signed
}

// bookingTimeoutErr implements net.Error so a transport can simulate a real
// network timeout, the same way DuffelClient would see one.
type bookingTimeoutErr struct{}

func (bookingTimeoutErr) Error() string   { return "simulated timeout" }
func (bookingTimeoutErr) Timeout() bool   { return true }
func (bookingTimeoutErr) Temporary() bool { return true }

// bookingTestHandlerErr is bookingTestHandler with full control over the
// transport's response, including returning a network error, for tests that
// need to simulate a timeout rather than just choosing a response body.
func bookingTestHandlerErr(t *testing.T, respond func(*http.Request) (*http.Response, error)) (*TravelHandler, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	coordinate := func(n []byte) string { return base64.RawURLEncoding.EncodeToString(n) }
	jwks := fmt.Sprintf(`{"keys":[{"kty":"EC","crv":"P-256","alg":"ES256","use":"sig","kid":"test","x":%q,"y":%q}]}`,
		coordinate(key.X.FillBytes(make([]byte, 32))), coordinate(key.Y.FillBytes(make([]byte, 32))))
	previous := http.DefaultTransport
	http.DefaultTransport = bookingTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "supabase.test" && r.URL.Path == "/auth/v1/.well-known/jwks.json" {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(jwks))}, nil
		}
		return respond(r)
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	t.Setenv("SUPABASE_URL", "https://supabase.test")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := auth.Init(ctx); err != nil {
		t.Fatal(err)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"email": "guest@example.com", "aud": "authenticated", "exp": time.Now().Add(time.Hour).Unix(),
		"sub": "11111111-1111-4111-8111-111111111111",
	})
	token.Header["kid"] = "test"
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	database, err := db.Open("https://supabase.test", "test-service-key")
	if err != nil {
		t.Fatal(err)
	}
	return &TravelHandler{DB: database, FlightStore: newFakeFlightStore()}, signed
}

func TestBookHotelRejectsUnverifiedClaimsWithoutSideEffects(t *testing.T) {
	h, token := bookingTestHandler(t, func(r *http.Request) string {
		t.Errorf("unverified hotel request triggered an HTTP operation: %s %s", r.Method, r.URL)
		return `[]`
	})
	t.Setenv("B2B_WEBHOOK_ENABLED", "true")
	t.Setenv("B2B_WEBHOOK_URL", "https://b2b.test")
	t.Setenv("B2B_WEBHOOK_SECRET", "test-secret")
	for _, price := range []string{"0.01", "999999"} {
		r := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(fmt.Sprintf(
			`{"type":"hotel","offerId":"forged","hotelName":"Forged Hotel","price":%s,"currency":"EUR","firstName":"Test","lastName":"Guest","email":"other@example.com","phone":"+49123456789","status":"confirmed_sandbox","prebookId":"forged"}`, price)))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.BookHandler(w, r)
		var result BookingResponse
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusConflict || result.Ref != "" || result.Status != "" ||
			result.Error != "Hotel booking is not available yet. No reservation was made and no payment was taken." {
			t.Fatalf("unverified hotel accepted: HTTP %d, %+v", w.Code, result)
		}
	}
	w := httptest.NewRecorder()
	h.BookHandler(w, httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(`{"type":"hotel"}`)))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("hotel authentication bypassed: HTTP %d", w.Code)
	}
}

func TestBookFlightRejectsPassengerCountMismatch(t *testing.T) {
	h, token := bookingTestHandler(t, func(r *http.Request) string {
		if r.Method != http.MethodGet || r.URL.Host != "api.duffel.com" || r.URL.Path != "/air/offers/offer-group" {
			t.Fatalf("group offer triggered an unexpected operation: %s %s", r.Method, r.URL)
		}
		return `{"data":{"id":"offer-group","total_amount":"200.00","total_currency":"EUR","passengers":[{"id":"pax-1"},{"id":"pax-2"}]}}`
	})
	t.Setenv("DUFFEL_API_TOKEN", "duffel_test_offline")
	// Legacy single-passenger shape against a two-passenger offer.
	r := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(
		`{"type":"flight","offerId":"offer-group","firstName":"Test","lastName":"Guest","phone":"+49123456789","bornOn":"1990-01-01","gender":"m","title":"mr"}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.BookHandler(w, r)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "passenger(s)") {
		t.Fatalf("passenger count mismatch was not rejected: HTTP %d, %s", w.Code, w.Body)
	}
}

func TestBookFlightRejectsChildAgeInconsistentWithSearch(t *testing.T) {
	h, token := bookingTestHandler(t, func(r *http.Request) string {
		if r.Method != http.MethodGet || r.URL.Host != "api.duffel.com" || r.URL.Path != "/air/offers/offer-group" {
			t.Fatalf("group offer triggered an unexpected operation: %s %s", r.Method, r.URL)
		}
		return `{"data":{"id":"offer-group","total_amount":"200.00","total_currency":"EUR","passengers":[{"id":"pax-1","type":"adult"},{"id":"pax-2","age":10}]}}`
	})
	t.Setenv("DUFFEL_API_TOKEN", "duffel_test_offline")
	r := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(`{"type":"flight","offerId":"offer-group",
		"email":"other@example.com","phone":"+49123456789","passengers":[
		{"firstName":"Adult","lastName":"Guest","bornOn":"1990-01-01","gender":"m","title":"mr"},
		{"firstName":"Kid","lastName":"Guest","bornOn":"2010-01-01","gender":"f","title":"ms"}]}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.BookHandler(w, r)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "does not match any searched child age") {
		t.Fatalf("inconsistent child date of birth was not rejected: HTTP %d, %s", w.Code, w.Body)
	}
}

func TestBookFlightBooksMultiplePassengers(t *testing.T) {
	var stored db.Booking
	childAge := time.Now().UTC().AddDate(-10, 0, 0).Format("2006-01-02")
	h, token := bookingTestHandler(t, func(r *http.Request) string {
		switch r.Method + " " + r.URL.Host + r.URL.Path {
		case "GET api.duffel.com/air/offers/offer-group":
			return `{"data":{"id":"offer-group","total_amount":"320.00","total_currency":"EUR","passengers":[{"id":"pax-1","type":"adult"},{"id":"pax-2","age":10}]}}`
		case "POST api.duffel.com/air/orders":
			var order struct {
				Data struct {
					Payments   []struct{ Amount, Currency string }
					Passengers []struct {
						ID         string `json:"id"`
						GivenName  string `json:"given_name"`
						FamilyName string `json:"family_name"`
					}
				} `json:"data"`
			}
			if err := json.NewDecoder(r.Body).Decode(&order); err != nil {
				t.Fatal(err)
			}
			if len(order.Data.Payments) != 1 || order.Data.Payments[0].Amount != "320.00" ||
				len(order.Data.Passengers) != 2 ||
				order.Data.Passengers[0].ID != "pax-1" || order.Data.Passengers[0].GivenName != "Adult" ||
				order.Data.Passengers[1].ID != "pax-2" || order.Data.Passengers[1].GivenName != "Kid" {
				t.Fatalf("flight order passengers not mapped by offer order: %+v", order.Data)
			}
			return `{"data":{"id":"order-group","booking_reference":"PNR999"}}`
		case "POST supabase.test/rest/v1/bookings":
			if err := json.NewDecoder(r.Body).Decode(&stored); err != nil {
				t.Fatal(err)
			}
			return `[]`
		default:
			t.Errorf("unexpected flight operation: %s %s", r.Method, r.URL)
			return `{}`
		}
	})
	t.Setenv("DUFFEL_API_TOKEN", "duffel_test_offline")
	r := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(fmt.Sprintf(`{"type":"flight","offerId":"offer-group",
		"email":"other@example.com","phone":"+49123456789","passengers":[
		{"firstName":"Adult","lastName":"Guest","bornOn":"1990-01-01","gender":"m","title":"mr"},
		{"firstName":"Kid","lastName":"Guest","bornOn":"%s","gender":"f","title":"ms"}]}`, childAge)))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.BookHandler(w, r)
	var result BookingResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || result.Status != "confirmed_sandbox" || result.Ref == "" ||
		stored.Price != 320 || stored.FirstName != "Adult" || stored.DuffelOrderID != "order-group" {
		t.Fatalf("multi-passenger flight flow failed: HTTP %d, %+v, %+v", w.Code, result, stored)
	}
}

// Two interchangeable adults is the simplest real CreateOrder exercise for a
// multi-passenger offer: no age banding is involved, just a genuine two-
// passenger order against the mocked sandbox endpoint.
func TestBookFlightBooksTwoAdults(t *testing.T) {
	var stored db.Booking
	h, token := bookingTestHandler(t, func(r *http.Request) string {
		switch r.Method + " " + r.URL.Host + r.URL.Path {
		case "GET api.duffel.com/air/offers/offer-2adults":
			return `{"data":{"id":"offer-2adults","total_amount":"400.00","total_currency":"EUR","passengers":[{"id":"pax-a","type":"adult"},{"id":"pax-b","type":"adult"}]}}`
		case "POST api.duffel.com/air/orders":
			var order struct {
				Data struct {
					Payments   []struct{ Amount, Currency string }
					Passengers []struct {
						ID         string `json:"id"`
						GivenName  string `json:"given_name"`
						FamilyName string `json:"family_name"`
					}
				} `json:"data"`
			}
			if err := json.NewDecoder(r.Body).Decode(&order); err != nil {
				t.Fatal(err)
			}
			if len(order.Data.Payments) != 1 || order.Data.Payments[0].Amount != "400.00" ||
				len(order.Data.Passengers) != 2 ||
				order.Data.Passengers[0].ID != "pax-a" || order.Data.Passengers[0].GivenName != "First" ||
				order.Data.Passengers[1].ID != "pax-b" || order.Data.Passengers[1].GivenName != "Second" {
				t.Fatalf("two-adult order passengers not mapped correctly: %+v", order.Data)
			}
			return `{"data":{"id":"order-2adults","booking_reference":"PNR2A"}}`
		case "POST supabase.test/rest/v1/bookings":
			if err := json.NewDecoder(r.Body).Decode(&stored); err != nil {
				t.Fatal(err)
			}
			return `[]`
		default:
			t.Errorf("unexpected flight operation: %s %s", r.Method, r.URL)
			return `{}`
		}
	})
	t.Setenv("DUFFEL_API_TOKEN", "duffel_test_offline")
	r := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(`{"type":"flight","offerId":"offer-2adults",
		"email":"other@example.com","phone":"+49123456789","passengers":[
		{"firstName":"First","lastName":"Guest","bornOn":"1988-05-01","gender":"m","title":"mr"},
		{"firstName":"Second","lastName":"Guest","bornOn":"1992-09-09","gender":"f","title":"ms"}]}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.BookHandler(w, r)
	var result BookingResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || result.Status != "confirmed_sandbox" || result.Ref == "" ||
		stored.DuffelOrderID != "order-2adults" {
		t.Fatalf("two-adult flight flow failed: HTTP %d, %+v, %+v", w.Code, result, stored)
	}
}

// Duffel's own documented contract is that offer.passengers mirrors the
// search request's passenger order 1:1. This test does not rely on that
// holding: it feeds back a GetOffer response with the child slot listed
// BEFORE the adult slot (the reverse of how the search request built them),
// and asserts the correct ID still lands on the correct name. If
// bookFlightViaDuffel ever regresses to pairing passengers by raw array
// index again, this test must fail.
func TestBookFlightDoesNotTrustOfferPassengerArrayOrder(t *testing.T) {
	childBornOn := time.Now().UTC().AddDate(-9, 0, 0).Format("2006-01-02")
	var orderedIDs []string
	h, token := bookingTestHandler(t, func(r *http.Request) string {
		switch r.Method + " " + r.URL.Host + r.URL.Path {
		case "GET api.duffel.com/air/offers/offer-reversed":
			// Child slot first, adult slot second: the opposite of the
			// adults-then-children order the search request used.
			return `{"data":{"id":"offer-reversed","total_amount":"250.00","total_currency":"EUR","passengers":[{"id":"pax-child","age":9},{"id":"pax-adult","type":"adult"}]}}`
		case "POST api.duffel.com/air/orders":
			var order struct {
				Data struct {
					Passengers []struct {
						ID        string `json:"id"`
						GivenName string `json:"given_name"`
					}
				} `json:"data"`
			}
			if err := json.NewDecoder(r.Body).Decode(&order); err != nil {
				t.Fatal(err)
			}
			for _, p := range order.Data.Passengers {
				orderedIDs = append(orderedIDs, p.ID+":"+p.GivenName)
			}
			return `{"data":{"id":"order-reversed","booking_reference":"PNRREV"}}`
		case "POST supabase.test/rest/v1/bookings":
			return `[]`
		default:
			t.Errorf("unexpected flight operation: %s %s", r.Method, r.URL)
			return `{}`
		}
	})
	t.Setenv("DUFFEL_API_TOKEN", "duffel_test_offline")
	// Submitted in the UI's usual adult-then-child order.
	r := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(fmt.Sprintf(`{"type":"flight","offerId":"offer-reversed",
		"email":"other@example.com","phone":"+49123456789","passengers":[
		{"firstName":"Adult","lastName":"Guest","bornOn":"1990-01-01","gender":"m","title":"mr"},
		{"firstName":"Kid","lastName":"Guest","bornOn":"%s","gender":"f","title":"ms"}]}`, childBornOn)))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.BookHandler(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("reversed-order offer was rejected: HTTP %d, %s", w.Code, w.Body)
	}
	want := []string{"pax-adult:Adult", "pax-child:Kid"}
	if !reflect.DeepEqual(orderedIDs, want) {
		t.Fatalf("passenger matched by raw array index instead of type/age: got %v, want %v", orderedIDs, want)
	}
}

// Age must be assessed as of the flight's own departure date (from Duffel's
// GetOffer response), not the booking date: these passengers were not yet
// born as of the real calendar date this test runs on, so a correct result
// is only reachable by using the offer's departure date as the age
// reference, never time.Now().
func TestBookFlightValidatesChildAgeAgainstDepartureNotBookingDate(t *testing.T) {
	h, token := bookingTestHandler(t, func(r *http.Request) string {
		switch r.Method + " " + r.URL.Host + r.URL.Path {
		case "GET api.duffel.com/air/offers/offer-future":
			return `{"data":{"id":"offer-future","total_amount":"500.00","total_currency":"EUR",
				"slices":[{"segments":[{"departing_at":"2099-06-20T10:00:00Z"}]}],
				"passengers":[{"id":"pax-1","type":"adult"},{"id":"pax-2","age":10}]}}`
		case "POST api.duffel.com/air/orders":
			return `{"data":{"id":"order-future","booking_reference":"PNRFUT"}}`
		case "POST supabase.test/rest/v1/bookings":
			return `[]`
		default:
			t.Errorf("unexpected flight operation: %s %s", r.Method, r.URL)
			return `{}`
		}
	})
	t.Setenv("DUFFEL_API_TOKEN", "duffel_test_offline")
	r := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(`{"type":"flight","offerId":"offer-future",
		"email":"other@example.com","phone":"+49123456789","passengers":[
		{"firstName":"Adult","lastName":"Guest","bornOn":"2075-01-01","gender":"m","title":"mr"},
		{"firstName":"Kid","lastName":"Guest","bornOn":"2089-06-10","gender":"f","title":"ms"}]}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.BookHandler(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("passenger ages valid as of departure were rejected: HTTP %d, %s", w.Code, w.Body)
	}
}

// Characterizes a real gap, not a guarantee: bookFlightViaDuffel sends no
// Idempotency-Key to Duffel and keeps no pending-order record keyed by
// offerId. If a client retries the same /book request after a timeout
// (ErrTimeout, "outcome unknown"), nothing here stops a second real sandbox
// order from being created. This test documents that double booking is
// currently possible; it is not a protection to preserve.
// After a timeout, the lock stays at "unknown" and the handler never
// retries internally within the same request (asserted by the first
// attempt's result below). A later client resubmission for the exact same
// offer is the one case allowed to call CreateOrder again, and it must
// reuse the lock row's own ID as the Idempotency-Key both times: this is
// what makes that resend a reconciliation of the original attempt rather
// than an uncoordinated second order. Whether Duffel's sandbox actually
// honours that header cannot be checked here (no live network access in
// this environment); what this test does verify, against this repo's own
// code, is that the application sends the same key both times and keeps
// exactly one lock row for the offer throughout.
// After a timeout, the lock stays at "unknown" and the handler never
// retries CreateOrder internally (asserted by the first attempt's result
// below) or on a later resubmission (asserted by orderCalls staying at 1
// throughout). The resubmission's only path to "confirmed_sandbox" is the
// read-only GET /air/orders?offer_id=... lookup finding exactly one
// matching order and the local bookings-table write succeeding -- Duffel is
// never asked to create anything a second time. Whether Duffel's real API
// actually accepts an offer_id filter on that endpoint is NOT verified
// anywhere in this test or the code it exercises (no live network access
// in this environment); this test only verifies this repo's own behavior
// against the filter it assumes.
func TestBookFlightRetryAfterTimeoutReconcilesViaReadOnlyOrderLookup(t *testing.T) {
	orderCalls, listCalls := 0, 0
	h, token := bookingTestHandlerErr(t, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Host == "api.duffel.com" && r.URL.Path == "/air/offers/offer-retry":
			body := `{"data":{"id":"offer-retry","total_amount":"150.00","total_currency":"EUR","passengers":[{"id":"pax-1"}]}}`
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		case r.Method == http.MethodPost && r.URL.Host == "api.duffel.com" && r.URL.Path == "/air/orders":
			orderCalls++
			return nil, bookingTimeoutErr{}
		case r.Method == http.MethodGet && r.URL.Host == "api.duffel.com" && r.URL.Path == "/air/orders":
			listCalls++
			if r.URL.Query().Get("offer_id") != "offer-retry" {
				t.Fatalf("reconciliation lookup missing expected offer_id filter: %s", r.URL)
			}
			body := `{"data":[{"id":"order-retry","booking_reference":"PNRRETRY","total_amount":"150.00","total_currency":"EUR"}]}`
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		case r.Method == http.MethodPost && r.URL.Host == "supabase.test" && r.URL.Path == "/rest/v1/bookings":
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`[]`))}, nil
		default:
			t.Errorf("unexpected flight operation: %s %s", r.Method, r.URL)
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
	})
	t.Setenv("DUFFEL_API_TOKEN", "duffel_test_offline")
	body := `{"type":"flight","offerId":"offer-retry","firstName":"Test","lastName":"Guest","email":"other@example.com","phone":"+49123456789","bornOn":"1990-01-01","gender":"m","title":"mr"}`

	r1 := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(body))
	r1.Header.Set("Authorization", "Bearer "+token)
	w1 := httptest.NewRecorder()
	h.BookHandler(w1, r1)
	var result1 BookingResponse
	json.Unmarshal(w1.Body.Bytes(), &result1)
	if result1.Status != "unknown" || orderCalls != 1 {
		t.Fatalf("expected the first, timed-out attempt to record status=unknown with no internal retry, got %+v (calls=%d)", result1, orderCalls)
	}

	// The client resubmits the identical request after the timeout, exactly
	// as a real client would on a network error.
	r2 := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(body))
	r2.Header.Set("Authorization", "Bearer "+token)
	w2 := httptest.NewRecorder()
	h.BookHandler(w2, r2)
	var result2 BookingResponse
	json.Unmarshal(w2.Body.Bytes(), &result2)
	if result2.Status != "confirmed_sandbox" || orderCalls != 1 || listCalls != 1 {
		t.Fatalf("expected the resubmission to reconcile via the read-only lookup, never CreateOrder: orderCalls=%d listCalls=%d result=%+v", orderCalls, listCalls, result2)
	}
	lock, _, err := h.FlightStore.ReserveFlight("11111111-1111-4111-8111-111111111111", "offer-retry")
	if err != nil || lock.Status != "confirmed_sandbox" || lock.BookingRef != result2.Ref {
		t.Fatalf("expected a single reconciled lock row, got %+v (err=%v)", lock, err)
	}
}

// If the read-only lookup cannot positively confirm an order -- here, it
// returns none, which this codebase treats as inconclusive rather than
// "safe to create a new order" (see ListOrdersByOfferID's doc comment) --
// the lock must move to the terminal "needs_review" state and CreateOrder
// must still never be called a second time.
func TestBookFlightUnresolvedTimeoutGoesToNeedsReviewNotANewOrder(t *testing.T) {
	orderCalls := 0
	h, token := bookingTestHandlerErr(t, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Host == "api.duffel.com" && r.URL.Path == "/air/offers/offer-ambiguous":
			body := `{"data":{"id":"offer-ambiguous","total_amount":"75.00","total_currency":"EUR","passengers":[{"id":"pax-1"}]}}`
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		case r.Method == http.MethodPost && r.URL.Host == "api.duffel.com" && r.URL.Path == "/air/orders":
			orderCalls++
			return nil, bookingTimeoutErr{}
		case r.Method == http.MethodGet && r.URL.Host == "api.duffel.com" && r.URL.Path == "/air/orders":
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[]}`))}, nil
		case r.Method == http.MethodPost && r.URL.Host == "supabase.test" && r.URL.Path == "/rest/v1/bookings":
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`[]`))}, nil
		default:
			t.Errorf("unexpected flight operation: %s %s", r.Method, r.URL)
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
	})
	t.Setenv("DUFFEL_API_TOKEN", "duffel_test_offline")
	body := `{"type":"flight","offerId":"offer-ambiguous","firstName":"Test","lastName":"Guest","email":"other@example.com","phone":"+49123456789","bornOn":"1990-01-01","gender":"m","title":"mr"}`

	r1 := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(body))
	r1.Header.Set("Authorization", "Bearer "+token)
	w1 := httptest.NewRecorder()
	h.BookHandler(w1, r1)

	r2 := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(body))
	r2.Header.Set("Authorization", "Bearer "+token)
	w2 := httptest.NewRecorder()
	h.BookHandler(w2, r2)
	var result2 BookingResponse
	json.Unmarshal(w2.Body.Bytes(), &result2)
	if w2.Code != http.StatusConflict || orderCalls != 1 || !strings.Contains(result2.Error, "could not be confirmed automatically") {
		t.Fatalf("expected needs_review without a second CreateOrder call: HTTP %d, calls=%d, result=%+v", w2.Code, orderCalls, result2)
	}

	// A third attempt for the same offer must also never call CreateOrder:
	// needs_review is terminal in this codebase.
	r3 := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(body))
	r3.Header.Set("Authorization", "Bearer "+token)
	w3 := httptest.NewRecorder()
	h.BookHandler(w3, r3)
	if w3.Code != http.StatusConflict || orderCalls != 1 {
		t.Fatalf("expected needs_review to stay terminal: HTTP %d, calls=%d", w3.Code, orderCalls)
	}
}

// A lock stuck at "ordering" past flightOrderStaleAfter (e.g. a process
// killed mid-CreateOrder, before it could ever run the deferred transition
// out of "ordering") must still be recoverable through the same read-only
// lookup, and must still never trigger a second CreateOrder call.
func TestBookFlightStaleOrderingLockReconciles(t *testing.T) {
	orderCalls, listCalls := 0, 0
	h, token := bookingTestHandlerErr(t, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Host == "api.duffel.com" && r.URL.Path == "/air/offers/offer-stale":
			body := `{"data":{"id":"offer-stale","total_amount":"310.00","total_currency":"EUR","passengers":[{"id":"pax-1"}]}}`
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		case r.Method == http.MethodPost && r.URL.Host == "api.duffel.com" && r.URL.Path == "/air/orders":
			orderCalls++
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		case r.Method == http.MethodGet && r.URL.Host == "api.duffel.com" && r.URL.Path == "/air/orders":
			listCalls++
			body := `{"data":[{"id":"order-stale","booking_reference":"PNRSTALE","total_amount":"310.00","total_currency":"EUR"}]}`
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		case r.Method == http.MethodPost && r.URL.Host == "supabase.test" && r.URL.Path == "/rest/v1/bookings":
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`[]`))}, nil
		default:
			t.Errorf("unexpected flight operation: %s %s", r.Method, r.URL)
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
	})
	t.Setenv("DUFFEL_API_TOKEN", "duffel_test_offline")
	fake := h.FlightStore.(*fakeFlightStore)
	userID := "11111111-1111-4111-8111-111111111111"
	// Simulate a lock that reached "ordering" in a prior, now-dead process.
	lock, _, err := fake.ReserveFlight(userID, "offer-stale")
	if err != nil {
		t.Fatal(err)
	}
	if _, won, err := fake.UpdateFlight(lock.ID, userID, "pending", "ordering", nil); err != nil || !won {
		t.Fatalf("setup: could not move lock to ordering: %v", err)
	}
	fake.backdateUpdatedAt(userID, "offer-stale", flightOrderStaleAfter+time.Second)

	body := `{"type":"flight","offerId":"offer-stale","firstName":"Test","lastName":"Guest","email":"other@example.com","phone":"+49123456789","bornOn":"1990-01-01","gender":"m","title":"mr"}`
	r := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.BookHandler(w, r)
	var result BookingResponse
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.Status != "confirmed_sandbox" || orderCalls != 0 || listCalls != 1 {
		t.Fatalf("expected the stale lock to reconcile via lookup only, never CreateOrder: orderCalls=%d listCalls=%d result=%+v", orderCalls, listCalls, result)
	}
}

// If the Duffel order succeeds but the local bookings-table write fails,
// the lock must land at "order_placed" (not "ordering" or "unknown"), and a
// later retry must finish the local write alone, without ever calling
// CreateOrder again.
func TestBookFlightLocalDBWriteFailureAfterSuccessfulOrderRecoversWithoutReordering(t *testing.T) {
	orderCalls, bookingWrites := 0, 0
	h, token := bookingTestHandlerErr(t, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Host == "api.duffel.com" && r.URL.Path == "/air/offers/offer-dbfail":
			body := `{"data":{"id":"offer-dbfail","total_amount":"140.00","total_currency":"EUR","passengers":[{"id":"pax-1"}]}}`
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		case r.Method == http.MethodPost && r.URL.Host == "api.duffel.com" && r.URL.Path == "/air/orders":
			orderCalls++
			body := `{"data":{"id":"order-dbfail","booking_reference":"PNRDBFAIL"}}`
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		case r.Method == http.MethodPost && r.URL.Host == "supabase.test" && r.URL.Path == "/rest/v1/bookings":
			bookingWrites++
			if bookingWrites == 1 {
				return &http.Response{StatusCode: 500, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"message":"internal error"}`))}, nil
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`[]`))}, nil
		default:
			t.Errorf("unexpected flight operation: %s %s", r.Method, r.URL)
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
	})
	t.Setenv("DUFFEL_API_TOKEN", "duffel_test_offline")
	body := `{"type":"flight","offerId":"offer-dbfail","firstName":"Test","lastName":"Guest","email":"other@example.com","phone":"+49123456789","bornOn":"1990-01-01","gender":"m","title":"mr"}`

	r1 := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(body))
	r1.Header.Set("Authorization", "Bearer "+token)
	w1 := httptest.NewRecorder()
	h.BookHandler(w1, r1)
	if w1.Code != http.StatusInternalServerError || orderCalls != 1 {
		t.Fatalf("expected the local write failure to surface as a 500 with exactly one CreateOrder call: HTTP %d, calls=%d", w1.Code, orderCalls)
	}
	fake := h.FlightStore.(*fakeFlightStore)
	lock, fresh, err := fake.ReserveFlight("11111111-1111-4111-8111-111111111111", "offer-dbfail")
	if err != nil || fresh || lock.Status != "order_placed" || lock.OrderID != "order-dbfail" {
		t.Fatalf("expected the lock to land at order_placed with the Duffel order recorded, got %+v (err=%v)", lock, err)
	}

	r2 := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(body))
	r2.Header.Set("Authorization", "Bearer "+token)
	w2 := httptest.NewRecorder()
	h.BookHandler(w2, r2)
	var result2 BookingResponse
	json.Unmarshal(w2.Body.Bytes(), &result2)
	if result2.Status != "confirmed_sandbox" || orderCalls != 1 {
		t.Fatalf("expected the retry to finish the local write without calling CreateOrder again: calls=%d, result=%+v", orderCalls, result2)
	}
}

// Two requests to book the exact same offer for the same user, arriving
// concurrently (e.g. a double-click or a client retrying before the first
// response returns), must not both reach CreateOrder: that is the actual
// double-booking risk this change closes. The mock sleeps briefly inside the
// order-creation call to widen the race window.
func TestBookFlightConcurrentRequestsForSameOfferCallCreateOrderOnce(t *testing.T) {
	var orderCalls int32
	h, token := bookingTestHandlerErr(t, func(r *http.Request) (*http.Response, error) {
		switch r.Method + " " + r.URL.Host + r.URL.Path {
		case "GET api.duffel.com/air/offers/offer-race":
			body := `{"data":{"id":"offer-race","total_amount":"220.00","total_currency":"EUR","passengers":[{"id":"pax-1"}]}}`
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		case "POST api.duffel.com/air/orders":
			atomic.AddInt32(&orderCalls, 1)
			time.Sleep(20 * time.Millisecond)
			body := `{"data":{"id":"order-race","booking_reference":"PNRRACE"}}`
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		case "POST supabase.test/rest/v1/bookings":
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`[]`))}, nil
		default:
			t.Errorf("unexpected flight operation: %s %s", r.Method, r.URL)
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
	})
	t.Setenv("DUFFEL_API_TOKEN", "duffel_test_offline")
	body := `{"type":"flight","offerId":"offer-race","firstName":"Test","lastName":"Guest","email":"other@example.com","phone":"+49123456789","bornOn":"1990-01-01","gender":"m","title":"mr"}`

	results := make([]BookingResponse, 2)
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			h.BookHandler(w, r)
			codes[i] = w.Code
			json.Unmarshal(w.Body.Bytes(), &results[i])
		}(i)
	}
	wg.Wait()

	if orderCalls != 1 {
		t.Fatalf("expected exactly one CreateOrder call across both concurrent requests, got %d", orderCalls)
	}
	confirmed, conflicts := 0, 0
	for i, res := range results {
		switch {
		case res.Status == "confirmed_sandbox":
			confirmed++
		case codes[i] == http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("unexpected concurrent result: HTTP %d, %+v", codes[i], res)
		}
	}
	if confirmed != 1 || conflicts != 1 {
		t.Fatalf("expected exactly one winner and one conflict response, got confirmed=%d conflicts=%d (%+v)", confirmed, conflicts, results)
	}
}

// A duplicate /book for an offer that already succeeded must not call
// CreateOrder again; it should be answered from the existing lock's
// confirmed state.
func TestBookFlightRepeatedRequestAfterSuccessDoesNotReorder(t *testing.T) {
	orderCalls := 0
	h, token := bookingTestHandler(t, func(r *http.Request) string {
		switch r.Method + " " + r.URL.Host + r.URL.Path {
		case "GET api.duffel.com/air/offers/offer-dup":
			return `{"data":{"id":"offer-dup","total_amount":"90.00","total_currency":"EUR","passengers":[{"id":"pax-1"}]}}`
		case "POST api.duffel.com/air/orders":
			orderCalls++
			return `{"data":{"id":"order-dup","booking_reference":"PNRDUP"}}`
		case "POST supabase.test/rest/v1/bookings":
			return `[]`
		default:
			t.Errorf("unexpected flight operation: %s %s", r.Method, r.URL)
			return `{}`
		}
	})
	t.Setenv("DUFFEL_API_TOKEN", "duffel_test_offline")
	body := `{"type":"flight","offerId":"offer-dup","firstName":"Test","lastName":"Guest","email":"other@example.com","phone":"+49123456789","bornOn":"1990-01-01","gender":"m","title":"mr"}`

	r1 := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(body))
	r1.Header.Set("Authorization", "Bearer "+token)
	w1 := httptest.NewRecorder()
	h.BookHandler(w1, r1)
	var result1 BookingResponse
	json.Unmarshal(w1.Body.Bytes(), &result1)
	if result1.Status != "confirmed_sandbox" || orderCalls != 1 {
		t.Fatalf("first booking did not succeed as expected: %+v (calls=%d)", result1, orderCalls)
	}

	r2 := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(body))
	r2.Header.Set("Authorization", "Bearer "+token)
	w2 := httptest.NewRecorder()
	h.BookHandler(w2, r2)
	var result2 BookingResponse
	json.Unmarshal(w2.Body.Bytes(), &result2)
	if orderCalls != 1 {
		t.Fatalf("duplicate request called CreateOrder again: calls=%d", orderCalls)
	}
	if result2.Status != "confirmed_sandbox" || result2.Ref != result1.Ref {
		t.Fatalf("duplicate request did not return the original booking: first=%+v, second=%+v", result1, result2)
	}
}

func TestBookFlightStillUsesSandboxProviderPrice(t *testing.T) {
	var stored db.Booking
	operations := []string{}
	h, token := bookingTestHandler(t, func(r *http.Request) string {
		operations = append(operations, r.Method+" "+r.URL.Host+r.URL.Path)
		switch r.Method + " " + r.URL.Host + r.URL.Path {
		case "GET api.duffel.com/air/offers/offer-test":
			return `{"data":{"id":"offer-test","total_amount":"178.89","total_currency":"EUR","passengers":[{"id":"pax-test"}]}}`
		case "POST api.duffel.com/air/orders":
			var order struct {
				Data struct {
					Payments   []struct{ Amount, Currency string }
					Passengers []struct{ Email string }
				} `json:"data"`
			}
			if err := json.NewDecoder(r.Body).Decode(&order); err != nil {
				t.Fatal(err)
			}
			if len(order.Data.Payments) != 1 || order.Data.Payments[0].Amount != "178.89" || order.Data.Payments[0].Currency != "EUR" ||
				len(order.Data.Passengers) != 1 || order.Data.Passengers[0].Email != "guest@example.com" {
				t.Fatalf("flight payment or authenticated passenger changed: %+v", order.Data)
			}
			return `{"data":{"id":"order-test","booking_reference":"PNR123"}}`
		case "POST supabase.test/rest/v1/bookings":
			if err := json.NewDecoder(r.Body).Decode(&stored); err != nil {
				t.Fatal(err)
			}
			return `[]`
		default:
			t.Errorf("unexpected flight operation: %s %s", r.Method, r.URL)
			return `{}`
		}
	})
	t.Setenv("DUFFEL_API_TOKEN", "duffel_test_offline")
	t.Setenv("B2B_WEBHOOK_ENABLED", "true")
	r := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(
		`{"type":"flight","offerId":"offer-test","firstName":"Test","lastName":"Guest","email":"other@example.com","phone":"+49123456789","bornOn":"1990-01-01","gender":"m","title":"mr","price":0.01,"currency":"USD"}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.BookHandler(w, r)
	var result BookingResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || result.Status != "confirmed_sandbox" || result.Ref == "" ||
		stored.Price != 178.89 || stored.Currency != "EUR" || stored.Email != "guest@example.com" ||
		stored.DuffelOrderID != "order-test" || stored.BookingReference != "PNR123" || stored.Status != "confirmed_sandbox" {
		t.Fatalf("sandbox flight flow changed: HTTP %d, %+v, %+v", w.Code, result, stored)
	}
	want := []string{"GET api.duffel.com/air/offers/offer-test", "POST api.duffel.com/air/orders", "POST supabase.test/rest/v1/bookings"}
	if !reflect.DeepEqual(operations, want) {
		t.Fatalf("flight operations changed: %v", operations)
	}
}

// Live-verified against the real Duffel sandbox: a phone number that passes
// our own e164Phone check (the app's previous UI example, +491234567) is
// still rejected by Duffel's own validation with exactly this error code.
func TestBookFlightSurfacesInvalidPhoneNumberSafely(t *testing.T) {
	h, token := bookingTestHandler(t, func(r *http.Request) string {
		switch r.Method + " " + r.URL.Host + r.URL.Path {
		case "GET api.duffel.com/air/offers/offer-test":
			return `{"data":{"id":"offer-test","total_amount":"63.92","total_currency":"EUR","passengers":[{"id":"pax-test"}]}}`
		case "POST api.duffel.com/air/orders":
			return `{"errors":[{"code":"invalid_phone_number","type":"validation_error"}]}`
		default:
			t.Errorf("unexpected flight operation: %s %s", r.Method, r.URL)
			return `{}`
		}
	})
	t.Setenv("DUFFEL_API_TOKEN", "duffel_test_offline")
	r := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(
		`{"type":"flight","offerId":"offer-test","firstName":"Test","lastName":"Guest","email":"other@example.com","phone":"+49123456","bornOn":"1990-01-01","gender":"m","title":"mr","price":0.01,"currency":"USD"}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.BookHandler(w, r)
	var result BookingResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusBadGateway || result.Ref != "" ||
		!strings.Contains(result.Error, "Telefon numarası geçersiz") || !strings.Contains(result.Error, phoneExample) {
		t.Fatalf("expected a specific, safe phone-number error, got HTTP %d: %+v", w.Code, result)
	}
}

// A different, unverified Duffel validation code must not be mapped to the
// phone-specific message: only the one live-proven case is special-cased.
func TestBookFlightKeepsGenericMessageForOtherOrderErrors(t *testing.T) {
	h, token := bookingTestHandler(t, func(r *http.Request) string {
		switch r.Method + " " + r.URL.Host + r.URL.Path {
		case "GET api.duffel.com/air/offers/offer-test":
			return `{"data":{"id":"offer-test","total_amount":"63.92","total_currency":"EUR","passengers":[{"id":"pax-test"}]}}`
		case "POST api.duffel.com/air/orders":
			return `{"errors":[{"code":"availability_change","type":"validation_error"}]}`
		default:
			t.Errorf("unexpected flight operation: %s %s", r.Method, r.URL)
			return `{}`
		}
	})
	t.Setenv("DUFFEL_API_TOKEN", "duffel_test_offline")
	r := httptest.NewRequest(http.MethodPost, "/book", strings.NewReader(
		`{"type":"flight","offerId":"offer-test","firstName":"Test","lastName":"Guest","email":"other@example.com","phone":"+4915112345678","bornOn":"1990-01-01","gender":"m","title":"mr","price":0.01,"currency":"USD"}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.BookHandler(w, r)
	var result BookingResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusBadGateway || result.Error != "Rezervasyon tamamlanamadı, tekrar deneyin" {
		t.Fatalf("expected the unchanged generic message for an unmapped error, got HTTP %d: %+v", w.Code, result)
	}
}

func TestB2BWebhookPayloadUnchanged(t *testing.T) {
	for _, kind := range []string{"hotel", "flight"} {
		t.Run(kind, func(t *testing.T) {
			var payload map[string]any
			previous := http.DefaultTransport
			http.DefaultTransport = bookingTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodPost || r.URL.String() != "https://b2b.test/webhooks/travaliz" {
					t.Fatalf("webhook destination changed: %s %s", r.Method, r.URL)
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = previous })
			t.Setenv("B2B_WEBHOOK_URL", "https://b2b.test")
			t.Setenv("B2B_WEBHOOK_SECRET", "test-secret")
			booking := &db.Booking{Type: kind, FirstName: "Test", LastName: "Guest", Email: "guest@example.com", Phone: "+49123456789",
				HotelName: "Verified Hotel", ToCode: "BER", FromCode: "LHR", Airline: "Test Air", Price: 178.89,
				Checkin: "2099-01-01", Checkout: "2099-01-02", DepartTime: "2099-01-01T10:00:00", ArriveTime: "2099-01-01T12:00:00"}
			sendB2BWebhook(booking, "TM-TEST")
			want := map[string]any{
				"secret": "test-secret", "externalOrderId": "TM-TEST",
				"customer": map[string]any{"name": "Test Guest", "email": "guest@example.com", "phone": "+49123456789"},
			}
			if kind == "hotel" {
				want["hotel"] = map[string]any{"hotelName": "Verified Hotel", "city": "BER", "checkIn": "2099-01-01", "checkOut": "2099-01-02", "totalPrice": 178.89}
			} else {
				want["flight"] = map[string]any{"departureAirport": "LHR", "arrivalAirport": "BER", "airline": "Test Air", "flightNumber": "Test Air",
					"departureDate": booking.DepartTime, "arrivalDate": booking.ArriveTime, "price": 178.89, "flightClass": "economy", "passengerCount": float64(1)}
			}
			if !reflect.DeepEqual(payload, want) {
				t.Fatalf("webhook payload changed: %#v", payload)
			}
		})
	}
}

func TestPlaceholderBookingNeedsNoCard(t *testing.T) {
	req := BookingRequest{
		Type: "hotel", FirstName: "Test", LastName: "Guest",
		Email: "guest@example.com", Phone: "+49123456789",
		Price: 1234.56, Currency: "USD",
	}
	if err := validateBooking(&req); err != nil {
		t.Fatalf("cardless request rejected: %v", err)
	}
	req.Price = 0
	if err := validateBooking(&req); err == nil {
		t.Fatal("invalid estimate accepted")
	}
}

func TestLast4(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", ""}, {"abc", ""}, {"123", ""},
		{"4111111111111234", "1234"},
		{"4111 1111 1111 1234", "1234"},
	} {
		if got := last4(tc.input); got != tc.want {
			t.Errorf("last4 returned %q; want %q", got, tc.want)
		}
	}
}
