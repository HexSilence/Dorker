package buildinfo

import (
	"fmt"
	"regexp"
	"runtime/debug"
	"strings"
	"time"
)

const Name = "dorker"
const EnvPrefix = "DORKER"
const ReleaseVersion = "v1.0.0"

var (
	Version = "unknown"
	Commit  = "unknown"
	Date    = "unknown"
	dateKey = ""
)

type metadata struct {
	Version string
	Commit  string
	Date    string
	DateKey string
}

var pseudoVersion = regexp.MustCompile(`^v\d+\.\d+\.\d+-(?:0\.)?(\d{14})-([0-9a-f]{12,})(?:\+.*)?$`)

func init() {
	var info *debug.BuildInfo
	if current, ok := debug.ReadBuildInfo(); ok {
		info = current
	}
	resolved := resolve(metadata{Version: Version, Commit: Commit, Date: Date}, info)
	Version, Commit, Date, dateKey = resolved.Version, resolved.Commit, resolved.Date, resolved.DateKey
}

// String returns compact version information without fabricating unavailable
// build metadata. A commit-date is the VCS commit time, not the compile time.
func String() string {
	parts := make([]string, 0, 2)
	if meaningful(Commit) {
		parts = append(parts, "commit "+Commit)
	}
	if meaningful(Date) && dateKey != "" {
		parts = append(parts, dateKey+" "+Date)
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%s %s", Name, Version)
	}
	return fmt.Sprintf("%s %s (%s)", Name, Version, strings.Join(parts, ", "))
}

func resolve(explicit metadata, info *debug.BuildInfo) metadata {
	result := metadata{Version: ReleaseVersion}
	moduleVersion := ""
	if info != nil {
		moduleVersion = info.Main.Version
	}
	pseudoMatch := pseudoVersion.FindStringSubmatch(moduleVersion)
	if meaningful(explicit.Version) && explicit.Version != "(devel)" {
		result.Version = explicit.Version
	} else if meaningful(moduleVersion) && moduleVersion != "(devel)" && pseudoMatch == nil {
		result.Version = moduleVersion
	}

	if meaningful(explicit.Commit) {
		result.Commit = shortCommit(explicit.Commit)
	} else if revision, ok := buildSetting(info, "vcs.revision"); ok && meaningful(revision) {
		result.Commit = shortCommit(revision)
		if modified, _ := buildSetting(info, "vcs.modified"); modified == "true" {
			result.Commit += "-dirty"
		}
	} else if pseudoMatch != nil {
		result.Commit = shortCommit(pseudoMatch[2])
	}

	if meaningful(explicit.Date) {
		result.Date = explicit.Date
		result.DateKey = "built"
	} else if vcsTime, ok := buildSetting(info, "vcs.time"); ok {
		if parsed, err := time.Parse(time.RFC3339, vcsTime); err == nil {
			result.Date = parsed.UTC().Format(time.RFC3339)
			result.DateKey = "commit-date"
		}
	} else if pseudoMatch != nil {
		if parsed, err := time.Parse("20060102150405", pseudoMatch[1]); err == nil {
			result.Date = parsed.UTC().Format(time.RFC3339)
			result.DateKey = "commit-date"
		}
	}
	return result
}

func buildSetting(info *debug.BuildInfo, key string) (string, bool) {
	if info == nil {
		return "", false
	}
	for _, setting := range info.Settings {
		if setting.Key == key {
			return setting.Value, true
		}
	}
	return "", false
}

func meaningful(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && value != "unknown"
}

func shortCommit(commit string) string {
	commit = strings.TrimSpace(commit)
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
