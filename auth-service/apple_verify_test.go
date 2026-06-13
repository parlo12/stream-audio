package main

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/golang-jwt/jwt"
)

// seedAppleKey installs a known RSA public key into the cache under kid and
// marks the cache fresh, so verifyAppleToken resolves it without any network
// call. Returns the matching private key for signing test tokens.
func seedAppleKey(t *testing.T, kid string) *rsa.PrivateKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	appleKeyCache.Lock()
	appleKeyCache.keys = map[string]*rsa.PublicKey{kid: &priv.PublicKey}
	appleKeyCache.fetchedAt = time.Now()
	appleKeyCache.Unlock()
	return priv
}

func signAppleToken(t *testing.T, method jwt.SigningMethod, signKey interface{}, kid string, claims *AppleTokenClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(method, claims)
	if kid != "" {
		token.Header["kid"] = kid
	}
	signed, err := token.SignedString(signKey)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

func validAppleClaims() *AppleTokenClaims {
	return &AppleTokenClaims{
		ISS:           "https://appleid.apple.com",
		AUD:           "com.narrafied.audiobook", // matches APPLE_BUNDLE_ID default
		EXP:           time.Now().Add(time.Hour).Unix(),
		SUB:           "001234.abcdef",
		Email:         "victim@example.com",
		EmailVerified: "true",
	}
}

func TestVerifyAppleToken_Valid(t *testing.T) {
	priv := seedAppleKey(t, "test-kid")
	tok := signAppleToken(t, jwt.SigningMethodRS256, priv, "test-kid", validAppleClaims())

	claims, err := verifyAppleToken(tok)
	if err != nil {
		t.Fatalf("expected valid token, got error: %v", err)
	}
	if claims.SUB != "001234.abcdef" {
		t.Fatalf("unexpected sub: %s", claims.SUB)
	}
}

func TestVerifyAppleToken_ForgedSignature(t *testing.T) {
	seedAppleKey(t, "test-kid")
	// Sign with a *different* key but claim the cached kid — the core S3 attack.
	attacker, _ := rsa.GenerateKey(rand.Reader, 2048)
	tok := signAppleToken(t, jwt.SigningMethodRS256, attacker, "test-kid", validAppleClaims())

	if _, err := verifyAppleToken(tok); err == nil {
		t.Fatal("expected forged-signature token to be rejected")
	}
}

func TestVerifyAppleToken_RejectsHMAC(t *testing.T) {
	seedAppleKey(t, "test-kid")
	// alg-confusion: sign with HS256 using the public modulus as the secret.
	tok := signAppleToken(t, jwt.SigningMethodHS256, []byte("anything"), "test-kid", validAppleClaims())

	if _, err := verifyAppleToken(tok); err == nil {
		t.Fatal("expected non-RSA signing method to be rejected")
	}
}

func TestVerifyAppleToken_WrongAudience(t *testing.T) {
	priv := seedAppleKey(t, "test-kid")
	claims := validAppleClaims()
	claims.AUD = "com.someone.else"
	tok := signAppleToken(t, jwt.SigningMethodRS256, priv, "test-kid", claims)

	if _, err := verifyAppleToken(tok); err == nil {
		t.Fatal("expected wrong-audience token to be rejected")
	}
}

func TestVerifyAppleToken_WrongIssuer(t *testing.T) {
	priv := seedAppleKey(t, "test-kid")
	claims := validAppleClaims()
	claims.ISS = "https://evil.example.com"
	tok := signAppleToken(t, jwt.SigningMethodRS256, priv, "test-kid", claims)

	if _, err := verifyAppleToken(tok); err == nil {
		t.Fatal("expected wrong-issuer token to be rejected")
	}
}

func TestVerifyAppleToken_Expired(t *testing.T) {
	priv := seedAppleKey(t, "test-kid")
	claims := validAppleClaims()
	claims.EXP = time.Now().Add(-time.Hour).Unix()
	tok := signAppleToken(t, jwt.SigningMethodRS256, priv, "test-kid", claims)

	if _, err := verifyAppleToken(tok); err == nil {
		t.Fatal("expected expired token to be rejected")
	}
}
