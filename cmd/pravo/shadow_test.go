package main

import (
	"fmt"
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

// The owner is in the state, not in a file beside the specification: a deleted note changes
// nothing, a command that changes what a record says is refused while Terraform owns the
// objects, and a root that was handed over is not shadowed again over what pravo changed.
func TestShadowOwnerLivesInTheState(t *testing.T) {
	rt := &runContext{}
	dir := shadowRoot(t, rt)
	if text, code := runPravo(t, rt, dir, "shadow", "terraform.tfstate"); code != 0 {
		t.Fatalf("shadow exited %d:\n%s", code, text)
	}
	if err := os.Remove(filepath.Join(dir, "shadow.lock.json")); err != nil {
		t.Fatal(err)
	}
	src, _ := os.ReadFile(filepath.Join(dir, "shadow.cue"))
	mustWrite(t, dir, "shadow.cue", strings.Replace(string(src), `size: logs.id`, `size: "large"`, 1))

	text, code := runPravo(t, rt, dir, "apply", "-auto-approve")
	if code != 5 {
		t.Fatalf("apply exited %d with the note deleted, want 5:\n%s", code, text)
	}
	contains(t, text, "Terraform still owns its objects")
	text, code = runPravo(t, rt, dir, "shadow", "status")
	if code != 0 {
		t.Fatalf("status exited %d:\n%s", code, text)
	}
	contains(t, text, "Owner: terraform", "is gone")

	for _, args := range [][]string{
		{"state", "rm", "web"},
		{"state", "mv", "web", "web2"},
		{"import", "web", "srv-web"},
	} {
		text, code = runPravo(t, rt, dir, args...)
		if code != 5 {
			t.Fatalf("%v exited %d, want 5:\n%s", args, code, text)
		}
		contains(t, text, "Terraform still owns its objects", "pravo shadow take-over")
	}

	if text, code = runPravo(t, rt, dir, "shadow", "take-over"); code != 0 {
		t.Fatalf("take-over exited %d:\n%s", code, text)
	}
	text, _ = runPravo(t, rt, dir, "shadow", "status")
	contains(t, text, "Owner: pravo")
	if text, code = runPravo(t, rt, dir, "shadow", "take-over"); code == 0 {
		t.Fatalf("a second take-over succeeded:\n%s", text)
	}
	if text, code = runPravo(t, rt, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("apply exited %d after the hand-over:\n%s", code, text)
	}
	if text, code = runPravo(t, rt, dir, "state", "rm", "web"); code != 0 {
		t.Fatalf("state rm exited %d after the hand-over:\n%s", code, text)
	}
	text, code = runPravo(t, rt, dir, "shadow", "--force", "terraform.tfstate")
	if code == 0 {
		t.Fatalf("a root that was handed over was shadowed again:\n%s", text)
	}
	contains(t, text, "handed over to pravo")
}

// In a sealed state a sensitive value that the specification leaves out is kept in the
// vault too, where pravo secret reads it; the owner is signed there, and a deleted note
// changes nothing.
func TestShadowKeepsSensitiveValuesInTheVaultOfASealedState(t *testing.T) {
	rt := &runContext{}
	dir := shadowRoot(t, rt)
	ids := isolateIdentities(t, rt)
	_, ada := person(t, ids, "ada")
	writeReaders(t, filepath.Join(dir, ".pravo", "access"), ada)
	if text, code := runPravo(t, rt, dir, "protect", "init", "--encrypt", "--yes", "--allow-local"); code != 0 {
		t.Fatalf("protect init exited %d:\n%s", code, text)
	}
	text, code := runPravo(t, rt, dir, "shadow", "terraform.tfstate")
	if code != 0 {
		t.Fatalf("shadow exited %d:\n%s", code, text)
	}
	contains(t, text, "sensitive values kept in the vault: 1", "Verified")
	src, _ := os.ReadFile(filepath.Join(dir, "shadow.cue"))
	contains(t, string(src), "a copy is in the vault as shadow/edge.this/secret")
	lacks(t, string(src), "hunter2")
	text, code = runPravo(t, rt, dir, "secret", "get", "shadow/edge.this/secret")
	if code != 0 {
		t.Fatalf("secret get exited %d:\n%s", code, text)
	}
	contains(t, text, "hunter2")

	if err := os.Remove(filepath.Join(dir, "shadow.lock.json")); err != nil {
		t.Fatal(err)
	}
	text, code = runPravo(t, rt, dir, "state", "rm", "web")
	if code != 5 {
		t.Fatalf("state rm exited %d with the note deleted, want 5:\n%s", code, text)
	}
	contains(t, text, "Terraform still owns its objects")
}

// A shadow taken again follows the Terraform state and keeps what a person changed: a block that
// only the person edited stays, a block of the state that moved is new, a resource the state
// gained is a block, and the plan wants the edit and nothing else.
func TestShadowSyncKeepsEditsAndFollowsTheState(t *testing.T) {
	rt := &runContext{}
	dir := shadowRoot(t, rt)
	if text, code := runPravo(t, rt, dir, "shadow", "terraform.tfstate"); code != 0 {
		t.Fatalf("shadow exited %d:\n%s", code, text)
	}
	src, _ := os.ReadFile(filepath.Join(dir, "shadow.cue"))
	mustWrite(t, dir, "shadow.cue", strings.Replace(string(src), `size: logs.id`, `size: "large"`, 1))

	// Terraform applied a change: a new server, and a new serial.
	more := strings.Replace(shadowTfstate, `"serial": 7`, `"serial": 8`, 1)
	more = strings.Replace(more, `  ]
}`, `    ,{"mode": "managed", "type": "demo_server", "name": "api", "provider": "provider[\"registry.opentofu.org/demo/demo\"]",
     "instances": [{"schema_version": 0, "attributes": {"id": "srv-api", "name": "api", "size": "small", "secret": null}}]}
  ]
}`, 1)
	mustWrite(t, dir, "terraform.tfstate", more)

	text, _ := runPravo(t, rt, dir, "shadow", "status")
	contains(t, text, "has moved", "serial 7 then, 8 now")

	text, code := runPravo(t, rt, dir, "shadow", "sync")
	if code != 0 {
		t.Fatalf("sync exited %d:\n%s", code, text)
	}
	contains(t, text, "Shadowed 4 resources", "1 are new", "1 are yours and kept", "all of them in the blocks you edited: web (update)")
	b, _ := os.ReadFile(filepath.Join(dir, "shadow.cue"))
	contains(t, string(b), `size: "large"`, "// demo_server.api", "// demo_bucket.logs")
	lacks(t, string(b), "hunter2")

	// The note moved on: the same sync again changes nothing and still keeps the edit.
	text, code = runPravo(t, rt, dir, "shadow", "sync")
	if code != 0 {
		t.Fatalf("a second sync exited %d:\n%s", code, text)
	}
	contains(t, text, "0 are new", "1 are yours and kept")
	text, _ = runPravo(t, rt, dir, "shadow", "status")
	lacks(t, text, "has moved")
	if text, code = runPravo(t, rt, dir, "apply", "-auto-approve"); code != 5 {
		t.Fatalf("apply exited %d, want 5:\n%s", code, text)
	}
}

// A state with eight servers that differ in their names and their sizes only.
func foldTfstate() string {
	var inst []string
	for i := 1; i <= 8; i++ {
		size := "small"
		if i%4 == 0 {
			size = "large"
		}
		inst = append(inst, fmt.Sprintf(`{"mode": "managed", "type": "demo_server", "name": "web%d", "provider": "provider[\"registry.opentofu.org/demo/demo\"]",
     "instances": [{"schema_version": 0, "attributes": {"id": "srv-%d", "name": "web-%d.internal.example", "size": %q, "secret": null}}]}`, i, i, i, size))
	}
	return `{"version": 4, "terraform_version": "1.13.1", "serial": 3, "lineage": "0e1f6a40-5b9c-4a43-9d3d-2c5a1d0b7e11", "outputs": {}, "resources": [` + strings.Join(inst, ",\n") + `]}`
}

// Resources that are alike become a definition and instances, and the plan of the folded file is
// the proof: it says No changes, and a shadow taken again whole folds the new state again.
func TestShadowFoldWritesDefinitionsAndPlansClean(t *testing.T) {
	rt := &runContext{}
	newDemoProvider(t)
	dir := demoEstate(t, rt, "")
	mustWrite(t, dir, "terraform.tfstate", foldTfstate())

	text, code := runPravo(t, rt, dir, "shadow", "--fold", "terraform.tfstate")
	if code != 0 {
		t.Fatalf("shadow --fold exited %d:\n%s", code, text)
	}
	contains(t, text, "Folded: 1 definitions for 8 resources", "Verified: the plan of the specification says No changes")
	b, _ := os.ReadFile(filepath.Join(dir, "shadow.cue"))
	contains(t, string(b), "#Server: $demo.#Resource.demo_server & {", "web1: #Server & {", "// demo_server.web8")
	if got := strings.Count(string(b), "$demo.#Resource.demo_server"); got != 1 {
		t.Errorf("the resource type is written %d times:\n%s", got, b)
	}
	if text, code = runPravo(t, rt, dir, "plan"); code != 0 {
		t.Fatalf("plan exited %d:\n%s", code, text)
	}
	contains(t, text, "No changes")

	// Untouched, the folded file is taken again whole; edited, it is not merged.
	if text, code = runPravo(t, rt, dir, "shadow", "sync"); code != 0 {
		t.Fatalf("sync of an untouched folded file exited %d:\n%s", code, text)
	}
	contains(t, text, "Folded: 1 definitions for 8 resources")
	mustWrite(t, dir, "shadow.cue", strings.Replace(string(b), `size: "large"`, `size: "huge"`, 1)+"\n")
	text, code = runPravo(t, rt, dir, "shadow", "sync")
	if code == 0 {
		t.Fatalf("an edited folded file was merged:\n%s", text)
	}
	contains(t, text, "was edited since")
}
