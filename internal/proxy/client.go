package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	rapidAPIKey = "8c145b23a1msh5288f08d5058e73p18692ejsn1e8fb15260c9"

	skyHost     = "skyscanner-flights-travel-api.p.rapidapi.com"
	skyBase     = "https://skyscanner-flights-travel-api.p.rapidapi.com"

	gfHost      = "google-flights2.p.rapidapi.com"
	gfBase      = "https://google-flights2.p.rapidapi.com"

	hotelsHost  = "hotels-com-provider.p.rapidapi.com"
	hotelsBase  = "https://hotels-com-provider.p.rapidapi.com"
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

// --- Google Flights internal structs ---

type gfAirportEntry struct {
	ID   string           `json:"id"`
	Type string           `json:"type"`
	Name string           `json:"title"`
	City string           `json:"city"`
	List []gfAirportEntry `json:"list"`
}

type gfAirportResponse struct {
	Data []gfAirportEntry `json:"data"`
}

type gfAirport struct {
	AirportCode string `json:"airport_code"`
	AirportName string `json:"airport_name"`
}

type gfFlightSegment struct {
	DepartureAirport gfAirport `json:"departure_airport"`
	ArrivalAirport   gfAirport `json:"arrival_airport"`
	Airline          string    `json:"airline"`
	AirlineLogo      string    `json:"airline_logo"`
}

type gfDuration struct {
	Raw int `json:"raw"`
}

type gfLayover struct {
	AirportCode string `json:"airport_code"`
}

type gfItinerary struct {
	DepartureTime string            `json:"departure_time"`
	ArrivalTime   string            `json:"arrival_time"`
	Duration      gfDuration        `json:"duration"`
	Flights       []gfFlightSegment `json:"flights"`
	Layovers      []gfLayover       `json:"layovers"`
	AirlineLogo   string            `json:"airline_logo"`
	Price         float64           `json:"price"`
}

type gfItineraries struct {
	TopFlights   []gfItinerary `json:"topFlights"`
	OtherFlights []gfItinerary `json:"otherFlights"`
}

type gfFlightData struct {
	Itineraries gfItineraries `json:"itineraries"`
}

type gfFlightResponse struct {
	Status bool         `json:"status"`
	Data   gfFlightData `json:"data"`
}

// --- Hotels.com regions internal structs ---

type hcRegion struct {
	GaiaID      string `json:"gaiaId"`
	Type        string `json:"type"`
	RegionNames struct {
		FullName    string `json:"fullName"`
		ShortName   string `json:"shortName"`
		DisplayName string `json:"displayName"`
	} `json:"regionNames"`
	HierarchyInfo struct {
		Country struct {
			Name string `json:"name"`
		} `json:"country"`
	} `json:"hierarchyInfo"`
}

type hcRegionResponse struct {
	Data []hcRegion `json:"data"`
}

// ---

type ProxyClient struct {
	HTTPClient *http.Client
}

func NewProxyClient(_ string) *ProxyClient {
	return &ProxyClient{
		HTTPClient: &http.Client{Timeout: 12 * time.Second},
	}
}

func (pc *ProxyClient) doGet(base, host, endpoint string, params map[string]string) (*http.Response, error) {
	req, err := http.NewRequest("GET", base+endpoint, nil)
	if err != nil {
		return nil, err
	}
	q := req.URL.Query()
	for k, v := range params {
		q.Add(k, v)
	}
	req.URL.RawQuery = q.Encode()
	req.Header.Set("X-RapidAPI-Key", rapidAPIKey)
	req.Header.Set("X-RapidAPI-Host", host)
	return pc.HTTPClient.Do(req)
}

// SearchHotelDestinations returns city suggestions for hotel autocomplete via Hotels.com regions API.
func (pc *ProxyClient) SearchHotelDestinations(query string) ([]DestSuggestion, error) {
	resp, err := pc.doGet(hotelsBase, hotelsHost, "/v2/regions", map[string]string{
		"query":  query,
		"locale": "en_US",
		"domain": "US",
	})
	if err != nil {
		return nil, fmt.Errorf("hotel destination search failed: %w", err)
	}
	defer resp.Body.Close()

	var payload hcRegionResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode hotel destination response: %w", err)
	}

	results := make([]DestSuggestion, 0)
	for _, r := range payload.Data {
		if r.Type != "CITY" && r.Type != "NEIGHBORHOOD" && r.Type != "AIRPORT" {
			continue
		}
		hierarchy := r.HierarchyInfo.Country.Name
		results = append(results, DestSuggestion{
			EntityID:  r.GaiaID,
			Name:      r.RegionNames.DisplayName,
			Type:      strings.ToLower(r.Type),
			Hierarchy: hierarchy,
		})
		if len(results) >= 6 {
			break
		}
	}
	return results, nil
}

// SearchHotelDestination returns the first city gaiaId for a query.
func (pc *ProxyClient) SearchHotelDestination(query string) (string, error) {
	results, err := pc.SearchHotelDestinations(query)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "", fmt.Errorf("no destination found for %q", query)
	}
	return results[0].EntityID, nil
}

// SearchAirports returns airport suggestions using Google Flights airport search.
func (pc *ProxyClient) SearchAirports(query string) ([]FlightDestSuggestion, error) {
	resp, err := pc.doGet(gfBase, gfHost, "/api/v1/searchAirport", map[string]string{"query": query})
	if err != nil {
		return nil, fmt.Errorf("airport search failed: %w", err)
	}
	defer resp.Body.Close()

	var payload gfAirportResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode airport response: %w", err)
	}

	var results []FlightDestSuggestion
	for _, group := range payload.Data {
		countryName := ""
		if idx := strings.LastIndex(group.Name, ", "); idx >= 0 {
			countryName = group.Name[idx+2:]
		}
		for _, a := range group.List {
			if a.Type != "airport" || len(a.ID) != 3 {
				continue
			}
			results = append(results, FlightDestSuggestion{
				SkyID:       a.ID,
				EntityID:    a.ID,
				Name:        a.Name,
				CityName:    a.City,
				CountryName: countryName,
				PlaceType:   "airport",
			})
		}
		if len(results) >= 8 {
			break
		}
	}
	return results, nil
}

// FetchHotels attempts Hotels.com search. Free tier returns empty results;
// caller shows a Hotels.com deep link fallback when the slice is empty.
func (pc *ProxyClient) FetchHotels(regionID, checkIn, checkOut, adults, children, rooms string) ([]HotelData, error) {
	if adults == "" {
		adults = "2"
	}
	params := map[string]string{
		"region_id":     regionID,
		"checkin_date":  checkIn,
		"checkout_date": checkOut,
		"adults_number": adults,
		"sort_order":    "REVIEW",
		"page_number":   "1",
		"locale":        "en_US",
		"domain":        "US",
	}
	if children != "" && children != "0" {
		params["children_number"] = children
	}

	resp, err := pc.doGet(hotelsBase, hotelsHost, "/v2/hotels/search", params)
	if err != nil {
		return nil, fmt.Errorf("hotel search failed: %w", err)
	}
	defer resp.Body.Close()

	// Hotels.com free tier returns {"propertySearchListings":[{"__typename":"LodgingCard"},...]}
	// with no actual property data - return empty slice so caller shows deep link fallback.
	type hcSearchResp struct {
		Properties []json.RawMessage `json:"properties"`
	}
	var payload hcSearchResp
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode hotel response: %w", err)
	}
	return []HotelData{}, nil
}

// FetchTravelData is kept for the /travel-data JSON endpoint.
func (pc *ProxyClient) FetchTravelData() ([]HotelData, error) {
	return pc.FetchHotels("2872", "2026-08-01", "2026-08-05", "2", "0", "1")
}

// FetchFlights searches flights via Google Flights API.
// fromEntityID and toEntityID are ignored (Google Flights uses IATA codes only).
func (pc *ProxyClient) FetchFlights(fromSkyID, _, toSkyID, _, date, returnDate, adults, children, cabinClass string) ([]FlightData, error) {
	if adults == "" {
		adults = "1"
	}
	if cabinClass == "" {
		cabinClass = "economy"
	}

	params := map[string]string{
		"departure_id":  fromSkyID,
		"arrival_id":    toSkyID,
		"outbound_date": date,
		"travel_class":  strings.ToUpper(cabinClass),
		"adults":        adults,
		"currency":      "USD",
		"search_type":   "best",
		"language_code": "en-US",
		"country_code":  "US",
	}
	if children != "" && children != "0" {
		params["children"] = children
	}
	if returnDate != "" {
		params["return_date"] = returnDate
	}

	resp, err := pc.doGet(gfBase, gfHost, "/api/v1/searchFlights", params)
	if err != nil {
		return nil, fmt.Errorf("flight search failed: %w", err)
	}
	defer resp.Body.Close()

	var payload gfFlightResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode flight response: %w", err)
	}
	if !payload.Status {
		return nil, fmt.Errorf("google flights returned no results")
	}

	all := append(payload.Data.Itineraries.TopFlights, payload.Data.Itineraries.OtherFlights...)
	flights := make([]FlightData, 0, len(all))
	for _, it := range all {
		if len(it.Flights) == 0 {
			continue
		}
		first := it.Flights[0]
		last := it.Flights[len(it.Flights)-1]
		flights = append(flights, FlightData{
			FromCode:        first.DepartureAirport.AirportCode,
			ToCode:          last.ArrivalAirport.AirportCode,
			DepartTime:      extractTime(it.DepartureTime),
			ArriveTime:      extractTime(it.ArrivalTime),
			DurationHours:   it.Duration.Raw / 60,
			DurationMinutes: it.Duration.Raw % 60,
			Airline:         first.Airline,
			AirlineLogo:     it.AirlineLogo,
			Price:           it.Price,
			Currency:        "USD",
			Stops:           len(it.Layovers),
		})
	}
	return flights, nil
}

// extractTime pulls "11:25 PM" from "15-06-2026 11:25 PM"
func extractTime(s string) string {
	if idx := strings.Index(s, " "); idx >= 0 {
		rest := s[idx+1:]
		if idx2 := strings.Index(rest, " "); idx2 >= 0 {
			return rest
		}
		return rest
	}
	return s
}
