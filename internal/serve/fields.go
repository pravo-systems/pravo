package serve

import (
	"encoding/json"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// fieldRow is one searchable value of a record: the dotted path of the
// attribute and its value, both lowered.
type fieldRow struct{ path, value string }

// fieldIndex holds the values of the records that the viewer would send, for
// /api/fields. It is built once for a model, the first time a search asks.
type fieldIndex struct {
	rows     map[string][]fieldRow
	indexed  int
	withheld int
}

// maxFieldValue bounds the text of a value that is searched.
const maxFieldValue = 512

// fields returns the index of the model, building it on the first call. It reads
// what /api/detail would send for each node: the same walk, with the same
// secrets, so a value that the detail withholds is not here.
func (m *model) fields() *fieldIndex {
	m.fieldsOnce.Do(func() {
		idx := &fieldIndex{rows: make(map[string][]fieldRow)}
		type job struct {
			addr string
			mn   *mnode
			rows []fieldRow
			ok   bool
		}
		var jobs []*job
		for _, addr := range m.order {
			mn := m.nodes[addr]
			switch {
			case mn.isOutput():
				// an output reads the specification, which one goroutine at a time may
				var rows []fieldRow
				attrs := m.outputDetail(mn, m.plan).Attrs
				for i := range attrs {
					rows = flattenAttr(rows, attrs[i].Name, &attrs[i])
				}
				idx.indexed++
				if len(rows) > 0 {
					idx.rows[addr] = rows
				}
			case mn.rec == nil:
				// not recorded: nothing to search
			case mn.Sealed || mn.sch == nil:
				idx.withheld++
			default:
				jobs = append(jobs, &job{addr: addr, mn: mn})
			}
		}
		// The records are independent, and the walk only reads the secrets.
		var wg sync.WaitGroup
		next := make(chan *job)
		for range max(1, min(runtime.GOMAXPROCS(0), len(jobs))) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range next {
					rec, err := decodeAttrs(j.mn.rec.Attributes)
					if err != nil {
						continue
					}
					w := m.judge(m.plan, j.addr).walk(true)
					attrs := w.block(j.mn.sch.Block, rec, 0)
					for i := range attrs {
						j.rows = flattenAttr(j.rows, attrs[i].Name, &attrs[i])
					}
					j.ok = true
				}
			}()
		}
		for _, j := range jobs {
			next <- j
		}
		close(next)
		wg.Wait()
		for _, j := range jobs {
			if !j.ok {
				idx.withheld++
				continue
			}
			idx.indexed++
			if len(j.rows) > 0 {
				idx.rows[j.addr] = j.rows
			}
		}
		m.fieldIdx = idx
	})
	return m.fieldIdx
}

// flattenAttr appends the values that an attribute sends, each at its dotted
// path. A value that is withheld, unknown or null is not searchable.
func flattenAttr(rows []fieldRow, path string, a *Attr) []fieldRow {
	if a.Redacted != "" || a.Unknown || a.Null {
		return rows
	}
	if len(a.Children) > 0 {
		for i := range a.Children {
			rows = flattenAttr(rows, path+"."+a.Children[i].Name, &a.Children[i])
		}
		return rows
	}
	var v string
	switch x := a.Value.(type) {
	case nil:
		return rows
	case string:
		v = x
	case json.Number:
		v = x.String()
	case bool:
		v = strconv.FormatBool(x)
	case float64:
		v = strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return rows
	}
	if len(v) > maxFieldValue {
		v = v[:maxFieldValue]
	}
	return append(rows, fieldRow{path: strings.ToLower(path), value: strings.ToLower(v)})
}

// fieldMatches answers /api/fields: the nodes with a value at the path that the
// spec names.
func (m *model) fieldMatches(spec string) *Fields {
	idx := m.fields()
	res := &Fields{Version: APIVersion, Addrs: []string{}, Indexed: idx.indexed, Withheld: idx.withheld}
	path, value, hasValue := strings.Cut(strings.ToLower(strings.TrimSpace(spec)), "=")
	path = strings.TrimSpace(path)
	if path == "" {
		return res
	}
	for _, addr := range m.order {
		for _, r := range idx.rows[addr] {
			if !pathMatches(path, r.path) {
				continue
			}
			if hasValue && !glob(value, r.value) {
				continue
			}
			res.Addrs = append(res.Addrs, addr)
			break
		}
	}
	return res
}

// pathMatches tells whether the attribute path is, or lies below, the one that
// pattern names, so that "zones=eu-1" finds a list that holds eu-1. A pattern
// without a dot or a star names a field of that name at any depth.
func pathMatches(pattern, path string) bool {
	bare := !strings.ContainsAny(pattern, ".*")
	for {
		if bare && (path == pattern || strings.HasSuffix(path, "."+pattern)) || !bare && glob(pattern, path) {
			return true
		}
		i := strings.LastIndexByte(path, '.')
		if i < 0 {
			return false
		}
		path = path[:i]
	}
}

// glob matches s against a pattern in which * stands for any run of characters.
func glob(pattern, s string) bool {
	if !strings.Contains(pattern, "*") {
		return pattern == s
	}
	parts := strings.Split(pattern, "*")
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(s, p)
		if i < 0 {
			return false
		}
		s = s[i+len(p):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}
