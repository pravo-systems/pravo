package state

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
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

// loadNodes reads the nodes at the listed keys, from the snapshot where it is still
// true and from the backend otherwise, and writes a new snapshot when much of it was not.
func (s *Store) loadNodes(ctx context.Context, vl VersionLister, objs []ObjectVersion) error {
	var (
		snap map[string]snapshotEntry
		wg   sync.WaitGroup
	)
	wg.Add(1)
	go func() { defer wg.Done(); snap = s.loadSnapshot(ctx) }()
	wg.Wait()
	type res struct {
		r   *Resource
		h   nodeRef
		raw nodeBytes
		err error
	}
	results := make([]res, len(objs))
	width := readWidthLocal
	if !s.b.Local() {
		width = readWidthRemote
	}
	var (
		wg2    sync.WaitGroup
		next   atomic.Int64
		missed atomic.Int64
	)
	for range min(width, len(objs)) {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(objs) {
					return
				}
				var pre *nodeBytes
				if e, ok := snap[objs[i].Key]; ok && e.Tag == objs[i].Tag && objs[i].Tag != "" {
					pre = &nodeBytes{ptr: e.Ptr, rec: e.Rec}
				} else {
					missed.Add(1)
				}
				r, h, raw, err := s.readNodeRaw(ctx, objs[i].Key, pre)
				results[i] = res{r, h, raw, err}
			}
		}()
	}
	wg2.Wait()
	var entries []snapshotEntry
	for i, r := range results {
		if r.err != nil {
			return r.err
		}
		if r.r != nil {
			s.nodes[r.r.Address()] = r.r
			s.held[r.r.Address()] = r.h
			if r.raw.ptr != nil {
				entries = append(entries, snapshotEntry{Key: objs[i].Key, Tag: objs[i].Tag, Ptr: r.raw.ptr, Rec: r.raw.rec})
			}
		}
	}
	// Only a store that may write keeps the snapshot, and only when it is worth the upload.
	if len(entries) >= snapshotMin && (snap == nil || int(missed.Load())*snapshotRewriteBelow > len(entries)) && s.writable() == nil {
		if data, err := json.Marshal(snapshot{V: 1, Lineage: s.meta.Lineage, Nodes: entries}); err == nil {
			_ = s.b.Put(ctx, snapshotKey, data) // a hint: a store that refuses it is read as before
		}
	}
	return nil
}
