package updater

import "testing"

func TestValidateDownloadURL(t *testing.T) {
	t.Parallel()
	ok := []string{
		"https://github.com/barzoj/dad-joke/releases/download/v0.0.2/x.exe",
		"http://127.0.0.1:8080/asset.exe",
		"http://localhost:9/a",
		"http://[::1]:8080/a",
	}
	for _, u := range ok {
		if err := ValidateDownloadURL(u); err != nil {
			t.Fatalf("ValidateDownloadURL(%q) = %v", u, err)
		}
	}
	bad := []string{
		"http://example.com/x",
		"ftp://127.0.0.1/x",
		"file:///C:/x",
		"://nope",
		"https://",
	}
	for _, u := range bad {
		if err := ValidateDownloadURL(u); err == nil {
			t.Fatalf("ValidateDownloadURL(%q) expected error", u)
		}
	}
}
