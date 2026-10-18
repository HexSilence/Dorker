package query

import "testing"

func TestExtractConstraints(t *testing.T) {
	cases := []struct {
		q                string
		sites, exts, neg int
		provider         int
	}{{"site:.edu ext:pdf", 1, 1, 0, 0}, {"SITE:.EDU FILETYPE:PDF", 1, 1, 0, 0}, {"site:example.com -site:private.example.com", 1, 0, 1, 0}, {"ext:pdf -ext:html", 0, 1, 1, 0}, {`"site:example.com"`, 0, 0, 0, 1}, {`"filetype:pdf"`, 0, 0, 0, 1}, {`site:example.com "annual report"`, 1, 0, 0, 1}, {`site:example.com intitle:"annual report" ext:pdf`, 1, 1, 0, 1}, {`site:example.com inurl:reports ext:pdf`, 1, 1, 0, 0}}
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
