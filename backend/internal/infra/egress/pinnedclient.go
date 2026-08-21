package egress

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/chenyme/grok2api/backend/internal/pkg/tunnelproxy"
	xproxy "golang.org/x/net/proxy"
)

// DoPinnedHTTPS sends a request whose URL host is an already validated IP
// address while preserving serverName for TLS verification. Keeping this
// transport separate from the shared browser client prevents a later DNS
// lookup or a pooled connection from reopening an SSRF validation gap.
func (l *Lease) DoPinnedHTTPS(request *http.Request, serverName string) (*http.Response, error) {
	if l == nil {
		return nil, errors.New("Pajakan egress tidak dimulakan")
	}
	if request == nil || request.URL == nil || request.URL.Scheme != "https" || request.URL.Port() != "443" {
		return nil, errors.New("Permintaan alamat tetap mesti menggunakan HTTPS 443")
	}
	address, err := netip.ParseAddr(request.URL.Hostname())
	if err != nil {
		return nil, errors.New("Permintaan alamat tetap mesti menggunakan hos IP")
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsMulticast() || address.IsUnspecified() {
		return nil, errors.New("Permintaan alamat tetap mesti menggunakan IP awam")
	}
	serverName = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(serverName)), ".")
	hostURL, err := url.Parse("https://" + request.Host)
	if err != nil || strings.TrimSuffix(strings.ToLower(hostURL.Hostname()), ".") != serverName {
		return nil, errors.New("Host permintaan alamat tetap tidak sepadan dengan TLS ServerName")
	}
	client, err := newPinnedHTTPSClient(l.ProxyURL, serverName, nil)
	if err != nil {
		return nil, err
	}
	return client.Do(request)
}

func newPinnedHTTPSClient(proxyURL, serverName string, tlsConfig *tls.Config) (*http.Client, error) {
	serverName = strings.TrimSuffix(strings.TrimSpace(serverName), ".")
	if serverName == "" {
		return nil, errors.New("TLS ServerName tak boleh kosong")
	}
	direct := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if tlsConfig == nil {
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		tlsConfig = tlsConfig.Clone()
	}
	tlsConfig.ServerName = serverName
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           direct.DialContext,
		ForceAttemptHTTP2:     true,
		DisableKeepAlives:     true,
		TLSClientConfig:       tlsConfig,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	if strings.TrimSpace(proxyURL) != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("Huraian proksi egress alamat tetap: %w", err)
		}
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https":
			transport.Proxy = http.ProxyURL(parsed)
		case "socks4", "socks4a", "socks5", "socks5h":
			dialer, err := xproxy.FromURL(parsed, direct)
			if err != nil {
				return nil, fmt.Errorf("Mencipta proksi SOCKS alamat tetap: %w", err)
			}
			transport.DialContext = dialContext(dialer)
		case "trojan", "vless", "ss", "vmess":
			dialer, err := tunnelproxy.NewDialer(proxyURL)
			if err != nil {
				return nil, fmt.Errorf("Mencipta proksi terowong alamat tetap: %w", err)
			}
			transport.DialContext = dialer.DialContext
		default:
			return nil, fmt.Errorf("Permintaan alamat tetap tidak menyokong protokol proksi %q", parsed.Scheme)
		}
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}
