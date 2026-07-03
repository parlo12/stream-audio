package main

import (
	"strings"
	"testing"
	"time"
)

func TestGenerateReferralCode(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		code, err := generateReferralCode()
		if err != nil {
			t.Fatalf("generateReferralCode error: %v", err)
		}
		if len(code) != referralCodeLength {
			t.Fatalf("code %q has length %d, want %d", code, len(code), referralCodeLength)
		}
		for _, ch := range code {
			if !strings.ContainsRune(referralCodeAlphabet, ch) {
				t.Fatalf("code %q contains %q outside the alphabet", code, ch)
			}
		}
		seen[code] = true
	}
	if len(seen) < 95 {
		t.Fatalf("too many collisions in 100 codes: only %d unique", len(seen))
	}
}

func TestExtendPremiumUntil(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

	// No existing credit → starts from now.
	got := extendPremiumUntil(nil, now, 1)
	if want := now.AddDate(0, 1, 0); !got.Equal(want) {
		t.Errorf("nil current: got %v, want %v", got, want)
	}

	// Expired credit → also starts from now.
	past := now.AddDate(0, -2, 0)
	got = extendPremiumUntil(&past, now, 1)
	if want := now.AddDate(0, 1, 0); !got.Equal(want) {
		t.Errorf("expired current: got %v, want %v", got, want)
	}

	// Active credit → stacks on top of the existing expiry.
	future := now.AddDate(0, 0, 10)
	got = extendPremiumUntil(&future, now, 1)
	if want := future.AddDate(0, 1, 0); !got.Equal(want) {
		t.Errorf("active current: got %v, want %v", got, want)
	}
}

func TestEffectiveAccountType(t *testing.T) {
	now := time.Now()
	future := now.AddDate(0, 1, 0)
	past := now.AddDate(0, -1, 0)

	cases := []struct {
		name string
		user User
		want string
	}{
		{"paid billing", User{AccountType: "paid"}, "paid"},
		{"free no credit", User{AccountType: "free"}, "free"},
		{"free with active credit", User{AccountType: "free", PremiumUntil: &future}, "paid"},
		{"free with expired credit", User{AccountType: "free", PremiumUntil: &past}, "free"},
		{"empty type", User{}, "free"},
		{"paid with expired credit stays paid", User{AccountType: "paid", PremiumUntil: &past}, "paid"},
	}
	for _, tc := range cases {
		if got := effectiveAccountType(&tc.user); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
