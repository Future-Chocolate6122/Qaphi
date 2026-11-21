package netcfg

import (
	"context"
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
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/future-chocolate6122/qaphi/internal/store"
)

// Claves de ajustes del certificado (PEM). La clave privada se queda en la
// base de datos y nunca entra en la exportación.
const (
	KeyCert = "net.tls_cert"
	KeyKey  = "net.tls_key"
)

// certValidity es la validez del certificado autofirmado.
const certValidity = 5 * 365 * 24 * time.Hour

// CertInfo describe el certificado en uso.
type CertInfo struct {
	NotAfter    time.Time
	Fingerprint string // SHA-256 del certificado, en hexadecimal con «:»
	Names       []string
}

// CertStore guarda el certificado autofirmado y lo sirve a TLS. Se puede
// regenerar en caliente: las conexiones nuevas usan el nuevo.
type CertStore struct {
	st store.Store

	mu   sync.RWMutex
	cert *tls.Certificate
	info CertInfo
}

// LoadCertStore carga el certificado guardado, si lo hay.
func LoadCertStore(ctx context.Context, st store.Store) (*CertStore, error) {
	cs := &CertStore{st: st}
	certPEM, ok1, err := st.GetSetting(ctx, KeyCert)
	if err != nil {
		return nil, err
	}
	keyPEM, ok2, err := st.GetSetting(ctx, KeyKey)
	if err != nil {
		return nil, err
	}
	if ok1 && ok2 && certPEM != "" && keyPEM != "" {
		if err := cs.set([]byte(certPEM), []byte(keyPEM)); err != nil {
			return nil, fmt.Errorf("stored TLS certificate: %w", err)
		}
	}
	return cs, nil
}

// Ensure genera el certificado si aún no existe.
func (cs *CertStore) Ensure(ctx context.Context) error {
	cs.mu.RLock()
	have := cs.cert != nil
	cs.mu.RUnlock()
	if have {
		return nil
	}
	return cs.Regenerate(ctx)
}

// Regenerate crea un certificado nuevo, lo guarda y lo pone en uso.
func (cs *CertStore) Regenerate(ctx context.Context) error {
	certPEM, keyPEM, err := generateCert()
	if err != nil {
		return err
	}
	if err := cs.set(certPEM, keyPEM); err != nil {
		return err
	}
	return errors.Join(
		cs.st.SetSetting(ctx, KeyCert, string(certPEM)),
		cs.st.SetSetting(ctx, KeyKey, string(keyPEM)),
	)
}

func (cs *CertStore) set(certPEM, keyPEM []byte) error {
	c, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return err
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return err
	}
	c.Leaf = leaf
	sum := sha256.Sum256(leaf.Raw)
	hexSum := strings.ToUpper(hex.EncodeToString(sum[:]))
	var fp []string
	for i := 0; i < len(hexSum); i += 2 {
		fp = append(fp, hexSum[i:i+2])
	}
	names := append([]string(nil), leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		names = append(names, ip.String())
	}
	cs.mu.Lock()
	cs.cert = &c
	cs.info = CertInfo{NotAfter: leaf.NotAfter, Fingerprint: strings.Join(fp, ":"), Names: names}
	cs.mu.Unlock()
	return nil
}

// Info devuelve los datos del certificado, si existe.
func (cs *CertStore) Info() (CertInfo, bool) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.info, cs.cert != nil
}

// GetCertificate implementa tls.Config.GetCertificate.
func (cs *CertStore) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	if cs.cert == nil {
		return nil, errors.New("no TLS certificate")
	}
	return cs.cert, nil
}

// generateCert crea un certificado autofirmado ECDSA P-256 válido para el
// nombre del equipo, «localhost» y todas sus IP.
func generateCert() (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, nil, err
	}
	host, _ := os.Hostname()
	dns := []string{"localhost"}
	if host != "" && host != "localhost" {
		dns = append([]string{host}, dns...)
	}
	ips := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	for _, s := range LocalIPs() {
		ips = append(ips, net.ParseIP(s))
	}
	now := time.Now()
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Qaphi (" + firstNonEmpty(host, "localhost") + ")", Organization: []string{"Qaphi"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(certValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dns,
		IPAddresses:           ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
