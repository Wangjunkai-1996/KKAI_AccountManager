package exporter

import (
	"fmt"
	"time"
)

// AccountResult represents the result of a login attempt
type AccountResult struct {
	Email            string
	Password         string
	TOTPSecret       string
	AccessToken      string
	RefreshToken     string
	ChatGPTAccountID string
	OrganizationID   string
	PlanType         string
	ExpiresAt        int64
	ExpiresIn        int
	Success          bool
	Error            string
}

// Sub2APIData represents the complete sub2api import format
type Sub2APIData struct {
	Type       string           `json:"type"`
	Version    int              `json:"version"`
	ExportedAt string           `json:"exported_at"`
	Proxies    []interface{}    `json:"proxies"`
	Accounts   []Sub2APIAccount `json:"accounts"`
}

// Sub2APIAccount represents a single account in sub2api format
type Sub2APIAccount struct {
	Name               string                    `json:"name"`
	Platform           string                    `json:"platform"`
	Type               string                    `json:"type"`
	Credentials        Sub2APICredentials        `json:"credentials"`
	Extra              Sub2APIExtra              `json:"extra"`
	Concurrency        int                       `json:"concurrency"`
	Priority           int                       `json:"priority"`
	RateMultiplier     float64                   `json:"rate_multiplier"`
	AutoPauseOnExpired bool                      `json:"auto_pause_on_expired"`
	PlanType           string                    `json:"plan_type"`
	ExpiresAt          int64                     `json:"expires_at,omitempty"`
}

// Sub2APICredentials represents the credentials section
type Sub2APICredentials struct {
	AccessToken      string `json:"access_token"`
	ChatGPTAccountID string `json:"chatgpt_account_id"`
	ExpiresAt        int64  `json:"expires_at"`
	ExpiresIn        int    `json:"expires_in"`
	OrganizationID   string `json:"organization_id"`
	RefreshToken     string `json:"refresh_token"`
	PlanType         string `json:"plan_type"`
}

// Sub2APIExtra represents the extra section
type Sub2APIExtra struct {
	Email             string              `json:"email"`
	DisplayName       string              `json:"display_name"`
	OpenAIPassthrough bool                `json:"openai_passthrough"`
	Recovery          Sub2APIRecovery     `json:"recovery"`
}

// Sub2APIRecovery represents the recovery credentials
type Sub2APIRecovery struct {
	Email          string `json:"email"`
	LoginPassword  string `json:"login_password"`
	TOTPSecret     string `json:"totp_secret"`
	CredentialLine string `json:"credential_line"`
}

// GenerateSub2APIJson converts login results to sub2api import format
func GenerateSub2APIJson(results []*AccountResult) *Sub2APIData {
	accounts := make([]Sub2APIAccount, 0)
	now := time.Now()

	for _, result := range results {
		if !result.Success {
			continue
		}

		// Generate display name with timestamp
		displayName := fmt.Sprintf("%s----%s",
			now.Format("2006-01-02_15:04:05"),
			result.Email,
		)

		// Calculate expires_at for the account (30 days from now)
		accountExpiresAt := now.Add(30 * 24 * time.Hour).Unix()

		account := Sub2APIAccount{
			Name:     displayName,
			Platform: "openai",
			Type:     "oauth",
			Credentials: Sub2APICredentials{
				AccessToken:      result.AccessToken,
				ChatGPTAccountID: result.ChatGPTAccountID,
				ExpiresAt:        result.ExpiresAt,
				ExpiresIn:        result.ExpiresIn,
				OrganizationID:   result.OrganizationID,
				RefreshToken:     result.RefreshToken,
				PlanType:         result.PlanType,
			},
			Extra: Sub2APIExtra{
				Email:             result.Email,
				DisplayName:       displayName,
				OpenAIPassthrough: false,
				Recovery: Sub2APIRecovery{
					Email:         result.Email,
					LoginPassword: result.Password,
					TOTPSecret:    result.TOTPSecret,
					CredentialLine: fmt.Sprintf("%s----%s----%s",
						result.Email,
						result.Password,
						result.TOTPSecret,
					),
				},
			},
			Concurrency:        100,
			Priority:           1,
			RateMultiplier:     1.0,
			AutoPauseOnExpired: true,
			PlanType:           result.PlanType,
			ExpiresAt:          accountExpiresAt,
		}

		accounts = append(accounts, account)
	}

	return &Sub2APIData{
		Type:       "sub2api-data",
		Version:    1,
		ExportedAt: now.Format(time.RFC3339),
		Proxies:    []interface{}{},
		Accounts:   accounts,
	}
}

// GenerateSummary generates a summary of the export
func GenerateSummary(data *Sub2APIData) string {
	successCount := len(data.Accounts)

	planTypes := make(map[string]int)
	for _, acc := range data.Accounts {
		planTypes[acc.PlanType]++
	}

	summary := fmt.Sprintf(`
╔════════════════════════════════════════════════════════════╗
║                    导出摘要                                 ║
╠════════════════════════════════════════════════════════════╣
║ 总账号数: %-47d ║
║ 导出时间: %-47s ║
╠════════════════════════════════════════════════════════════╣
║ 按计划类型统计:                                             ║
`, successCount, data.ExportedAt)

	for planType, count := range planTypes {
		summary += fmt.Sprintf("║   %-10s: %-43d ║\n", planType, count)
	}

	summary += "╚════════════════════════════════════════════════════════════╝\n"

	return summary
}
