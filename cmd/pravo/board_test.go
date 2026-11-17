package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/serve"
	"github.com/pravo-systems/pravo/internal/ui"
)

// The board of "pravo serve --apply" keeps the plan of the files as they are and applies it once, by its ticket, as
// "pravo apply <planfile>" applies a plan file. A plan with nothing to do, and a plan that was not asked to be kept, get
// no ticket; another ticket, or the same one again, applies nothing.
func TestBoardAppliesTheKeptPlanOnce(t *testing.T) {
	rt := &runContext{}
	dir := localRoot(t, rt, "hello: $local.#Resource.local_file & {filename: \"hello.txt\", content: \"hello\\n\", $lifecycle: prevent_destroy: true}\n")
	rt.out = ui.NewPrinter(io.Discard, false)
	t.Chdir(dir)
	opts, closeBoard := boardOptions(rt, true)
	ctx := context.Background()
	var steps []serve.BoardStep
	step := func(s serve.BoardStep) { steps = append(steps, s) }

	if res, err := opts.Plan(ctx, serve.BoardPlanRequest{}, step); err != nil || res.Add != 1 || res.Ticket != "" {
		t.Fatalf("a plan that was not asked to be kept: %+v %v", res, err)
	}
	res, err := opts.Plan(ctx, serve.BoardPlanRequest{Keep: true}, step)
	if err != nil || res.Add != 1 || !strings.HasPrefix(res.Ticket, "bp-") || res.Held != "" {
		t.Fatalf("the plan to keep: %+v %v", res, err)
	}
	if _, err := opts.Apply(ctx, "bp-0", step); err == nil || !strings.Contains(err.Error(), "not the last plan of the board") {
		t.Fatalf("another ticket: %v", err)
	}
	if exists(dir, "hello.txt") {
		t.Fatal("another ticket applied the plan")
	}
	steps = nil
	got, err := opts.Apply(ctx, res.Ticket, step)
	if err != nil || got.Added != 1 || got.Txn == "" || !exists(dir, "hello.txt") {
		t.Fatalf("the apply: %+v %v", got, err)
	}
	if !hasStep(steps, "hello") {
		t.Errorf("the steps of the apply: %+v", steps)
	}
	if _, err := opts.Apply(ctx, res.Ticket, step); err == nil {
		t.Fatal("a ticket was used twice")
	}
	if res, err := opts.Plan(ctx, serve.BoardPlanRequest{Keep: true}, step); err != nil || res.Add+res.Change+res.Destroy != 0 || res.Ticket != "" || res.Held != "" {
		t.Errorf("a plan with nothing to do: %+v %v", res, err)
	}
	// a plan that the state moved past since is not applied
	main := filepath.Join(dir, "main.cue")
	spec, _ := os.ReadFile(main)
	if err := os.WriteFile(main, append(spec, "two: $local.#Resource.local_file & {filename: \"two.txt\", content: \"two\\n\"}\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = opts.Plan(ctx, serve.BoardPlanRequest{Keep: true}, step)
	if err != nil || res.Add != 1 || res.Ticket == "" {
		t.Fatalf("the plan of another file: %+v %v", res, err)
	}
	if text, code := runPravo(t, rt, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("the apply from a terminal: %s", text)
	}
	rt.out = ui.NewPrinter(io.Discard, false)
	if _, err := opts.Apply(ctx, res.Ticket, step); err == nil || !strings.Contains(err.Error(), "the state changed since this plan was made: plan again (two changed in state") {
		t.Errorf("a plan that the state moved past: %v", err)
	}
	// what "pravo apply" asks a person at a terminal for is not applied from the page
	spec, _ = os.ReadFile(main)
	if err := os.WriteFile(main, []byte(strings.Split(string(spec), "hello:")[0]+"two: $local.#Resource.local_file & {filename: \"two.txt\", content: \"two\\n\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res, err := opts.Plan(ctx, serve.BoardPlanRequest{Keep: true}, step); err != nil || res.Destroy != 1 || res.Ticket != "" || !strings.Contains(res.Held, "prevent_destroy") {
		t.Errorf("a plan that destroys what is kept from destroying: %+v %v", res, err)
	}
	closeBoard()
	text, _ := runPravo(t, rt, dir, "state", "log")
	if !strings.Contains(text, "applied from the drawing board of pravo serve") {
		t.Errorf("the history does not say where the apply came from:\n%s", text)
	}
}

func hasStep(steps []serve.BoardStep, addr string) bool {
	for _, s := range steps {
		if s.Addr == addr {
			return true
		}
	}
	return false
}

// A ticket is good for a while: an old one is refused, and its plan file removed.
func TestBoardTicketIsGoodForAWhile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bp-1.plan")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	k := &boardKept{dir: dir, cur: &boardTicket{id: "bp-1", path: path, at: time.Now().Add(-boardTicketLife - time.Minute)}}
	if _, err := k.take("bp-1"); err == nil || !strings.Contains(err.Error(), "older than 15 minutes") {
		t.Fatalf("an old ticket: %v", err)
	}
	if exists(dir, "bp-1.plan") || k.cur != nil {
		t.Error("the old plan is kept")
	}
	k.close()
	if exists(dir, ".") {
		t.Error("close left the directory")
	}
}

// A plan of the board that only moves a record, as a fold does, says what it moves and from where, is kept to be
// applied, and the apply says how many records it moved.
func TestBoardAppliesAMove(t *testing.T) {
	rt := &runContext{}
	dir := localRoot(t, rt, "hello: $local.#Resource.local_file & {filename: \"hello.txt\", content: \"hello\\n\"}\n")
	if text, code := runPravo(t, rt, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("the first apply: %s", text)
	}
	rt.out = ui.NewPrinter(io.Discard, false)
	t.Chdir(dir)
	main := filepath.Join(dir, "main.cue")
	spec, _ := os.ReadFile(main)
	if err := os.WriteFile(main, []byte(strings.Replace(string(spec), "hello: $local.#Resource.local_file & {", "greeting: $local.#Resource.local_file & {$from: \"hello\", ", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	opts, closeBoard := boardOptions(rt, true)
	defer closeBoard()
	ctx := context.Background()
	step := func(serve.BoardStep) {}
	res, err := opts.Plan(ctx, serve.BoardPlanRequest{Keep: true}, step)
	if err != nil || res.Move != 1 || res.Add+res.Change+res.Destroy != 0 || res.Ticket == "" {
		t.Fatalf("the plan of a move: %+v %v", res, err)
	}
	if len(res.Changes) != 1 || res.Changes[0] != (serve.BoardChange{Addr: "greeting", Action: "move", From: "hello"}) {
		t.Errorf("the changes of a move: %+v", res.Changes)
	}
	got, err := opts.Apply(ctx, res.Ticket, step)
	if err != nil || got.Moved != 1 || got.Added+got.Changed+got.Destroyed != 0 {
		t.Fatalf("the apply of a move: %+v %v", got, err)
	}
}

// The board starts from a Terraform state as "pravo shadow" does: the same file, the same records, the same plan of
// them, and the secret of the state is in neither the file nor what the page sees. The text of a state that the person
// chose is read as standard input is. A second start finds the state holding records and is refused.
func TestBoardStartsFromATerraformState(t *testing.T) {
	rt := &runContext{}
	dir := shadowRoot(t, rt)
	rt.out = ui.NewPrinter(io.Discard, false)
	t.Chdir(dir)
	opts, closeBoard := boardOptions(rt, false)
	defer closeBoard()
	ctx := context.Background()
	res, err := opts.Start(ctx, serve.BoardStartRequest{Place: "terraform.tfstate"})
	if err != nil || res.File != "shadow.cue" || res.From != "terraform.tfstate" || res.Resources != 3 || !res.Verified || !res.Guarded || res.Secrets == 0 {
		t.Fatalf("the start: %+v %v", res, err)
	}
	seen, _ := json.Marshal(res)
	src, _ := os.ReadFile(filepath.Join(dir, "shadow.cue"))
	if strings.Contains(string(seen), "hunter2") || strings.Contains(string(src), "hunter2") {
		t.Errorf("the secret of the state reached the page or the file:\n%s\n%s", seen, src)
	}
	if !strings.Contains(string(src), "size: logs.id") {
		t.Errorf("the file of the start:\n%s", src)
	}
	text, code := runPravo(t, rt, dir, "plan")
	if code != 0 || !strings.Contains(text, "No changes") {
		t.Errorf("the plan after the start (%d):\n%s", code, text)
	}
	rt.out = ui.NewPrinter(io.Discard, false)
	if _, err := opts.Start(ctx, serve.BoardStartRequest{State: shadowTfstate}); err == nil || !strings.Contains(err.Error(), "already holds 3 resources") {
		t.Errorf("a start into a state that holds records: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "shadow-2.cue")); err == nil {
		t.Error("a refused start left a file")
	}

	rt2 := &runContext{}
	dir2 := shadowRoot(t, rt2)
	rt2.out = ui.NewPrinter(io.Discard, false)
	t.Chdir(dir2)
	opts2, close2 := boardOptions(rt2, false)
	defer close2()
	res, err = opts2.Start(ctx, serve.BoardStartRequest{State: shadowTfstate})
	if err != nil || res.Resources != 3 || !res.Verified || res.From != "the state you chose" {
		t.Fatalf("the start from the text of a state: %+v %v", res, err)
	}
}
