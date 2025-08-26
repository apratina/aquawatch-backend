package internal

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"time"
)

// MintSessionToken creates a signed token bound to a phone and expiry time.
// Format: base64(phone|exp|sig) where sig=HMAC_SHA256(secret, phone|exp)
func MintSessionToken(phoneE164 string, ttl time.Duration) (string, error) {
	secret := os.Getenv("SESSION_SECRET")
	if secret == "" {
		return "", errors.New("SESSION_SECRET not configured")
	}
	exp := time.Now().Add(ttl).Unix()
	payload := fmt.Sprintf("%s|%d", phoneE164, exp)
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(h.Sum(nil))
	token := base64.RawURLEncoding.EncodeToString([]byte(payload + "|" + sig))
	return token, nil
}

// ValidateSessionToken verifies signature and expiry.
// Returns the bound phone if valid.
func ValidateSessionToken(token string) (string, error) {
	secret := os.Getenv("SESSION_SECRET")
	if secret == "" {
		return "", errors.New("SESSION_SECRET not configured")
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", errors.New("invalid token encoding")
	}
	parts := bytesSplitN(string(raw), '|', 3)
	if len(parts) != 3 {
		return "", errors.New("invalid token format")
	}
	phone, expStr, sig := parts[0], parts[1], parts[2]
	// verify expiry
	var exp int64
	_, err = fmt.Sscanf(expStr, "%d", &exp)
	if err != nil || time.Now().Unix() > exp {
		return "", errors.New("token expired")
	}
	// verify signature
	payload := phone + "|" + expStr
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(payload))
	expected := base64.RawURLEncoding.EncodeToString(h.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return "", errors.New("invalid token signature")
	}
	return phone, nil
}

// bytesSplitN splits s on sep, like strings.SplitN but without importing strings for minimal footprint here.
func bytesSplitN(s string, sep rune, n int) []string {
	var res []string
	start := 0
	count := 0
	for i, r := range s {
		if r == sep {
			res = append(res, s[start:i])
			start = i + 1
			count++
			if count == n-1 {
				break
			}
		}
	}
	res = append(res, s[start:])
	return res
}
