package index

import (
	"maps"
	"strings"
	"sync"
	"sync/atomic"
)

type node struct {
	children map[string]*node
	ids      map[int]struct{}
}

type DomainTree struct {
	root *node
}

func NewDomainTree() *DomainTree {
	return &DomainTree{
		root: &node{
			children: make(map[string]*node),
			ids:      make(map[int]struct{}),
		},
	}
}

func (dt *DomainTree) CloneAdd(domain string, id int) *DomainTree {
	parts := splitDomain(domain)
	newRoot := dt.root.cloneAdd(parts, id)
	return &DomainTree{root: newRoot}
}

func (n *node) cloneAdd(parts []string, id int) *node {
	newChildren := make(map[string]*node, len(n.children))
	maps.Copy(newChildren, n.children)
	newIDs := make(map[int]struct{}, len(n.ids)+1)
	for iid := range n.ids {
		newIDs[iid] = struct{}{}
	}

	if len(parts) == 0 {
		newIDs[id] = struct{}{}
		return &node{children: newChildren, ids: newIDs}
	}

	p := parts[0]
	child, ok := n.children[p]
	if !ok {
		child = &node{
			children: make(map[string]*node),
			ids:      make(map[int]struct{}),
		}
	}
	newChildren[p] = child.cloneAdd(parts[1:], id)
	return &node{children: newChildren, ids: newIDs}
}

func (dt *DomainTree) CloneRemove(domain string, id int) *DomainTree {
	parts := splitDomain(domain)
	newRoot, _ := dt.root.cloneRemove(parts, id)
	if newRoot == nil {
		return NewDomainTree()
	}
	return &DomainTree{root: newRoot}
}

func (n *node) cloneRemove(parts []string, id int) (*node, bool) {
	newChildren := make(map[string]*node, len(n.children))
	maps.Copy(newChildren, n.children)
	newIDs := make(map[int]struct{}, len(n.ids))
	for iid := range n.ids {
		newIDs[iid] = struct{}{}
	}

	if len(parts) == 0 {
		delete(newIDs, id)
	} else {
		p := parts[0]
		if child, ok := n.children[p]; ok {
			if nc, del := child.cloneRemove(parts[1:], id); del {
				delete(newChildren, p)
			} else {
				newChildren[p] = nc
			}
		}
	}

	if len(newIDs) == 0 && len(newChildren) == 0 {
		return nil, true
	}
	return &node{children: newChildren, ids: newIDs}, false
}

func (dt *DomainTree) Lookup(domain string) []int {
	parts := splitDomain(domain)
	out := make(map[int]struct{})
	dt.lookup(dt.root, parts, 0, out)
	res := make([]int, 0, len(out))
	for id := range out {
		res = append(res, id)
	}
	return res
}

func (dt *DomainTree) lookup(n *node, parts []string, depth int, out map[int]struct{}) {
	if n == nil {
		return
	}
	for id := range n.ids {
		out[id] = struct{}{}
	}
	if depth >= len(parts) {
		return
	}
	p := parts[depth]
	if child, ok := n.children[p]; ok {
		dt.lookup(child, parts, depth+1, out)
	}
	//if child, ok := n.children["*"]; ok {
	//	dt.lookup(child, parts, depth+1, out)
	//}
}

func splitDomain(domain string) []string {
	d := strings.ToLower(strings.TrimSpace(domain))
	if d == "" {
		return nil
	}
	parts := strings.Split(d, ".")
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return parts
}

func (dt *DomainTree) Has(domain string, id int) bool {
	parts := splitDomain(domain)
	n := dt.root
	for _, p := range parts {
		if n == nil {
			return false
		}
		n = n.children[p]
	}
	if n == nil {
		return false
	}
	_, ok := n.ids[id]
	return ok
}

type DomainIndex struct {
	treeStore atomic.Value // holds *DomainTree
	mu        sync.Mutex
}

func NewDomainIndex() *DomainIndex {
	di := &DomainIndex{}
	di.treeStore.Store(NewDomainTree())
	return di
}

func normalizeDomain(domain string) string {
	d := strings.ToLower(strings.TrimSpace(domain))
	d = strings.TrimPrefix(d, "*.")
	d = strings.TrimPrefix(d, "*")
	return d
}

func (di *DomainIndex) Lookup(domain string) []int {
	d := strings.ToLower(strings.TrimSpace(domain))
	return di.treeStore.Load().(*DomainTree).Lookup(d)
}

func (di *DomainIndex) Add(domain string, id int) {
	d := normalizeDomain(domain)
	di.mu.Lock()
	defer di.mu.Unlock()

	old := di.treeStore.Load().(*DomainTree)
	if old.Has(d, id) {
		return
	}

	newTree := old.CloneAdd(d, id)
	di.treeStore.Store(newTree)
}

func (di *DomainIndex) Remove(domain string, id int) {
	d := normalizeDomain(domain)
	di.mu.Lock()
	defer di.mu.Unlock()

	old := di.treeStore.Load().(*DomainTree)
	if !old.Has(d, id) {
		return
	}

	newTree := old.CloneRemove(d, id)
	di.treeStore.Store(newTree)
}

func (di *DomainIndex) Destroy() {
	di.mu.Lock()
	defer di.mu.Unlock()
	di.treeStore.Store(NewDomainTree())
}
