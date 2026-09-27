package proxy

import (
	"net/http"
	"os"
	"strings"
	"time"
)

// CarData is the flat struct for car rental listings.
type CarData struct {
	CarID         int     `json:"car_id"`
	CarName       string  `json:"car_name"`
	Category      string  `json:"category"`
	Seats         int     `json:"seats"`
	Doors         int     `json:"doors"`
	Transmission  string  `json:"transmission"`
	PricePerDay   float64 `json:"price_per_day"`
	OriginalPrice float64 `json:"original_price"`
	Currency      string  `json:"currency"`
	Provider      string  `json:"provider"`
	ProviderLogo  string  `json:"provider_logo"`
	Badge         string  `json:"badge"`
	CategoryColor string  `json:"category_color"`
}

// HotelData is the flat struct passed to templates and JSON API.
type HotelData struct {
	BookingURL string  `json:"booking_url"`
	TotalPrice float64 `json:"total_price"`
	HotelID    int     `json:"hotel_id"`
	HotelName  string  `json:"hotel_name"`
	Price      float64 `json:"price"`
	Currency   string  `json:"currency"`
	Rating     float64 `json:"rating"`
	RatingWord string  `json:"rating_word"`
	PhotoURL   string  `json:"photo_url"`
	Stars      int     `json:"stars"`
}

// FlightData is the flat struct for flight offers.
type FlightData struct {
	BookingURL      string  `json:"booking_url"`
	PriceLabel      string  `json:"price_label"`
	FromCity        string  `json:"from_city"`
	ToCity          string  `json:"to_city"`
	FromCode        string  `json:"from_code"`
	ToCode          string  `json:"to_code"`
	DepartDate      string  `json:"depart_date"`
	DepartTime      string  `json:"depart_time"`
	ArriveTime      string  `json:"arrive_time"`
	DurationHours   int     `json:"duration_hours"`
	DurationMinutes int     `json:"duration_minutes"`
	Airline         string  `json:"airline"`
	AirlineLogo     string  `json:"airline_logo"`
	Price           float64 `json:"price"`
	Currency        string  `json:"currency"`
	Stops           int     `json:"stops"`
}

// DestSuggestion is returned by the /suggest endpoint (hotel city search).
type DestSuggestion struct {
	EntityID  string `json:"entityId"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Hierarchy string `json:"hierarchy"`
}

// FlightDestSuggestion is returned by the /suggest-flight endpoint.
type FlightDestSuggestion struct {
	SkyID       string `json:"skyId"`
	EntityID    string `json:"entityId"`
	Name        string `json:"name"`
	CityName    string `json:"cityName"`
	CountryName string `json:"countryName"`
	PlaceType   string `json:"placeType"`
}

// ProxyClient uses SerpApi only. Suggestions never call a paid provider.
type ProxyClient struct {
	HTTPClient *http.Client
	apiKey     string
}

func NewProxyClient(key string) *ProxyClient {
	if key == "" {
		key = os.Getenv("SERPAPI_API_KEY")
	}
	return &ProxyClient{apiKey: strings.TrimSpace(key), HTTPClient: &http.Client{
		Timeout:       60 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// The old mock-data endpoint no longer presents invented offers as live data.
func (pc *ProxyClient) FetchTravelData() ([]HotelData, error) { return []HotelData{}, nil }
