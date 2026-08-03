package catalog

func sameRowSlice(left, right [][]string) bool {
	if len(left) != len(right) {
		return false
	}
	if len(left) == 0 {
		return true
	}
	return &left[0] == &right[0]
}

func sameRowRef(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	if len(left) == 0 {
		return true
	}
	return &left[0] == &right[0]
}

func columnPositions(table Table, names []string) []int {
	indexes := make([]int, len(names))
	for number, name := range names {
		for index, column := range table.Columns {
			if Key(column) == Key(name) {
				indexes[number] = index
				break
			}
		}
	}
	return indexes
}

func rowKey(row []string, indexes []int) string {
	if len(indexes) == 1 {
		return row[indexes[0]]
	}
	parts := make([]string, len(indexes))
	for number, index := range indexes {
		parts[number] = row[index]
	}
	return stringsJoin(parts)
}

func rowEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func stringsJoin(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	size := 0
	for _, part := range parts {
		size += len(part) + 1
	}
	buffer := make([]byte, 0, size)
	for index, part := range parts {
		if index > 0 {
			buffer = append(buffer, 0)
		}
		buffer = append(buffer, part...)
	}
	return string(buffer)
}

func (s *Store) syncRowsLocked(previous, next Definition) error {
	prepared, err := s.prepareRowSync(previous, next)
	if err != nil {
		return err
	}
	return prepared.commit()
}
