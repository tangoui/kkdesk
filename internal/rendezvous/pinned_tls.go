package rendezvous

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// LoadOrCreateIdentity keeps the same key across restarts. The TLS certificate
// is a container for that key, which clients verify directly.
func LoadOrCreateIdentity(path string) (*tls.Config, string, error) {
	private, err := identityKey(path)
	if err != nil {
		return nil, "", err
	}
	public := private.Public().(ed25519.PublicKey)
	now := time.Now()
	template := &x509.Certificate{
		NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(1, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certificate, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		return nil, "", err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{{Certificate: [][]byte{certificate}, PrivateKey: private}},
	}, base64.StdEncoding.EncodeToString(public), nil
}

func identityKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := createIdentityKey(path); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errors.New("invalid server identity PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("server identity must be an Ed25519 key")
	}
	return key, nil
}

func createIdentityKey(path string) error {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".kkdesk-identity-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	err = pem.Encode(file, &pem.Block{Type: "PRIVATE KEY", Bytes: encoded})
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	// Publish the complete key without replacing one made by another process.
	return os.Link(file.Name(), path)
}

func (c Config) pinnedTLS(endpoint string) (*tls.Config, error) {
	if strings.TrimSpace(c.PublicKey) == "" {
		return nil, nil
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "wss" || net.ParseIP(u.Hostname()) == nil {
		return nil, errors.New("服务器公钥需要配合 HTTPS IP 地址使用")
	}
	want, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(c.PublicKey))
	if err != nil || len(want) != ed25519.PublicKeySize {
		return nil, errors.New("服务器公钥格式不正确")
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		// VerifyConnection replaces CA validation with an explicit trust anchor.
		// The TLS handshake still verifies possession of the pinned private key.
		InsecureSkipVerify: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) != 0 {
				got, ok := state.PeerCertificates[0].PublicKey.(ed25519.PublicKey)
				if ok && subtle.ConstantTimeCompare(got, want) == 1 {
					return nil
				}
			}
			return errors.New("跨网服务器公钥不匹配")
		},
	}, nil
}

// TURNProxy handles only authenticated TLS connections to the same IP as
// signaling. Pion uses this dialer for its TCP relay candidates.
func (c Config) TURNProxy() (proxy.Dialer, error) {
	if strings.TrimSpace(c.PublicKey) == "" {
		return nil, nil
	}
	endpoint, err := c.endpoint()
	if err != nil {
		return nil, err
	}
	cfg, err := c.pinnedTLS(endpoint)
	if err != nil {
		return nil, err
	}
	u, _ := url.Parse(endpoint)
	return &pinnedRelayDialer{
		host:   net.ParseIP(u.Hostname()),
		dialer: tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: cfg},
	}, nil
}

type pinnedRelayDialer struct {
	host   net.IP
	dialer tls.Dialer
}

func (d *pinnedRelayDialer) Dial(network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || !d.host.Equal(net.ParseIP(host)) {
		return nil, errors.New("中继地址与跨网服务器 IP 不一致")
	}
	return d.dialer.Dial(network, addr)
}
