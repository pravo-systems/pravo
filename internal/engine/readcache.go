package engine

import "github.com/hashicorp/terraform-plugin-go/tftypes"

// readResult is what the provider reported when a plan read a record.
type readResult struct {
	// state is null when the object does not exist.
	state   tftypes.Value
	private []byte
	// drifted reports that the object differs from the record.
	drifted bool
	// forced is why the read was made whatever the refresh age, or "" when the
	// age asked for it.
	forced string
}

func (e *Engine) cachedRead(addr string) *readResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reads[addr]
}

func (e *Engine) rememberRead(addr string, rd *readResult) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.reads == nil {
		e.reads = map[string]*readResult{}
	}
	e.reads[addr] = rd
}

// dataRead is what the provider returned when a plan read a data source.
type dataRead struct {
	// hash is the digest of the configuration that was read.
	hash  string
	state tftypes.Value
}

// cachedData returns what the plan read of the data source when its
// configuration had the hash.
func (e *Engine) cachedData(addr, hash string) (tftypes.Value, bool) {
	if hash == "" {
		return tftypes.Value{}, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	rd, ok := e.dataReads[addr]
	return rd.state, ok && rd.hash == hash
}

func (e *Engine) rememberData(addr, hash string, st tftypes.Value) {
	if hash == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dataReads == nil {
		e.dataReads = map[string]dataRead{}
	}
	e.dataReads[addr] = dataRead{hash: hash, state: st}
}

// forgetReads starts a plan with nothing read and nothing imported.
func (e *Engine) forgetReads() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.reads, e.dataReads, e.imported = nil, nil, nil
}
