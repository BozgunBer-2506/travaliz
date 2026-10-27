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
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"travel-proxy-service/internal/auth"
	"travel-proxy-service/internal/db"
)

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
	return &TravelHandler{DB: database}, signed
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
