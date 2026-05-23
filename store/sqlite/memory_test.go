// store/sqlite/memory_test.go
package sqlite_test

import (
	"context"
	"testing"

	"github.com/nevindra/oasis/memory"
	"github.com/nevindra/oasis/memory/memtest"
	"github.com/nevindra/oasis/store/sqlite"
)

func TestSQLite_ItemStoreConformance(t *testing.T) {
	memtest.ConformanceTest(t, func(t *testing.T) memory.ItemStore {
		ctx := context.Background()
		s := sqlite.New(":memory:")
		if err := s.Init(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s.Memory() // returns the ItemStore handle on the satellite store
	})
}
