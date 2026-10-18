package ghdb

import (
	"strings"
	"testing"
)

func TestStableIDsAndOrdering(t *testing.T) {
	entries := All()
	if len(entries) != 25 {
		t.Fatalf("entries=%d", len(entries))
	}
	for i, e := range entries {
		if e.ID != i+1 {
			t.Fatalf("entry %d has ID %d", i, e.ID)
		}
		if got, ok := ByID(e.ID); !ok || got.Title != e.Title {
			t.Fatalf("ByID(%d)=%v/%v", e.ID, got, ok)
		}
	}
	if _, ok := ByID(0); ok {
		t.Fatal("ID 0 unexpectedly exists")
	}
}

func TestCategoryAndFind(t *testing.T) {
	if got := Category("documents"); len(got) != 3 {
		t.Fatalf("documents=%d", len(got))
	}
	got := Find("login")
	if len(got) == 0 || got[0].ID != 3 {
		t.Fatalf("find login=%v", got)
	}
}

func TestDomainExpansion(t *testing.T) {
	e, _ := ByID(5)
	got, err := Expand(e, "Example.COM.")
	if err != nil || got != `site:example.com intitle:"index of"` {
		t.Fatalf("query=%q err=%v", got, err)
	}
	got, err = Expand(e, "")
	if err != nil || got != `intitle:"index of"` {
		t.Fatalf("blank query=%q err=%v", got, err)
	}
	for _, bad := range []string{"example.com\nsite:evil.test", "https://example.com", "example com", "localhost"} {
		if _, err = Expand(e, bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if strings.Contains(got, "{{") {
		t.Fatal("template variable was not removed")
	}
}
