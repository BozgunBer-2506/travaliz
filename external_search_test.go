package main

import (
	"html/template"
	"net/http/httptest"
	"strings"
	"testing"
	"travel-proxy-service/internal/handlers"
)

func TestExternalSearchWithoutAPI(t *testing.T) {
	tmpl := template.Must(template.ParseFS(templateFiles, "templates/*.html"))
	// Nil ProxyClient ensures these searches cannot call a paid API.
	mux := newMux(&handlers.TravelHandler{Templates: tmpl, ExternalSearch: true})
	for _, tc := range []struct{ path, want string }{
		{"/?q=Berlin&checkin=2099-11-10&checkout=2099-11-12&adults=2&children=1&children_ages=8&rooms=2", "age=8&amp;checkin=2099-11-10"},
		{"/?q=Unknown+Town&checkin=2099-11-10&checkout=2099-11-12", "ss=Unknown"},
		{"/?category=beach&checkin=2099-11-10&checkout=2099-11-12", "Search Antalya on Booking.com"},
		{"/flights?fromSky=BER&toSky=LHR&date=2099-11-10", "Search on Google Flights"},
		{"/flights?fromSky=BER&toSky=LHR&date=2099-11-10&returnDate=2099-11-12", "from&#43;LHR&#43;to&#43;BER"},
		{"/flights?tripType=multi&leg0from=BER&leg0to=LHR&leg0date=2099-11-10&leg1from=LHR&leg1to=JFK&leg1date=2099-11-12", "Search on Google Flights"},
		{"/?q=Berlin&checkin=2099-11-12&checkout=2099-11-10", "Check-out must be after check-in."},
		{"/flights?fromSky=BADCODE&toSky=LHR&date=2099-11-10", "Enter valid three-letter airport codes"},
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), tc.want) {
			t.Errorf("%s: missing %q (status %d)", tc.path, tc.want, w.Code)
		}
		if strings.Contains(w.Body.String(), "ZgotmplZ") {
			t.Error("unsafe URL")
		}
	}
}
