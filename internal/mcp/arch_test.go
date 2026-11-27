package mcp_test

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

const (
	modulePath       = "github.com/barzoj/dad-joke"
	storePrefix      = modulePath + "/internal/store"
	mongoDriverPrefix = "go.mongodb.org/mongo-driver"
)

// TestMCPPackagesNoStoreOrMongoDriver fails CI if any MCP package imports
// internal/store (or subpackages) or mongo-driver.
func TestMCPPackagesNoStoreOrMongoDriver(t *testing.T) {
	t.Parallel()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))

	cfg := &packages.Config{
		Mode:  packages.NeedName | packages.NeedImports | packages.NeedFiles,
		Dir:   repoRoot,
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, "./internal/mcp", "./internal/mcp/...")
	if err != nil {
		t.Fatalf("packages.Load: %v", err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		t.Fatal("packages.Load reported errors")
	}
	if len(pkgs) == 0 {
		t.Fatal("no MCP packages loaded")
	}

	for _, pkg := range pkgs {
		if pkg.PkgPath == "" {
			continue
		}
		// Production MCP code only. External E2E tests (package mcp_test) may boot
		// the full server and therefore import store/memory — that is intentional.
		if pkg.PkgPath != modulePath+"/internal/mcp" &&
			!strings.HasPrefix(pkg.PkgPath, modulePath+"/internal/mcp/") {
			continue
		}
		if strings.HasSuffix(pkg.PkgPath, "_test") {
			continue
		}
		for imp := range pkg.Imports {
			if imp == storePrefix || strings.HasPrefix(imp, storePrefix+"/") {
				t.Errorf("MCP package %s imports %s (forbidden)", pkg.PkgPath, imp)
			}
			if strings.HasPrefix(imp, mongoDriverPrefix) {
				t.Errorf("MCP package %s imports %s (forbidden)", pkg.PkgPath, imp)
			}
		}
	}
}
