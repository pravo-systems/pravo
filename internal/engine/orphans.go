package engine

import (
	"container/heap"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
)

// planOrphans plans the deletion of state resources absent from the
// configuration (all managed resources when present is nil). The records are
// read at the same time, Parallelism at once; the changes come back dependents
// first.
func (e *Engine) planOrphans(ctx context.Context, present map[string]bool) ([]*Change, error) {
	reason := "removed from the specification: the state still records it"
	if present == nil {
		reason = "destroy requested: every resource in the state is deleted"
	}
	var records []*state.Resource
	for _, sr := range e.State.List() {
		if !present[sr.Address()] {
			records = append(records, sr)
		}
	}
	warm := newWarmth()
	for _, sr := range records {
		if ps, ok := e.Schemas[config.LocalName(sr.ProviderKey)]; ok {
			warm.up(ps.Resources[sr.Type])
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		mu       sync.Mutex
		firstErr error
		out      = make([]*Change, len(records))
	)
	order := newPrintOrder(len(records))
	inParallel(len(records), Parallelism, func(i int) {
		turn := order.turn(i)
		defer turn.pass()
		sr := records[i]
		if sr.Mode == "data" {
			out[i] = &Change{Addr: sr.Address(), Mode: sr.Mode, Type: sr.Type, Name: sr.Name, Action: ActionDelete, ProviderKey: sr.ProviderKey, Deps: sr.Dependencies, Reasons: []string{reason}, PriorRevision: sr.Revision, PriorGeneration: sr.Generation}
			return
		}
		if ctx.Err() != nil {
			return
		}
		ch, err := e.planDelete(withTurn(ctx, turn), sr, reason)
		if err != nil {
			// The first failure is the one that is reported; the ones that follow
			// are the reads it cancelled.
			mu.Lock()
			if firstErr == nil {
				firstErr = err
				cancel()
			}
			mu.Unlock()
			return
		}
		out[i] = ch
	})
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if present != nil {
		// A record that was read, or that was left unread within its refresh age,
		// is in those counts whether or not the specification declares it.
		removed := 0
		for _, ch := range out {
			if ch != nil && (ch.Refreshed || ch.Fresh) {
				removed++
			}
		}
		e.count(func(st *Stats) { st.Removed += removed })
	}
	return orderForDestroy(out), nil
}

// printOrder lets goroutines that read records at the same time print the line
// that announces a read in the order of the records, which is the order the
// reads would have one after the other. The reads themselves overlap: a
// goroutine waits for the line before its own, not for the read before its own.
type printOrder struct{ printed []chan struct{} }

func newPrintOrder(n int) *printOrder {
	o := &printOrder{printed: make([]chan struct{}, n)}
	for i := range o.printed {
		o.printed[i] = make(chan struct{})
	}
	return o
}

// turn returns the turn of record i.
func (o *printOrder) turn(i int) *printTurn { return &printTurn{order: o, i: i} }

// printTurn is the turn of one record to print its line.
type printTurn struct {
	order *printOrder
	i     int
	once  sync.Once
}

// pass ends the turn, whether or not the record printed a line. Every turn is
// passed, or the records after it wait for ever. A turn is over only when the
// turns before it are, so a record that prints no line does not let the ones
// after it print before the ones before it.
func (t *printTurn) pass() {
	t.once.Do(func() {
		mine := t.order.printed[t.i]
		if t.i == 0 {
			close(mine)
			return
		}
		prev := t.order.printed[t.i-1]
		select {
		case <-prev:
			close(mine)
		default:
			go func() {
				<-prev
				close(mine)
			}()
		}
	})
}

type turnKey struct{}

// withTurn makes the turn the one that announce takes.
func withTurn(ctx context.Context, t *printTurn) context.Context {
	return context.WithValue(ctx, turnKey{}, t)
}

// announce prints a line, in its turn when ctx has one. A turn is for one line.
func announce(ctx context.Context, print func()) {
	t, _ := ctx.Value(turnKey{}).(*printTurn)
	if t == nil {
		print()
		return
	}
	if t.i > 0 {
		select {
		case <-t.order.printed[t.i-1]:
		case <-ctx.Done():
		}
	}
	print()
	t.pass()
}

// destroyOrphans deletes the objects of the delete changes of a plan that are
// not applied yet, a change after the changes of every record that depends on
// it, and independent changes at the same time. It returns how many it
// deleted. After an error the deletes that are running finish and are recorded;
// no other starts.
func (e *Engine) destroyOrphans(ctx context.Context, plan *Plan) (int, error) {
	var deletes []*Change
	for _, ch := range plan.Changes {
		if ch.Action == ActionDelete && !ch.Applied {
			deletes = append(deletes, ch)
		}
	}
	warm := newWarmth()
	for _, ch := range deletes {
		warm.up(ch.Schema)
	}
	var (
		mu        sync.Mutex
		destroyed int
	)
	err := inReverse(deletes, Parallelism, func(ch *Change) error {
		if ctx.Err() != nil {
			// A destroy that the provider has begun is not cancelled; one that has
			// not begun does not.
			return fmt.Errorf("interrupted before %s was destroyed", ch.Addr)
		}
		if ch.Mode == "data" {
			if _, err := e.State.Remove(ch.Addr); err != nil {
				return err
			}
			e.txn.Record(ch.Addr, "forget", 0, 0)
			e.complete(ch.Addr)
			return nil
		}
		prov, err := e.Providers.Provider(ctx, ch.ProviderKey)
		if err != nil {
			return err
		}
		start := time.Now()
		e.step("destroy", ch.Addr, "start", idOf(ch.Prior), time.Time{}, "%s: Destroying... %s", ch.Addr, idSuffix(ch.Prior))
		nullV := tftypes.NewValue(ch.Schema.Block.Type(), nil)
		if _, err := e.applyChange(ctx, prov, ch, ch.Prior, nullV, nullV, ch.PriorPrivate); err != nil {
			return err
		}
		before := 0
		if prev := e.State.Get(ch.Addr); prev != nil {
			before = prev.Revision
		}
		if _, err := e.State.Remove(ch.Addr); err != nil {
			// The object is destroyed and its record is not removed.
			e.unrecorded(ch.Addr, "destruction")
			return fmt.Errorf("%s: the record could not be removed: %w", ch.Addr, err)
		}
		e.txn.Record(ch.Addr, "delete", before, 0)
		e.complete(ch.Addr)
		mu.Lock()
		destroyed++
		mu.Unlock()
		e.step("destroy", ch.Addr, "done", "", start, "%s: Destruction complete after %s", ch.Addr, since(start))
		return nil
	})
	return destroyed, err
}

// warmth keeps track of the schemas whose types are worked out. A schema works
// out the types of its attributes the first time they are asked for and keeps
// them, which is not safe to do from several goroutines at once; once they are
// kept, any number of goroutines read them.
type warmth map[*schema.Schema]bool

func newWarmth() warmth { return warmth{} }

// up works out the types of s, if that was not done yet.
func (w warmth) up(s *schema.Schema) {
	if s != nil && s.Block != nil && !w[s] {
		w[s] = true
		s.Block.Type()
	}
}

// inReverse calls do for every change, a change after the calls for the changes
// of the records that depend on it, so that a record goes before what it
// depends on. Changes that do not wait for each other run at the same time,
// width at most, and the one that heads the longest chain of changes that follow
// it starts first, so that a long chain does not begin when everything else is
// done. After an error no further change starts, the ones that run finish, and
// the first error is returned. Changes that wait for each other in a cycle of the
// recorded dependencies have no order to follow; they run one at a time in the
// order of changes once the others are done, which is the order orderForDestroy
// gives them.
func inReverse(changes []*Change, width int, do func(*Change) error) error {
	index := make(map[string]int, len(changes))
	for i, c := range changes {
		index[c.Addr] = i
	}
	// below[i] lists the changes that change i depends on; waiting[j] counts the
	// changes that depend on j and have not run.
	below := make([][]int, len(changes))
	waiting := make([]int, len(changes))
	for i, c := range changes {
		listed := map[int]bool{}
		for _, d := range c.Deps {
			if j, ok := index[d]; ok && j != i && !listed[j] {
				listed[j] = true
				below[i] = append(below[i], j)
				waiting[j]++
			}
		}
	}
	// rank[i] is the length of the longest chain that starts with change i.
	rank := make([]int, len(changes))
	visit := make([]int8, len(changes)) // 1 while on the way down, 2 when done
	var rise func(i int) int
	rise = func(i int) int {
		if visit[i] == 2 {
			return rank[i]
		}
		visit[i] = 1
		longest := 0
		for _, j := range below[i] {
			if visit[j] != 1 { // an edge back to a change on the way down closes a cycle
				longest = max(longest, rise(j))
			}
		}
		visit[i], rank[i] = 2, longest+1
		return rank[i]
	}
	for i := range changes {
		rise(i)
	}

	var (
		mu       sync.Mutex
		wake     = sync.NewCond(&mu)
		firstErr error
		ran      = make([]bool, len(changes))
		queue    = &byRank{rank: rank}
		running  int
	)
	for i := range changes {
		if waiting[i] == 0 {
			heap.Push(queue, i)
		}
	}
	worker := func() {
		mu.Lock()
		defer mu.Unlock()
		for firstErr == nil {
			if queue.Len() == 0 {
				if running == 0 {
					break
				}
				wake.Wait()
				continue
			}
			i := heap.Pop(queue).(int)
			running++
			mu.Unlock()
			err := do(changes[i])
			mu.Lock()
			running--
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
			} else {
				ran[i] = true
				for _, j := range below[i] {
					if waiting[j]--; waiting[j] == 0 {
						heap.Push(queue, j)
					}
				}
			}
			wake.Broadcast()
		}
		wake.Broadcast()
	}
	var wg sync.WaitGroup
	for w := 0; w < min(width, len(changes)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker()
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	for i, c := range changes {
		if !ran[i] {
			if err := do(c); err != nil {
				return err
			}
		}
	}
	return nil
}

// byRank is a queue of the indexes of changes: the one with the longest chain
// first, and the first of the changes in order among equals.
type byRank struct {
	rank  []int
	items []int
}

func (q *byRank) Len() int { return len(q.items) }
func (q *byRank) Less(a, b int) bool {
	x, y := q.items[a], q.items[b]
	return q.rank[x] > q.rank[y] || (q.rank[x] == q.rank[y] && x < y)
}
func (q *byRank) Swap(a, b int) { q.items[a], q.items[b] = q.items[b], q.items[a] }
func (q *byRank) Push(x any)    { q.items = append(q.items, x.(int)) }
func (q *byRank) Pop() any {
	n := len(q.items) - 1
	x := q.items[n]
	q.items = q.items[:n]
	return x
}
