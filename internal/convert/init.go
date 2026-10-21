package convert

import "cuelang.org/go/cue/cuecontext"

func init() {
	cueEmptyStruct = cuecontext.New().CompileString("{}")
}
