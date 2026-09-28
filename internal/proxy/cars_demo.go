package proxy

import (
	"math"
	"strings"
)

var carTemplates = []struct {
	name          string
	category      string
	categoryColor string
	seats         int
	doors         int
	transmission  string
	basePrice     float64
	provider      string
	providerLogo  string
	badge         string
}{
	{"Toyota Yaris", "Economy", "#4f46e5", 5, 4, "Automatic", 29, "Hertz", "hertz.com", "Free cancel"},
	{"Volkswagen Golf", "Compact", "#0284c7", 5, 4, "Automatic", 38, "Avis", "avis.com", "Free cancel"},
	{"Toyota RAV4", "SUV", "#b45309", 7, 5, "Automatic", 62, "Sixt", "sixt.com", "Unlimited km"},
	{"Mercedes-Benz C-Class", "Luxury", "#7c3aed", 5, 4, "Automatic", 145, "Europcar", "europcar.com", "Free cancel"},
	{"Fiat 500", "Economy", "#4f46e5", 4, 3, "Manual", 24, "Budget", "budget.com", "-20% today"},
	{"Ford Mustang", "Sports", "#dc2626", 4, 2, "Automatic", 110, "Hertz", "hertz.com", "Hot deal"},
	{"BMW X5", "SUV", "#b45309", 7, 5, "Automatic", 185, "Sixt", "sixt.com", "Premium"},
	{"Renault Clio", "Economy", "#4f46e5", 5, 4, "Manual", 22, "Europcar", "europcar.com", "Free cancel"},
	{"Audi A4", "Compact", "#0284c7", 5, 4, "Automatic", 75, "Avis", "avis.com", "Free cancel"},
}

// cityPriceMultiplier returns a cost-of-living multiplier for a city.
func cityPriceMultiplier(city string) float64 {
	expensive := map[string]float64{
		"dubai": 1.9, "singapore": 1.8, "new york": 1.7, "london": 1.6,
		"zurich": 1.8, "geneva": 1.75, "oslo": 1.65, "stockholm": 1.5,
		"paris": 1.45, "amsterdam": 1.4, "copenhagen": 1.55, "tokyo": 1.35,
		"sydney": 1.4, "melbourne": 1.35, "toronto": 1.3, "vancouver": 1.3,
		"barcelona": 1.1, "rome": 1.05, "madrid": 1.05, "berlin": 1.1,
		"munich": 1.2, "vienna": 1.15, "prague": 0.75, "budapest": 0.7,
		"warsaw": 0.72, "bucharest": 0.65, "sofia": 0.6, "athens": 0.85,
		"istanbul": 0.8, "cairo": 0.55, "bangkok": 0.6, "bali": 0.55,
		"lisbon": 0.9, "porto": 0.85, "miami": 1.25, "los angeles": 1.3,
		"chicago": 1.2, "dallas": 1.1, "mexico city": 0.7, "bogota": 0.65,
	}
	lower := strings.ToLower(city)
	if m, ok := expensive[lower]; ok {
		return m
	}
	// deterministic fallback for unknown cities
	h := int64(0)
	for _, c := range lower {
		h = h*31 + int64(c)
	}
	if h < 0 {
		h = -h
	}
	return 0.7 + float64(h%60)/100.0 // 0.70 - 1.30
}

// FetchCarsByCity returns deterministic mock car listings seeded by city.
func (pc *ProxyClient) FetchCarsByCity(city string) []CarData {
	seed := uint64(0)
	for _, c := range city {
		seed = seed*31 + uint64(c)
	}
	mult := cityPriceMultiplier(city)
	cars := make([]CarData, 0, len(carTemplates))
	for i := range carTemplates {
		idx := (int(seed%uint64(len(carTemplates))) + i*7) % len(carTemplates)
		t := carTemplates[idx]
		// per-car variation ±15% based on seed+position
		varPct := 1.0 + float64(((int(seed%31)+i*13)%31)-15)/100.0
		price := math.Round(t.basePrice * mult * varPct)
		if price < 10 {
			price = 10
		}
		orig := 0.0
		if t.badge == "-20% today" {
			orig = math.Round(price / 0.8)
		}
		cars = append(cars, CarData{
			CarID:         int(seed%9000) + 1000 + i,
			CarName:       t.name,
			Category:      t.category,
			CategoryColor: t.categoryColor,
			Seats:         t.seats,
			Doors:         t.doors,
			Transmission:  t.transmission,
			PricePerDay:   price,
			OriginalPrice: orig,
			Currency:      "USD",
			Provider:      t.provider,
			ProviderLogo:  "https://logo.clearbit.com/" + t.providerLogo,
			Badge:         t.badge,
		})
	}
	return cars
}
