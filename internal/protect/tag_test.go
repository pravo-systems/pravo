package protect

import (
	"regexp"
	"testing"
)

func TestTag(t *testing.T) {
	rd, _ := mustReader(t, "ada")
	_, k1, err := NewMeta("lineage-one", []Reader{rd})
	if err != nil {
		t.Fatal(err)
	}
	_, k2, err := NewMeta("lineage-one", []Reader{rd})
	if err != nil {
		t.Fatal(err)
	}
	a := k1.Tag("tag\x00env=prod")
	if !regexp.MustCompile(`^[0-9a-f]{8}$`).MatchString(a) {
		t.Fatalf("tag %q is not 8 hex characters", a)
	}
	if k1.Tag("tag\x00env=prod") != a {
		t.Fatal("equal values must have equal tags")
	}
	if k1.Tag("tag\x00env=dev") == a {
		t.Fatal("different values have the same tag")
	}
	if k2.Tag("tag\x00env=prod") == a {
		t.Fatal("two states (two data keys) have the same tag for one value")
	}
	k1.Close()
	if got := k1.Tag("x"); got != "" {
		t.Fatalf("a closed key gave the tag %q", got)
	}
}
