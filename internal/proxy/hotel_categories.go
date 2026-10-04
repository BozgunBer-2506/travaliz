package proxy

import "errors"

type hotelCategory struct {
	title        string
	terms        string
	destinations []string
}

// Fixed destinations bound the number of paid searches and prevent arbitrary fan-out.
var hotelCategories = map[string]hotelCategory{
	"city":       {"City Hotels", "city centre hotels", []string{"London", "Paris", "Berlin"}},
	"beach":      {"Beach Resorts", "beach resorts", []string{"Antalya", "Bali", "Maldives"}},
	"apartments": {"Apartments", "holiday apartments", []string{"Amsterdam", "Barcelona", "Rome"}},
	"villas":     {"Private Villas", "private villas", []string{"Tuscany", "Bali", "Antalya"}},
	"budget":     {"Hostels & Budget", "budget hotels hostels", []string{"Prague", "Berlin", "Bangkok"}},
	"boutique":   {"Boutique Hotels", "boutique hotels", []string{"Florence", "Paris", "Istanbul"}},
	"cabins":     {"Cabins & Chalets", "cabins chalets", []string{"Lapland", "Swiss Alps", "Tyrol"}},
}

func (pc *ProxyClient) FetchHotelCategory(category, checkIn, checkOut, adults, children, rooms string, ages ...string) (string, []HotelData, error) {
	config, ok := hotelCategories[category]
	if !ok {
		return "Hotel category", nil, errors.New("Please select a supported hotel category.")
	}
	groups := make([][]HotelData, len(config.destinations))
	var firstErr error
	for i, destination := range config.destinations {
		hotels, err := pc.fetchHotelQuery(destination, destination+" "+config.terms, checkIn, checkOut, adults, children, rooms, ages...)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			// A quota response means further provider requests cannot succeed and
			// would spend needless calls. Preserve any results already collected.
			if errors.Is(err, ErrQuota) {
				break
			}
			continue
		}
		groups[i] = hotels
	}
	// Interleave destinations so the first screen contains a mix of locations.
	results := []HotelData{}
	for row := 0; ; row++ {
		added := false
		for _, group := range groups {
			if row < len(group) {
				hotel := group[row]
				hotel.HotelID = len(results) + 1
				results = append(results, hotel)
				added = true
			}
		}
		if !added {
			break
		}
	}
	if firstErr != nil && len(results) > 0 {
		return config.title, results, errors.New("Some destinations could not be loaded. Showing available results.")
	}
	return config.title, results, firstErr
}
