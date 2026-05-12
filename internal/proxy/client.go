package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	rapidAPIKey  = "8c145b23a1msh5288f08d5058e73p18692ejsn1e8fb15260c9"
	rapidAPIHost = "booking-com15.p.rapidapi.com"
	rapidAPIBase = "https://booking-com15.p.rapidapi.com"
)

// TravelData is the flat struct passed to the HTML template.
type TravelData struct {
	HotelID    int
	HotelName  string
	Location   string
	Price      float64
	Currency   string
	Rating     float64
	RatingWord string
	PhotoURL   string
	Stars      int
}

// --- internal structs for parsing the booking-com15 response ---

type grossPrice struct {
	Value    float64 `json:"value"`
	Currency string  `json:"currency"`
}

type priceBreakdown struct {
	GrossPrice grossPrice `json:"grossPrice"`
}

type hotelProperty struct {
	ID                    int            `json:"id"`
	Name                  string         `json:"name"`
	ReviewScore           float64        `json:"reviewScore"`
	ReviewScoreWord       string         `json:"reviewScoreWord"`
	PhotoURLs             []string       `json:"photoUrls"`
	PriceBreakdown        priceBreakdown `json:"priceBreakdown"`
	AccuratePropertyClass int            `json:"accuratePropertyClass"`
}

type hotelItem struct {
	HotelID  int           `json:"hotel_id"`
	Property hotelProperty `json:"property"`
}

type hotelsData struct {
	Hotels []hotelItem `json:"hotels"`
}

type apiResponse struct {
	Status bool       `json:"status"`
	Data   hotelsData `json:"data"`
}

// ---

type ProxyClient struct {
	HTTPClient *http.Client
}

func NewProxyClient(_ string) *ProxyClient {
	return &ProxyClient{
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (pc *ProxyClient) FetchTravelData() ([]TravelData, error) {
	req, err := http.NewRequest("GET", rapidAPIBase+"/api/v1/hotels/searchHotels", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}

	q := req.URL.Query()
	q.Add("dest_id", "-2601889")
	q.Add("search_type", "CITY")
	q.Add("arrival_date", "2026-06-01")
	q.Add("departure_date", "2026-06-05")
	q.Add("adults", "2")
	q.Add("room_qty", "1")
	q.Add("currency_code", "USD")
	q.Add("languagecode", "en-us")
	req.URL.RawQuery = q.Encode()

	req.Header.Set("X-RapidAPI-Key", rapidAPIKey)
	req.Header.Set("X-RapidAPI-Host", rapidAPIHost)

	resp, err := pc.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream API returned status %d", resp.StatusCode)
	}

	var payload apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode API response: %w", err)
	}

	hotels := make([]TravelData, 0, len(payload.Data.Hotels))
	for _, item := range payload.Data.Hotels {
		p := item.Property
		photoURL := ""
		if len(p.PhotoURLs) > 0 {
			photoURL = p.PhotoURLs[0]
		}
		hotels = append(hotels, TravelData{
			HotelID:    item.HotelID,
			HotelName:  p.Name,
			Price:      p.PriceBreakdown.GrossPrice.Value,
			Currency:   p.PriceBreakdown.GrossPrice.Currency,
			Rating:     p.ReviewScore,
			RatingWord: p.ReviewScoreWord,
			PhotoURL:   photoURL,
			Stars:      p.AccuratePropertyClass,
		})
	}

	return hotels, nil
}
