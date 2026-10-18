package normalize

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"sort"
	"strings"

	"golang.org/x/net/idna"
)

type Options struct {
	KeepTracking bool
	MaxLength    int
}
type Normalizer struct{ opts Options }

func New(opts Options) *Normalizer {
	if opts.MaxLength == 0 {
		opts.MaxLength = 8192
	}
	return &Normalizer{opts: opts}
}

var tracking = map[string]struct{}{"gclid": {}, "fbclid": {}, "dclid": {}, "msclkid": {}, "mc_cid": {}, "mc_eid": {}}

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
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return "", "", fmt.Errorf("URL has no host")
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return "", "", fmt.Errorf("invalid IDN: %w", err)
	}
	port := u.Port()
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	if strings.Contains(ascii, ":") {
		ascii = "[" + ascii + "]"
	}
	u.Host = ascii
	if port != "" {
		u.Host = net.JoinHostPort(strings.Trim(ascii, "[]"), port)
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
	return u.String(), strings.ToLower(host), nil
}

func encodeStable(v url.Values) string {
	for k := range v {
		sort.Strings(v[k])
	}
	return v.Encode()
}
