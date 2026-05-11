package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// TravelData represents the structure of the dummy travel API response.
type TravelData struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	UserID int    `json:"userId"`
}

// ProxyClient handles outgoing requests to external APIs.
type ProxyClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewProxyClient initializes a new ProxyClient with a timeout.
func NewProxyClient(baseURL string) *ProxyClient {
	return &ProxyClient{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// FetchTravelData retrieves travel-related data from the external API.
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
	err = json.NewDecoder(resp.Body).Decode(&data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode external API response: %w", err)
	}

	return data, nil
}
