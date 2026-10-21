package query

import (
	"strings"
	"testing"
)

func TestExtractConstraints(t *testing.T) {
	cases := []struct {
		q                string
		sites, exts, neg int
		provider         int
	}{{"site:.edu ext:pdf", 1, 1, 0, 0}, {"SITE:.EDU FILETYPE:PDF", 1, 1, 0, 0}, {"site:example.com -site:private.example.com", 1, 0, 1, 0}, {"ext:pdf -ext:html", 0, 1, 1, 0}, {`"site:example.com"`, 0, 0, 0, 1}, {`site:"example.com"`, 1, 0, 0, 0}, {`"filetype:pdf"`, 0, 0, 0, 1}, {`site:example.com "annual report"`, 1, 0, 0, 1}, {`site:example.com intitle:"annual report" ext:pdf`, 1, 1, 0, 1}, {`site:example.com inurl:reports ext:pdf`, 1, 1, 0, 0}}
	for _, tc := range cases {
		c, err := ExtractConstraints(tc.q)
		if err != nil {
			t.Fatal(err)
		}
		if len(c.Sites) != tc.sites || len(c.Extensions) != tc.exts || len(c.ExcludedSites)+len(c.ExcludedExts) != tc.neg || len(c.ProviderOnly) != tc.provider {
			t.Errorf("%q: %+v", tc.q, c)
		}
	}
}
func TestSiteValidation(t *testing.T) {
	tests := []struct {
		q, u string
		want MatchState
	}{{"site:example.com", "https://example.com", Match}, {"site:example.com", "https://www.example.com", Match}, {"site:example.com", "https://docs.example.com", Match}, {"site:example.com", "https://example.com.evil.test", Mismatch}, {"site:example.com", "https://notexample.com", Mismatch}, {"site:.edu", "https://mit.edu", Match}, {"site:.edu", "https://math.berkeley.edu", Match}, {"site:.edu", "https://example.com", Mismatch}, {"site:.edu", "https://fake.education", Mismatch}, {"site:*.example.com", "https://docs.example.com", Match}, {"site:*.example.com", "https://example.com", Mismatch}, {"site:bücher.example", "https://xn--bcher-kva.example/", Match}}
	for _, tc := range tests {
		c, _ := ExtractConstraints(tc.q)
		got := c.Evaluate(tc.u, ValidationConservative)
		if got.State != tc.want {
			t.Errorf("%s %s got %s", tc.q, tc.u, got.State)
		}
	}
}
func TestExtensionModes(t *testing.T) {
	tests := []struct {
		u     string
		state MatchState
	}{{"https://x.edu/a.pdf", Match}, {"https://x.edu/A.PDF?x=1#p", Match}, {"https://x.edu/a.html", Mismatch}, {"https://x.edu/download?id=1", Unknown}}
	c, _ := ExtractConstraints("ext:pdf")
	for _, tc := range tests {
		for _, m := range []ValidationMode{ValidationConservative, ValidationStrict} {
			r := c.Evaluate(tc.u, m)
			if r.State != tc.state {
				t.Errorf("%s %s", tc.u, r.State)
			}
			want := "ACCEPT"
			if tc.state == Mismatch || (tc.state == Unknown && m == ValidationStrict) {
				want = "REJECT"
			}
			if r.Decision != want {
				t.Errorf("%s %s got %s", tc.u, m, r.Decision)
			}
		}
	}
}
func TestNegativeAndMultipleExtensions(t *testing.T) {
	c, _ := ExtractConstraints("ext:pdf ext:docx -ext:html")
	for u, w := range map[string]MatchState{"https://x/a.pdf": Match, "https://x/a.docx": Match, "https://x/a.html": Mismatch, "https://x/download": Unknown} {
		if g := c.Evaluate(u, ValidationConservative).State; g != w {
			t.Errorf("%s got %s", u, g)
		}
	}
}
func TestNegativeSiteAndInURL(t *testing.T) {
	c, _ := ExtractConstraints("site:example.com -site:private.example.com inurl:reports -inurl:login")
	for u, w := range map[string]MatchState{"https://DOCS.EXAMPLE.COM./reports/a": Match, "https://private.example.com/reports/a": Mismatch, "https://docs.example.com/login/reports": Mismatch, "https://example.com/other": Mismatch} {
		if g := c.Evaluate(u, ValidationConservative).State; g != w {
			t.Errorf("%s got %s", u, g)
		}
	}
}
func TestSiteScopeIsMandatoryWhenValidationOff(t *testing.T) {
	c, _ := ExtractConstraints("site:example.com -site:dev.example.com ext:pdf")
	for _, tc := range []struct {
		u    string
		want string
	}{{"https://example.com/page.html", "ACCEPT"}, {"https://www.example.com/page.html", "ACCEPT"}, {"https://dev.example.com/a.pdf", "REJECT"}, {"https://a.dev.example.com/a.pdf", "REJECT"}, {"https://example.com.attacker.test/a.pdf", "REJECT"}} {
		if got := c.Evaluate(tc.u, ValidationOff).Decision; got != tc.want {
			t.Errorf("%s got %s", tc.u, got)
		}
	}
}
func TestMultiplePositiveSitesAreAlternatives(t *testing.T) {
	c, _ := ExtractConstraints("site:example.com site:example.org")
	for _, u := range []string{"https://example.com/", "https://a.example.org/"} {
		if c.Evaluate(u, ValidationConservative).Decision != "ACCEPT" {
			t.Errorf("rejected %s", u)
		}
	}
	if c.Evaluate("https://example.net/", ValidationConservative).Decision != "REJECT" {
		t.Fatal("accepted unrelated host")
	}
}
func TestCountrySuffixBoundaryFixture(t *testing.T) {
	c, _ := ExtractConstraints("site:.ye")
	for _, u := range []string{"https://example.ye/", "https://example.com.ye/", "https://a.example.edu.ye/"} {
		if c.Evaluate(u, ValidationConservative).Decision != "ACCEPT" {
			t.Errorf("rejected %s", u)
		}
	}
	for _, u := range []string{"https://example.yemen/", "https://example.ye.attacker.test/", "https://fakeye.test/", "https://attacker.test/?u=https://example.ye"} {
		if c.Evaluate(u, ValidationConservative).Decision != "REJECT" {
			t.Errorf("accepted %s", u)
		}
	}
}

func TestSASuffixAndNegativeBoundaries(t *testing.T) {
	positive, _ := ExtractConstraints("site:.sa")
	for _, u := range []string{"https://example.sa/", "https://www.example.sa/", "https://gov.sa/", "https://foo.gov.sa/"} {
		if positive.Evaluate(u, ValidationConservative).Decision != "ACCEPT" {
			t.Errorf("positive suffix rejected %s", u)
		}
	}
	for _, u := range []string{"https://example.sa.attacker.test/", "https://attacker.test/example.sa", "https://attacker.test/?next=example.sa", "https://attacker-sa.test/", "https://notsa.test/"} {
		if positive.Evaluate(u, ValidationConservative).Decision != "REJECT" {
			t.Errorf("positive suffix accepted %s", u)
		}
	}
	negative, _ := ExtractConstraints("-site:.sa")
	if negative.Evaluate("https://foo.gov.sa/", ValidationConservative).Decision != "REJECT" || negative.Evaluate("https://example.com/", ValidationConservative).Decision != "ACCEPT" {
		t.Fatal("negative suffix boundary failed")
	}
}
func TestOtherLocalConstraintBoundaries(t *testing.T) {
	for _, tc := range []struct{ q, u, want string }{{"filetype:pdf", "https://x.test/a.PDF", "ACCEPT"}, {"-filetype:pdf", "https://x.test/a.pdf", "REJECT"}, {"inurl:admin", "https://x.test/path/admin?q=1", "ACCEPT"}, {"-inurl:admin", "https://x.test/path?next=admin", "REJECT"}} {
		c, _ := ExtractConstraints(tc.q)
		if got := c.Evaluate(tc.u, ValidationStrict).Decision; got != tc.want {
			t.Errorf("%s %s got %s", tc.q, tc.u, got)
		}
	}
}
func TestMalformedAndMissingHostFailClosed(t *testing.T) {
	c, _ := ExtractConstraints("site:example.com")
	for _, u := range []string{"%zz", "https:///path"} {
		if got := c.Evaluate(u, ValidationConservative); got.Decision != "REJECT" {
			t.Errorf("accepted %q", u)
		}
	}
}
func TestSiteIPAndPortHandling(t *testing.T) {
	for _, tc := range []struct{ q, u string }{{"site:192.0.2.1", "http://192.0.2.1:8080/a"}, {"site:[2001:db8::1]", "https://[2001:db8::1]:8443/a"}} {
		c, err := ExtractConstraints(tc.q)
		if err != nil || c.Evaluate(tc.u, ValidationConservative).Decision != "ACCEPT" {
			t.Errorf("%s %s: %v", tc.q, tc.u, err)
		}
	}
	if _, err := ExtractConstraints("site:example.com:443"); err == nil {
		t.Fatal("site constraint containing port must be rejected")
	}
}
func FuzzSiteMatcherLabelBoundaries(f *testing.F) {
	for _, s := range []string{"example.com", "www.example.com", "notexample.com", "example.com.attacker.test", "EXAMPLE.COM."} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, h string) {
		s := SiteConstraint{Value: "example.com"}
		got := SiteMatch(h, s)
		lower := strings.ToLower(strings.TrimSuffix(h, "."))
		want := lower == "example.com" || strings.HasSuffix(lower, ".example.com")
		if got != want {
			t.Fatalf("%q got %v want %v", h, got, want)
		}
	})
}
func BenchmarkExtractConstraints(b *testing.B) {
	for b.Loop() {
		_, _ = ExtractConstraints(`site:.edu -site:private.edu inurl:reports ext:pdf -ext:html intitle:"annual report"`)
	}
}
func BenchmarkValidateConstraints(b *testing.B) {
	c, _ := ExtractConstraints("site:.edu inurl:reports ext:pdf")
	for b.Loop() {
		_ = c.Evaluate("https://mit.edu/reports/annual.pdf?download=1", ValidationConservative)
	}
}
