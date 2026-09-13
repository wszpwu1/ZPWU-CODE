package agent

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ssrfStrict enables additional blocking of loopback and private (RFC1918 /
// IPv6 ULA) address ranges for user-supplied LLM base_url values. It is off by
// default because the common self-hosted deployment points the agent at a local
// inference gateway (Ollama/vLLM on 127.0.0.1 or a LAN host). It is toggled from
// config at process start via SetSSRFStrict.
var ssrfStrict bool

// SetSSRFStrict configures whether loopback/private hosts are rejected by
// ValidateLLMBaseURL. Safe to call once during startup.
func SetSSRFStrict(v bool) { ssrfStrict = v }

// alwaysBlockedHostnames are resolved by the OS to internal metadata endpoints
// on some clouds; reject them regardless of strictness.
var alwaysBlockedHostnames = map[string]struct{}{
	"metadata.google.internal": {},
	"metadata.goog":            {},
}

// ValidateLLMBaseURL rejects base_url values that would let the server reach
// internal/metadata endpoints (SSRF). It always blocks link-local, the cloud
// metadata range 169.254.0.0/16, IPv6 link-local, unspecified and broadcast
// addresses. When ssrfStrict is set it additionally blocks loopback, RFC1918
// private space and IPv6 ULA, and resolves hostnames to catch DNS names that
// point at internal addresses.
func ValidateLLMBaseURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil // caller supplies a safe default when empty
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid base_url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("base_url scheme must be http or https")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("base_url has no host")
	}
	lh := strings.ToLower(strings.TrimSuffix(host, "."))
	if _, blocked := alwaysBlockedHostnames[lh]; blocked {
		return fmt.Errorf("base_url host is not allowed")
	}

	// Hostname in bracket form (IPv6) or bare. net.LookupIP accepts a literal IP
	// directly, so we let non-strict mode check literals without DNS.
	if ip := net.ParseIP(host); ip != nil {
		if reason := blockedIPOrigin(ip); reason != "" {
			return fmt.Errorf("base_url host %q is not allowed (%s)", host, reason)
		}
		return nil
	}

	// A bare (non-IP) hostname. Non-strict mode trusts DNS but still blocks the
	// obvious internal names. Strict mode resolves and validates the answers.
	if ssrfStrict {
		addrs, lookupErr := net.LookupIP(host)
		if lookupErr != nil {
			// Cannot verify → reject under strict policy.
			return fmt.Errorf("base_url host %q could not be resolved", host)
		}
		for _, ip := range addrs {
			if reason := blockedIPOrigin(ip); reason != "" {
				return fmt.Errorf("base_url host %q resolves to a disallowed address (%s)", host, reason)
			}
		}
	}
	return nil
}

// blockedIPOrigin returns a short reason when ip must not be contacted, or ""
// when it is permitted under the current strictness setting.
func blockedIPOrigin(ip net.IP) string {
	if ip == nil {
		return "unparseable address"
	}
	// Always-blocked: link-local (incl. 169.254.0.0/16 cloud metadata),
	// interface-local multicast, unspecified, and multicast/broadcast.
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return "link-local/metadata range"
	}
	if ip.IsUnspecified() {
		return "unspecified address"
	}
	if ip.IsMulticast() {
		return "multicast address"
	}
	if is4 := ip.To4(); is4 != nil {
		if is4[0] == 169 && is4[1] == 254 {
			return "cloud metadata range"
		}
	}
	// Strict-only blocks: loopback + private/RFC1918 + IPv6 ULA.
	if ssrfStrict {
		if ip.IsLoopback() {
			return "loopback address"
		}
		if ip.IsPrivate() {
			return "private address"
		}
	}
	return ""
}
