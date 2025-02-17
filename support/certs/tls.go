package certs

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math"
	"math/big"
	"net"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/pkg/errors"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

const (
	keySize = 2048

	ValidityOneDay   = 24 * time.Hour
	ValidityOneYear  = 365 * ValidityOneDay
	ValidityTenYears = 10 * ValidityOneYear
)

// CertCfg contains all needed fields to configure a new certificate
type CertCfg struct {
	DNSNames     []string
	ExtKeyUsages []x509.ExtKeyUsage
	IPAddresses  []net.IP
	KeyUsages    x509.KeyUsage
	Subject      pkix.Name
	Validity     time.Duration
	IsCA         bool
}

// rsaPublicKey reflects the ASN.1 structure of a PKCS#1 public key.
type rsaPublicKey struct {
	N *big.Int
	E int
}

// GenerateSelfSignedCertificate generates a key/cert pair defined by CertCfg.
func GenerateSelfSignedCertificate(cfg *CertCfg) (*rsa.PrivateKey, *x509.Certificate, error) {
	key, err := PrivateKey()
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to generate private key")
	}

	crt, err := SelfSignedCertificate(cfg, key)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to create self-signed certificate")
	}
	return key, crt, nil
}

// GenerateSignedCertificate generate a key and cert defined by CertCfg and signed by CA.
func GenerateSignedCertificate(caKey *rsa.PrivateKey, caCert *x509.Certificate,
	cfg *CertCfg) (*rsa.PrivateKey, *x509.Certificate, error) {

	// create a private key
	key, err := PrivateKey()
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to generate private key")
	}

	// create a CSR
	csrTmpl := x509.CertificateRequest{Subject: cfg.Subject, DNSNames: cfg.DNSNames, IPAddresses: cfg.IPAddresses}
	csrBytes, err := x509.CreateCertificateRequest(rand.Reader, &csrTmpl, key)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to create certificate request")
	}
	csr, err := x509.ParseCertificateRequest(csrBytes)
	if err != nil {
		return nil, nil, errors.Wrap(err, "error parsing x509 certificate request")
	}

	// create a cert
	cert, err := signedCertificate(cfg, csr, key, caCert, caKey)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to create a signed certificate")
	}
	return key, cert, nil
}

// PrivateKey generates an RSA Private key and returns the value
func PrivateKey() (*rsa.PrivateKey, error) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, keySize)
	if err != nil {
		return nil, errors.Wrap(err, "error generating RSA private key")
	}

	return rsaKey, nil
}

// SelfSignedCertificate creates a self signed certificate
func SelfSignedCertificate(cfg *CertCfg, key *rsa.PrivateKey) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).SetInt64(math.MaxInt64))
	if err != nil {
		return nil, err
	}
	cert := x509.Certificate{
		BasicConstraintsValid: true,
		IsCA:                  cfg.IsCA,
		KeyUsage:              cfg.KeyUsages,
		NotAfter:              time.Now().Add(cfg.Validity),
		NotBefore:             time.Now(),
		SerialNumber:          serial,
		Subject:               cfg.Subject,
	}
	// verifies that the CN and/or OU for the cert is set
	if len(cfg.Subject.CommonName) == 0 || len(cfg.Subject.OrganizationalUnit) == 0 {
		return nil, errors.Errorf("certification's subject is not set, or invalid")
	}
	pub := key.Public()
	cert.SubjectKeyId, err = generateSubjectKeyID(pub)
	if err != nil {
		return nil, errors.Wrap(err, "failed to set subject key identifier")
	}
	certBytes, err := x509.CreateCertificate(rand.Reader, &cert, &cert, key.Public(), key)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create certificate")
	}
	return x509.ParseCertificate(certBytes)
}

// signedCertificate creates a new X.509 certificate based on a template.
func signedCertificate(
	cfg *CertCfg,
	csr *x509.CertificateRequest,
	key *rsa.PrivateKey,
	caCert *x509.Certificate,
	caKey *rsa.PrivateKey,
) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).SetInt64(math.MaxInt64))
	if err != nil {
		return nil, err
	}

	certTmpl := x509.Certificate{
		DNSNames:              csr.DNSNames,
		ExtKeyUsage:           cfg.ExtKeyUsages,
		IPAddresses:           csr.IPAddresses,
		KeyUsage:              cfg.KeyUsages,
		NotAfter:              time.Now().Add(cfg.Validity),
		NotBefore:             caCert.NotBefore,
		SerialNumber:          serial,
		Subject:               csr.Subject,
		IsCA:                  cfg.IsCA,
		Version:               3,
		BasicConstraintsValid: true,
	}
	pub := caCert.PublicKey.(*rsa.PublicKey)
	certTmpl.SubjectKeyId, err = generateSubjectKeyID(pub)
	if err != nil {
		return nil, errors.Wrap(err, "failed to set subject key identifier")
	}
	certBytes, err := x509.CreateCertificate(rand.Reader, &certTmpl, caCert, key.Public(), caKey)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create x509 certificate")
	}
	return x509.ParseCertificate(certBytes)
}

// generateSubjectKeyID generates a SHA-1 hash of the subject public key.
func generateSubjectKeyID(pub crypto.PublicKey) ([]byte, error) {
	var publicKeyBytes []byte
	var err error

	switch pub := pub.(type) {
	case *rsa.PublicKey:
		publicKeyBytes, err = asn1.Marshal(rsaPublicKey{N: pub.N, E: pub.E})
		if err != nil {
			return nil, errors.Wrap(err, "failed to Marshal ans1 public key")
		}
	case *ecdsa.PublicKey:
		publicKeyBytes = elliptic.Marshal(pub.Curve, pub.X, pub.Y)
	default:
		return nil, errors.New("only RSA and ECDSA public keys supported")
	}

	hash := sha1.Sum(publicKeyBytes)
	return hash[:], nil
}

// PrivateKeyToPem converts an rsa.PrivateKey object to pem string
func PrivateKeyToPem(key *rsa.PrivateKey) []byte {
	keyInBytes := x509.MarshalPKCS1PrivateKey(key)
	keyinPem := pem.EncodeToMemory(
		&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: keyInBytes,
		},
	)
	return keyinPem
}

// CertToPem converts an x509.Certificate object to a pem string
func CertToPem(cert *x509.Certificate) []byte {
	certInPem := pem.EncodeToMemory(
		&pem.Block{
			Type:  "CERTIFICATE",
			Bytes: cert.Raw,
		},
	)
	return certInPem
}

// CSRToPem converts an x509.CertificateRequest to a pem string
func CSRToPem(cert *x509.CertificateRequest) []byte {
	certInPem := pem.EncodeToMemory(
		&pem.Block{
			Type:  "CERTIFICATE REQUEST",
			Bytes: cert.Raw,
		},
	)
	return certInPem
}

// PublicKeyToPem converts an rsa.PublicKey object to pem string
func PublicKeyToPem(key *rsa.PublicKey) ([]byte, error) {
	keyInBytes, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return nil, errors.Wrap(err, "failed to MarshalPKIXPublicKey")
	}
	keyinPem := pem.EncodeToMemory(
		&pem.Block{
			Type:  "RSA PUBLIC KEY",
			Bytes: keyInBytes,
		},
	)
	return keyinPem, nil
}

// PemToPrivateKey converts a data block to rsa.PrivateKey.
func PemToPrivateKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.Errorf("could not find a PEM block in the private key")
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

// PemToCertificate converts a data block to x509.Certificate.
func PemToCertificate(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.Errorf("could not find a PEM block in the certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

func Base64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func parsePemKeypair(key, certificate []byte) (*rsa.PrivateKey, *x509.Certificate, error) {
	privKey, err := PemToPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	cert, err := PemToCertificate(certificate)
	if err != nil {
		return nil, nil, err
	}
	rsaPublicKey, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return nil, nil, fmt.Errorf("certificate does not have a RSA public key but a %T, not supported", cert.PublicKey)
	}

	// https://cs.opensource.google/go/go/+/refs/tags/go1.17.5:src/crypto/tls/tls.go;drc=860704317e02d699e4e4a24103853c4782d746c1;l=310
	if rsaPublicKey.N.Cmp(privKey.N) != 0 {
		return nil, nil, errors.New("private key does not match certificate")
	}

	return privKey, cert, nil
}

func ValidateKeyPair(pemKey, pemCertificate []byte, cfg *CertCfg, minimumRemainingValidity time.Duration) error {
	_, cert, err := parsePemKeypair(pemKey, pemCertificate)
	if err != nil {
		return fmt.Errorf("failed to parse keypair: %w", err)
	}

	var errs []error
	stringLessFN := func(a, b string) bool { return a < b }

	dnsNamesDiff := cmp.Diff(cert.DNSNames, cfg.DNSNames, cmpopts.SortSlices(stringLessFN))
	if dnsNamesDiff != "" {
		errs = append(errs, fmt.Errorf("actual dns names differ from expected: %s", dnsNamesDiff))
	}

	extUsageDiff := cmp.Diff(cert.ExtKeyUsage, cfg.ExtKeyUsages, cmpopts.SortSlices(func(a, b x509.ExtKeyUsage) bool { return a < b }))
	if extUsageDiff != "" {
		errs = append(errs, fmt.Errorf("actual extended key usages differ from expected: %s", extUsageDiff))
	}

	ipAddressDiff := cmp.Diff(cert.IPAddresses, cfg.IPAddresses, cmpopts.SortSlices(func(a, b []byte) bool { return bytes.Compare(a, b) == -1 }))
	if ipAddressDiff != "" {
		errs = append(errs, fmt.Errorf("actual ip addresses differ from expected: %s", ipAddressDiff))
	}

	if cert.KeyUsage != cfg.KeyUsages {
		errs = append(errs, fmt.Errorf("actual key usage %d differs from expected %d", cert.KeyUsage, cfg.KeyUsages))
	}

	// subjectDiff ignores the "Names" field, as it contains the parsed attributes but is ignored during marshalling.
	subjectDiff := cmp.Diff(cert.Subject, cfg.Subject, cmpopts.SortSlices(stringLessFN), cmpopts.IgnoreFields(pkix.Name{}, "Names"))
	if subjectDiff != "" {
		errs = append(errs, fmt.Errorf("actual subject differs from expected: %s", subjectDiff))
	}

	if remainingvalidity := time.Until(cert.NotAfter); remainingvalidity < minimumRemainingValidity {
		errs = append(errs, fmt.Errorf("remaining validity %s is smaller than the minimum remaining validity %s", remainingvalidity, minimumRemainingValidity))
	}

	if cert.IsCA != cfg.IsCA {
		errs = append(errs, fmt.Errorf("actual isCA %t does not match expected %t", cert.IsCA, cfg.IsCA))
	}

	return utilerrors.NewAggregate(errs)
}
