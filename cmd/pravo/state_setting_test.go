package main

import (
	"strings"
	"testing"
)

// A state setting that comes from a tag is named in the error with the tag, and a default on
// the tag gives it a value. Neither prints a broken format verb.
func TestStateSettingFromATag(t *testing.T) {
	rt := &runContext{}
	spec := func(tag string) string {
		return "package x\n\n_env: bucket: " + tag + "\n\n$settings: {state: {backend: \"s3\", bucket: _env.bucket, prefix: \"p\", region: \"us-east-1\"}}\n"
	}
	t.Run("a tag without a value", func(t *testing.T) {
		dir := specDir(t, map[string]string{"main.cue": spec("string @tag(state_bucket)")})
		for _, cmd := range []string{"validate", "plan"} {
			out, code := runPravo(t, rt, dir, cmd)
			if code == 0 {
				t.Fatalf("%s succeeded without the tag:\n%s", cmd, out)
			}
			want := "$settings.state.bucket is not set: it comes from @tag(state_bucket); pass -t state_bucket=..."
			if !strings.Contains(out, want) {
				t.Errorf("%s: no %q in\n%s", cmd, want, out)
			}
		}
	})
	t.Run("a tag with a default", func(t *testing.T) {
		dir := specDir(t, map[string]string{"main.cue": spec(`string | *"x" @tag(state_bucket)`)})
		if out, code := runPravo(t, rt, dir, "validate"); code != 0 {
			t.Fatalf("validate: exit %d\n%s", code, out)
		}
	})
	t.Run("a field that is not a string", func(t *testing.T) {
		dir := specDir(t, map[string]string{"main.cue": spec("string")})
		out, code := runPravo(t, rt, dir, "validate")
		if code == 0 || !strings.Contains(out, "$settings.state.bucket is not concrete") {
			t.Errorf("exit %d\n%s", code, out)
		}
	})
}
