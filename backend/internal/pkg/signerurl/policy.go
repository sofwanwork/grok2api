package signerurl

import (
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

const MaxLength = 2048

// Validate 接受公网 HTTPS:443，或管理员显式配置的内网 HTTP/HTTPS 地址。
// 内网范围包括容器单标签服务名、localhost、.local/.internal 和私有地址。
func Validate(value string) error {
	raw := strings.TrimSpace(value)
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Contains(raw, "#") || len(raw) > MaxLength {
		return fmt.Errorf("URL tandatangan mesti alamat penuh tanpa kredensial, parameter pertanyaan dan serpihan")
	}
	if port := parsed.Port(); port != "" {
		value, portErr := strconv.Atoi(port)
		if portErr != nil || value < 1 || value > 65535 {
			return fmt.Errorf("Port URL tandatangan tidak sah")
		}
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	internal := IsInternalHost(host)
	switch strings.ToLower(parsed.Scheme) {
	case "http":
		if internal {
			return nil
		}
	case "https":
		if internal || parsed.Port() == "" || parsed.Port() == "443" {
			return nil
		}
	}
	return fmt.Errorf("URL tandatangan awam mesti menggunakan HTTPS:443; HTTP dan port tersuai hanya dibenarkan untuk alamat rangkaian dalaman yang dipercayai")
}

func IsInternalHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if address, err := netip.ParseAddr(host); err == nil {
		address = address.Unmap()
		return address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast()
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return true
	}
	return !strings.Contains(host, ".") && validServiceLabel(host)
}

func validServiceLabel(value string) bool {
	if len(value) < 1 || len(value) > 63 || !asciiAlphaNumeric(value[0]) || !asciiAlphaNumeric(value[len(value)-1]) {
		return false
	}
	for index := 1; index < len(value)-1; index++ {
		if !asciiAlphaNumeric(value[index]) && value[index] != '-' && value[index] != '_' {
			return false
		}
	}
	return true
}

func asciiAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}
