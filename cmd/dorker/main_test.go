package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPublicCommandProcessSurface(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "dorker")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build dorker: %v\n%s", err, output)
	}

	home := t.TempDir()
	baseEnv := append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, "config"),
		"XDG_DATA_HOME="+filepath.Join(home, "data"),
		"XDG_CACHE_HOME="+filepath.Join(home, "cache"),
		"DORKER_NO_UPDATE_CHECK=1",
	)

	tests := []struct {
		name string
		args []string
		code int
	}{
		{name: "root help", args: []string{"--help"}},
		{name: "short root help", args: []string{"-h"}},
		{name: "version", args: []string{"version"}},
		{name: "search help", args: []string{"search", "--help"}},
		{name: "discover help", args: []string{"discover", "--help"}},
		{name: "ghdb help", args: []string{"ghdb", "--help"}},
		{name: "ghdb list help", args: []string{"ghdb", "list", "--help"}},
		{name: "ghdb show help", args: []string{"ghdb", "show", "--help"}},
		{name: "ghdb find help", args: []string{"ghdb", "find", "--help"}},
		{name: "ghdb run help", args: []string{"ghdb", "run", "--help"}},
		{name: "setup help", args: []string{"setup", "--help"}},
		{name: "setup searxng help", args: []string{"setup", "searxng", "--help"}},
		{name: "doctor help", args: []string{"doctor", "--help"}},
		{name: "providers help", args: []string{"providers", "--help"}},
		{name: "providers test help", args: []string{"providers", "test", "--help"}},
		{name: "query help", args: []string{"query", "--help"}},
		{name: "query explain help", args: []string{"query", "explain", "--help"}},
		{name: "query validate help", args: []string{"query", "validate", "--help"}},
		{name: "jobs help", args: []string{"jobs", "--help"}},
		{name: "resume help", args: []string{"resume", "--help"}},
		{name: "stats help", args: []string{"stats", "--help"}},
		{name: "export help", args: []string{"export", "--help"}},
		{name: "config help", args: []string{"config", "--help"}},
		{name: "config init help", args: []string{"config", "init", "--help"}},
		{name: "config check help", args: []string{"config", "check", "--help"}},
		{name: "unknown root flag", args: []string{"--definitely-invalid-flag"}, code: 2},
		{name: "version extra argument", args: []string{"version", "extra"}, code: 2},
		{name: "nested help is not data", args: []string{"ghdb", "show", "--help"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(bin, tt.args...)
			cmd.Env = baseEnv
			output, err := cmd.CombinedOutput()
			code := 0
			if err != nil {
				var exitErr *exec.ExitError
				if !strings.Contains(err.Error(), "exit status") || !asExitError(err, &exitErr) {
					t.Fatalf("run %q: %v\n%s", tt.args, err, output)
				}
				code = exitErr.ExitCode()
			}
			if code != tt.code {
				t.Fatalf("run %q: exit %d, want %d\n%s", tt.args, code, tt.code, output)
			}
			if len(output) == 0 {
				t.Fatalf("run %q: expected user-facing output", tt.args)
			}
		})
	}
}

func asExitError(err error, target **exec.ExitError) bool {
	exitErr, ok := err.(*exec.ExitError)
	if ok {
		*target = exitErr
	}
	return ok
}
