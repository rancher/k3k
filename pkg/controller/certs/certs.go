package certs

import (
	"crypto"
	"crypto/x509"
	"fmt"
	"net"
	"time"

	certutil "github.com/rancher/dynamiclistener/cert"
)

func CreateClientCertKey(commonName string, organization []string, altNames *certutil.AltNames, extKeyUsage []x509.ExtKeyUsage, expiresAt time.Duration, caCert, caKey string) ([]byte, []byte, error) {
	caKeyPEM, err := certutil.ParsePrivateKeyPEM([]byte(caKey))
	if err != nil {
		return nil, nil, err
	}

	caCertPEM, err := certutil.ParseCertsPEM([]byte(caCert))
	if err != nil {
		return nil, nil, err
	}

	b, err := generateKey()
	if err != nil {
		return nil, nil, err
	}

	key, err := certutil.ParsePrivateKeyPEM(b)
	if err != nil {
		return nil, nil, err
	}

	cfg := certutil.Config{
		CommonName:   commonName,
		Organization: organization,
		Usages:       extKeyUsage,
		ExpiresAt:    expiresAt,
	}
	if altNames != nil {
		cfg.AltNames = *altNames
	}

	cert, err := certutil.NewSignedCert(cfg, key.(crypto.Signer), caCertPEM[0], caKeyPEM.(crypto.Signer))
	if err != nil {
		return nil, nil, err
	}

	return append(certutil.EncodeCertPEM(cert), certutil.EncodeCertPEM(caCertPEM[0])...), b, nil
}

func generateKey() (data []byte, err error) {
	generatedData, err := certutil.MakeEllipticPrivateKeyPEM()
	if err != nil {
		return nil, fmt.Errorf("error generating key: %v", err)
	}

	return generatedData, nil
}

func AddSANs(sans []string) certutil.AltNames {
	var altNames certutil.AltNames

	for _, san := range sans {
		ip := net.ParseIP(san)
		if ip == nil {
			altNames.DNSNames = append(altNames.DNSNames, san)
		} else {
			altNames.IPs = append(altNames.IPs, ip)
		}
	}

	return altNames
}
