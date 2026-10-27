package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"sort"
	"strings"
	"time"
)

// Lock describes a held resource lock.
type Lock struct {
	Addr    string    `json:"addr"`
	PID     int       `json:"pid"`
	Host    string    `json:"host"`
	User    string    `json:"user,omitempty"`
	Command string    `json:"command"`
	Txn     string    `json:"txn,omitempty"`
	Nonce   string    `json:"nonce,omitempty"`
	Actor   string    `json:"actor,omitempty"`
	Since   time.Time `json:"since"`
	// Kind is "lock" (held by a running command) or "reservation" (taken by
	// a plan for a later apply by the same actor). Expires applies to
	// reservations.
	Kind    string     `json:"kind,omitempty"`
	Expires *time.Time `json:"expires,omitempty"`
	// RenewedAt is bumped by the holder's heartbeat; a lock not renewed for
	// StaleAfter may be reclaimed.
	RenewedAt time.Time `json:"renewed_at"`
	// Stale is set by Locks on a lock that may be reclaimed. It is not stored.
	Stale bool `json:"stale,omitempty"`
}

// Holder is the name a lock or reservation is shown and matched under: the
// actor label it was taken with, or the operating system user when there was
// none.
func (l Lock) Holder() string {
	if l.Actor != "" {
		return l.Actor
	}
	return l.User
}

// ErrLocked is returned when another process holds a resource lock.
type ErrLocked struct {
	Lock Lock
}

func (e *ErrLocked) Error() string {
	l := e.Lock
	who := l.Holder()
	if l.Kind == "reservation" {
		until := ""
		if l.Expires != nil {
			until = " until " + l.Expires.Local().Format(time.RFC3339)
		}
		return fmt.Sprintf("%s is reserved by %s (%s%s, since %s)", l.Addr, who, l.Command, until, l.Since.Local().Format(time.RFC3339))
	}
	return fmt.Sprintf("%s is locked by %s (pid %d on %s, %s, since %s)", l.Addr, l.Command, l.PID, l.Host, who, l.Since.Local().Format(time.RFC3339))
}

// osUser returns the name of the operating system user, or "" when it cannot
// be found.
func osUser() string {
	if cu, err := user.Current(); err == nil {
		return cu.Username
	}
	return ""
}

// Owner is the name under which this store's commands hold reservations: the
// actor label, or the operating system user when no label was given. Without
// it, the reservation of an unlabelled run would belong to no one that could
// match it again. Labels are not verified, so a label that equals a user name
// names the same owner as that user's unlabelled runs.
func (s *Store) Owner() string {
	if s.actor != "" {
		return s.actor
	}
	return osUser()
}

func (s *Store) newLock(addr, command, txn string) Lock {
	host, _ := os.Hostname()
	now := time.Now().UTC()
	return Lock{Addr: addr, PID: os.Getpid(), Host: host, User: osUser(), Command: command, Txn: txn, Nonce: s.nonce, Actor: s.actor, Since: now, RenewedAt: now, Kind: "lock"}
}

// stale reports whether a lock may be reclaimed: its process is gone on
// this host, or it has not been renewed for StaleAfter.
func (s *Store) stale(l Lock) bool {
	if l.Kind == "reservation" {
		// A reservation outlives the process that took it; only time ends it.
		return l.Expires == nil || time.Now().After(*l.Expires)
	}
	if s.b.Local() {
		host, _ := os.Hostname()
		if l.Host == host && l.PID != os.Getpid() && !processAlive(l.PID) {
			return true
		}
	}
	last := l.RenewedAt
	if last.IsZero() {
		last = l.Since
	}
	return !last.IsZero() && time.Since(last) > StaleAfter
}

// Lock takes the lock for addr on behalf of this process, reclaiming a
// stale one and taking over the reservation that this store's owner holds.
func (s *Store) Lock(addr, command, txn string) error {
	_, err := s.lock(addr, command, txn, false)
	return err
}

// deleteLock deletes the lock at addr when it is still the object that was read
// as raw, and reports whether it did. A lock that was judged gone and has been
// taken again since is another object and is left alone: of two processes that
// judge the same lock gone, one deletes it and the other deletes nothing.
func (s *Store) deleteLock(addr string, raw []byte) bool {
	ok, _ := s.b.DeleteIf(s.cx(), lockKey(addr), func(current []byte) bool { return bytes.Equal(current, raw) })
	return ok
}

// lock takes the lock for addr and reports whether it created the lock
// object. With keep, the live reservation of this store's owner is left as
// it is and counts as held without being taken over; a lock this process
// already holds counts as held in either case.
func (s *Store) lock(addr, command, txn string, keep bool) (bool, error) {
	l := s.newLock(addr, command, txn)
	data, _ := indentJSON(l)
	for range 3 {
		created, err := s.b.PutIfAbsent(s.cx(), lockKey(addr), data)
		// A write of unknown outcome is not acquired, as a refusal is not, and the
		// lock is read: if the store holds the bytes written, this call took it.
		unknown := errors.Is(err, ErrUnknownOutcome)
		if err != nil && !unknown {
			return false, err
		}
		if created {
			return true, nil
		}
		raw, holder, rerr := s.readHolder(addr)
		if rerr != nil {
			if errors.Is(rerr, ErrNotFound) {
				continue // released between the two calls; try again
			}
			// Unreadable lock: treat as stale.
			s.deleteLock(addr, raw)
			continue
		}
		if holder.Nonce == s.nonce {
			// The nonce is this store's, and so is a lock that holds the bytes of
			// this write: it was created by this call, which must release it. The
			// bytes name this call, not this round: a create of unknown outcome that
			// was made is found by a later round, whose own create was refused.
			return bytes.Equal(raw, data), nil
		}
		stale := s.stale(holder)
		if keep && !stale && s.OwnsReservation(holder) {
			return false, nil
		}
		if stale || s.OwnsReservation(holder) {
			s.deleteLock(addr, raw)
			continue
		}
		return false, &ErrLocked{Lock: holder}
	}
	holder, err := s.readLock(addr)
	if err != nil {
		return false, fmt.Errorf("locking %s: %w", addr, err)
	}
	return false, &ErrLocked{Lock: holder}
}

// Conflict returns the error that Lock returns for addr, or nil when Lock
// would take the lock, and takes nothing.
func (s *Store) Conflict(addr string) error {
	holder, err := s.readLock(addr)
	if err != nil || holder.Nonce == s.nonce || s.stale(holder) || s.OwnsReservation(holder) {
		return nil // free, unreadable (which Lock reclaims), ours or reclaimable
	}
	return &ErrLocked{Lock: holder}
}

// TxnRunning reports whether the transaction with the given id has been
// written with the status running: its command holds all its locks and has begun
// to apply. A lock whose transaction is not running belongs to a command that is
// still taking its locks, or to a short write of a record, or to a command that
// is gone.
func (s *Store) TxnRunning(id string) bool {
	if id == "" {
		return false
	}
	data, err := s.b.Get(s.cx(), "txn/"+id+".json")
	if err != nil {
		return false
	}
	var t struct {
		Status string `json:"status"`
	}
	return json.Unmarshal(data, &t) == nil && t.Status == "running"
}

// TryHold takes the lock for addr for the short write of a record and
// reports whether the caller may write. Unlike TryLock it does not end the
// reservation that this store's owner holds on addr: the reservation stays as
// it is, and the returned release function leaves it alone. A lock that
// another process holds, which means it is applying the node, is not taken
// and ok is false.
func (s *Store) TryHold(addr, command, txn string) (release func(), ok bool, err error) {
	created, err := s.lock(addr, command, txn, true)
	var locked *ErrLocked
	if errors.As(err, &locked) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !created {
		return func() {}, true, nil
	}
	return func() { _ = s.Unlock(addr) }, true, nil
}

// OwnsReservation reports whether l is a reservation of this store's owner,
// which the owner's own apply may take over and own plan may renew.
func (s *Store) OwnsReservation(l Lock) bool {
	owner := s.Owner()
	return l.Kind == "reservation" && owner != "" && l.Holder() == owner
}

// ReserveAll takes a reservation on every address until the given time, or
// on none: a declaration of intent that keeps other actors from planning or
// applying changes to the nodes, without a process having to stay alive. The
// owner's later apply takes a reservation over; it lapses by itself at
// expiry.
//
// A reservation the owner already holds is rewritten in place, so that the
// node is never without one, and it ends at the later of its own expiry and
// until: a reservation can be extended and not shortened, and the command
// recorded on it is the latest one. If any address cannot be reserved, the
// addresses done so far are restored to what the owner held on them before,
// a reservation or nothing, and the error is returned. On success the result
// maps each address to the end of its reservation.
func (s *Store) ReserveAll(addrs []string, command string, until time.Time) (map[string]time.Time, error) {
	type taken struct {
		addr string
		prev *Lock
	}
	var done []taken
	ends := map[string]time.Time{}
	for _, a := range addrs {
		end, prev, err := s.reserve(a, command, until)
		if err != nil {
			for _, d := range done {
				s.undoReserve(d.addr, d.prev)
			}
			return nil, err
		}
		done = append(done, taken{a, prev})
		ends[a] = end
	}
	return ends, nil
}

// reserve reserves one address. It returns the end of the reservation and the
// reservation of the owner that it replaced, if any.
func (s *Store) reserve(addr, command string, until time.Time) (time.Time, *Lock, error) {
	// maybe lists the writes of this call that the store may or may not have made,
	// from any round: the bytes written, the end of the reservation, and the
	// reservation it replaced, nil for a create. A round that finds one of them in
	// the store took the reservation, and the rounds after an unknown outcome and a
	// failed read must not take it for the reservation of an earlier command.
	type possible struct {
		data []byte
		end  time.Time
		prev *Lock
	}
	var maybe []possible
	for range 4 {
		l := s.newLock(addr, command, "")
		l.Kind = "reservation"
		l.Expires = &until
		data, _ := indentJSON(l)
		created, err := s.b.PutIfAbsent(s.cx(), lockKey(addr), data)
		unknown := errors.Is(err, ErrUnknownOutcome)
		if err != nil && !unknown {
			return time.Time{}, nil, err
		}
		if created {
			return until, nil, nil
		}
		if unknown {
			maybe = append(maybe, possible{data, until, nil})
		}
		raw, holder, rerr := s.readHolder(addr)
		if rerr != nil {
			if errors.Is(rerr, ErrNotFound) {
				continue
			}
			s.deleteLock(addr, raw)
			continue
		}
		for _, m := range maybe {
			if bytes.Equal(raw, m.data) {
				return m.end, m.prev, nil // the create or the replacement was made
			}
		}
		stale := s.stale(holder)
		if !stale && s.OwnsReservation(holder) {
			end := until
			if holder.Expires != nil && holder.Expires.After(until) {
				end = *holder.Expires
			}
			l.Expires = &end
			if !holder.Since.IsZero() {
				l.Since = holder.Since
			}
			data, _ = indentJSON(l)
			// The reservation is replaced only if it is still the one that was read.
			ok, err := s.b.ReplaceIf(s.cx(), lockKey(addr), func(current []byte) ([]byte, bool) { return data, bytes.Equal(current, raw) })
			switch {
			case errors.Is(err, ErrNotFound):
				continue
			case errors.Is(err, ErrUnknownOutcome):
				// Read again, as after a refusal; the lock is this call's if it holds
				// these bytes, and then the reservation that it replaced is this one.
				maybe = append(maybe, possible{data, end, &holder})
				continue
			case err != nil:
				return time.Time{}, nil, err
			case !ok:
				continue
			}
			return end, &holder, nil
		}
		if holder.Nonce == s.nonce || stale || s.OwnsReservation(holder) {
			s.deleteLock(addr, raw)
			continue
		}
		return time.Time{}, nil, &ErrLocked{Lock: holder}
	}
	holder, err := s.readLock(addr)
	if err != nil {
		return time.Time{}, nil, fmt.Errorf("reserving %s: %w", addr, err)
	}
	return time.Time{}, nil, &ErrLocked{Lock: holder}
}

// undoReserve puts addr back as it was before reserve: the reservation prev
// that was replaced, or no lock when there was none. It leaves alone a lock that
// is no longer this store's.
func (s *Store) undoReserve(addr string, prev *Lock) {
	if prev == nil {
		_, _ = s.Release(addr, false)
		return
	}
	data, _ := indentJSON(prev)
	_, _ = s.b.ReplaceIf(s.cx(), lockKey(addr), func(current []byte) ([]byte, bool) {
		var cur Lock
		return data, json.Unmarshal(current, &cur) == nil && cur.Nonce == s.nonce
	})
}

// Release removes a reservation held by this store's owner (or any lock, when
// force is set).
func (s *Store) Release(addr string, force bool) (bool, error) {
	raw, l, err := s.readLockRaw(addr)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if force {
		return true, s.b.Delete(s.cx(), lockKey(addr))
	}
	if !s.OwnsReservation(l) && l.Nonce != s.nonce {
		return false, &ErrLocked{Lock: l}
	}
	ok, err := s.b.DeleteIf(s.cx(), lockKey(addr), func(current []byte) bool { return bytes.Equal(current, raw) })
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return ok, err
}

// readHolder reads the lock at addr that another process holds, with the bytes it
// was read from. A backend that makes a lock exist before its contents are all
// there leaves a lock that cannot be read for a moment, so a lock that cannot be
// read is read again a few times before it is taken for a broken one. A lock is
// created whole by the local backend and by S3, and this is for a filesystem with
// no hard links.
func (s *Store) readHolder(addr string) ([]byte, Lock, error) {
	var (
		raw []byte
		l   Lock
		err error
	)
	for i := 1; i <= 5; i++ {
		if raw, l, err = s.readLockRaw(addr); err == nil || errors.Is(err, ErrNotFound) {
			break
		}
		time.Sleep(time.Duration(i) * time.Millisecond)
	}
	return raw, l, err
}

func (s *Store) readLock(addr string) (Lock, error) {
	_, l, err := s.readLockRaw(addr)
	return l, err
}

// readLockRaw reads the lock at addr and returns the bytes as well. The bytes
// are returned when the lock cannot be parsed.
func (s *Store) readLockRaw(addr string) ([]byte, Lock, error) {
	var l Lock
	data, err := s.b.Get(s.cx(), lockKey(addr))
	if err != nil {
		return nil, l, err
	}
	err = json.Unmarshal(data, &l)
	if l.Addr == "" {
		l.Addr = addr
	}
	return data, l, err
}

// Renew refreshes a lock this process holds (the heartbeat). The lock is written
// only if it is still this process's at the moment it is replaced, so a lock that
// another process reclaimed in the meantime is not written over.
func (s *Store) Renew(addr string) error {
	var (
		other  Lock
		broken error
	)
	ok, err := s.b.ReplaceIf(s.cx(), lockKey(addr), func(current []byte) ([]byte, bool) {
		var l Lock
		if err := json.Unmarshal(current, &l); err != nil {
			broken = err
			return nil, false
		}
		if l.Nonce != s.nonce {
			other = l
			return nil, false
		}
		l.RenewedAt = time.Now().UTC()
		data, _ := indentJSON(l)
		return data, true
	})
	switch {
	case err != nil:
		return err
	case ok:
		return nil
	case broken != nil:
		return fmt.Errorf("the lock on %s cannot be read: %w", addr, broken)
	}
	if other.Addr == "" {
		other.Addr = addr
	}
	return &ErrLocked{Lock: other}
}

// Unlock releases the lock for addr if this process holds it. A lock that
// another process holds, or that cannot be read, is left as it is.
func (s *Store) Unlock(addr string) error {
	var (
		other  Lock
		broken error
	)
	ok, err := s.b.DeleteIf(s.cx(), lockKey(addr), func(current []byte) bool {
		var l Lock
		if err := json.Unmarshal(current, &l); err != nil {
			broken = err
			return false
		}
		if l.Nonce != s.nonce {
			other = l
			return false
		}
		return true
	})
	switch {
	case errors.Is(err, ErrNotFound):
		return nil
	case err != nil:
		return err
	case ok:
		return nil
	case broken != nil:
		return fmt.Errorf("the lock on %s cannot be read: %w", addr, broken)
	case other.Nonce != "" || other.PID != 0:
		if other.Addr == "" {
			other.Addr = addr
		}
		return &ErrLocked{Lock: other}
	}
	return nil
}

// ForceUnlock releases the lock for addr whoever holds it.
func (s *Store) ForceUnlock(addr string) (bool, error) {
	_, err := s.readLock(addr)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return true, s.b.Delete(s.cx(), lockKey(addr))
}

// ReleaseStale releases every lock and reservation that may be reclaimed, and
// returns their addresses. A lock is deleted only if it is stale at the moment it
// is deleted: one that its holder renewed, or that another process took, since
// the listing is left alone.
func (s *Store) ReleaseStale() ([]string, error) {
	locks, err := s.Locks()
	if err != nil {
		return nil, err
	}
	var released []string
	for _, l := range locks {
		if !l.Stale {
			continue
		}
		ok, err := s.b.DeleteIf(s.cx(), lockKey(l.Addr), func(current []byte) bool {
			var cur Lock
			return json.Unmarshal(current, &cur) == nil && s.stale(cur)
		})
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return released, err
		}
		if ok {
			released = append(released, l.Addr)
		}
	}
	return released, nil
}

// TryLock takes the lock when it is free and reports whether it did.
func (s *Store) TryLock(addr, command, txn string) (bool, error) {
	err := s.Lock(addr, command, txn)
	var locked *ErrLocked
	if errors.As(err, &locked) {
		return false, nil
	}
	return err == nil, err
}

// Locks lists the locks currently held on the store. Stale is set on each lock
// that may be reclaimed: its process is gone on this host, it has not been
// renewed for StaleAfter, or it is a reservation that has expired.
func (s *Store) Locks() ([]Lock, error) {
	keys, err := s.b.List(s.cx(), "locks")
	if err != nil {
		return nil, err
	}
	out := []Lock{}
	for _, k := range keys {
		if !strings.HasSuffix(k, ".lock") {
			continue
		}
		data, err := s.b.Get(s.cx(), k)
		if err != nil {
			continue
		}
		var l Lock
		if json.Unmarshal(data, &l) == nil {
			l.Stale = s.stale(l)
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr < out[j].Addr })
	return out, nil
}

// Suspects returns, by address, the locks that may be reclaimed: the command that
// took each is gone, or has not renewed it for StaleAfter. A command that stops
// while it holds a lock may have changed the object, and the node's record says
// nothing about it. A reservation is not a suspect: it outlives its command by
// design. It lists the locks once, and reads the ones it finds; a store with no
// locks makes no read.
func (s *Store) Suspects() (map[string]Lock, error) {
	locks, err := s.Locks()
	if err != nil {
		return nil, err
	}
	out := map[string]Lock{}
	for _, l := range locks {
		if l.Stale && l.Kind != "reservation" {
			out[l.Addr] = l
		}
	}
	return out, nil
}

// Interrupted returns the ids of the transactions among txns that are recorded as
// running and whose command is gone: no lock that has not gone stale carries
// their id. A command that applies holds a lock for its transaction from before
// the transaction is written until after it is finished, so a running
// transaction without one was left by a command that was killed.
func (s *Store) Interrupted(txns []Transaction) (map[string]bool, error) {
	out := map[string]bool{}
	var running []string
	for _, t := range txns {
		if t.Status == "running" {
			running = append(running, t.ID)
		}
	}
	if len(running) == 0 {
		return out, nil
	}
	locks, err := s.Locks()
	if err != nil {
		return nil, err
	}
	live := map[string]bool{}
	for _, l := range locks {
		if !l.Stale && l.Txn != "" {
			live[l.Txn] = true
		}
	}
	for _, id := range running {
		if !live[id] {
			out[id] = true
		}
	}
	return out, nil
}
