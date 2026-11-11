package engine

import "testing"

func TestHasCycle(t *testing.T) {
	for name, c := range map[string]struct {
		edges map[string][]string
		want  bool
	}{
		"none":     {map[string][]string{"a": nil, "b": {"a"}}, false},
		"self":     {map[string][]string{"a": {"a"}}, true},
		"two":      {map[string][]string{"a": {"b"}, "b": {"a"}, "c": nil}, true},
		"diamond":  {map[string][]string{"a": nil, "b": {"a"}, "c": {"a"}, "d": {"b", "c"}}, false},
		"deep":     {map[string][]string{"a": {"c"}, "b": {"a"}, "c": {"b"}}, true},
		"detached": {map[string][]string{"a": nil, "b": nil}, false},
	} {
		if got := HasCycle(c.edges); got != c.want {
			t.Errorf("%s: HasCycle = %v, want %v", name, got, c.want)
		}
	}
}
