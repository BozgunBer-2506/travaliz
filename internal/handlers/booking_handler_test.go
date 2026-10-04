package handlers

import "testing"

func TestPlaceholderBookingNeedsNoCard(t *testing.T) {
	req := BookingRequest{
		Type: "hotel", FirstName: "Test", LastName: "Guest",
		Email: "guest@example.com", Phone: "+49123456789",
		Price: 1234.56, Currency: "USD",
	}
	if err := validateBooking(&req); err != nil {
		t.Fatalf("cardless request rejected: %v", err)
	}
	req.Price = 0
	if err := validateBooking(&req); err == nil {
		t.Fatal("invalid estimate accepted")
	}
}

func TestLast4(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", ""}, {"abc", ""}, {"123", ""},
		{"4111111111111234", "1234"},
		{"4111 1111 1111 1234", "1234"},
	} {
		if got := last4(tc.input); got != tc.want {
			t.Errorf("last4 returned %q; want %q", got, tc.want)
		}
	}
}
