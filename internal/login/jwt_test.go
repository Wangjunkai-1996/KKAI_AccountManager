package login

import "testing"

func TestParseJWTNestedAndLegacyIdentity(t *testing.T) {
	for _, nested := range []bool{false, true} {
		claims := map[string]interface{}{"exp": float64(2100000000)}
		if nested {
			claims["https://api.openai.com/auth"] = map[string]interface{}{
				"chatgpt_account_id": "workspace", "chatgpt_user_id": "user", "chatgpt_plan_type": "free", "poid": "org",
			}
			claims["https://api.openai.com/profile"] = map[string]interface{}{"email": "same@example.test"}
			claims["https://api.openai.com/auth.chatgpt_account_id"] = "stale-flat-workspace"
		} else {
			claims["https://api.openai.com/auth.chatgpt_account_id"] = "workspace"
			claims["https://api.openai.com/auth.chatgpt_user_id"] = "user"
			claims["https://api.openai.com/auth.chatgpt_plan_type"] = "free"
			claims["https://api.openai.com/auth.poid"] = "org"
			claims["https://api.openai.com/profile.email"] = "same@example.test"
		}
		got, err := ParseJWT(testJWT(t, claims))
		if err != nil {
			t.Fatal(err)
		}
		if got.ChatGPTAccountID != "workspace" || got.ChatGPTUserID != "user" || got.ChatGPTPlanType != "free" || got.OrganizationID != "org" || got.Email != "same@example.test" || got.ExpiresAt != 2100000000 {
			t.Fatalf("nested=%v parsed identity=%+v", nested, got)
		}
	}
}

func TestParseOAuthIdentityFallsBackToNestedAccessToken(t *testing.T) {
	access := testJWT(t, map[string]interface{}{
		"exp": float64(2100000000),
		"https://api.openai.com/auth": map[string]interface{}{
			"chatgpt_account_id": "personal-workspace", "chatgpt_plan_type": "free", "poid": "personal-org",
		},
		"https://api.openai.com/profile": map[string]interface{}{"email": "same@example.test"},
	})
	idToken := testJWT(t, map[string]interface{}{"email": "same@example.test"})
	identity, err := parseOAuthIdentity(access, idToken)
	if err != nil || identity.ChatGPTAccountID != "personal-workspace" || identity.PlanType != "free" || identity.OrganizationID != "personal-org" {
		t.Fatalf("identity=%+v err=%v", identity, err)
	}
}
