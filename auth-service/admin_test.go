package main

import (
	"testing"
	"time"
)

func TestConsumeWipeNonce(t *testing.T) {
	// Valid, unexpired nonce: first consume succeeds, second fails (single-use).
	wipeNonceStore.Lock()
	wipeNonceStore.nonces["good"] = time.Now().Add(time.Minute)
	wipeNonceStore.Unlock()
	if !consumeWipeNonce("good") {
		t.Fatal("expected valid nonce to be accepted")
	}
	if consumeWipeNonce("good") {
		t.Fatal("expected nonce to be single-use (second consume must fail)")
	}

	// Expired nonce is rejected (and consumed/cleared).
	wipeNonceStore.Lock()
	wipeNonceStore.nonces["stale"] = time.Now().Add(-time.Second)
	wipeNonceStore.Unlock()
	if consumeWipeNonce("stale") {
		t.Fatal("expected expired nonce to be rejected")
	}

	// Unknown nonce is rejected.
	if consumeWipeNonce("never-issued") {
		t.Fatal("expected unknown nonce to be rejected")
	}
}
