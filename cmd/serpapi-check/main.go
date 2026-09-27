// serpapi-check is an opt-in, local provider evaluation. It does not change the site.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type result struct {
	Metadata struct {
		Status string `json:"status"`
	} `json:"search_metadata"`
	Error      string   `json:"error"`
	Best       []flight `json:"best_flights"`
	Other      []flight `json:"other_flights"`
	Properties []hotel  `json:"properties"`
}

type flight struct {
	Price *float64 `json:"price"`
}

type hotel struct {
	Rate struct {
		Price *float64 `json:"extracted_lowest"`
	} `json:"rate_per_night"`
}

func queries(date time.Time) []url.Values {
	return []url.Values{
		{"engine": {"google_hotels"}, "q": {"Berlin hotels"}, "check_in_date": {date.Format("2006-01-02")}, "check_out_date": {date.AddDate(0, 0, 2).Format("2006-01-02")}, "adults": {"1"}, "currency": {"USD"}, "hl": {"en"}, "gl": {"us"}},
		{"engine": {"google_flights"}, "departure_id": {"BER"}, "arrival_id": {"LHR"}, "outbound_date": {date.Format("2006-01-02")}, "type": {"2"}, "adults": {"1"}, "currency": {"USD"}, "hl": {"en"}, "gl": {"us"}},
	}
}

// Read only this setting; never source .env as shell code or print its contents.
func apiKey() string {
	if key := strings.TrimSpace(os.Getenv("SERPAPI_API_KEY")); key != "" {
		return key
	}
	data, _ := os.ReadFile(".env")
	for _, line := range strings.Split(string(data), "\n") {
		name, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.TrimSpace(name) == "SERPAPI_API_KEY" {
			return strings.Trim(strings.TrimSpace(value), "\"'")
		}
	}
	return ""
}

func search(client *http.Client, params url.Values, key string) (string, error) {
	q := url.Values{}
	for k, values := range params {
		q[k] = append([]string(nil), values...)
	}
	q.Set("api_key", key)
	resp, err := client.Get("https://serpapi.com/search.json?" + q.Encode())
	if err != nil {
		// net/http errors can contain the full URL, including api_key.
		return "", errors.New("SerpApi network error or timeout; request URL withheld to protect API key")
	}
	defer resp.Body.Close()
	var data result
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&data)
	if resp.StatusCode != http.StatusOK || data.Error != "" {
		message := strings.ReplaceAll(data.Error, key, "[REDACTED]")
		if len(message) > 500 {
			message = message[:500]
		}
		return "", fmt.Errorf("SerpApi HTTP %d: %s", resp.StatusCode, message)
	}
	if decodeErr != nil {
		return "", errors.New("SerpApi returned invalid or oversized JSON")
	}
	if data.Metadata.Status != "Success" {
		return "", errors.New("SerpApi search did not complete successfully; no automatic polling performed")
	}
	prices := []float64{}
	count := len(data.Properties)
	unit := "USD/night (hotel)"
	if params.Get("engine") == "google_flights" {
		count = len(data.Best) + len(data.Other)
		unit = "USD (one-way flight)"
		for _, f := range append(data.Best, data.Other...) {
			if f.Price != nil && *f.Price > 0 {
				prices = append(prices, *f.Price)
			}
		}
	} else {
		for _, h := range data.Properties {
			if h.Rate.Price != nil && *h.Rate.Price > 0 {
				prices = append(prices, *h.Rate.Price)
			}
		}
	}
	if len(prices) == 0 {
		return fmt.Sprintf("HTTP 200: %d results, no usable prices; no mock fallback", count), nil
	}
	lowest := prices[0]
	for _, price := range prices {
		if price < lowest {
			lowest = price
		}
	}
	return fmt.Sprintf("HTTP 200: %d results, %d priced, lowest %.2f %s", count, len(prices), lowest, unit), nil
}

func main() {
	live := flag.Bool("live", false, "Send up to two real searches; may consume free quota. Stops on first error.")
	date := flag.String("date", time.Now().AddDate(0, 0, 30).Format("2006-01-02"), "Departure/check-in date (YYYY-MM-DD)")
	flag.Parse()
	day, err := time.Parse("2006-01-02", *date)
	if err != nil || *date <= time.Now().Format("2006-01-02") {
		fmt.Fprintln(os.Stderr, "Provide a future date in YYYY-MM-DD format.")
		os.Exit(1)
	}
	key := ""
	if *live {
		key = apiKey()
		if key == "" {
			fmt.Fprintln(os.Stderr, "Set SERPAPI_API_KEY in the environment or local .env; no requests sent.")
			os.Exit(1)
		}
	}
	client := &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, q := range queries(day) {
		fmt.Println(q.Get("engine"), q.Encode())
		if !*live {
			continue
		}
		summary, err := search(client, q, key)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(summary)
	}
	if !*live {
		fmt.Println("Dry run: no API requests sent. Use -live explicitly to test the provider.")
	}
}
