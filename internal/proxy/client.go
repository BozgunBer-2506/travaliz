package proxy

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"time"
)

type TravelData struct {
	ID     int     `json:"id"`
	Title  string  `json:"title"`
	Body   string  `json:"body"`
	UserID int     `json:"userId"`
	Price  float64 `json:"price"`
	Rating float64 `json:"rating"`
}

type ProxyClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

func NewProxyClient(baseURL string) *ProxyClient {
	return &ProxyClient{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func simulatePrice() float64 {
	return 50 + rand.Float64()*250
}

func simulateRating() float64 {
	return 3.0 + rand.Float64()*2.0
}

func (pc *ProxyClient) FetchTravelData() ([]TravelData, error) {
	resp, err := pc.HTTPClient.Get(pc.BaseURL + "/posts")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch data from external API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("external API returned non-OK status: %d", resp.StatusCode)
	}

	var data []TravelData
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to decode external API response: %w", err)
	}

	for i := range data {
		data[i].Price = simulatePrice()
		data[i].Rating = simulateRating()
	}

	return data, nil
}
