package engine

import "testing"

// A run that a failure stops finishes the nodes it had not started without
// planning them. Finishing one releases its dependents, and the queue may
// already be closed by then: that must not send into it.
func TestReleaseSendsNothingIntoAClosedQueue(t *testing.T) {
	newPair := func() (*node, *node) {
		dep := &node{pending: 1}
		return &node{dependents: []*node{dep}}, dep
	}

	n, dep := newPair()
	queue := make(chan *node, 1)
	release(n, queue, false)
	if len(queue) != 1 || dep.pending != 0 {
		t.Fatalf("an open queue holds %d nodes and the dependent waits for %d, want 1 and 0", len(queue), dep.pending)
	}

	n, dep = newPair()
	closed := make(chan *node, 1)
	close(closed)
	release(n, closed, true) // sending into a closed channel would panic
	if dep.pending != 0 {
		t.Errorf("the dependent of a node that finished waits for %d, want 0", dep.pending)
	}

	n, dep = newPair()
	dep.pending = 2
	queue = make(chan *node, 1)
	release(n, queue, false)
	if len(queue) != 0 || dep.pending != 1 {
		t.Errorf("a dependent that still waits was queued: %d nodes, waiting for %d", len(queue), dep.pending)
	}
}
