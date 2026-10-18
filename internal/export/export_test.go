package export

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HexSilence/dorker/internal/model"
	"github.com/HexSilence/dorker/internal/store"
)

func TestWriteRefusesExistingAndExportsUnique(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.CreateJob(ctx, "j", "{}"); err != nil {
		t.Fatal(err)
	}
	r := model.Result{URL: "https://example.test/a?utm_source=x", NormalizedURL: "https://example.test/a", Domain: "example.test", Provider: "p", OriginalQuery: "q", QueryVariant: "q", Rank: 1, Page: "0", DiscoveredAt: time.Now()}
	if _, err = st.InsertResult(ctx, "j", r); err != nil {
		t.Fatal(err)
	}
	r.Provider = "p2"
	if _, err = st.InsertResult(ctx, "j", r); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = Write(ctx, st, "j", "txt", "-", false, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "https://example.test/a\n" {
		t.Fatal(out.String())
	}
	dest := filepath.Join(dir, "out.txt")
	if err = os.WriteFile(dest, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = Write(ctx, st, "j", "txt", dest, false, &out); err == nil {
		t.Fatal("expected existing-file error")
	}
	b, _ := os.ReadFile(dest)
	if string(b) != "keep" {
		t.Fatalf("destination changed: %q", b)
	}
}
