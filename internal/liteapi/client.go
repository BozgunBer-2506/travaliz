// Package liteapi provides a client for LiteAPI hotel search and rates.
package liteapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const apiBase = "https://api.liteapi.travel/v3.0"

// Rates responses are large: ~335KB per hotel observed live (offer IDs are
// bulky opaque tokens), so this is sized well above a realistic batch.
const maxResponseBytes = 24 << 20

var ErrTimeout = errors.New("liteapi: request timed out; outcome may be unknown")

// Client follows the existing proxy/duffel client pattern. It never loads .env.
type Client struct {
	key        string
	HTTPClient *http.Client
}

func NewClient() *Client {
	return &Client{
		key: strings.TrimSpace(os.Getenv("LITEAPI_API_KEY")),
		HTTPClient: &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

type Hotel struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Stars     int     `json:"stars"`
	Rating    float64 `json:"rating"`
	MainPhoto string  `json:"main_photo"`
}

// SearchHotels lists hotels within radiusMeters of a coordinate. radius is in
// meters (confirmed against the live API: 500 returns none, 2000+ returns results).
func (c *Client) SearchHotels(lat, lng float64, radiusMeters int) ([]Hotel, error) {
	q := url.Values{
		"latitude":  {strconv.FormatFloat(lat, 'f', -1, 64)},
		"longitude": {strconv.FormatFloat(lng, 'f', -1, 64)},
		"radius":    {strconv.Itoa(radiusMeters)},
	}
	var out struct {
		Data []Hotel `json:"data"`
	}
	if err := c.do(http.MethodGet, "/data/hotels?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

type Amount struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

type Rate struct {
	RateID string `json:"rateId"`
	Name   string `json:"name"`
}

type RoomType struct {
	RoomTypeID      string `json:"roomTypeId"`
	OfferID         string `json:"offerId"`
	Rates           []Rate `json:"rates"`
	OfferRetailRate Amount `json:"offerRetailRate"`
}

type HotelRates struct {
	HotelID   string     `json:"hotelId"`
	RoomTypes []RoomType `json:"roomTypes"`
}

type Occupancy struct {
	Adults   int   `json:"adults"`
	Children []int `json:"children,omitempty"`
}

type RateQuery struct {
	Occupancies      []Occupancy `json:"occupancies"`
	Checkin          string      `json:"checkin"`
	Checkout         string      `json:"checkout"`
	Currency         string      `json:"currency"`
	GuestNationality string      `json:"guestNationality"`
}

// ISO 3166-1 alpha-2 codes; never infer nationality from the destination.
const countryCodes = " AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW "

func (q RateQuery) Validate() error {
	if len(q.Occupancies) != 1 {
		return errors.New("Hotel search supports one room only; a per-room guest distribution is required for multiple rooms.")
	}
	o := q.Occupancies[0]
	if o.Adults < 1 || o.Adults > 9 || len(o.Children) > 6 {
		return errors.New("Each room needs 1–9 adults and at most 6 children.")
	}
	for _, age := range o.Children {
		if age < 0 || age > 17 {
			return errors.New("Child ages must be between 0 and 17.")
		}
	}
	if len(q.GuestNationality) != 2 || !strings.Contains(countryCodes, " "+q.GuestNationality+" ") {
		return errors.New("Please enter a valid guest nationality (two-letter country code, e.g. DE).")
	}
	switch q.Currency {
	case "EUR", "USD", "GBP", "TRY":
	default:
		return errors.New("Please select EUR, USD, GBP or TRY for hotel prices.")
	}
	start, err := time.Parse("2006-01-02", q.Checkin)
	end, endErr := time.Parse("2006-01-02", q.Checkout)
	if err != nil || endErr != nil || !end.After(start) || q.Checkin < time.Now().Format("2006-01-02") {
		return errors.New("Please select valid future hotel dates, with check-out after check-in.")
	}
	return nil
}

// CheapestOffer compares only complete, positive offers in the same currency.
func (h HotelRates) CheapestOffer() (RoomType, bool) {
	var best RoomType
	for _, offer := range h.RoomTypes {
		a := offer.OfferRetailRate
		if offer.OfferID == "" || len(offer.Rates) != 1 || offer.Rates[0].RateID == "" ||
			a.Amount <= 0 || math.IsNaN(a.Amount) || math.IsInf(a.Amount, 0) || len(a.Currency) != 3 ||
			strings.IndexFunc(a.Currency, func(r rune) bool { return r < 'A' || r > 'Z' }) >= 0 {
			continue
		}
		if best.OfferID != "" && best.OfferRetailRate.Currency != a.Currency {
			return RoomType{}, false // Different currencies cannot be compared without a quote.
		}
		if best.OfferID == "" || a.Amount < best.OfferRetailRate.Amount {
			best = offer
		}
	}
	return best, best.OfferID != ""
}

func (c *Client) FetchRates(hotelIDs []string, query RateQuery) ([]HotelRates, error) {
	if len(hotelIDs) == 0 {
		return nil, errors.New("liteapi: at least one hotel id is required")
	}
	if err := query.Validate(); err != nil {
		return nil, err
	}
	payload := struct {
		RateQuery
		HotelIDs []string `json:"hotelIds"`
	}{query, hotelIDs}
	var out struct {
		Data []HotelRates `json:"data"`
	}
	if err := c.do(http.MethodPost, "/hotels/rates", payload, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (c *Client) do(method, path string, payload, out any) error {
	if c.key == "" {
		return errors.New("liteapi: LITEAPI_API_KEY is not configured")
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return errors.New("liteapi: could not encode request")
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, apiBase+path, body)
	if err != nil {
		return errors.New("liteapi: could not build request")
	}
	req.Header.Set("X-API-Key", c.key)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return ErrTimeout
		}
		// Transport errors may include request URLs or injected sensitive data.
		return errors.New("liteapi: network request failed; outcome may be unknown")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return errors.New("liteapi: could not read response; outcome may be unknown")
	}
	if len(raw) > maxResponseBytes {
		return errors.New("liteapi: response exceeds size limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("liteapi %s %s: HTTP %d", method, path, resp.StatusCode)
		return fmt.Errorf("liteapi: HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return errors.New("liteapi: could not decode response data")
	}
	return nil
}
