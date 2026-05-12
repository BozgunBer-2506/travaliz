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

// HotelData is the flat struct passed to templates and JSON API.
type HotelData struct {
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
	FromCity        string  `json:"from_city"`
	ToCity          string  `json:"to_city"`
	FromCode        string  `json:"from_code"`
	ToCode          string  `json:"to_code"`
	DepartTime      string  `json:"depart_time"`
	ArriveTime      string  `json:"arrive_time"`
	DurationHours   int     `json:"duration_hours"`
	DurationMinutes int     `json:"duration_minutes"`
	Airline         string  `json:"airline"`
	AirlineLogo     string  `json:"airline_logo"`
	Price           float64 `json:"price"`
	Currency        string  `json:"currency"`
}

// DestSuggestion is returned by the /suggest endpoint.
type DestSuggestion struct {
	DestID     string `json:"dest_id"`
	SearchType string `json:"search_type"`
	DestType   string `json:"dest_type"`
	Name       string `json:"name"`
	Label      string `json:"label"`
	ImageURL   string `json:"image_url"`
}

type destAPIResponse struct {
	Status bool             `json:"status"`
	Data   []DestSuggestion `json:"data"`
}

// --- internal hotel structs ---

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

type hotelsAPIResponse struct {
	Status bool       `json:"status"`
	Data   hotelsData `json:"data"`
}

// --- internal flight structs ---

type flightAirport struct {
	Code     string `json:"code"`
	CityName string `json:"cityName"`
}

type flightPrice struct {
	CurrencyCode string `json:"currencyCode"`
	Units        int    `json:"units"`
	Nanos        int    `json:"nanos"`
}

type flightPriceBreakdown struct {
	Total flightPrice `json:"total"`
}

type carrierData struct {
	Name string `json:"name"`
	Logo string `json:"logo"`
}

type flightSegment struct {
	DepartureAirport flightAirport `json:"departureAirport"`
	ArrivalAirport   flightAirport `json:"arrivalAirport"`
	DepartureTime    string        `json:"departureTime"`
	ArrivalTime      string        `json:"arrivalTime"`
	TotalTime        int           `json:"totalTime"`
	CarriersData     []carrierData `json:"carriersData"`
}

type flightOffer struct {
	Segments       []flightSegment      `json:"segments"`
	PriceBreakdown flightPriceBreakdown `json:"priceBreakdown"`
}

type flightOffersData struct {
	FlightOffers []flightOffer `json:"flightOffers"`
}

type flightAPIResponse struct {
	Status bool             `json:"status"`
	Data   flightOffersData `json:"data"`
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

func (pc *ProxyClient) doGet(endpoint string, params map[string]string) (*http.Response, error) {
	req, err := http.NewRequest("GET", rapidAPIBase+endpoint, nil)
	if err != nil {
		return nil, err
	}
	q := req.URL.Query()
	for k, v := range params {
		q.Add(k, v)
	}
	req.URL.RawQuery = q.Encode()
	req.Header.Set("X-RapidAPI-Key", rapidAPIKey)
	req.Header.Set("X-RapidAPI-Host", rapidAPIHost)
	return pc.HTTPClient.Do(req)
}

func (pc *ProxyClient) SearchDestinations(query string) ([]DestSuggestion, error) {
	resp, err := pc.doGet("/api/v1/hotels/searchDestination", map[string]string{"query": query})
	if err != nil {
		return nil, fmt.Errorf("destination search failed: %w", err)
	}
	defer resp.Body.Close()

	var payload destAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode destination response: %w", err)
	}
	return payload.Data, nil
}

func (pc *ProxyClient) SearchDestination(query string) (destID, searchType string, err error) {
	results, err := pc.SearchDestinations(query)
	if err != nil {
		return "", "", err
	}
	if len(results) == 0 {
		return "", "", fmt.Errorf("no results found for %q", query)
	}
	return results[0].DestID, results[0].SearchType, nil
}

func (pc *ProxyClient) FetchHotels(destID, searchType, checkin, checkout string) ([]HotelData, error) {
	resp, err := pc.doGet("/api/v1/hotels/searchHotels", map[string]string{
		"dest_id":        destID,
		"search_type":    searchType,
		"arrival_date":   checkin,
		"departure_date": checkout,
		"adults":         "2",
		"room_qty":       "1",
		"currency_code":  "USD",
		"languagecode":   "en-us",
	})
	if err != nil {
		return nil, fmt.Errorf("hotel search failed: %w", err)
	}
	defer resp.Body.Close()

	var payload hotelsAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode hotel response: %w", err)
	}

	hotels := make([]HotelData, 0, len(payload.Data.Hotels))
	for _, item := range payload.Data.Hotels {
		p := item.Property
		photoURL := ""
		if len(p.PhotoURLs) > 0 {
			photoURL = p.PhotoURLs[0]
		}
		hotels = append(hotels, HotelData{
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

// FetchTravelData is kept for the JSON API endpoint (/travel-data).
func (pc *ProxyClient) FetchTravelData() ([]HotelData, error) {
	return pc.FetchHotels("-2601889", "CITY", "2026-06-01", "2026-06-05")
}

func (pc *ProxyClient) FetchFlights(fromID, toID, date string) ([]FlightData, error) {
	resp, err := pc.doGet("/api/v1/flights/searchFlights", map[string]string{
		"fromId":        fromID,
		"toId":          toID,
		"departDate":    date,
		"adults":        "1",
		"currency_code": "USD",
		"sort":          "BEST",
		"cabinClass":    "ECONOMY",
	})
	if err != nil {
		return nil, fmt.Errorf("flight search failed: %w", err)
	}
	defer resp.Body.Close()

	var payload flightAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode flight response: %w", err)
	}

	flights := make([]FlightData, 0, len(payload.Data.FlightOffers))
	for _, offer := range payload.Data.FlightOffers {
		if len(offer.Segments) == 0 {
			continue
		}
		seg := offer.Segments[0]
		price := float64(offer.PriceBreakdown.Total.Units) +
			float64(offer.PriceBreakdown.Total.Nanos)/1e9

		airline, airlineLogo := "", ""
		if len(seg.CarriersData) > 0 {
			airline = seg.CarriersData[0].Name
			airlineLogo = seg.CarriersData[0].Logo
		}

		totalMins := seg.TotalTime / 60
		flights = append(flights, FlightData{
			FromCity:        seg.DepartureAirport.CityName,
			ToCity:          seg.ArrivalAirport.CityName,
			FromCode:        seg.DepartureAirport.Code,
			ToCode:          seg.ArrivalAirport.Code,
			DepartTime:      seg.DepartureTime,
			ArriveTime:      seg.ArrivalTime,
			DurationHours:   totalMins / 60,
			DurationMinutes: totalMins % 60,
			Airline:         airline,
			AirlineLogo:     airlineLogo,
			Price:           price,
			Currency:        offer.PriceBreakdown.Total.CurrencyCode,
		})
	}
	return flights, nil
}
