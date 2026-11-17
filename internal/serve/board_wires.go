package serve

import (
	"sort"
	"strings"
	"sync"

	"github.com/pravo-systems/pravo/internal/schema"
)

// A wire is a reference from an attribute of one resource to an attribute of another, as in subnet_id:
// private.id. The schema of a provider does not say what an attribute refers to: subnet_id is a string like any
// other. The board guesses it from the name of the attribute, the way a person reading the documentation does:
// subnet_id reads the id of an aws_subnet, db_subnet_group_name the name of an aws_db_subnet_group, and
// network, in a google_compute_subnetwork, a google_compute_network. A guess is only an offer: a person accepts it,
// and a wrong one is still a string of the right type, so the real plan is the judge. How often the guess is right
// on the real AWS and Google providers is measured by TestBoardWiresMeasure.

// wireHint is what an attribute can be wired to.
type wireHint struct {
	// To are the resource types it can read, the likeliest first, each with the attribute it reads of them: id, arn,
	// name, self_link, or the attribute of the same name (the function_name of a function).
	To []wireTarget
	// Many is set for a list or a set of references.
	Many bool
}

// wireTarget is a type that an attribute can read, and what it reads of it.
type wireTarget struct {
	Type, Read string
}

// types lists the types of the targets.
func (h *wireHint) types() []string {
	out := make([]string, len(h.To))
	for i, t := range h.To {
		out[i] = t.Type
	}
	return out
}

// wireSuffix is an ending of an attribute name that says what is read of the resource it names.
type wireSuffix struct {
	suffix, read string
	many         bool
}

// wireSuffixes are tried in this order: _ids before _id.
var wireSuffixes = []wireSuffix{
	{"_ids", "id", true}, {"_id", "id", false},
	{"_arns", "arn", true}, {"_arn", "arn", false},
	{"_names", "name", true}, {"_name", "name", false},
	{"_self_links", "self_link", true}, {"_self_link", "self_link", false},
	{"_urls", "url", true}, {"_url", "url", false},
}

// wireAliases are the stems that name a type of another name, for the types that are wired to most and that no ending
// finds: a load balancer of AWS is an aws_lb, a role an aws_iam_role, and a key that is named by an id a key of KMS
// while one named by its name is a key pair. The first type that has the attribute read is taken. The table is short on
// purpose; what it does not hold the guide finds, or a person picks.
var wireAliases = map[string]map[string][]string{
	"aws": {
		"load_balancer":    {"aws_lb"},
		"lb":               {"aws_lb"},
		"target_group":     {"aws_lb_target_group"},
		"listener":         {"aws_lb_listener"},
		"role":             {"aws_iam_role"},
		"instance_profile": {"aws_iam_instance_profile"},
		"policy":           {"aws_iam_policy"},
		"user":             {"aws_iam_user"},
		"certificate":      {"aws_acm_certificate"},
		"zone":             {"aws_route53_zone"},
		"hosted_zone":      {"aws_route53_zone"},
		"key":              {"aws_kms_key", "aws_key_pair"},
		"allocation":       {"aws_eip"},
		"log_group":        {"aws_cloudwatch_log_group"},
		"gateway":          {"aws_internet_gateway"},
	},
	"google": {
		"network":         {"google_compute_network"},
		"subnetwork":      {"google_compute_subnetwork"},
		"service_account": {"google_service_account"},
		"bucket":          {"google_storage_bucket"},
		"topic":           {"google_pubsub_topic"},
		"crypto_key":      {"google_kms_crypto_key"},
		"key_ring":        {"google_kms_key_ring"},
		"managed_zone":    {"google_dns_managed_zone"},
	},
}

// bareStop are names of attributes that are never references, though a type ends in them.
var bareStop = map[string]bool{
	"name": true, "description": true, "type": true, "kind": true, "region": true, "zone": true, "location": true,
	"mode": true, "state": true, "status": true, "comment": true, "version": true, "engine": true, "protocol": true,
	"path": true, "email": true, "policy": true, "document": true, "definition": true, "schema": true, "template": true,
	"label": true, "title": true, "value": true, "content": true, "source": true, "target": true, "action": true,
	"project": true, "member": true, "key": true, "availability_zone": true,
}

// stemStop are stems that name something outside the drawing however a type ends: an account number, the resource of
// any type that a tag or a protection names, the tenant of a call.
var stemStop = map[string]bool{
	"account": true, "aws_account": true, "admin_account": true, "owner_account": true, "resource": true, "tenant": true,
	"asset": true, "bundle": true, "principal": true, "parent": true, "owner": true, "client": true, "domain": true,
	"endpoint": true, "external": true, "org": true, "organization": true, "folder": true, "billing_account": true,
}

// wireIndex knows the resource types of the providers of a board and finds the ones an attribute can read.
type wireIndex struct {
	// types are the managed resource types of every provider, and block the schema of one, nil when unknown.
	types map[string]bool
	block func(typ string) *schema.Block
	// byEnd lists, for each ending of a type name of one or more of its words after the prefix of the provider, the types
	// that end in it: "subnet" lists aws_subnet, aws_default_subnet, "db_subnet_group" aws_db_subnet_group.
	byEnd map[string][]string

	mu   sync.Mutex
	memo map[string]*wireHint
}

// newWireIndex indexes the types. block gives the schema of a type, for the check that the attribute a wire reads is
// one the type has.
func newWireIndex(types []string, block func(typ string) *schema.Block) *wireIndex {
	ix := &wireIndex{types: map[string]bool{}, block: block, byEnd: map[string][]string{}, memo: map[string]*wireHint{}}
	for _, t := range types {
		ix.types[t] = true
		words := strings.Split(t, "_")
		for k := 1; k < len(words); k++ {
			end := strings.Join(words[k:], "_")
			ix.byEnd[end] = append(ix.byEnd[end], t)
		}
	}
	for _, l := range ix.byEnd {
		sort.Strings(l)
	}
	return ix
}

// prefixOf is the provider part of a type name: aws of aws_db_instance.
func prefixOf(typ string) string {
	p, _, _ := strings.Cut(typ, "_")
	return p
}

// familyOf is the word of a type name after the provider: db of aws_db_instance, compute of google_compute_network.
func familyOf(typ string) string {
	words := strings.Split(typ, "_")
	if len(words) < 3 {
		return ""
	}
	return words[1]
}

// refShaped reports whether an attribute can hold a reference: a string, or a list or set of strings, that a person
// sets.
func refShaped(a *schema.Attribute) (ok, many bool) {
	if a == nil || a.NestedType != nil || (!a.Required && !a.Optional) || a.Sensitive || a.WriteOnly {
		return false, false
	}
	switch attrType(a) {
	case "string":
		return true, false
	case "list(string)", "set(string)":
		return true, true
	}
	return false, false
}

// wireOf finds what an attribute of a type can be wired to, or nil.
func (ix *wireIndex) wireOf(srcType string, a *schema.Attribute) *wireHint {
	ok, many := refShaped(a)
	if !ok {
		return nil
	}
	key := srcType + "." + a.Name
	ix.mu.Lock()
	if h, done := ix.memo[key]; done {
		ix.mu.Unlock()
		return h
	}
	ix.mu.Unlock()
	h := ix.find(srcType, a, many)
	ix.mu.Lock()
	ix.memo[key] = h
	ix.mu.Unlock()
	return h
}

// find works out wireOf.
func (ix *wireIndex) find(srcType string, a *schema.Attribute, many bool) *wireHint {
	name := a.Name
	// A document is a string too, and no reference: the policy of a role, a definition of a state machine.
	if d := strings.ToLower(a.Description); strings.Contains(d, "json") || strings.Contains(d, "yaml") {
		return nil
	}
	prefix := prefixOf(srcType)
	own := strings.TrimPrefix(srcType, prefix+"_")
	for _, s := range wireSuffixes {
		stem, ok := strings.CutSuffix(name, s.suffix)
		if !ok || stem == "" {
			continue
		}
		// The name or the id of the resource itself is no reference: the function_name of an aws_lambda_function, the
		// db_name of an aws_db_instance, the backup_vault_id of a vault.
		if stem == own || strings.HasSuffix(own, "_"+stem) || (s.read == "name" && strings.HasPrefix(own, stem+"_")) {
			return nil
		}
		// The attribute that is read is the one of the same name when the type has it (the function_name of the
		// function that a permission names), else the one that the ending says. A stem that names something outside
		// the drawing is still a reference inside the family of the source: the resource_id of an aws_api_gateway_method
		// is an aws_api_gateway_resource.
		if to := ix.typesFor(srcType, prefix, stem, []string{name, s.read}, stemStop[stem]); len(to) > 0 {
			return &wireHint{To: to, Many: many || s.many}
		}
		return nil
	}
	// A name with no ending can be the name of a type: the network of a subnetwork, the cluster of a service, the role of
	// a function. It is taken from the family of the type or the aliases only, since many names end a type somewhere.
	bare := name
	if many {
		bare = strings.TrimSuffix(bare, "s")
	}
	if bareStop[bare] {
		return nil
	}
	if to := ix.typesFor(srcType, prefix, bare, []string{"id", "name"}, true); len(to) > 0 {
		return &wireHint{To: to, Many: many}
	}
	return nil
}

// typesFor lists the types that a stem names and that have the attribute read, best first: the type of the provider that is
// named by the stem alone, then the ones of the family of the source, then the shorter names, then an alias. A stem of
// several words is tried whole and then without its first words, down to two words (vpc_security_group finds
// aws_security_group), and the alias of its last word last (the node_role of a node group is an aws_iam_role). A bare
// name takes the type named by it or one of the family of the source only.
func (ix *wireIndex) typesFor(srcType, prefix, stem string, reads []string, bare bool) []wireTarget {
	words := strings.Split(stem, "_")
	family := familyOf(srcType)
	for k := range words {
		end := strings.Join(words[k:], "_")
		// The alias of the last word is for a name of two words when the name is bare: the authorized_network of an
		// instance, and not the ca_certificates_bundle_s3_key of a store.
		last := k > 0 && len(words)-k < 2
		if l := ix.alias(srcType, prefix, end, reads); l != nil && (k == 0 || last && (!bare || len(words) == 2)) {
			return l
		}
		exact := prefix + "_" + end
		if k > 0 && len(words)-k < 2 {
			// The last word alone names only the type of that very name: the peer_vpc_id of a peering is an aws_vpc.
			if !bare && exact != srcType && ix.types[exact] {
				if read := ix.reads(exact, reads); read != "" {
					return []wireTarget{{exact, read}}
				}
			}
			break
		}
		var found []wireTarget
		for _, t := range ix.byEnd[end] {
			if t == srcType || prefixOf(t) != prefix {
				continue
			}
			if bare && t != exact && (family == "" || familyOf(t) != family) {
				continue
			}
			if read := ix.reads(t, reads); read != "" {
				found = append(found, wireTarget{t, read})
			}
		}
		if len(found) == 0 {
			continue
		}
		sort.SliceStable(found, func(i, j int) bool {
			ti, tj := found[i].Type, found[j].Type
			if (ti == exact) != (tj == exact) {
				return ti == exact
			}
			fi, fj := familyOf(ti) == family && family != "", familyOf(tj) == family && family != ""
			if fi != fj {
				return fi
			}
			if li, lj := strings.Count(ti, "_"), strings.Count(tj, "_"); li != lj {
				return li < lj
			}
			return ti < tj
		})
		if len(found) > 6 {
			found = found[:6]
		}
		return found
	}
	return nil
}

// alias is the type that the table of aliases gives a stem, when it has one of the attributes.
func (ix *wireIndex) alias(srcType, prefix, stem string, reads []string) []wireTarget {
	for _, t := range wireAliases[prefix][stem] {
		if t == srcType || !ix.types[t] {
			continue
		}
		if read := ix.reads(t, reads); read != "" {
			return []wireTarget{{t, read}}
		}
	}
	return nil
}

// reads is the first of the attributes that a type has, "" when it has none.
func (ix *wireIndex) reads(typ string, reads []string) string {
	for _, r := range reads {
		if ix.has(typ, r) {
			return r
		}
	}
	return ""
}

// has reports whether a type has a string attribute of the name.
func (ix *wireIndex) has(typ, attr string) bool {
	if ix.block == nil {
		return true
	}
	b := ix.block(typ)
	if b == nil {
		return false
	}
	a := b.Attribute(attr)
	return a != nil && a.NestedType == nil && attrType(a) == "string"
}
