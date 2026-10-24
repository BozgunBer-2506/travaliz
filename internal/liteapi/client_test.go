package liteapi

import (
	"math"
	"testing"
)

func TestRateQueryRejectsUnsupportedGuests(t *testing.T) {
	valid := RateQuery{Occupancies: []Occupancy{{Adults: 2, Children: []int{0, 17}}},
		Checkin: "2099-11-10", Checkout: "2099-11-13", Currency: "EUR", GuestNationality: "DE"}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*RateQuery){
		func(q *RateQuery) { q.Occupancies = nil },
		func(q *RateQuery) { q.Occupancies = []Occupancy{{Adults: 1}, {Adults: 1}} },
		func(q *RateQuery) { q.Occupancies = []Occupancy{{Adults: 0}} },
		func(q *RateQuery) { q.Occupancies = []Occupancy{{Adults: 10}} },
		func(q *RateQuery) { q.Occupancies = []Occupancy{{Adults: 1, Children: []int{-1}}} },
		func(q *RateQuery) { q.Occupancies = []Occupancy{{Adults: 1, Children: []int{18}}} },
		func(q *RateQuery) { q.Occupancies = []Occupancy{{Adults: 1, Children: make([]int, 7)}} },
		func(q *RateQuery) { q.GuestNationality = "ZZ" },
		func(q *RateQuery) { q.GuestNationality = "" },
		func(q *RateQuery) { q.Currency = "INVALID" },
		func(q *RateQuery) { q.Checkout = q.Checkin },
	} {
		q := valid
		change(&q)
		if err := q.Validate(); err == nil {
			t.Fatalf("invalid query accepted: %+v", q)
		}
		// Must fail before credentials or transport are consulted.
		if _, err := (&Client{}).FetchRates([]string{"hotel"}, q); err == nil || err.Error() == "liteapi: LITEAPI_API_KEY is not configured" {
			t.Fatalf("invalid query reached transport: %v", err)
		}
	}
}

func TestCheapestOfferRejectsInvalidAndMixedPrices(t *testing.T) {
	valid := RoomType{OfferID: "offer", Rates: []Rate{{RateID: "rate"}}, OfferRetailRate: Amount{100.01, "GBP"}}
	for _, amount := range []float64{-1, 0, math.NaN(), math.Inf(1)} {
		invalid := valid
		invalid.OfferRetailRate.Amount = amount
		got, ok := (HotelRates{RoomTypes: []RoomType{invalid, valid}}).CheapestOffer()
		if !ok || got.OfferRetailRate != valid.OfferRetailRate {
			t.Fatalf("bad price selected: %+v", got)
		}
	}
	for _, invalid := range []RoomType{
		{}, {OfferID: "empty", OfferRetailRate: Amount{1, "GBP"}},
		{OfferID: "bad-currency", Rates: valid.Rates, OfferRetailRate: Amount{1, ""}},
		{OfferID: "bad-currency", Rates: valid.Rates, OfferRetailRate: Amount{1, "123"}},
		{OfferID: "two-rooms", Rates: []Rate{{RateID: "a"}, {RateID: "b"}}, OfferRetailRate: Amount{1, "GBP"}},
	} {
		if _, ok := (HotelRates{RoomTypes: []RoomType{invalid}}).CheapestOffer(); ok {
			t.Fatal("invalid offer accepted")
		}
	}
	other := valid
	other.OfferRetailRate = Amount{1, "USD"}
	if _, ok := (HotelRates{RoomTypes: []RoomType{valid, other}}).CheapestOffer(); ok {
		t.Fatal("different currencies were compared")
	}
}
