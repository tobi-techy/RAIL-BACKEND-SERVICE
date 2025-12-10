package security

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"time"
)

// TLSConfig holds TLS configuration options
type TLSConfig struct {
	MinVersion       uint16
	CertFile         string
	KeyFile          string
	CAFile           string // For mTLS client verification
	ClientAuth       tls.ClientAuthType
	PinnedCerts      []string // SHA256 fingerprints of pinned certificates
	EnableHTTP2      bool
	SessionTickets   bool
	PreferServerCiphers bool
}

// DefaultTLSConfig returns secure TLS 1.3 configuration
func DefaultTLSConfig() *TLSConfig {
	return &TLSConfig{
		MinVersion:          tls.VersionTLS13,
		EnableHTTP2:         true,
		SessionTickets:      true,
		PreferServerCiphers: true,
	}
}

// BuildTLSConfig creates a tls.Config from TLSConfig
func (c *TLSConfig) BuildTLSConfig() (*tls.Config, error) {
	tlsConfig := &tls.Config{
		MinVersion:               c.MinVersion,
		PreferServerCipherSuites: c.PreferServerCiphers,
		SessionTicketsDisabled:   !c.SessionTickets,
		// TLS 1.3 cipher suites (automatically selected, these are for TLS 1.2 fallback)
		CipherSuites: []uint16{
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_CHACHA20_POLY1305_SHA256,
			// TLS 1.2 fallback (if needed)
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		},
		// Curve preferences for ECDHE
		CurvePreferences: []tls.CurveID{
			tls.X25519,
			tls.CurveP384,
			tls.CurveP256,
		},
	}

	// Load server certificate if provided
	if c.CertFile != "" && c.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load server certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	// Configure mTLS if CA file provided
	if c.CAFile != "" {
		caCert, err := os.ReadFile(c.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA file: %w", err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}

		tlsConfig.ClientCAs = caCertPool
		tlsConfig.ClientAuth = c.ClientAuth
	}

	// Enable HTTP/2
	if c.EnableHTTP2 {
		tlsConfig.NextProtos = []string{"h2", "http/1.1"}
	}

	return tlsConfig, nil
}

// MTLSConfig creates configuration for mutual TLS
func MTLSConfig(certFile, keyFile, caFile string) (*tls.Config, error) {
	config := &TLSConfig{
		MinVersion: tls.VersionTLS13,
		CertFile:   certFile,
		KeyFile:    keyFile,
		CAFile:     caFile,
		ClientAuth: tls.RequireAndVerifyClientCert,
	}
	return config.BuildTLSConfig()
}

// CertificatePinningTransport wraps http.Transport with certificate pinning
type CertificatePinningTransport struct {
	Transport      *http.Transport
	PinnedCerts    map[string]bool // SHA256 fingerprints
	AllowUnpinned  bool            // For development
}

// NewCertificatePinningTransport creates a transport with certificate pinning
func NewCertificatePinningTransport(pinnedFingerprints []string) *CertificatePinningTransport {
	pinned := make(map[string]bool)
	for _, fp := range pinnedFingerprints {
		pinned[fp] = true
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13,
		},
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}

	return &CertificatePinningTransport{
		Transport:   transport,
		PinnedCerts: pinned,
	}
}

// RoundTrip implements http.RoundTripper with certificate pinning
func (t *CertificatePinningTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Skip pinning for non-HTTPS or if no pins configured
	if req.URL.Scheme != "https" || len(t.PinnedCerts) == 0 || t.AllowUnpinned {
		return t.Transport.RoundTrip(req)
	}

	// Create custom TLS config with verification
	t.Transport.TLSClientConfig.VerifyPeerCertificate = func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
		for _, chain := range verifiedChains {
			for _, cert := range chain {
				fingerprint := CertificateFingerprint(cert)
				if t.PinnedCerts[fingerprint] {
					return nil // Certificate is pinned
				}
			}
		}
		return fmt.Errorf("certificate not pinned")
	}

	return t.Transport.RoundTrip(req)
}

// CertificateFingerprint returns SHA256 fingerprint of a certificate
func CertificateFingerprint(cert *x509.Certificate) string {
	hash := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(hash[:])
}

// ServiceToServiceClient creates an HTTP client for internal service communication with mTLS
type ServiceToServiceClient struct {
	client *http.Client
}

// NewServiceToServiceClient creates a client with mTLS for service-to-service communication
func NewServiceToServiceClient(certFile, keyFile, caFile string) (*ServiceToServiceClient, error) {
	// Load client certificate
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load client certificate: %w", err)
	}

	// Load CA certificate
	caCert, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA file: %w", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}

	tlsConfig := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
	}

	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
		MaxIdleConns:    100,
		IdleConnTimeout: 90 * time.Second,
	}

	return &ServiceToServiceClient{
		client: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
	}, nil
}

// Do performs an HTTP request with mTLS
func (c *ServiceToServiceClient) Do(req *http.Request) (*http.Response, error) {
	return c.client.Do(req)
}

// MobileCertPinConfig returns certificate pinning configuration for mobile apps
type MobileCertPinConfig struct {
	Domains     []string `json:"domains"`
	Pins        []Pin    `json:"pins"`
	ExpiresAt   string   `json:"expires_at"`
	EnforceMode bool     `json:"enforce_mode"`
}

type Pin struct {
	Algorithm   string `json:"algorithm"` // SHA256
	Fingerprint string `json:"fingerprint"`
	Comment     string `json:"comment,omitempty"`
}

// GenerateMobilePinConfig generates certificate pinning config for mobile apps
func GenerateMobilePinConfig(domains []string, certFiles []string) (*MobileCertPinConfig, error) {
	config := &MobileCertPinConfig{
		Domains:     domains,
		Pins:        []Pin{},
		ExpiresAt:   time.Now().AddDate(0, 6, 0).Format(time.RFC3339), // 6 months
		EnforceMode: true,
	}

	for _, certFile := range certFiles {
		certPEM, err := os.ReadFile(certFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read cert file %s: %w", certFile, err)
		}

		// Parse certificate
		block, _ := x509.ParseCertificate(certPEM)
		if block != nil {
			fingerprint := CertificateFingerprint(block)
			config.Pins = append(config.Pins, Pin{
				Algorithm:   "SHA256",
				Fingerprint: fingerprint,
				Comment:     certFile,
			})
		}
	}

	return config, nil
}

// SecureHTTPServer creates an HTTP server with secure TLS configuration
func SecureHTTPServer(addr string, handler http.Handler, tlsConfig *TLSConfig) (*http.Server, error) {
	tls, err := tlsConfig.BuildTLSConfig()
	if err != nil {
		return nil, err
	}

	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		TLSConfig:         tls,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1MB
	}, nil
}
