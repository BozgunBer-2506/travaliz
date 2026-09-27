package proxy

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func testClient(t *testing.T, status int, body string) (*ProxyClient, *[]url.Values) {
	t.Helper()
	calls := []url.Values{}
	pc := NewProxyClient("secret-for-test")
	pc.HTTPClient.Transport = testTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "serpapi.com" || r.URL.Path != "/search.json" {
			t.Fatalf("unexpected provider: %s", r.URL.Host)
		}
		q := r.URL.Query()
		if q.Get("api_key") != "secret-for-test" || q.Get("no_cache") == "true" {
			t.Fatal("bad credentials/cache configuration")
		}
		calls = append(calls, q)
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	return pc, &calls
}
func dates() (string, string) {
	d := time.Now().AddDate(0, 0, 30)
	return d.Format("2006-01-02"), d.AddDate(0, 0, 2).Format("2006-01-02")
}

const flightJSON = `{"search_metadata":{"status":"Success","google_flights_url":"https://www.google.com/travel/flights?test=1"},"best_flights":[{"price":240,"total_duration":125,"flights":[{"departure_airport":{"id":"BER","name":"Berlin","time":"2030-01-01 08:00"},"arrival_airport":{"id":"LHR","name":"London","time":"2030-01-01 09:05"},"airline":"Test Air"}]}]}`

func TestHotelSearchMapsRealDataAndMakesOneCall(t *testing.T) {
	pc, calls := testClient(t, 200, `{"search_metadata":{"status":"Success"},"properties":[{"name":"Real Hotel","link":"https://example.com/hotel","rate_per_night":{"extracted_lowest":100.5},"total_rate":{"extracted_lowest":201},"overall_rating":4.5,"extracted_hotel_class":4,"images":[{"thumbnail":"https://example.com/image.jpg"}]},{"name":"Missing price"}]}`)
	start, end := dates()
	got, err := pc.FetchHotels("Berlin", "existing-id", start, end, "2", "1", "1", "7")
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
	h := got[0]
	if h.Price != 100.5 || h.TotalPrice != 201 || h.Rating != 9 || h.Stars != 4 || h.BookingURL != "https://example.com/hotel" {
		t.Fatalf("incorrect mapping: %+v", h)
	}
	if len(*calls) != 1 || (*calls)[0].Get("children_ages") != "7" || (*calls)[0].Get("q") != "Berlin hotels" {
		t.Fatal("unexpected calls")
	}
}
func TestFlightSearchTypesUseOneCall(t *testing.T) {
	start, end := dates()
	for _, kind := range []string{"oneway", "round", "multi"} {
		t.Run(kind, func(t *testing.T) {
			pc, calls := testClient(t, 200, flightJSON)
			legs := []FlightSearchLeg{{"BER", "LHR", start}}
			back := ""
			wantType := "2"
			if kind == "round" {
				back = end
				wantType = "1"
			}
			if kind == "multi" {
				legs = append(legs, FlightSearchLeg{"LHR", "JFK", end})
				wantType = "3"
			}
			got, err := pc.FetchItinerary(legs, back, "2", "0", "business")
			if err != nil || len(got) != 1 {
				t.Fatalf("got %v, %v", got, err)
			}
			if got[0].Price != 240 || got[0].FromCode != "BER" || got[0].DurationMinutes != 5 || got[0].DepartTime != "08:00" || !strings.Contains(got[0].PriceLabel, "all travellers") {
				t.Fatalf("incorrect mapping: %+v", got[0])
			}
			if len(*calls) != 1 || (*calls)[0].Get("type") != wantType || (*calls)[0].Get("travel_class") != "3" {
				t.Fatal("incorrect request")
			}
			if kind == "multi" && ((*calls)[0].Get("multi_city_json") == "" || (*calls)[0].Get("outbound_date") != "") {
				t.Fatal("incorrect multi-city request")
			}
		})
	}
}
func TestErrorsNeverFallBackToMockOrAnotherProvider(t *testing.T) {
	start, end := dates()
	for _, tc := range []struct {
		status int
		body   string
		want   error
	}{
		{429, `{"error":"quota"}`, ErrQuota}, {401, `{"error":"key secret-for-test"}`, ErrUnavailable},
		{200, `{"error":"invalid secret-for-test"}`, ErrUnavailable}, {200, `not-json`, ErrUnavailable},
		{200, `{"search_metadata":{"status":"Processing"}}`, ErrUnavailable},
	} {
		pc, calls := testClient(t, tc.status, tc.body)
		got, err := pc.FetchHotels("Berlin", "", start, end, "2", "0", "1")
		if !errors.Is(err, tc.want) || len(got) != 0 || len(*calls) != 1 || strings.Contains(err.Error(), "secret-for-test") {
			t.Fatalf("unexpected result: %v %v", got, err)
		}
		flights, err := pc.FetchFlights("BER", "", "LHR", "", start, "", "1", "0", "economy")
		if !errors.Is(err, tc.want) || len(flights) != 0 || len(*calls) != 2 {
			t.Fatal("flight error hidden or retried")
		}
	}
}
func TestInvalidQueriesAndSuggestionsDoNotConsumeQuota(t *testing.T) {
	pc, calls := testClient(t, 200, flightJSON)
	start, end := dates()
	if _, err := pc.FetchHotels("Berlin", "", start, end, "2", "0", "2"); err == nil {
		t.Fatal("multiple rooms silently ignored")
	}
	if _, err := pc.FetchHotels("Berlin", "", start, end, "2", "1", "1"); err == nil {
		t.Fatal("missing child age ignored")
	}
	if _, err := pc.FetchFlights("Berlin", "", "LHR", "", start, "", "1", "0", "economy"); err == nil {
		t.Fatal("invalid airport accepted")
	}
	if _, err := pc.FetchFlights("BER", "", "LHR", "", start, "", "8", "8", "economy"); err == nil {
		t.Fatal("invalid traveller count accepted")
	}
	airports, _ := pc.SearchAirports("Berlin")
	cities, _ := pc.SearchHotelDestinations("Berlin")
	if len(airports) == 0 || len(cities) == 0 || len(*calls) != 0 {
		t.Fatal("suggestions missing or paid request used")
	}
	data, _ := pc.FetchTravelData()
	if len(data) != 0 {
		t.Fatal("mock endpoint still serves offers")
	}
}
func TestEmptyResultsAndMissingKey(t *testing.T) {
	pc, calls := testClient(t, 200, `{"search_metadata":{"status":"Success"}}`)
	start, end := dates()
	got, err := pc.FetchHotels("Berlin", "", start, end, "2", "0", "1")
	if err != nil || len(got) != 0 {
		t.Fatal("empty results fabricated")
	}
	pc.apiKey = ""
	_, err = pc.FetchHotels("Berlin", "", start, end, "2", "0", "1")
	if !errors.Is(err, ErrUnavailable) || len(*calls) != 1 {
		t.Fatal("missing key triggered request")
	}
}
func TestNetworkErrorsAndUnsafeLinks(t *testing.T) {
	pc := NewProxyClient("secret-for-test")
	pc.HTTPClient.Transport = testTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("secret-for-test") })
	_, err := pc.search(url.Values{"engine": {"google_hotels"}})
	if err != ErrUnavailable {
		t.Fatal("raw network error exposed")
	}
	for _, s := range []string{"javascript:alert(1)", "http://example.com", "https://serpapi.com/search?api_key=secret", "https://example.com/?api_key=secret"} {
		if safeLink(s) != "" {
			t.Fatal("unsafe outbound link")
		}
	}
}

func TestChildAgesReachProvider(t *testing.T) {
	pc, calls := testClient(t, 200, flightJSON)
	start, _ := dates()
	_, err := pc.FetchFlights("BER", "", "LHR", "", start, "", "1", "2", "economy", "1,7")
	if err != nil {
		t.Fatal(err)
	}
	q := (*calls)[0]
	if q.Get("children") != "1" || q.Get("infants_on_lap") != "1" || q.Get("adults") != "1" {
		t.Fatal("infant/child counts not passed correctly")
	}
}
