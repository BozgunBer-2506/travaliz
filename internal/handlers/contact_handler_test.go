package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

type contactTransport func(*http.Request) (*http.Response, error)

func (f contactTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// contactHarness swaps the Resend transport for a recorder; no real email is
// ever sent from tests.
func contactHarness(t *testing.T, status int, fail bool) *[]contactEmail {
	t.Helper()
	t.Setenv("RESEND_API_KEY", "re_test_offline")
	var sent []contactEmail
	previous := contactHTTPClient.Transport
	contactHTTPClient.Transport = contactTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != resendEndpoint || r.Header.Get("Authorization") != "Bearer re_test_offline" {
			t.Errorf("unexpected outbound request: %s", r.URL)
		}
		var msg contactEmail
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			t.Fatal(err)
		}
		sent = append(sent, msg)
		if fail {
			return nil, errors.New("network down")
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"x"}`))}, nil
	})
	t.Cleanup(func() { contactHTTPClient.Transport = previous })
	return &sent
}

func postContact(body, contentType string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	(&TravelHandler{}).ContactHandler(w, r)
	return w
}

func TestContactSendsOnlyToBusinessInboxWithEscapedHTML(t *testing.T) {
	sent := contactHarness(t, 200, false)
	w := postContact(`{"name":"<img src=x onerror=alert(1)>","email":"visitor@example.com",
		"message":"Hi <script>alert('x')</script>\n<a href=\"https://evil.test\">click</a> & more",
		"to":"victim@example.com","recipient":"victim@example.com","bcc":["victim@example.com"]}`, "application/json")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("valid message rejected: %d %s", w.Code, w.Body)
	}
	if len(*sent) != 1 {
		t.Fatalf("expected exactly one email (no auto-reply), got %d", len(*sent))
	}
	msg := (*sent)[0]
	if len(msg.To) != 1 || msg.To[0] != contactRecipient {
		t.Fatalf("recipient manipulated: %v", msg.To)
	}
	if msg.ReplyTo != "visitor@example.com" {
		t.Fatalf("reply-to = %q", msg.ReplyTo)
	}
	for _, raw := range []string{"<img", "<script", "<a href", "onerror=alert(1)>"} {
		if strings.Contains(msg.HTML, raw) {
			t.Fatalf("unescaped user HTML %q in email body: %s", raw, msg.HTML)
		}
	}
	for _, want := range []string{"&lt;img src=x onerror=alert(1)&gt;", "&lt;script&gt;", "&amp; more", "<br>"} {
		if !strings.Contains(msg.HTML, want) {
			t.Fatalf("email body missing %q: %s", want, msg.HTML)
		}
	}
}

func TestContactRejectsInvalidInputWithoutSending(t *testing.T) {
	sent := contactHarness(t, 200, false)
	long := func(n int) string { return strings.Repeat("a", n) }
	for _, tc := range []struct {
		name, body, contentType string
		code                    int
	}{
		{"wrong content type", `{"name":"A","email":"a@example.com","message":"hi"}`, "text/plain", 415},
		{"no content type", `{"name":"A","email":"a@example.com","message":"hi"}`, "", 415},
		{"malformed json", `{"name":`, "application/json", 400},
		{"oversized body", `{"name":"A","email":"a@example.com","message":"` + long(contactMaxBody) + `"}`, "application/json", 413},
		{"missing message", `{"name":"A","email":"a@example.com","message":"  "}`, "application/json", 422},
		{"no at sign", `{"name":"A","email":"not-an-email","message":"hi"}`, "application/json", 422},
		{"no domain dot", `{"name":"A","email":"a@localhost","message":"hi"}`, "application/json", 422},
		{"display name", `{"name":"A","email":"Eve <a@example.com>","message":"hi"}`, "application/json", 422},
		{"address list", `{"name":"A","email":"a@example.com, b@example.com","message":"hi"}`, "application/json", 422},
		{"header injection", `{"name":"A","email":"a@example.com\r\nBcc: b@example.com","message":"hi"}`, "application/json", 422},
		{"newline in name", `{"name":"A\nBcc: b@example.com","email":"a@example.com","message":"hi"}`, "application/json", 422},
		{"name too long", `{"name":"` + long(contactMaxName+1) + `","email":"a@example.com","message":"hi"}`, "application/json", 422},
		{"email too long", `{"name":"A","email":"` + long(250) + `@example.com","message":"hi"}`, "application/json", 422},
		{"message too long", `{"name":"A","email":"a@example.com","message":"` + long(contactMaxMessage+1) + `"}`, "application/json", 422},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if w := postContact(tc.body, tc.contentType); w.Code != tc.code {
				t.Fatalf("HTTP %d, want %d: %s", w.Code, tc.code, w.Body)
			}
		})
	}
	r := httptest.NewRequest(http.MethodGet, "/contact", nil)
	w := httptest.NewRecorder()
	(&TravelHandler{}).ContactHandler(w, r)
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("GET: HTTP %d allow=%q", w.Code, w.Header().Get("Allow"))
	}
	if len(*sent) != 0 {
		t.Fatalf("invalid requests sent %d email(s)", len(*sent))
	}
}

func TestContactDeliveryFailuresAreReported(t *testing.T) {
	body := `{"name":"Ada","email":"ada@example.com","message":"Hello"}`
	for _, tc := range []struct {
		name   string
		status int
		fail   bool
	}{{"provider error", 500, false}, {"provider rejects", 422, false}, {"network error", 0, true}} {
		t.Run(tc.name, func(t *testing.T) {
			sent := contactHarness(t, tc.status, tc.fail)
			var logs strings.Builder
			log.SetOutput(&logs)
			t.Cleanup(func() { log.SetOutput(os.Stderr) })
			w := postContact(body, "application/json")
			for _, secret := range []string{"re_test_offline", "ada@example.com", "Ada", "Hello"} {
				if strings.Contains(logs.String(), secret) {
					t.Fatalf("log leaks %q: %s", secret, logs.String())
				}
			}
			if w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), `"ok":true`) {
				t.Fatalf("failed delivery reported as HTTP %d %s", w.Code, w.Body)
			}
			if len(*sent) != 1 {
				t.Fatalf("expected one attempt, got %d", len(*sent))
			}
		})
	}
	t.Run("not configured", func(t *testing.T) {
		sent := contactHarness(t, 200, false)
		t.Setenv("RESEND_API_KEY", "")
		if w := postContact(body, "application/json"); w.Code != http.StatusServiceUnavailable {
			t.Fatalf("HTTP %d", w.Code)
		}
		if len(*sent) != 0 {
			t.Fatal("sent without a key")
		}
	})
}
