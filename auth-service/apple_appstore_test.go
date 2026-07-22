package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"testing"
	"time"

	"github.com/golang-jwt/jwt"
)

const testBundle = "com.rmhrealestate.AudioBook"

// mkCert makes an EC cert signed by parent (self-signed root if parent==nil).
func mkCert(t *testing.T, cn string, parent *x509.Certificate, parentKey *ecdsa.PrivateKey, isCA bool) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  isCA,
	}
	signParent, signKey := tmpl, key // self-signed by default
	if parent != nil {
		signParent, signKey = parent, parentKey
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signParent, &key.PublicKey, signKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key, der
}

func signJWS(t *testing.T, claims jwsTransaction, leafKey *ecdsa.PrivateKey, chainDER [][]byte) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	x5c := make([]string, 0, len(chainDER))
	for _, d := range chainDER {
		x5c = append(x5c, base64.StdEncoding.EncodeToString(d))
	}
	tok.Header["x5c"] = x5c
	s, err := tok.SignedString(leafKey)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestVerifySignedTransaction(t *testing.T) {
	root, rootKey, rootDER := mkCert(t, "Test Root", nil, nil, true)
	inter, interKey, interDER := mkCert(t, "Test Intermediate", root, rootKey, true)
	_, leafKey, leafDER := mkCert(t, "Test Leaf", inter, interKey, false)
	chain := [][]byte{leafDER, interDER, rootDER}

	// Trust the test root for the duration of this test.
	orig := appStoreRootPool
	appStoreRootPool = x509.NewCertPool()
	appStoreRootPool.AddCert(root)
	defer func() { appStoreRootPool = orig }()

	base := jwsTransaction{
		TransactionID: "tx-1",
		BundleID:      testBundle,
		ProductID:     "com.narrafied.premium.monthly",
		Type:          "Auto-Renewable Subscription",
		PurchaseDate:  time.Now().UnixMilli(),
		ExpiresDate:   time.Now().Add(30 * 24 * time.Hour).UnixMilli(),
	}

	t.Run("valid", func(t *testing.T) {
		tx, err := verifySignedTransaction(signJWS(t, base, leafKey, chain), testBundle)
		if err != nil {
			t.Fatalf("valid transaction rejected: %v", err)
		}
		if tx.ProductID != "com.narrafied.premium.monthly" {
			t.Fatalf("wrong product id: %s", tx.ProductID)
		}
	})

	t.Run("wrong bundle", func(t *testing.T) {
		if _, err := verifySignedTransaction(signJWS(t, base, leafKey, chain), "com.someone.else"); err == nil {
			t.Fatal("expected bundle-mismatch rejection")
		}
	})

	t.Run("revoked", func(t *testing.T) {
		rev := base
		rev.RevocationDate = time.Now().UnixMilli()
		if _, err := verifySignedTransaction(signJWS(t, rev, leafKey, chain), testBundle); err == nil {
			t.Fatal("expected revoked rejection")
		}
	})

	t.Run("expired", func(t *testing.T) {
		exp := base
		exp.ExpiresDate = time.Now().Add(-time.Hour).UnixMilli()
		if _, err := verifySignedTransaction(signJWS(t, exp, leafKey, chain), testBundle); err == nil {
			t.Fatal("expected expired rejection")
		}
	})

	t.Run("tampered payload", func(t *testing.T) {
		jws := signJWS(t, base, leafKey, chain)
		// Flip a character in the payload segment → signature must fail.
		b := []byte(jws)
		for i := 0; i < len(b); i++ {
			if b[i] == '.' { // corrupt the byte right after the first dot (payload start)
				if b[i+1] == 'A' {
					b[i+1] = 'B'
				} else {
					b[i+1] = 'A'
				}
				break
			}
		}
		if _, err := verifySignedTransaction(string(b), testBundle); err == nil {
			t.Fatal("expected tampered-token rejection")
		}
	})

	t.Run("untrusted root", func(t *testing.T) {
		// Swap in a DIFFERENT root the chain doesn't lead to.
		otherRoot, _, _ := mkCert(t, "Other Root", nil, nil, true)
		appStoreRootPool = x509.NewCertPool()
		appStoreRootPool.AddCert(otherRoot)
		defer func() {
			appStoreRootPool = x509.NewCertPool()
			appStoreRootPool.AddCert(root)
		}()
		if _, err := verifySignedTransaction(signJWS(t, base, leafKey, chain), testBundle); err == nil {
			t.Fatal("expected untrusted-chain rejection")
		}
	})
}

// TestAppleRootEmbedded confirms the shipped Apple root parses.
func TestAppleRootEmbedded(t *testing.T) {
	if mustAppleRootPool() == nil {
		t.Fatal("Apple root pool is nil")
	}
}
