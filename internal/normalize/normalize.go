package normalize

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"sort"
	"strings"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

type Options struct {
	KeepTracking bool
	MaxLength    int
}
type Normalizer struct{ opts Options }

type Host struct {
	Name        string `json:"host"`
	Registrable string `json:"registrable_domain,omitempty"`
	IsRoot      bool   `json:"is_root"`
}

func New(opts Options) *Normalizer {
	if opts.MaxLength == 0 {
		opts.MaxLength = 8192
	}
	return &Normalizer{opts: opts}
}

var tracking = map[string]struct{}{"gclid": {}, "fbclid": {}, "dclid": {}, "msclkid": {}, "mc_cid": {}, "mc_eid": {}, "srsltid": {}}

func (n *Normalizer) Normalize(raw string) (normalized, domain string, err error) {
	if len(raw) == 0 || len(raw) > n.opts.MaxLength {
		return "", "", fmt.Errorf("invalid URL length")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("parse URL: %w", err)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	if u.User != nil {
		return "", "", fmt.Errorf("URLs containing user info are rejected")
	}
	ascii, err := Hostname(u.Hostname())
	if err != nil {
		return "", "", err
	}
	port := u.Port()
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	hostname := ascii
	if strings.Contains(hostname, ":") {
		ascii = "[" + hostname + "]"
	}
	u.Host = ascii
	if port != "" {
		u.Host = net.JoinHostPort(hostname, port)
	}
	u.Fragment = ""
	// EscapedPath preserves meaningful encoded slashes. Clean only repeated literal slashes/dot segments.
	if u.Path == "" {
		u.Path = "/"
	} else if !strings.Contains(strings.ToLower(u.RawPath), "%2f") {
		trailing := strings.HasSuffix(u.Path, "/")
		u.Path = path.Clean(u.Path)
		if trailing && u.Path != "/" {
			u.Path += "/"
		}
		u.RawPath = ""
	}
	q := u.Query()
	if !n.opts.KeepTracking {
		for k := range q {
			lk := strings.ToLower(k)
			if strings.HasPrefix(lk, "utm_") {
				q.Del(k)
				continue
			}
			if _, ok := tracking[lk]; ok {
				q.Del(k)
			}
		}
	}
	// url.Values.Encode sorts keys and values, giving stable equivalence.
	u.RawQuery = encodeStable(q)
	return u.String(), hostname, nil
}

// Hostname is the single canonical hostname normalization path. It accepts a
// hostname (not a URL), applies IDNA lookup processing, removes a terminal DNS
// dot, and validates IP literals and DNS labels.
func Hostname(raw string) (string, error) {
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if strings.HasPrefix(host, "[") || strings.HasSuffix(host, "]") {
		if !(strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]")) {
			return "", fmt.Errorf("invalid IP literal")
		}
		host = host[1 : len(host)-1]
		if net.ParseIP(host) == nil {
			return "", fmt.Errorf("invalid IP literal")
		}
	}
	if host == "" {
		return "", fmt.Errorf("hostname is empty")
	}
	if ip := net.ParseIP(host); ip != nil {
		return strings.ToLower(ip.String()), nil
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return "", fmt.Errorf("invalid IDN: %w", err)
	}
	if len(ascii) > 253 || strings.HasPrefix(ascii, ".") || strings.Contains(ascii, "..") {
		return "", fmt.Errorf("invalid hostname")
	}
	for _, label := range strings.Split(ascii, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", fmt.Errorf("invalid hostname label")
		}
		for _, c := range []byte(label) {
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
				return "", fmt.Errorf("invalid hostname label")
			}
		}
	}
	return ascii, nil
}

func ClassifyHost(raw string) (Host, error) {
	host, err := Hostname(raw)
	if err != nil {
		return Host{}, err
	}
	h := Host{Name: host}
	if net.ParseIP(host) != nil {
		return h, nil
	}
	if root, e := publicsuffix.EffectiveTLDPlusOne(host); e == nil {
		h.Registrable, h.IsRoot = root, root == host
	}
	return h, nil
}

func encodeStable(v url.Values) string {
	for k := range v {
		sort.Strings(v[k])
	}
	return v.Encode()
}
