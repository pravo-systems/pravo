package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/ui"
)

func protectCheckCmd() *cobra.Command {
	var allowLocal bool
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check that the store can be sealed",
		Long: `Reads the store and says whether it can be sealed. It tests the store harder
than the probe that every command makes: eight writers race to replace one
object and exactly one must win, and a key that has just been written must be in
the listing. It lists what is running now, reads the readers file, and writes
nothing to the state.

A state in a local directory is refused, because its compare-and-swap is a lock
of one user. --allow-local accepts a directory that only you use.

Exit codes: 4 when a command is running or a lock is held, 6 when the readers
file is missing or invalid.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newProtectRun(cmd)
			if err != nil {
				return err
			}
			return p.check(allowLocal)
		},
	}
	cmd.Flags().BoolVar(&allowLocal, "allow-local", false, "accept a state in a local directory that only you use")
	return cmd
}

func (p *protectRun) check(allowLocal bool) error {
	if err := p.refuseLocal(allowLocal); err != nil {
		return err
	}
	b, err := p.backend(false)
	if err != nil {
		return err
	}
	st, _, _, err := p.open(b, false, false)
	if err != nil {
		return err
	}
	prot := st.Protection()
	res := checkResult{Store: p.location(), Format: prot.Format, Records: prot.Records}
	if prot.State == "sealed" {
		res.Sealed = true
		if flags.JSON {
			return emitJSON(res)
		}
		out.Println("Already sealed. Use pravo protect status.")
		return nil
	}
	if prot.State == "unsealing" {
		if flags.JSON {
			return emitJSON(res)
		}
		out.Println("The state is being unsealed: it was started and did not finish. Finish it with pravo protect unseal.")
		return nil
	}
	out.Printf("%-16s %s\n", "Store", p.storeLine())
	cond, err := p.probeLines(b)
	out.Printf("%-16s %s\n", "Conditional", cond[0])
	if cond[1] != "" {
		out.Printf("%-16s %s\n", "", cond[1])
	}
	if err != nil {
		return err
	}
	txns, err := st.Transactions()
	if err != nil {
		return err
	}
	line := fmt.Sprintf("%s · format %d", counted(prot.Records, "record", "records"), prot.Format)
	if v := lastWriter(txns); v != "" {
		line += " · last written by pravo " + v
		res.LastWrite = v
	}
	if prot.State == "migrating" && prot.Migration != nil {
		line += fmt.Sprintf(" · sealing began, %s of %s records done", commas(prot.Migration.Done), commas(prot.Migration.Total))
	}
	if prot.State == "unsealing" && prot.Migration != nil {
		line += fmt.Sprintf(" · unsealing began, %s of %s records done", commas(prot.Migration.Done), commas(prot.Migration.Total))
	}
	out.Printf("%-16s %s\n", "State", line)
	rf := p.rf
	switch {
	case !rf.Found:
		out.Printf("%-16s not found (looked in %s)\n", "Readers file", rf.shownDir())
	case rf.Err != nil:
		out.Printf("%-16s %s · invalid: %s\n", "Readers file", rf.shown(), rf.problem())
	default:
		out.Printf("%-16s %s · %s (%s)\n", "Readers file", rf.shown(), plural(len(rf.Readers), "reader", "readers"), nameList(readerNames(rf.Readers)))
		res.Readers = readerNames(rf.Readers)
	}
	running, locks, err := activity(st, txns)
	if err != nil {
		return err
	}
	res.Running, res.Locks = running, locks
	out.Printf("%-16s %s · %s held\n", "Running", plural(running, "transaction", "transactions"), plural(locks, "lock", "locks"))
	out.Printf("%-16s %s\n", "Machines", machines(txns, time.Now()))
	switch {
	case !rf.Found || rf.Err != nil || len(rf.Readers) == 0:
		res.NotReady = "readers file"
		if flags.JSON {
			if e := emitJSON(res); e != nil {
				return e
			}
			return engine.WithCode(engine.ExitInvalid, errSilent)
		}
		return engine.WithCode(engine.ExitInvalid, errSilent)
	case running > 0 || locks > 0:
		res.NotReady = "running"
		out.Println(out.C(ui.Red, "Not ready: wait for them, or release stale locks with pravo state unlock --stale."))
		if flags.JSON {
			if e := emitJSON(res); e != nil {
				return e
			}
		}
		return engine.WithCode(engine.ExitLocked, errSilent)
	}
	res.Ready = true
	out.Println(out.C(ui.Green, "Ready to seal. Next: pravo protect init --encrypt"))
	if flags.JSON {
		return emitJSON(res)
	}
	return nil
}

// probeLines runs the probes of an S3 or http store and returns the two lines of
// "Conditional", and the error of the first check that failed.
func (p *protectRun) probeLines(b state.Backend) ([2]string, error) {
	sb, ok := b.(state.Prober)
	if !ok {
		return [2]string{"a local directory: compare-and-swap is a lock of one user (--allow-local)"}, nil
	}
	short := map[string]string{
		state.ProbeChecks[0]: "create-only", state.ProbeChecks[1]: "replace", state.ProbeChecks[2]: "delete", state.ProbeChecks[3]: "concurrent creates",
	}
	var first []string
	failed := false
	err := sb.ProbeSteps(p.ctx, func(check string, err error) {
		switch {
		case err != nil:
			failed = true
			first = append(first, short[check]+" "+out.C(ui.Red, "FAILED"))
		case check != state.ProbeChecks[3]:
			first = append(first, short[check]+" ok")
		}
	})
	if err != nil {
		if len(first) == 0 {
			first = append(first, "not tested")
		}
		_ = failed
		return [2]string{strings.Join(first, " · ")}, err
	}
	var second []string
	err = sb.ProbeStrict(p.ctx, func(check string, err error) {
		switch {
		case check == state.StrictChecks[0] && err == nil:
			second = append(second, "8 concurrent replaces: 1 winner")
		case check == state.StrictChecks[0]:
			second = append(second, "8 concurrent replaces: "+out.C(ui.Red, "FAILED"))
		case err == nil:
			second = append(second, "listing sees a new key at once")
		default:
			second = append(second, "listing after a put: "+out.C(ui.Red, "FAILED"))
		}
	})
	return [2]string{strings.Join(first, " · "), strings.Join(second, " · ")}, err
}

// lastWriter is the version of pravo that wrote the newest transaction that
// records one.
func lastWriter(txns []state.Transaction) string {
	for _, t := range txns { // newest first
		if t.Version != "" {
			return t.Version
		}
	}
	return ""
}

// activity counts the transactions that are running and not explained by a
// command that was killed, and the locks that are held and not stale.
func activity(st *state.Store, txns []state.Transaction) (running, locks int, err error) {
	interrupted, err := st.Interrupted(txns)
	if err != nil {
		return 0, 0, err
	}
	for _, t := range txns {
		if t.Status == "running" && !interrupted[t.ID] {
			running++
		}
	}
	ls, err := st.Locks()
	if err != nil {
		return 0, 0, err
	}
	for _, l := range ls {
		if !l.Stale {
			locks++
		}
	}
	return running, locks, nil
}

// machines says which versions of pravo wrote in the last 30 days.
func machines(txns []state.Transaction, now time.Time) string {
	since := now.AddDate(0, 0, -30)
	var recent, versioned int
	oldest := ""
	for _, t := range txns {
		if t.Started.Before(since) {
			continue
		}
		recent++
		if t.Version == "" {
			continue
		}
		versioned++
		if oldest == "" || versionLess(t.Version, oldest) {
			oldest = t.Version
		}
	}
	switch {
	case recent == 0:
		return "no writer in the last 30 days"
	case versioned < recent:
		return fmt.Sprintf("versions are not recorded by this state's older writers (%d of %d transactions in 30 days)", recent-versioned, recent)
	}
	return "every writer in the last 30 days used pravo >= " + oldest
}

// versionLess orders versions such as 0.1.0, 0.10.2 and 0.1.0-dev: numbers by
// value, and a version with a suffix before the same one without.
func versionLess(a, b string) bool {
	an, as, _ := strings.Cut(strings.TrimPrefix(a, "v"), "-")
	bn, bs, _ := strings.Cut(strings.TrimPrefix(b, "v"), "-")
	ap, bp := strings.Split(an, "."), strings.Split(bn, ".")
	for i := 0; i < len(ap) || i < len(bp); i++ {
		var x, y int
		if i < len(ap) {
			x, _ = strconv.Atoi(ap[i])
		}
		if i < len(bp) {
			y, _ = strconv.Atoi(bp[i])
		}
		if x != y {
			return x < y
		}
	}
	if (as == "") != (bs == "") {
		return as != ""
	}
	return as < bs
}
