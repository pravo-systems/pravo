package state

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// snapshotKey is the object that holds a copy of every node of the state, so that
// a command that opens the state reads one object and a listing, and not two
// objects for each node.
const snapshotKey = "snapshot.json"

// snapshotRewriteBelow is the share of the nodes, in one in this many, that may
// have changed since the snapshot was taken before a writer takes a new one.
const snapshotRewriteBelow = 20

// snapshotMin is the number of nodes below which a snapshot is not worth keeping.
const snapshotMin = 50

// snapshot is the content of snapshot.json. It is a hint and nothing in it is
// trusted: a node is taken from it only when the listing of the state gives the
// tag that the snapshot recorded for the node's pointer, and the pointer and the
// record are then verified as if they had been read, and read from the backend
// when they do not verify. A snapshot that is stale, torn or of another state
// costs reads and nothing else.
type snapshot struct {
	V       int             `json:"v"`
	Lineage string          `json:"lineage"`
	Nodes   []snapshotEntry `json:"nodes"`
}

type snapshotEntry struct {
	Key string `json:"key"`
	Tag string `json:"tag"`
	Ptr []byte `json:"ptr"`
	Rec []byte `json:"rec"`
}

// snapshotOn reports whether the store reads and keeps snapshots: for a backend that lists
// versions, unless the settings turn it off (snapshot: false).
func (s *Store) snapshotOn() (VersionLister, bool) {
	if s.meta.Format < FormatImmutable {
		return nil, false
	}
	return versionLister(s.b)
}

// loadSnapshot reads snapshot.json, or nothing when there is none or it is not of this state.
func (s *Store) loadSnapshot(ctx context.Context) map[string]snapshotEntry {
	data, err := s.b.Get(ctx, snapshotKey)
	if err != nil {
		return nil
	}
	var snap snapshot
	if json.Unmarshal(data, &snap) != nil || snap.V != 1 || snap.Lineage != s.meta.Lineage {
		return nil
	}
	out := make(map[string]snapshotEntry, len(snap.Nodes))
	for _, e := range snap.Nodes {
		out[e.Key] = e
	}
	return out
}

// snapshotState is what a store remembers of the snapshot between its open and its close.
type snapshotState struct {
	// entries are the nodes of the state as the open found them, with the tag the listing gave.
	entries []snapshotEntry
	// missed is how many nodes the open did not take from the snapshot.
	missed int
	// written counts the nodes that this process has written or removed since the open.
	written atomic.Int64
	// kept is set when this process has written the snapshot, so that Close does not do it twice.
	kept bool
	// on is set by an open that could use snapshots.
	on bool
}

// readListed reads the nodes at the listed keys, from snap where it is still true and from the
// backend otherwise. The results are in the order of objs.
func (s *Store) readListed(ctx context.Context, objs []ObjectVersion, snap map[string]snapshotEntry) (results []listed, missed int, err error) {
	results = make([]listed, len(objs))
	width := readWidthLocal
	if !s.b.Local() {
		width = readWidthRemote
	}
	var (
		wg   sync.WaitGroup
		next atomic.Int64
		miss atomic.Int64
	)
	for range min(width, len(objs)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(objs) {
					return
				}
				var pre *nodeBytes
				if e, ok := snap[objs[i].Key]; ok && e.Tag == objs[i].Tag && objs[i].Tag != "" {
					pre = &nodeBytes{ptr: e.Ptr, rec: e.Rec}
				} else {
					miss.Add(1)
				}
				r, h, raw, err := s.readNodeRaw(ctx, objs[i].Key, pre)
				results[i] = listed{r, h, raw, err}
			}
		}()
	}
	wg.Wait()
	for _, r := range results {
		if r.err != nil {
			return nil, 0, r.err
		}
	}
	return results, int(miss.Load()), nil
}

type listed struct {
	r   *Resource
	h   nodeRef
	raw nodeBytes
	err error
}

func entriesOf(objs []ObjectVersion, results []listed) []snapshotEntry {
	var entries []snapshotEntry
	for i, r := range results {
		if r.r != nil && r.raw.ptr != nil {
			entries = append(entries, snapshotEntry{Key: objs[i].Key, Tag: objs[i].Tag, Ptr: r.raw.ptr, Rec: r.raw.rec})
		}
	}
	return entries
}

// putSnapshot uploads a snapshot of entries. A store that refuses it is read as before.
func (s *Store) putSnapshot(ctx context.Context, entries []snapshotEntry) bool {
	data, err := json.Marshal(snapshot{V: 1, Lineage: s.meta.Lineage, Nodes: entries})
	if err != nil {
		return false
	}
	return s.b.Put(ctx, snapshotKey, data) == nil
}

// loadNodes reads the nodes at the listed keys, from the snapshot where it is still true and
// from the backend otherwise. It writes a snapshot when there is none: after that the
// command that changes the state keeps it up to date (see settleSnapshot).
func (s *Store) loadNodes(ctx context.Context, vl VersionLister, objs []ObjectVersion) error {
	snap := s.loadSnapshot(ctx)
	results, missed, err := s.readListed(ctx, objs, snap)
	if err != nil {
		return err
	}
	for _, r := range results {
		if r.r != nil {
			s.nodes[r.r.Address()] = r.r
			s.held[r.r.Address()] = r.h
		}
	}
	entries := entriesOf(objs, results)
	s.snap.on, s.snap.entries, s.snap.missed = true, entries, missed
	if snap == nil && len(entries) >= snapshotMin && s.writable() == nil {
		if s.snap.kept = s.putSnapshot(ctx, entries); s.snap.kept {
			s.snap.missed = 0
		}
	}
	return nil
}

// settleSnapshot is the end of a command that may have changed the state: when what it wrote
// and what the open had to read afresh are more than one node in snapshotRewriteBelow, it
// lists the nodes again, reads only those that are not what the snapshot recorded, and
// uploads a snapshot that is true again. It never fails the command.
func (s *Store) settleSnapshot() {
	s.mu.Lock()
	n, closed := len(s.nodes), s.closed
	s.mu.Unlock()
	if !s.snap.on || closed || n < snapshotMin || s.writable() != nil {
		return
	}
	if (s.snap.missed+int(s.snap.written.Load()))*snapshotRewriteBelow <= n {
		return
	}
	vl, ok := s.snapshotOn()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(s.cx(), settleTimeout)
	defer cancel()
	objs, err := vl.ListVersions(ctx, "resources")
	if err != nil {
		return
	}
	objs = slices.DeleteFunc(objs, func(o ObjectVersion) bool { return !strings.HasSuffix(o.Key, ".json") })
	old := make(map[string]snapshotEntry, len(s.snap.entries))
	for _, e := range s.snap.entries {
		old[e.Key] = e
	}
	results, _, err := s.readListed(ctx, objs, old)
	if err != nil {
		return
	}
	if entries := entriesOf(objs, results); len(entries) >= snapshotMin && s.putSnapshot(ctx, entries) {
		s.snap.kept, s.snap.missed, s.snap.entries = true, 0, entries
		s.snap.written.Store(0)
	}
}

// settleTimeout bounds the work that settleSnapshot does when a command ends.
const settleTimeout = 2 * time.Minute
