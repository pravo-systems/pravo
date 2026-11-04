package state

import (
	"encoding/json"
	"errors"
	"os"
	"time"
)

// PendingCreate is the note of a create that runs while the node still has a record: the
// replacement of a node that creates before it destroys. The lock cannot carry it, because a
// plan reclaims a stale lock of a node that has a record. The note is its own object, written
// before the provider is asked, deleted once the replacement is recorded or the create failed
// cleanly, and left where it is by a command that stops. It names the id of the object the
// record holds, so that a record with another id shows the replacement was recorded.
type PendingCreate struct {
	Addr  string `json:"addr"`
	OldID string `json:"old_id"`
	// OldAppliedAt is when the record was last applied. An id is not always unique to an
	// object (the id of a local_file is a hash of its content), so a replacement that has the
	// old id is told from the old object by this time, which the replacement's record renews.
	OldAppliedAt *time.Time `json:"old_applied_at,omitempty"`
	Txn          string     `json:"txn"`
	Host         string     `json:"host,omitempty"`
	PID          int        `json:"pid,omitempty"`
	Command      string     `json:"command,omitempty"`
	Since        time.Time  `json:"since"`
}

// Holds reports whether rec is still the record of the object that the note names: it has
// the id, and, when both know when the record was applied, the same time. In doubt it holds.
func (p *PendingCreate) Holds(rec *Resource) bool {
	if p.OldID == "" || rec.ID() != p.OldID {
		return false
	}
	return p.OldAppliedAt == nil || rec.AppliedAt == nil || p.OldAppliedAt.Equal(*rec.AppliedAt)
}

func pendingKey(addr string) string { return "locks/" + encodeAddr(addr) + ".replacing" }

// NotePendingCreate writes the note for addr, in place of any that is there.
func (s *Store) NotePendingCreate(addr, oldID string, oldAppliedAt *time.Time, txn, command string) error {
	p := PendingCreate{Addr: addr, OldID: oldID, OldAppliedAt: oldAppliedAt, Txn: txn, Command: command, Since: time.Now().UTC()}
	p.Host, _ = os.Hostname()
	p.PID = os.Getpid()
	data, err := indentJSON(p)
	if err != nil {
		return err
	}
	return s.b.Put(s.cx(), pendingKey(addr), data)
}

// ClearPendingCreate deletes the note for addr; a missing note is not an error.
func (s *Store) ClearPendingCreate(addr string) error {
	err := s.b.Delete(s.cx(), pendingKey(addr))
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// PendingCreateOf returns the note for addr, or nil.
func (s *Store) PendingCreateOf(addr string) (*PendingCreate, error) {
	data, err := s.b.Get(s.cx(), pendingKey(addr))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p PendingCreate
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ID is the provider id the record holds, or "" when it records none.
func (r *Resource) ID() string {
	var a struct {
		ID any `json:"id"`
	}
	if json.Unmarshal(r.Attributes, &a) != nil {
		return ""
	}
	s, _ := a.ID.(string)
	return s
}

// Orphaned reports whether the note belongs to a command that is gone and whose create the
// record rec (the record of the node now) does not account for: the record is absent or still
// holds the old object. A command that is running still holds a lock for the transaction.
func (s *Store) Orphaned(p *PendingCreate, rec *Resource) (bool, error) {
	if rec != nil && !p.Holds(rec) {
		return false, nil
	}
	locks, err := s.Locks()
	if err != nil {
		return false, err
	}
	for _, l := range locks {
		if !l.Stale && l.Txn == p.Txn {
			return false, nil
		}
	}
	return true, nil
}

// MovePendingCreate carries the note for from to the address to, where the record went. A
// node that has no note is left alone.
func (s *Store) MovePendingCreate(from, to string) error {
	p, err := s.PendingCreateOf(from)
	if err != nil || p == nil {
		return err
	}
	p.Addr = to
	data, err := indentJSON(p)
	if err != nil {
		return err
	}
	if err := s.b.Put(s.cx(), pendingKey(to), data); err != nil {
		return err
	}
	return s.ClearPendingCreate(from)
}
