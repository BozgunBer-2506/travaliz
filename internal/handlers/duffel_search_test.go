package handlers

import "testing"

func TestParseFlightDuration(t *testing.T) {
	for _, tc := range []struct {
		raw            string
		hours, minutes int
	}{
		{"PT5H30M", 5, 30}, {"PT02H26M", 2, 26}, {"PT5H", 5, 0},
		{"PT45M", 0, 45}, {"PT90M", 1, 30}, {"PT0M", 0, 0},
	} {
		h, m, err := parseFlightDuration(tc.raw)
		if err != nil || h != tc.hours || m != tc.minutes {
			t.Fatalf("%s: got %dh %dm, %v", tc.raw, h, m, err)
		}
	}
	for _, raw := range []string{"", "PT", "5H", "PT-1H", "P1D", "PT1H30S", "PT999999999999999999999H"} {
		if _, _, err := parseFlightDuration(raw); err == nil {
			t.Fatalf("accepted invalid/unsupported duration %q", raw)
		}
	}
}
