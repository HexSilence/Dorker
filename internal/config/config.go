package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/HexSilence/dorker/internal/buildinfo"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Database    DatabaseConfig            `yaml:"database"`
	Scheduler   SchedulerConfig           `yaml:"scheduler"`
	Retry       RetryConfig               `yaml:"retry"`
	HTTP        HTTPConfig                `yaml:"http"`
	Providers   map[string]ProviderConfig `yaml:"providers"`
	Validation  ValidationConfig          `yaml:"validation"`
	Output      OutputConfig              `yaml:"output"`
	UI          UIConfig                  `yaml:"ui"`
	Discovery   DiscoveryConfig           `yaml:"discovery"`
	UpdateCheck UpdateCheckConfig         `yaml:"update_check"`
}
type UpdateCheckConfig struct {
	Enabled bool `yaml:"enabled"`
}
type DiscoveryConfig struct {
	MaxDepth                    int           `yaml:"max_depth"`
	MaxHosts                    int           `yaml:"max_hosts"`
	MaxQueries                  int           `yaml:"max_queries"`
	MaxDuration                 time.Duration `yaml:"max_duration"`
	SaturationThreshold         float64       `yaml:"saturation_threshold"`
	SaturationConsecutiveDepths int           `yaml:"saturation_consecutive_depths"`
}
type OutputConfig struct {
	Collapse  string `yaml:"collapse"`
	Directory string `yaml:"directory"`
}
type UIConfig struct {
	Banner bool   `yaml:"banner"`
	Color  string `yaml:"color"`
}
type ValidationConfig struct {
	Mode string `yaml:"mode"`
}
type DatabaseConfig struct {
	Path string `yaml:"path"`
}
type SchedulerConfig struct {
	Workers    int              `yaml:"workers"`
	Saturation SaturationConfig `yaml:"saturation"`
	Recovery   RecoveryConfig   `yaml:"recovery"`
}
type SaturationConfig struct {
	Enabled                   bool `yaml:"enabled"`
	ConsecutiveZeroYieldPages int  `yaml:"consecutive_zero_yield_pages"`
}
type RecoveryConfig struct {
	Wait    bool          `yaml:"wait"`
	MaxWait time.Duration `yaml:"max_wait"`
}
type RetryConfig struct {
	MaxAttempts int           `yaml:"max_attempts"`
	BaseDelay   time.Duration `yaml:"base_delay"`
	MaxDelay    time.Duration `yaml:"max_delay"`
	Jitter      bool          `yaml:"jitter"`
}
type HTTPConfig struct {
	RequestTimeout time.Duration `yaml:"request_timeout"`
	ConnectTimeout time.Duration `yaml:"connect_timeout"`
	MaxBodyBytes   int64         `yaml:"max_body_bytes"`
}
type ProviderConfig struct {
	Enabled           bool                    `yaml:"enabled"`
	RequestsPerSecond float64                 `yaml:"requests_per_second"`
	Burst             int                     `yaml:"burst"`
	Strategy          string                  `yaml:"strategy"`
	Engines           []string                `yaml:"engines"`
	Categories        []string                `yaml:"categories"`
	Language          string                  `yaml:"language"`
	SafeSearch        *int                    `yaml:"safesearch"`
	TimeRange         string                  `yaml:"time_range"`
	Instances         []SearXNGInstanceConfig `yaml:"instances"`
}
type SearXNGInstanceConfig struct {
	Name              string  `yaml:"name"`
	URL               string  `yaml:"url"`
	TokenEnv          string  `yaml:"token_env"`
	RequestsPerSecond float64 `yaml:"requests_per_second"`
	Burst             int     `yaml:"burst"`
}

func Default() Config {
	return Config{
		Database:    DatabaseConfig{Path: defaultDatabasePath()},
		Scheduler:   SchedulerConfig{Workers: 5, Saturation: SaturationConfig{Enabled: true, ConsecutiveZeroYieldPages: 2}, Recovery: RecoveryConfig{MaxWait: 2 * time.Minute}},
		Retry:       RetryConfig{MaxAttempts: 4, BaseDelay: 2 * time.Second, MaxDelay: 2 * time.Minute, Jitter: true},
		HTTP:        HTTPConfig{RequestTimeout: 20 * time.Second, ConnectTimeout: 5 * time.Second, MaxBodyBytes: 4 << 20},
		Validation:  ValidationConfig{Mode: "conservative"},
		Output:      OutputConfig{Collapse: "none", Directory: defaultOutputPath()},
		UI:          UIConfig{Banner: true, Color: "auto"},
		Discovery:   DiscoveryConfig{MaxDepth: 3, MaxHosts: 5000, MaxQueries: 10000, MaxDuration: 30 * time.Minute, SaturationThreshold: 0.03, SaturationConsecutiveDepths: 2},
		UpdateCheck: UpdateCheckConfig{Enabled: true},
		Providers: map[string]ProviderConfig{
			"searxng":    {Enabled: true, RequestsPerSecond: 1, Burst: 1},
			"google-api": {Enabled: true, RequestsPerSecond: 1, Burst: 1},
			"brave":      {Enabled: true, RequestsPerSecond: 1, Burst: 2},
			"serpapi":    {Enabled: false, RequestsPerSecond: 1, Burst: 1},
			"mock":       {Enabled: false, RequestsPerSecond: 100, Burst: 10},
		},
	}
}

func defaultDatabasePath() string {
	return filepath.Join(defaultDataDir(), buildinfo.Name+".db")
}

// DefaultDatabasePath and DefaultOutputPath expose the resolved locations for
// diagnostics and documentation without requiring callers to duplicate OS
// path rules.
func DefaultDatabasePath() string { return defaultDatabasePath() }
func DefaultOutputPath() string   { return defaultOutputPath() }
func UpdateCachePath() string     { return filepath.Join(defaultDataDir(), "update.json") }

func defaultDataDir() string {
	home, _ := os.UserHomeDir()
	configDir, _ := os.UserConfigDir()
	return dataDirForOS(runtime.GOOS, home, configDir, os.Getenv("XDG_DATA_HOME"), os.Getenv("LOCALAPPDATA"))
}

func dataDirForOS(goos, home, configDir, xdgData, localAppData string) string {
	switch goos {
	case "linux":
		if xdgData != "" {
			return filepath.Join(xdgData, buildinfo.Name)
		}
		if home != "" {
			return filepath.Join(home, ".local", "share", buildinfo.Name)
		}
	case "windows":
		if localAppData != "" {
			return filepath.Join(localAppData, buildinfo.Name)
		}
	}
	if configDir != "" {
		return filepath.Join(configDir, buildinfo.Name)
	}
	if home != "" {
		return filepath.Join(home, ".dorker")
	}
	return ".dorker"
}

func defaultOutputPath() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, "dorker-output")
	}
	return filepath.Join(defaultDataDir(), "output")
}

func DefaultPath() string {
	if p := os.Getenv(buildinfo.EnvPrefix + "_CONFIG"); p != "" {
		return p
	}
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, buildinfo.Name, "config.yaml")
	}
	return "config.yaml"
}

func Load(path string) (Config, error) {
	c := Default()
	if path == "" {
		path = DefaultPath()
	}
	b, err := os.ReadFile(expandHome(path))
	if errors.Is(err, os.ErrNotExist) {
		return c, c.Validate()
	}
	if err != nil {
		return c, fmt.Errorf("read config: %w", err)
	}
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("parse config: %w", err)
	}
	return c, c.Validate()
}

// Bootstrap writes a complete default configuration atomically when none
// exists. The hard-link publication step is atomic and cannot replace an
// existing file, including one created concurrently by another process.
func Bootstrap(path string) (bool, error) {
	if path == "" {
		path = DefaultPath()
	}
	path = expandHome(path)
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("inspect config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return false, fmt.Errorf("create config directory: %w", err)
	}
	b, err := yaml.Marshal(Default())
	if err != nil {
		return false, fmt.Errorf("encode default config: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".dorker-config-*")
	if err != nil {
		return false, fmt.Errorf("create temporary config: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return false, fmt.Errorf("write config: %w", err)
	}
	if err = os.Link(tmp, path); err != nil {
		if _, statErr := os.Stat(path); statErr == nil {
			return false, nil
		}
		return false, fmt.Errorf("publish config: %w", err)
	}
	return true, nil
}

func (c Config) Validate() error {
	if c.UI.Color != "auto" && c.UI.Color != "always" && c.UI.Color != "never" {
		return errors.New("ui.color must be auto, always, or never")
	}
	if c.Validation.Mode != "off" && c.Validation.Mode != "conservative" && c.Validation.Mode != "strict" {
		return errors.New("validation.mode must be off, conservative, or strict")
	}
	if c.Output.Collapse != "none" && c.Output.Collapse != "host" && c.Output.Collapse != "domain" {
		return errors.New("output.collapse must be none, host, or domain")
	}
	if strings.TrimSpace(c.Output.Directory) == "" {
		return errors.New("output.directory must not be empty")
	}
	if c.Scheduler.Workers < 1 || c.Scheduler.Workers > 256 {
		return errors.New("scheduler.workers must be between 1 and 256")
	}
	if c.Scheduler.Saturation.ConsecutiveZeroYieldPages < 1 || c.Scheduler.Saturation.ConsecutiveZeroYieldPages > 100 {
		return errors.New("scheduler.saturation.consecutive_zero_yield_pages must be between 1 and 100")
	}
	if c.Scheduler.Recovery.MaxWait < 0 {
		return errors.New("scheduler.recovery.max_wait must not be negative")
	}
	if c.Retry.MaxAttempts < 1 || c.Retry.MaxAttempts > 20 {
		return errors.New("retry.max_attempts must be between 1 and 20")
	}
	if c.Retry.BaseDelay < 0 || c.Retry.MaxDelay < c.Retry.BaseDelay {
		return errors.New("invalid retry delays")
	}
	if c.HTTP.MaxBodyBytes < 1024 {
		return errors.New("http.max_body_bytes must be at least 1024")
	}
	if c.Discovery.MaxDepth < 0 || c.Discovery.MaxHosts < 1 || c.Discovery.MaxQueries < 1 || c.Discovery.MaxDuration <= 0 || c.Discovery.SaturationThreshold < 0 || c.Discovery.SaturationThreshold > 1 || c.Discovery.SaturationConsecutiveDepths < 1 {
		return errors.New("invalid discovery limits")
	}
	for n, p := range c.Providers {
		if p.RequestsPerSecond < 0 || p.Burst < 0 {
			return fmt.Errorf("provider %s has invalid rate limit", n)
		}
	}
	searx := c.Providers["searxng"]
	if searx.Strategy != "" && searx.Strategy != "balanced" && searx.Strategy != "coverage" {
		return errors.New("searxng.strategy must be balanced or coverage")
	}
	seen := map[string]bool{}
	for _, in := range searx.Instances {
		if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.URL) == "" {
			return errors.New("searxng instances require name and url")
		}
		if seen[in.Name] {
			return fmt.Errorf("duplicate searxng instance name %q", in.Name)
		}
		seen[in.Name] = true
		if in.RequestsPerSecond < 0 || in.Burst < 0 {
			return fmt.Errorf("searxng instance %s has invalid rate limit", in.Name)
		}
		u, err := url.Parse(strings.TrimSpace(in.URL))
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
			return fmt.Errorf("searxng instance %s has invalid URL (http/https, host, and no embedded credentials required)", in.Name)
		}
	}
	return nil
}

func ExpandPath(p string) string { return expandHome(p) }
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, strings.TrimPrefix(p, "~/"))
		}
	}
	return p
}
