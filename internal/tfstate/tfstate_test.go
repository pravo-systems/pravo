package tfstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pravo.dev/pravo/internal/engine"
	"pravo.dev/pravo/internal/state"
)

// fixture is a tfstate document (format version 4) with a managed resource
// that depends on a data source, a for_each resource using a provider
// alias, a count resource, a resource inside a module, and an output.
const fixture = `{
  "version": 4,
  "terraform_version": "1.9.0",
  "serial": 5,
  "lineage": "0f3a4a4e-1c2b-4f6a-9c3e-1a2b3c4d5e6f",
  "outputs": {
    "pet": {"value": "quick-brown-fox", "type": "string"},
    "buckets": {"value": ["acme-logs", "acme-assets"], "type": ["list", "string"], "sensitive": true}
  },
  "resources": [
    {
      "mode": "data",
      "type": "local_file",
      "name": "readme",
      "provider": "provider[\"registry.terraform.io/hashicorp/local\"]",
      "instances": [
        {
          "schema_version": 0,
          "attributes": {"content": "hi", "filename": "README.md", "id": "abc"},
          "sensitive_attributes": []
        }
      ]
    },
    {
      "mode": "managed",
      "type": "random_pet",
      "name": "server",
      "provider": "provider[\"registry.terraform.io/hashicorp/random\"]",
      "instances": [
        {
          "schema_version": 0,
          "attributes": {"id": "quick-brown-fox", "keepers": null, "length": 3, "prefix": null, "separator": "-"},
          "sensitive_attributes": [],
          "private": "aGVsbG8=",
          "dependencies": ["data.local_file.readme"]
        }
      ]
    },
    {
      "mode": "managed",
      "type": "aws_s3_bucket",
      "name": "b",
      "provider": "provider[\"registry.terraform.io/hashicorp/aws\"].west",
      "each": "map",
      "instances": [
        {
          "index_key": "logs",
          "schema_version": 2,
          "attributes": {"bucket": "acme-logs", "id": "acme-logs"},
          "dependencies": ["random_pet.server"]
        },
        {
          "index_key": "assets",
          "schema_version": 2,
          "attributes": {"bucket": "acme-assets", "id": "acme-assets"},
          "dependencies": ["random_pet.server"]
        }
      ]
    },
    {
      "mode": "managed",
      "type": "null_resource",
      "name": "web",
      "provider": "provider[\"registry.terraform.io/hashicorp/null\"]",
      "each": "list",
      "instances": [
        {"index_key": 0, "schema_version": 0, "attributes": {"id": "1", "triggers": null}, "dependencies": ["aws_s3_bucket.b"]},
        {"index_key": 1, "schema_version": 0, "attributes": {"id": "2", "triggers": null}, "dependencies": ["aws_s3_bucket.b"]}
      ]
    },
    {
      "module": "module.child",
      "mode": "managed",
      "type": "random_pet",
      "name": "server",
      "provider": "module.child.provider[\"registry.terraform.io/hashicorp/random\"]",
      "instances": [
        {"schema_version": 0, "status": "tainted", "attributes": {"id": "lazy-dog", "length": 2}}
      ]
    }
  ],
  "check_results": null
}`

func TestConvert(t *testing.T) {
	res, err := Convert([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}

	byAddr := map[string]int{}
	for i, r := range res.Resources {
		byAddr[r.Address()] = i
	}
	want := []string{
		"aws_s3_bucket.b_assets", "aws_s3_bucket.b_logs", "data.local_file.readme",
		"null_resource.web_0", "null_resource.web_1", "random_pet.child_server", "random_pet.server",
	}
	if len(res.Resources) != len(want) {
		t.Fatalf("got %d resources, want %d: %v", len(res.Resources), len(want), addresses(res))
	}
	for i, a := range want {
		if res.Resources[i].Address() != a {
			t.Errorf("resource %d = %s, want %s", i, res.Resources[i].Address(), a)
		}
	}

	// Managed resource: provider, private data, dependency on the data source.
	pet := res.Resources[byAddr["random_pet.server"]]
	if pet.Mode != "managed" || pet.Provider != "registry.opentofu.org/hashicorp/random" || pet.ProviderKey != "random" {
		t.Errorf("random_pet.server provider = %q key %q mode %q", pet.Provider, pet.ProviderKey, pet.Mode)
	}
	if string(pet.Private) != "hello" {
		t.Errorf("private = %q, want hello", pet.Private)
	}
	if got := strings.Join(pet.Dependencies, ","); got != "data.local_file.readme" {
		t.Errorf("dependencies = %q", got)
	}
	var attrs map[string]any
	if err := json.Unmarshal(pet.Attributes, &attrs); err != nil || attrs["id"] != "quick-brown-fox" || attrs["length"] != float64(3) {
		t.Errorf("attributes = %s (%v)", pet.Attributes, err)
	}

	// Data source.
	rd := res.Resources[byAddr["data.local_file.readme"]]
	if rd.Mode != "data" || rd.Type != "local_file" || rd.Name != "readme" || rd.ProviderKey != "local" || rd.Private != nil {
		t.Errorf("data source = %+v", rd)
	}

	// for_each instances: renamed, alias in provider key, schema version kept.
	logs := res.Resources[byAddr["aws_s3_bucket.b_logs"]]
	if logs.ProviderKey != "aws.west" || logs.Provider != "registry.opentofu.org/hashicorp/aws" || logs.SchemaVersion != 2 {
		t.Errorf("aws_s3_bucket.b_logs = %+v", logs)
	}
	if got := strings.Join(logs.Dependencies, ","); got != "random_pet.server" {
		t.Errorf("b_logs dependencies = %q", got)
	}

	// count instances depend on every instance of the for_each resource.
	web := res.Resources[byAddr["null_resource.web_1"]]
	if got := strings.Join(web.Dependencies, ","); got != "aws_s3_bucket.b_assets,aws_s3_bucket.b_logs" {
		t.Errorf("web_1 dependencies = %q", got)
	}

	// Module resource keeps the module path as a prefix.
	child := res.Resources[byAddr["random_pet.child_server"]]
	if child.ProviderKey != "random" || child.Provider != "registry.opentofu.org/hashicorp/random" {
		t.Errorf("child = %+v", child)
	}

	// Renames are reported in pravo address order.
	wantRenames := []Rename{
		{From: `aws_s3_bucket.b["assets"]`, To: "aws_s3_bucket.b_assets"},
		{From: `aws_s3_bucket.b["logs"]`, To: "aws_s3_bucket.b_logs"},
		{From: "null_resource.web[0]", To: "null_resource.web_0"},
		{From: "null_resource.web[1]", To: "null_resource.web_1"},
		{From: "module.child.random_pet.server", To: "random_pet.child_server"},
	}
	if len(res.Renamed) != len(wantRenames) {
		t.Fatalf("renamed = %v", res.Renamed)
	}
	for i, r := range wantRenames {
		if res.Renamed[i] != r {
			t.Errorf("renamed[%d] = %v, want %v", i, res.Renamed[i], r)
		}
	}

	// A plain output keeps its value; a sensitive one is recorded with the
	// mark that listings and readers honour.
	if len(res.Outputs) != 2 || string(res.Outputs["pet"]) != `"quick-brown-fox"` {
		t.Errorf("outputs = %v", res.Outputs)
	}
	v, sensitive := engine.UnwrapOutput(res.Outputs["buckets"])
	if !sensitive || string(v) != `["acme-logs","acme-assets"]` {
		t.Errorf("sensitive output = %s (sensitive %v, value %s)", res.Outputs["buckets"], sensitive, v)
	}
	if _, sensitive := engine.UnwrapOutput(res.Outputs["pet"]); sensitive {
		t.Error("plain output recorded as sensitive")
	}

	// The tainted instance is noted.
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "tainted") {
		t.Errorf("notes = %v", res.Notes)
	}
}

func TestConvertSensitiveOutputs(t *testing.T) {
	res, err := Convert([]byte(`{"version": 4, "outputs": {
	  "token": {"value": "s3cret", "type": "string", "sensitive": true},
	  "nothing": {"value": null, "type": "string", "sensitive": true},
	  "open": {"value": {"a": 1}, "type": ["object", {"a": "number"}], "sensitive": false},
	  "unmarked": {"value": 7, "type": "number"}
	}, "resources": []}`))
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]struct {
		value     string
		sensitive bool
	}{
		"token":    {`"s3cret"`, true},
		"nothing":  {`null`, true},
		"open":     {`{"a": 1}`, false},
		"unmarked": {`7`, false},
	} {
		v, sensitive := engine.UnwrapOutput(res.Outputs[name])
		if sensitive != want.sensitive || string(v) != want.value {
			t.Errorf("%s = %s: value %s, sensitive %v; want %s, %v", name, res.Outputs[name], v, sensitive, want.value, want.sensitive)
		}
	}
	// The stored form is the one the engine records after an apply.
	if got, want := string(res.Outputs["token"]), string(engine.WrapOutput(json.RawMessage(`"s3cret"`), true)); got != want {
		t.Errorf("token = %s, want %s", got, want)
	}
}

// builtinFixture holds the records of the provider that OpenTofu builds in:
// a data source, a managed resource and a keyed instance of the data source,
// next to a resource that depends on them.
const builtinFixture = `{"version": 4, "resources": [
  {"mode": "data", "type": "terraform_remote_state", "name": "net",
   "provider": "provider[\"terraform.io/builtin/terraform\"]",
   "instances": [{"attributes": {"backend": "s3", "outputs": {"value": {"vpc": "v-1"}, "type": ["object", {"vpc": "string"}]}}}]},
  {"module": "module.m", "mode": "data", "type": "terraform_remote_state", "name": "net",
   "provider": "module.m.provider[\"registry.terraform.io/builtin/terraform\"]",
   "instances": [{"attributes": {"backend": "s3"}}]},
  {"mode": "managed", "type": "terraform_data", "name": "marker",
   "provider": "provider[\"terraform.io/builtin/terraform\"]",
   "instances": [{"index_key": "a", "attributes": {"id": "1", "input": null, "output": null, "triggers_replace": null}}]},
  {"mode": "managed", "type": "random_pet", "name": "p",
   "provider": "provider[\"registry.terraform.io/hashicorp/random\"]",
   "instances": [{"attributes": {"id": "x-y", "length": 2},
     "dependencies": ["data.terraform_remote_state.net", "terraform_data.marker", "module.m.data.terraform_remote_state.net"]}]}
]}`

func TestConvertSkipsBuiltinProvider(t *testing.T) {
	res, err := Convert([]byte(builtinFixture))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(addresses(res), ","); got != "random_pet.p" {
		t.Fatalf("resources = %s, want random_pet.p only", got)
	}
	if len(res.Resources[0].Dependencies) != 0 {
		t.Errorf("dependencies on skipped records were kept: %v", res.Resources[0].Dependencies)
	}
	if len(res.Renamed) != 0 {
		t.Errorf("a skipped record was reported as renamed: %v", res.Renamed)
	}
	wantNotes := []string{
		"data.terraform_remote_state.net: skipped, provider terraform.io/builtin/terraform is built into OpenTofu and pravo cannot install it (read the outputs of another root with pravo.#RemoteOutputs)",
		"module.m.data.terraform_remote_state.net: skipped, provider registry.opentofu.org/builtin/terraform is built into OpenTofu and pravo cannot install it (read the outputs of another root with pravo.#RemoteOutputs)",
		"terraform_data.marker: skipped, provider terraform.io/builtin/terraform is built into OpenTofu and pravo cannot install it (declare a null_resource of hashicorp/null instead)",
	}
	if len(res.Notes) != len(wantNotes) {
		t.Fatalf("notes = %q", res.Notes)
	}
	for i, n := range wantNotes {
		if res.Notes[i] != n {
			t.Errorf("note %d = %q\nwant %q", i, res.Notes[i], n)
		}
	}
}

func TestIsBuiltin(t *testing.T) {
	for source, want := range map[string]bool{
		"terraform.io/builtin/terraform":              true,
		"registry.opentofu.org/builtin/terraform":     true,
		"registry.opentofu.org/hashicorp/terraform":   false,
		"registry.opentofu.org/hashicorp/random":      false,
		"registry.example.com/acme/builtin":           false,
		"registry.example.com/acme/builtin/extra/one": false,
	} {
		if got := isBuiltin(source); got != want {
			t.Errorf("isBuiltin(%q) = %v, want %v", source, got, want)
		}
	}
}

func TestConvertErrors(t *testing.T) {
	if _, err := Convert([]byte(`{"version": 3, "resources": []}`)); err == nil || !strings.Contains(err.Error(), "version 3") {
		t.Errorf("version 3: %v", err)
	}
	if _, err := Convert([]byte(`not json`)); err == nil {
		t.Error("expected parse error")
	}
	collide := `{"version": 4, "resources": [
	  {"mode": "managed", "type": "t", "name": "a_x", "provider": "provider[\"registry.terraform.io/h/t\"]", "instances": [{"attributes": {}}]},
	  {"mode": "managed", "type": "t", "name": "a", "provider": "provider[\"registry.terraform.io/h/t\"]", "instances": [{"index_key": "x", "attributes": {}}]}
	]}`
	if _, err := Convert([]byte(collide)); err == nil || !strings.Contains(err.Error(), "t.a_x") {
		t.Errorf("collision: %v", err)
	}
	flat := `{"version": 4, "resources": [
	  {"mode": "managed", "type": "t", "name": "a", "provider": "provider[\"registry.terraform.io/h/t\"]", "instances": [{"attributes_flat": {"id": "1"}}]}
	]}`
	if _, err := Convert([]byte(flat)); err == nil || !strings.Contains(err.Error(), "flatmap") {
		t.Errorf("flatmap: %v", err)
	}
	bad := `{"version": 4, "resources": [
	  {"mode": "managed", "type": "t", "name": "a", "provider": "aws", "instances": [{"attributes": {}}]}
	]}`
	if _, err := Convert([]byte(bad)); err == nil || !strings.Contains(err.Error(), "provider address") {
		t.Errorf("bad provider: %v", err)
	}
}

func TestParseProvider(t *testing.T) {
	for _, tc := range []struct{ in, source, alias string }{
		{`provider["registry.terraform.io/hashicorp/aws"]`, "registry.opentofu.org/hashicorp/aws", ""},
		{`provider["registry.terraform.io/hashicorp/aws"].west`, "registry.opentofu.org/hashicorp/aws", "west"},
		{`module.a["k"].provider["registry.opentofu.org/hashicorp/random"]`, "registry.opentofu.org/hashicorp/random", ""},
	} {
		s, a, err := parseProvider(tc.in)
		if err != nil || s != tc.source || a != tc.alias {
			t.Errorf("parseProvider(%q) = %q %q %v", tc.in, s, a, err)
		}
	}
}

func TestNames(t *testing.T) {
	for _, tc := range []struct{ mod, name, key, want string }{
		{"", "b", `"logs"`, "b_logs"},
		{"", "b", `"eu-west-1/a.b"`, "b_eu-west-1_a_b"},
		{"", "web", "2", "web_2"},
		{"", "web", "", "web"},
		{"module.child", "x", "", "child_x"},
		{`module.a["k"].module.b[1]`, "x", "", "a_k_b_1_x"},
		// A dot, a bracket or a quote in a module key is part of the key.
		{`module.site["eu.west"]`, "p", "", "site_eu_west_p"},
		{`module.site["ap.south.1"]`, "n", "", "site_ap_south_1_n"},
		{`module.site["a]b"].module.in`, "p", `"k.1"`, "site_a_b_in_p_k_1"},
		{`module.site["say \"hi\""]`, "p", "", "site_say__hi__p"},
		{`module.site["na\u00efve"]`, "p", "", "site_na_ve_p"},
		{"", "b", `"na\u00efve"`, "b_na_ve"},
	} {
		steps, rest, err := splitModulePrefix(tc.mod)
		if err != nil || rest != "" {
			t.Errorf("splitModulePrefix(%q) = %v, %q, %v", tc.mod, steps, rest, err)
			continue
		}
		var key json.RawMessage
		if tc.key != "" {
			key = json.RawMessage(tc.key)
		}
		got, _ := pravoName(tc.name, steps, key)
		if got != tc.want {
			t.Errorf("pravoName(%q, %q, %s) = %q, want %q", tc.mod, tc.name, tc.key, got, tc.want)
		}
	}
}

func TestSplitModulePrefix(t *testing.T) {
	for _, tc := range []struct {
		in, rest string
		steps    []modStep
		bad      bool
	}{
		{in: "", steps: nil},
		{in: "random_pet.p", rest: "random_pet.p"},
		{in: "data.local_file.f", rest: "data.local_file.f"},
		{in: "module.a", steps: []modStep{{name: "a"}}},
		{in: "module.a.random_pet.p", rest: "random_pet.p", steps: []modStep{{name: "a"}}},
		{in: `module.a["x.y"].module.b[2].data.t.n`, rest: "data.t.n",
			steps: []modStep{{name: "a", key: "x.y", keyed: true}, {name: "b", key: "2", keyed: true}}},
		{in: `module.a["k"]`, steps: []modStep{{name: "a", key: "k", keyed: true}}},
		{in: `module.a["k"]x`, bad: true},
		{in: `module.a["k`, bad: true},
		{in: `module.a[1`, bad: true},
		{in: `module.`, bad: true},
	} {
		steps, rest, err := splitModulePrefix(tc.in)
		if tc.bad {
			if err == nil {
				t.Errorf("splitModulePrefix(%q) = %v, %q; want an error", tc.in, steps, rest)
			}
			continue
		}
		if err != nil || rest != tc.rest || fmt.Sprint(steps) != fmt.Sprint(tc.steps) {
			t.Errorf("splitModulePrefix(%q) = %v, %q, %v; want %v, %q", tc.in, steps, rest, err, tc.steps, tc.rest)
		}
	}
}

func addresses(res *Result) []string {
	var out []string
	for _, r := range res.Resources {
		out = append(out, r.Address())
	}
	return out
}

// The files under testdata were written by OpenTofu 1.13.1 and trimmed.
// platform.tfstate and gateway.tfstate are the two roots of the migration
// worked example: a module called with for_each, a sensitive output, and a
// data source of the built-in provider that reads the other root.

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func resourceByAddr(res *Result, addr string) *state.Resource {
	for _, r := range res.Resources {
		if r.Address() == addr {
			return r
		}
	}
	return nil
}

func TestConvertRealPlatformState(t *testing.T) {
	res, err := Convert(readTestdata(t, "platform.tfstate"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"local_file.service_api_config", "local_file.service_web_config", "local_file.service_worker_config",
		"random_password.admin",
		"random_pet.service_api_host", "random_pet.service_web_host", "random_pet.service_worker_host",
	}
	if got := addresses(res); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("resources = %v, want %v", got, want)
	}
	// OpenTofu names its own registry in the provider address.
	pw := resourceByAddr(res, "random_password.admin")
	if pw.Provider != "registry.opentofu.org/hashicorp/random" || pw.ProviderKey != "random" || pw.SchemaVersion != 3 {
		t.Errorf("random_password.admin = %+v", pw)
	}
	// The state writes the dependency of a module resource without the key of
	// the module instance: module.service.random_pet.host. Each config file
	// depends on the pet of its own instance, and on no other.
	for _, svc := range []string{"api", "web", "worker"} {
		cfg := resourceByAddr(res, "local_file.service_"+svc+"_config")
		if got, want := strings.Join(cfg.Dependencies, ","), "random_pet.service_"+svc+"_host"; got != want {
			t.Errorf("%s config depends on %q, want %q", svc, got, want)
		}
		if host := resourceByAddr(res, "random_pet.service_"+svc+"_host"); len(host.Dependencies) != 0 {
			t.Errorf("%s host depends on %v", svc, host.Dependencies)
		}
	}
	renamed := map[string]string{}
	for _, r := range res.Renamed {
		renamed[r.From] = r.To
	}
	if len(renamed) != 6 || renamed[`module.service["worker"].random_pet.host`] != "random_pet.service_worker_host" {
		t.Errorf("renamed = %v", res.Renamed)
	}
	if len(res.Notes) != 0 {
		t.Errorf("notes = %v", res.Notes)
	}
	// The sensitive output is wrapped; the list output is plain.
	v, sensitive := engine.UnwrapOutput(res.Outputs["admin_password"])
	if !sensitive || len(v) != len(`"HrxmHlTapk9ROWEN936mgZOP"`) {
		t.Errorf("admin_password = %s (sensitive %v)", res.Outputs["admin_password"], sensitive)
	}
	if _, sensitive := engine.UnwrapOutput(res.Outputs["endpoints"]); sensitive {
		t.Error("endpoints recorded as sensitive")
	}
}

func TestConvertRealGatewayState(t *testing.T) {
	res, err := Convert(readTestdata(t, "gateway.tfstate"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(addresses(res), ","); got != "local_file.upstreams" {
		t.Fatalf("resources = %s", got)
	}
	// The only dependency of upstreams is the skipped data source.
	if deps := res.Resources[0].Dependencies; len(deps) != 0 {
		t.Errorf("dependencies = %v", deps)
	}
	if len(res.Notes) != 1 || !strings.HasPrefix(res.Notes[0], "data.terraform_remote_state.platform: skipped, provider terraform.io/builtin/terraform") {
		t.Errorf("notes = %q", res.Notes)
	}
	if len(res.Outputs) != 0 {
		t.Errorf("outputs = %v", res.Outputs)
	}
	// The skipped record holds a copy of the other root's outputs, including
	// the sensitive one. None of it reaches the converted state.
	for _, r := range res.Resources {
		if strings.Contains(string(r.Attributes), "admin_password") {
			t.Errorf("%s carries the outputs of the other root", r.Address())
		}
	}
}

func TestConvertRealModules(t *testing.T) {
	res, err := Convert(readTestdata(t, "modules.tfstate"))
	if err != nil {
		t.Fatal(err)
	}
	deps := func(addr string) string {
		r := resourceByAddr(res, addr)
		if r == nil {
			t.Fatalf("%s is not in the converted state; have %v", addr, addresses(res))
		}
		return strings.Join(r.Dependencies, ",")
	}
	// A for_each module whose keys hold dots: the dots in a key are part of the
	// name, not path separators.
	for addr, want := range map[string]string{
		"random_pet.site_ap_south_1_p": "",
		"random_pet.site_eu_west_p":    "",
		"random_pet.site_us-east_p":    "",
		// Each null_resource depends on the pet of its own module instance.
		"null_resource.site_ap_south_1_n": "random_pet.site_ap_south_1_p",
		"null_resource.site_eu_west_n":    "random_pet.site_eu_west_p",
		"null_resource.site_us-east_n":    "random_pet.site_us-east_p",
		// A counted module that calls another module.
		"null_resource.mid_0_m":       "random_pet.mid_0_inner_p",
		"null_resource.mid_1_m":       "random_pet.mid_1_inner_p",
		"null_resource.mid_0_inner_n": "random_pet.mid_0_inner_p",
		"null_resource.mid_1_inner_n": "random_pet.mid_1_inner_p",
		// A root resource that depends on a module, or on the output of one,
		// depends on every instance the state names.
		"null_resource.via_output": "random_pet.site_ap_south_1_p,random_pet.site_eu_west_p,random_pet.site_us-east_p",
		"null_resource.after_module": strings.Join([]string{
			"null_resource.mid_0_inner_n", "null_resource.mid_0_m", "null_resource.mid_1_inner_n", "null_resource.mid_1_m",
			"null_resource.site_ap_south_1_n", "null_resource.site_eu_west_n", "null_resource.site_us-east_n",
			"random_pet.mid_0_inner_p", "random_pet.mid_1_inner_p",
			"random_pet.site_ap_south_1_p", "random_pet.site_eu_west_p", "random_pet.site_us-east_p",
		}, ","),
	} {
		if got := deps(addr); got != want {
			t.Errorf("%s depends on\n  %s\nwant\n  %s", addr, got, want)
		}
	}
	// A dependency names a record in the converted state, or nothing.
	known := map[string]bool{}
	for _, r := range res.Resources {
		known[r.Address()] = true
	}
	for _, r := range res.Resources {
		for _, d := range r.Dependencies {
			if !known[d] {
				t.Errorf("%s depends on %s, which is not in the converted state", r.Address(), d)
			}
		}
	}
	// A provider alias is part of the provider key; the built-in provider is skipped.
	if r := resourceByAddr(res, "random_pet.aliased"); r.ProviderKey != "random.east" {
		t.Errorf("aliased provider key = %q", r.ProviderKey)
	}
	if resourceByAddr(res, "terraform_data.stamp") != nil {
		t.Error("terraform_data.stamp was converted")
	}
	wantRenames := map[string]string{
		`module.site["eu.west"].random_pet.p`:       "random_pet.site_eu_west_p",
		`module.site["ap.south.1"].null_resource.n`: "null_resource.site_ap_south_1_n",
		`module.mid[1].module.inner.random_pet.p`:   "random_pet.mid_1_inner_p",
		`random_pet.keyed["naïve"]`:                 "random_pet.keyed_na_ve",
		`random_pet.keyed["x y"]`:                   "random_pet.keyed_x_y",
		"null_resource.cnt[1]":                      "null_resource.cnt_1",
	}
	got := map[string]string{}
	for _, r := range res.Renamed {
		got[r.From] = r.To
	}
	for from, to := range wantRenames {
		if got[from] != to {
			t.Errorf("%s -> %q, want %q", from, got[from], to)
		}
	}
}

func TestConvertRealTaintedAndDeposed(t *testing.T) {
	res, err := Convert(readTestdata(t, "taint.tfstate"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(addresses(res), ","), "local_file.web_0,local_file.web_1,null_resource.check,random_pet.server"; got != want {
		t.Fatalf("resources = %s, want %s", got, want)
	}
	// The current object of a replaced instance is kept; the deposed one is not.
	web := resourceByAddr(res, "local_file.web_1")
	var attrs struct{ Filename string }
	if err := json.Unmarshal(web.Attributes, &attrs); err != nil || attrs.Filename != "out/web1-2.txt" {
		t.Errorf("local_file.web_1 attributes = %s (%v)", web.Attributes, err)
	}
	wantNotes := []string{
		"local_file.web[0]: deposed object 2477c3ca skipped (pravo keeps one object per resource; destroy it with tofu first)",
		"local_file.web[1]: deposed object f180dada skipped (pravo keeps one object per resource; destroy it with tofu first)",
		"null_resource.check: tainted in the source state; pravo has no taint, the object is recorded as healthy",
		"random_pet.server: tainted in the source state; pravo has no taint, the object is recorded as healthy",
	}
	if strings.Join(res.Notes, "\n") != strings.Join(wantNotes, "\n") {
		t.Errorf("notes:\n%s\nwant:\n%s", strings.Join(res.Notes, "\n"), strings.Join(wantNotes, "\n"))
	}
	// The null_resource depended on both counted instances.
	if got := strings.Join(resourceByAddr(res, "null_resource.check").Dependencies, ","); got != "local_file.web_0,local_file.web_1" {
		t.Errorf("null_resource.check depends on %q", got)
	}
}

func TestConvertRealKeyCollision(t *testing.T) {
	_, err := Convert(readTestdata(t, "keys-collision.tfstate"))
	want := `random_pet.keyed["a.b"] and random_pet.keyed["a_b"] would both become random_pet.keyed_a_b; rename one of them in OpenTofu first`
	if err == nil || err.Error() != want {
		t.Errorf("error = %v\nwant %s", err, want)
	}
}

func TestConvertRegistryHosts(t *testing.T) {
	// A state written by legacy Terraform names its providers on
	// registry.terraform.io. The same providers are read from the OpenTofu
	// registry. Any other host is kept.
	platform := string(readTestdata(t, "platform.tfstate"))
	legacy := strings.ReplaceAll(platform, "registry.opentofu.org/", "registry.terraform.io/")
	if legacy == platform {
		t.Fatal("the fixture names no provider on registry.opentofu.org")
	}
	a, err := Convert([]byte(platform))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Convert([]byte(legacy))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Resources) != len(b.Resources) {
		t.Fatalf("%d resources, then %d", len(a.Resources), len(b.Resources))
	}
	for i := range a.Resources {
		if a.Resources[i].Provider != b.Resources[i].Provider || b.Resources[i].Provider != "registry.opentofu.org/hashicorp/"+a.Resources[i].ProviderKey {
			t.Errorf("%s: provider %q and %q", a.Resources[i].Address(), a.Resources[i].Provider, b.Resources[i].Provider)
		}
	}
	other := `{"version": 4, "resources": [{"mode": "managed", "type": "acme_thing", "name": "t",
	  "provider": "provider[\"registry.example.com/acme/acme\"]", "instances": [{"attributes": {"id": "1"}}]}]}`
	c, err := Convert([]byte(other))
	if err != nil {
		t.Fatal(err)
	}
	if c.Resources[0].Provider != "registry.example.com/acme/acme" {
		t.Errorf("provider = %q", c.Resources[0].Provider)
	}
}

func TestConvertUnsupportedVersions(t *testing.T) {
	for _, version := range []string{"0", "1", "3", "5"} {
		_, err := Convert([]byte(`{"version": ` + version + `, "resources": []}`))
		want := "unsupported state format version " + version + ": pravo reads format version 4, which OpenTofu and Terraform 0.12 or newer write"
		if err == nil || err.Error() != want {
			t.Errorf("version %s: %v", version, err)
		}
	}
	if _, err := Convert([]byte(`{"resources": []}`)); err == nil || !strings.Contains(err.Error(), "version 0") {
		t.Errorf("no version: %v", err)
	}
}

func TestConvertDependencyEdges(t *testing.T) {
	// A dependency on a resource that has no record names nothing, and a
	// record never depends on itself.
	const doc = `{"version": 4, "resources": [
	  {"mode": "managed", "type": "t", "name": "a", "provider": "provider[\"registry.opentofu.org/h/t\"]",
	   "instances": [{"attributes": {}, "dependencies": ["t.a", "t.gone", "module.gone.t.x", "module.broken[", "t.b"]}]},
	  {"mode": "managed", "type": "t", "name": "b", "provider": "provider[\"registry.opentofu.org/h/t\"]",
	   "instances": [{"attributes": {}}]},
	  {"module": "module.m[\"x\"]", "mode": "managed", "type": "t", "name": "c", "provider": "provider[\"registry.opentofu.org/h/t\"]",
	   "instances": [{"attributes": {}, "dependencies": ["module.m.t.d", "module.m.module.n"]}]},
	  {"module": "module.m[\"y\"]", "mode": "managed", "type": "t", "name": "d", "provider": "provider[\"registry.opentofu.org/h/t\"]",
	   "instances": [{"attributes": {}}]},
	  {"module": "module.m[\"x\"]", "mode": "managed", "type": "t", "name": "d", "provider": "provider[\"registry.opentofu.org/h/t\"]",
	   "instances": [{"attributes": {}}]},
	  {"module": "module.m[\"x\"].module.n", "mode": "managed", "type": "t", "name": "e", "provider": "provider[\"registry.opentofu.org/h/t\"]",
	   "instances": [{"attributes": {}}]}
	]}`
	res, err := Convert([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range res.Resources {
		got[r.Address()] = strings.Join(r.Dependencies, ",")
	}
	want := map[string]string{
		"t.a":       "t.b",
		"t.b":       "",
		"t.m_x_c":   "t.m_x_d,t.m_x_n_e",
		"t.m_x_d":   "",
		"t.m_y_d":   "",
		"t.m_x_n_e": "",
	}
	for addr, deps := range want {
		if got[addr] != deps {
			t.Errorf("%s depends on %q, want %q", addr, got[addr], deps)
		}
	}
}

func TestConvertDependencyNamingAnInstanceKey(t *testing.T) {
	// A dependency that names the key of a module instance keeps that instance
	// only, for a dependent outside the module.
	const doc = `{"version": 4, "resources": [
	  {"mode": "managed", "type": "t", "name": "a", "provider": "provider[\"registry.opentofu.org/h/t\"]",
	   "instances": [{"attributes": {}, "dependencies": ["module.m[\"x\"].t.d"]}]},
	  {"module": "module.m[\"x\"]", "mode": "managed", "type": "t", "name": "d", "provider": "provider[\"registry.opentofu.org/h/t\"]",
	   "instances": [{"attributes": {}}]},
	  {"module": "module.m[\"y\"]", "mode": "managed", "type": "t", "name": "d", "provider": "provider[\"registry.opentofu.org/h/t\"]",
	   "instances": [{"attributes": {}}]}
	]}`
	res, err := Convert([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(resourceByAddr(res, "t.a").Dependencies, ","); got != "t.m_x_d" {
		t.Errorf("t.a depends on %q, want t.m_x_d", got)
	}
}

func TestConvertRealSensitiveAttributesAndDataSource(t *testing.T) {
	// tls.tfstate holds a private key whose attributes OpenTofu marks in
	// sensitive_attributes, a data source that reads its public key, and a
	// sensitive output.
	res, err := Convert(readTestdata(t, "tls.tfstate"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(addresses(res), ","), "data.tls_public_key.k,tls_private_key.k"; got != want {
		t.Fatalf("resources = %s, want %s", got, want)
	}
	key := resourceByAddr(res, "tls_private_key.k")
	var attrs map[string]any
	if err := json.Unmarshal(key.Attributes, &attrs); err != nil {
		t.Fatal(err)
	}
	// sensitive_attributes is not read: every attribute is kept as the
	// provider returned it, and the name is not touched.
	if pem, _ := attrs["private_key_pem"].(string); !strings.HasPrefix(pem, "-----BEGIN EC PRIVATE KEY-----") {
		t.Errorf("private_key_pem = %v", attrs["private_key_pem"])
	}
	if key.Mode != "managed" || key.ProviderKey != "tls" || key.Provider != "registry.opentofu.org/hashicorp/tls" || len(res.Renamed) != 0 {
		t.Errorf("tls_private_key.k = %+v, renamed %v", key, res.Renamed)
	}
	data := resourceByAddr(res, "data.tls_public_key.k")
	if data.Mode != "data" || data.Type != "tls_public_key" || data.Name != "k" || data.Private != nil {
		t.Errorf("data source = %+v", data)
	}
	// The sensitive output is wrapped and the plain one is not.
	v, sensitive := engine.UnwrapOutput(res.Outputs["priv"])
	if !sensitive || !strings.HasPrefix(string(v), `"-----BEGIN EC PRIVATE KEY-----`) {
		t.Errorf("priv = %s (sensitive %v)", res.Outputs["priv"], sensitive)
	}
	if _, sensitive := engine.UnwrapOutput(res.Outputs["pub"]); sensitive {
		t.Error("pub recorded as sensitive")
	}
}
