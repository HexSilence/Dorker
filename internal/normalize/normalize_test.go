package normalize

import "testing"

func TestNormalize(t *testing.T) {
	n := New(Options{})
	tests := []struct{ in, want string }{
		{"HTTPS://ExAmPle.COM:443/a//b/../c#frag", "https://example.com/a/c"},
		{"http://example.com:80", "http://example.com/"},
		{"https://example.com/a?utm_source=x&b=2&a=1", "https://example.com/a?a=1&b=2"},
		{"https://bücher.example/", "https://xn--bcher-kva.example/"},
		{"https://example.com/%7Euser", "https://example.com/~user"},
	}
	for _, tc := range tests {
		got, _, err := n.Normalize(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("Normalize(%q)=%q,%v want %q", tc.in, got, err, tc.want)
		}
	}
}
func TestKeepTracking(t *testing.T) {
	got, _, _ := New(Options{KeepTracking: true}).Normalize("https://x.test/?utm_source=a")
	if got != "https://x.test/?utm_source=a" {
		t.Fatal(got)
	}
}

func TestSRSltidTrackingNormalization(t *testing.T) {
	tests := []struct {
		name, input, want string
		keep              bool
	}{
		{"only", "https://nv.sa/?srsltid=ABC", "https://nv.sa/", false},
		{"case insensitive", "https://nv.sa/?SRSltid=ABC", "https://nv.sa/", false},
		{"with utm", "https://example.sa/path?srsltid=ABC&utm_source=x", "https://example.sa/path", false},
		{"meaningful stable", "https://example.sa/path?z=2&srsltid=ABC&id=123", "https://example.sa/path?id=123&z=2", false},
		{"kept", "https://nv.sa/?srsltid=ABC", "https://nv.sa/?srsltid=ABC", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, err := New(Options{KeepTracking: tt.keep}).Normalize(tt.input)
			if err != nil || got != tt.want {
				t.Fatalf("Normalize()=%q,%v want %q", got, err, tt.want)
			}
		})
	}
}
func TestRejectSchemes(t *testing.T) {
	if _, _, err := New(Options{}).Normalize("javascript:alert(1)"); err == nil {
		t.Fatal("expected error")
	}
}

func TestHostnameAndClassification(t *testing.T) {
	for _, tc := range []struct{ in, want string }{{"EXAMPLE.COM.", "example.com"}, {"bücher.example", "xn--bcher-kva.example"}, {"192.0.2.1", "192.0.2.1"}, {"2001:DB8::1", "2001:db8::1"}} {
		got, err := Hostname(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("Hostname(%q)=%q,%v", tc.in, got, err)
		}
	}
	for _, bad := range []string{"", ".example.com", "bad..example", "-bad.example", "bad-.example", "name:443"} {
		if got, err := Hostname(bad); err == nil {
			t.Errorf("Hostname(%q)=%q, want error", bad, got)
		}
	}
	h, err := ClassifyHost("api.example.co.uk")
	if err != nil || h.Registrable != "example.co.uk" || h.IsRoot {
		t.Fatalf("%+v %v", h, err)
	}
	root, _ := ClassifyHost("example.co.uk")
	if !root.IsRoot {
		t.Fatalf("%+v", root)
	}
}

func TestNormalizeIPv6AndPorts(t *testing.T) {
	n := New(Options{})
	for _, tc := range []struct{ in, host string }{{"https://EXAMPLE.com:8443/a", "example.com"}, {"https://[2001:DB8::1]:443/", "2001:db8::1"}} {
		_, got, err := n.Normalize(tc.in)
		if err != nil || got != tc.host {
			t.Errorf("%q host=%q err=%v", tc.in, got, err)
		}
	}
}

func FuzzHostnameNeverPanics(f *testing.F) {
	for _, s := range []string{"example.com", "EXAMPLE.COM.", "bücher.example", "2001:db8::1", "bad..host", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		h, err := Hostname(s)
		if err == nil {
			again, e := Hostname(h)
			if e != nil || again != h {
				t.Fatalf("not idempotent: %q => %q => %q (%v)", s, h, again, e)
			}
		}
	})
}

func FuzzNormalizeNeverPanics(f *testing.F) {
	for _, s := range []string{"https://example.com/a", "https://attacker.test/?u=example.com", "://bad", "https://[::1]/"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) { _, _, _ = New(Options{}).Normalize(s) })
}

func BenchmarkNormalize(b *testing.B) {
	n := New(Options{})
	for b.Loop() {
		_, _, _ = n.Normalize("https://Example.com:443/a/b?utm_source=x&x=1#f")
	}
}
