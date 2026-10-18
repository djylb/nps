package file

import (
	"sort"
	"sync"
)

// sortClientByKey returns the ids of the clients in m sorted by their inlet
// or export traffic, sortKey "InletFlow" or "ExportFlow", descending unless
// order is "asc". The traffic is read once per client under its lock, and
// entries that are not clients are left out.
func sortClientByKey(m *sync.Map, sortKey, order string) (res []int) {
	type entry struct {
		id    int
		value int64
	}
	var list []entry
	m.Range(func(_, value interface{}) bool {
		c, ok := value.(*Client)
		if !ok || c == nil {
			return true
		}
		v := c.Flow.Values()
		n := v.InletFlow
		if sortKey == "ExportFlow" {
			n = v.ExportFlow
		}
		list = append(list, entry{c.Id, n})
		return true
	})
	sort.SliceStable(list, func(i, j int) bool {
		if order == "asc" {
			return list[i].value < list[j].value
		}
		return list[i].value > list[j].value
	})
	res = make([]int, 0, len(list))
	for _, e := range list {
		res = append(res, e.id)
	}
	return res
}
