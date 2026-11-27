package updater

import (
	"testing"
)

func TestNormalizeVersion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"v0.0.1", "0.0.1"},
		{"V0.0.1", "0.0.1"},
		{"0.0.1", "0.0.1"},
		{"  v1.2.3-dev ", "1.2.3-dev"},
		{"", ""},
	}
	for _, tt := range cases {
		if got := NormalizeVersion(tt.in); got != tt.want {
			t.Fatalf("NormalizeVersion(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseVersion(t *testing.T) {
	t.Parallel()
	ok, err := ParseVersion("v0.0.1-dev")
	if err != nil {
		t.Fatal(err)
	}
	if ok.Major != 0 || ok.Minor != 0 || ok.Patch != 1 || ok.Prerelease != "dev" {
		t.Fatalf("parsed = %+v", ok)
	}
	if ok.Raw != "0.0.1-dev" {
		t.Fatalf("Raw = %q", ok.Raw)
	}

	for _, bad := range []string{"", "v", "1.2", "1.2.x", "01.2.3", "-1.0.0", "v0.0.1.4"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Fatalf("ParseVersion(%q) expected error", bad)
		}
	}
}

func TestIsNewer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name            string
		latest, current string
		want            bool
		wantErr         bool
	}{
		{name: "numbered newer", latest: "v0.0.2", current: "0.0.1", want: true},
		{name: "numbered equal", latest: "0.0.2", current: "v0.0.2", want: false},
		{name: "numbered older", latest: "0.0.1", current: "0.0.2", want: false},
		{name: "dev to same numbered", latest: "v0.0.1", current: "0.0.1-dev", want: true},
		{name: "dev to newer numbered", latest: "0.0.2", current: "0.0.1-dev", want: true},
		{name: "dev to older numbered", latest: "0.0.1", current: "0.0.2-dev", want: false},
		{name: "numbered ignores prerelease latest", latest: "0.0.3-dev", current: "0.0.2", want: false},
		{name: "v prefix both", latest: "v1.0.0", current: "v0.9.9", want: true},
		{name: "invalid latest", latest: "nope", current: "0.0.1", wantErr: true},
		{name: "invalid current", latest: "0.0.2", current: "x", wantErr: true},
		{name: "dev to equal-core other-dev", latest: "0.0.1-beta", current: "0.0.1-dev", want: false},
		{name: "dev to greater-core other-dev", latest: "0.0.2-beta", current: "0.0.1-dev", want: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := IsNewer(tt.latest, tt.current)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tt.want {
				t.Fatalf("IsNewer(%q,%q) = %v, want %v", tt.latest, tt.current, got, tt.want)
			}
		})
	}
}
