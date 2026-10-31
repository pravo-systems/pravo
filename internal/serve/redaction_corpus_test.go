package serve

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/registry"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
)

// The precision and soundness corpus of the redaction model. Each scenario is a small project built from the shapes that
// the AWS, random, local and tls providers really have (their Required/Optional/Computed/Sensitive flags), asserted on
// every surface: /api/detail, /api/graph (outputs and node counts), /api/fields, the template wizard, /api/source, the
// history of a detail and the attribute changes of a plan. The model under test:
//
//	MARK   sensitive or write-only in the schema, a name that NameGuard matches, a type without a cached schema;
//	READ   a top-level field that reads a withheld attribute (or a derived field) of another node is "derived";
//	MADE   a string that a provider made (Sensitive+Computed, not Optional, managed) is withheld wherever its text is;
//	WORLD  a value of a data source holding a string of a node it depends on is withheld, and its readers follow;
//	UNDECLARED nodes are judged by text against the typed and made strings of their recorded dependency closure.
//
// A typed string (what a person wrote into a sensitive attribute that is Required or Optional) never withholds a value
// by being the same text: it travels along references and, for data sources, along dependencies.

const (
	rcRegion    = "eu-north-1"                                                   // typed into a sensitive, Required attribute
	rcHunter    = "hunter22"                                                     // typed into a sensitive, Optional attribute
	rcPW        = "Zq7rT2vN9xK4mB6wL1pH8sD3"                                     // random_password.result: made
	rcBcrypt    = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy" // random_password.bcrypt_hash: made
	rcConsumer1 = "consumer-secret-value-1"                                      // typed, unique: appears nowhere
	rcConsumer2 = "consumer-secret-value-2"                                      // typed, unique: appears nowhere
	rcAccess    = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"                     // aws_iam_access_key.secret: made
	rcArnRegion = "arn:aws:ssm:eu-north-1:123456789012:parameter/app/region"
)

type rcM = map[string]any

// ---------------------------------------------------------------------------------------------------------------------
// The fake provider: types with the flags the real providers set.

type rcTy struct{ tf, cue string }

var (
	rcString = rcTy{`"string"`, "string"}
	rcNumber = rcTy{`"number"`, "number"}
	rcBool   = rcTy{`"bool"`, "bool"}
	rcList   = rcTy{`["list","string"]`, "[...string]"}
	rcMap    = rcTy{`["map","string"]`, "{[string]: string}"}
	rcObj    = rcTy{`["object",{"user":"string","phrase":"string"}]`, "{user: string, phrase: string}"}
)

// rcAttrDef is one attribute: flags are R(equired), O(ptional), C(omputed), S(ensitive), W(rite-only).
type rcAttrDef struct{ name, tf, cue, flags string }

func rcA(name string, ty rcTy, flags string) rcAttrDef {
	return rcAttrDef{name: name, tf: ty.tf, cue: ty.cue, flags: flags}
}

func (a rcAttrDef) has(f string) bool { return strings.Contains(a.flags, f) }

func (a rcAttrDef) attr() *schema.Attribute {
	return &schema.Attribute{Name: a.name, TypeJSON: json.RawMessage(a.tf), Required: a.has("R"), Optional: a.has("O"),
		Computed: a.has("C"), Sensitive: a.has("S"), WriteOnly: a.has("W")}
}

// cueLine is the field of the generated definition, as internal/schema/cue.go writes it.
func (a rcAttrDef) cueLine() string {
	suffix := ""
	switch {
	case a.has("S") && a.has("W"):
		suffix = " @pravo(sensitive,write_only)"
	case a.has("S"):
		suffix = " @pravo(sensitive)"
	case a.has("W"):
		suffix = " @pravo(write_only)"
	}
	switch {
	case a.has("C"):
		return fmt.Sprintf("%s: %s | null%s", a.name, a.cue, suffix)
	case a.has("R"):
		return fmt.Sprintf("%s: %s%s", a.name, a.cue, suffix)
	}
	return fmt.Sprintf("%s?: %s%s", a.name, a.cue, suffix)
}

type rcType struct {
	name  string
	data  bool
	attrs []rcAttrDef
}

var rcTypes = []rcType{
	{name: "aws_ssm_parameter", attrs: []rcAttrDef{
		rcA("name", rcString, "R"), rcA("type", rcString, "R"), rcA("value", rcString, "RS"), rcA("description", rcString, "O"),
		rcA("tier", rcString, "OC"), rcA("arn", rcString, "C"), rcA("version", rcNumber, "C"),
		rcA("tags", rcMap, "O"), rcA("tags_all", rcMap, "C"), rcA("id", rcString, "C")}},
	{name: "aws_ssm_parameter", data: true, attrs: []rcAttrDef{
		rcA("name", rcString, "R"), rcA("with_decryption", rcBool, "O"), rcA("arn", rcString, "C"), rcA("type", rcString, "C"),
		rcA("value", rcString, "CS"), rcA("version", rcNumber, "C"), rcA("id", rcString, "C")}},
	{name: "aws_availability_zones", data: true, attrs: []rcAttrDef{
		rcA("state", rcString, "O"), rcA("names", rcList, "C"), rcA("group_names", rcList, "C"), rcA("zone_ids", rcList, "C"),
		rcA("all_availability_zones", rcBool, "O"), rcA("id", rcString, "C"), rcA("region", rcString, "C")}},
	{name: "aws_region", data: true, attrs: []rcAttrDef{
		rcA("name", rcString, "OC"), rcA("description", rcString, "C"), rcA("endpoint", rcString, "C"), rcA("id", rcString, "C")}},
	{name: "aws_s3_bucket", attrs: []rcAttrDef{
		rcA("bucket", rcString, "OC"), rcA("region", rcString, "C"), rcA("arn", rcString, "C"),
		rcA("tags", rcMap, "O"), rcA("tags_all", rcMap, "C"), rcA("id", rcString, "C")}},
	{name: "aws_sns_topic", attrs: []rcAttrDef{
		rcA("name", rcString, "OC"), rcA("display_name", rcString, "O"), rcA("region", rcString, "OC"), rcA("arn", rcString, "C"),
		rcA("tags", rcMap, "O"), rcA("tags_all", rcMap, "C"), rcA("id", rcString, "C")}},
	{name: "aws_subnet", attrs: []rcAttrDef{
		rcA("vpc_id", rcString, "R"), rcA("cidr_block", rcString, "OC"), rcA("availability_zone", rcString, "OC"), rcA("arn", rcString, "C"),
		rcA("tags", rcMap, "O"), rcA("tags_all", rcMap, "C"), rcA("id", rcString, "C")}},
	{name: "random_password", attrs: []rcAttrDef{
		rcA("length", rcNumber, "R"), rcA("special", rcBool, "OC"), rcA("override_special", rcString, "O"), rcA("keepers", rcMap, "O"),
		rcA("result", rcString, "CS"), rcA("bcrypt_hash", rcString, "CS"), rcA("id", rcString, "C")}},
	{name: "aws_iam_access_key", attrs: []rcAttrDef{
		rcA("user", rcString, "R"), rcA("status", rcString, "OC"), rcA("secret", rcString, "CS"), rcA("ses_smtp_password_v4", rcString, "CS"),
		rcA("create_date", rcString, "C"), rcA("id", rcString, "C")}},
	{name: "local_file", attrs: []rcAttrDef{
		rcA("filename", rcString, "R"), rcA("content", rcString, "O"), rcA("file_permission", rcString, "OC"),
		rcA("content_md5", rcString, "C"), rcA("id", rcString, "C")}},
	{name: "local_file", data: true, attrs: []rcAttrDef{
		rcA("filename", rcString, "R"), rcA("content", rcString, "C"), rcA("content_base64", rcString, "C"),
		rcA("content_md5", rcString, "C"), rcA("id", rcString, "C")}},
	// rc_db is a database as a provider would have one: a password that a person types (Optional, Sensitive), a url, a
	// free comment, a computed endpoint.
	{name: "rc_db", attrs: []rcAttrDef{
		rcA("name", rcString, "R"), rcA("engine", rcString, "OC"), rcA("password", rcString, "OS"), rcA("url", rcString, "O"),
		rcA("comment", rcString, "O"), rcA("description", rcString, "O"), rcA("endpoint", rcString, "C"), rcA("id", rcString, "C"),
		rcA("pin_code", rcString, "OCS")}},
	// rc_vault holds collections that the provider made and marks sensitive as a whole.
	{name: "rc_vault", attrs: []rcAttrDef{
		rcA("name", rcString, "R"), rcA("issued", rcMap, "CS"), rcA("pins", rcList, "CS"), rcA("bundle", rcObj, "CS"), rcA("id", rcString, "C")}},
}

func rcSchemas() Schemas {
	res, data := map[string]*schema.Schema{}, map[string]*schema.Schema{}
	for _, ty := range rcTypes {
		var attrs []*schema.Attribute
		for _, a := range ty.attrs {
			attrs = append(attrs, a.attr())
		}
		s := &schema.Schema{Version: 1, Block: &schema.Block{Attributes: attrs}}
		if ty.data {
			data[ty.name] = s
		} else {
			res[ty.name] = s
		}
	}
	return ProviderSchemas{PS: &schema.ProviderSchema{Resources: res, DataSources: data, Ephemerals: map[string]*schema.Schema{}, Functions: map[string]*schema.Function{}}}
}

func rcLookup(src registry.Source, version string) (Schemas, bool) {
	if src.Type == "fake" && version == "1.0.0" {
		return rcSchemas(), true
	}
	return nil, false
}

// rcPreamble is the provider as a generated package would declare it, and the import of the template package.
func rcPreamble(tpl bool) string {
	var b strings.Builder
	b.WriteString("package demo\n\n")
	if tpl {
		b.WriteString("import \"pravo.dev/pravo\"\n\n")
	}
	b.WriteString("$fake: {\n\t$kind:   \"provider\"\n\t$source: \"example.test/x/fake\"\n")
	for _, g := range []struct {
		field, kind string
		data        bool
	}{{"#Resource", "resource", false}, {"#Data", "data", true}} {
		fmt.Fprintf(&b, "\t%s: {\n", g.field)
		for _, ty := range rcTypes {
			if ty.data != g.data {
				continue
			}
			fmt.Fprintf(&b, "\t\t%s: {\n\t\t\t$kind:   %q\n\t\t\t$type:   %q\n\t\t\t$source: \"example.test/x/fake\"\n", ty.name, g.kind, ty.name)
			for _, a := range ty.attrs {
				b.WriteString("\t\t\t" + a.cueLine() + "\n")
			}
			b.WriteString("\t\t\t...\n\t\t}\n")
		}
		b.WriteString("\t}\n")
	}
	b.WriteString("}\n\n")
	return b.String()
}

// ---------------------------------------------------------------------------------------------------------------------
// Building a project.

func rcRec(addr, typ string, attrs rcM, deps ...string) *state.Resource {
	b, err := json.Marshal(attrs)
	if err != nil {
		panic(err)
	}
	return record(addr, typ, string(b), deps...)
}

func rcData(addr, typ string, attrs rcM, deps ...string) *state.Resource {
	r := rcRec(addr, typ, attrs, deps...)
	r.Mode = "data"
	return r
}

// rcMake writes the specification and the state of a project, without starting a server.
func rcMake(t *testing.T, spec string, tpl bool, recs []*state.Resource, outs map[string]string, hooks ...func(*testing.T, *state.Store)) (string, *state.Store) {
	t.Helper()
	dir := t.TempDir()
	writeSpec(t, dir, rcPreamble(tpl)+spec)
	if tpl {
		pkg := filepath.Join(dir, "cue.mod", "gen", "pravo.dev", "pravo", "settings_gen.cue")
		if err := os.MkdirAll(filepath.Dir(pkg), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pkg, []byte(schema.SettingsPackage), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	txn := st.Begin("apply", "pravo apply")
	for _, r := range recs {
		before := r.Revision
		if err := st.Set(r, txn.ID()); err != nil {
			t.Fatal(err)
		}
		txn.Record(r.Address(), "create", before, r.Revision)
	}
	if err := txn.Finish("applied", fmt.Sprintf("%d created", len(recs)), true); err != nil {
		t.Fatal(err)
	}
	if len(outs) > 0 {
		m := map[string]json.RawMessage{}
		for k, v := range outs {
			m[k] = json.RawMessage(v)
		}
		if err := st.SetOutputs(m); err != nil {
			t.Fatal(err)
		}
	}
	for _, h := range hooks {
		h(t, st)
	}
	return dir, st
}

// rcServe starts a server over a project made by rcMake and checks that its specification loads.
func rcServe(t *testing.T, dir string, st *state.Store, opts Options) *project {
	t.Helper()
	opts.Schemas = rcLookup
	p := start(t, dir, st, opts)
	if g := p.graph(); g.Project.SpecError != "" {
		t.Fatalf("the specification does not load: %s", g.Project.SpecError)
	}
	return p
}

func rcProj(t *testing.T, spec string, tpl bool, recs []*state.Resource, outs map[string]string, hooks ...func(*testing.T, *state.Store)) *project {
	t.Helper()
	dir, st := rcMake(t, spec, tpl, recs, outs, hooks...)
	return rcServe(t, dir, st, Options{})
}

// ---------------------------------------------------------------------------------------------------------------------
// Verdicts.

// rcAttrOf finds the attribute at a dotted path of a detail ("tags_all.team", "names.0").
func rcAttrOf(d Detail, path string) *Attr {
	attrs := d.Attrs
	var found *Attr
	for _, part := range strings.Split(path, ".") {
		found = nil
		for i := range attrs {
			if attrs[i].Name == part {
				found = &attrs[i]
				break
			}
		}
		if found == nil {
			return nil
		}
		attrs = found.Children
	}
	return found
}

// rcState is the verdict on an attribute: "shown" (it and everything below it is sent), "partial" (sent, with something
// below it withheld), "withheld:REASON", "null", "unknown" or "absent".
func rcState(a *Attr) string {
	switch {
	case a == nil:
		return "absent"
	case a.Redacted != "":
		return "withheld:" + a.Redacted
	case a.Null:
		return "null"
	case a.Unknown:
		return "unknown"
	}
	for i := range a.Children {
		if s := rcState(&a.Children[i]); strings.HasPrefix(s, "withheld") || s == "partial" {
			return "partial"
		}
	}
	return "shown"
}

// rcMatches tells whether a verdict is the wanted one: "shown", "shown=TEXT", "withheld" (any reason), "withheld:REASON",
// "partial", "null", "absent".
func rcMatches(a *Attr, want string) bool {
	got := rcState(a)
	switch {
	case want == "withheld":
		return strings.HasPrefix(got, "withheld:")
	case strings.HasPrefix(want, "shown="):
		return got == "shown" && fmt.Sprint(a.Value) == strings.TrimPrefix(want, "shown=")
	}
	return got == want
}

type rcWant struct{ addr, path, want string }

func rcCheck(t *testing.T, p *project, wants []rcWant) {
	t.Helper()
	cache := map[string]Detail{}
	for _, w := range wants {
		d, ok := cache[w.addr]
		if !ok {
			d = p.detail(w.addr)
			cache[w.addr] = d
		}
		a := rcAttrOf(d, w.path)
		if !rcMatches(a, w.want) {
			got := rcState(a)
			if a != nil && a.Value != nil {
				got += fmt.Sprintf(" (value %v)", a.Value)
			}
			t.Errorf("/api/detail %s %s: %s, want %s", w.addr, w.path, got, w.want)
		}
	}
}

// rcOutput checks one output on the three places that say something about it: the outputs of the graph, the count of its
// node and its detail. want is "shown", "shown=TEXT", "withheld" or "withheld:REASON".
func rcOutput(t *testing.T, p *project, name, want string) {
	t.Helper()
	g := p.graph()
	var out *Output
	for i := range g.Outputs {
		if g.Outputs[i].Name == name {
			out = &g.Outputs[i]
		}
	}
	n := nodeOf(g, name)
	if out == nil || n == nil {
		t.Errorf("output %s: not in /api/graph (output %v, node %v)", name, out != nil, n != nil)
		return
	}
	a := rcAttrOf(p.detail(name), "value")
	if !rcMatches(a, want) {
		t.Errorf("/api/detail %s value: %s, want %s", name, rcState(a), want)
	}
	withheld := strings.HasPrefix(want, "withheld")
	if withheld {
		if out.Redacted == "" || !out.Sensitive || out.Value != nil || n.Sensitive != 1 {
			t.Errorf("/api/graph output %s: redacted %q sensitive %v value %v, node count %d; want it withheld", name, out.Redacted, out.Sensitive, out.Value, n.Sensitive)
		}
		if r := strings.TrimPrefix(want, "withheld:"); r != want && out.Redacted != r {
			t.Errorf("/api/graph output %s: redacted %q, want %q", name, out.Redacted, r)
		}
		return
	}
	if out.Redacted != "" || out.Sensitive || out.Value == nil || n.Sensitive != 0 {
		t.Errorf("/api/graph output %s: redacted %q sensitive %v value %v, node count %d; want it shown", name, out.Redacted, out.Sensitive, out.Value, n.Sensitive)
	}
	if v := strings.TrimPrefix(want, "shown="); v != want && fmt.Sprint(out.Value) != v {
		t.Errorf("/api/graph output %s: value %v, want %s", name, out.Value, v)
	}
}

// rcSweep requires that none of the strings is in the body of any surface: the graph, the detail and the source of every
// node, and a search of the field values for each of them.
func rcSweep(t *testing.T, p *project, secrets ...string) {
	t.Helper()
	g := p.graph()
	noSecrets(t, "/api/graph", p.get("/api/graph"), secrets...)
	for _, n := range g.Nodes {
		q := queryEscape(n.Addr)
		noSecrets(t, "/api/detail?addr="+n.Addr, p.get("/api/detail?addr="+q), secrets...)
		noSecrets(t, "/api/source?addr="+n.Addr, p.get("/api/source?addr="+q), secrets...)
	}
	for _, s := range secrets {
		var f Fields
		if err := json.Unmarshal(p.get("/api/fields?q="+queryEscape("*=*"+s+"*")), &f); err != nil {
			t.Fatal(err)
		}
		if len(f.Addrs) > 0 {
			t.Errorf("/api/fields finds a value that holds %q in %v", s, f.Addrs)
		}
	}
}

// ---------------------------------------------------------------------------------------------------------------------
// S1 to S4: one project. A parameter that holds the region as a sensitive, typed value; the zones and the region name
// that are read from outside; resources and outputs that read them, and one that reads the typed value.

const rcP1Spec = `
regionParam: $fake.#Resource.aws_ssm_parameter & {
	name:  "/app/region"
	type:  "String"
	value: "eu-north-1"
	tags: {env: "prod"}
}

zones: $fake.#Data.aws_availability_zones & {
	state: "available"
}

regionData: $fake.#Data.aws_region & {}

subnet: $fake.#Resource.aws_subnet & {
	vpc_id:            "vpc-1"
	cidr_block:        "10.0.1.0/24"
	availability_zone: zones.names[0]
	tags: {az: zones.names[0]}
}

bucket: $fake.#Resource.aws_s3_bucket & {
	bucket: "logs-\(regionData.name)"
	tags: {
		region: regionData.name
		team:   "data"
	}
}

consumer: $fake.#Resource.aws_ssm_parameter & {
	name:        "/app/consumer"
	type:        "String"
	value:       "consumer-secret-value-1"
	description: regionParam.value
	tags: {where: regionParam.value}
}

regionArn:   {$kind: "output", value: regionParam.arn}
regionValue: {$kind: "output", value: regionParam.value}
firstZone:   {$kind: "output", value: zones.names[0]}
regionName:  {$kind: "output", value: regionData.name}

#Pool: pravo.#Template & {
	$template: {
		name:        "pool"
		title:       "Pool"
		description: "Topics in a zone."
		inputs: {
			name:   {}
			zone:   {choices: zones.names}
			label:  {choices: bucket.tags}
			hidden: {choices: consumer.tags}
		}
	}
	name:   string & =~"^[a-z]+$"
	zone:   string
	label:  *"region" | string
	hidden: *"where" | string
	member: $fake.#Resource.aws_sns_topic & {"name": name, display_name: zone}
}
`

func rcP1(t *testing.T) (*project, []string) {
	t.Helper()
	zonesList := []string{"eu-north-1a", "eu-north-1b", "eu-north-1c"}
	recs := []*state.Resource{
		rcRec("regionParam", "aws_ssm_parameter", rcM{"id": "/app/region", "name": "/app/region", "type": "String", "value": rcRegion, "tier": "Standard",
			"arn": rcArnRegion, "version": 1, "description": nil, "tags": rcM{"env": "prod"}, "tags_all": rcM{"env": "prod"}}),
		rcData("zones", "aws_availability_zones", rcM{"id": rcRegion, "region": rcRegion, "state": "available", "names": zonesList,
			"group_names": []string{"eu-north-1-zg-1"}, "zone_ids": []string{"eun1-az1", "eun1-az2", "eun1-az3"}, "all_availability_zones": nil}),
		rcData("regionData", "aws_region", rcM{"id": rcRegion, "name": rcRegion, "description": "Europe (Stockholm)", "endpoint": "ec2.eu-north-1.amazonaws.com"}),
		rcRec("subnet", "aws_subnet", rcM{"id": "subnet-0a1b", "vpc_id": "vpc-1", "cidr_block": "10.0.1.0/24", "availability_zone": "eu-north-1a",
			"arn": "arn:aws:ec2:eu-north-1:123456789012:subnet/subnet-0a1b", "tags": rcM{"az": "eu-north-1a"}, "tags_all": rcM{"az": "eu-north-1a"}}, "zones"),
		rcRec("bucket", "aws_s3_bucket", rcM{"id": "logs-eu-north-1", "bucket": "logs-eu-north-1", "region": rcRegion, "arn": "arn:aws:s3:::logs-eu-north-1",
			"tags": rcM{"region": rcRegion, "team": "data"}, "tags_all": rcM{"region": rcRegion, "team": "data"}}, "regionData"),
		rcRec("consumer", "aws_ssm_parameter", rcM{"id": "/app/consumer", "name": "/app/consumer", "type": "String", "value": rcConsumer1, "description": rcRegion,
			"tier": "Standard", "arn": "arn:aws:ssm:eu-north-1:123456789012:parameter/app/consumer", "version": 1, "tags": rcM{"where": rcRegion}, "tags_all": rcM{"where": rcRegion}}, "regionParam"),
	}
	outs := map[string]string{"regionArn": `"` + rcArnRegion + `"`, "regionValue": `"` + rcRegion + `"`, "firstZone": `"eu-north-1a"`, "regionName": `"` + rcRegion + `"`}
	return rcProj(t, rcP1Spec, true, recs, outs), []string{rcConsumer1}
}

// S1: a parameter whose value is a typed, sensitive region. Everything but the value is shown, and so is the arn that has
// the region in its text.
func TestRCRegionParamShowsEverythingButItsValue(t *testing.T) {
	p, secrets := rcP1(t)
	rcCheck(t, p, []rcWant{
		{"regionParam", "value", "withheld:sensitive"},
		{"regionParam", "name", "shown=/app/region"},
		{"regionParam", "type", "shown=String"},
		{"regionParam", "tier", "shown=Standard"},
		{"regionParam", "arn", "shown=" + rcArnRegion},
		{"regionParam", "id", "shown=/app/region"},
		{"regionParam", "version", "shown=1"},
		{"regionParam", "description", "null"},
		{"regionParam", "tags", "shown"},
		{"regionParam", "tags_all", "shown"},
		{"regionParam", "tags.env", "shown=prod"},
	})
	rcSweep(t, p, secrets...)
}

// S2: an availability-zones data source without dependencies, in the same state as S1: all of it is shown.
func TestRCZonesWithoutDependenciesAreShown(t *testing.T) {
	p, _ := rcP1(t)
	rcCheck(t, p, []rcWant{
		{"zones", "id", "shown=eu-north-1"},
		{"zones", "region", "shown=eu-north-1"},
		{"zones", "state", "shown=available"},
		{"zones", "names", "shown"},
		{"zones", "names.0", "shown=eu-north-1a"},
		{"zones", "names.2", "shown=eu-north-1c"},
		{"zones", "group_names", "shown"},
		{"zones", "group_names.0", "shown=eu-north-1-zg-1"},
		{"zones", "zone_ids", "shown"},
		{"regionData", "name", "shown=eu-north-1"},
		{"regionData", "id", "shown=eu-north-1"},
		{"regionData", "description", "shown=Europe (Stockholm)"},
		{"regionData", "endpoint", "shown=ec2.eu-north-1.amazonaws.com"},
	})
}

// S3: a resource and an output that read the zones and the region name are shown, children included.
func TestRCReadersOfPublicDataAreShown(t *testing.T) {
	p, _ := rcP1(t)
	rcCheck(t, p, []rcWant{
		{"subnet", "availability_zone", "shown=eu-north-1a"},
		{"subnet", "tags", "shown"},
		{"subnet", "tags.az", "shown=eu-north-1a"},
		{"subnet", "tags_all", "shown"},
		{"subnet", "arn", "shown"},
		{"subnet", "cidr_block", "shown=10.0.1.0/24"},
		{"bucket", "bucket", "shown=logs-eu-north-1"},
		{"bucket", "region", "shown=eu-north-1"},
		{"bucket", "arn", "shown=arn:aws:s3:::logs-eu-north-1"},
		{"bucket", "tags", "shown"},
		{"bucket", "tags.region", "shown=eu-north-1"},
		{"bucket", "tags.team", "shown=data"},
		{"bucket", "tags_all", "shown"},
	})
	rcOutput(t, p, "firstZone", "shown=eu-north-1a")
	rcOutput(t, p, "regionName", "shown=eu-north-1")
}

// S4: what reads the typed value is derived; what only sits next to it, or reads its arn, is not.
func TestRCReadersOfTheTypedValueAreDerived(t *testing.T) {
	p, secrets := rcP1(t)
	rcCheck(t, p, []rcWant{
		{"consumer", "description", "withheld:derived"},
		{"consumer", "tags", "withheld:derived"},
		{"consumer", "value", "withheld:sensitive"},
		{"consumer", "arn", "shown=arn:aws:ssm:eu-north-1:123456789012:parameter/app/consumer"},
		{"consumer", "name", "shown=/app/consumer"},
		{"consumer", "tier", "shown=Standard"},
		{"consumer", "tags_all", "shown"},
		{"consumer", "id", "shown"},
	})
	rcSweep(t, p, secrets...)
}

// S4, the outputs: one that reads the arn of the parameter is shown although the arn spells the region that the parameter
// keeps typed (a typed string never withholds a value by its text), one that reads the value is derived. The graph, the
// count of the node and the detail say the same of each.
func TestRCOutputsThatReadTheTypedParameter(t *testing.T) {
	p, _ := rcP1(t)
	rcOutput(t, p, "regionArn", "shown="+rcArnRegion)
	rcOutput(t, p, "regionValue", "withheld:derived")
	p9, _ := rcP9(t)
	rcOutput(t, p9, "okArn", "shown="+rcArnRegion)
}

// The wizard offers what the detail shows: the zones of the data source and the keys of a map of a bucket, and nothing
// from a map that the detail withholds.
func TestRCWizardChoicesFollowTheVerdict(t *testing.T) {
	p, secrets := rcP1(t)
	zone := p.choices("zone")
	if zone.State != "ok" || len(zone.Items) != 3 || zone.Items[0].Value != "eu-north-1a" || zone.Items[2].Value != "eu-north-1c" {
		t.Errorf("/api/templates/choices zone (zones.names, shown in /api/detail): %+v", zone)
	}
	label := p.choices("label")
	if label.State != "ok" || len(label.Items) != 2 || label.Items[0].Value != "region" || label.Items[0].Label != rcRegion || label.Items[1].Value != "team" {
		t.Errorf("/api/templates/choices label (bucket.tags, shown in /api/detail): %+v", label)
	}
	hidden := p.choices("hidden")
	if hidden.State != "withheld" || len(hidden.Items) != 0 {
		t.Errorf("/api/templates/choices hidden (consumer.tags, derived in /api/detail): %+v", hidden)
	}
	for _, in := range []string{"zone", "label", "hidden"} {
		body, _ := json.Marshal(p.choices(in))
		noSecrets(t, "/api/templates/choices "+in, body, secrets...)
	}
}

// ---------------------------------------------------------------------------------------------------------------------
// S5: a generated password, a database that is set from it, and a node that holds its text with no reference.

const rcP5Spec = `
pw: $fake.#Resource.random_password & {
	length:  24
	special: false
}

db: $fake.#Resource.rc_db & {
	name:     "main"
	engine:   "postgres"
	password: pw.result
	url:      "postgres://u:\(pw.result)@h"
}

echo: $fake.#Resource.rc_db & {
	name:   "echo"
	engine: "mysql"
}
`

func rcP5(t *testing.T) (*project, []string) {
	t.Helper()
	recs := []*state.Resource{
		rcRec("pw", "random_password", rcM{"id": "none", "length": 24, "special": false, "override_special": nil, "keepers": nil, "result": rcPW, "bcrypt_hash": rcBcrypt}),
		rcRec("db", "rc_db", rcM{"id": "db-1", "name": "main", "engine": "postgres", "password": rcPW, "url": "postgres://u:" + rcPW + "@h",
			"comment": nil, "description": nil, "endpoint": "main.db.internal:5432"}, "pw"),
		rcRec("echo", "rc_db", rcM{"id": "db-2", "name": "echo", "engine": "mysql", "password": nil, "url": nil,
			"comment": "restored with " + rcPW + " today", "description": "mysql replica", "endpoint": "echo.db.internal:3306"}),
	}
	return rcProj(t, rcP5Spec, false, recs, nil), []string{rcPW, rcBcrypt}
}

func TestRCRandomPasswordIsMadeAndReadByReference(t *testing.T) {
	p, secrets := rcP5(t)
	rcCheck(t, p, []rcWant{
		{"pw", "result", "withheld:sensitive"},
		{"pw", "bcrypt_hash", "withheld:sensitive"},
		{"pw", "length", "shown=24"},
		{"pw", "special", "shown=false"},
		{"pw", "id", "shown=none"},
		// set from the password and from a url that embeds it
		{"db", "password", "withheld:sensitive"},
		{"db", "url", "withheld:derived"},
		{"db", "name", "shown=main"},
		{"db", "engine", "shown=postgres"},
		{"db", "endpoint", "shown=main.db.internal:5432"},
		{"db", "id", "shown=db-1"},
		// a provider echo: no reference leads to it, the text of the made string does. Only that leaf is withheld.
		{"echo", "comment", "withheld"},
		{"echo", "name", "shown=echo"},
		{"echo", "engine", "shown=mysql"},
		{"echo", "description", "shown=mysql replica"},
		{"echo", "endpoint", "shown=echo.db.internal:3306"},
		{"echo", "id", "shown=db-2"},
	})
	rcSweep(t, p, secrets...)
}

// ---------------------------------------------------------------------------------------------------------------------
// S6: the shape of TestServeARealRoot. A generated password in a file, a data source that reads the file back by its
// name, and what reads the data source.

const rcP6Spec = `
pw: $fake.#Resource.random_password & {length: 24}

cfg: $fake.#Resource.local_file & {
	filename: "out/api.json"
	content:  "{\"secret\": \"\(pw.result)\"}"
}

apiConfig: $fake.#Data.local_file & {filename: cfg.filename}

mirror: $fake.#Resource.local_file & {
	filename: "out/mirror.json"
	content:  apiConfig.content
}

api_config: {$kind: "output", value: apiConfig.content}
`

func rcP6(t *testing.T) (*project, []string) { return rcP6With(t, false) }

// rcP6With is S6; withB64 gives the data source the base64 form of the file as well (content_base64).
func rcP6With(t *testing.T, withB64 bool) (*project, []string) {
	t.Helper()
	content := `{"secret": "` + rcPW + `"}`
	b64 := base64.StdEncoding.EncodeToString([]byte(content))
	var b64Attr any
	if withB64 {
		b64Attr = b64
	}
	recs := []*state.Resource{
		rcRec("pw", "random_password", rcM{"id": "none", "length": 24, "special": true, "result": rcPW, "bcrypt_hash": rcBcrypt}),
		rcRec("cfg", "local_file", rcM{"id": "cfg1", "filename": "out/api.json", "content": content, "file_permission": "0777", "content_md5": "9a0364b9e99bb480dd25e1f0284c8555"}, "pw"),
		rcData("apiConfig", "local_file", rcM{"id": "cfg1", "filename": "out/api.json", "content": content, "content_base64": b64Attr, "content_md5": "9a0364b9e99bb480dd25e1f0284c8555"}, "cfg"),
		rcRec("mirror", "local_file", rcM{"id": "mir1", "filename": "out/mirror.json", "content": content, "file_permission": "0777", "content_md5": "9a0364b9e99bb480dd25e1f0284c8555"}, "apiConfig"),
	}
	jc, _ := json.Marshal(content)
	secrets := []string{rcPW, content}
	if withB64 {
		secrets = append(secrets, b64)
	}
	return rcProj(t, rcP6Spec, false, recs, map[string]string{"api_config": string(jc)}), secrets
}

func TestRCGeneratedPasswordThroughAFileTheDataSourceReads(t *testing.T) {
	p, secrets := rcP6(t)
	rcCheck(t, p, []rcWant{
		{"pw", "result", "withheld:sensitive"},
		{"pw", "length", "shown=24"},
		{"cfg", "content", "withheld:derived"},
		{"cfg", "filename", "shown=out/api.json"},
		{"cfg", "file_permission", "shown=0777"},
		{"cfg", "content_md5", "shown"},
		{"apiConfig", "content", "withheld"},
		{"apiConfig", "filename", "shown=out/api.json"},
		{"apiConfig", "id", "shown=cfg1"},
		{"apiConfig", "content_md5", "shown"},
		{"mirror", "content", "withheld:derived"},
		{"mirror", "filename", "shown=out/mirror.json"},
	})
	rcOutput(t, p, "api_config", "withheld:derived")
	rcSweep(t, p, secrets...)
}

// ---------------------------------------------------------------------------------------------------------------------
// S7: a typed password written into a file, read back by a data source. The data source is judged by what it depends on.
// (The old code lets this one through: a typed string reaches a data source along its dependencies, which it does not
// follow.)

const rcP7Spec = `
db: $fake.#Resource.rc_db & {
	name:     "main"
	engine:   "postgres"
	password: "hunter22"
}

file: $fake.#Resource.local_file & {
	filename: "out/db.env"
	content:  "pw=\(db.password)"
}

envData: $fake.#Data.local_file & {filename: file.filename}

reader: $fake.#Resource.local_file & {
	filename: "out/copy.env"
	content:  envData.content
}

envOut: {$kind: "output", value: envData.content}
`

func rcP7(t *testing.T) (*project, []string) { return rcP7With(t, false) }

// rcP7With is S7; withB64 gives the data source the base64 form of the file as well (content_base64).
func rcP7With(t *testing.T, withB64 bool) (*project, []string) {
	t.Helper()
	content := "pw=" + rcHunter
	b64 := base64.StdEncoding.EncodeToString([]byte(content))
	var b64Attr any
	if withB64 {
		b64Attr = b64
	}
	recs := []*state.Resource{
		rcRec("db", "rc_db", rcM{"id": "db-1", "name": "main", "engine": "postgres", "password": rcHunter, "url": nil, "comment": nil, "description": nil, "endpoint": "main.db.internal:5432"}),
		rcRec("file", "local_file", rcM{"id": "f1", "filename": "out/db.env", "content": content, "file_permission": "0777", "content_md5": "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a"}, "db"),
		rcData("envData", "local_file", rcM{"id": "f1", "filename": "out/db.env", "content": content, "content_base64": b64Attr, "content_md5": "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a"}, "file"),
		rcRec("reader", "local_file", rcM{"id": "f2", "filename": "out/copy.env", "content": content, "file_permission": "0777", "content_md5": "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a"}, "envData"),
	}
	secrets := []string{rcHunter}
	if withB64 {
		secrets = append(secrets, b64)
	}
	return rcProj(t, rcP7Spec, false, recs, map[string]string{"envOut": `"` + content + `"`}), secrets
}

func TestRCTypedSecretThroughAFileTheDataSourceReads(t *testing.T) {
	p, secrets := rcP7(t)
	rcCheck(t, p, []rcWant{
		{"db", "password", "withheld:sensitive"},
		{"db", "name", "shown=main"},
		{"db", "engine", "shown=postgres"},
		{"db", "endpoint", "shown=main.db.internal:5432"},
		{"file", "content", "withheld:derived"},
		{"file", "filename", "shown=out/db.env"},
		{"envData", "content", "withheld"},
		{"envData", "filename", "shown=out/db.env"},
		{"envData", "id", "shown=f1"},
		{"envData", "content_md5", "shown"},
		{"reader", "content", "withheld:derived"},
		{"reader", "filename", "shown=out/copy.env"},
	})
	rcOutput(t, p, "envOut", "withheld:derived")
	rcSweep(t, p, secrets...)
}

// ---------------------------------------------------------------------------------------------------------------------
// The surfaces agree.

type rcRow struct{ path, value string }

// rcRows are the values of a detail that /api/fields can find: the shown scalars, each at its dotted path.
func rcRows(rows []rcRow, path string, a *Attr) []rcRow {
	if a.Redacted != "" || a.Unknown || a.Null {
		return rows
	}
	if len(a.Children) > 0 {
		for i := range a.Children {
			rows = rcRows(rows, path+"."+a.Children[i].Name, &a.Children[i])
		}
		return rows
	}
	switch x := a.Value.(type) {
	case string:
		return append(rows, rcRow{strings.ToLower(path), strings.ToLower(x)})
	case float64, bool:
		return append(rows, rcRow{strings.ToLower(path), strings.ToLower(fmt.Sprint(x))})
	}
	return rows
}

// rcBareMatch is how /api/fields matches a name without a dot or a star: a field of that name at any depth.
func rcBareMatch(name, path string) bool {
	for {
		if path == name || strings.HasSuffix(path, "."+name) {
			return true
		}
		i := strings.LastIndexByte(path, '.')
		if i < 0 {
			return false
		}
		path = path[:i]
	}
}

func rcAnyRedacted(a *Attr, marked bool) bool {
	if a.Redacted != "" && (!marked || a.Redacted != "derived") {
		return true
	}
	for i := range a.Children {
		if rcAnyRedacted(&a.Children[i], marked) {
			return true
		}
	}
	return false
}

// rcAgree compares, for every node of the project, what the surfaces say about the same value.
func rcAgree(t *testing.T, p *project) {
	t.Helper()
	g := p.graph()
	details := map[string]Detail{}
	names := map[string]bool{}
	for _, n := range g.Nodes {
		d := p.detail(n.Addr)
		details[n.Addr] = d
		for _, a := range d.Attrs {
			names[a.Name] = true
		}
	}
	// /api/detail against /api/fields: a value that the detail sends has a row, and one that it withholds or leaves null has none.
	for name := range names {
		var f Fields
		if err := json.Unmarshal(p.get("/api/fields?q="+queryEscape(name)), &f); err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, a := range f.Addrs {
			got[a] = true
		}
		for _, n := range g.Nodes {
			var rows []rcRow
			d := details[n.Addr]
			for i := range d.Attrs {
				rows = rcRows(rows, d.Attrs[i].Name, &d.Attrs[i])
			}
			want := false
			for _, r := range rows {
				want = want || rcBareMatch(strings.ToLower(name), r.path)
			}
			if want != got[n.Addr] {
				t.Errorf("%s %s: /api/detail says a value is %s, /api/fields says it is %s", n.Addr, name,
					map[bool]string{true: "shown", false: "withheld or null"}[want], map[bool]string{true: "shown", false: "withheld or null"}[got[n.Addr]])
			}
		}
	}
	// /api/graph against /api/detail: an output.
	for _, o := range g.Outputs {
		n := nodeOf(g, o.Name)
		if n == nil || o.Unknown {
			continue
		}
		a := rcAttrOf(details[o.Name], "value")
		graphW, detailW, nodeW := o.Redacted != "" || o.Sensitive, a != nil && a.Redacted != "", n.Sensitive > 0
		if graphW != detailW || graphW != nodeW {
			t.Errorf("output %s: /api/graph outputs say withheld=%v, the node count of /api/graph says %v, /api/detail says %v", o.Name, graphW, nodeW, detailW)
		}
		if graphW && (o.Value != nil) || !graphW && o.Value == nil && a != nil && !a.Null {
			t.Errorf("output %s: /api/graph value %v with redacted %q", o.Name, o.Value, o.Redacted)
		}
	}
	// /api/graph against /api/detail: the count of a node. A value withheld for what it is counts; a node that sends all
	// its values counts none. (Whether a derived value counts is not fixed here.)
	for _, n := range g.Nodes {
		if n.Mode == "output" || n.NoSchema {
			continue // a node without a schema is withheld whole and its count "says nothing" (Node.NoSchema)
		}
		d := details[n.Addr]
		marked, any := false, false
		for i := range d.Attrs {
			marked = marked || rcAnyRedacted(&d.Attrs[i], true)
			any = any || rcAnyRedacted(&d.Attrs[i], false)
		}
		if marked && n.Sensitive == 0 {
			t.Errorf("%s: /api/detail withholds a marked value and the node count of /api/graph is 0", n.Addr)
		}
		if !any && n.Sensitive > 0 {
			t.Errorf("%s: the node count of /api/graph is %d and /api/detail withholds nothing", n.Addr, n.Sensitive)
		}
	}
	// The history of a detail and the attribute changes of its plan against the attributes of the same detail: a change of
	// an attribute that the detail withholds sends no value, and a change of one that the detail sends is not redacted.
	for _, n := range g.Nodes {
		d := details[n.Addr]
		type change struct {
			what string
			c    AttrChange
		}
		var changes []change
		for _, h := range d.History {
			for _, c := range h.Attrs {
				changes = append(changes, change{"the history entry " + h.ID, c})
			}
		}
		if d.Change != nil {
			for _, c := range d.Change.Attrs {
				changes = append(changes, change{"the plan change", c})
			}
		}
		for _, ch := range changes {
			top := ch.c.Path
			if i := strings.IndexAny(top, ".["); i >= 0 {
				top = top[:i]
			}
			a := rcAttrOf(d, top)
			if a == nil {
				continue
			}
			st := rcState(a)
			if strings.HasPrefix(st, "withheld") && (ch.c.Before != nil || ch.c.After != nil) {
				t.Errorf("%s %s: /api/detail says %s, and %s sends a value (before %v, after %v)", n.Addr, top, st, ch.what, ch.c.Before, ch.c.After)
			}
			if st == "shown" && (ch.c.BeforeRedacted || ch.c.AfterRedacted) {
				t.Errorf("%s %s: /api/detail sends the attribute, and %s withholds %s", n.Addr, top, ch.what, ch.c.Path)
			}
		}
	}
}

var rcBuilders = []struct {
	name  string
	build func(*testing.T) (*project, []string)
}{
	{"S1-S4", rcP1},
	{"S5", rcP5},
	{"S6", rcP6},
	{"S7", rcP7},
	{"S8", rcP8},
	{"S9", rcP9},
	{"S10", rcP10},
	{"S11", rcP11},
	{"S12", rcP12},
	{"S13", rcP13},
	{"S14", rcP14},
	{"S15", rcP15},
	{"S16", rcP16},
	{"S17", rcP17},
	{"S18", rcP18},
	{"S19", rcP19},
}

func TestRCEverySurfaceAgrees(t *testing.T) {
	for _, b := range rcBuilders {
		t.Run(b.name, func(t *testing.T) {
			p, _ := b.build(t)
			rcAgree(t, p)
		})
	}
}

// ---------------------------------------------------------------------------------------------------------------------
// S8: a data source that depends on the parameter. Only the sound direction is fixed: its value is withheld by its mark, and
// an unrelated resource in the same region is shown whole. What becomes of the data source's own arn is not asserted.

const rcP8Spec = `
regionParam: $fake.#Resource.aws_ssm_parameter & {
	name:  "/app/region"
	type:  "String"
	value: "eu-north-1"
}

regionLookup: $fake.#Data.aws_ssm_parameter & {name: regionParam.name}

topic: $fake.#Resource.aws_sns_topic & {
	name:         "alerts"
	region:       "eu-north-1"
	display_name: "Alerts"
}
`

func rcP8(t *testing.T) (*project, []string) {
	t.Helper()
	recs := []*state.Resource{
		rcRec("regionParam", "aws_ssm_parameter", rcM{"id": "/app/region", "name": "/app/region", "type": "String", "value": rcRegion, "tier": "Standard",
			"arn": rcArnRegion, "version": 1, "description": nil, "tags": nil, "tags_all": rcM{}}),
		rcData("regionLookup", "aws_ssm_parameter", rcM{"id": "/app/region", "name": "/app/region", "type": "String", "value": rcRegion, "arn": rcArnRegion, "version": 1, "with_decryption": true}, "regionParam"),
		rcRec("topic", "aws_sns_topic", rcM{"id": "arn:aws:sns:eu-north-1:123456789012:alerts", "name": "alerts", "display_name": "Alerts", "region": rcRegion,
			"arn": "arn:aws:sns:eu-north-1:123456789012:alerts", "tags": rcM{"region": rcRegion}, "tags_all": rcM{"region": rcRegion}}),
	}
	return rcProj(t, rcP8Spec, false, recs, nil), nil
}

func TestRCDataSourceOfTheTypedParameterAndAnUnrelatedResource(t *testing.T) {
	p, _ := rcP8(t)
	rcCheck(t, p, []rcWant{
		{"regionLookup", "value", "withheld"},
		{"regionParam", "value", "withheld:sensitive"},
		{"regionParam", "arn", "shown=" + rcArnRegion},
		// same text in the arn and in the region, no reference and no dependency
		{"topic", "name", "shown=alerts"},
		{"topic", "display_name", "shown=Alerts"},
		{"topic", "region", "shown=eu-north-1"},
		{"topic", "arn", "shown=arn:aws:sns:eu-north-1:123456789012:alerts"},
		{"topic", "id", "shown=arn:aws:sns:eu-north-1:123456789012:alerts"},
		{"topic", "tags", "shown"},
		{"topic", "tags.region", "shown=eu-north-1"},
		{"topic", "tags_all", "shown"},
	})
}

// ---------------------------------------------------------------------------------------------------------------------
// S9: outputs.

const rcP9Spec = `
pw: $fake.#Resource.random_password & {length: 24}

regionParam: $fake.#Resource.aws_ssm_parameter & {
	name:  "/app/region"
	type:  "String"
	value: "eu-north-1"
}

declSens:   {$kind: "output", value: "declared-secret-out-1", sensitive: true}
derivedOut: {$kind: "output", value: "region=\(regionParam.value)"}
nestedOut:  {$kind: "output", value: {conn: {host: "db.internal", creds: ["app", pw.result]}, region: "eu"}}
publicOut:  {$kind: "output", value: {conn: {host: "db.internal", port: 5432}, tags: ["a", "b"]}}
okArn:      {$kind: "output", value: regionParam.arn}
`

func rcP9(t *testing.T) (*project, []string) {
	t.Helper()
	recs := []*state.Resource{
		rcRec("pw", "random_password", rcM{"id": "none", "length": 24, "special": false, "result": rcPW, "bcrypt_hash": rcBcrypt}),
		rcRec("regionParam", "aws_ssm_parameter", rcM{"id": "/app/region", "name": "/app/region", "type": "String", "value": rcRegion, "tier": "Standard",
			"arn": rcArnRegion, "version": 1, "description": nil, "tags": nil, "tags_all": rcM{}}),
	}
	outs := map[string]string{
		"declSens":     `{"$sensitive":"declared-secret-out-1"}`,
		"derivedOut":   `"region=eu-north-1"`,
		"nestedOut":    `{"conn":{"host":"db.internal","creds":["app","` + rcPW + `"]},"region":"eu"}`,
		"publicOut":    `{"conn":{"host":"db.internal","port":5432},"tags":["a","b"]}`,
		"okArn":        `"` + rcArnRegion + `"`,
		"legacyRegion": `"eu-north-1"`,
		"legacyPlain":  `"plain-text-value"`,
	}
	return rcProj(t, rcP9Spec, false, recs, outs), []string{rcPW, "declared-secret-out-1"}
}

func TestRCOutputs(t *testing.T) {
	p, secrets := rcP9(t)
	rcOutput(t, p, "declSens", "withheld:sensitive")
	rcOutput(t, p, "derivedOut", "withheld:derived")
	rcOutput(t, p, "nestedOut", "withheld:derived")
	rcOutput(t, p, "publicOut", "shown")
	// legacyRegion and legacyPlain are recorded and the specification does not declare them (it loads): there are no
	// references to follow, so they are judged by text against every typed and made string. "eu-north-1" is withheld
	// there although it is a plain region: the deliberate cost of an output without references. A text that is no
	// secret is shown.
	rcOutput(t, p, "legacyRegion", "withheld")
	rcOutput(t, p, "legacyPlain", "shown=plain-text-value")
	if n := nodeOf(p.graph(), "legacyRegion"); n == nil || n.Presence != "state" {
		t.Errorf("legacyRegion is not an undeclared output: %+v", n)
	}
	rcSweep(t, p, secrets...)
}

// ---------------------------------------------------------------------------------------------------------------------
// S10 and S11: the same change of a region seen in the history of a record and in a plan, while the region is also a typed
// secret in another node; and a change of a marked attribute in both.

const rcP10Spec = `
regionParam: $fake.#Resource.aws_ssm_parameter & {
	name:  "/app/region"
	type:  "String"
	value: "eu-north-1"
}

topic: $fake.#Resource.aws_sns_topic & {
	name:         "alerts"
	region:       "eu-north-1"
	display_name: "Alerts"
}

consumer: $fake.#Resource.aws_ssm_parameter & {
	name:  "/app/consumer"
	type:  "String"
	value: "consumer-secret-value-2"
}
`

func rcTopicAttrs(region string) rcM {
	arn := "arn:aws:sns:" + region + ":123456789012:alerts"
	return rcM{"id": arn, "name": "alerts", "display_name": "Alerts", "region": region, "arn": arn, "tags": nil, "tags_all": rcM{}}
}

func rcConsumerAttrs(value string) rcM {
	return rcM{"id": "/app/consumer", "name": "/app/consumer", "type": "String", "value": value, "description": nil, "tier": "Standard",
		"arn": "arn:aws:ssm:eu-north-1:123456789012:parameter/app/consumer", "version": 1, "tags": nil, "tags_all": rcM{}}
}

func rcWorld10(region, consumer string) []*state.Resource {
	return []*state.Resource{
		rcRec("regionParam", "aws_ssm_parameter", rcM{"id": "/app/region", "name": "/app/region", "type": "String", "value": rcRegion, "tier": "Standard",
			"arn": rcArnRegion, "version": 1, "description": nil, "tags": nil, "tags_all": rcM{}}),
		rcRec("topic", "aws_sns_topic", rcTopicAttrs(region)),
		rcRec("consumer", "aws_ssm_parameter", rcConsumerAttrs(consumer)),
	}
}

// rcUpdate is a second apply that changes the record at addr, with the diff the transaction keeps.
func rcUpdate(addr string, attrs rcM) func(*testing.T, *state.Store) {
	return func(t *testing.T, st *state.Store) {
		t.Helper()
		r := st.Get(addr)
		next := *r
		b, err := json.Marshal(attrs)
		if err != nil {
			t.Fatal(err)
		}
		next.Attributes = json.RawMessage(b)
		txn := st.Begin("apply", "pravo apply")
		if err := st.Set(&next, txn.ID()); err != nil {
			t.Fatal(err)
		}
		txn.Record(addr, "update", r.Revision, next.Revision)
		txn.RecordDiff(addr, r.Attributes, next.Attributes, nil)
		if err := txn.Finish("applied", "1 changed", true); err != nil {
			t.Fatal(err)
		}
	}
}

func rcP10(t *testing.T) (*project, []string) {
	t.Helper()
	p := rcProj(t, rcP10Spec, false, rcWorld10("eu-west-1", rcConsumer1), nil,
		rcUpdate("topic", rcTopicAttrs(rcRegion)), rcUpdate("consumer", rcConsumerAttrs(rcConsumer2)))
	return p, []string{rcConsumer1, rcConsumer2}
}

func TestRCHistoryShowsARegionChangeAndRedactsAMarkedOne(t *testing.T) {
	p, secrets := rcP10(t)
	byPath := func(addr string) map[string]AttrChange {
		h := p.detail(addr).History
		if len(h) != 2 {
			t.Fatalf("history of %s: %+v", addr, h)
		}
		out := map[string]AttrChange{}
		for _, c := range h[0].Attrs {
			out[c.Path] = c
		}
		return out
	}
	topic := byPath("topic")
	for path, ba := range map[string][2]string{"region": {"eu-west-1", "eu-north-1"},
		"arn": {"arn:aws:sns:eu-west-1:123456789012:alerts", "arn:aws:sns:eu-north-1:123456789012:alerts"}} {
		if c := topic[path]; c.BeforeRedacted || c.AfterRedacted || c.Before != ba[0] || c.After != ba[1] {
			t.Errorf("history of topic, %s: %+v, want %q to %q shown", path, c, ba[0], ba[1])
		}
	}
	consumer := byPath("consumer")
	if c, ok := consumer["value"]; !ok || !c.BeforeRedacted || !c.AfterRedacted || c.Before != nil || c.After != nil {
		t.Errorf("history of consumer, value: %+v, want both sides redacted", c)
	}
	rcSweep(t, p, secrets...)
}

// rcChange, rcMk and rcPack build a plan file as the engine writes one.
type rcChange struct {
	Addr            string   `json:"addr"`
	Mode            string   `json:"mode"`
	Type            string   `json:"type"`
	Name            string   `json:"name"`
	Action          string   `json:"action"`
	ProviderKey     string   `json:"provider_key"`
	Config          []byte   `json:"config,omitempty"`
	Prior           []byte   `json:"prior,omitempty"`
	Planned         []byte   `json:"planned,omitempty"`
	Reasons         []string `json:"reasons,omitempty"`
	PriorRevision   int      `json:"prior_revision"`
	PriorGeneration string   `json:"prior_generation,omitempty"`
}

func rcMk(t tftypes.Type, v any) tftypes.Value {
	if v == nil {
		return tftypes.NewValue(t, nil)
	}
	switch {
	case t.Is(tftypes.Object{}):
		m := v.(map[string]any)
		out := map[string]tftypes.Value{}
		for k, at := range t.(tftypes.Object).AttributeTypes {
			out[k] = rcMk(at, m[k])
		}
		return tftypes.NewValue(t, out)
	case t.Is(tftypes.Map{}):
		out := map[string]tftypes.Value{}
		for k, e := range v.(map[string]any) {
			out[k] = rcMk(t.(tftypes.Map).ElementType, e)
		}
		return tftypes.NewValue(t, out)
	}
	return tftypes.NewValue(t, v)
}

func rcPack(t *testing.T, typ tftypes.Type, v any) []byte {
	t.Helper()
	b, err := rcMk(typ, v).MarshalMsgPack(typ)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func rcP11(t *testing.T) (*project, []string) {
	t.Helper()
	dir, st := rcMake(t, rcP10Spec, false, rcWorld10("eu-west-1", rcConsumer1), nil)
	root, err := config.Load(dir, config.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fp, err := root.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	topicType := rcSchemas().Type("managed", "aws_sns_topic").Block.Type()
	ssmType := rcSchemas().Type("managed", "aws_ssm_parameter").Block.Type()
	change := func(addr, typ string, bt tftypes.Type, prior, next rcM) rcChange {
		r := st.Get(addr)
		return rcChange{Addr: addr, Mode: "managed", Type: typ, Name: addr, Action: "update", ProviderKey: "fake",
			Prior: rcPack(t, bt, prior), Planned: rcPack(t, bt, next), Config: rcPack(t, bt, next),
			Reasons: []string{"the specification changed since the last apply"}, PriorRevision: r.Revision, PriorGeneration: r.Generation}
	}
	pf := map[string]any{
		"format": 1, "created": time.Now().UTC(), "command": "pravo plan -out plan.pravo", "lineage": st.Lineage(),
		"config_fingerprint": fp, "providers": map[string]string{"fake": "1.0.0"}, "actor": "ci@example", "stats": map[string]any{"total": 3},
		"changes": []rcChange{
			change("topic", "aws_sns_topic", topicType, rcTopicAttrs("eu-west-1"), rcTopicAttrs(rcRegion)),
			change("consumer", "aws_ssm_parameter", ssmType, rcConsumerAttrs(rcConsumer1), rcConsumerAttrs(rcConsumer2)),
		},
	}
	data, err := json.MarshalIndent(pf, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "plan.pravo")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return rcServe(t, dir, st, Options{PlanFile: path}), []string{rcConsumer1, rcConsumer2}
}

func TestRCPlanOverlayShowsARegionChangeAndRedactsAMarkedOne(t *testing.T) {
	p, secrets := rcP11(t)
	if g := p.graph(); g.Plan == nil || g.Plan.Stale {
		t.Fatalf("the plan is not laid over the graph: %+v", g.Plan)
	}
	changes := func(addr string) map[string]AttrChange {
		d := p.detail(addr)
		if d.Change == nil || d.Change.Action != "update" {
			t.Fatalf("change of %s: %+v", addr, d.Change)
		}
		out := map[string]AttrChange{}
		for _, c := range d.Change.Attrs {
			out[c.Path] = c
		}
		return out
	}
	topic := changes("topic")
	for path, ba := range map[string][2]string{"region": {"eu-west-1", "eu-north-1"},
		"arn": {"arn:aws:sns:eu-west-1:123456789012:alerts", "arn:aws:sns:eu-north-1:123456789012:alerts"}} {
		if c := topic[path]; c.BeforeRedacted || c.AfterRedacted || c.Before != ba[0] || c.After != ba[1] {
			t.Errorf("plan change of topic, %s: %+v, want %q to %q shown", path, c, ba[0], ba[1])
		}
	}
	if c, ok := changes("consumer")["value"]; !ok || !c.BeforeRedacted || !c.AfterRedacted || c.Before != nil || c.After != nil {
		t.Errorf("plan change of consumer, value: %+v, want both sides redacted", c)
	}
	rcSweep(t, p, secrets...)
}

// ---------------------------------------------------------------------------------------------------------------------
// S12: nodes the specification does not declare are judged by text against the typed and made strings of the nodes in
// their recorded dependency closure, and against every made string.

const rcP12Spec = `
db: $fake.#Resource.rc_db & {
	name:     "main"
	password: "hunter22"
}

regionParam: $fake.#Resource.aws_ssm_parameter & {
	name:  "/app/region"
	type:  "String"
	value: "eu-north-1"
}

pw: $fake.#Resource.random_password & {length: 24}

access: $fake.#Resource.aws_iam_access_key & {user: "svc"}
`

func rcOrphan(addr string, comment, description string, deps ...string) *state.Resource {
	return rcRec(addr, "rc_db", rcM{"id": addr, "name": addr, "engine": "postgres", "password": nil, "url": nil,
		"comment": comment, "description": description, "endpoint": addr + ".db.internal"}, deps...)
}

func rcP12(t *testing.T) (*project, []string) {
	t.Helper()
	recs := []*state.Resource{
		rcRec("db", "rc_db", rcM{"id": "db-1", "name": "main", "engine": "postgres", "password": rcHunter, "url": nil, "comment": nil, "description": nil, "endpoint": "main.db.internal:5432"}),
		rcRec("regionParam", "aws_ssm_parameter", rcM{"id": "/app/region", "name": "/app/region", "type": "String", "value": rcRegion, "tier": "Standard",
			"arn": rcArnRegion, "version": 1, "description": nil, "tags": nil, "tags_all": rcM{}}),
		rcRec("pw", "random_password", rcM{"id": "none", "length": 24, "special": false, "result": rcPW, "bcrypt_hash": rcBcrypt}),
		rcRec("access", "aws_iam_access_key", rcM{"id": "AKIAEXAMPLE", "user": "svc", "status": "Active", "secret": rcAccess, "ses_smtp_password_v4": nil, "create_date": "2026-01-01T00:00:00Z"}),
		rcOrphan("orphanDb", rcHunter, rcRegion, "db"),
		rcOrphan("orphanUp", "pw is "+rcHunter, "up", "orphanDb"),
		rcOrphan("orphanRegion", rcRegion, "ok-text", "regionParam"),
		rcOrphan("orphanPW", "restored "+rcPW, rcRegion),
		rcOrphan("orphanKey", rcAccess, "keyed"),
		rcOrphan("orphanFree", rcRegion, rcHunter),
	}
	return rcProj(t, rcP12Spec, false, recs, nil), []string{rcPW, rcAccess}
}

func TestRCUndeclaredNodesAreJudgedByScope(t *testing.T) {
	p, secrets := rcP12(t)
	for _, addr := range []string{"orphanDb", "orphanUp", "orphanRegion", "orphanPW", "orphanKey", "orphanFree"} {
		if n := p.detail(addr).Node; n.Presence != "state" {
			t.Errorf("%s is declared (presence %q): the scenario needs it undeclared", addr, n.Presence)
		}
	}
	rcCheck(t, p, []rcWant{
		// depends on db, whose typed password is hunter22: the text is withheld, "eu-north-1" (typed elsewhere) is shown
		{"orphanDb", "comment", "withheld"},
		{"orphanDb", "description", "shown=eu-north-1"},
		{"orphanDb", "name", "shown=orphanDb"},
		{"orphanDb", "endpoint", "shown=orphanDb.db.internal"},
		// the closure is transitive
		{"orphanUp", "comment", "withheld"},
		{"orphanUp", "description", "shown=up"},
		// depends on the parameter that keeps "eu-north-1" typed
		{"orphanRegion", "comment", "withheld"},
		{"orphanRegion", "description", "shown=ok-text"},
		// every made string counts for every node, with a dependency or not
		{"orphanPW", "comment", "withheld"},
		{"orphanPW", "description", "shown=eu-north-1"},
		{"orphanKey", "comment", "withheld"},
		{"orphanKey", "description", "shown=keyed"},
		// no dependency: the typed strings of other nodes are out of its scope
		{"orphanFree", "comment", "shown=eu-north-1"},
		{"orphanFree", "description", "shown=hunter22"},
		{"access", "secret", "withheld:sensitive"},
	})
	rcSweep(t, p, secrets...)
}

// ---------------------------------------------------------------------------------------------------------------------
// S13: collections that a provider made and marks sensitive as a whole. Their strings are made, children included.

const rcP13Spec = `
vault: $fake.#Resource.rc_vault & {name: "main"}

echoMap:    $fake.#Resource.aws_sns_topic & {name: "echoMap"}
echoList:   $fake.#Resource.aws_sns_topic & {name: "echoList"}
echoUser:   $fake.#Resource.aws_sns_topic & {name: "echoUser"}
echoPhrase: $fake.#Resource.aws_sns_topic & {name: "echoPhrase"}
plain:      $fake.#Resource.aws_sns_topic & {name: "plain", display_name: "ordinary text"}
`

const (
	rcVaultAdmin  = "adm-9Qd2Lm7Xc4"
	rcVaultOps    = "ops-5Tn8Rv1Ba6"
	rcVaultPin0   = "pin-3Hk7Wz2Yq9"
	rcVaultPin1   = "pin-8Jf4Ug6Ns1"
	rcVaultUser   = "svc-acct-5731Qp"
	rcVaultPhrase = "phr-2Zx6Cv9Bm3"
)

func rcTopic(addr, display string, tags rcM) *state.Resource {
	return rcRec(addr, "aws_sns_topic", rcM{"id": "arn:aws:sns:eu-north-1:123456789012:" + addr, "name": addr, "display_name": display, "region": rcRegion,
		"arn": "arn:aws:sns:eu-north-1:123456789012:" + addr, "tags": tags, "tags_all": rcM{}})
}

func rcP13(t *testing.T) (*project, []string) {
	t.Helper()
	recs := []*state.Resource{
		rcRec("vault", "rc_vault", rcM{"id": "v1", "name": "main", "issued": rcM{"admin": rcVaultAdmin, "ops": rcVaultOps},
			"pins": []string{rcVaultPin0, rcVaultPin1}, "bundle": rcM{"user": rcVaultUser, "phrase": rcVaultPhrase}}),
		rcTopic("echoMap", "contact "+rcVaultAdmin+" now", nil),
		rcTopic("echoList", "on call", rcM{"owner": rcVaultPin1, "team": "data"}),
		rcTopic("echoUser", rcVaultUser, nil),
		rcTopic("echoPhrase", "phrase: "+rcVaultPhrase, nil),
		rcTopic("plain", "ordinary text", rcM{"team": "data"}),
	}
	return rcProj(t, rcP13Spec, false, recs, nil), []string{rcVaultAdmin, rcVaultOps, rcVaultPin0, rcVaultPin1, rcVaultUser, rcVaultPhrase}
}

func TestRCMadeCollectionsPassOnToTheirChildren(t *testing.T) {
	p, secrets := rcP13(t)
	rcCheck(t, p, []rcWant{
		{"vault", "issued", "withheld:sensitive"},
		{"vault", "pins", "withheld:sensitive"},
		{"vault", "bundle", "withheld:sensitive"},
		{"vault", "name", "shown=main"},
		{"echoMap", "display_name", "withheld"},
		{"echoMap", "name", "shown=echoMap"},
		{"echoMap", "arn", "shown"},
		{"echoMap", "region", "shown=eu-north-1"},
		{"echoList", "tags", "partial"},
		{"echoList", "tags.owner", "withheld"},
		{"echoList", "tags.team", "shown=data"},
		{"echoList", "display_name", "shown=on call"},
		{"echoUser", "display_name", "withheld"},
		{"echoPhrase", "display_name", "withheld"},
		{"echoPhrase", "name", "shown=echoPhrase"},
		{"plain", "display_name", "shown=ordinary text"},
		{"plain", "tags", "shown"},
	})
	rcSweep(t, p, secrets...)
}

// ---------------------------------------------------------------------------------------------------------------------
// S14 and S15: a value that is withheld for what its place is does not make its text a secret elsewhere.

const rcP14Spec = `
legacyBucket: $fake.#Resource.aws_s3_bucket & {bucket: "legacy"}
newBucket:    $fake.#Resource.aws_s3_bucket & {bucket: "fresh"}
zones:        $fake.#Data.aws_availability_zones & {state: "available"}

where: {$kind: "output", value: newBucket.region}
`

func rcP14(t *testing.T) (*project, []string) {
	t.Helper()
	recs := []*state.Resource{
		rcRec("legacyBucket", "aws_s3_bucket", rcM{"id": "legacy", "bucket": "legacy", "region": "eu-west-1", "arn": "arn:aws:s3:::legacy", "tags": nil, "tags_all": rcM{},
			"legacy": rcRegion}),
		rcRec("newBucket", "aws_s3_bucket", rcM{"id": "fresh", "bucket": "fresh", "region": rcRegion, "arn": "arn:aws:s3:::fresh",
			"tags": rcM{"region": rcRegion}, "tags_all": rcM{"region": rcRegion}}),
		rcData("zones", "aws_availability_zones", rcM{"id": rcRegion, "region": rcRegion, "state": "available", "names": []string{"eu-north-1a", "eu-north-1b"},
			"group_names": []string{"eu-north-1-zg-1"}, "zone_ids": []string{"eun1-az1"}, "all_availability_zones": nil}),
	}
	return rcProj(t, rcP14Spec, false, recs, map[string]string{"where": `"` + rcRegion + `"`}), nil
}

func TestRCUnknownAttributeWithholdsItselfAndNothingElse(t *testing.T) {
	p, _ := rcP14(t)
	rcCheck(t, p, []rcWant{
		{"legacyBucket", "legacy", "withheld"},
		{"legacyBucket", "region", "shown=eu-west-1"},
		{"legacyBucket", "bucket", "shown=legacy"},
		{"newBucket", "region", "shown=eu-north-1"},
		{"newBucket", "tags", "shown"},
		{"newBucket", "tags.region", "shown=eu-north-1"},
		{"newBucket", "tags_all", "shown"},
		{"newBucket", "arn", "shown=arn:aws:s3:::fresh"},
		{"zones", "id", "shown=eu-north-1"},
		{"zones", "names", "shown"},
		{"zones", "region", "shown=eu-north-1"},
	})
	rcOutput(t, p, "where", "shown=eu-north-1")
}

const rcP15Spec = `
guarded: $fake.#Resource.aws_s3_bucket & {bucket: "guarded"}
other:   $fake.#Resource.aws_s3_bucket & {bucket: "other"}

otherRegion: {$kind: "output", value: other.region}
`

func rcP15(t *testing.T) (*project, []string) {
	t.Helper()
	recs := []*state.Resource{
		rcRec("guarded", "aws_s3_bucket", rcM{"id": "guarded", "bucket": "guarded", "region": rcRegion, "arn": "arn:aws:s3:::guarded", "tags": nil,
			"tags_all": rcM{"Password": rcRegion, "team": "data"}}),
		rcRec("other", "aws_s3_bucket", rcM{"id": "other", "bucket": "other", "region": rcRegion, "arn": "arn:aws:s3:::other",
			"tags": rcM{"region": rcRegion}, "tags_all": rcM{"region": rcRegion}}),
	}
	return rcProj(t, rcP15Spec, false, recs, map[string]string{"otherRegion": `"` + rcRegion + `"`}), nil
}

func TestRCNameGuardedMapKeyDoesNotMakeItsTextASecret(t *testing.T) {
	p, _ := rcP15(t)
	rcCheck(t, p, []rcWant{
		{"guarded", "tags_all", "partial"},
		{"guarded", "tags_all.Password", "withheld"},
		{"guarded", "tags_all.team", "shown=data"},
		{"guarded", "region", "shown=eu-north-1"},
		{"guarded", "arn", "shown=arn:aws:s3:::guarded"},
		{"other", "region", "shown=eu-north-1"},
		{"other", "tags", "shown"},
		{"other", "tags.region", "shown=eu-north-1"},
		{"other", "tags_all", "shown"},
		{"other", "arn", "shown=arn:aws:s3:::other"},
	})
	rcOutput(t, p, "otherRegion", "shown=eu-north-1")
}

// ---------------------------------------------------------------------------------------------------------------------
// Forms and source.

// The forms that a specification puts a secret in are looked for too: a data source that reads a file back also holds
// its base64 form (content_base64), and that is withheld like the content, for a made password (S6) and a typed one (S7).
func TestRCEncodedFormOfASecretInADataSourceIsWithheld(t *testing.T) {
	p6, secrets6 := rcP6With(t, true)
	rcCheck(t, p6, []rcWant{
		{"apiConfig", "content_base64", "withheld"},
		{"apiConfig", "content", "withheld"},
		{"apiConfig", "filename", "shown=out/api.json"},
	})
	rcSweep(t, p6, secrets6...)
	p7, secrets7 := rcP7With(t, true)
	rcCheck(t, p7, []rcWant{
		{"envData", "content_base64", "withheld"},
		{"envData", "content", "withheld"},
		{"envData", "filename", "shown=out/db.env"},
	})
	rcSweep(t, p7, secrets7...)
}

// /api/source masks the literal of a secret in the declaration of the node that holds it and of a node that depends on
// it, and shows the same text as an ordinary literal in a node with no dependency on it.
func TestRCSourceMasksALiteralOnlyWhereItIsASecret(t *testing.T) {
	p, _ := rcP8(t)
	body := func(addr string) string { return string(p.get("/api/source?addr=" + queryEscape(addr))) }
	if b := body("regionParam"); strings.Contains(b, rcRegion) {
		t.Errorf("/api/source regionParam sends the literal of its sensitive value: %.300s", b)
	}
	if b := body("regionLookup"); strings.Contains(b, rcRegion) {
		t.Errorf("/api/source regionLookup (depends on the parameter) sends %q: %.300s", rcRegion, b)
	}
	if b := body("topic"); !strings.Contains(b, rcRegion) {
		t.Errorf("/api/source topic (no dependency on the parameter) masks the region it spells: %.300s", b)
	}
}

// ---------------------------------------------------------------------------------------------------------------------
// S16: what a person types into an Optional and Computed sensitive attribute is not made by a provider, so its text
// travels along references only.

const rcP16Spec = `
pinned: $fake.#Resource.rc_db & {
	name:     "pinned"
	pin_code: "pin-typed-value-77"
}

echoPin: $fake.#Resource.rc_db & {name: "echoPin"}

pinText: {$kind: "output", value: echoPin.comment}
`

func rcP16(t *testing.T) (*project, []string) {
	t.Helper()
	recs := []*state.Resource{
		rcRec("pinned", "rc_db", rcM{"id": "p1", "name": "pinned", "engine": "postgres", "password": nil, "url": nil, "comment": nil, "description": nil,
			"endpoint": "pinned.db.internal", "pin_code": "pin-typed-value-77"}),
		rcRec("echoPin", "rc_db", rcM{"id": "p2", "name": "echoPin", "engine": "postgres", "password": nil, "url": nil, "comment": "see pin-typed-value-77",
			"description": nil, "endpoint": "echoPin.db.internal", "pin_code": nil}),
	}
	return rcProj(t, rcP16Spec, false, recs, map[string]string{"pinText": `"see pin-typed-value-77"`}), nil
}

func TestRCTypedOptionalComputedSensitiveIsNotMade(t *testing.T) {
	p, _ := rcP16(t)
	rcCheck(t, p, []rcWant{
		{"pinned", "pin_code", "withheld:sensitive"},
		{"pinned", "name", "shown=pinned"},
		{"pinned", "endpoint", "shown=pinned.db.internal"},
		{"echoPin", "comment", "shown=see pin-typed-value-77"},
		{"echoPin", "name", "shown=echoPin"},
		{"echoPin", "endpoint", "shown=echoPin.db.internal"},
	})
	rcOutput(t, p, "pinText", "shown=see pin-typed-value-77")
}

// ---------------------------------------------------------------------------------------------------------------------
// S17: a data source that reads the typed value by reference is derived in that field (READ applies to data nodes too).
// S18: a record whose type has no cached schema is withheld whole.

const rcP17Spec = `
regionParam: $fake.#Resource.aws_ssm_parameter & {
	name:  "/app/region"
	type:  "String"
	value: "eu-north-1"
}

byValue: $fake.#Data.aws_ssm_parameter & {name: regionParam.value}
`

func rcP17(t *testing.T) (*project, []string) {
	t.Helper()
	recs := []*state.Resource{
		rcRec("regionParam", "aws_ssm_parameter", rcM{"id": "/app/region", "name": "/app/region", "type": "String", "value": rcRegion, "tier": "Standard",
			"arn": rcArnRegion, "version": 1, "description": nil, "tags": nil, "tags_all": rcM{}}),
		rcData("byValue", "aws_ssm_parameter", rcM{"id": rcRegion, "name": rcRegion, "type": "String", "value": "stored-elsewhere-value", "arn": "arn:aws:ssm:eu-north-1:123456789012:parameter/eu-north-1",
			"version": 3, "with_decryption": true}, "regionParam"),
	}
	return rcProj(t, rcP17Spec, false, recs, nil), []string{"stored-elsewhere-value"}
}

func TestRCDataSourceThatReadsTheTypedValueIsDerivedInThatField(t *testing.T) {
	p, secrets := rcP17(t)
	rcCheck(t, p, []rcWant{
		{"byValue", "name", "withheld:derived"},
		{"byValue", "value", "withheld:sensitive"},
		{"byValue", "type", "shown=String"},
		{"byValue", "version", "shown=3"},
	})
	rcSweep(t, p, secrets...)
}

func rcP18(t *testing.T) (*project, []string) {
	t.Helper()
	recs := []*state.Resource{
		rcRec("ghost", "aws_unknown_thing", rcM{"id": "g1", "name": "ghost", "region": "eu-west-1", "tags": rcM{"team": "data"}}),
		rcRec("topic", "aws_sns_topic", rcM{"id": "arn:aws:sns:eu-central-1:123456789012:alerts", "name": "alerts", "display_name": "Alerts", "region": "eu-central-1",
			"arn": "arn:aws:sns:eu-central-1:123456789012:alerts", "tags": nil, "tags_all": rcM{}}),
	}
	return rcProj(t, `
topic: $fake.#Resource.aws_sns_topic & {name: "alerts", region: "eu-central-1", display_name: "Alerts"}
`, false, recs, nil), nil
}

func TestRCRecordWithoutASchemaIsWithheldWhole(t *testing.T) {
	p, _ := rcP18(t)
	rcCheck(t, p, []rcWant{
		{"ghost", "id", "withheld:schema"},
		{"ghost", "name", "withheld:schema"},
		{"ghost", "region", "withheld:schema"},
		{"ghost", "tags", "withheld:schema"},
		{"topic", "name", "shown=alerts"},
		{"topic", "region", "shown=eu-central-1"},
		{"topic", "arn", "shown=arn:aws:sns:eu-central-1:123456789012:alerts"},
	})
	g := p.graph()
	if n := nodeOf(g, "ghost"); n == nil || !n.NoSchema {
		t.Errorf("/api/graph ghost: %+v, want no_schema", n)
	}
	if d := p.detail("ghost"); d.SchemaKnown {
		t.Errorf("/api/detail ghost: schema_known is set")
	}
	var f Fields
	if err := json.Unmarshal(p.get("/api/fields?q="+queryEscape("region=eu-west-1")), &f); err != nil {
		t.Fatal(err)
	}
	for _, a := range f.Addrs {
		if a == "ghost" {
			t.Errorf("/api/fields finds a value of ghost, which /api/detail withholds")
		}
	}
	if f.Withheld < 1 {
		t.Errorf("/api/fields counts %d withheld nodes, want ghost counted", f.Withheld)
	}
}

// ---------------------------------------------------------------------------------------------------------------------
// S19: /api/source masks a literal that equals a typed secret in the dependency closure of the node, and only there.

const rcP19Spec = `
db: $fake.#Resource.rc_db & {
	name:     "main"
	password: "hunter22"
}

app: $fake.#Resource.rc_db & {
	name:        "app"
	description: db.name
	comment:     "hunter22"
}

other: $fake.#Resource.rc_db & {
	name:    "other"
	comment: "hunter22"
}
`

func rcP19(t *testing.T) (*project, []string) {
	t.Helper()
	recs := []*state.Resource{
		rcRec("db", "rc_db", rcM{"id": "db-1", "name": "main", "engine": "postgres", "password": rcHunter, "url": nil, "comment": nil, "description": nil, "endpoint": "main.db.internal:5432"}),
		rcRec("app", "rc_db", rcM{"id": "db-2", "name": "app", "engine": "postgres", "password": nil, "url": nil, "comment": rcHunter, "description": "main", "endpoint": "app.db.internal:5432"}, "db"),
		rcRec("other", "rc_db", rcM{"id": "db-3", "name": "other", "engine": "postgres", "password": nil, "url": nil, "comment": rcHunter, "description": nil, "endpoint": "other.db.internal:5432"}),
	}
	return rcProj(t, rcP19Spec, false, recs, nil), nil
}

func TestRCSourceMasksATypedLiteralInTheClosureOnly(t *testing.T) {
	p, _ := rcP19(t)
	body := func(addr string) string { return string(p.get("/api/source?addr=" + queryEscape(addr))) }
	if b := body("db"); strings.Contains(b, rcHunter) {
		t.Errorf("/api/source db sends the literal of its sensitive password: %.300s", b)
	}
	if b := body("app"); strings.Contains(b, rcHunter) {
		t.Errorf("/api/source app (reads db) sends a literal equal to the typed password of db: %.300s", b)
	}
	if b := body("other"); !strings.Contains(b, rcHunter) {
		t.Errorf("/api/source other (no dependency on db) masks a literal that is no secret of its closure: %.300s", b)
	}
}
