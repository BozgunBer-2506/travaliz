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
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal error: %w", err)
	}
	req, err := http.NewRequest("POST", "https://api.resend.com/emails", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("request error: %w", err)
	}
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
	if err := sendEmail(apiKey, "TravelMirror <noreply@thebozgun.com>", req.Email,
		"We received your message — TravelMirror",
		fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="margin:0;padding:0;background:#0d0d1a;font-family:'Helvetica Neue',Arial,sans-serif">
  <table width="100%%" cellpadding="0" cellspacing="0" style="background:#0d0d1a;padding:40px 20px">
    <tr><td align="center">
      <table width="600" cellpadding="0" cellspacing="0" style="max-width:600px;width:100%%">

        <!-- Logo -->
        <tr><td align="center" style="padding-bottom:32px">
          <a href="https://go-web-api-eight.vercel.app" style="text-decoration:none;display:inline-block">
            <table cellpadding="0" cellspacing="0">
              <tr>
                <td style="background:rgba(99,102,241,0.2);border-radius:14px;padding:12px 16px;text-align:center">
                  <span style="font-size:24px">✈</span>
                </td>
                <td style="padding-left:12px">
                  <span style="font-size:22px;font-weight:900;color:#ffffff;letter-spacing:-0.5px">TravelMirror</span>
                </td>
              </tr>
            </table>
          </a>
        </td></tr>

        <!-- Card -->
        <tr><td style="background:#13132b;border-radius:24px;padding:40px;border:1px solid rgba(255,255,255,0.08)">

          <!-- Greeting -->
          <p style="margin:0 0 8px;font-size:22px;font-weight:800;color:#e2e8f0">Hi %s 👋</p>
          <p style="margin:0 0 28px;font-size:15px;color:#94a3b8;line-height:1.6">
            Thanks for getting in touch! We've received your message and our team is already on it.
          </p>

          <!-- Divider -->
          <div style="height:1px;background:rgba(255,255,255,0.08);margin-bottom:28px"></div>

          <!-- What happens next -->
          <p style="margin:0 0 16px;font-size:12px;font-weight:700;color:#6366f1;text-transform:uppercase;letter-spacing:1px">What happens next</p>
          <table cellpadding="0" cellspacing="0" width="100%%">
            <tr>
              <td style="padding:12px 0;border-bottom:1px solid rgba(255,255,255,0.06)">
                <table cellpadding="0" cellspacing="0"><tr>
                  <td style="background:rgba(99,102,241,0.15);border-radius:8px;padding:8px;font-size:16px;text-align:center">📨</td>
                  <td style="padding-left:14px">
                    <p style="margin:0;font-size:13px;font-weight:600;color:#e2e8f0">Message received</p>
                    <p style="margin:2px 0 0;font-size:12px;color:#64748b">Your message has been delivered to our team</p>
                  </td>
                </tr></table>
              </td>
            </tr>
            <tr>
              <td style="padding:12px 0;border-bottom:1px solid rgba(255,255,255,0.06)">
                <table cellpadding="0" cellspacing="0"><tr>
                  <td style="background:rgba(99,102,241,0.15);border-radius:8px;padding:8px;font-size:16px;text-align:center">⏱️</td>
                  <td style="padding-left:14px">
                    <p style="margin:0;font-size:13px;font-weight:600;color:#e2e8f0">Response within 24 hours</p>
                    <p style="margin:2px 0 0;font-size:12px;color:#64748b">We typically respond same day</p>
                  </td>
                </tr></table>
              </td>
            </tr>
            <tr>
              <td style="padding:12px 0">
                <table cellpadding="0" cellspacing="0"><tr>
                  <td style="background:rgba(99,102,241,0.15);border-radius:8px;padding:8px;font-size:16px;text-align:center">✅</td>
                  <td style="padding-left:14px">
                    <p style="margin:0;font-size:13px;font-weight:600;color:#e2e8f0">Reply to your inbox</p>
                    <p style="margin:2px 0 0;font-size:12px;color:#64748b">We'll reach you at %s</p>
                  </td>
                </tr></table>
              </td>
            </tr>
          </table>

          <!-- Divider -->
          <div style="height:1px;background:rgba(255,255,255,0.08);margin:28px 0"></div>

          <!-- CTA -->
          <p style="margin:0 0 20px;font-size:14px;color:#94a3b8;line-height:1.6">
            In the meantime, feel free to explore flights, hotels, and car rentals on TravelMirror.
          </p>
          <a href="https://go-web-api-eight.vercel.app" style="display:inline-block;background:linear-gradient(135deg,#4f46e5,#7c3aed);color:#ffffff;font-size:14px;font-weight:700;padding:14px 28px;border-radius:12px;text-decoration:none">
            Back to TravelMirror →
          </a>

        </td></tr>

        <!-- Footer -->
        <tr><td align="center" style="padding-top:28px">
          <p style="margin:0 0 6px;font-size:12px;color:#334155">© 2026 TravelMirror · All rights reserved</p>
          <p style="margin:0;font-size:11px;color:#1e293b">
            IT &amp; Web · <a href="https://thebozgun.com" style="color:#4f46e5;text-decoration:none">thebozgun.com</a>
          </p>
        </td></tr>

      </table>
    </td></tr>
  </table>
</body>
</html>`, req.Name, req.Email),
	); err != nil {
		fmt.Printf("confirmation email failed for %s: %v\n", req.Email, err)
	}

	json.NewEncoder(w).Encode(ContactResponse{OK: true})
}
