// Package duffel provides a client for Duffel v2 sandbox flights.
package duffel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const apiBase = "https://api.duffel.com"
const maxResponseBytes = 8 << 20

// multiSliceOfferLimit is the single List Offers page read for a round-trip
// or multi-city search; the page shows at most 20 of these after filtering.
const multiSliceOfferLimit = 50

var ErrTimeout = errors.New("duffel: request timed out; outcome may be unknown")

// DuffelClient follows the existing proxy client pattern. It never loads .env.
// HTTPClient is exported so a transport can be substituted for offline tests.
type DuffelClient struct {
	token      string
	HTTPClient *http.Client
}

// orderCreateTimeout is set from Duffel's own documented guidance that an
// instant order can occasionally take up to ~130s to confirm; this client
// previously used 60s, which could misclassify a slow-but-successful order
// as ErrTimeout well before Duffel itself gave up, feeding the one path
// (status "unknown") that otherwise needs reconciliation. This number comes
// from Duffel's published API guidance, not a live call made from this
// session. Whether it is safe to wait this long depends on how this
// process is actually hosted: this repo's own Dockerfile/docker-compose.yml
// describe a persistent, long-running container with no http.Server
// ReadTimeout/WriteTimeout set in main.go, which has no such ceiling. If
// this service instead runs as a short-lived serverless function (e.g. a
// Vercel Function), its platform execution-time limit -- historically far
// below 130s on common plans, though this codebase has no vercel.json and
// that exact number was not checked live here -- would kill the request
// before this timeout ever fires, and before the deferred
// UpdateFlight(...,"unknown",...) below can run, leaving a lock stuck at
// "ordering". flightOrderStaleAfter in booking_handler.go is the mitigation
// for that case regardless of which of these is true in production.
const orderCreateTimeout = 135 * time.Second

func NewDuffelClient() *DuffelClient {
	return &DuffelClient{
		token: strings.TrimSpace(os.Getenv("DUFFEL_API_TOKEN")),
		HTTPClient: &http.Client{
			Timeout:       orderCreateTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

type SearchSlice struct {
	Origin        string `json:"origin"`
	Destination   string `json:"destination"`
	DepartureDate string `json:"departure_date"`
}

// Supply Type (e.g. adult) OR Age, never both. A pointer preserves age zero.
type SearchPassenger struct {
	Type string `json:"type,omitempty"`
	Age  *int   `json:"age,omitempty"`
}

type OfferPassenger struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Age  *int   `json:"age,omitempty"`
}

type Airport struct {
	IATACode string `json:"iata_code"`
	Name     string `json:"name"`
	CityName string `json:"city_name"`
	TimeZone string `json:"time_zone"`
}

type Carrier struct {
	Name          string `json:"name"`
	IATACode      string `json:"iata_code"`
	LogoSymbolURL string `json:"logo_symbol_url"`
}

type Segment struct {
	Origin                       Airport `json:"origin"`
	Destination                  Airport `json:"destination"`
	DepartingAt                  string  `json:"departing_at"`
	ArrivingAt                   string  `json:"arriving_at"`
	Duration                     string  `json:"duration"`
	MarketingCarrier             Carrier `json:"marketing_carrier"`
	OperatingCarrier             Carrier `json:"operating_carrier"`
	MarketingCarrierFlightNumber string  `json:"marketing_carrier_flight_number"`
}

type OfferSlice struct {
	Origin      Airport   `json:"origin"`
	Destination Airport   `json:"destination"`
	Duration    string    `json:"duration"`
	Segments    []Segment `json:"segments"`
}

type Offer struct {
	ID            string           `json:"id"`
	TotalAmount   string           `json:"total_amount"`
	TotalCurrency string           `json:"total_currency"`
	ExpiresAt     string           `json:"expires_at"`
	Slices        []OfferSlice     `json:"slices"`
	Passengers    []OfferPassenger `json:"passengers"`
}

type OrderPassenger struct {
	ID          string `json:"id"`
	GivenName   string `json:"given_name"`
	FamilyName  string `json:"family_name"`
	BornOn      string `json:"born_on"`
	Gender      string `json:"gender"`
	Title       string `json:"title"`
	Email       string `json:"email"`
	PhoneNumber string `json:"phone_number"`
}

type Order struct {
	ID               string `json:"id"`
	BookingReference string `json:"booking_reference"`
	// TotalAmount/TotalCurrency are decode-only and used solely to sanity-check
	// a candidate match from ListOrdersByOfferID against the offer's own
	// price before adopting it. Field names follow the same total_amount /
	// total_currency convention Offer already uses; that naming has not been
	// independently confirmed for the order resource specifically.
	TotalAmount   string `json:"total_amount,omitempty"`
	TotalCurrency string `json:"total_currency,omitempty"`
}

// APIError keeps diagnostic codes but does not expose upstream messages or PII.
type APIError struct {
	StatusCode int
	Code       string
	Type       string
}

func (e *APIError) Error() string { return fmt.Sprintf("duffel: HTTP %d", e.StatusCode) }

func (c *DuffelClient) CreateOfferRequest(slices []SearchSlice, passengers []SearchPassenger, cabinClass string) ([]Offer, error) {
	if len(slices) == 0 || len(passengers) == 0 {
		return nil, errors.New("duffel: slices and passengers are required")
	}
	payload := struct {
		Slices     []SearchSlice     `json:"slices"`
		Passengers []SearchPassenger `json:"passengers"`
		CabinClass string            `json:"cabin_class"`
	}{slices, passengers, cabinClass}
	var data struct {
		ID     string  `json:"id"`
		Offers []Offer `json:"offers"`
	}
	if len(slices) > 1 {
		// Multi-slice requests return far more offers: a sandbox LHR-JFK
		// round trip returned 1,680 offers (about 21 MB) inline, beyond
		// maxResponseBytes. Instead, create the request without offers and
		// read only the cheapest page via List Offers (sort=total_amount),
		// both live-verified against the sandbox. No pagination beyond the
		// first page and no retries.
		if err := c.do(http.MethodPost, "/air/offer_requests?return_offers=false", payload, "", &data); err != nil {
			return nil, err
		}
		if data.ID == "" {
			return nil, errors.New("duffel: offer request ID missing")
		}
		var offers []Offer
		query := url.Values{"offer_request_id": {data.ID}, "sort": {"total_amount"}, "limit": {strconv.Itoa(multiSliceOfferLimit)}}
		if err := c.do(http.MethodGet, "/air/offers?"+query.Encode(), nil, "", &offers); err != nil {
			return nil, err
		}
		if offers == nil {
			offers = []Offer{}
		}
		return offers, nil
	}
	// One request, flat offers, no retries or pagination. Supplier timeout defaults
	// to 20 seconds at Duffel, below this client's 60-second HTTP timeout.
	if err := c.do(http.MethodPost, "/air/offer_requests?return_offers=true", payload, "", &data); err != nil {
		return nil, err
	}
	if data.Offers == nil {
		data.Offers = []Offer{}
	}
	return data.Offers, nil
}

func (c *DuffelClient) GetOffer(offerID string) (Offer, error) {
	var offer Offer
	if offerID == "" {
		return offer, errors.New("duffel: offer ID is required")
	}
	err := c.do(http.MethodGet, "/air/offers/"+url.PathEscape(offerID), nil, "", &offer)
	return offer, err
}

// CreateOrder does not refresh the offer implicitly. The caller must GetOffer,
// confirm any price change, and pass its exact TotalAmount and TotalCurrency.
// No float parsing, rounding or currency conversion is performed here.
//
// idempotencyKey is sent as defense-in-depth against this exact call being
// resent at the transport level (e.g. an HTTP client/proxy retry on a
// connection reset) -- it is NOT treated as a guarantee by any caller in
// this codebase. Duffel's sandbox has not been confirmed here to honour
// this header at all (no live call was made to check), so the caller-side
// protection against a duplicate order is the caller's own durable
// concurrency lock, which only ever invokes CreateOrder once per lock
// (see bookFlightViaDuffel and flight_sandbox_checkouts); nothing in this
// codebase calls CreateOrder a second time for the same lock on the
// strength of this key alone.
func (c *DuffelClient) CreateOrder(offerID, amount, currency string, passengers []OrderPassenger, idempotencyKey string) (Order, error) {
	var order Order
	if offerID == "" || amount == "" || currency == "" || len(passengers) == 0 {
		return order, errors.New("duffel: offer, exact amount, currency and passengers are required")
	}
	type payment struct {
		Type     string `json:"type"`
		Currency string `json:"currency"`
		Amount   string `json:"amount"`
	}
	payload := struct {
		Type           string           `json:"type"`
		SelectedOffers []string         `json:"selected_offers"`
		Payments       []payment        `json:"payments"`
		Passengers     []OrderPassenger `json:"passengers"`
	}{"instant", []string{offerID}, []payment{{"balance", currency, amount}}, passengers}
	err := c.do(http.MethodPost, "/air/orders", payload, idempotencyKey, &order)
	return order, err
}

// ListOrdersByOfferID is a best-effort, read-only reconciliation lookup for
// a lock stuck at "unknown" after a timeout. It assumes Duffel's List
// Orders endpoint accepts an offer_id filter -- that assumption is NOT
// confirmed against Duffel's real API or a live sandbox call in this
// codebase; it was not possible to verify from this environment, which has
// no network access to Duffel. Treat every result from this method as
// advisory only:
//   - a non-empty, matching result is reasonable grounds to adopt that
//     order as the outcome of the original attempt;
//   - an empty result is NOT proof that no order was created (the filter
//     may silently not exist, or may be spelled differently), so it must
//     never by itself be treated as "safe to create a new order";
//   - an error from this call must be treated the same as an empty result:
//     inconclusive, not reassuring.
//
// No caller in this codebase uses this method's result to justify calling
// CreateOrder again; an inconclusive result routes to a terminal
// "needs_review" state instead (see bookFlightViaDuffel).
func (c *DuffelClient) ListOrdersByOfferID(offerID string) ([]Order, error) {
	if offerID == "" {
		return nil, errors.New("duffel: offer ID is required")
	}
	var orders []Order
	err := c.do(http.MethodGet, "/air/orders?offer_id="+url.QueryEscape(offerID), nil, "", &orders)
	return orders, err
}

func (c *DuffelClient) do(method, path string, payload any, idempotencyKey string, out any) error {
	if c.token == "" {
		return errors.New("duffel: DUFFEL_API_TOKEN is not configured")
	}
	// This client is deliberately sandbox-only: it is the only type with a
	// CreateOrder method, so a live token must go through LiveSearchClient
	// instead, which has no CreateOrder method at all.
	if !strings.HasPrefix(c.token, "duffel_test_") {
		return errors.New("duffel: a sandbox test token is required")
	}
	return doRequest(c.HTTPClient, c.token, method, path, payload, idempotencyKey, out)
}

// doRequest is the shared HTTP/envelope plumbing for both DuffelClient
// (sandbox) and LiveSearchClient (live, search-only). Token-scope decisions
// belong to each caller, not here. idempotencyKey is sent as the
// Idempotency-Key header when non-empty; see CreateOrder.
func doRequest(httpClient *http.Client, token, method, path string, payload any, idempotencyKey string, out any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(struct {
			Data any `json:"data"`
		}{payload})
		if err != nil {
			return errors.New("duffel: could not encode request")
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, apiBase+path, body)
	if err != nil {
		return errors.New("duffel: could not build request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Duffel-Version", "v2")
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			return ErrTimeout
		}
		// Transport errors may include request URLs or injected sensitive data.
		return errors.New("duffel: network request failed; outcome may be unknown")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return errors.New("duffel: could not read response; outcome may be unknown")
	}
	if len(raw) > maxResponseBytes {
		return errors.New("duffel: response exceeds size limit")
	}
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"errors"`
	}
	decodeErr := json.Unmarshal(raw, &envelope)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || len(envelope.Errors) > 0 {
		apiErr := &APIError{StatusCode: resp.StatusCode}
		if len(envelope.Errors) > 0 {
			apiErr.Code = strings.ReplaceAll(envelope.Errors[0].Code, token, "[REDACTED]")
			apiErr.Type = strings.ReplaceAll(envelope.Errors[0].Type, token, "[REDACTED]")
		}
		return apiErr
	}
	if decodeErr != nil || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return errors.New("duffel: invalid response data")
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return errors.New("duffel: could not decode response data")
	}
	return nil
}
