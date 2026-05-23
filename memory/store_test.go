// memory/store_test.go
package memory

import (
	"testing"

	"github.com/nevindra/oasis/core"
)

func TestFilter_ZeroValue(t *testing.T) {
	var f Filter
	if len(f.Kinds) != 0 || f.Scope != nil || f.Pinned != nil || f.Limit != 0 || f.IncludeExp {
		t.Fatalf("zero Filter not empty: %+v", f)
	}
}

// Compile-time assertion that the Store type composes core.Store and ItemStore.
var _ Store = (interface {
	core.Store
	ItemStore
})(nil)
