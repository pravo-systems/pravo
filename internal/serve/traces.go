package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/pravo-systems/pravo/internal/telemetry"
)

// The runs that pravo recorded (see telemetry.RunTrace) are read from a
// directory and shown as they are. The server reads them and writes none.
const (
	// maxTraceFile is the largest file that is read.
	maxTraceFile = 64 << 20
	// maxTraceList bounds the list.
	maxTraceList = 200
	// maxTraceSpans bounds the spans of one answer.
	maxTraceSpans  = 30000
	maxTraceAttrs  = 64
	maxTraceEvents = 200
	maxTraceList2  = 50 // elements of a list attribute
	traceTextMax   = 2048
)

var traceIDRE = regexp.MustCompile(`^[0-9a-f]{32}-[0-9a-f]{16}$`)

// traceDir is where the recorded runs are.
func (s *Server) traceDir() string {
	if s.opts.TraceDir != "" {
		return s.opts.TraceDir
	}
	return filepath.Join(s.opts.Dir, ".pravo", "traces")
}

// traceCache keeps the summary of each file between requests, by name, size and time.
type traceCache struct {
	mu      sync.Mutex
	entries map[string]traceEntry
}

type traceEntry struct {
	size int64
	mod  int64
	info TraceInfo
	ok   bool
}

// traceSecrets are the secrets that texts of a run are judged against: the state's, when the project loads.
func (s *Server) traceSecrets() *secretSet {
	if m, err := s.current(); err == nil && m.secrets != nil {
		return m.secrets
	}
	return newSecretSet()
}

func (s *Server) traces(w http.ResponseWriter, r *http.Request) {
	dir := s.traceDir()
	sec := s.traceSecrets()
	list := &TraceList{Version: APIVersion, Dir: displayTraceDir(s.opts.Dir, dir), Traces: []TraceInfo{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, http.StatusOK, list)
		return
	}
	type file struct {
		id   string
		size int64
		mod  int64
	}
	var files []file
	for _, e := range entries {
		id := strings.TrimSuffix(e.Name(), ".json")
		if id == e.Name() || !traceIDRE.MatchString(id) {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, file{id, info.Size(), info.ModTime().UnixNano()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod > files[j].mod })
	if len(files) > maxTraceList {
		list.Omitted = len(files) - maxTraceList
		files = files[:maxTraceList]
	}
	s.traceMu.Lock()
	if s.traceSums.entries == nil {
		s.traceSums.entries = map[string]traceEntry{}
	}
	cache := s.traceSums.entries
	s.traceMu.Unlock()
	keep := map[string]bool{}
	for _, f := range files {
		keep[f.id] = true
		s.traceMu.Lock()
		e, hit := cache[f.id]
		s.traceMu.Unlock()
		if !hit || e.size != f.size || e.mod != f.mod {
			e = traceEntry{size: f.size, mod: f.mod}
			if rt, err := readTrace(dir, f.id); err == nil {
				e.info, e.ok = summarize(f.id, rt), true
			}
			s.traceMu.Lock()
			cache[f.id] = e
			s.traceMu.Unlock()
		}
		if !e.ok {
			list.Unreadable++
			continue
		}
		list.Traces = append(list.Traces, e.info)
	}
	s.traceMu.Lock()
	for id := range cache {
		if !keep[id] {
			delete(cache, id)
		}
	}
	s.traceMu.Unlock()
	// The texts a person wrote are masked against the secrets of the state, which can change between requests: not cached.
	for i := range list.Traces {
		t := &list.Traces[i]
		t.Reason = traceText(sec, t.Reason, 256)
		t.Actor = traceText(sec, t.Actor, 128)
		t.Command = traceText(sec, t.Command, 128)
	}
	sort.SliceStable(list.Traces, func(i, j int) bool { return list.Traces[i].Start > list.Traces[j].Start })
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) trace(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !traceIDRE.MatchString(id) {
		writeError(w, http.StatusBadRequest, "id is not the id of a run")
		return
	}
	rt, err := readTrace(s.traceDir(), id)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "no such run")
		} else {
			writeError(w, http.StatusUnprocessableEntity, "the run cannot be read: "+traceText(newSecretSet(), err.Error(), 200))
		}
		return
	}
	sec := s.traceSecrets()
	info := summarize(id, rt)
	info.Reason, info.Actor, info.Command = traceText(sec, info.Reason, 256), traceText(sec, info.Actor, 128), traceText(sec, info.Command, 128)
	out := &Trace{Version: APIVersion, TraceInfo: info, TraceID: rt.TraceID, Dropped: rt.Dropped, Spans: make([]TraceSpan, 0, len(rt.Spans))}
	for _, sp := range rt.Spans {
		if len(out.Spans) >= maxTraceSpans {
			out.Omitted++
			continue
		}
		ts := TraceSpan{
			ID: sp.ID, Parent: sp.Parent, Name: traceText(sec, sp.Name, 256), Kind: spanKind(sp.Name), Status: sp.Status,
			Message: traceText(sec, sp.Message, traceTextMax), StartUS: sp.StartUS, DurUS: sp.DurUS, Attrs: traceAttrs(sec, sp.Attrs),
		}
		for i, ev := range sp.Events {
			if i >= maxTraceEvents {
				break
			}
			ts.Events = append(ts.Events, TraceEvent{Name: traceText(sec, ev.Name, 256), AtUS: ev.AtUS, Attrs: traceAttrs(sec, ev.Attrs)})
		}
		out.Spans = append(out.Spans, ts)
	}
	writeJSON(w, http.StatusOK, out)
}

// readTrace reads the file of a run. The id has the form of one and is never a path.
func readTrace(dir, id string) (*telemetry.RunTrace, error) {
	name := filepath.Join(dir, id+".json")
	fi, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("not a file")
	}
	if fi.Size() > maxTraceFile {
		return nil, fmt.Errorf("the file is %d MiB, over the limit of %d", fi.Size()>>20, maxTraceFile>>20)
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	var rt telemetry.RunTrace
	if err := json.Unmarshal(data, &rt); err != nil {
		return nil, fmt.Errorf("not a recorded run")
	}
	if rt.Version != telemetry.TraceFormat || len(rt.Spans) == 0 {
		return nil, fmt.Errorf("not a recorded run of this format")
	}
	return &rt, nil
}

// summarize reads the root span and the engine spans of a run.
func summarize(id string, rt *telemetry.RunTrace) TraceInfo {
	info := TraceInfo{ID: id, Start: rt.Start, DurationUS: rt.DurationUS, Exit: -1, Status: "ok", SpanCount: len(rt.Spans)}
	have := map[string]bool{}
	for _, sp := range rt.Spans {
		have[sp.ID] = true
	}
	resources := map[string]bool{}
	rooted := false
	for _, sp := range rt.Spans {
		if sp.Status == "error" {
			info.Errors++
		}
		if !rooted && (sp.Parent == "" || !have[sp.Parent]) {
			rooted = true
			info.Command = sp.Name
			if c, ok := sp.Attrs["pravo.command"].(string); ok && c != "" {
				info.Command = c
			}
			if n, ok := sp.Attrs["pravo.exit_code"].(float64); ok {
				info.Exit = int(n)
			}
			info.Actor, _ = sp.Attrs["pravo.actor"].(string)
			info.Reason, _ = sp.Attrs["pravo.reason"].(string)
		}
		switch spanKind(sp.Name) {
		case "resource":
			if a, ok := sp.Attrs["pravo.resource.address"].(string); ok {
				resources[a] = true
			}
		case "engine":
			// a plan has the id its transaction would have, and writes none: only an apply names one
			if t, ok := sp.Attrs["pravo.txn"].(string); ok && t != "" && info.Txn == "" && sp.Name == "engine apply" {
				info.Txn = t
			}
		}
	}
	info.Resources = len(resources)
	if info.Errors > 0 {
		info.Status = "error"
	}
	return info
}

func spanKind(name string) string {
	switch {
	case strings.HasPrefix(name, "pravo "):
		return "command"
	case strings.HasPrefix(name, "engine "):
		return "engine"
	case strings.HasPrefix(name, "resource "):
		return "resource"
	case strings.HasPrefix(name, "provider "):
		return "provider"
	case strings.HasPrefix(name, "state "):
		return "state"
	case strings.HasPrefix(name, "phase "):
		return "phase"
	case strings.HasPrefix(name, "setup "), strings.HasPrefix(name, "config "):
		return "setup"
	case strings.HasPrefix(name, "mcp "):
		return "mcp"
	}
	return "other"
}

// traceText bounds, cleans and masks a text of a run.
func traceText(sec *secretSet, s string, n int) string {
	s = strings.ToValidUTF8(s, "?")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return -1
		}
		return r
	}, s)
	return maskText(sec, s, n)
}

// traceAttrs keeps the scalars of a span's attributes, and short lists of them, masked.
func traceAttrs(sec *secretSet, in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := map[string]any{}
	for _, k := range keys {
		if len(out) >= maxTraceAttrs {
			break
		}
		switch v := in[k].(type) {
		case string:
			out[traceText(sec, k, 128)] = traceText(sec, v, traceTextMax)
		case bool, float64:
			out[traceText(sec, k, 128)] = v
		case []any:
			var l []any
			for i, x := range v {
				if i >= maxTraceList2 {
					break
				}
				switch e := x.(type) {
				case string:
					l = append(l, traceText(sec, e, 256))
				case bool, float64:
					l = append(l, e)
				}
			}
			out[traceText(sec, k, 128)] = l
		}
	}
	return out
}

// displayTraceDir shows the directory relative to the project when it is inside it.
func displayTraceDir(project, dir string) string {
	if rel, err := filepath.Rel(project, dir); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return dir
}
