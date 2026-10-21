package buildinfo

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestResolveMetadata(t *testing.T) {
	const revision = "e95ad2bf00265d81362f1b945e90f490735a8c57"
	tests := []struct {
		name     string
		explicit metadata
		info     *debug.BuildInfo
		want     metadata
	}{
		{
			name:     "explicit linker metadata wins",
			explicit: metadata{Version: "v9.8.7", Commit: "abcdef123456", Date: "2026-10-19T01:02:03Z"},
			info:     buildInfo("v1.0.1", revision, "2026-10-18T22:00:00Z", "true"),
			want:     metadata{Version: "v9.8.7", Commit: "abcdef1", Date: "2026-10-19T01:02:03Z", DateKey: "built"},
		},
		{
			name: "module version fallback",
			info: &debug.BuildInfo{Main: debug.Module{Version: "v1.0.1"}},
			want: metadata{Version: "v1.0.1"},
		},
		{
			name: "VCS fallback and dirty marker",
			info: buildInfo("(devel)", revision, "2026-10-18T22:00:00Z", "true"),
			want: metadata{Version: ReleaseVersion, Commit: "e95ad2b-dirty", Date: "2026-10-18T22:00:00Z", DateKey: "commit-date"},
		},
		{
			name: "no metadata",
			want: metadata{Version: ReleaseVersion},
		},
		{
			name: "devel is not displayed as version",
			info: &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}},
			want: metadata{Version: ReleaseVersion},
		},
		{
			name: "pseudo-version retains release version",
			info: &debug.BuildInfo{Main: debug.Module{Version: "v1.0.0-0.20261018220943-e95ad2bf0026+dirty"}},
			want: metadata{Version: ReleaseVersion, Commit: "e95ad2b", Date: "2026-10-18T22:09:43Z", DateKey: "commit-date"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolve(tc.explicit, tc.info); got != tc.want {
				t.Fatalf("resolve()=%+v want %+v", got, tc.want)
			}
		})
	}
}

func TestResolveDoesNotGenerateCurrentTimestamp(t *testing.T) {
	got := resolve(metadata{}, nil)
	if got.Date != "" || got.DateKey != "" {
		t.Fatalf("unexpected generated date: %+v", got)
	}
}

func TestStringOmitsUnavailableMetadata(t *testing.T) {
	oldVersion, oldCommit, oldDate, oldDateKey := Version, Commit, Date, dateKey
	t.Cleanup(func() { Version, Commit, Date, dateKey = oldVersion, oldCommit, oldDate, oldDateKey })
	Version, Commit, Date, dateKey = "v1.0.1", "", "", ""
	if got := String(); got != "dorker v1.0.1" || strings.Contains(got, "unknown") {
		t.Fatal(got)
	}
}

func buildInfo(version, revision, vcsTime, modified string) *debug.BuildInfo {
	return &debug.BuildInfo{
		Main: debug.Module{Version: version},
		Settings: []debug.BuildSetting{
			{Key: "vcs", Value: "git"},
			{Key: "vcs.revision", Value: revision},
			{Key: "vcs.time", Value: vcsTime},
			{Key: "vcs.modified", Value: modified},
		},
	}
}
