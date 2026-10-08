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
	"strings"
	"time"
)

const apiBase = "https://api.duffel.com"
const maxResponseBytes = 8 << 20

var ErrTimeout = errors.New("duffel: request timed out; outcome may be unknown")

// DuffelClient follows the existing proxy client pattern. It never loads .env.
// HTTPClient is exported so a transport can be substituted for offline tests.
type DuffelClient struct {
	token      string
	HTTPClient *http.Client
}

func NewDuffelClient() *DuffelClient {
	return &DuffelClient{
		token: strings.TrimSpace(os.Getenv("DUFFEL_API_TOKEN")),
		HTTPClient: &http.Client{
			Timeout:       60 * time.Second,
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
		Offers []Offer `json:"offers"`
	}
	// One request, flat offers, no retries or pagination. Supplier timeout defaults
	// to 20 seconds at Duffel, below this client's 60-second HTTP timeout.
	if err := c.do(http.MethodPost, "/air/offer_requests?return_offers=true", payload, &data); err != nil {
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
	err := c.do(http.MethodGet, "/air/offers/"+url.PathEscape(offerID), nil, &offer)
	return offer, err
}

// CreateOrder does not refresh the offer implicitly. The caller must GetOffer,
// confirm any price change, and pass its exact TotalAmount and TotalCurrency.
// No float parsing, rounding or currency conversion is performed here.
func (c *DuffelClient) CreateOrder(offerID, amount, currency string, passengers []OrderPassenger) (Order, error) {
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
	err := c.do(http.MethodPost, "/air/orders", payload, &order)
	return order, err
}

func (c *DuffelClient) do(method, path string, payload, out any) error {
	if c.token == "" {
		return errors.New("duffel: DUFFEL_API_TOKEN is not configured")
	}
	// This first-stage client is deliberately sandbox-only.
	if !strings.HasPrefix(c.token, "duffel_test_") {
		return errors.New("duffel: a sandbox test token is required")
	}
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
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Duffel-Version", "v2")
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTPClient.Do(req)
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
			apiErr.Code = strings.ReplaceAll(envelope.Errors[0].Code, c.token, "[REDACTED]")
			apiErr.Type = strings.ReplaceAll(envelope.Errors[0].Type, c.token, "[REDACTED]")
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
