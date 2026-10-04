package login

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// GenerateTOTP generates a TOTP code from a secret
func GenerateTOTP(secret string) (string, error) {
	// Remove spaces and convert to uppercase
	secret = strings.ToUpper(strings.ReplaceAll(secret, " ", ""))

	// Base32 decode
	key, err := base32.StdEncoding.DecodeString(secret)
	if err != nil {
		return "", fmt.Errorf("invalid TOTP secret: %w", err)
	}

	// Calculate time counter (30 second period)
	counter := time.Now().Unix() / 30

	// Generate HMAC-SHA1
	h := hmac.New(sha1.New, key)
	binary.Write(h, binary.BigEndian, counter)
	hash := h.Sum(nil)

	// Dynamic truncation
	offset := hash[len(hash)-1] & 0x0F
	truncated := binary.BigEndian.Uint32(hash[offset:offset+4]) & 0x7FFFFFFF

	// Generate 6-digit code
	code := truncated % 1000000

	return fmt.Sprintf("%06d", code), nil
}

// ValidateTOTPSecret validates the format of a TOTP secret
func ValidateTOTPSecret(secret string) bool {
	secret = strings.ToUpper(strings.ReplaceAll(secret, " ", ""))
	_, err := base32.StdEncoding.DecodeString(secret)
	return err == nil
}

// Note: Random seed is initialized in main.go to avoid multiple initializations
