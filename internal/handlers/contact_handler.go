package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"mime"
	"net/http"
	"net/mail"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// The contact form only ever mails the business inbox below. The visitor's
// address is used as Reply-To so the team can answer, but nothing is ever
// sent to it automatically: an unauthenticated form that mails arbitrary
// addresses is an open relay for spam and phishing under our domain.
const (
	contactFrom      = "travaliz.com <noreply@thebozgun.com>"
	contactRecipient = "contact@thebozgun.com"
	resendEndpoint   = "https://api.resend.com/emails"

	contactMaxBody    = 16 << 10
	contactMaxName    = 100
	contactMaxEmail   = 254
	contactMaxMessage = 5000
)

type ContactRequest struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Message string `json:"message"`
}

type ContactResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type contactEmail struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	ReplyTo string   `json:"reply_to,omitempty"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
}

var contactHTTPClient = &http.Client{Timeout: 10 * time.Second}

// sendEmail posts one message to Resend. Errors carry only the HTTP status,
// never the key, the recipient or the message content.
func sendEmail(apiKey string, msg contactEmail) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return errors.New("resend: could not encode message")
	}
	req, err := http.NewRequest(http.MethodPost, resendEndpoint, bytes.NewReader(payload))
	if err != nil {
		return errors.New("resend: could not build request")
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := contactHTTPClient.Do(req)
	if err != nil {
		return errors.New("resend: transport error")
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("resend: HTTP %d", resp.StatusCode)
	}
	return nil
}

func contactReply(w http.ResponseWriter, status int, resp ContactResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}

// singleLine reports whether s has no control characters (newlines, NUL,
// etc.), which have no place in a name or an address.
func singleLine(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// validContactEmail accepts a bare address only (no display name, no
// comments, no list), so the value used as Reply-To is exactly what the
// visitor typed and nothing more.
func validContactEmail(s string) bool {
	if len(s) > contactMaxEmail || !singleLine(s) {
		return false
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s || addr.Name != "" {
		return false
	}
	at := strings.LastIndex(s, "@")
	return at > 0 && strings.Contains(s[at+1:], ".")
}

func (h *TravelHandler) ContactHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		contactReply(w, http.StatusMethodNotAllowed, ContactResponse{Error: "method not allowed"})
		return
	}
	if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
		contactReply(w, http.StatusUnsupportedMediaType, ContactResponse{Error: "Content-Type must be application/json"})
		return
	}

	var req ContactRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, contactMaxBody)).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			contactReply(w, http.StatusRequestEntityTooLarge, ContactResponse{Error: "Your message is too long."})
			return
		}
		contactReply(w, http.StatusBadRequest, ContactResponse{Error: "invalid request"})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Email = strings.TrimSpace(req.Email)
	req.Message = strings.TrimSpace(req.Message)

	switch {
	case req.Name == "" || req.Email == "" || req.Message == "":
		contactReply(w, http.StatusUnprocessableEntity, ContactResponse{Error: "all fields are required"})
		return
	case !utf8.ValidString(req.Name) || !utf8.ValidString(req.Message):
		contactReply(w, http.StatusUnprocessableEntity, ContactResponse{Error: "invalid characters in message"})
		return
	case utf8.RuneCountInString(req.Name) > contactMaxName || !singleLine(req.Name):
		contactReply(w, http.StatusUnprocessableEntity, ContactResponse{Error: fmt.Sprintf("Please enter a name of up to %d characters.", contactMaxName)})
		return
	case !validContactEmail(req.Email):
		contactReply(w, http.StatusUnprocessableEntity, ContactResponse{Error: "Please enter a valid email address."})
		return
	case utf8.RuneCountInString(req.Message) > contactMaxMessage:
		contactReply(w, http.StatusUnprocessableEntity, ContactResponse{Error: fmt.Sprintf("Please keep your message under %d characters.", contactMaxMessage)})
		return
	}

	apiKey := strings.TrimSpace(os.Getenv("RESEND_API_KEY"))
	if apiKey == "" {
		log.Printf("contact: email service not configured")
		contactReply(w, http.StatusServiceUnavailable, ContactResponse{Error: "The contact form is temporarily unavailable. Please try again later."})
		return
	}

	name, email := html.EscapeString(req.Name), html.EscapeString(req.Email)
	message := strings.ReplaceAll(html.EscapeString(strings.ReplaceAll(req.Message, "\r\n", "\n")), "\n", "<br>")
	err := sendEmail(apiKey, contactEmail{
		From:    contactFrom,
		To:      []string{contactRecipient},
		ReplyTo: req.Email,
		Subject: "New message from " + req.Name,
		HTML:    `<p><strong>Name:</strong> ` + name + `</p><p><strong>Email:</strong> ` + email + `</p><p><strong>Message:</strong><br>` + message + `</p>`,
	})
	if err != nil {
		// Status only: no key, address, name or message content in logs.
		log.Printf("contact: delivery failed: %v", err)
		contactReply(w, http.StatusBadGateway, ContactResponse{Error: "Your message could not be sent. Please try again later."})
		return
	}
	contactReply(w, http.StatusOK, ContactResponse{OK: true})
}
