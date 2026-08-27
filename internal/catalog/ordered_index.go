package catalog

import "sync"

// OrderedIndexRow is one immutable position and its canonical index keys.
type OrderedIndexRow struct {
	Position int
	Keys     []string
	Nulls    []bool
}

// OrderedIndexCache owns the ordered access paths for one immutable row image.
// Snapshots share this cache until a writer publishes a different row image.
type OrderedIndexCache struct {
	mu      sync.Mutex
	indexes map[string][]OrderedIndexRow
}

// CachedOrderedIndex returns one ordered access path. The first caller builds
// it while later callers reuse the immutable result.
func (t *Table) CachedOrderedIndex(key string, build func() ([]OrderedIndexRow, error)) ([]OrderedIndexRow, error) {
	if t.OrderedIndexes == nil {
		t.OrderedIndexes = &OrderedIndexCache{}
	}
	cache := t.OrderedIndexes
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if rows, found := cache.indexes[key]; found {
		return rows, nil
	}
	rows, err := build()
	if err != nil {
		return nil, err
	}
	if cache.indexes == nil {
		cache.indexes = map[string][]OrderedIndexRow{}
	}
	cache.indexes[key] = rows
	return rows, nil
}

func refreshOrderedIndexCaches(previous Definition, next *Definition) {
	for namespaceKey, namespace := range next.Namespaces {
		previousNamespace := previous.Namespaces[namespaceKey]
		for tableKey, table := range namespace.Tables {
			previousTable, found := previousNamespace.Tables[tableKey]
			if found && sameRowSlice(previousTable.Rows, table.Rows) && previousTable.OrderedIndexes != nil {
				table.OrderedIndexes = previousTable.OrderedIndexes
			} else {
				table.OrderedIndexes = &OrderedIndexCache{}
			}
			namespace.Tables[tableKey] = table
		}
		next.Namespaces[namespaceKey] = namespace
	}
}
