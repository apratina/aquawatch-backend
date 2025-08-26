package internal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// VerifyCheck validates a Vonage Verify code for a given request ID.
// It returns true when the code is valid (status == "0").
// Docs: https://dashboard.nexmo.com/getting-started/verify
func VerifyCheck(ctx context.Context, requestID, code string) (bool, error) {
	apiKey := os.Getenv("VONAGE_API_KEY")
	apiSecret := os.Getenv("VONAGE_API_SECRET")
	if apiKey == "" || apiSecret == "" {
		return false, errors.New("vonage api credentials not configured")
	}

	form := url.Values{}
	form.Set("api_key", apiKey)
	form.Set("api_secret", apiSecret)
	form.Set("request_id", requestID)
	form.Set("code", code)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.nexmo.com/verify/check/json", strings.NewReader(form.Encode()))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	var out struct {
		Status    string `json:"status"`
		ErrorText string `json:"error_text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, err
	}
	if out.Status == "0" {
		return true, nil
	}
	if out.ErrorText != "" {
		return false, errors.New(out.ErrorText)
	}
	return false, errors.New("verification failed")
}

// VerifyStart initiates a Vonage Verify request to send a PIN via SMS/voice.
// Returns the request_id on success (status == "0").
func VerifyStart(ctx context.Context, phoneE164, brand string) (string, error) {
	apiKey := os.Getenv("VONAGE_API_KEY")
	apiSecret := os.Getenv("VONAGE_API_SECRET")
	if apiKey == "" || apiSecret == "" {
		return "", errors.New("vonage api credentials not configured")
	}
	if brand == "" {
		brand = "AquaWatch"
	}

	form := url.Values{}
	form.Set("api_key", apiKey)
	form.Set("api_secret", apiSecret)
	form.Set("number", phoneE164)
	form.Set("brand", brand)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.nexmo.com/verify/json", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out struct {
		Status    string `json:"status"`
		RequestID string `json:"request_id"`
		ErrorText string `json:"error_text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Status == "0" && out.RequestID != "" {
		return out.RequestID, nil
	}
	if out.ErrorText != "" {
		return "", errors.New(out.ErrorText)
	}
	return "", errors.New("verify start failed")
}
