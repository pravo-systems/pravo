package engine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// inconsistencies lists the places where the object a provider returned from an apply does not
// keep to the plan: a planned value that is known has to come back as it was planned, and an
// unknown one may come back as anything that is known. It is the check that OpenTofu makes
// ("Provider produced inconsistent result after apply"). Values are never named in the
// result, because they may be secrets; only the paths are.
func inconsistencies(planned, actual tftypes.Value) []string {
	var out []string
	compat(planned, actual, nil, &out)
	sort.Strings(out)
	return out
}

func pathString(p []string) string {
	if len(p) == 0 {
		return "(the object)"
	}
	var b strings.Builder
	for _, s := range p {
		if s != "" && s[0] == '[' {
			b.WriteString(s)
		} else {
			b.WriteString("." + s)
		}
	}
	return b.String()
}

func compat(planned, actual tftypes.Value, path []string, out *[]string) {
	add := func(why string) { *out = append(*out, fmt.Sprintf("%s: %s", pathString(path), why)) }
	if !planned.IsKnown() {
		if !actual.IsKnown() {
			add("the provider returned an unknown value")
		}
		return
	}
	if !actual.IsKnown() {
		add("the planned value is known, but the provider returned an unknown value")
		return
	}
	if planned.IsNull() != actual.IsNull() {
		if planned.IsNull() {
			add("planned to be absent, but the provider returned a value")
		} else {
			add("planned to have a value, but the provider returned none")
		}
		return
	}
	if planned.IsNull() {
		return
	}
	t := planned.Type()
	switch {
	case t.Is(tftypes.List{}), t.Is(tftypes.Tuple{}):
		var p, a []tftypes.Value
		if planned.As(&p) != nil || actual.As(&a) != nil {
			return
		}
		if len(p) != len(a) {
			add("the planned list has a different length than the one the provider returned")
			return
		}
		for i := range p {
			compat(p[i], a[i], append(path[:len(path):len(path)], fmt.Sprintf("[%d]", i)), out)
		}
	case t.Is(tftypes.Set{}):
		var p, a []tftypes.Value
		if planned.As(&p) != nil || actual.As(&a) != nil {
			return
		}
		if len(p) != len(a) {
			add("the planned set has a different number of elements than the one the provider returned")
			return
		}
		if planned.IsFullyKnown() && !planned.Equal(actual) {
			add("the planned set differs from the one the provider returned")
		}
	case t.Is(tftypes.Map{}), t.Is(tftypes.Object{}):
		var p, a map[string]tftypes.Value
		if planned.As(&p) != nil || actual.As(&a) != nil {
			return
		}
		keys := make([]string, 0, len(p))
		for k := range p {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			av, ok := a[k]
			if !ok {
				if t.Is(tftypes.Map{}) {
					add("the provider dropped the key " + k)
				}
				continue
			}
			compat(p[k], av, append(path[:len(path):len(path)], k), out)
		}
		if t.Is(tftypes.Map{}) && len(p) != len(a) {
			add("the provider returned other keys than were planned")
		}
	default:
		if !planned.Equal(actual) {
			add("the planned value differs from the one the provider returned")
		}
	}
}
