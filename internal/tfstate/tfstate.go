// Package tfstate converts OpenTofu or legacy Terraform state files (format version 4) into
// pravo's state model.
//
// The .tfstate format records one entry per resource with a list of instances (one per
// count index or for_each key); pravo records one entry per instance and
// has no instance keys, so keyed instances are renamed to <name>_<key>. A
// resource in a module takes the module path, with its instance keys, as a
// prefix. The conversion is lossy in the other direction: tfstate-only
// concepts such as taint, deposed objects and check results have no pravo
// equivalent and are reported in Result.Notes. Records of the provider that
// OpenTofu builds in (terraform_remote_state, terraform_data) are skipped and
// reported in the same way.
//
// A state records the dependencies of an instance as configuration addresses
// without instance keys, for example module.service.random_pet.host. The
// conversion expands each one to the records it names: the instances of the
// same module instance as the dependent, or every instance when the
// dependent sits elsewhere.
package tfstate

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"pravo.dev/pravo/internal/engine"
	"pravo.dev/pravo/internal/registry"
	"pravo.dev/pravo/internal/state"
)

// Rename records an instance whose pravo address differs from its
// tfstate address.
type Rename struct {
	From string // tfstate address, e.g. aws_s3_bucket.b["logs"]
	To   string // pravo address, e.g. aws_s3_bucket.b_logs
}

// Result is a converted .tfstate file.
type Result struct {
	Resources []*state.Resource
	// Outputs holds the output values as pravo records them: an output that
	// the source state marks sensitive is wrapped as engine.WrapOutput does.
	Outputs map[string]json.RawMessage
	// Renamed lists every instance whose name changed, in address order.
	Renamed []Rename
	// Notes lists anything that was dropped or approximated.
	Notes []string
}

// file mirrors the parts of the tfstate format version 4 that are
// needed for the conversion.
type file struct {
	Version   int               `json:"version"`
	Outputs   map[string]output `json:"outputs"`
	Resources []resource        `json:"resources"`
}

type output struct {
	Value     json.RawMessage `json:"value"`
	Sensitive bool            `json:"sensitive"`
}

type resource struct {
	Module    string     `json:"module"`
	Mode      string     `json:"mode"`
	Type      string     `json:"type"`
	Name      string     `json:"name"`
	Provider  string     `json:"provider"`
	Instances []instance `json:"instances"`
}

type instance struct {
	IndexKey       json.RawMessage `json:"index_key"`
	Status         string          `json:"status"`
	Deposed        string          `json:"deposed"`
	SchemaVersion  int64           `json:"schema_version"`
	Attributes     json.RawMessage `json:"attributes"`
	AttributesFlat json.RawMessage `json:"attributes_flat"`
	Private        string          `json:"private"`
	Dependencies   []string        `json:"dependencies"`
}

// Convert parses a .tfstate file and converts every managed resource
// and data source instance to a pravo state resource.
func Convert(data []byte) (*Result, error) {
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing tfstate: %w", err)
	}
	if f.Version != 4 {
		return nil, fmt.Errorf("unsupported state format version %d: pravo reads format version 4, which OpenTofu and Terraform 0.12 or newer write", f.Version)
	}

	res := &Result{Outputs: map[string]json.RawMessage{}}
	for name, o := range f.Outputs {
		if len(o.Value) == 0 {
			continue
		}
		res.Outputs[name] = engine.WrapOutput(o.Value, o.Sensitive)
	}

	// First pass: decide the pravo address of every instance, so that
	// dependencies on a keyed resource can be expanded to all its instances.
	var all []*pending
	byConfig := map[string][]*pending{} // keyless configuration address -> instances
	seen := map[string]string{}         // pravo address -> tfstate address
	skipped := map[string]bool{}        // keyless configuration addresses left out of the conversion
	for _, r := range f.Resources {
		if r.Mode != "managed" && r.Mode != "data" {
			res.Notes = append(res.Notes, fmt.Sprintf("%s: unknown mode %q, skipped", configAddress(r), r.Mode))
			continue
		}
		source, alias, err := parseProvider(r.Provider)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", configAddress(r), err)
		}
		steps, rest, err := splitModulePrefix(r.Module)
		if err != nil || rest != "" {
			return nil, fmt.Errorf("%s: unrecognised module address %q", configAddress(r), r.Module)
		}
		key := keylessAddress(steps, state.Address(r.Mode, r.Type, r.Name))
		if isBuiltin(source) {
			res.Notes = append(res.Notes, fmt.Sprintf("%s: skipped, provider %s is built into OpenTofu and pravo cannot install it (%s)", configAddress(r), source, builtinReplacement(r)))
			skipped[key] = true
			continue
		}
		providerKey := typePrefix(r.Type)
		if alias != "" {
			providerKey += "." + alias
		}
		for _, inst := range r.Instances {
			tfAddr := instanceAddress(r, inst.IndexKey)
			if inst.Deposed != "" {
				res.Notes = append(res.Notes, fmt.Sprintf("%s: deposed object %s skipped (pravo keeps one object per resource; destroy it with tofu first)", tfAddr, inst.Deposed))
				continue
			}
			name, renamed := pravoName(r.Name, steps, inst.IndexKey)
			sr := &state.Resource{
				Mode:          r.Mode,
				Type:          r.Type,
				Name:          name,
				Provider:      source,
				ProviderKey:   providerKey,
				SchemaVersion: inst.SchemaVersion,
			}
			addr := sr.Address()
			if prev, ok := seen[addr]; ok {
				return nil, fmt.Errorf("%s and %s would both become %s; rename one of them in OpenTofu first", prev, tfAddr, addr)
			}
			seen[addr] = tfAddr
			if renamed {
				res.Renamed = append(res.Renamed, Rename{From: tfAddr, To: addr})
			}
			if inst.Status == "tainted" {
				res.Notes = append(res.Notes, fmt.Sprintf("%s: tainted in the source state; pravo has no taint, the object is recorded as healthy", tfAddr))
			}
			p := &pending{r: sr, inst: inst, module: steps, tfAddr: tfAddr}
			byConfig[key] = append(byConfig[key], p)
			all = append(all, p)
		}
	}

	// Second pass: attributes, private data and dependencies.
	for _, p := range all {
		sr, inst := p.r, p.inst
		switch {
		case len(inst.Attributes) > 0 && string(inst.Attributes) != "null":
			sr.Attributes = inst.Attributes
		case len(inst.AttributesFlat) > 0:
			return nil, fmt.Errorf("%s: legacy flatmap attributes are not supported; OpenTofu cannot read them either, so refresh or apply the state with an older provider version first", p.tfAddr)
		default:
			sr.Attributes = json.RawMessage("{}")
		}
		if inst.Private != "" {
			b, err := base64.StdEncoding.DecodeString(inst.Private)
			if err != nil {
				return nil, fmt.Errorf("%s: decoding private data: %w", p.tfAddr, err)
			}
			sr.Private = b
		}
		var deps []string
		for _, d := range inst.Dependencies {
			for _, t := range resolveDependency(d, p, byConfig, all) {
				if t != sr.Address() {
					deps = append(deps, t)
				}
			}
		}
		sort.Strings(deps)
		sr.Dependencies = dedupe(deps)
	}

	sort.Slice(all, func(i, j int) bool { return all[i].r.Address() < all[j].r.Address() })
	for _, p := range all {
		res.Resources = append(res.Resources, p.r)
	}
	sort.Slice(res.Renamed, func(i, j int) bool { return res.Renamed[i].To < res.Renamed[j].To })
	return res, nil
}

// pending is an instance whose pravo address is decided and whose
// attributes and dependencies are still to be read.
type pending struct {
	r      *state.Resource
	inst   instance
	module []modStep // the module instance the resource sits in
	tfAddr string
}

// resolveDependency returns the pravo addresses that a dependency of dependent
// names. A state writes a dependency as a configuration address without
// instance keys, so one address can name several records: every instance of a
// counted resource, and the resource of every instance of a module. A
// dependency inside a module instance names the record of that same instance
// when there is one. A dependency on a record that is not in the converted
// state, such as a skipped record of the built-in provider, names nothing.
func resolveDependency(dep string, dependent *pending, byConfig map[string][]*pending, all []*pending) []string {
	steps, rest, err := splitModulePrefix(dep)
	if err != nil {
		return nil
	}
	var candidates []*pending
	if rest == "" {
		// A dependency on a whole module: every record inside it.
		for _, c := range all {
			if hasModulePrefix(c.module, steps) {
				candidates = append(candidates, c)
			}
		}
	} else {
		candidates = byConfig[keylessAddress(steps, rest)]
	}
	var out []string
	for _, c := range candidates {
		if sameModuleInstance(dependent.module, c.module) && matchesKeys(steps, c.module) {
			out = append(out, c.r.Address())
		}
	}
	return out
}

// matchesKeys reports whether a module path has the instance keys that a
// dependency names. OpenTofu writes a dependency without keys, so this holds
// for every candidate; a state that names a key keeps only that instance.
func matchesKeys(want, path []modStep) bool {
	for i, w := range want {
		if w.keyed && i < len(path) && path[i].keyed && path[i].key != w.key {
			return false
		}
	}
	return true
}

// sameModuleInstance reports whether two module instances can belong
// together: wherever both paths pass through the same module call, they pass
// through the same instance of it.
func sameModuleInstance(a, b []modStep) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i].name != b[i].name {
			return true
		}
		if a[i].keyed && b[i].keyed && a[i].key != b[i].key {
			return false
		}
	}
	return true
}

// hasModulePrefix reports whether the module path path starts with the calls
// of prefix. Instance keys are not compared: a state names a module without
// them.
func hasModulePrefix(path, prefix []modStep) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i := range prefix {
		if path[i].name != prefix[i].name {
			return false
		}
	}
	return true
}

// parseProvider strips the provider["..."] wrapper from a tfstate provider
// configuration address and returns the bare source and the alias, if any.
//
//	provider["registry.terraform.io/hashicorp/aws"]       -> aws source, ""
//	provider["registry.terraform.io/hashicorp/aws"].west  -> aws source, "west"
//	module.x.provider["..."]                               -> the same
func parseProvider(addr string) (source, alias string, err error) {
	i := strings.Index(addr, `provider["`)
	if i < 0 {
		return "", "", fmt.Errorf("unrecognised provider address %q", addr)
	}
	rest := addr[i+len(`provider["`):]
	j := strings.Index(rest, `"]`)
	if j < 0 {
		return "", "", fmt.Errorf("unrecognised provider address %q", addr)
	}
	source = rest[:j]
	// A Terraform-era state names the legacy registry; the same providers are
	// served by the OpenTofu registry, which is where pravo gets them.
	source = strings.Replace(source, "registry.terraform.io/", registry.DefaultHost+"/", 1)
	if source == "" {
		return "", "", fmt.Errorf("unrecognised provider address %q", addr)
	}
	if tail := rest[j+2:]; tail != "" {
		if !strings.HasPrefix(tail, ".") {
			return "", "", fmt.Errorf("unrecognised provider address %q", addr)
		}
		alias = tail[1:]
	}
	return source, alias, nil
}

// isBuiltin reports whether a provider source names the provider that
// OpenTofu builds in, terraform.io/builtin/terraform. It has no registry
// entry, so pravo cannot install it.
func isBuiltin(source string) bool {
	parts := strings.Split(source, "/")
	return len(parts) == 3 && parts[1] == "builtin"
}

// builtinReplacement says what stands in for a record of a built-in provider.
func builtinReplacement(r resource) string {
	switch {
	case r.Mode == "data" && r.Type == "terraform_remote_state":
		return "read the outputs of another root with pravo.#RemoteOutputs"
	case r.Mode == "managed" && r.Type == "terraform_data":
		return "declare a null_resource of hashicorp/null instead"
	}
	return "pravo has no counterpart"
}

// typePrefix returns the provider local name pravo infers from a resource
// type: "aws" for "aws_instance".
func typePrefix(typ string) string {
	if i := strings.IndexByte(typ, '_'); i > 0 {
		return typ[:i]
	}
	return typ
}

// modStep is one module call in a module instance path.
type modStep struct {
	name  string
	key   string // the instance key, unquoted
	keyed bool   // the call has an instance key (count or for_each)
}

// splitModulePrefix reads the module calls at the start of a tfstate address
// and returns them with the rest of the address. The address of a module
// instance is module.a["k"].module.b[1]; a configuration address inside it
// continues with the resource, as in module.a.module.b.random_pet.p. The rest
// is empty when the address names a module only.
func splitModulePrefix(addr string) (steps []modStep, rest string, err error) {
	i := 0
	for strings.HasPrefix(addr[i:], "module.") {
		i += len("module.")
		j := i
		for j < len(addr) && addr[j] != '.' && addr[j] != '[' {
			j++
		}
		if j == i {
			return nil, "", fmt.Errorf("empty module name in %q", addr)
		}
		st := modStep{name: addr[i:j]}
		i = j
		if i < len(addr) && addr[i] == '[' {
			key, n, err := parseModuleKey(addr[i:])
			if err != nil {
				return nil, "", fmt.Errorf("%w in %q", err, addr)
			}
			st.key, st.keyed = key, true
			i += n
		}
		steps = append(steps, st)
		if i == len(addr) {
			break
		}
		if addr[i] != '.' {
			return nil, "", fmt.Errorf("unexpected %q after a module in %q", addr[i:], addr)
		}
		i++
	}
	return steps, addr[i:], nil
}

// parseModuleKey reads an instance key such as ["eu.west"] or [1] from the
// start of s and returns the key and the number of bytes it took. A string key
// is quoted as Go quotes it and may hold dots, brackets and escapes.
func parseModuleKey(s string) (key string, n int, err error) {
	if len(s) < 3 || s[0] != '[' {
		return "", 0, fmt.Errorf("unrecognised instance key")
	}
	if s[1] != '"' {
		end := strings.IndexByte(s, ']')
		if end < 0 {
			return "", 0, fmt.Errorf("unterminated instance key")
		}
		return s[1:end], end + 1, nil
	}
	i := 2
	for i < len(s) && s[i] != '"' {
		if s[i] == '\\' {
			i++
		}
		i++
	}
	if i+1 >= len(s) || s[i+1] != ']' {
		return "", 0, fmt.Errorf("unterminated instance key")
	}
	key, err = strconv.Unquote(s[1 : i+1])
	if err != nil {
		return "", 0, fmt.Errorf("unrecognised instance key %s", s[1:i+1])
	}
	return key, i + 2, nil
}

// keylessAddress joins module calls, without their instance keys, to a
// resource address: module.a.module.b.random_pet.p.
func keylessAddress(steps []modStep, resource string) string {
	var b strings.Builder
	for _, st := range steps {
		b.WriteString("module." + st.name + ".")
	}
	b.WriteString(resource)
	return b.String()
}

// configAddress is the tfstate address of a resource without an instance
// key, as used in messages: module.x.data.local_file.f.
func configAddress(r resource) string {
	addr := state.Address(r.Mode, r.Type, r.Name)
	if r.Module != "" {
		addr = r.Module + "." + addr
	}
	return addr
}

// instanceAddress is the tfstate address of one instance.
func instanceAddress(r resource, key json.RawMessage) string {
	addr := configAddress(r)
	if k, ok := indexKey(key); ok {
		addr += "[" + k + "]"
	}
	return addr
}

// indexKey formats an index_key as tofu prints it: 0 or "key".
func indexKey(key json.RawMessage) (string, bool) {
	if len(key) == 0 || string(key) == "null" {
		return "", false
	}
	return string(key), true
}

// indexKeyText returns the text of an index_key without quotes: 0 or key.
func indexKeyText(key json.RawMessage) (string, bool) {
	k, ok := indexKey(key)
	if !ok {
		return "", false
	}
	var s string
	if err := json.Unmarshal(key, &s); err == nil {
		return s, true
	}
	return k, true
}

// pravoName returns the pravo resource name for an instance of the resource
// name that sits in the module instance steps, and whether it differs from the
// tfstate name. The name takes the module calls and their keys as a prefix,
// and the instance key as a suffix: <module>_<key>_<name>_<key>.
func pravoName(name string, steps []modStep, key json.RawMessage) (string, bool) {
	orig := name
	if prefix := modulePrefix(steps); prefix != "" {
		name = prefix + "_" + name
	}
	if k, ok := indexKeyText(key); ok {
		name += "_" + k
	}
	name = sanitise(name)
	return name, name != orig
}

// modulePrefix turns module.a["x"].module.b[1] into a_x_b_1.
func modulePrefix(steps []modStep) string {
	var parts []string
	for _, st := range steps {
		parts = append(parts, st.name)
		if st.keyed {
			parts = append(parts, st.key)
		}
	}
	return strings.Join(parts, "_")
}

// sanitise keeps a name usable as a pravo address component: ASCII letters
// and digits, '_' and '-' are kept; everything else becomes '_'.
func sanitise(name string) string {
	var b strings.Builder
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
			b.WriteRune(c)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func dedupe(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	out := s[:1]
	for _, x := range s[1:] {
		if x != out[len(out)-1] {
			out = append(out, x)
		}
	}
	return out
}
