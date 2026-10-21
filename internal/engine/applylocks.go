package engine

import (
	"errors"
	"fmt"
	mrand "math/rand/v2"
	"sync"
	"time"

	"pravo.dev/pravo/internal/state"
)

// lockWidth is how many lock objects an apply takes, renews or releases at the
// same time.
const lockWidth = 16

// The patience of an apply that finds a lock held. The command that holds the
// lock may be taking its own locks, which it gives up again if it meets another
// apply, and not yet applying. The apply looks at the lock for several times the
// time that one try to take all its locks took, and not less than minPatience or
// more than maxPatience, before it is rejected. A lock held by a command that
// has begun to apply, or a reservation, rejects the apply at once.
var (
	minPatience = 25 * time.Millisecond
	maxPatience = 3 * time.Second
)

// patienceTries is how many tries the patience is.
const patienceTries = 10

// patience is how long an apply looks at a lock that another command holds, when
// a try to take its locks took the given time.
func patience(try time.Duration) time.Duration {
	return min(max(patienceTries*try, minPatience), maxPatience)
}

// inParallel calls fn(0) to fn(n-1), at most width at a time, starting them in
// order, and returns when all have returned.
func inParallel(n, width int, fn func(i int)) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, width)
	for i := 0; i < n; i++ {
		sem <- struct{}{}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			fn(i)
		}(i)
	}
	wg.Wait()
}

// heldLocks is the set of locks an apply holds on the nodes it writes. It
// renews them while the apply runs and releases them when it ends.
type heldLocks struct {
	st       *state.Store
	progress func(format string, args ...any)
	// interval is the time between two renewals.
	interval time.Duration

	mu sync.Mutex
	// addrs lists the locks taken so far.
	addrs []string
	// beat is the last time that every lock taken was known to be this apply's:
	// when the locks were taken, or when a renewal of all of them succeeded.
	beat time.Time
	// lost is set when a renewal found that a lock is no longer this apply's.
	lost error

	// renewMu orders the renewals: the heartbeat and the check before a change
	// do not renew at the same time.
	renewMu sync.Mutex
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
}

// newHeldLocks returns a set of locks that renews what it holds every interval
// until it is released.
func newHeldLocks(st *state.Store, progress func(string, ...any), interval time.Duration) *heldLocks {
	h := &heldLocks{st: st, progress: progress, interval: interval, beat: time.Now(), stop: make(chan struct{}), done: make(chan struct{})}
	go h.heartbeat()
	return h
}

// heartbeat renews the locks so that other hosts can tell a running apply from
// one that crashed.
func (h *heldLocks) heartbeat() {
	defer close(h.done)
	t := time.NewTicker(h.interval)
	defer t.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-t.C:
			h.renew()
		}
	}
}

// held returns a copy of the addresses of the locks taken.
func (h *heldLocks) held() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.addrs...)
}

func (h *heldLocks) lastBeat() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.beat
}

// renew renews every lock taken so far. A lock that is no longer this apply's
// is recorded as lost.
func (h *heldLocks) renew() {
	h.renewMu.Lock()
	defer h.renewMu.Unlock()
	addrs := h.held()
	errs := make([]error, len(addrs))
	inParallel(len(addrs), lockWidth, func(i int) { errs[i] = h.st.Renew(addrs[i]) })
	failed := false
	for i, err := range errs {
		if err == nil {
			continue
		}
		failed = true
		h.progress("%s: lock renewal failed: %v", addrs[i], err)
		var locked *state.ErrLocked
		if errors.Is(err, state.ErrNotFound) || errors.As(err, &locked) {
			h.mu.Lock()
			if h.lost == nil {
				h.lost = WithCode(ExitLocked, fmt.Errorf("lock lost: this apply does not hold the lock on %s any more (%w)", addrs[i], err))
			}
			h.mu.Unlock()
		}
	}
	if !failed {
		h.mu.Lock()
		h.beat = time.Now()
		h.mu.Unlock()
	}
}

// drop releases the locks taken so far and keeps the heartbeat. A renewal does
// not run while the locks are released, or it would find them gone.
func (h *heldLocks) drop() {
	h.renewMu.Lock()
	defer h.renewMu.Unlock()
	addrs := h.held()
	inParallel(len(addrs), lockWidth, func(i int) { _ = h.st.Unlock(addrs[i]) })
	h.mu.Lock()
	h.addrs = nil
	h.mu.Unlock()
}

// attempt takes the locks on addrs, which are sorted, once, and returns what it
// found. The addresses in contested, which an earlier try found held, are taken
// first, one at a time and in order, so that applies that meet at the same nodes
// meet at the first of them. The others are taken in order, the first alone when
// nothing is contested, so that an apply whose first lock is held tries no other,
// and then lockWidth at a time, each group after the one before it. The try stops
// at the first lock or group in which another process holds a lock; the locks
// taken stay taken. held is the error of the smallest address that was held, and
// all the addresses that were held are added to contested. A failure other than a
// lock that is held is returned as err. rtt is how long the first lock took.
func (h *heldLocks) attempt(addrs []string, contested map[string]bool, command, txn string) (held *state.ErrLocked, rtt time.Duration, err error) {
	errs := make(map[string]error)
	take := func(addr string) {
		e := h.st.Lock(addr, command, txn)
		if e == nil {
			h.mu.Lock()
			h.addrs = append(h.addrs, addr)
			h.mu.Unlock()
			return
		}
		h.mu.Lock()
		errs[addr] = e
		h.mu.Unlock()
	}
	var first, rest []string
	for _, a := range addrs {
		if contested[a] {
			first = append(first, a)
		} else {
			rest = append(rest, a)
		}
	}
	for _, a := range first {
		take(a)
		if len(errs) > 0 {
			break
		}
	}
	if len(errs) == 0 {
		for tried := 0; tried < len(rest); {
			from, to := tried, min(tried+lockWidth, len(rest))
			if from == 0 && len(first) == 0 {
				to = 1
			}
			begun := time.Now()
			inParallel(to-from, lockWidth, func(i int) { take(rest[from+i]) })
			if from == 0 && len(first) == 0 {
				rtt = time.Since(begun)
			}
			tried = to
			if len(errs) > 0 {
				break
			}
		}
	}
	for _, a := range addrs {
		e := errs[a]
		if e == nil {
			continue
		}
		var locked *state.ErrLocked
		if !errors.As(e, &locked) {
			return nil, rtt, e
		}
		contested[a] = true
		if held == nil {
			held = locked
		}
	}
	return held, rtt, nil
}

// acquire takes the locks on addrs, which are sorted, or none of them. An apply
// never holds some of its locks while it waits for the others, so two applies
// cannot wait for each other. When a lock is held by another process the ones
// taken are released at once, and if the command that holds it has not begun to
// apply, which may mean that it is taking locks of its own and will give them up,
// the apply looks at the lock for a short time, waits a random time and tries
// again, now taking the nodes that were held first and in order. Of applies that
// start together and overlap, one is granted and the others are turned away
// because of it, and an apply is not turned away because of another that was
// turned away. A lock held by a command that applies, by a reservation, or for
// longer than the patience of the apply rejects it. If any lock is held by
// another process, the error names the smallest address that is held and counts
// the others, which are looked at and not taken.
func (h *heldLocks) acquire(addrs []string, command, txn string) error {
	var (
		contested = map[string]bool{}
		deadline  time.Time
		lock      time.Duration // how long taking one lock took
	)
	for round := 0; ; round++ {
		begun := time.Now()
		held, rtt, err := h.attempt(addrs, contested, command, txn)
		if err != nil {
			h.drop()
			return err
		}
		if held == nil {
			h.mu.Lock()
			h.beat = time.Now()
			h.mu.Unlock()
			return nil
		}
		h.drop()
		lock = max(lock, rtt, 100*time.Microsecond)
		if deadline.IsZero() {
			try := max(time.Since(begun), lock*time.Duration(1+(len(addrs)+lockWidth-1)/lockWidth))
			deadline = time.Now().Add(patience(try))
		}
		if !h.waits(held.Lock) || !time.Now().Before(deadline) {
			return h.rejected(addrs, held)
		}
		// Look at the lock until it is free. The lock may have been taken by an
		// apply that has taken more than this one.
		last := held
		for pause := lock; ; pause = min(pause*3/2, 100*time.Millisecond) {
			time.Sleep(pause/2 + mrand.N(pause))
			err := h.st.Conflict(last.Lock.Addr)
			if err == nil {
				break
			}
			var locked *state.ErrLocked
			if !errors.As(err, &locked) {
				return err
			}
			last = locked
			if !h.waits(last.Lock) || !time.Now().Before(deadline) {
				return h.rejected(addrs, last)
			}
		}
		// Another apply that was turned away by the locks released here starts
		// again at about the same moment. A pause of random length, longer after
		// each try, keeps the two from meeting again every time.
		time.Sleep(mrand.N(lock << min(round+1, 6)))
	}
}

// waits reports whether an apply that finds the lock l held may look at it for a
// while: it is held by a command that has not begun to apply.
func (h *heldLocks) waits(l state.Lock) bool {
	return l.Kind != "reservation" && !h.st.TxnRunning(l.Txn)
}

// scanTime is how long the error of a rejected apply looks at the locks it
// needed to count the ones that are held. A rejection does not wait for a
// thousand reads.
var scanTime = 2 * time.Second

// rejected is the error of an apply that is turned away. It looks at the locks
// the apply needed, in order, so that the error names the smallest that is held
// and counts the others; when none is held any longer it names held. When the
// apply needs more locks than can be looked at in scanTime, the count says that
// there are at least so many.
func (h *heldLocks) rejected(addrs []string, held *state.ErrLocked) error {
	var holders []*state.ErrLocked
	begun := time.Now()
	more := false
	for from := 0; from < len(addrs); from += lockWidth {
		if time.Since(begun) > scanTime {
			more = true
			break
		}
		to := min(from+lockWidth, len(addrs))
		errs := make([]error, to-from)
		inParallel(to-from, lockWidth, func(i int) { errs[i] = h.st.Conflict(addrs[from+i]) })
		for _, err := range errs {
			var locked *state.ErrLocked
			if errors.As(err, &locked) {
				holders = append(holders, locked)
			}
		}
	}
	others := 0
	if len(holders) > 0 {
		held, others = holders[0], len(holders)-1
	}
	return WithCode(ExitLocked, fmt.Errorf("apply rejected: %w%s", held, heldAsWell(others, more)))
}

// heldAsWell is the end of the message about the nodes that are held besides
// the one the message names. more says that not every node was looked at.
func heldAsWell(n int, more bool) string {
	atLeast := ""
	if more {
		atLeast = "at least "
	}
	switch {
	case n == 0 && !more:
		return ""
	case n == 1 && !more:
		return "; 1 other node is held as well"
	case n == 0:
		return "; other nodes may be held as well"
	}
	return fmt.Sprintf("; %s%d other nodes are held as well", atLeast, n)
}

// fence is called before a change reaches a provider. The heartbeat keeps the
// locks from going stale, but a computer that was suspended does not run it: if
// more than half the time after which a lock goes stale has passed since every
// lock was last known to be this apply's, the locks are renewed first, which
// finds out whether another process took one over meanwhile. An apply whose lock
// was lost makes no further change.
func (h *heldLocks) fence() error {
	if err := h.lostError(); err != nil {
		return err
	}
	if time.Since(h.lastBeat()) <= state.StaleAfter/2 {
		return nil
	}
	h.renew()
	if err := h.lostError(); err != nil {
		return err
	}
	if time.Since(h.lastBeat()) > state.StaleAfter/2 {
		return fmt.Errorf("cannot confirm that this apply still holds its locks: they were last renewed %s ago", time.Since(h.lastBeat()).Round(time.Second))
	}
	return nil
}

func (h *heldLocks) lostError() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lost
}

// release stops the heartbeat and releases the locks that are held. A lock that
// another process has taken over is left alone.
func (h *heldLocks) release() {
	h.once.Do(func() {
		close(h.stop)
		<-h.done // a renewal in flight would write a lock that is deleted below
		addrs := h.held()
		inParallel(len(addrs), lockWidth, func(i int) { _ = h.st.Unlock(addrs[i]) })
	})
}
