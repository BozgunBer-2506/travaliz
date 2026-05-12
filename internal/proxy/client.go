package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	rapidAPIKey  = "8c145b23a1msh5288f08d5058e73p18692ejsn1e8fb15260c9"
	rapidAPIHost = "skyscanner-flights-travel-api.p.rapidapi.com"
	rapidAPIBase = "https://skyscanner-flights-travel-api.p.rapidapi.com"
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

// --- internal Skyscanner structs ---

type skyDestResponse struct {
	Places []DestSuggestion `json:"places"`
}

type skyAirportResponse struct {
	Places []FlightDestSuggestion `json:"places"`
}

type skyCarrier struct {
	Name    string `json:"name"`
	LogoURL string `json:"logoUrl"`
}

type skyLeg struct {
	Origin          string       `json:"origin"`
	Destination     string       `json:"destination"`
	Departure       string       `json:"departure"`
	Arrival         string       `json:"arrival"`
	DurationMinutes int          `json:"durationMinutes"`
	StopCount       int          `json:"stopCount"`
	Carriers        []skyCarrier `json:"carriers"`
}

type skyPrice struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

type skyItinerary struct {
	Price skyPrice `json:"price"`
	Legs  []skyLeg `json:"legs"`
}

type skyFlightResponse struct {
	Itineraries []skyItinerary `json:"itineraries"`
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

// SearchHotelDestinations returns city/hotel suggestions for the hotel search autocomplete.
func (pc *ProxyClient) SearchHotelDestinations(query string) ([]DestSuggestion, error) {
	resp, err := pc.doGet("/hotels/searchDestination", map[string]string{"query": query})
	if err != nil {
		return nil, fmt.Errorf("hotel destination search failed: %w", err)
	}
	defer resp.Body.Close()

	var payload skyDestResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode hotel destination response: %w", err)
	}
	// Filter to cities only for hotel search
	cities := make([]DestSuggestion, 0)
	for _, p := range payload.Places {
		if p.Type == "city" || p.Type == "country" {
			cities = append(cities, p)
		}
	}
	return cities, nil
}

// SearchHotelDestination returns the first city entityId for a query.
func (pc *ProxyClient) SearchHotelDestination(query string) (entityID string, err error) {
	results, err := pc.SearchHotelDestinations(query)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "", fmt.Errorf("no destination found for %q", query)
	}
	return results[0].EntityID, nil
}

// SearchAirports returns airport suggestions for the flight search autocomplete.
func (pc *ProxyClient) SearchAirports(query string) ([]FlightDestSuggestion, error) {
	resp, err := pc.doGet("/flights/searchAirport", map[string]string{"query": query})
	if err != nil {
		return nil, fmt.Errorf("airport search failed: %w", err)
	}
	defer resp.Body.Close()

	var payload skyAirportResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode airport response: %w", err)
	}
	return payload.Places, nil
}

// FetchHotels returns hotels for a destination. Skyscanner free tier returns empty;
// callers should redirect to Skyscanner when the slice is empty.
func (pc *ProxyClient) FetchHotels(entityID, checkIn, checkOut string) ([]HotelData, error) {
	resp, err := pc.doGet("/hotels/searchHotels", map[string]string{
		"entityId": entityID,
		"checkIn":  checkIn,
		"checkOut": checkOut,
		"adults":   "2",
		"rooms":    "1",
		"currency": "USD",
	})
	if err != nil {
		return nil, fmt.Errorf("hotel search failed: %w", err)
	}
	defer resp.Body.Close()

	// Skyscanner free tier returns {"hotels":[],"total":0} - return empty slice without error
	// so the template can show the Skyscanner deep-link fallback.
	type skyHotelPrice struct {
		Amount   float64 `json:"amount"`
		Currency string  `json:"currency"`
	}
	type skyHotel struct {
		HotelID string        `json:"hotelId"`
		Name    string        `json:"name"`
		Stars   int           `json:"stars"`
		Rating  float64       `json:"rating"`
		Price   skyHotelPrice `json:"price"`
		Images  []string      `json:"images"`
	}
	type skyHotelResp struct {
		Hotels          []skyHotel `json:"hotels"`
		DestinationName string     `json:"destinationName"`
	}

	var payload skyHotelResp
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode hotel response: %w", err)
	}

	hotels := make([]HotelData, 0, len(payload.Hotels))
	for i, h := range payload.Hotels {
		photo := ""
		if len(h.Images) > 0 {
			photo = h.Images[0]
		}
		hotels = append(hotels, HotelData{
			HotelID:   i + 1,
			HotelName: h.Name,
			Price:     h.Price.Amount,
			Currency:  h.Price.Currency,
			Rating:    h.Rating,
			PhotoURL:  photo,
			Stars:     h.Stars,
		})
	}
	return hotels, nil
}

// FetchTravelData is kept for the JSON API endpoint (/travel-data).
func (pc *ProxyClient) FetchTravelData() ([]HotelData, error) {
	return pc.FetchHotels("27544008", "2026-08-01", "2026-08-05")
}

func (pc *ProxyClient) FetchFlights(fromSkyID, fromEntityID, toSkyID, toEntityID, date string) ([]FlightData, error) {
	resp, err := pc.doGet("/flights/searchFlights", map[string]string{
		"originSkyId":          fromSkyID,
		"destinationSkyId":     toSkyID,
		"originEntityId":       fromEntityID,
		"destinationEntityId":  toEntityID,
		"date":                 date,
		"adults":               "1",
		"currency":             "USD",
		"cabinClass":           "economy",
	})
	if err != nil {
		return nil, fmt.Errorf("flight search failed: %w", err)
	}
	defer resp.Body.Close()

	var payload skyFlightResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode flight response: %w", err)
	}

	flights := make([]FlightData, 0, len(payload.Itineraries))
	for _, it := range payload.Itineraries {
		if len(it.Legs) == 0 {
			continue
		}
		leg := it.Legs[0]
		airline, logo := "", ""
		if len(leg.Carriers) > 0 {
			airline = leg.Carriers[0].Name
			logo = leg.Carriers[0].LogoURL
		}
		flights = append(flights, FlightData{
			FromCode:        leg.Origin,
			ToCode:          leg.Destination,
			DepartTime:      formatFlightTime(leg.Departure),
			ArriveTime:      formatFlightTime(leg.Arrival),
			DurationHours:   leg.DurationMinutes / 60,
			DurationMinutes: leg.DurationMinutes % 60,
			Airline:         airline,
			AirlineLogo:     logo,
			Price:           it.Price.Amount,
			Currency:        it.Price.Currency,
			Stops:           leg.StopCount,
		})
	}
	return flights, nil
}

func formatFlightTime(iso string) string {
	if len(iso) >= 16 {
		return iso[11:16]
	}
	return iso
}
