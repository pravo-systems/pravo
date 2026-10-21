package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/registry"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/ui"
)

// Machine-facing surface: JSON output, attribution, explanations.

var (
	flagJSON   bool
	flagActor  string
	flagReason string
	// streamEvents makes --json stream progress as NDJSON (apply, destroy).
	streamEvents bool
)

// jsonText encodes v as JSON for a person or a program to read. The characters
// <, > and & are written as they are and not as \u003c, \u003e and \u0026, also
// inside a value that is already encoded (a json.RawMessage, such as a
// recorded attribute). The text ends with a newline.
func jsonText(v any, indent bool) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return unescapeHTML(buf.Bytes()), nil
}

// unescapeHTML writes the characters <, > and & of the strings of a JSON text
// as themselves where the text has them as \u003c, \u003e and \u0026. The text
// stays valid JSON: these three are ordinary characters in a string. Every
// backslash starts a pair, so a backslash that is itself escaped (\\u003c)
// is left alone.
func unescapeHTML(b []byte) []byte {
	if !bytes.Contains(b, []byte(`\u00`)) {
		return b
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] != '\\' || i+1 >= len(b) {
			out = append(out, b[i])
			continue
		}
		if b[i+1] == 'u' && i+6 <= len(b) {
			switch strings.ToLower(string(b[i+2 : i+6])) {
			case "003c":
				out, i = append(out, '<'), i+5
				continue
			case "003e":
				out, i = append(out, '>'), i+5
				continue
			case "0026":
				out, i = append(out, '&'), i+5
				continue
			}
		}
		out, i = append(out, b[i], b[i+1]), i+1
	}
	return out
}

// emitJSON writes one JSON document to stdout.
func emitJSON(v any) error {
	b, err := jsonText(v, true)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(b)
	return err
}

// emitLine writes one NDJSON line to stdout.
func emitLine(v any) {
	b, err := jsonText(v, false)
	if err != nil {
		return
	}
	os.Stdout.Write(b)
}

// gitRevision returns the root's git revision (with "-dirty" when the tree
// has changes), or "" when not in a repository.
func gitRevision(dir string) string {
	rev, err := exec.Command("git", "-C", dir, "rev-parse", "--short=12", "HEAD").Output()
	if err != nil {
		return ""
	}
	out := strings.TrimSpace(string(rev))
	if status, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output(); err == nil && len(strings.TrimSpace(string(status))) > 0 {
		out += "-dirty"
	}
	return out
}

func exitCodeOf(err error) int {
	var coded *engine.CodedError
	if errors.As(err, &coded) {
		return int(coded.Code)
	}
	var stale *engine.ErrStalePlan
	if errors.As(err, &stale) {
		return int(engine.ExitStale)
	}
	if strings.Contains(err.Error(), "is locked by") || strings.Contains(err.Error(), "reserved by") {
		return int(engine.ExitLocked)
	}
	return int(engine.ExitError)
}

// ---------- pravo schema ----------

func schemaCmd() *cobra.Command {
	var required bool
	cmd := &cobra.Command{
		Use:   "schema <type> [<type>...]",
		Short: "Print the CUE definition of resource, data source, ephemeral or function types",
		Long: `Prints the generated CUE definition of one or more types, with the
provider's documentation as comments. The provider is inferred from the type
prefix (random_pet -> random) or given explicitly as provider/type. With
-required only the attributes that must be set are shown, which is the
shortest useful description for writing a specification. With --json the
definitions are returned as {"type": "...cue..."}.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession(cmd.Context(), sessionOptions{install: true})
			if err != nil {
				return err
			}
			defer s.close()
			results := map[string]string{}
			for _, arg := range args {
				local, typ := arg, arg
				if i := strings.IndexByte(arg, '/'); i >= 0 {
					local, typ = arg[:i], arg[i+1:]
				} else if i := strings.IndexByte(arg, '_'); i > 0 {
					local = arg[:i]
				}
				if _, ok := s.providers.Resolved[local]; !ok {
					return engine.WithCode(engine.ExitInvalid, fmt.Errorf("no provider %q for type %s; declare a provider struct for it, or write provider/type", local, typ))
				}
				ps, err := s.providers.Schema(cmd.Context(), local)
				if err != nil {
					return err
				}
				text, err := schemaText(ps, typ, required, s.providers.Resolved[local].Req.Source)
				if err != nil {
					return engine.WithCode(engine.ExitInvalid, err)
				}
				results[arg] = text
			}
			if flagJSON {
				return emitJSON(results)
			}
			for _, arg := range args {
				fmt.Print(results[arg])
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&required, "required", false, "show only required attributes")
	addTagFlags(cmd)
	return cmd
}

func schemaText(ps *schema.ProviderSchema, typ string, required bool, source registry.Source) (string, error) {
	opts := schema.GenOptions{Resources: []string{}, DataSources: []string{}, Ephemerals: []string{}, Functions: []string{}, Source: source.String()}
	var kind string
	switch {
	case ps.Resources[typ] != nil:
		opts.Resources, kind = []string{typ}, "resource"
	case ps.DataSources[typ] != nil:
		opts.DataSources, kind = []string{typ}, "data"
	case ps.Ephemerals[typ] != nil:
		opts.Ephemerals, kind = []string{typ}, "ephemeral"
	case ps.Functions[typ] != nil:
		opts.Functions, kind = []string{typ}, "call"
	default:
		return "", fmt.Errorf("the provider has no type %q", typ)
	}
	if required {
		filtered := *ps
		filtered.Resources = map[string]*schema.Schema{}
		filtered.DataSources = map[string]*schema.Schema{}
		filtered.Ephemerals = map[string]*schema.Schema{}
		keep := func(m map[string]*schema.Schema, name string) {
			if s := ps.Resources[name]; s != nil && kind == "resource" {
				m[name] = requiredOnly(s)
			}
		}
		keep(filtered.Resources, typ)
		if s := ps.DataSources[typ]; s != nil && kind == "data" {
			filtered.DataSources[typ] = requiredOnly(s)
		}
		if s := ps.Ephemerals[typ]; s != nil && kind == "ephemeral" {
			filtered.Ephemerals[typ] = requiredOnly(s)
		}
		ps = &filtered
	}
	src := schema.GenerateCUE(ps, "schema", opts)
	// Show only the definition of the requested type, dedented out of #Provider.
	text := string(src)
	def := map[string]string{"resource": "#Resource", "data": "#Data", "ephemeral": "#Ephemeral", "call": "#Function"}[kind]
	lines := strings.Split(text, "\n")
	var body []string
	in := false
	for _, l := range lines {
		switch {
		case !in && l == "\t"+def+": {":
			in = true
		case in && l == "\t}":
			in = false
		case in:
			body = append(body, strings.TrimPrefix(l, "\t\t"))
		}
	}
	if len(body) == 0 {
		return "", fmt.Errorf("could not render the definition of %s", typ)
	}
	importPath, _ := genPackage(source)
	text = fmt.Sprintf("// %s %s, from %s\n// Use it with:\n//\n//\timport <alias> %q\n//\n//\t<provider>: <alias>.#Provider\n//\t<name>:     <provider>.%s.%s & {...}\n//\n// The definition:\n%s\n", kind, typ, source.Display(), importPath, def, typ, strings.Join(body, "\n"))
	return text, nil
}

// requiredOnly keeps required attributes and blocks with required content.
func requiredOnly(s *schema.Schema) *schema.Schema {
	var trim func(b *schema.Block) *schema.Block
	trim = func(b *schema.Block) *schema.Block {
		out := &schema.Block{Description: b.Description}
		for _, a := range b.Attributes {
			if a.Required {
				out.Attributes = append(out.Attributes, a)
			}
		}
		for _, nb := range b.BlockTypes {
			inner := trim(nb.Block)
			if nb.MinItems > 0 || len(inner.Attributes) > 0 || len(inner.BlockTypes) > 0 {
				out.BlockTypes = append(out.BlockTypes, &schema.NestedBlock{TypeName: nb.TypeName, Block: inner, Nesting: nb.Nesting, MinItems: nb.MinItems, MaxItems: nb.MaxItems})
			}
		}
		return out
	}
	return &schema.Schema{Version: s.Version, Block: trim(s.Block)}
}

// ---------- pravo why ----------

func whyCmd() *cobra.Command {
	var refresh string
	cmd := &cobra.Command{
		Use:   "why <addr> [<addr>...]",
		Short: "Explain why a plan would change, skip or replace a resource",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := parseRefresh(refresh)
			if err != nil {
				return err
			}
			s, err := newSession(cmd.Context(), sessionOptions{install: true, schemas: true, loadState: true})
			if err != nil {
				return err
			}
			defer s.close()
			quiet := engine.Options{Refresh: mode, Progress: func(string, ...any) {}}
			eng := s.engine(quiet)
			plan, err := eng.Plan(cmd.Context())
			if err != nil {
				return err
			}
			view := eng.NewView(plan, engine.ViewOptions{Skipped: true, Full: true})
			byAddr := map[string]engine.ChangeView{}
			for _, c := range view.Changes {
				byAddr[c.Addr] = c
			}
			type explanation struct {
				Addr       string   `json:"addr"`
				Action     string   `json:"action"`
				Reasons    []string `json:"reasons"`
				Dependents []string `json:"dependents,omitempty"`
				Found      bool     `json:"found"`
			}
			var all []explanation
			for _, a := range args {
				c, ok := byAddr[a]
				ex := explanation{Addr: a, Found: ok}
				if ok {
					ex.Action, ex.Reasons, ex.Dependents = string(c.Action), c.Reasons, c.Dependents
					if len(ex.Reasons) == 0 {
						ex.Reasons = []string{"nothing to report"}
					}
				} else {
					ex.Reasons = []string{"not declared in the specification and not in the state"}
				}
				all = append(all, ex)
			}
			if flagJSON {
				return emitJSON(all)
			}
			for _, ex := range all {
				if !ex.Found {
					out.Printf("%s: %s\n", out.C(ui.Bold, ex.Addr), ex.Reasons[0])
					continue
				}
				out.Printf("%s: %s\n", out.C(ui.Bold, ex.Addr), ex.Action)
				for _, r := range ex.Reasons {
					out.Printf("  - %s\n", r)
				}
				if len(ex.Dependents) > 0 {
					out.Printf("  reaches: %s\n", strings.Join(ex.Dependents, ", "))
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&refresh, "refresh", "age", "when to read real state: age, expired, false or all")
	addTagFlags(cmd)
	return cmd
}

// ---------- pravo drift ----------

func driftCmd() *cobra.Command {
	var webhook string
	var due bool
	var limit int
	cmd := &cobra.Command{
		Use:   "drift",
		Short: "Refresh every resource and report the ones that changed outside pravo",
		Args:  noArgs,
		Long: `Reads the real state of every resource, ignoring refresh ages, and
reports drift: resources whose real state differs from the state, and
resources that no longer exist. Refreshed state is written back to the
state, with a mark on every record that differs or is gone, so the next
plan, saved plan and apply act on it. With -webhook the report is also
POSTed as JSON, which lets a scheduler hand drift to whoever triages it.
Exit code 2 means drift was found.

With --due, drift visits every record and reads only the ones that are older
than their refresh age, the oldest first, at most --limit of them. It plans
nothing, so a scheduled run costs what it reads.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit != 0 && !due {
				return fmt.Errorf("--limit applies to --due")
			}
			if limit < 0 {
				return fmt.Errorf("--limit %d: want a number above zero", limit)
			}
			s, err := newSession(cmd.Context(), sessionOptions{install: true, schemas: true, loadState: true})
			if err != nil {
				return err
			}
			defer s.close()
			if due {
				return driftDue(cmd, s, limit, webhook)
			}
			eng := s.engine(engine.Options{Refresh: engine.RefreshAll, Progress: func(string, ...any) {}})
			plan, err := eng.Plan(cmd.Context())
			if err != nil {
				return err
			}
			view := eng.NewView(plan, engine.ViewOptions{})
			type item struct {
				Addr    string   `json:"addr"`
				Action  string   `json:"action"`
				Reasons []string `json:"reasons"`
				Gone    bool     `json:"gone,omitempty"`
			}
			report := struct {
				Time    time.Time      `json:"time"`
				Root    string         `json:"root"`
				Actor   string         `json:"actor,omitempty"`
				Drifted []item         `json:"drifted"`
				Pending int            `json:"pending_changes"`
				Summary engine.Summary `json:"summary"`
			}{Time: time.Now().UTC(), Root: s.root.Dir, Actor: flagActor, Drifted: []item{}, Summary: view.Summary}
			for _, c := range view.Changes {
				if c.Drifted {
					gone := c.Action == engine.ActionCreate
					reasons := c.Reasons
					if reasons == nil {
						reasons = []string{}
					}
					report.Drifted = append(report.Drifted, item{Addr: c.Addr, Action: string(c.Action), Reasons: reasons, Gone: gone})
				}
				if c.Action != engine.ActionNoop && c.Action != engine.ActionRead && c.Action != engine.ActionOpen && c.Action != engine.ActionCall {
					report.Pending++
				}
			}
			sort.Slice(report.Drifted, func(i, j int) bool { return report.Drifted[i].Addr < report.Drifted[j].Addr })
			if webhook != "" {
				if err := postJSON(webhook, report); err != nil {
					return err
				}
			}
			if flagJSON {
				if err := emitJSON(report); err != nil {
					return err
				}
			} else if len(report.Drifted) == 0 {
				out.Println(out.C(ui.Bold+ui.Green, "No drift.") + fmt.Sprintf(" %d resources refreshed; %d pending changes from the specification.", view.Stats.Refreshed, report.Pending))
			} else {
				out.Printf("%s %d of %d resources changed outside pravo:\n", out.C(ui.Bold+ui.Yellow, "Drift."), len(report.Drifted), view.Stats.Refreshed)
				for _, d := range report.Drifted {
					out.Printf("  %s: %s\n", out.C(ui.Bold, d.Addr), d.Action)
					for _, r := range d.Reasons {
						out.Printf("    - %s\n", r)
					}
				}
			}
			if len(report.Drifted) > 0 {
				return engine.WithCode(engine.ExitChanges, errSilent)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&webhook, "webhook", "", "POST the drift report as JSON to this URL")
	cmd.Flags().BoolVar(&due, "due", false, "read only the records that are older than their refresh age, oldest first, and plan nothing")
	cmd.Flags().IntVar(&limit, "limit", 0, "with --due: read at most this many records")
	addTagFlags(cmd)
	return cmd
}

// driftDue is "pravo drift --due": the records past their refresh age are read
// and the report lists what differs.
func driftDue(cmd *cobra.Command, s *session, limit int, webhook string) error {
	eng := s.engine(engine.Options{Progress: func(string, ...any) {}})
	rep, err := eng.DriftDue(cmd.Context(), limit)
	if err != nil {
		return err
	}
	report := struct {
		Time  time.Time `json:"time"`
		Root  string    `json:"root"`
		Actor string    `json:"actor,omitempty"`
		*engine.DueReport
	}{Time: time.Now().UTC(), Root: s.root.Dir, Actor: flagActor, DueReport: rep}
	if webhook != "" {
		if err := postJSON(webhook, report); err != nil {
			return err
		}
	}
	switch {
	case flagJSON:
		if err := emitJSON(report); err != nil {
			return err
		}
	case len(rep.Drifted) == 0:
		out.Println(out.C(ui.Bold+ui.Green, "No drift.") + " " + dueSummary(rep))
	default:
		out.Printf("%s %d of %d %s changed outside pravo. %s\n", out.C(ui.Bold+ui.Yellow, "Drift."), len(rep.Drifted), len(rep.Read), recordNoun(len(rep.Read)), dueSummary(rep))
		for _, d := range rep.Drifted {
			what := "the real state differs from the state"
			if d.Gone {
				what = "the object does not exist"
			}
			out.Printf("  %s: %s\n", out.C(ui.Bold, d.Addr), what)
		}
	}
	if len(rep.Drifted) > 0 {
		return engine.WithCode(engine.ExitChanges, errSilent)
	}
	return nil
}

// recordNoun is "record" for one record and "records" for any other number.
func recordNoun(n int) string {
	if n == 1 {
		return "record"
	}
	return "records"
}

// dueSummary says how many of the records a drift --due run read.
func dueSummary(rep *engine.DueReport) string {
	text := fmt.Sprintf("%d of %d %s read", len(rep.Read), rep.Records, recordNoun(rep.Records))
	switch left := rep.Due - len(rep.Read); {
	case left == 1:
		text += "; 1 more is older than its refresh age"
	case left > 1:
		text += fmt.Sprintf("; %d more are older than their refresh age", left)
	}
	return text + "."
}

// errSilent signals a non-zero exit without a message.
var errSilent = errors.New("")
