package state

// A plan reads the state and writes nothing for what the specification declares: a
// record that $from moves, a record that $import brings in, a provider that $from
// renames. It does not write them, since the plan may be thrown away, but the nodes
// it plans have to see them: the record at the new address, none at the old one. A
// store holds them in memory, as staged, in place of the records of the backend:
// Get, List and the queries over the graph read them, and a write to an address that
// is staged changes the copy in memory and no more. DropStaged puts the records of
// the backend back, and the apply that carries the declaration out writes it.

// stagedNode is what an address held before it was staged.
type stagedNode struct {
	// orig is the record of the address that the store held, nil for an address that
	// has no record.
	orig *Resource
}

// Stage makes r the node at its address, in memory only. An address that has no
// record gets one that is at revision 0 and in no generation, as a record that is not
// written is, so that a plan that records the revisions it saw does not name one that
// no backend has; an address that has one keeps the revision and the generation of
// the copy it is given. A second call replaces the node and keeps what the address
// held at first.
func (s *Store) Stage(r *Resource) {
	addr := r.Address()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.staged == nil {
		s.staged = map[string]stagedNode{}
	}
	if _, ok := s.staged[addr]; !ok {
		s.staged[addr] = stagedNode{orig: s.nodes[addr]}
	}
	c := *r
	if s.staged[addr].orig == nil {
		c.Revision, c.Generation = 0, ""
	}
	s.nodes[addr] = &c
	s.index = nil
}

// StageRemove takes the node at addr away, in memory only. The record in the backend
// stays.
func (s *Store) StageRemove(addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.staged == nil {
		s.staged = map[string]stagedNode{}
	}
	if _, ok := s.staged[addr]; !ok {
		s.staged[addr] = stagedNode{orig: s.nodes[addr]}
	}
	delete(s.nodes, addr)
	s.index = nil
}

// Staged reports whether the node at addr is one that Stage or StageRemove changed.
func (s *Store) Staged(addr string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.staged[addr]
	return ok
}

// DropStaged gives every staged address the node that the backend has, or none.
func (s *Store) DropStaged() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for addr, st := range s.staged {
		if st.orig == nil {
			delete(s.nodes, addr)
		} else {
			s.nodes[addr] = st.orig
		}
	}
	s.staged = nil
	s.index = nil
}

// writeStaged is a write to an address that may be staged. When it is, the copy in
// memory takes the change and the backend is not touched. remove deletes the copy.
func (s *Store) writeStaged(r *Resource, addr string, remove bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.staged[addr]; !ok {
		return false
	}
	if remove {
		delete(s.nodes, addr)
	} else {
		c := *r
		if prev, ok := s.nodes[addr]; ok {
			c.Revision, c.Generation = prev.Revision, prev.Generation
		} else if st := s.staged[addr]; st.orig == nil {
			c.Revision, c.Generation = 0, ""
		}
		s.nodes[addr] = &c
	}
	s.index = nil
	return true
}
