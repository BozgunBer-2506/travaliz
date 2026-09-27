package main

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSearch(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		query, status    int
		wantErr          bool
	}{
		{"hotels", `{"search_metadata":{"status":"Success"},"properties":[{"rate_per_night":{"extracted_lowest":90}},{"rate_per_night":{"extracted_lowest":75}},{}]}`, "3 results, 2 priced, lowest 75.00 USD/night", 0, 200, false},
		{"flights", `{"search_metadata":{"status":"Success"},"best_flights":[{"price":150}],"other_flights":[{"price":100}]}`, "2 results, 2 priced, lowest 100.00 USD", 1, 200, false},
		{"empty", `{"search_metadata":{"status":"Success"}}`, "no usable prices; no mock fallback", 1, 200, false},
		{"quota", `{"error":"quota exhausted test-secret"}`, "HTTP 429: quota exhausted [REDACTED]", 0, 429, true},
		{"api error", `{"error":"invalid key"}`, "HTTP 200: invalid key", 0, 200, true},
		{"malformed", `<html>error</html>`, "invalid or oversized JSON", 0, 200, true},
		{"pending", `{"search_metadata":{"status":"Processing"}}`, "no automatic polling", 1, 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			params := queries(time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC))[tc.query]
			client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				calls++
				q := r.URL.Query()
				if r.URL.Host != "serpapi.com" || q.Get("api_key") != "test-secret" || q.Get("no_cache") == "true" || q.Get("async") == "true" {
					t.Fatal("unexpected request")
				}
				if tc.query == 1 && (q.Get("type") != "2" || q.Get("departure_id") != "BER" || q.Get("outbound_date") != "2030-01-02") {
					t.Fatal("invalid flight parameters")
				}
				if tc.query == 0 && q.Get("check_out_date") != "2030-01-04" {
					t.Fatal("invalid hotel dates")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})}
			got, err := search(client, params, "test-secret")
			if (err != nil) != tc.wantErr {
				t.Fatalf("unexpected error: %v", err)
			}
			if err != nil {
				got = err.Error()
			}
			if !strings.Contains(got, tc.want) || strings.Contains(got, "test-secret") {
				t.Fatalf("unexpected output: %s", got)
			}
			if calls != 1 || params.Get("api_key") != "" {
				t.Fatal("request repeated or parameters mutated")
			}
		})
	}
}

func TestNetworkErrorDoesNotLeakKey(t *testing.T) {
	client := &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { return nil, errors.New("test-secret") })}
	_, err := search(client, queries(time.Now())[0], "test-secret")
	if err == nil || strings.Contains(err.Error(), "test-secret") {
		t.Fatal("network error was not sanitized")
	}
}
