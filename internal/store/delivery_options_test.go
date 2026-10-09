package store

import (
	"strings"
	"testing"
)

func TestDeliveryOptionsNamePrefixValidation(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		invalid           bool
	}{
		{"legacy_default", "", "", false},
		{"blank", "  ", "", false},
		{"trim", "  测试池_A  ", "测试池_A", false},
		{"unicode_limit", strings.Repeat("池", 32), strings.Repeat("池", 32), false},
		{"too_long", strings.Repeat("池", 33), "", true},
		{"newline", "A\nB", "", true},
		{"control", "A\x00B", "", true},
		{"unicode_control", "A\u009fB", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := DefaultDeliveryOptions()
			options.NamePrefix = tc.input
			err := options.Validate()
			if (err != nil) != tc.invalid || err == nil && options.NamePrefix != tc.want {
				t.Fatalf("prefix=%q err=%v", options.NamePrefix, err)
			}
		})
	}
}
