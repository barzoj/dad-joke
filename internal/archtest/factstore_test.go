package archtest_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/barzoj/dad-joke/internal/store"
)

func TestFactStore_InsertOnly(t *testing.T) {
	t.Parallel()
	iface := reflect.TypeOf((*store.FactStore)(nil)).Elem()
	if iface.NumMethod() == 0 {
		t.Fatal("FactStore has no methods")
	}
	for i := 0; i < iface.NumMethod(); i++ {
		name := iface.Method(i).Name
		lower := strings.ToLower(name)
		if strings.Contains(lower, "update") || strings.Contains(lower, "delete") ||
			strings.Contains(lower, "remove") || strings.Contains(lower, "replace") {
			t.Fatalf("FactStore must be insert/read-only; found %s", name)
		}
	}
	// Must expose InsertBatch.
	if _, ok := iface.MethodByName("InsertBatch"); !ok {
		t.Fatal("FactStore missing InsertBatch")
	}
}
