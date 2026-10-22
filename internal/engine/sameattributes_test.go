package engine

import "testing"

func TestSameAttributesTakesNullAndEmptyForTheSame(t *testing.T) {
	for _, c := range []struct {
		name string
		a, b string
		want bool
	}{
		{"equal", `{"id":"x","n":1}`, `{"n":1,"id":"x"}`, true},
		{"null and an empty list", `{"alarm_actions":null}`, `{"alarm_actions":[]}`, true},
		{"null and an empty map", `{"tags":null,"id":"x"}`, `{"id":"x","tags":{}}`, true},
		{"deep", `{"a":{"b":[],"c":{"d":null}}}`, `{"a":{"b":null,"c":{"d":{}}}}`, true},
		{"elements of a list", `{"l":[{"x":[]},{"x":null}]}`, `{"l":[{"x":null},{"x":[]}]}`, true},
		{"a list with an element", `{"a":[]}`, `{"a":["x"]}`, false},
		{"a map with an entry", `{"t":null}`, `{"t":{"k":"v"}}`, false},
		{"an empty string is not null", `{"policy":""}`, `{"policy":null}`, false},
		{"zero is not null", `{"n":0}`, `{"n":null}`, false},
		{"false is not null", `{"b":false}`, `{"b":null}`, false},
		{"a value that differs", `{"a":"x"}`, `{"a":"y"}`, false},
		{"a list that moves", `{"a":["x","y"]}`, `{"a":["y","x"]}`, false},
		{"not JSON", `a`, `a`, true},
		{"not JSON, different", `a`, `b`, false},
	} {
		if got := sameAttributes([]byte(c.a), []byte(c.b)); got != c.want {
			t.Errorf("%s: sameAttributes(%s, %s) = %v, want %v", c.name, c.a, c.b, got, c.want)
		}
	}
}
