package passoauth

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// Profile contains the endpoints and client identity used by passo-cli.
type Profile struct {
	Name       string
	APIURL     string
	IssuerURL  string
	ClientID   string
	ConsoleURL string
}

// ResolveProfile returns a profile with environment overrides applied.
func ResolveProfile(name string) (Profile, error) {
	p := Profile{Name: name, ClientID: "passo-cli"}
	switch name {
	case "sandbox":
		p.APIURL = "http://127.0.0.1:16080"
		p.IssuerURL = "http://127.0.0.1:18081/realms/passobuild"
		p.ConsoleURL = "http://127.0.0.1:13000"
	case "staging":
	default:
		return Profile{}, fmt.Errorf("unknown profile %q", name)
	}
	if v := os.Getenv("PASSO_API_URL"); v != "" {
		p.APIURL = v
	}
	if v := os.Getenv("PASSO_ISSUER_URL"); v != "" {
		p.IssuerURL = v
	}
	if v := os.Getenv("PASSO_CONSOLE_URL"); v != "" {
		p.ConsoleURL = v
	}
	if v := os.Getenv("PASSO_CLIENT_ID"); v != "" {
		p.ClientID = v
	}
	if err := ValidateProfile(p); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// ValidateProfile checks that all configured endpoints are safe HTTP(S) URLs.
func ValidateProfile(p Profile) error {
	if p.Name != "sandbox" && p.Name != "staging" {
		return fmt.Errorf("invalid profile name %q", p.Name)
	}
	if strings.TrimSpace(p.ClientID) == "" {
		return fmt.Errorf("client ID is required")
	}
	for label, raw := range map[string]string{"API": p.APIURL, "issuer": p.IssuerURL, "console": p.ConsoleURL} {
		if raw == "" {
			return fmt.Errorf("%s URL is required", label)
		}
		u, err := url.Parse(raw)
		if err != nil || u.Scheme == "" || u.Hostname() == "" {
			return fmt.Errorf("invalid %s URL", label)
		}
		if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("invalid %s URL", label)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("invalid %s URL scheme", label)
		}
		if u.Scheme == "http" && !isLoopbackHost(u.Hostname()) {
			return fmt.Errorf("HTTP %s URL must use a loopback host", label)
		}
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
