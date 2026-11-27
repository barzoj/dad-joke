package archtest_test

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

const mongoDriverPrefix = "go.mongodb.org/mongo-driver"

func TestMongoDriverImportIsolation(t *testing.T) {
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
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		t.Fatalf("packages.Load: %v", err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		t.Fatal("packages.Load reported errors")
	}

	for _, pkg := range pkgs {
		if pkg.PkgPath == "" {
			continue
		}
		if isMongoStorePackage(pkg.PkgPath) {
			continue
		}
		for imp := range pkg.Imports {
			if strings.HasPrefix(imp, mongoDriverPrefix) {
				t.Errorf("package %s imports %s (only internal/store/mongo may import mongo-driver)", pkg.PkgPath, imp)
			}
		}
	}
}

func isMongoStorePackage(pkgPath string) bool {
	return strings.HasSuffix(pkgPath, "/internal/store/mongo") ||
		strings.HasSuffix(pkgPath, "/internal/store/mongo_test")
}
