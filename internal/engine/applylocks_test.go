package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"pravo.dev/pravo/internal/plog"
	"pravo.dev/pravo/internal/state"
	"pravo.dev/pravo/internal/state/statetest"
)

// lockEstate is n independent resources, r000 and up, and in addition a data
// source and a function call, which r000 reads, and an ephemeral resource, which
// nothing reads. The content of the resources whose index is in changed is v2.
func lockEstate(n int, changed ...int) spec {
	var src strings.Builder
	var names []string
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("r%03d", i)
		names = append(names, name)
		content := "v1"
		for _, c := range changed {
			if c == i {
				content = "v2"
			}
		}
		extra := fmt.Sprintf("content: %q", content)
		if i == 0 {
			extra = fmt.Sprintf(`content: "\(src.content)-\(fn.result)-%s"`, content)
		}
		fmt.Fprintf(&src, "%s: %s\n", name, thing(name, extra))
	}
	src.WriteString("src: " + source("s") + "\n")
	src.WriteString(`sess: {name: "sess", token: string | null}` + "\n")
	src.WriteString(`fn: {args: ["abc"], result: string | null}` + "\n")
	return spec{cue: src.String(), resources: names, data: []string{"src"}, ephemerals: []string{"sess"}, calls: []string{"fn"}}
}

// An apply takes the locks of the nodes it writes and no others. A data source
// that it reads, an ephemeral resource that it opens, a function that it calls
// and a node that it leaves as it is take none.
func TestLockSetIsTheWriteSet(t *testing.T) {
	h := newHarness(t)
	h.apply(lockEstate(10))
	if got := h.fake.called("OpenEphemeral"); got == 0 {
		t.Fatal("the estate does not open its ephemeral resource")
	}

	run := func(t *testing.T, changed ...int) (*Plan, *ApplyResult) {
		t.Helper()
		e := h.engine(lockEstate(10, changed...), Options{})
		p := plan(t, e)
		h.backend.Reset()
		h.fake.resetCalls()
		return p, apply(t, e, p)
	}

	t.Run("no change", func(t *testing.T) {
		p, _ := run(t)
		if len(p.Write) != 0 {
			t.Fatalf("the plan writes %v", p.Write)
		}
		if got := h.backend.Count("PutIfAbsent", "locks"); got != 0 {
			t.Errorf("%d locks were taken", got)
		}
		if w := h.backend.Writes(); w != 0 {
			t.Errorf("the apply wrote %d objects", w)
		}
	})

	t.Run("one change that reads the others", func(t *testing.T) {
		// r000 reads the data source and the function. The apply calls the function
		// again and reads the data source as its record, and locks r000 alone.
		p, _ := run(t, 0)
		if want := []string{"r000"}; !reflect.DeepEqual(p.Write, want) {
			t.Fatalf("the plan writes %v, want %v", p.Write, want)
		}
		if want := []string{"fn"}; !reflect.DeepEqual(p.Support, want) {
			t.Fatalf("the plan runs %v again, want %v", p.Support, want)
		}
		expectCalls(t, h, map[string]int{"OpenEphemeral": 0, "CallFunction": 1, "ReadDataSource": 0, "ApplyResourceChange": 1})
		if got := h.backend.Count("PutIfAbsent", "locks"); got != 1 {
			t.Errorf("%d locks were taken, want the one of r000", got)
		}
		if got := h.backend.Count("Delete", "locks"); got != 1 {
			t.Errorf("%d locks were released, want 1", got)
		}
		run(t) // back to the first content
	})

	t.Run("several changes", func(t *testing.T) {
		for _, k := range []int{2, 5, 9} {
			var changed []int
			for i := 1; i <= k; i++ {
				changed = append(changed, i)
			}
			p, _ := run(t, changed...)
			if len(p.Write) != k {
				t.Fatalf("%d changes: the plan writes %v", k, p.Write)
			}
			if got := h.backend.Count("PutIfAbsent", "locks"); got != k {
				t.Errorf("%d changes: %d locks were taken", k, got)
			}
			// Back to the first content for the next round.
			run(t)
		}
	})
}

// The count of locks an apply took is in the log that -v prints, and a plan that
// writes nothing takes none.
func TestApplyLogsTheNumberOfLocksItTook(t *testing.T) {
	var log bytes.Buffer
	plog.SetOutput(&log)
	plog.Configure(1, "")
	t.Cleanup(func() {
		plog.Configure(0, "")
		plog.SetOutput(os.Stderr)
	})

	h := newHarness(t)
	h.apply(lockEstate(10))
	for _, tc := range []struct {
		name    string
		changed []int
		want    string
	}{
		{"one change", []int{3}, "locked 1 resources"},
		{"three more changes", []int{1, 2, 3, 4}, "locked 3 resources"},
		{"no change", []int{1, 2, 3, 4}, ""},
	} {
		log.Reset()
		e := h.engine(lockEstate(10, tc.changed...), Options{})
		apply(t, e, plan(t, e))
		got := ""
		for _, line := range strings.Split(log.String(), "\n") {
			if i := strings.Index(line, "locked "); i >= 0 {
				got = line[i:]
			}
		}
		if got != tc.want {
			t.Errorf("%s: the log says %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The set of locks is taken whole or not at all. An apply that finds a node held
// changes nothing, leaves no lock of its own behind, and names the smallest node
// that is held and the number of the others.
func TestLockAdmissionIsAllOrNone(t *testing.T) {
	const n = 40
	all := func(content string) spec { return manySpec(n, func(int) string { return content }) }

	cases := []struct {
		name string
		hold []string
		want string
		// others is the text that counts the other nodes held.
		others string
		// tried is how many locks the apply took before it knew.
		tried int
	}{
		{"one node held", []string{"r025"}, "r025 is locked by", "", 0},
		{"three nodes held", []string{"r030", "r005", "r012"}, "r005 is locked by", "; 2 other nodes are held as well", 0},
		{"two nodes held", []string{"r030", "r012"}, "r012 is locked by", "; 1 other node is held as well", 0},
		{"the first node held", []string{"r000", "r039"}, "r000 is locked by", "; 1 other node is held as well", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.apply(all("v1"))
			other := h.process()
			other.SetActor("bob", "", "")
			for _, a := range c.hold {
				if err := other.Lock(a, "pravo apply", ""); err != nil {
					t.Fatal(err)
				}
			}
			e := h.engineOn(h.process(), all("v2"), Options{})
			p := plan(t, e)
			if len(p.Write) != n {
				t.Fatalf("the plan writes %d nodes", len(p.Write))
			}
			h.fake.resetCalls()
			h.backend.Reset()
			_, err := e.Apply(context.Background(), p)
			var coded *CodedError
			if !errors.As(err, &coded) || coded.Code != ExitLocked {
				t.Fatalf("apply: %v, want exit code 4", err)
			}
			if !strings.Contains(err.Error(), "apply rejected: "+c.want) || !strings.HasSuffix(err.Error(), c.others) {
				t.Errorf("the error is %q; want it to start with %q and end with %q", err, "apply rejected: "+c.want, c.others)
			}
			if c.others == "" && strings.Contains(err.Error(), "other") {
				t.Errorf("the error counts other nodes: %v", err)
			}
			var locked *state.ErrLocked
			if !errors.As(err, &locked) {
				t.Errorf("the error does not carry the lock: %v", err)
			}
			expectCalls(t, h, map[string]int{"ApplyResourceChange": 0})
			if got := h.backend.Count("Put", "resources"); got != 0 {
				t.Errorf("the apply wrote %d records", got)
			}
			if c.tried > 0 {
				if got := h.backend.Count("PutIfAbsent", "locks"); got != c.tried {
					t.Errorf("the apply tried %d locks, want %d", got, c.tried)
				}
			}
			// Only the locks of the other command remain.
			locks, lerr := h.process().Locks()
			if lerr != nil {
				t.Fatal(lerr)
			}
			var left []string
			for _, l := range locks {
				left = append(left, l.Addr)
			}
			want := append([]string(nil), c.hold...)
			sort.Strings(want)
			if !reflect.DeepEqual(left, want) {
				t.Errorf("locks left %v, want %v", left, want)
			}
			// The rejection is recorded as a transaction without changes.
			txns, _ := h.process().Transactions()
			if len(txns) == 0 || txns[0].Status != "rejected" || len(txns[0].Changes) != 0 {
				t.Errorf("the newest transaction is %+v", txns[0])
			}
		})
	}
}

// A plan approved at the prompt and applied after another command applied the
// same node is rejected, and the other command's record stays.
func TestApplyIsRejectedWhenNodeMovedAfterPlan(t *testing.T) {
	mk := func(content string) spec {
		return spec{cue: "r: " + thing("r", fmt.Sprintf("content: %q", content)) + "\n", resources: []string{"r"}}
	}
	h := newHarness(t)
	h.apply(mk("v1"))

	first := h.engineOn(h.process(), mk("v2"), Options{})
	p := plan(t, first) // the plan is shown and waits for approval
	second := h.engineOn(h.process(), mk("v3"), Options{})
	apply(t, second, plan(t, second))

	h.fake.resetCalls()
	_, err := first.Apply(context.Background(), p)
	var coded *CodedError
	if !errors.As(err, &coded) || coded.Code != ExitStale {
		t.Fatalf("apply: %v, want exit code 3", err)
	}
	for _, want := range []string{"r changed in state (revision 2, plan expected 1)", `run "pravo plan" again`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not say %q: %v", want, err)
		}
	}
	expectCalls(t, h, map[string]int{"ApplyResourceChange": 0})

	now := h.process()
	if sr := now.Get("r"); sr == nil || sr.Revision != 2 || !strings.Contains(string(sr.Attributes), "v3") {
		t.Errorf("r is %+v after the rejected apply; want the record of the other command", sr)
	}
	if locks, _ := now.Locks(); len(locks) != 0 {
		t.Errorf("locks left behind: %v", locks)
	}
	txns, err := now.Transactions()
	if err != nil {
		t.Fatal(err)
	}
	var writes, rejected int
	for _, tx := range txns {
		for _, c := range tx.Changes {
			if c.Addr == "r" && c.Before == 1 && c.After == 2 {
				writes++
			}
		}
		if tx.Status == "rejected" {
			rejected++
			if len(tx.Changes) != 0 || !strings.Contains(tx.Summary, "r changed in state") {
				t.Errorf("the rejected transaction is %+v", tx)
			}
		}
	}
	if writes != 1 || rejected != 1 {
		t.Errorf("%d transactions took r from revision 1 to 2 and %d were rejected; want 1 and 1", writes, rejected)
	}
}

// A held lock is reported before a moved record: the apply that cannot lock is
// turned away with exit code 4 whether or not its plan is still current.
func TestHeldLockIsReportedBeforeMovedRecord(t *testing.T) {
	mk := func(content string) spec {
		return spec{cue: "r: " + thing("r", fmt.Sprintf("content: %q", content)) + "\n", resources: []string{"r"}}
	}
	h := newHarness(t)
	h.apply(mk("v1"))
	e := h.engineOn(h.process(), mk("v2"), Options{})
	p := plan(t, e)
	other := h.engineOn(h.process(), mk("v3"), Options{})
	apply(t, other, plan(t, other)) // r moved
	holder := h.process()
	holder.SetActor("bob", "", "")
	if err := holder.Lock("r", "pravo apply", ""); err != nil {
		t.Fatal(err)
	}
	_, err := e.Apply(context.Background(), p)
	if got := exitCodeOf(err); got != ExitLocked {
		t.Errorf("apply: %v, exit code %d, want 4", err, got)
	}
}

// An apply that writes records leaves a transaction that is running from the
// moment it holds its locks until it ends; one that writes nothing leaves none.
func TestApplyRecordsARunningTransaction(t *testing.T) {
	mk := func(content string) spec {
		return spec{cue: "r: " + thing("r", fmt.Sprintf("content: %q", content)) + "\n", resources: []string{"r"}}
	}
	h := newHarness(t)
	h.apply(mk("v1"))
	e := h.engineOn(h.process(), mk("v2"), Options{})
	p := plan(t, e)
	var (
		mu   sync.Mutex
		seen []state.Transaction
	)
	e.onTask = func(string, bool) {
		txns, _ := h.process().Transactions()
		mu.Lock()
		defer mu.Unlock()
		for _, tx := range txns {
			if tx.ID == e.txnID() {
				seen = append(seen, tx)
			}
		}
	}
	res := apply(t, e, p)
	if len(seen) != 1 || seen[0].Status != "running" || seen[0].Finished != nil || seen[0].Command == "" {
		t.Fatalf("while the apply ran its transaction was %+v", seen)
	}
	txns, _ := h.process().Transactions()
	if txns[0].ID != res.Txn || txns[0].Status != "applied" || txns[0].Finished == nil || len(txns[0].Changes) != 1 {
		t.Errorf("after the apply its transaction is %+v", txns[0])
	}

	// Nothing to write, nothing recorded.
	again := h.engineOn(h.process(), mk("v2"), Options{})
	h.backend.Reset()
	apply(t, again, plan(t, again))
	if got := h.backend.Count("Put", "txn"); got != 0 {
		t.Errorf("an apply that writes nothing wrote %d transactions", got)
	}
}

// Holding the locks does not keep the heartbeat from running: it renews the
// locks taken so far while the others are still being taken, which takes longer
// than its interval. Run with -race.
func TestHeartbeatDoesNotRaceWithAcquisition(t *testing.T) {
	lb, err := state.NewLocalBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.OpenBackend(context.Background(), statetest.NewLatency(lb, 2*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	var addrs []string
	for i := 0; i < 160; i++ {
		addrs = append(addrs, fmt.Sprintf("r%03d", i))
	}
	var (
		mu     sync.Mutex
		failed []string
	)
	progress := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		failed = append(failed, fmt.Sprintf(format, args...))
	}
	h := newHeldLocks(st, progress, time.Millisecond)
	if err := h.acquire(addrs, "pravo apply", "txn"); err != nil {
		t.Fatal(err)
	}
	if got := h.held(); len(got) != len(addrs) {
		t.Fatalf("%d locks held, want %d", len(got), len(addrs))
	}
	time.Sleep(20 * time.Millisecond) // a few rounds of the heartbeat with every lock held
	h.release()
	if len(failed) > 0 {
		t.Errorf("the heartbeat failed: %v", failed[0])
	}
	if locks, _ := st.Locks(); len(locks) != 0 {
		t.Errorf("%d locks left", len(locks))
	}
}

// A lock that another command reclaimed is not deleted by the command that lost
// it.
func TestUnlockChecksOwnership(t *testing.T) {
	dir := t.TempDir()
	open := func() *state.Store {
		st, err := state.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	old, newer := open(), open()
	progress := func(string, ...any) {}
	held := newHeldLocks(old, progress, time.Hour)
	if err := held.acquire([]string{"a", "b"}, "pravo apply", "t1"); err != nil {
		t.Fatal(err)
	}
	// The old command stopped renewing, and a day passes.
	b := old.Backend()
	for _, addr := range []string{"a", "b"} {
		key := "locks/" + addr + ".lock"
		data, err := b.Get(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		var l state.Lock
		if err := json.Unmarshal(data, &l); err != nil {
			t.Fatal(err)
		}
		l.RenewedAt = l.RenewedAt.Add(-24 * time.Hour)
		l.Since = l.Since.Add(-24 * time.Hour)
		data, _ = json.Marshal(l)
		if err := b.Put(context.Background(), key, data); err != nil {
			t.Fatal(err)
		}
	}
	// Another command reclaims "a" and does not touch "b".
	if err := newer.Lock("a", "pravo apply", "t2"); err != nil {
		t.Fatalf("reclaiming a stale lock: %v", err)
	}
	held.release()

	locks, err := newer.Locks()
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, l := range locks {
		left = append(left, l.Addr)
		if l.Addr == "a" && l.Txn != "t2" {
			t.Errorf("the lock on a belongs to %q, want the command that reclaimed it", l.Txn)
		}
	}
	if want := []string{"a"}; !reflect.DeepEqual(left, want) {
		t.Errorf("locks left %v, want %v: a is the new holder's, and b was the old one's to release", left, want)
	}
}

// Before a change reaches a provider, an apply whose locks have not been
// renewed for a long time looks at them again, and stops if one is lost.
func TestSelfFenceAbortsWhenHeartbeatIsOverdue(t *testing.T) {
	setup := func(t *testing.T) (*harness, *heldLocks, *state.Store) {
		h := newHarness(t)
		held := newHeldLocks(h.state, func(string, ...any) {}, time.Hour)
		if err := held.acquire([]string{"a", "b", "c"}, "pravo apply", "t1"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(held.release)
		return h, held, h.process()
	}
	overdue := func(h *heldLocks) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.beat = time.Now().Add(-state.StaleAfter)
	}

	t.Run("a recent heartbeat costs nothing", func(t *testing.T) {
		h, held, _ := setup(t)
		h.backend.Reset()
		if err := held.fence(); err != nil {
			t.Fatal(err)
		}
		if got := h.backend.Count("Get") + h.backend.Count("Put"); got != 0 {
			t.Errorf("the check made %d calls to the backend", got)
		}
	})

	t.Run("an overdue heartbeat renews the locks", func(t *testing.T) {
		h, held, _ := setup(t)
		overdue(held)
		h.backend.Reset()
		if err := held.fence(); err != nil {
			t.Fatal(err)
		}
		if got := h.backend.Count("Put", "locks"); got != 3 {
			t.Errorf("%d locks were renewed, want 3", got)
		}
		if time.Since(held.lastBeat()) > time.Minute {
			t.Errorf("the check did not note the renewal")
		}
		h.backend.Reset()
		if err := held.fence(); err != nil || h.backend.Count("Get")+h.backend.Count("Put") != 0 {
			t.Errorf("the next check was not free: %v, %d calls", err, h.backend.Count("Get")+h.backend.Count("Put"))
		}
	})

	for _, how := range []string{"deleted", "taken over"} {
		t.Run("a lock that was "+how, func(t *testing.T) {
			_, held, other := setup(t)
			if how == "deleted" {
				if _, err := other.ForceUnlock("b"); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := other.ForceUnlock("b"); err != nil {
					t.Fatal(err)
				}
				if err := other.Lock("b", "pravo apply", "t2"); err != nil {
					t.Fatal(err)
				}
			}
			overdue(held)
			err := held.fence()
			if exitCodeOf(err) != ExitLocked || err == nil || !strings.Contains(err.Error(), "lock lost") || !strings.Contains(err.Error(), " b ") {
				t.Fatalf("check: %v, exit code %d; want exit code 4, lock lost, b", err, exitCodeOf(err))
			}
			// Once lost, every later check says so.
			if err := held.fence(); exitCodeOf(err) != ExitLocked {
				t.Errorf("the next check: %v", err)
			}
		})
	}

	t.Run("in an apply, before the first change", func(t *testing.T) {
		mk := func(content string) spec {
			return spec{cue: "r: " + thing("r", fmt.Sprintf("content: %q", content)) + "\n", resources: []string{"r"}}
		}
		h := newHarness(t)
		h.apply(mk("v1"))
		e := h.engineOn(h.process(), mk("v2"), Options{})
		p := plan(t, e)
		// The computer sleeps after the locks are taken: the heartbeat is overdue
		// and another command takes the lock over.
		e.onTask = func(addr string, apply bool) {
			if !apply {
				return
			}
			overdue(e.held)
			other := h.process()
			if _, err := other.ForceUnlock("r"); err != nil {
				t.Error(err)
			}
			if err := other.Lock("r", "pravo apply", "t2"); err != nil {
				t.Error(err)
			}
		}
		h.fake.resetCalls()
		_, err := e.Apply(context.Background(), p)
		if exitCodeOf(err) != ExitLocked || err == nil || !strings.Contains(err.Error(), "lock lost") {
			t.Fatalf("apply: %v, exit code %d; want exit code 4 and lock lost", err, exitCodeOf(err))
		}
		expectCalls(t, h, map[string]int{"ApplyResourceChange": 0})
		if sr := h.process().Get("r"); sr == nil || sr.Revision != 1 {
			t.Errorf("r is %+v; want the record before the apply", sr)
		}
	})
}

// Two commands that finish together each add their outputs to what the other
// recorded.
func TestConcurrentAppliesKeepBothOutputs(t *testing.T) {
	mk := func(ou, oz string) spec {
		return spec{
			cue: "u: " + thing("u", fmt.Sprintf("content: %q", ou)) + "\n" +
				"z: " + thing("z", fmt.Sprintf("content: %q", oz)) + "\n" +
				`ou: {value: "out-\(u.content)"}` + "\n" + `oz: {value: "out-\(z.content)"}` + "\n",
			resources: []string{"u", "z"},
			outputs:   []string{"ou", "oz"},
		}
	}
	h := newHarness(t)
	h.apply(mk("0", "0"))
	u, z := "0", "0"
	const rounds = 25
	for i := 1; i <= rounds; i++ {
		nu, nz := fmt.Sprintf("u%d", i), fmt.Sprintf("z%d", i)
		engines := []*Engine{
			h.engineOn(h.process(), mk(nu, z), Options{}),
			h.engineOn(h.process(), mk(u, nz), Options{}),
		}
		var (
			wg   sync.WaitGroup
			errs [2]error
		)
		for k, e := range engines {
			wg.Add(1)
			go func() {
				defer wg.Done()
				p, err := e.Plan(context.Background())
				if err == nil {
					_, err = e.Apply(context.Background(), p)
				}
				errs[k] = err
			}()
		}
		wg.Wait()
		for k, err := range errs {
			if err != nil {
				t.Fatalf("round %d, apply %d: %v", i, k, err)
			}
		}
		u, z = nu, nz
		out := h.process().Outputs()
		if got, want := string(out["ou"]), `"out-`+u+`"`; got != want {
			t.Fatalf("round %d: ou = %s, want %s", i, got, want)
		}
		if got, want := string(out["oz"]), `"out-`+z+`"`; got != want {
			t.Fatalf("round %d: oz = %s, want %s", i, got, want)
		}
	}
	if _, err := h.backend.Get(context.Background(), outputsLock); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("the lock on the outputs is left behind: %v", err)
	}
}

// outputsLock is the object an update of the outputs holds.
const outputsLock = "outputs.lock"

// Applies of disjoint sets of nodes in one state all succeed, whatever the
// number of them, and every record ends at the revision its writes give.
func TestDisjointAppliesAllSucceed(t *testing.T) {
	const applies, per = 8, 50
	h := newHarness(t)
	content := func(changed func(i int) bool) spec {
		return manySpec(applies*per, func(i int) string {
			if changed(i) {
				return "v2"
			}
			return "v1"
		})
	}
	h.apply(content(func(int) bool { return false }))

	engines := make([]*Engine, applies)
	plans := make([]*Plan, applies)
	for k := range engines {
		engines[k] = h.engineOn(h.process(), content(func(i int) bool { return i/per == k }), Options{})
		plans[k] = plan(t, engines[k])
		if len(plans[k].Write) != per {
			t.Fatalf("apply %d writes %d nodes", k, len(plans[k].Write))
		}
	}
	var (
		wg   sync.WaitGroup
		errs = make([]error, applies)
		gate = make(chan struct{})
	)
	for k := range engines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			_, errs[k] = engines[k].Apply(context.Background(), plans[k])
		}()
	}
	close(gate)
	wg.Wait()
	for k, err := range errs {
		if err != nil {
			t.Errorf("apply %d: %v", k, err)
		}
	}
	now := h.process()
	for i := 0; i < applies*per; i++ {
		addr := fmt.Sprintf("r%03d", i)
		if sr := now.Get(addr); sr == nil || sr.Revision != 2 || !strings.Contains(string(sr.Attributes), "v2") {
			t.Fatalf("%s is %+v; want revision 2 and v2", addr, sr)
		}
	}
	if locks, _ := now.Locks(); len(locks) != 0 {
		t.Errorf("%d locks left", len(locks))
	}
}

// Applies whose sets overlap are never granted the same node, and an apply is
// turned away only because another apply holds a node it needs. Four applies start
// together with sets that share nodes in the ways below; a granted apply holds its
// locks until all four have been decided, as an apply that is running would. The
// test counts the applies that were turned away although no apply that overlaps
// them was granted, which is the harm of taking locks without waiting.
func TestOverlappingAppliesHaveOneWinnerPerNode(t *testing.T) {
	trials := 200
	if testing.Short() {
		trials = 40
	}
	const applies = 4

	nodes := func(prefix string, lo, hi int) []string {
		var out []string
		for i := lo; i < hi; i++ {
			out = append(out, fmt.Sprintf("%s%03d", prefix, i))
		}
		return out
	}
	subsets := func(rnd *rand.Rand, size int) [][]string {
		out := make([][]string, applies)
		for k := range out {
			for _, i := range rnd.Perm(100)[:size] {
				out[k] = append(out[k], fmt.Sprintf("r%03d", i))
			}
		}
		return out
	}
	shapes := []struct {
		name string
		sets func(rnd *rand.Rand) [][]string
	}{
		{"one node that all four write", func(*rand.Rand) [][]string {
			out := make([][]string, applies)
			for k := range out {
				out[k] = append(nodes(fmt.Sprintf("p%d-", k), 0, 22), "x-shared")
			}
			return out
		}},
		{"a ring: each shares one node with the next", func(*rand.Rand) [][]string {
			out := make([][]string, applies)
			for k := range out {
				out[k] = append(nodes(fmt.Sprintf("p%d-", k), 0, 22), fmt.Sprintf("x-ring%d", k), fmt.Sprintf("x-ring%d", (k+1)%applies))
			}
			return out
		}},
		{"a chain of windows of 15 nodes, 10 apart", func(*rand.Rand) [][]string {
			out := make([][]string, applies)
			for k := range out {
				out[k] = nodes("r", k*10, k*10+15)
			}
			return out
		}},
		{"4 random subsets of 30 of 100 nodes", func(rnd *rand.Rand) [][]string { return subsets(rnd, 30) }},
		{"4 random subsets of 5 of 100 nodes", func(rnd *rand.Rand) [][]string { return subsets(rnd, 5) }},
		{"3 nodes shared in the last group", func(*rand.Rand) [][]string {
			out := make([][]string, applies)
			for k := range out {
				out[k] = append(nodes(fmt.Sprintf("p%d-", k), 0, 100), "z1", "z2", "z3")
			}
			return out
		}},
		{"pairs that share 10 nodes", func(*rand.Rand) [][]string {
			out := make([][]string, applies)
			for k := range out {
				out[k] = append(nodes(fmt.Sprintf("p%d-", k), 0, 40), nodes(fmt.Sprintf("s%d-", k/2), 0, 10)...)
			}
			return out
		}},
		{"4 identical sets of 50 nodes", func(*rand.Rand) [][]string {
			out := make([][]string, applies)
			for k := range out {
				out[k] = nodes("r", 0, 50)
			}
			return out
		}},
	}
	for _, sh := range shapes {
		t.Run(sh.name, func(t *testing.T) {
			t.Parallel()
			rnd := rand.New(rand.NewPCG(7, 11))
			dir := t.TempDir()
			stores := make([]*state.Store, applies)
			for k := range stores {
				st, err := state.Open(dir)
				if err != nil {
					t.Fatal(err)
				}
				stores[k] = st
			}
			var (
				owner    sync.Map // node -> the apply that holds it now
				granted  int
				spurious int
			)
			for trial := 0; trial < trials; trial++ {
				sets := sh.sets(rnd)
				for k := range sets {
					sort.Strings(sets[k])
				}
				overlap := func(i, j int) bool {
					for _, a := range sets[i] {
						if slices.Contains(sets[j], a) {
							return true
						}
					}
					return false
				}
				var (
					wg      sync.WaitGroup
					decided sync.WaitGroup
					gate    = make(chan struct{})
					won     = make([]bool, applies)
					results = make([]error, applies)
				)
				decided.Add(applies)
				for k := range stores {
					wg.Add(1)
					go func() {
						defer wg.Done()
						held := newHeldLocks(stores[k], func(string, ...any) {}, time.Hour)
						defer held.release()
						txn := stores[k].Begin("apply", "pravo apply")
						<-gate
						results[k] = held.acquire(sets[k], "pravo apply", txn.ID())
						won[k] = results[k] == nil
						if won[k] {
							// As an apply does once it holds its locks.
							if err := txn.Start(); err != nil {
								t.Error(err)
							}
						}
						decided.Done()
						if !won[k] {
							return
						}
						for _, a := range sets[k] {
							if prev, loaded := owner.LoadOrStore(a, k); loaded {
								t.Errorf("trial %d: %s is held by apply %v and by apply %d", trial, a, prev, k)
							}
						}
						decided.Wait() // an apply that is running holds its locks
						for _, a := range sets[k] {
							owner.Delete(a)
						}
					}()
				}
				close(gate)
				wg.Wait()
				if t.Failed() {
					t.FailNow()
				}
				for k := range won {
					if won[k] {
						granted++
						continue
					}
					if exitCodeOf(results[k]) != ExitLocked {
						t.Fatalf("trial %d: apply %d: %v", trial, k, results[k])
					}
					free := true
					for j := range won {
						if j != k && won[j] && overlap(k, j) {
							free = false
						}
					}
					if free {
						spurious++
					}
				}
			}
			if locks, _ := stores[0].Locks(); len(locks) != 0 {
				t.Errorf("%d locks left", len(locks))
			}
			rate := float64(spurious) / float64(trials*applies)
			t.Logf("%d trials of %d applies: %d granted, %d turned away although no apply they overlap was granted (%.1f%% of the applies)", trials, applies, granted, spurious, 100*rate)
			if granted == 0 {
				t.Error("no apply was granted its locks in any trial")
			}
			if rate > 0.05 {
				t.Errorf("%.1f%% of the applies were turned away with no winner to blame; want at most 5%%", 100*rate)
			}
		})
	}
}

// Applies of overlapping sets of nodes, started together from plans made at the
// same moment: each one applies in full or is turned away with exit code 3 or 4
// before it changes anything, and every record ends at the revision that the
// applies which wrote it give it.
func TestOverlappingAppliesLoseNoRecord(t *testing.T) {
	const applies, nodes, span, step = 4, 40, 15, 10
	trials := 20
	if testing.Short() {
		trials = 6
	}
	h := newHarness(t)
	current := make([]string, nodes) // what each record holds, as far as the test knows
	inRange := func(i, k int) bool { return i >= k*step && i < k*step+span }
	// specFor is the specification of apply k in a round: its nodes change, the
	// others stay as they are.
	specFor := func(round, k int) spec {
		return manySpec(nodes, func(i int) string {
			if inRange(i, k) {
				return fmt.Sprintf("round-%d-apply-%d", round, k)
			}
			return current[i]
		})
	}
	for i := range current {
		current[i] = "v0"
	}
	h.apply(specFor(0, 0))
	for i := range current {
		if inRange(i, 0) {
			current[i] = "round-0-apply-0"
		}
	}
	want := map[string]int{} // successful writes of each record
	for i := 0; i < nodes; i++ {
		want[fmt.Sprintf("r%03d", i)] = 1
	}
	var winners, rejected int
	for round := 1; round <= trials; round++ {
		engines := make([]*Engine, applies)
		plans := make([]*Plan, applies)
		for k := range engines {
			engines[k] = h.engineOn(h.process(), specFor(round, k), Options{})
			plans[k] = plan(t, engines[k])
		}
		var (
			wg   sync.WaitGroup
			errs = make([]error, applies)
			gate = make(chan struct{})
		)
		for k := range engines {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-gate
				_, errs[k] = engines[k].Apply(context.Background(), plans[k])
			}()
		}
		close(gate)
		wg.Wait()
		for k, err := range errs {
			if err != nil {
				if code := exitCodeOf(err); code != ExitStale && code != ExitLocked {
					t.Fatalf("round %d, apply %d: %v", round, k, err)
				}
				rejected++
				continue
			}
			winners++
			for i := range current {
				if inRange(i, k) {
					current[i] = fmt.Sprintf("round-%d-apply-%d", round, k)
				}
			}
			for _, addr := range plans[k].Write {
				want[addr]++
			}
		}
		now := h.process()
		for addr, rev := range want {
			if sr := now.Get(addr); sr == nil || sr.Revision != rev {
				t.Fatalf("round %d: %s is at revision %v, want %d", round, addr, sr, rev)
			}
		}
		for i, c := range current {
			if got := recordedContent(t, &harness{state: now}, fmt.Sprintf("r%03d", i)); got != c {
				t.Fatalf("round %d: r%03d holds %q, want %q", round, i, got, c)
			}
		}
		if locks, _ := now.Locks(); len(locks) != 0 {
			t.Fatalf("round %d: %d locks left", round, len(locks))
		}
	}
	t.Logf("%d rounds of %d applies: %d applied, %d turned away", trials, applies, winners, rejected)
	if winners == 0 {
		t.Error("no apply succeeded in any round")
	}
}

// Against a backend where every call takes 10 ms, an apply that creates 501
// resources takes the time of its writes and not of its locks: each node costs
// one lock, one read to check it, one write and one release.
func TestApplyOverASlowBackend(t *testing.T) {
	const n = 501
	delay := 10 * time.Millisecond
	h := newHarnessOver(t, func(b state.Backend) state.Backend { return statetest.NewLatency(b, delay) })
	e := h.engine(manySpec(n, func(int) string { return "v1" }), Options{})
	p := plan(t, e)
	if len(p.Write) != n {
		t.Fatalf("the plan writes %d nodes", len(p.Write))
	}
	h.backend.Reset()
	start := time.Now()
	apply(t, e, p)
	took := time.Since(start)
	t.Logf("apply of %d creates, %s per call: %s", n, delay, took.Round(time.Millisecond))

	for _, c := range []struct {
		op, prefix string
		want       int
	}{
		{"PutIfAbsent", "locks", n},
		{"Get", "resources", n},
		{"Put", "resources", n},
		{"Get", "locks", n},
		{"Delete", "locks", n},
		{"Put", "locks", 0},
	} {
		if got := h.backend.Count(c.op, c.prefix); got != c.want {
			t.Errorf("%s on %s: %d calls, want %d", c.op, c.prefix, got, c.want)
		}
	}
	// One call at a time, the calls above alone would take more than 20 s.
	if took > 8*time.Second {
		t.Errorf("the apply took %s", took)
	}
}

// A node that another actor has reserved and that a plan only reads is reported
// by the plan and does not stop it, or the apply of it: the apply locks what it
// writes.
func TestPlanWarnsOfAReservedInput(t *testing.T) {
	mk := func(suffix string) spec {
		return spec{
			cue: "in: " + thing("in", "") + "\n" +
				"w: " + thing("w", `content: "\(in.id)-`+suffix+`"`) + "\n",
			resources: []string{"in", "w"},
		}
	}
	h := newHarness(t)
	h.apply(mk("1"))
	bob := h.process()
	bob.SetActor("bob", "", "")
	if _, err := bob.ReserveAll([]string{"in"}, "pravo plan --reserve 1h", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	var (
		mu    sync.Mutex
		lines []string
	)
	progress := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	e := h.engineOn(h.process(), mk("2"), Options{Actor: "alice", Progress: progress})
	p := plan(t, e)
	if _, ok := p.Inputs["in"]; !ok {
		t.Fatalf("the plan does not read in: %v", p.Inputs)
	}
	var warnings []string
	for _, l := range lines {
		if strings.HasPrefix(l, "Warning:") {
			warnings = append(warnings, l)
		}
	}
	if len(warnings) != 1 || !strings.HasPrefix(warnings[0], "Warning: in is reserved by bob (pravo plan --reserve 1h") || !strings.HasSuffix(warnings[0], "; this plan reads it") {
		t.Errorf("warnings: %q", warnings)
	}
	apply(t, e, p)
	if got := recordedContent(t, &harness{state: h.process()}, "w"); !strings.HasSuffix(got, "-2") {
		t.Errorf("w holds %q after the apply", got)
	}

	// A plan that changes the reserved node itself is rejected, as before.
	again := h.engineOn(h.process(), spec{
		cue:       "in: " + thing("in", `content: "changed"`) + "\nw: " + thing("w", `content: "\(in.id)-2"`) + "\n",
		resources: []string{"in", "w"},
	}, Options{Actor: "alice"})
	if _, err := again.Plan(context.Background()); exitCodeOf(err) != ExitLocked {
		t.Errorf("plan of the reserved node: %v", err)
	}
}
