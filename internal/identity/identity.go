// Package identity creates and loads this machine's self-signed TLS identity.
package identity

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
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

const (
	certFile = "cert.pem"
	keyFile  = "key.pem"
)

// Identity is the certificate this process presents, and the ids derived from it.
type Identity struct {
	Dir         string
	DeviceID    string
	Fingerprint string
	Certificate *x509.Certificate
	TLS         tls.Certificate
}

// LoadOrCreate reads identity/ under dataDir, or generates an ECDSA P-256
// certificate on first launch. device id is the first 16 bytes of
// SHA-256(SPKI), hex. Fingerprint is SHA-256 of the certificate DER.
func LoadOrCreate(dataDir string) (*Identity, error) {
	dir := filepath.Join(dataDir, "identity")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	certPath := filepath.Join(dir, certFile)
	keyPath := filepath.Join(dir, keyFile)
	if _, err := os.Stat(certPath); errors.Is(err, os.ErrNotExist) {
		if err := generate(dir); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	id, err := DeviceID(leaf)
	if err != nil {
		return nil, err
	}
	return &Identity{
		Dir:         dir,
		DeviceID:    id,
		Fingerprint: Fingerprint(leaf),
		Certificate: leaf,
		TLS:         pair,
	}, nil
}

func generate(dir string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(pub)
	id := hex.EncodeToString(sum[:16])
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: id},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(filepath.Join(dir, certFile), certPEM, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, keyFile), keyPEM, 0o600); err != nil {
		return err
	}
	return nil
}

// DeviceID is the hex of the first 16 bytes of SHA-256(SPKI).
func DeviceID(cert *x509.Certificate) (string, error) {
	if cert == nil {
		return "", errors.New("没有证书")
	}
	spki, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(spki)
	return hex.EncodeToString(sum[:16]), nil
}

// Fingerprint is the hex SHA-256 of the certificate DER.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// FormatFingerprint groups a hex fingerprint for humans.
func FormatFingerprint(hexFP string) string {
	if len(hexFP) == 0 {
		return ""
	}
	var b []byte
	for i := 0; i < len(hexFP); i += 2 {
		end := i + 2
		if end > len(hexFP) {
			end = len(hexFP)
		}
		if i > 0 {
			b = append(b, ':')
		}
		b = append(b, hexFP[i:end]...)
	}
	return string(b)
}

// TLSConfig is the mutual-TLS config for both the listener and the dialer.
// Certificates are self-signed, so verification is fingerprint pinning after
// the handshake, not WebPKI.
func TLSConfig(id *Identity) *tls.Config {
	return &tls.Config{
		Certificates:       []tls.Certificate{id.TLS},
		ClientAuth:         tls.RequireAnyClientCert,
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS13,
	}
}

func (id *Identity) String() string {
	return fmt.Sprintf("%s %s", id.DeviceID, FormatFingerprint(id.Fingerprint))
}
