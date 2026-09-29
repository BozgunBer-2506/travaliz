package proxy

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type categoryTransport func(*http.Request) (*http.Response, error)

func (f categoryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHotelCategories(t *testing.T) {
	for category, config := range hotelCategories {
		t.Run(category, func(t *testing.T) {
			pc := NewProxyClient("test-key")
			calls := 0
			failed := false
			pc.HTTPClient.Transport = categoryTransport(func(r *http.Request) (*http.Response, error) {
				destination := config.destinations[calls%3]
				calls++
				if got := r.URL.Query().Get("q"); got != destination+" "+config.terms {
					t.Fatalf("unexpected query %q", got)
				}
				body := fmt.Sprintf(`{"search_metadata":{"status":"Success"},"properties":[{"name":%q,"rate_per_night":{"extracted_lowest":90}},{"name":%q,"rate_per_night":{"extracted_lowest":100}}]}`, destination+" Hotel A", destination+" Hotel B")
				status := 200
				if failed && destination == config.destinations[0] {
					status = 429
					body = `{"error":"quota exceeded"}`
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			start := time.Now().AddDate(0, 0, 30)
			search := func() (string, []HotelData, error) {
				return pc.FetchHotelCategory(category, start.Format("2006-01-02"), start.AddDate(0, 0, 2).Format("2006-01-02"), "2", "0", "1")
			}
			title, hotels, err := search()
			if err != nil || title != config.title || calls != 3 || len(hotels) != 6 {
				t.Fatalf("unexpected result: %s %d %d %v", title, calls, len(hotels), err)
			}
			for i, hotel := range hotels {
				if hotel.Destination != config.destinations[i%3] || hotel.HotelID != i+1 {
					t.Fatal("destinations not interleaved or IDs duplicated")
				}
			}
			failed = true
			_, hotels, err = search()
			if err == nil || len(hotels) != 4 {
				t.Fatal("partial failure lost results or warning")
			}
			_, _, err = pc.FetchHotelCategory("unknown", "", "", "", "", "")
			if err == nil || calls != 6 {
				t.Fatal("unknown category consumed quota")
			}
		})
	}
}
