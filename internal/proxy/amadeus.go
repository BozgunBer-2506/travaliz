package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const amadeusBase = "https://test.api.amadeus.com"

var verifiedHotelPhotos = []string{
	"photo-1564501049412-61c2a3083791",
	"photo-1551882547-ff40c63fe5fa",
	"photo-1520250497591-112f2f40a3f4",
	"photo-1582719508461-905c673771fd",
	"photo-1573843981267-be1999ff37cd",
	"photo-1522708323590-d24dbb6b0267",
	"photo-1590490360182-c33d57733427",
	"photo-1613977257363-707ba9348227",
	"photo-1555854877-bab0e564b8d5",
	"photo-1455587734955-081b22074882",
}

func cityPhoto(city string, idx int) string {
	_ = city
	return "https://images.unsplash.com/" + verifiedHotelPhotos[idx%len(verifiedHotelPhotos)] + "?w=600&q=80"
}

// --- Amadeus API response types ---

type amadeusTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

type amadeusCityResponse struct {
	Data []struct {
		IataCode string `json:"iataCode"`
		SubType  string `json:"subType"`
	} `json:"data"`
}

type amadeusHotelListResponse struct {
	Data []struct {
		HotelId string `json:"hotelId"`
		Name    string `json:"name"`
		Rating  string `json:"rating"`
	} `json:"data"`
}

type amadeusHotelOffersResponse struct {
	Data []struct {
		Hotel struct {
			HotelId string `json:"hotelId"`
			Name    string `json:"name"`
			Rating  string `json:"rating"`
		} `json:"hotel"`
		Available bool `json:"available"`
		Offers    []struct {
			Price struct {
				Currency string `json:"currency"`
				Total    string `json:"total"`
				Base     string `json:"base"`
			} `json:"price"`
		} `json:"offers"`
	} `json:"data"`
}

// ---

// AmadeusClient handles Amadeus Self-Service API calls with OAuth2 token caching.
type AmadeusClient struct {
	clientID     string
	clientSecret string
	httpClient   *http.Client
	mu           sync.Mutex
	token        string
	tokenExpiry  time.Time
}

func NewAmadeusClient(clientID, clientSecret string) *AmadeusClient {
	return &AmadeusClient{
		clientID:     clientID,
		clientSecret: clientSecret,
		httpClient:   &http.Client{Timeout: 15 * time.Second},
	}
}

func (ac *AmadeusClient) Enabled() bool {
	return ac.clientID != "" && ac.clientSecret != ""
}

func (ac *AmadeusClient) getToken() (string, error) {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	if ac.token != "" && time.Now().Before(ac.tokenExpiry) {
		return ac.token, nil
	}
	data := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {ac.clientID},
		"client_secret": {ac.clientSecret},
	}
	resp, err := ac.httpClient.PostForm(amadeusBase+"/v1/security/oauth2/token", data)
	if err != nil {
		return "", fmt.Errorf("amadeus auth: %w", err)
	}
	defer resp.Body.Close()
	var result amadeusTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("amadeus auth decode: %w", err)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("amadeus returned empty token")
	}
	expiry := result.ExpiresIn
	if expiry < 120 {
		expiry = 120
	}
	ac.token = result.AccessToken
	ac.tokenExpiry = time.Now().Add(time.Duration(expiry-60) * time.Second)
	return ac.token, nil
}

func (ac *AmadeusClient) doGet(endpoint string, params map[string]string) (*http.Response, error) {
	token, err := ac.getToken()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("GET", amadeusBase+endpoint, nil)
	if err != nil {
		return nil, err
	}
	q := req.URL.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Authorization", "Bearer "+token)
	return ac.httpClient.Do(req)
}

// LookupCityCode returns the Amadeus IATA city code for a city name (e.g., "Paris" -> "PAR").
func (ac *AmadeusClient) LookupCityCode(cityName string) (string, error) {
	resp, err := ac.doGet("/v1/reference-data/locations", map[string]string{
		"keyword": cityName,
		"subType": "CITY",
	})
	if err != nil {
		return "", fmt.Errorf("city lookup: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("city lookup: status %d", resp.StatusCode)
	}
	var payload amadeusCityResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("city lookup decode: %w", err)
	}
	if len(payload.Data) == 0 {
		return "", fmt.Errorf("no city found for %q", cityName)
	}
	return payload.Data[0].IataCode, nil
}

// amadeusRatingWord maps guest score to a label.
func amadeusRatingWord(score float64) string {
	switch {
	case score >= 9.0:
		return "Exceptional"
	case score >= 8.5:
		return "Excellent"
	case score >= 8.0:
		return "Very Good"
	case score >= 7.0:
		return "Good"
	default:
		return "Pleasant"
	}
}

// pseudoScore generates a deterministic guest score (7.5-9.5) from the hotel name
// since Amadeus free tier does not provide guest ratings.
func pseudoScore(name string) float64 {
	h := int64(0)
	for _, c := range name {
		h = h*31 + int64(c)
	}
	if h < 0 {
		h = -h
	}
	return float64(75+h%21) / 10.0 // 7.5 - 9.5
}

// titleCase converts an ALL-CAPS hotel name to Title Case.
func titleCase(s string) string {
	words := strings.Fields(strings.ToLower(s))
	skip := map[string]bool{
		"de": true, "du": true, "le": true, "la": true, "les": true,
		"et": true, "the": true, "a": true, "an": true, "and": true,
		"of": true, "in": true, "at": true, "by": true,
	}
	for i, w := range words {
		if i == 0 || !skip[w] {
			if len(w) > 0 {
				words[i] = strings.ToUpper(string(w[0])) + w[1:]
			}
		}
	}
	return strings.Join(words, " ")
}

// FetchHotelsByCity fetches real hotel listings with live prices from Amadeus.
// Returns (nil, err) on failure so caller can fall back to mock.
func (ac *AmadeusClient) FetchHotelsByCity(city, cityCode, checkIn, checkOut, adults, rooms string) ([]HotelData, error) {
	if adults == "" {
		adults = "1"
	}
	if rooms == "" {
		rooms = "1"
	}

	// Step 1: get hotel IDs for the city
	hotelResp, err := ac.doGet("/v1/reference-data/locations/hotels/by-city", map[string]string{
		"cityCode":    cityCode,
		"radius":      "5",
		"radiusUnit":  "KM",
		"hotelSource": "ALL",
	})
	if err != nil {
		return nil, fmt.Errorf("hotel list: %w", err)
	}
	defer hotelResp.Body.Close()
	if hotelResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hotel list: status %d", hotelResp.StatusCode)
	}

	var hotelList amadeusHotelListResponse
	if err := json.NewDecoder(hotelResp.Body).Decode(&hotelList); err != nil {
		return nil, fmt.Errorf("hotel list decode: %w", err)
	}
	if len(hotelList.Data) == 0 {
		return nil, fmt.Errorf("no hotels found in %s", cityCode)
	}

	// Cap at 20 IDs to stay within a single offers call
	limit := 20
	if len(hotelList.Data) < limit {
		limit = len(hotelList.Data)
	}
	nameMap := make(map[string]string, limit)
	starsMap := make(map[string]int, limit)
	ids := make([]string, 0, limit)
	for _, h := range hotelList.Data[:limit] {
		ids = append(ids, h.HotelId)
		nameMap[h.HotelId] = h.Name
		stars, _ := strconv.Atoi(h.Rating)
		starsMap[h.HotelId] = stars
	}

	// Step 2: get live prices for those hotels
	offersResp, err := ac.doGet("/v3/shopping/hotel-offers", map[string]string{
		"hotelIds":     strings.Join(ids, ","),
		"adults":       adults,
		"checkInDate":  checkIn,
		"checkOutDate": checkOut,
		"currency":     "USD",
		"roomQuantity": rooms,
		"bestRateOnly": "true",
	})
	if err != nil {
		return nil, fmt.Errorf("hotel offers: %w", err)
	}
	defer offersResp.Body.Close()
	if offersResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hotel offers: status %d", offersResp.StatusCode)
	}

	var offersPayload amadeusHotelOffersResponse
	if err := json.NewDecoder(offersResp.Body).Decode(&offersPayload); err != nil {
		return nil, fmt.Errorf("hotel offers decode: %w", err)
	}

	hotels := make([]HotelData, 0, len(offersPayload.Data))
	for i, item := range offersPayload.Data {
		if !item.Available || len(item.Offers) == 0 {
			continue
		}
		priceStr := item.Offers[0].Price.Total
		if priceStr == "" {
			priceStr = item.Offers[0].Price.Base
		}
		price, _ := strconv.ParseFloat(priceStr, 64)
		if price == 0 {
			continue
		}

		rawName := nameMap[item.Hotel.HotelId]
		if rawName == "" {
			rawName = item.Hotel.Name
		}
		name := titleCase(rawName)

		stars := starsMap[item.Hotel.HotelId]
		if stars == 0 {
			stars, _ = strconv.Atoi(item.Hotel.Rating)
		}
		if stars == 0 {
			stars = 3
		}

		score := pseudoScore(name)
		photo := cityPhoto(city, i)

		hotels = append(hotels, HotelData{
			HotelID:    i + 1,
			HotelName:  name,
			Price:      price,
			Currency:   "USD",
			Rating:     score,
			RatingWord: amadeusRatingWord(score),
			PhotoURL:   photo,
			Stars:      stars,
		})
	}
	if len(hotels) == 0 {
		return nil, fmt.Errorf("amadeus returned no available offers for %s", cityCode)
	}
	return hotels, nil
}
