package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

func DefaultDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".mdp", "certs")
}

func CertPath(dir string) string { return filepath.Join(dir, "localhost.pem") }
func KeyPath(dir string) string  { return filepath.Join(dir, "localhost-key.pem") }

// EnsureCert returns cert and key paths. Uses mkcert if available (certs are
// automatically trusted), otherwise falls back to a self-signed cert.
func EnsureCert(dir string) (certPath, keyPath string, err error) {
	certPath = CertPath(dir)
	keyPath = KeyPath(dir)

	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			return certPath, keyPath, nil
		}
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("create cert dir: %w", err)
	}

	if tryMkcert(certPath, keyPath) {
		slog.Info("generated trusted cert via mkcert")
		return certPath, keyPath, nil
	}

	slog.Info("mkcert not found, generating self-signed cert")
	if err := generateSelfSigned(certPath, keyPath); err != nil {
		return "", "", err
	}
	trustCert(certPath)
	return certPath, keyPath, nil
}

func tryMkcert(certPath, keyPath string) bool {
	mkcert, err := exec.LookPath("mkcert")
	if err != nil {
		return false
	}
	// Ensure the local CA is installed
	exec.Command(mkcert, "-install").Run()

	cmd := exec.Command(mkcert,
		"-cert-file", certPath,
		"-key-file", keyPath,
		"localhost", "127.0.0.1", "::1")
	return cmd.Run() == nil
}

func generateSelfSigned(certPath, keyPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "mdp localhost"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create certificate: %w", err)
	}

	certFile, err := os.OpenFile(certPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("write cert: %w", err)
	}
	pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	certFile.Close()

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}
	keyFile, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("write key: %w", err)
	}
	pem.Encode(keyFile, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	keyFile.Close()

	return nil
}

func trustCert(certPath string) {
	switch runtime.GOOS {
	case "darwin":
		cmd := exec.Command("security", "add-trusted-cert", "-d", "-r", "trustRoot",
			"-k", "/Library/Keychains/System.keychain", certPath)
		if err := cmd.Run(); err != nil {
			slog.Info("auto-trust requires sudo, run manually to avoid browser warnings",
				"cmd", fmt.Sprintf("sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain %s", certPath))
		} else {
			slog.Info("cert added to system trust store")
		}
	case "linux":
		dest := "/usr/local/share/ca-certificates/mdp-localhost.crt"
		if err := exec.Command("cp", certPath, dest).Run(); err == nil {
			exec.Command("update-ca-certificates").Run()
			slog.Info("cert added to system trust store")
		} else {
			slog.Info("auto-trust requires sudo, run manually to avoid browser warnings",
				"cmd", fmt.Sprintf("sudo cp %s %s && sudo update-ca-certificates", certPath, dest))
		}
	default:
		slog.Info("auto-trust not supported on this OS, import cert manually to avoid browser warnings",
			"cert", certPath)
	}
}
