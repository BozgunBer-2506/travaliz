package duffel

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var (
	ErrLiveSearchDisabled = errors.New("duffel: live flight search is disabled")
	ErrLiveTokenInvalid   = errors.New("duffel: a live read-only token (duffel_live_) is required")
)

// LiveSearchClient reaches Duffel's live (non-test) environment for search
// only. It has no CreateOrder method: a live order can never be placed
// through this type, not merely refused by a runtime check. It uses its own
// token (DUFFEL_LIVE_API_TOKEN) so live and sandbox credentials, and the
// data read with them, are never shared with DuffelClient (DUFFEL_API_TOKEN,
// duffel_test_, used by the existing search-and-book flow).
//
// Create the live token itself with Duffel's own "read-only" scope
// (dashboard, Live mode): that is Duffel's own enforcement against placing
// an order, ahead of and independent from every check in this package.
type LiveSearchClient struct {
	token      string
	HTTPClient *http.Client
}

// NewLiveSearchClient returns ErrLiveSearchDisabled unless
// LIVE_FLIGHT_SEARCH_ENABLED=true, and ErrLiveTokenInvalid unless
// DUFFEL_LIVE_API_TOKEN has the duffel_live_ prefix. Both checks apply
// regardless of the other, so neither alone is enough to reach the network.
func NewLiveSearchClient() (*LiveSearchClient, error) {
	if os.Getenv("LIVE_FLIGHT_SEARCH_ENABLED") != "true" {
		return nil, ErrLiveSearchDisabled
	}
	token := strings.TrimSpace(os.Getenv("DUFFEL_LIVE_API_TOKEN"))
	if !strings.HasPrefix(token, "duffel_live_") {
		return nil, ErrLiveTokenInvalid
	}
	return &LiveSearchClient{
		token: token,
		HTTPClient: &http.Client{
			Timeout:       60 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func (c *LiveSearchClient) CreateOfferRequest(slices []SearchSlice, passengers []SearchPassenger, cabinClass string) ([]Offer, error) {
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
	if err := doRequest(c.HTTPClient, c.token, http.MethodPost, "/air/offer_requests?return_offers=true", payload, &data); err != nil {
		return nil, err
	}
	if data.Offers == nil {
		data.Offers = []Offer{}
	}
	return data.Offers, nil
}

func (c *LiveSearchClient) GetOffer(offerID string) (Offer, error) {
	var offer Offer
	if offerID == "" {
		return offer, errors.New("duffel: offer ID is required")
	}
	err := doRequest(c.HTTPClient, c.token, http.MethodGet, "/air/offers/"+url.PathEscape(offerID), nil, &offer)
	return offer, err
}
