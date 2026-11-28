//go:build !windows

package tzmap

import (
	"os"
	"path/filepath"
	"strings"
)

// windowsZoneID is unavailable off Windows.
func windowsZoneID() (string, bool) { return "", false }

// localIANAName tries to read the IANA id from /etc/localtime symlink.
func localIANAName() string {
	target, err := os.Readlink("/etc/localtime")
	if err != nil {
		return ""
	}
	const marker = "/zoneinfo/"
	i := strings.Index(target, marker)
	if i < 0 {
		return ""
	}
	name := filepath.ToSlash(target[i+len(marker):])
	if name == "" || strings.Contains(name, "..") {
		return ""
	}
	return name
}
