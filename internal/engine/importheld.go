package engine

import (
	"encoding/json"
	"fmt"

	"github.com/pravo-systems/pravo/internal/state"
)

// HeldBy returns the address of another record of the state that holds the object the
// record n was read from, or "". An object is held when a record of the same provider and
// type has the same id and every attribute that the read returns (a value that is not null
// or empty) equal: the id alone does not decide, because some providers give two objects one
// id (local_file's id is a hash of the content, and two files of one content are two objects
// with different filenames). Attributes only the record has, such as arguments a read does
// not return, are ignored. A record whose object changed in the cloud since it was written
// is not recognised. Two nodes that manage one object would each destroy it.
func HeldBy(st *state.Store, n *state.Resource) string {
	id := idAttr(n.Attributes)
	if id == "" {
		return ""
	}
	for _, o := range st.List() {
		if o.Address() == n.Address() || o.Mode != "managed" || o.ProviderKey != n.ProviderKey || o.Type != n.Type {
			continue
		}
		if idAttr(o.Attributes) == id && readWithin(n.Attributes, o.Attributes) {
			return o.Address()
		}
	}
	return ""
}

func idAttr(attrs []byte) string {
	var m struct {
		ID *string `json:"id"`
	}
	if json.Unmarshal(attrs, &m) != nil || m.ID == nil {
		return ""
	}
	return *m.ID
}

// ErrHeld is the refusal of an import of an object that another record holds.
func ErrHeld(addr, label, holder string) error {
	return fmt.Errorf("%s: %q is already managed as %s; one object cannot have two records", addr, label, holder)
}

// readWithin reports whether every attribute of read that is not null or empty is the same in held.
func readWithin(read, held []byte) bool {
	var r, h map[string]any
	if json.Unmarshal(read, &r) != nil || json.Unmarshal(held, &h) != nil {
		return false
	}
	for k, v := range r {
		if v == "" || emptyAsNull(v) == nil {
			continue
		}
		a, _ := json.Marshal(emptyAsNull(v))
		b, _ := json.Marshal(emptyAsNull(h[k]))
		if string(a) != string(b) {
			return false
		}
	}
	return true
}
