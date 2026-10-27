package state

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
)

// graphIndex answers the questions about the edges of the graph without a pass
// over every record.
type graphIndex struct {
	// addrs lists the address of every node, sorted.
	addrs []string
	// dependents maps an address to the addresses of the nodes that list it in
	// their dependencies, sorted, each once.
	dependents map[string][]string
}

// indexLocked returns the index of the nodes, built if a change dropped it. The
// caller holds mu.
func (s *Store) indexLocked() *graphIndex {
	if s.index != nil {
		return s.index
	}
	idx := &graphIndex{addrs: make([]string, 0, len(s.nodes)), dependents: map[string][]string{}}
	for addr := range s.nodes {
		idx.addrs = append(idx.addrs, addr)
	}
	sort.Strings(idx.addrs)
	for _, addr := range idx.addrs {
		for _, d := range s.nodes[addr].Dependencies {
			// The lists fill in address order, so a repeated dependency of one node
			// is the last entry of the list it repeats in.
			if list := idx.dependents[d]; len(list) == 0 || list[len(list)-1] != addr {
				idx.dependents[d] = append(list, addr)
			}
		}
	}
	s.index = idx
	return idx
}

// graph returns the index of the nodes.
func (s *Store) graph() *graphIndex {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.indexLocked()
}

// Dependents returns the addresses of nodes that directly depend on addr.
func (s *Store) Dependents(addr string) []string {
	return append([]string{}, s.graph().dependents[addr]...)
}

// LockDependents takes the lock of every record that directly depends on addr,
// all or none, and checks that each is in the backend as this store read it. It
// is for a command that rewrites the dependencies of those records, which is a
// write to each of them: a record that another process holds the lock of, or has
// written since this store read it, would be overwritten with an older copy. It
// returns the addresses of the records and the function that releases the locks.
func (s *Store) LockDependents(addr, command, txn string) (deps []string, release func(), err error) {
	deps = s.Dependents(addr)
	var held []string
	release = func() {
		for _, a := range held {
			_ = s.Unlock(a)
		}
	}
	for _, d := range deps {
		if err := s.Lock(d, command, txn); err != nil {
			release()
			return nil, nil, fmt.Errorf("%s depends on %s: %w", d, addr, err)
		}
		held = append(held, d)
	}
	versions, err := s.Versions(deps)
	if err != nil {
		release()
		return nil, nil, err
	}
	for _, d := range deps {
		s.mu.Lock()
		n := s.nodes[d]
		s.mu.Unlock()
		if n == nil || versions[d] != (Version{Revision: n.Revision, Generation: n.Generation}) {
			release()
			return nil, nil, fmt.Errorf("%s depends on %s and changed in the state since it was read; run the command again", d, addr)
		}
	}
	return deps, release, nil
}

// Reach returns every node that transitively depends on addr: the blast
// radius of a change to it. The result is sorted and excludes addr.
func (s *Store) Reach(addr string) []string {
	return s.ReachOf([]string{addr})
}

// ReachOf returns every node that transitively depends on one of the addresses,
// sorted, in one walk of the edges that visits each node once. A node is in the
// result when an edge leads to it, so an address is in it only when another
// address, or a cycle, leads back to it.
func (s *Store) ReachOf(addrs []string) []string {
	idx := s.graph()
	seen := map[string]bool{}
	queue := slices.Clone(addrs)
	for len(queue) > 0 {
		a := queue[0]
		queue = queue[1:]
		for _, d := range idx.dependents[a] {
			if !seen[d] {
				seen[d] = true
				queue = append(queue, d)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for a := range seen {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// Edge is one dependency edge of the graph.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Edges lists every dependency edge (from depends on to).
func (s *Store) Edges() []Edge {
	out := []Edge{}
	for _, r := range s.List() {
		for _, d := range r.Dependencies {
			out = append(out, Edge{From: r.Address(), To: d})
		}
	}
	return out
}

// Snapshot is a whole-graph view for queries and export.
type Snapshot struct {
	Format    int                        `json:"format"`
	Lineage   string                     `json:"lineage"`
	Resources map[string]*Resource       `json:"resources"`
	Edges     []Edge                     `json:"edges"`
	Outputs   map[string]json.RawMessage `json:"outputs"`
}

// Snapshot returns the whole graph as one document.
func (s *Store) Snapshot() *Snapshot {
	snap := &Snapshot{Format: s.Format(), Lineage: s.Lineage(), Resources: map[string]*Resource{}, Edges: s.Edges(), Outputs: s.Outputs()}
	for _, r := range s.List() {
		snap.Resources[r.Address()] = r
	}
	if snap.Edges == nil {
		snap.Edges = []Edge{}
	}
	return snap
}
