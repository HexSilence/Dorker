package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidationMode(t *testing.T) {
	for _, mode := range []string{"off", "conservative", "strict"} {
		c := Default()
		c.Validation.Mode = mode
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", mode, err)
		}
	}
	c := Default()
	c.Validation.Mode = "maybe"
	if err := c.Validate(); err == nil {
		t.Fatal("expected invalid mode")
	}
}

func TestDataDirForSupportedPlatforms(t *testing.T) {
	tests := []struct {
		name, goos, home, configDir, xdg, local, want string
	}{
		{"linux xdg", "linux", "/home/alice", "/cfg", "/data", "", filepath.Join("/data", "dorker")},
		{"linux fallback", "linux", "/home/alice", "/cfg", "", "", filepath.Join("/home/alice", ".local", "share", "dorker")},
		{"macos", "darwin", "/Users/alice", filepath.Join("/Users/alice", "Library", "Application Support"), "", "", filepath.Join("/Users/alice", "Library", "Application Support", "dorker")},
		{"windows local", "windows", `C:\\Users\\Alice`, `C:\\Users\\Alice\\AppData\\Roaming`, "", `C:\\Users\\Alice\\AppData\\Local`, filepath.Join(`C:\\Users\\Alice\\AppData\\Local`, "dorker")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dataDirForOS(tt.goos, tt.home, tt.configDir, tt.xdg, tt.local); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestBootstrapCreatesSecureConfigAndNeverOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	created, err := Bootstrap(path)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "database:") {
		t.Fatalf("config=%q err=%v", b, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%v want=0600", info.Mode().Perm())
	}
	if _, err = Load(path); err != nil {
		t.Fatalf("load bootstrapped config: %v", err)
	}
	if err = os.WriteFile(path, []byte("preserve\n"), 0600); err != nil {
		t.Fatal(err)
	}
	created, err = Bootstrap(path)
	if err != nil || created {
		t.Fatalf("second created=%v err=%v", created, err)
	}
	b, _ = os.ReadFile(path)
	if string(b) != "preserve\n" {
		t.Fatalf("overwritten: %q", b)
	}
}
func TestUIColorMode(t *testing.T) {
	for _, mode := range []string{"auto", "always", "never"} {
		c := Default()
		c.UI.Color = mode
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	c := Default()
	c.UI.Color = "rainbow"
	if err := c.Validate(); err == nil {
		t.Fatal("expected invalid color mode")
	}
}

func TestUpdateCheckDefaultsAndCanBeDisabled(t *testing.T) {
	if !Default().UpdateCheck.Enabled {
		t.Fatal("passive release awareness should default on")
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("update_check:\n  enabled: false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UpdateCheck.Enabled {
		t.Fatal("explicit disable ignored")
	}
}
