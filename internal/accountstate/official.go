package accountstate

import "strings"

// OfficialStatus maps an explicit upstream machine code to the account state.
// The caller verifies the source of the observation and its binding/version.
func OfficialStatus(code string) string {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "account_deleted", "user_deleted", "deleted":
		return "deleted"
	case "account_deactivated", "user_deactivated", "account_disabled", "user_disabled",
		"payment_required", "subscription_expired", "deactivated_workspace", "deactivated":
		return "deactivated"
	default:
		return ""
	}
}
