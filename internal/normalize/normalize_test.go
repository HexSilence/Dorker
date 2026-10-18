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
func TestRejectSchemes(t *testing.T) {
	if _, _, err := New(Options{}).Normalize("javascript:alert(1)"); err == nil {
		t.Fatal("expected error")
	}
}

func BenchmarkNormalize(b *testing.B) {
	n := New(Options{})
	for b.Loop() {
		_, _, _ = n.Normalize("https://Example.com:443/a/b?utm_source=x&x=1#f")
	}
}
