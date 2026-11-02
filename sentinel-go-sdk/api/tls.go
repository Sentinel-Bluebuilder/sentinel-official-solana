package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	certFile     = "tls.crt"
	certValidity = 10 * 365 * 24 * time.Hour
	keyFile      = "tls.key"
)

func EncodeCertificatePEM(cert tls.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{
		Bytes: cert.Certificate[0],
		Type:  "CERTIFICATE",
	})
}

func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)

	return hex.EncodeToString(sum[:])
}

func FormatColonFingerprint(fingerprint string) string {
	encoded := strings.ToUpper(fingerprint)

	pairs := make([]string, 0, len(encoded)/2)
	for i := 0; i+2 <= len(encoded); i += 2 {
		pairs = append(pairs, encoded[i:i+2])
	}

	return strings.Join(pairs, ":")
}

func LoadOrCreateServerCert(dir string) (tls.Certificate, string, error) {
	certPath := filepath.Join(dir, certFile)
	keyPath := filepath.Join(dir, keyFile)

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err == nil {
		leaf, err := x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			return tls.Certificate{}, "", fmt.Errorf("parsing certificate: %w", err)
		}

		return cert, Fingerprint(leaf), nil
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return tls.Certificate{}, "", fmt.Errorf("creating tls directory: %w", err)
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("generating key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("generating serial: %w", err)
	}

	now := time.Now()
	template := x509.Certificate{
		BasicConstraintsValid: true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		NotAfter:              now.Add(certValidity),
		NotBefore:             now.Add(-time.Hour),
		SerialNumber:          serial,
		Subject: pkix.Name{
			CommonName:   "sentinel-dvpn",
			Organization: []string{"Sentinel"},
		},
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("creating certificate: %w", err)
	}

	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("encoding key: %w", err)
	}

	certBlock := &pem.Block{
		Bytes: der,
		Type:  "CERTIFICATE",
	}
	certPEM := pem.EncodeToMemory(certBlock)
	keyBlock := &pem.Block{
		Bytes: keyDER,
		Type:  "PRIVATE KEY",
	}
	keyPEM := pem.EncodeToMemory(keyBlock)

	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return tls.Certificate{}, "", fmt.Errorf("writing certificate: %w", err)
	}

	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, "", fmt.Errorf("writing key: %w", err)
	}

	return LoadOrCreateServerCert(dir)
}
