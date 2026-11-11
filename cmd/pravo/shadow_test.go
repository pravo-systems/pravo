package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A state of an OpenTofu root with a bucket, a server that is named after the bucket's id,
// and a server in a module whose size the provider refuses and whose secret must not reach
// the specification.
const shadowTfstate = `{
  "version": 4, "terraform_version": "1.13.1", "serial": 7, "lineage": "0e1f6a40-5b9c-4a43-9d3d-2c5a1d0b7e11",
  "outputs": {"bucket_id": {"value": "b-1234", "type": "string"}},
  "resources": [
    {"mode": "managed", "type": "demo_bucket", "name": "logs", "provider": "provider[\"registry.opentofu.org/demo/demo\"]",
     "instances": [{"schema_version": 0, "attributes": {"id": "b-1234", "bucket": "logs"}}]},
    {"mode": "managed", "type": "demo_server", "name": "web", "provider": "provider[\"registry.opentofu.org/demo/demo\"]",
     "instances": [{"schema_version": 0, "attributes": {"id": "srv-web", "name": "web", "size": "b-1234", "secret": null}, "dependencies": ["demo_bucket.logs"]}]},
    {"module": "module.edge", "mode": "managed", "type": "demo_server", "name": "this", "provider": "provider[\"registry.opentofu.org/demo/demo\"]",
     "instances": [{"schema_version": 0, "attributes": {"id": "srv-edge", "name": "edge", "size": "huge", "secret": "hunter2"}, "sensitive_attributes": [[{"type": "get_attr", "value": "secret"}]]}]}
  ]
}
`

func shadowRoot(t *testing.T, rt *runContext) string {
	t.Helper()
	newDemoProvider(t)
	dir := demoEstate(t, rt, "")
	mustWrite(t, dir, "terraform.tfstate", shadowTfstate)
	return dir
}

// A shadow is a specification that plans clean, whose objects stay Terraform's until it
// is told otherwise: it is verified by a plan, it keeps the secret out, it leaves out
// what the provider refuses, and its first plan after the command reads nothing.
func TestShadowPlansCleanAndTerraformKeepsTheObjects(t *testing.T) {
	rt := &runContext{}
	dir := shadowRoot(t, rt)

	text, code := runPravo(t, rt, dir, "shadow", "terraform.tfstate")
	if code != 0 {
		t.Fatalf("shadow exited %d:\n%s", code, text)
	}
	contains(t, text, "Shadowed 3 resources in 1 modules", "Verified: the plan of the specification says No changes", "pravo shadow take-over", "1 invalid", "3 clean", "ignore_changes: 1")
	b, err := os.ReadFile(filepath.Join(dir, "shadow.cue"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	lacks(t, src, "hunter2", `"huge"`)
	contains(t, src, "package estate", "// demo_server.web", "size: logs.id", "edge: {", "$lifecycle: ignore_changes")

	text, code = runPravo(t, rt, dir, "plan")
	if code != 0 {
		t.Fatalf("plan exited %d:\n%s", code, text)
	}
	contains(t, text, "No changes", "not read")
	lacks(t, text, "Refreshing state")

	// Terraform still owns the objects: a change is planned and refused.
	mustWrite(t, dir, "shadow.cue", strings.Replace(src, `size: logs.id`, `size: "large"`, 1))
	text, code = runPravo(t, rt, dir, "plan")
	contains(t, text, "web (demo_server)", "updated in-place", "1 to change")
	if code != 0 {
		t.Fatalf("plan exited %d:\n%s", code, text)
	}
	text, code = runPravo(t, rt, dir, "apply", "-auto-approve")
	if code != 5 {
		t.Fatalf("apply exited %d, want 5:\n%s", code, text)
	}
	contains(t, text, "Terraform still owns its objects", "pravo shadow take-over")

	text, code = runPravo(t, rt, dir, "shadow", "status")
	if code != 0 {
		t.Fatalf("status exited %d:\n%s", code, text)
	}
	contains(t, text, "Owner: terraform", "serial 7")

	text, code = runPravo(t, rt, dir, "shadow", "take-over")
	if code != 0 {
		t.Fatalf("take-over exited %d:\n%s", code, text)
	}
	text, code = runPravo(t, rt, dir, "apply", "-auto-approve")
	if code != 0 {
		t.Fatalf("apply exited %d after the hand-over:\n%s", code, text)
	}

	// The way back: the records are written into the Terraform state again.
	text, code = runPravo(t, rt, dir, "shadow", "back", "--out", "back.tfstate")
	if code != 0 {
		t.Fatalf("back exited %d:\n%s", code, text)
	}
	back, err := os.ReadFile(filepath.Join(dir, "back.tfstate"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, string(back), `"serial": 8`, `"large"`, "hunter2")
}

// A shadow refuses to replace what is there without --force, and a failed run puts the old
// file back.
func TestShadowKeepsWhatIsThere(t *testing.T) {
	rt := &runContext{}
	dir := shadowRoot(t, rt)
	if text, code := runPravo(t, rt, dir, "shadow", "terraform.tfstate"); code != 0 {
		t.Fatalf("shadow exited %d:\n%s", code, text)
	}
	text, code := runPravo(t, rt, dir, "shadow", "terraform.tfstate")
	if code == 0 {
		t.Fatalf("a second shadow replaced the first:\n%s", text)
	}
	contains(t, text, "shadow.cue exists", "--force")

	before, _ := os.ReadFile(filepath.Join(dir, "shadow.cue"))
	mustWrite(t, dir, "broken.tfstate", `{"version": 3}`)
	if text, code = runPravo(t, rt, dir, "shadow", "--force", "broken.tfstate"); code == 0 {
		t.Fatalf("a state of another format was shadowed:\n%s", text)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "shadow.cue"))
	if string(before) != string(after) {
		t.Errorf("a failed run changed shadow.cue:\n%s", after)
	}
	if _, err := os.Stat(filepath.Join(dir, "shadow.cue.replaced")); err == nil {
		t.Error("the file that was put aside is still there")
	}
}
