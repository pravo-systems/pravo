package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/serve"
	"github.com/pravo-systems/pravo/internal/ui"
)

// boardStart brings a Terraform state onto the drawing board as "pravo shadow" brings it: the same run, with the same
// checks, into a new file of the specification and the records of the objects, which Terraform keeps until
// "pravo shadow take-over". It never replaces a file or a state that holds anything.
func boardStart(ctx context.Context, rt *runContext, req serve.BoardStartRequest) (*serve.BoardStartResult, error) {
	report, err := os.CreateTemp("", "pravo-board-start-*.json")
	if err != nil {
		return nil, err
	}
	_ = report.Close()
	defer os.Remove(report.Name())
	f := &shadowFlags{out: boardStartFile(), report: report.Name()}
	cmd := &cobra.Command{}
	addShadowSourceFlags(cmd)
	cmd.SetContext(ctx)
	place := req.Place
	if req.State != "" {
		place = "-"
		cmd.SetIn(strings.NewReader(req.State))
	}
	var warned bytes.Buffer
	prt := *rt
	prt.flags.JSON, prt.flags.Markdown = false, false
	prt.out = &ui.Printer{Out: &bytes.Buffer{}, Warnings: &warned}
	runErr := runShadow(cmd, &prt, place, f)
	out := &serve.BoardStartResult{File: f.out, From: place, Guarded: true, Notes: boardWarnings(warned.Bytes())}
	if req.State != "" {
		out.From = "the state you chose"
	}
	b, err := os.ReadFile(report.Name())
	if err != nil || len(b) == 0 {
		// the run stopped before it wrote anything
		if runErr == nil {
			runErr = errors.New("the shadow wrote no report")
		}
		return nil, runErr
	}
	var rep engine.ShadowReport
	if err := json.Unmarshal(b, &rep); err != nil {
		return nil, err
	}
	out.Resources, out.Modules, out.Lines = rep.Resources, rep.Modules, rep.Lines
	out.Verified, out.Wants = rep.Verified, rep.PlanWants
	out.LeftOut = rep.AttributesLeftOut
	out.Secrets = rep.SensitiveOmitted + rep.SensitiveReferred + rep.SensitiveComputed + rep.SensitiveRequired
	if runErr != nil {
		// the file and the records are written, and the plan of them wants a change: the page says what, and shows them
		out.Notes = append(out.Notes, runErr.Error())
	}
	return out, nil
}

// boardStartFile is the file a start writes: shadow.cue, or shadow-2.cue when that is taken.
func boardStartFile() string {
	name := "shadow.cue"
	for i := 2; i < 100; i++ {
		if _, err := os.Stat(name); errors.Is(err, os.ErrNotExist) {
			return name
		}
		name = "shadow-" + strconv.Itoa(i) + ".cue"
	}
	return name
}

// boardWarnings are the warnings a run printed, one for each, without their mark.
func boardWarnings(b []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if rest, ok := strings.CutPrefix(line, "Warning: "); ok {
			out = append(out, rest)
		} else if len(out) > 0 && strings.TrimSpace(line) != "" {
			out[len(out)-1] += "\n" + line
		}
	}
	return out
}
