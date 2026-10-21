// Replace OWNER and NAME, then commit. The path must start with
// github.com/<owner>/ to be published to ghcr.io/<owner>/cue by default.
module: "github.com/OWNER/NAME@v0"
language: version: "v0.14.0"
source: kind: "git"
