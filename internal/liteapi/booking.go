package liteapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const bookBase = "https://book.liteapi.travel/v3.0"

var ErrSandboxDisabled = errors.New("hotel sandbox booking is disabled")

func (c *Client) SandboxBookingEnabled() bool {
	return c != nil && os.Getenv("LITEAPI_SANDBOX_BOOKING_ENABLED") == "true" &&
		(strings.HasPrefix(c.key, "sand_") || strings.HasPrefix(c.key, "sandbox_"))
}

type CancellationPolicy struct {
	RefundableTag string `json:"refundableTag"`
	Rules         []struct {
		CancelTime string  `json:"cancelTime"`
		Amount     float64 `json:"amount"`
		Currency   string  `json:"currency"`
		Type       string  `json:"type"`
		Timezone   string  `json:"timezone"`
	} `json:"cancelPolicyInfos"`
	HotelRemarks []string `json:"hotelRemarks"`
}

type Fee struct {
	Included    bool    `json:"included"`
	Description string  `json:"description"`
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
}

type Prebook struct {
	ID           string   `json:"prebookId"`
	OfferID      string   `json:"offerId"`
	HotelID      string   `json:"hotelId"`
	Checkin      string   `json:"checkin"`
	Checkout     string   `json:"checkout"`
	Currency     string   `json:"currency"`
	Price        float64  `json:"price"`
	Terms        string   `json:"termsAndConditions"`
	PaymentTypes []string `json:"paymentTypes"`
	Rooms        []struct {
		Rates []struct {
			Name            string             `json:"name"`
			OccupancyNumber int                `json:"occupancyNumber"`
			Adults          int                `json:"adultCount"`
			Children        int                `json:"childCount"`
			ChildrenAges    []int              `json:"childrenAges"`
			Cancellation    CancellationPolicy `json:"cancellationPolicies"`
			RetailRate      struct {
				Fees []Fee `json:"taxesAndFees"`
			} `json:"retailRate"`
		} `json:"rates"`
	} `json:"roomTypes"`
}

// Quote contains only provider-verified display data, never credentials or the
// opaque offer token. Its digest binds confirmation to the displayed terms.
type Quote struct {
	HotelID      string             `json:"hotelId"`
	HotelName    string             `json:"hotelName"`
	RoomName     string             `json:"roomName"`
	Checkin      string             `json:"checkin"`
	Checkout     string             `json:"checkout"`
	Currency     string             `json:"currency"`
	Price        float64            `json:"price"`
	Adults       int                `json:"adults"`
	Children     int                `json:"children"`
	Cancellation CancellationPolicy `json:"cancellation"`
	Fees         []Fee              `json:"fees"`
	Terms        string             `json:"terms"`
}

func (q Quote) Digest() string {
	b, _ := json.Marshal(q)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (p Prebook) Quote(hotelName string) (Quote, error) {
	start, err := time.Parse("2006-01-02", p.Checkin)
	end, endErr := time.Parse("2006-01-02", p.Checkout)
	if p.ID == "" || p.HotelID == "" || hotelName == "" || err != nil || endErr != nil || !end.After(start) || p.Checkin < time.Now().Format("2006-01-02") ||
		p.Price <= 0 || math.IsInf(p.Price, 0) || math.IsNaN(p.Price) ||
		!(p.Currency == "EUR" || p.Currency == "USD" || p.Currency == "GBP" || p.Currency == "TRY") || len(p.Rooms) != 1 || len(p.Rooms[0].Rates) != 1 {
		return Quote{}, errors.New("invalid provider quote")
	}
	r := p.Rooms[0].Rates[0]
	// OccupancyNumber is not checked: it is an index to disambiguate rates
	// across multiple rooms/occupancies, moot since exactly one room and one
	// rate are already required above. Verified live: GET /prebooks/{id}
	// consistently returns 0 here regardless of the original POST value,
	// while every other field below round-trips correctly.
	if r.Adults < 1 || r.Adults > 9 || r.Children < 0 || r.Children > 6 ||
		len(r.ChildrenAges) != r.Children || r.Cancellation.RefundableTag == "" {
		return Quote{}, errors.New("incomplete occupancy or cancellation policy")
	}
	for _, age := range r.ChildrenAges {
		if age < 0 || age > 17 {
			return Quote{}, errors.New("invalid child age")
		}
	}
	allowed := false
	for _, method := range p.PaymentTypes {
		allowed = allowed || method == "ACC_CREDIT_CARD"
	}
	if !allowed {
		return Quote{}, errors.New("sandbox payment method unavailable")
	}
	return Quote{p.HotelID, hotelName, r.Name, p.Checkin, p.Checkout, p.Currency, p.Price,
		r.Adults, r.Children, r.Cancellation, r.RetailRate.Fees, p.Terms}, nil
}

type Guest struct {
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Email     string `json:"email"`
}

type HotelBooking struct {
	ID                    string      `json:"bookingId"`
	PrebookID             string      `json:"prebookId"`
	ClientReference       string      `json:"clientReference"`
	HotelID               string      `json:"hotelId"`
	Price                 float64     `json:"price"`
	Currency              string      `json:"currency"`
	Status                string      `json:"status"`
	HotelConfirmationCode string      `json:"hotelConfirmationCode"`
	Sandbox               sandboxFlag `json:"sandbox"`
}

// Book uses a boolean marker; booking retrieval uses integer 0/1.
type sandboxFlag bool

func (s *sandboxFlag) UnmarshalJSON(b []byte) error {
	switch string(b) {
	case "true", "1":
		*s = true
	case "false", "0":
		*s = false
	default:
		return errors.New("invalid sandbox marker")
	}
	return nil
}

func (c *Client) Prebook(offerID string) (Prebook, error) {
	var out struct {
		Data    Prebook `json:"data"`
		Sandbox bool    `json:"sandbox"`
	}
	err := c.sandboxRequest(http.MethodPost, "/rates/prebook", map[string]any{"offerId": offerID, "usePaymentSdk": false}, &out)
	if err == nil && (!out.Sandbox || out.Data.OfferID != offerID) {
		err = errors.New("provider did not confirm the sandbox offer")
	}
	return out.Data, err
}

func (c *Client) GetPrebook(id string) (Prebook, error) {
	var out struct {
		Data    Prebook `json:"data"`
		Sandbox *bool   `json:"sandbox"`
	}
	err := c.sandboxRequest(http.MethodGet, "/prebooks/"+url.PathEscape(id), nil, &out)
	// This GET omits the sandbox field in current live responses. The stored
	// prebook was created with an explicit sandbox confirmation; the key and
	// ID must still match, and an explicit production marker is rejected.
	if err == nil && ((out.Sandbox != nil && !*out.Sandbox) || out.Data.ID != id) {
		err = errors.New("provider did not confirm the sandbox prebook")
	}
	return out.Data, err
}

func (c *Client) HotelName(id string) (string, error) {
	if !c.SandboxBookingEnabled() {
		return "", ErrSandboxDisabled
	}
	var out struct {
		Data Hotel `json:"data"`
	}
	err := c.do(http.MethodGet, "/data/hotel?hotelId="+url.QueryEscape(id), nil, &out)
	if err != nil {
		return "", err
	}
	if out.Data.ID != id || out.Data.Name == "" {
		return "", errors.New("invalid hotel content")
	}
	return out.Data.Name, nil
}

func (c *Client) Book(prebookID, reference string, guest Guest) (HotelBooking, error) {
	var out struct {
		Data    HotelBooking `json:"data"`
		Sandbox bool         `json:"sandbox"`
	}
	// Only the first occupant of our single room is the lead guest. Neither the
	// caller nor environment variables can choose another payment method.
	payload := map[string]any{"prebookId": prebookID, "clientReference": reference, "holder": guest,
		"guests":  []map[string]any{{"occupancyNumber": 1, "firstName": guest.FirstName, "lastName": guest.LastName, "email": guest.Email}},
		"payment": map[string]string{"method": "ACC_CREDIT_CARD"}, "customTags": map[string]string{"TRAVALIZ_CHECKOUT": reference}}
	err := c.sandboxRequest(http.MethodPost, "/rates/book", payload, &out)
	if err == nil && (!out.Sandbox || out.Data.ID == "" || !strings.EqualFold(out.Data.Status, "confirmed")) {
		err = errors.New("sandbox confirmation is incomplete")
	}
	if err == nil {
		out.Data.Sandbox = true
	}
	return out.Data, err
}

// Recovery is a GET only. Even 409/503/transport failures never cause another
// book request; a missing result remains unknown instead of releasing the lock.
func (c *Client) FindBooking(reference, prebookID string, quote Quote) (HotelBooking, bool, error) {
	var out struct {
		Data []HotelBooking `json:"data"`
	}
	// The live API requires a date pair, even though the reference overview
	// describes dates as optional. This contract was verified in sandbox.
	path := "/bookings/?sandbox=1&startDate=" + url.QueryEscape(quote.Checkin) + "&endDate=" + url.QueryEscape(quote.Checkout) + "&customTags=" + url.QueryEscape("TRAVALIZ_CHECKOUT:"+reference)
	if err := c.sandboxRequest(http.MethodGet, path, nil, &out); err != nil {
		return HotelBooking{}, false, err
	}
	for _, b := range out.Data {
		if bool(b.Sandbox) && b.ID != "" && b.PrebookID == prebookID && b.ClientReference == reference &&
			b.Price == quote.Price && b.Currency == quote.Currency && (b.HotelID == "" || b.HotelID == quote.HotelID) && strings.EqualFold(b.Status, "confirmed") {
			return b, true, nil
		}
	}
	return HotelBooking{}, false, nil
}

func (c *Client) sandboxRequest(method, path string, payload, out any) error {
	if !c.SandboxBookingEnabled() {
		return ErrSandboxDisabled
	}
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return errors.New("invalid booking payload")
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, bookBase+path, body)
	if err != nil {
		return errors.New("invalid booking request")
	}
	req.Header.Set("X-API-Key", c.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return errors.New("booking transport failed; outcome may be unknown")
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(b) > maxResponseBytes {
		return errors.New("incomplete booking response; outcome may be unknown")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("booking API HTTP %d; outcome may be unknown", resp.StatusCode)
	}
	if json.Unmarshal(b, out) != nil {
		return errors.New("invalid booking response; outcome may be unknown")
	}
	return nil
}
