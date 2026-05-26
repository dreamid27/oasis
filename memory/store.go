// memory/store.go
package memory

import "github.com/nevindra/oasis/core"

// ItemStore is the memory-item persistence interface. Package-level alias
// for core.MemoryItemStore.
type ItemStore = core.MemoryItemStore

// Filter selects MemoryItems for read or delete queries. Package-level alias
// for core.MemoryFilter.
type Filter = core.MemoryFilter

