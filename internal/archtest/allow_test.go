package archtest_test

import (
	"testing"

	"github.com/barzoj/dad-joke/internal/archtest"
)

func TestIsMongoStorePackage_ExactMatchOnly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want bool
	}{
		{path: "github.com/barzoj/dad-joke/internal/store/mongo", want: true},
		{path: "github.com/barzoj/dad-joke/internal/store/mongo_test", want: true},
		{path: "github.com/barzoj/dad-joke/cmd/evil/internal/store/mongo", want: false},
		{path: "github.com/barzoj/dad-joke/cmd/evil/internal/store/mongo_test", want: false},
		{path: "github.com/barzoj/dad-joke/internal/store", want: false},
		{path: "github.com/barzoj/dad-joke/internal/store/memory", want: false},
		{path: "github.com/example/other/internal/store/mongo", want: false},
		{path: "", want: false},
	}
	for _, tc := range cases {
		if got := archtest.IsMongoStorePackage(tc.path); got != tc.want {
			t.Errorf("IsMongoStorePackage(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
