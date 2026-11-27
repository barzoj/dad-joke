// Package archtest holds architecture-boundary checks enforced in CI.
package archtest

const modulePath = "github.com/barzoj/dad-joke"

// IsMongoStorePackage reports whether pkgPath is exactly the MongoDB store
// package (or its external test package). Suffix matches are rejected so a
// nested path like …/cmd/evil/internal/store/mongo cannot bypass the boundary.
func IsMongoStorePackage(pkgPath string) bool {
	switch pkgPath {
	case modulePath + "/internal/store/mongo",
		modulePath + "/internal/store/mongo_test":
		return true
	default:
		return false
	}
}
