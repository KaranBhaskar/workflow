package egress

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

var (
	ErrMissingURL      = errors.New("http_tool url is required")
	ErrInvalidURL      = errors.New("http_tool url must be an absolute http or https URL")
	ErrUnsafeTargetURL = errors.New("http_tool url target is not allowed")
)

func ValidateHTTPToolURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ErrMissingURL
	}

	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() {
		return ErrInvalidURL
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return ErrInvalidURL
	}
	if parsed.User != nil {
		return fmt.Errorf("%w: embedded credentials are not supported", ErrUnsafeTargetURL)
	}

	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return ErrInvalidURL
	}
	if isBlockedHostname(host) {
		return fmt.Errorf("%w: host %q is local or private", ErrUnsafeTargetURL, host)
	}

	ip := net.ParseIP(host)
	if ip != nil && isBlockedIP(ip) {
		return fmt.Errorf("%w: host %q is local or private", ErrUnsafeTargetURL, host)
	}

	return nil
}

func isBlockedHostname(host string) bool {
	if host == "localhost" {
		return true
	}

	for _, suffix := range []string{".localhost", ".local", ".internal", ".localdomain", ".home.arpa"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}

	return false
}

func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}
