package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
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

func sendEmail(apiKey, from, to, subject, html string) error {
	body := map[string]interface{}{
		"from":    from,
		"to":      []string{to},
		"subject": subject,
		"html":    html,
	}
	payload, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", "https://api.resend.com/emails", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("resend error: %d", resp.StatusCode)
	}
	return nil
}

func (h *TravelHandler) ContactHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ContactResponse{Error: "method not allowed"})
		return
	}

	var req ContactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ContactResponse{Error: "invalid request"})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Email = strings.TrimSpace(req.Email)
	req.Message = strings.TrimSpace(req.Message)

	if req.Name == "" || !strings.Contains(req.Email, "@") || req.Message == "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(ContactResponse{Error: "all fields are required"})
		return
	}

	apiKey := os.Getenv("RESEND_API_KEY")
	if apiKey == "" {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ContactResponse{Error: "email service not configured"})
		return
	}

	if err := sendEmail(apiKey, "TravelMirror <noreply@thebozgun.com>", "contact@thebozgun.com",
		fmt.Sprintf("New message from %s", req.Name),
		fmt.Sprintf(`<p><strong>Name:</strong> %s</p><p><strong>Email:</strong> %s</p><p><strong>Message:</strong><br>%s</p>`,
			req.Name, req.Email, strings.ReplaceAll(req.Message, "\n", "<br>")),
	); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ContactResponse{Error: "failed to send email"})
		return
	}

	// Confirmation mail to user
	sendEmail(apiKey, "TravelMirror <noreply@thebozgun.com>", req.Email,
		"We received your message!",
		fmt.Sprintf(`
			<p>Hi %s,</p>
			<p>Thanks for reaching out! We received your message and will get back to you within 24 hours.</p>
			<br>
			<p style="color:#6366f1;font-weight:bold">TravelMirror</p>
			<p style="color:#94a3b8;font-size:12px">This is an automated confirmation. Please do not reply to this email.</p>
		`, req.Name),
	)

	json.NewEncoder(w).Encode(ContactResponse{OK: true})
}
