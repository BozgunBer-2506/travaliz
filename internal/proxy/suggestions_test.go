package proxy

import "testing"

func TestHotelDestinationMatching(t *testing.T) {
	pc := &ProxyClient{}
	for _, tc := range []struct{ query, city string }{
		{"İst", "Istanbul"}, {"ızm", "Izmir"}, {"Münih", "Munich"},
		{"Londra", "London"}, {"  ber  ", "Berlin"}, {"Duss", "Düsseldorf"},
	} {
		results, err := pc.SearchHotelDestinations(tc.query)
		if err != nil || len(results) != 1 || results[0].Name != tc.city {
			t.Errorf("%q: got %+v, %v", tc.query, results, err)
		}
	}
	for _, tc := range []struct {
		name, id string
		valid    bool
	}{
		{"İstanbul", "Istanbul", true}, {"Londra", "", true},
		{"Berlin", "Berlin", true}, {"Berlin", "Paris", false},
		{"Berlinn", "", false}, {"Ber", "", false}, {"", "", false},
	} {
		_, valid := ResolveHotelDestination(tc.name, tc.id)
		if valid != tc.valid {
			t.Errorf("%+v: matched=%v", tc, valid)
		}
	}
}
