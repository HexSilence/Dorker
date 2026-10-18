package query

import (
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"

	"golang.org/x/net/idna"
)

type ValidationMode string

const (
	ValidationOff          ValidationMode = "off"
	ValidationConservative ValidationMode = "conservative"
	ValidationStrict       ValidationMode = "strict"
)

func ParseValidationMode(s string) (ValidationMode, error) {
	m := ValidationMode(strings.ToLower(strings.TrimSpace(s)))
	switch m {
	case ValidationOff, ValidationConservative, ValidationStrict:
		return m, nil
	}
	return "", fmt.Errorf("invalid validation mode %q\nvalid modes: off, conservative, strict", s)
}

type MatchState string

const (
	Match    MatchState = "MATCH"
	Mismatch MatchState = "MISMATCH"
	Unknown  MatchState = "UNKNOWN"
)

type ValidationReason string

const (
	ReasonSiteMatch         ValidationReason = "site_match"
	ReasonSiteMismatch      ValidationReason = "site_mismatch"
	ReasonExcludedSite      ValidationReason = "excluded_site"
	ReasonExtensionMatch    ValidationReason = "extension_match"
	ReasonExtensionMismatch ValidationReason = "extension_mismatch"
	ReasonExtensionUnknown  ValidationReason = "extension_unknown"
	ReasonExcludedExtension ValidationReason = "excluded_extension"
	ReasonInURLMatch        ValidationReason = "inurl_match"
	ReasonInURLMismatch     ValidationReason = "inurl_mismatch"
	ReasonExcludedInURL     ValidationReason = "excluded_inurl"
)

type SiteConstraint struct {
	Value    string
	Wildcard bool
}

func (s SiteConstraint) String() string {
	if s.Wildcard {
		return "*." + s.Value
	}
	if strings.Count(s.Value, ".") == 0 {
		return "." + s.Value
	}
	return s.Value
}

type QueryConstraints struct {
	Sites, ExcludedSites                               []SiteConstraint
	Extensions, ExcludedExts, URLContains, URLExcludes []string
	ProviderOnly                                       []string
	HasEnforceable                                     bool
}
type Evaluation struct {
	Constraint string
	State      MatchState
	Reason     ValidationReason
}
type ValidationResult struct {
	Decision    string
	State       MatchState
	Reasons     []ValidationReason
	Evaluations []Evaluation
}

func ExtractConstraints(raw string) (QueryConstraints, error) {
	var c QueryConstraints
	for _, tok := range tokens(raw) {
		if tok.quoted {
			c.ProviderOnly = append(c.ProviderOnly, tok.text)
			continue
		}
		neg := strings.HasPrefix(tok.text, "-")
		body := tok.text
		if neg {
			body = strings.TrimPrefix(body, "-")
		}
		i := strings.IndexByte(body, ':')
		if i <= 0 || i == len(body)-1 {
			c.ProviderOnly = append(c.ProviderOnly, tok.text)
			continue
		}
		op, val := strings.ToLower(body[:i]), strings.TrimSpace(body[i+1:])
		if strings.Contains(val, `"`) {
			c.ProviderOnly = append(c.ProviderOnly, tok.text)
			continue
		}
		switch op {
		case "site":
			s, err := normalizeSite(val)
			if err != nil {
				return c, err
			}
			if neg {
				c.ExcludedSites = appendUniqueSite(c.ExcludedSites, s)
			} else {
				c.Sites = appendUniqueSite(c.Sites, s)
			}
			c.HasEnforceable = true
		case "ext", "filetype":
			v := strings.ToLower(strings.TrimPrefix(val, "."))
			if v == "" {
				c.ProviderOnly = append(c.ProviderOnly, tok.text)
				continue
			}
			if neg {
				c.ExcludedExts = appendUnique(c.ExcludedExts, v)
			} else {
				c.Extensions = appendUnique(c.Extensions, v)
			}
			c.HasEnforceable = true
		case "inurl":
			v := strings.ToLower(val)
			if neg {
				c.URLExcludes = appendUnique(c.URLExcludes, v)
			} else {
				c.URLContains = appendUnique(c.URLContains, v)
			}
			c.HasEnforceable = true
		default:
			c.ProviderOnly = append(c.ProviderOnly, tok.text)
		}
	}
	return c, nil
}

type token struct {
	text   string
	quoted bool
}

func tokens(s string) []token {
	var out []token
	var b strings.Builder
	quoted, hadQuote := false, false
	flush := func() {
		if b.Len() > 0 {
			out = append(out, token{b.String(), hadQuote})
			b.Reset()
			hadQuote = false
		}
	}
	for _, r := range s {
		if r == '"' {
			quoted = !quoted
			hadQuote = true
			b.WriteRune(r)
			continue
		}
		if !quoted && (r == ' ' || r == '\t' || r == '\n' || r == '\r') {
			flush()
			continue
		}
		b.WriteRune(r)
	}
	flush()
	return out
}
func normalizeSite(v string) (SiteConstraint, error) {
	v = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(v), "."))
	wild := strings.HasPrefix(v, "*.")
	if wild {
		v = strings.TrimPrefix(v, "*.")
	} else {
		v = strings.TrimPrefix(v, ".")
	}
	a, err := idna.Lookup.ToASCII(v)
	if err != nil || a == "" || strings.ContainsAny(a, "/?#:@") {
		return SiteConstraint{}, fmt.Errorf("invalid site constraint %q", v)
	}
	return SiteConstraint{Value: a, Wildcard: wild}, nil
}
func appendUnique(a []string, v string) []string {
	for _, x := range a {
		if x == v {
			return a
		}
	}
	return append(a, v)
}
func appendUniqueSite(a []SiteConstraint, v SiteConstraint) []SiteConstraint {
	for _, x := range a {
		if x == v {
			return a
		}
	}
	return append(a, v)
}

func (c QueryConstraints) Evaluate(normalized string, mode ValidationMode) ValidationResult {
	if mode == ValidationOff || !c.HasEnforceable {
		return ValidationResult{Decision: "ACCEPT", State: Match}
	}
	u, err := url.Parse(normalized)
	if err != nil {
		return ValidationResult{Decision: "REJECT", State: Mismatch}
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	var ev []Evaluation
	if len(c.Sites) > 0 {
		ok := false
		for _, s := range c.Sites {
			if siteMatch(host, s) {
				ok = true
				break
			}
		}
		if ok {
			ev = append(ev, Evaluation{"site", Match, ReasonSiteMatch})
		} else {
			ev = append(ev, Evaluation{"site", Mismatch, ReasonSiteMismatch})
		}
	}
	for _, s := range c.ExcludedSites {
		if siteMatch(host, s) {
			ev = append(ev, Evaluation{"-site:" + s.String(), Mismatch, ReasonExcludedSite})
		}
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(u.Path), "."))
	if len(c.Extensions) > 0 {
		state, reason := Unknown, ReasonExtensionUnknown
		if ext != "" {
			if contains(c.Extensions, ext) {
				state, reason = Match, ReasonExtensionMatch
			} else {
				state, reason = Mismatch, ReasonExtensionMismatch
			}
		}
		ev = append(ev, Evaluation{"extension", state, reason})
	}
	if len(c.ExcludedExts) > 0 {
		state, reason := Unknown, ReasonExtensionUnknown
		if ext != "" {
			if contains(c.ExcludedExts, ext) {
				state, reason = Mismatch, ReasonExcludedExtension
			} else {
				state, reason = Match, ReasonExtensionMatch
			}
		}
		ev = append(ev, Evaluation{"excluded extension", state, reason})
	}
	semantic := strings.ToLower(u.EscapedPath())
	if u.RawQuery != "" {
		semantic += "?" + strings.ToLower(u.RawQuery)
	}
	if len(c.URLContains) > 0 {
		ok := false
		for _, v := range c.URLContains {
			if strings.Contains(semantic, v) {
				ok = true
				break
			}
		}
		if ok {
			ev = append(ev, Evaluation{"inurl", Match, ReasonInURLMatch})
		} else {
			ev = append(ev, Evaluation{"inurl", Mismatch, ReasonInURLMismatch})
		}
	}
	for _, v := range c.URLExcludes {
		if strings.Contains(semantic, v) {
			ev = append(ev, Evaluation{"-inurl:" + v, Mismatch, ReasonExcludedInURL})
		}
	}
	state := Match
	for _, e := range ev {
		if e.State == Mismatch {
			state = Mismatch
			break
		}
		if e.State == Unknown {
			state = Unknown
		}
	}
	decision := "ACCEPT"
	if state == Mismatch || (state == Unknown && mode == ValidationStrict) {
		decision = "REJECT"
	}
	reasons := make([]ValidationReason, 0, len(ev))
	for _, e := range ev {
		reasons = append(reasons, e.Reason)
	}
	return ValidationResult{Decision: decision, State: state, Reasons: reasons, Evaluations: ev}
}
func siteMatch(host string, s SiteConstraint) bool {
	if s.Wildcard {
		return host != s.Value && strings.HasSuffix(host, "."+s.Value)
	}
	return host == s.Value || strings.HasSuffix(host, "."+s.Value)
}
func contains(a []string, v string) bool {
	i := sort.SearchStrings(a, v)
	if i < len(a) && a[i] == v {
		return true
	}
	for _, x := range a {
		if x == v {
			return true
		}
	}
	return false
}
