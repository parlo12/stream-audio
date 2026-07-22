package main

// App Store Server verification of StoreKit 2 signed transactions.
//
// The pre-launch fraud gate: instead of trusting the product id the app claims,
// we cryptographically verify the signed transaction (a JWS) Apple issues. The
// JWS header carries an x5c cert chain (leaf → Apple intermediate → Apple Root
// CA G3); we verify that chain to Apple's public root, verify the ES256
// signature with the leaf key, then enforce the business invariants (our bundle
// id, not revoked, not expired). The decoded productId is authoritative — the
// caller must never trust the client-supplied product id.
//
// This needs only Apple's PUBLIC root (embedded below), not an App Store Connect
// API key, so it works without the .p8 the machine doesn't have. A future
// enhancement — real-time refund/revocation status — would call the App Store
// Server API and does require that key.

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt"
)

// Apple Root CA - G3 (public; https://www.apple.com/certificateauthority/).
// SHA-256 fingerprint 63:34:3A:BF:...:91:79. The trust anchor for signed
// transactions — never trust the root copy Apple embeds in the JWS x5c chain.
const appleRootCAG3PEM = `-----BEGIN CERTIFICATE-----
MIICQzCCAcmgAwIBAgIILcX8iNLFS5UwCgYIKoZIzj0EAwMwZzEbMBkGA1UEAwwS
QXBwbGUgUm9vdCBDQSAtIEczMSYwJAYDVQQLDB1BcHBsZSBDZXJ0aWZpY2F0aW9u
IEF1dGhvcml0eTETMBEGA1UECgwKQXBwbGUgSW5jLjELMAkGA1UEBhMCVVMwHhcN
MTQwNDMwMTgxOTA2WhcNMzkwNDMwMTgxOTA2WjBnMRswGQYDVQQDDBJBcHBsZSBS
b290IENBIC0gRzMxJjAkBgNVBAsMHUFwcGxlIENlcnRpZmljYXRpb24gQXV0aG9y
aXR5MRMwEQYDVQQKDApBcHBsZSBJbmMuMQswCQYDVQQGEwJVUzB2MBAGByqGSM49
AgEGBSuBBAAiA2IABJjpLz1AcqTtkyJygRMc3RCV8cWjTnHcFBbZDuWmBSp3ZHtf
TjjTuxxEtX/1H7YyYl3J6YRbTzBPEVoA/VhYDKX1DyxNB0cTddqXl5dvMVztK517
IDvYuVTZXpmkOlEKMaNCMEAwHQYDVR0OBBYEFLuw3qFYM4iapIqZ3r6966/ayySr
MA8GA1UdEwEB/wQFMAMBAf8wDgYDVR0PAQH/BAQDAgEGMAoGCCqGSM49BAMDA2gA
MGUCMQCD6cHEFl4aXTQY2e3v9GwOAEZLuN+yRhHFD/3meoyhpmvOwgPUnPWTxnS4
at+qIxUCMG1mihDK1A3UT82NQz60imOlM27jbdoXt2QfyFMm+YhidDkLF1vLUagM
6BgD56KyKA==
-----END CERTIFICATE-----`

// appStoreRootPool is the trust anchor for signed-transaction cert chains.
// A var (not a const pool) so tests can swap in a generated root.
var appStoreRootPool = mustAppleRootPool()

func mustAppleRootPool() *x509.CertPool {
	p := x509.NewCertPool()
	if !p.AppendCertsFromPEM([]byte(appleRootCAG3PEM)) {
		panic("apple: failed to parse embedded Apple Root CA G3")
	}
	return p
}

// jwsTransaction is the subset of Apple's JWSTransactionDecodedPayload we need.
// Date fields are Unix milliseconds. Implements jwt.Claims; Valid() is a no-op
// because the payload has no standard exp/nbf — we validate expiry ourselves.
type jwsTransaction struct {
	TransactionID  string `json:"transactionId"`
	OriginalID     string `json:"originalTransactionId"`
	BundleID       string `json:"bundleId"`
	ProductID      string `json:"productId"`
	Type           string `json:"type"`
	PurchaseDate   int64  `json:"purchaseDate"`
	ExpiresDate    int64  `json:"expiresDate"`
	RevocationDate int64  `json:"revocationDate"`
}

func (jwsTransaction) Valid() error { return nil }

// verifySignedTransaction verifies a StoreKit 2 signed transaction JWS and
// returns the DECODED, TRUSTED transaction. Verifies: (1) Apple's cert chain to
// the embedded Apple root, (2) the ES256 signature with the leaf key, (3) the
// bundle id matches, (4) not revoked, (5) not expired (for subscriptions).
// Callers must use the returned ProductID, never the client-supplied one.
func verifySignedTransaction(signedTransaction, expectedBundleID string) (*jwsTransaction, error) {
	if signedTransaction == "" {
		return nil, errors.New("empty signed transaction")
	}
	var claims jwsTransaction
	_, err := jwt.ParseWithClaims(signedTransaction, &claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodECDSA); !ok || t.Method.Alg() != "ES256" {
			return nil, fmt.Errorf("unexpected signing method %q", t.Method.Alg())
		}
		leaf, err := verifyAppleCertChain(t.Header["x5c"])
		if err != nil {
			return nil, err
		}
		pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
		if !ok {
			return nil, errors.New("leaf certificate key is not ECDSA")
		}
		return pub, nil
	})
	if err != nil {
		return nil, fmt.Errorf("signature/chain verification failed: %w", err)
	}
	if claims.BundleID != expectedBundleID {
		return nil, fmt.Errorf("bundle id mismatch: got %q, want %q", claims.BundleID, expectedBundleID)
	}
	if claims.RevocationDate != 0 {
		return nil, errors.New("transaction has been revoked/refunded")
	}
	if claims.ExpiresDate != 0 && time.UnixMilli(claims.ExpiresDate).Before(time.Now()) {
		return nil, errors.New("subscription has expired")
	}
	return &claims, nil
}

// verifyAppleCertChain parses the JWS x5c header — [leaf, intermediate(s), root]
// as base64 DER — verifies the chain to the trusted Apple root, and returns the
// leaf certificate. The root supplied in x5c is ignored as an anchor; only
// appStoreRootPool is trusted.
func verifyAppleCertChain(x5c interface{}) (*x509.Certificate, error) {
	arr, ok := x5c.([]interface{})
	if !ok || len(arr) < 2 {
		return nil, errors.New("missing or too-short x5c certificate chain")
	}
	certs := make([]*x509.Certificate, 0, len(arr))
	for _, v := range arr {
		s, ok := v.(string)
		if !ok {
			return nil, errors.New("malformed x5c entry")
		}
		der, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("x5c base64: %w", err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("x5c parse: %w", err)
		}
		certs = append(certs, cert)
	}
	intermediates := x509.NewCertPool()
	for _, c := range certs[1:] { // everything but the leaf are chain-builders
		intermediates.AddCert(c)
	}
	if _, err := certs[0].Verify(x509.VerifyOptions{
		Roots:         appStoreRootPool,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		return nil, fmt.Errorf("certificate chain does not verify to Apple root: %w", err)
	}
	return certs[0], nil
}
