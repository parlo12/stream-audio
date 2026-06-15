package main

import (
	"testing"

	"github.com/stripe/stripe-go/v78"
)

func TestAccountTypeForSubStatus(t *testing.T) {
	cases := map[stripe.SubscriptionStatus]string{
		stripe.SubscriptionStatusActive:            "paid",
		stripe.SubscriptionStatusTrialing:          "paid",
		stripe.SubscriptionStatusPastDue:           "free",
		stripe.SubscriptionStatusCanceled:          "free",
		stripe.SubscriptionStatusUnpaid:            "free",
		stripe.SubscriptionStatusIncompleteExpired: "free",
		stripe.SubscriptionStatusIncomplete:        "free",
	}
	for status, want := range cases {
		if got := accountTypeForSubStatus(status); got != want {
			t.Errorf("accountTypeForSubStatus(%q) = %q, want %q", status, got, want)
		}
	}
}
