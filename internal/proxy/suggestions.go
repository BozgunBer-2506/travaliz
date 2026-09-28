package proxy

import "strings"

// A small offline starter list, not a complete airport directory. Any three-letter
// IATA code can also be entered directly; suggestions never consume search quota.
var airports = []FlightDestSuggestion{
	{SkyID: "BER", Name: "Berlin Brandenburg", CityName: "Berlin", CountryName: "Germany"},
	{SkyID: "FRA", Name: "Frankfurt Airport", CityName: "Frankfurt", CountryName: "Germany"},
	{SkyID: "MUC", Name: "Munich Airport", CityName: "Munich", CountryName: "Germany"},
	{SkyID: "HAM", Name: "Hamburg Airport", CityName: "Hamburg", CountryName: "Germany"},
	{SkyID: "DUS", Name: "Düsseldorf Airport", CityName: "Düsseldorf", CountryName: "Germany"},
	{SkyID: "LHR", Name: "Heathrow", CityName: "London", CountryName: "United Kingdom"},
	{SkyID: "LGW", Name: "Gatwick", CityName: "London", CountryName: "United Kingdom"},
	{SkyID: "CDG", Name: "Charles de Gaulle", CityName: "Paris", CountryName: "France"},
	{SkyID: "AMS", Name: "Schiphol", CityName: "Amsterdam", CountryName: "Netherlands"},
	{SkyID: "IST", Name: "Istanbul Airport", CityName: "Istanbul", CountryName: "Turkey"},
	{SkyID: "SAW", Name: "Sabiha Gökçen", CityName: "Istanbul", CountryName: "Turkey"},
	{SkyID: "AYT", Name: "Antalya Airport", CityName: "Antalya", CountryName: "Turkey"},
	{SkyID: "ADB", Name: "Adnan Menderes", CityName: "Izmir", CountryName: "Turkey"},
	{SkyID: "ESB", Name: "Esenboğa", CityName: "Ankara", CountryName: "Turkey"},
	{SkyID: "JFK", Name: "John F. Kennedy", CityName: "New York", CountryName: "United States"},
	{SkyID: "LAX", Name: "Los Angeles International", CityName: "Los Angeles", CountryName: "United States"},
	{SkyID: "DXB", Name: "Dubai International", CityName: "Dubai", CountryName: "United Arab Emirates"},
	{SkyID: "FCO", Name: "Fiumicino", CityName: "Rome", CountryName: "Italy"},
	{SkyID: "BCN", Name: "Barcelona El Prat", CityName: "Barcelona", CountryName: "Spain"},
	{SkyID: "MAD", Name: "Madrid Barajas", CityName: "Madrid", CountryName: "Spain"},
	{SkyID: "VIE", Name: "Vienna International", CityName: "Vienna", CountryName: "Austria"},
	{SkyID: "ZRH", Name: "Zurich Airport", CityName: "Zurich", CountryName: "Switzerland"},
	{SkyID: "SIN", Name: "Changi", CityName: "Singapore", CountryName: "Singapore"},
	{SkyID: "NRT", Name: "Narita", CityName: "Tokyo", CountryName: "Japan"},
	{SkyID: "BKK", Name: "Suvarnabhumi", CityName: "Bangkok", CountryName: "Thailand"},
}

func (pc *ProxyClient) SearchAirports(query string) ([]FlightDestSuggestion, error) {
	results := []FlightDestSuggestion{}
	query = normalizeDestination(query)
	if len(query) < 2 {
		return results, nil
	}
	for _, a := range airports {
		if strings.Contains(strings.ToLower(a.SkyID+" "+a.Name+" "+a.CityName), query) {
			a.EntityID = a.SkyID
			a.PlaceType = "airport"
			results = append(results, a)
			if len(results) == 6 {
				break
			}
		}
	}
	return results, nil
}
func (pc *ProxyClient) SearchHotelDestinations(query string) ([]DestSuggestion, error) {
	results := []DestSuggestion{}
	seen := map[string]bool{}
	query = normalizeDestination(query)
	if len(query) < 2 {
		return results, nil
	}
	for _, a := range airports {
		if !seen[a.CityName] && strings.Contains(normalizeDestination(a.CityName+" "+hotelCityAliases[a.CityName]), query) {
			seen[a.CityName] = true
			results = append(results, DestSuggestion{EntityID: a.CityName, Name: a.CityName, Type: "city", Hierarchy: a.CountryName})
			if len(results) == 6 {
				break
			}
		}
	}
	return results, nil
}

// Aliases reuse the existing offline directory without making paid search calls.
var hotelCityAliases = map[string]string{
	"London": "Londra", "Munich": "München Münih", "Rome": "Roma",
	"Vienna": "Wien Viyana", "Zurich": "Zürich Zürih", "Tokyo": "Tokio",
	"New York": "NYC", "Singapore": "Singapur",
}

func normalizeDestination(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.NewReplacer("ı", "i", "ü", "u", "ö", "o", "ç", "c", "ş", "s", "ğ", "g", "̈", "", "̇", "").Replace(value)
}

// ResolveHotelDestination accepts only exact names/aliases from our directory.
func ResolveHotelDestination(name, entityID string) (DestSuggestion, bool) {
	query := normalizeDestination(name)
	for _, a := range airports {
		if entityID != "" && entityID != a.CityName {
			continue
		}
		matches := query == normalizeDestination(a.CityName)
		for _, alias := range strings.Split(hotelCityAliases[a.CityName], " ") {
			matches = matches || (query != "" && query == normalizeDestination(alias))
		}
		if matches {
			return DestSuggestion{EntityID: a.CityName, Name: a.CityName, Type: "city", Hierarchy: a.CountryName}, true
		}
	}
	return DestSuggestion{}, false
}
