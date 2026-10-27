package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"travel-proxy-service/internal/db"
	"travel-proxy-service/internal/liteapi"
)

type hotelMemoryStore struct {
	mu         sync.Mutex
	rows       map[string]*db.HotelCheckout
	failClaim  bool
	failFinish bool
}

func copyHotel(row *db.HotelCheckout) *db.HotelCheckout {
	b, _ := json.Marshal(row)
	var clone db.HotelCheckout
	json.Unmarshal(b, &clone)
	return &clone
}
func (s *hotelMemoryStore) ReserveHotel(user, hash string) (*db.HotelCheckout, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rows {
		if r.UserID == user && r.OfferHash == hash {
			return copyHotel(r), false, nil
		}
	}
	r := &db.HotelCheckout{ID: uuid.NewString(), UserID: user, OfferHash: hash, Status: "prebooking", CreatedAt: time.Now().UTC()}
	s.rows[r.ID] = r
	return copyHotel(r), true, nil
}
func (s *hotelMemoryStore) GetHotel(id, user string) (*db.HotelCheckout, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rows[id]
	if r == nil || r.UserID != user {
		return nil, nil
	}
	return copyHotel(r), nil
}
func (s *hotelMemoryStore) UpdateHotel(id, user, from, to string, fields map[string]any) (*db.HotelCheckout, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rows[id]
	if r == nil || r.UserID != user || r.Status != from {
		return nil, false, nil
	}
	if s.failFinish && to == "confirmed_sandbox" {
		return nil, false, errors.New("simulated final storage failure")
	}
	r.Status = to
	if v, ok := fields["quote"]; ok {
		q := v.(liteapi.Quote)
		r.Quote = &q
	}
	if v, ok := fields["prebook_id"]; ok {
		r.PrebookID = v.(string)
	}
	if v, ok := fields["guest"]; ok {
		g := v.(liteapi.Guest)
		r.Guest = &g
	}
	if v, ok := fields["booking_id"]; ok {
		r.BookingID = v.(string)
	}
	if v, ok := fields["hotel_confirmation_code"]; ok {
		r.HotelConfirmationCode = v.(string)
	}
	if s.failClaim && to == "booking" {
		return nil, false, errors.New("simulated lost CAS response after commit")
	}
	return copyHotel(r), true, nil
}
func (s *hotelMemoryStore) ListHotels(user string) ([]db.HotelCheckout, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []db.HotelCheckout
	for _, r := range s.rows {
		if r.UserID == user {
			out = append(out, *copyHotel(r))
		}
	}
	return out, nil
}

type hotelFixture struct {
	h             *TravelHandler
	token         string
	store         *hotelMemoryStore
	prebooks      atomic.Int32
	books         atomic.Int32
	lookups       atomic.Int32
	timeout       bool
	recover       bool
	priceChanged  bool
	lastReference string
}

func hotelTest(t *testing.T) *hotelFixture {
	t.Helper()
	t.Setenv("LITEAPI_API_KEY", "sand_offline")
	t.Setenv("LITEAPI_SANDBOX_BOOKING_ENABLED", "true")
	h, token := bookingTestHandler(t, func(r *http.Request) string { t.Errorf("unexpected real HTTP request: %s", r.URL.Host); return `{}` })
	f := &hotelFixture{h: h, token: token, store: &hotelMemoryStore{rows: map[string]*db.HotelCheckout{}}}
	h.HotelStore = f.store
	client := liteapi.NewClient()
	h.HotelClient = client
	client.HTTPClient.Transport = bookingTransport(func(r *http.Request) (*http.Response, error) {
		body := ""
		price := 100
		if f.priceChanged && r.Method == "GET" {
			price = 101
		}
		prebook := map[string]any{"prebookId": "prebook-test", "offerId": "offer-test", "hotelId": "hotel-test", "checkin": "2099-01-01", "checkout": "2099-01-02", "currency": "EUR", "price": price, "termsAndConditions": "Provider terms", "paymentTypes": []string{"ACC_CREDIT_CARD"},
			"roomTypes": []any{map[string]any{"rates": []any{map[string]any{"name": "Room", "occupancyNumber": 1, "adultCount": 2, "childCount": 0, "cancellationPolicies": map[string]any{"refundableTag": "NRFN"}}}}}}
		switch r.Method + " " + r.URL.Path {
		case "POST /v3.0/rates/prebook":
			f.prebooks.Add(1)
			var p map[string]any
			json.NewDecoder(r.Body).Decode(&p)
			if p["offerId"] != "offer-test" || p["usePaymentSdk"] != false {
				t.Errorf("unsafe prebook payload: %+v", p)
			}
			b, _ := json.Marshal(map[string]any{"data": prebook, "sandbox": true})
			body = string(b)
		case "GET /v3.0/prebooks/prebook-test":
			b, _ := json.Marshal(map[string]any{"data": prebook})
			body = string(b)
		case "GET /v3.0/data/hotel":
			body = `{"data":{"id":"hotel-test","name":"Provider Hotel"}}`
		case "POST /v3.0/rates/book":
			f.books.Add(1)
			var p map[string]any
			json.NewDecoder(r.Body).Decode(&p)
			f.lastReference = p["clientReference"].(string)
			if p["payment"].(map[string]any)["method"] != "ACC_CREDIT_CARD" || p["prebookId"] != "prebook-test" || p["price"] != nil || p["holder"].(map[string]any)["email"] != "guest@example.com" {
				t.Errorf("unsafe book payload: %+v", p)
			}
			if f.timeout {
				return nil, errors.New("simulated response loss after provider accepted booking")
			}
			b, _ := json.Marshal(map[string]any{"data": map[string]any{"bookingId": "booking-test", "status": "CONFIRMED", "prebookId": "prebook-test", "clientReference": f.lastReference, "hotelId": "hotel-test", "hotelConfirmationCode": "CONF-TEST", "price": 100, "currency": "EUR"}, "sandbox": true})
			body = string(b)
		case "GET /v3.0/bookings/":
			f.lookups.Add(1)
			if r.URL.Query().Get("sandbox") != "1" || r.URL.Query().Get("startDate") != "2099-01-01" || r.URL.Query().Get("endDate") != "2099-01-02" || r.URL.Query().Get("customTags") != "TRAVALIZ_CHECKOUT:"+f.lastReference {
				t.Error("unscoped recovery query")
			}
			if f.recover {
				b, _ := json.Marshal(map[string]any{"data": []any{map[string]any{"bookingId": "booking-test", "status": "CONFIRMED", "prebookId": "prebook-test", "clientReference": f.lastReference, "sandbox": 1, "price": 100, "currency": "EUR"}}})
				body = string(b)
			} else {
				body = `{"data":[]}`
			}
		default:
			t.Errorf("unexpected hotel operation: %s %s", r.Method, r.URL.Path)
			body = `{}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	return f
}

func (f *hotelFixture) request(method, path string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, strings.NewReader(string(b)))
	r.Header.Set("Authorization", "Bearer "+f.token)
	w := httptest.NewRecorder()
	switch path {
	case "/hotel/prebook":
		f.h.HotelPrebookHandler(w, r)
	case "/book":
		f.h.BookHandler(w, r)
	default:
		f.h.HotelStatusHandler(w, r)
	}
	return w
}
func (f *hotelFixture) quote(t *testing.T) hotelResult {
	t.Helper()
	w := f.request("POST", "/hotel/prebook", map[string]any{"offerId": "offer-test", "price": 0.01, "hotelName": "FORGED"})
	if w.Code != 200 {
		t.Fatalf("prebook failed: %d %s", w.Code, w.Body.String())
	}
	var result hotelResult
	json.Unmarshal(w.Body.Bytes(), &result)
	return result
}
func bookInput(q hotelResult) map[string]any {
	return map[string]any{"type": "hotel", "hotelCheckoutId": q.CheckoutID, "quoteHash": q.QuoteHash, "acceptQuote": true, "firstName": "Test", "lastName": "Guest", "email": "forged@example.invalid", "price": 0.01, "hotelName": "FORGED", "currency": "USD"}
}

func TestHotelSandboxSuccessAndReplay(t *testing.T) {
	f := hotelTest(t)
	q := f.quote(t)
	if q.Quote.HotelName != "Provider Hotel" || q.Quote.Price != 100 || q.Quote.Cancellation.RefundableTag != "NRFN" {
		t.Fatal("client claims or incomplete terms accepted")
	}
	if f.quote(t).CheckoutID != q.CheckoutID || f.prebooks.Load() != 1 {
		t.Fatal("duplicate prebook created")
	}
	for i := 0; i < 2; i++ {
		w := f.request("POST", "/book", bookInput(q))
		var result hotelResult
		json.Unmarshal(w.Body.Bytes(), &result)
		if w.Code != 200 || result.Status != "confirmed_sandbox" || result.BookingID != "booking-test" {
			t.Fatalf("book failed: %d %s", w.Code, w.Body.String())
		}
	}
	if f.books.Load() != 1 {
		t.Fatal("duplicate provider booking")
	}
}

func TestHotelConcurrentConfirmationsOnlyBookOnce(t *testing.T) {
	f := hotelTest(t)
	q := f.quote(t)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := f.request("POST", "/book", bookInput(q))
			if w.Code != 200 && w.Code != 202 {
				t.Errorf("concurrent confirm: %d", w.Code)
			}
		}()
	}
	wg.Wait()
	if f.books.Load() != 1 {
		t.Fatalf("%d provider bookings", f.books.Load())
	}
}

func TestHotelTimeoutRecoveryNeverRebooks(t *testing.T) {
	f := hotelTest(t)
	f.timeout = true
	q := f.quote(t)
	for i := 0; i < 2; i++ {
		if w := f.request("POST", "/book", bookInput(q)); w.Code != 202 {
			t.Fatalf("uncertain outcome lost: %d %s", w.Code, w.Body.String())
		}
	}
	// A fresh handler with the same durable store simulates process replacement.
	replacement := *f.h
	f.h = &replacement
	f.recover = true
	w := f.request("GET", "/hotel/booking-status?id="+q.CheckoutID, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "confirmed_sandbox") || f.books.Load() != 1 || f.lookups.Load() != 1 {
		t.Fatalf("unsafe recovery: %d %s", w.Code, w.Body.String())
	}
}

func TestHotelAmbiguousStorageFailuresDoNotRetryBook(t *testing.T) {
	for _, phase := range []string{"claim", "finish"} {
		t.Run(phase, func(t *testing.T) {
			f := hotelTest(t)
			q := f.quote(t)
			f.store.failClaim = phase == "claim"
			f.store.failFinish = phase == "finish"
			w := f.request("POST", "/book", bookInput(q))
			if w.Code != 503 && w.Code != 202 {
				t.Fatalf("ambiguous storage failure: %d", w.Code)
			}
			f.request("POST", "/book", bookInput(q))
			want := int32(0)
			if phase == "finish" {
				want = 1
			}
			if f.books.Load() != want {
				t.Fatalf("unsafe book replay: %d", f.books.Load())
			}
		})
	}
}

func TestHotelRequiresOwnershipConsentAndUnchangedQuote(t *testing.T) {
	f := hotelTest(t)
	q := f.quote(t)
	for _, kind := range []string{"foreign", "consent", "digest", "changed"} {
		input := bookInput(q)
		switch kind {
		case "foreign":
			input["hotelCheckoutId"] = uuid.NewString()
		case "consent":
			input["acceptQuote"] = false
		case "digest":
			input["quoteHash"] = "forged"
		case "changed":
			f.priceChanged = true
		}
		w := f.request("POST", "/book", input)
		if w.Code != 404 && w.Code != 409 {
			t.Fatalf("%s accepted: %d", kind, w.Code)
		}
	}
	if f.books.Load() != 0 {
		t.Fatal("unverified booking reached provider")
	}
}

func TestHotelProductionKeyIsRejectedBeforeStorageOrHTTP(t *testing.T) {
	f := hotelTest(t)
	t.Setenv("LITEAPI_API_KEY", "prod_forbidden")
	f.h.HotelClient = liteapi.NewClient()
	w := f.request("POST", "/hotel/prebook", map[string]any{"offerId": "offer-test"})
	if w.Code != 409 || len(f.store.rows) != 0 || f.prebooks.Load() != 0 {
		t.Fatal("production booking guard bypassed")
	}
}
