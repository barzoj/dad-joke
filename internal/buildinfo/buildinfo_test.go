package buildinfo

import "testing"

func TestDefaults(t *testing.T) {
	t.Parallel()

	if Version != DefaultVersion {
		t.Fatalf("Version default = %q, want %q", Version, DefaultVersion)
	}
	if Commit != DefaultCommit {
		t.Fatalf("Commit default = %q, want %q", Commit, DefaultCommit)
	}
	if ReleaseAPIURL != DefaultReleaseAPIURL {
		t.Fatalf("ReleaseAPIURL default = %q, want %q", ReleaseAPIURL, DefaultReleaseAPIURL)
	}
	if DefaultVersion != "0.0.1-dev" {
		t.Fatalf("DefaultVersion = %q, want 0.0.1-dev", DefaultVersion)
	}
	if DefaultCommit != "unknown" {
		t.Fatalf("DefaultCommit = %q, want unknown", DefaultCommit)
	}
	wantURL := "https://api.github.com/repos/barzoj/dad-joke/releases/latest"
	if DefaultReleaseAPIURL != wantURL {
		t.Fatalf("DefaultReleaseAPIURL = %q, want %q", DefaultReleaseAPIURL, wantURL)
	}
}

func TestFormatVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version string
		commit  string
		want    string
	}{
		{
			name:    "normal",
			version: "1.2.3",
			commit:  "abc1234",
			want:    "dad-joke 1.2.3 (commit abc1234)",
		},
		{
			name:    "empty version uses default",
			version: "",
			commit:  "deadbeef",
			want:    "dad-joke 0.0.1-dev (commit deadbeef)",
		},
		{
			name:    "empty commit uses unknown",
			version: "0.0.1",
			commit:  "",
			want:    "dad-joke 0.0.1 (commit unknown)",
		},
		{
			name:    "both empty",
			version: "",
			commit:  "",
			want:    "dad-joke 0.0.1-dev (commit unknown)",
		},
		{
			name:    "dev default shape",
			version: DefaultVersion,
			commit:  DefaultCommit,
			want:    "dad-joke 0.0.1-dev (commit unknown)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := FormatVersion(tt.version, tt.commit)
			if got != tt.want {
				t.Fatalf("FormatVersion(%q, %q) = %q, want %q", tt.version, tt.commit, got, tt.want)
			}
		})
	}
}

func TestFormatUsesPackageVars(t *testing.T) {
	t.Parallel()

	got := Format()
	want := FormatVersion(Version, Commit)
	if got != want {
		t.Fatalf("Format() = %q, want %q", got, want)
	}
}
