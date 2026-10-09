package login

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// JWTPayload contains the decoded JWT payload
type JWTPayload struct {
	ChatGPTAccountID string `json:"https://api.openai.com/auth.chatgpt_account_id"`
	ChatGPTUserID    string `json:"https://api.openai.com/auth.chatgpt_user_id"`
	ChatGPTPlanType  string `json:"https://api.openai.com/auth.chatgpt_plan_type"`
	OrganizationID   string `json:"https://api.openai.com/auth.poid"`
	Email            string `json:"https://api.openai.com/profile.email"`
	Name             string `json:"https://api.openai.com/profile.name"`
	ExpiresAt        int64  `json:"exp"`
	IssuedAt         int64  `json:"iat"`
}

// ParseJWT parses a JWT token and extracts the payload
func ParseJWT(token string) (*JWTPayload, error) {
	// JWT format: header.payload.signature
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format: expected 3 parts, got %d", len(parts))
	}

	// Decode payload (Base64 URL encoding)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("failed to decode JWT payload: %w", err)
	}

	var data JWTPayload
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JWT payload: %w", err)
	}
	// Current OAuth tokens nest these claims; retain the flat names for older
	// credentials while preferring the token's explicit nested identity.
	var nested struct {
		Auth struct {
			ChatGPTAccountID string `json:"chatgpt_account_id"`
			ChatGPTUserID    string `json:"chatgpt_user_id"`
			ChatGPTPlanType  string `json:"chatgpt_plan_type"`
			OrganizationID   string `json:"poid"`
		} `json:"https://api.openai.com/auth"`
		Profile struct {
			Email string `json:"email"`
			Name  string `json:"name"`
		} `json:"https://api.openai.com/profile"`
	}
	if err := json.Unmarshal(payload, &nested); err != nil {
		return nil, fmt.Errorf("failed to unmarshal nested JWT claims: %w", err)
	}
	for target, value := range map[*string]string{
		&data.ChatGPTAccountID: nested.Auth.ChatGPTAccountID,
		&data.ChatGPTUserID:    nested.Auth.ChatGPTUserID,
		&data.ChatGPTPlanType:  nested.Auth.ChatGPTPlanType,
		&data.OrganizationID:   nested.Auth.OrganizationID,
		&data.Email:            nested.Profile.Email,
		&data.Name:             nested.Profile.Name,
	} {
		if value != "" {
			*target = value
		}
	}

	return &data, nil
}

// ValidateJWT performs basic validation on a JWT token
func ValidateJWT(token string) error {
	payload, err := ParseJWT(token)
	if err != nil {
		return err
	}

	// Check if token is expired
	now := time.Now().Unix()
	if payload.ExpiresAt < now {
		return fmt.Errorf("token expired at %s", time.Unix(payload.ExpiresAt, 0).Format(time.RFC3339))
	}

	// Check if required fields are present
	if payload.ChatGPTAccountID == "" {
		return fmt.Errorf("missing chatgpt_account_id in token")
	}

	if payload.Email == "" {
		return fmt.Errorf("missing email in token")
	}

	return nil
}
