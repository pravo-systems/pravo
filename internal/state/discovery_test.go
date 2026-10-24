package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "pravo.state")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func TestGlob(t *testing.T) {
	for _, c := range []struct {
		pattern, s string
		want       bool
	}{
		{"i-1", "i-1", true},
		{"i-1", "i-12", false},
		{"i-*", "i-12", true},
		{"i-*", "i-", true},
		{"*-1", "i-1", true},
		{"*", "", true},
		{"a*b*c", "aXXbYYc", true},
		{"a*b*c", "aXXbYY", false},
		{"i-?", "i-1", true},
		{"i-?", "i-12", false},
		{"arn:aws:s3:::*/logs", "arn:aws:s3:::b/x/logs", true}, // * crosses a slash
		{"[a]", "a", false}, // a bracket is itself
		{"[a]", "[a]", true},
		{"héllo*", "héllo wörld", true},
		{"", "", true},
		{"", "x", false},
	} {
		if got := Glob(c.pattern, c.s); got != c.want {
			t.Errorf("Glob(%q, %q) = %v", c.pattern, c.s, got)
		}
	}
}

func TestListedOf(t *testing.T) {
	l := ListedOf(map[string]any{"id": "i-1", "region": "eu", "account_id": int64(7)}, "web")
	if l.ID != "i-1" || l.ImportID != "i-1" || l.Name != "web" || l.Key != "account_id=7,id=i-1,region=eu" {
		t.Errorf("%+v", l)
	}
	one := ListedOf(map[string]any{"bucket": "logs"}, "")
	if one.ID != "logs" || one.ImportID != "logs" {
		t.Errorf("a one-attribute identity is the id: %+v", one)
	}
	many := ListedOf(map[string]any{"bucket": "logs", "region": "eu"}, "")
	if many.ID != "bucket=logs,region=eu" || many.ImportID != "" {
		t.Errorf("an identity of several attributes without id has no import id: %+v", many)
	}
	nested := ListedOf(map[string]any{"tags": []any{"a", int64(2)}}, "")
	if nested.ImportID != "" || nested.Key != `tags=["a",2]` {
		t.Errorf("%+v", nested)
	}
	if empty := ListedOf(nil, "n"); empty.Key != "" || empty.ID != "" || empty.ImportID != "" {
		t.Errorf("%+v", empty)
	}
}

func TestDiscoveryRunRoundTrip(t *testing.T) {
	s, dir := openTemp(t)
	if run, err := s.Discovery("vms"); run != nil || err != nil {
		t.Fatalf("a discovery that was not run: %v %v", run, err)
	}
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	run := &DiscoveryRun{Name: "web.vms", Type: "aws_instance", Provider: "registry.opentofu.org/hashicorp/aws", ProviderKey: "aws", Digest: "abc",
		Scope: map[string]any{"region": "eu"}, Started: now, Finished: now.Add(time.Second), Actor: "marcin", Status: RunComplete,
		Items: []Listed{ListedOf(map[string]any{"id": "i-1"}, "one")}}
	if err := s.PutDiscovery(run); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "discovery", "runs", "web.vms.json")); err != nil {
		t.Fatalf("the object is not where the layout says: %v", err)
	}
	got, err := s.Discovery("web.vms")
	if err != nil || got == nil {
		t.Fatal(got, err)
	}
	if got.Digest != "abc" || got.V != DiscoveryFormat || len(got.Items) != 1 || got.Items[0].ID != "i-1" || !got.Finished.Equal(run.Finished) || got.Scope["region"] != "eu" {
		t.Errorf("%+v", got)
	}
	// The next run replaces it.
	run.Digest = "def"
	run.Items = nil
	if err := s.PutDiscovery(run); err != nil {
		t.Fatal(err)
	}
	all, problems, err := s.Discoveries()
	if err != nil || len(problems) != 0 || len(all) != 1 || all["web.vms"].Digest != "def" || all["web.vms"].Items == nil {
		t.Fatalf("%+v %v %v", all, problems, err)
	}
	if err := s.DeleteDiscovery("web.vms"); err != nil {
		t.Fatal(err)
	}
	if run, _ := s.Discovery("web.vms"); run != nil {
		t.Error("a deleted run is still there")
	}
	if err := s.PutDiscovery(&DiscoveryRun{}); err == nil {
		t.Error("a run without a name was stored")
	}
	big := &DiscoveryRun{Name: "x", Items: make([]Listed, MaxListed+1)}
	if err := s.PutDiscovery(big); err == nil {
		t.Error("a run over the bound was stored")
	}
}

func TestDiscoveriesSkipWhatCannotBeRead(t *testing.T) {
	s, dir := openTemp(t)
	if err := s.PutDiscovery(&DiscoveryRun{Name: "good", Status: RunComplete}); err != nil {
		t.Fatal(err)
	}
	runs := filepath.Join(dir, "discovery", "runs")
	os.WriteFile(filepath.Join(runs, "bad.json"), []byte("{nope"), 0o600)
	os.WriteFile(filepath.Join(runs, "moved.json"), []byte(`{"v":1,"name":"elsewhere"}`), 0o600)
	os.WriteFile(filepath.Join(runs, "newer.json"), []byte(`{"v":99,"name":"newer"}`), 0o600)
	os.WriteFile(filepath.Join(runs, "note.txt"), []byte("not json"), 0o600)
	all, problems, err := s.Discoveries()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all["good"] == nil {
		t.Errorf("the objects that can be read: %v", all)
	}
	if len(problems) != 3 {
		t.Errorf("problems: %v", problems)
	}
	for _, p := range problems {
		if !strings.HasPrefix(p.Key, "discovery/runs/") || p.Error() == "" {
			t.Errorf("%v", p)
		}
	}
}

func TestAckCoversByIDAndName(t *testing.T) {
	a := &Ack{Discovery: "vms", Match: AckMatch{IDs: []string{"i-0abc*", "bucket=logs,*"}, Names: []string{"batch-*"}}}
	for _, c := range []struct {
		l    Listed
		want bool
	}{
		{ListedOf(map[string]any{"id": "i-0abc123"}, ""), true},
		{ListedOf(map[string]any{"id": "i-0def"}, "batch-7"), true},
		{ListedOf(map[string]any{"id": "i-0def"}, "web"), false},
		{ListedOf(map[string]any{"id": "i-0def"}, ""), false},
		{ListedOf(map[string]any{"bucket": "logs", "region": "eu"}, ""), true},
		{ListedOf(map[string]any{"bucket": "data", "region": "eu"}, ""), false},
		{Listed{ID: "x", Key: "x", ImportID: "i-0abc9"}, true},
	} {
		if got := a.Covers(c.l); got != c.want {
			t.Errorf("Covers(%+v) = %v", c.l, got)
		}
	}
	if (&Ack{Match: AckMatch{Names: []string{"*"}}}).Covers(Listed{ID: "x", Key: "x"}) {
		t.Error("a name pattern matched an object with no name")
	}
}

func TestAckIDIsTheSameForTheSameRule(t *testing.T) {
	a := AckID("vms", AckMatch{IDs: []string{"b", "a"}, Names: []string{"n"}})
	if a != AckID("vms", AckMatch{IDs: []string{"a", "b"}, Names: []string{"n"}}) {
		t.Error("the order of the patterns changed the id")
	}
	if a == AckID("other", AckMatch{IDs: []string{"a", "b"}, Names: []string{"n"}}) || a == AckID("vms", AckMatch{IDs: []string{"a", "b"}}) {
		t.Error("another rule has the same id")
	}
	if !validAckID(a) || validAckID("ack-xyz") || validAckID("../etc/passwd") || validAckID(a+"0") {
		t.Errorf("validAckID: %s", a)
	}
}

func TestPutAckAndWithdraw(t *testing.T) {
	s, dir := openTemp(t)
	s.SetActor("marcin", "", "")
	rec, err := s.PutAck(&Ack{Discovery: "vms", Match: AckMatch{IDs: []string{"i-1"}}, Reason: "the build box"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.V != DiscoveryFormat || rec.By != "marcin" || rec.At.IsZero() || !strings.HasPrefix(rec.ID, "ack-") {
		t.Errorf("%+v", rec)
	}
	if _, err := os.Stat(filepath.Join(dir, "discovery", "ack", rec.ID+".json")); err != nil {
		t.Fatal(err)
	}
	// The same rule again is the same record, with the new reason.
	again, err := s.PutAck(&Ack{Discovery: "vms", Match: AckMatch{IDs: []string{"i-1"}}, Reason: "still the build box"})
	if err != nil || again.ID != rec.ID {
		t.Fatal(again, err)
	}
	other, err := s.PutAck(&Ack{Discovery: "buckets", Match: AckMatch{Names: []string{"logs-*"}}})
	if err != nil {
		t.Fatal(err)
	}
	acks, problems, err := s.Acks()
	if err != nil || len(problems) != 0 || len(acks) != 2 {
		t.Fatalf("%v %v %v", acks, problems, err)
	}
	if acks[0].Discovery != "buckets" || acks[1].Reason != "still the build box" {
		t.Errorf("sorted by discovery, latest reason: %+v %+v", acks[0], acks[1])
	}
	if ok, err := s.DeleteAck(other.ID); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, err := s.DeleteAck(other.ID); ok || err != nil {
		t.Errorf("a record withdrawn twice: %v %v", ok, err)
	}
	if _, err := s.DeleteAck("../../meta"); err == nil {
		t.Error("an id that is a path was accepted")
	}
	if acks, _, _ := s.Acks(); len(acks) != 1 {
		t.Errorf("%v", acks)
	}
}

func TestPutAckRefusesWhatCoversNothingOrEverything(t *testing.T) {
	s, _ := openTemp(t)
	long := strings.Repeat("a", MaxPatternLen+1)
	for name, a := range map[string]*Ack{
		"no discovery":   {Match: AckMatch{IDs: []string{"a"}}},
		"no match":       {Discovery: "d"},
		"an empty id":    {Discovery: "d", Match: AckMatch{IDs: []string{""}}},
		"everything":     {Discovery: "d", Match: AckMatch{IDs: []string{"*"}}},
		"every name":     {Discovery: "d", Match: AckMatch{Names: []string{"**"}}},
		"a long pattern": {Discovery: "d", Match: AckMatch{IDs: []string{long}}},
		"a long reason":  {Discovery: "d", Match: AckMatch{IDs: []string{"a"}}, Reason: strings.Repeat("r", MaxReasonLen+1)},
		"too many":       {Discovery: "d", Match: AckMatch{IDs: make([]string, MaxAckPatterns+1)}},
	} {
		if _, err := s.PutAck(a); err == nil {
			t.Errorf("%s was stored", name)
		}
	}
	// ? is not "everything": it takes one character.
	if _, err := s.PutAck(&Ack{Discovery: "d", Match: AckMatch{IDs: []string{"?"}}}); err != nil {
		t.Error(err)
	}
}

func TestAcksSkipWhatCannotBeRead(t *testing.T) {
	s, dir := openTemp(t)
	good, err := s.PutAck(&Ack{Discovery: "d", Match: AckMatch{IDs: []string{"a"}}})
	if err != nil {
		t.Fatal(err)
	}
	ack := filepath.Join(dir, "discovery", "ack")
	os.WriteFile(filepath.Join(ack, "ack-00000000.json"), []byte("{"), 0o600)
	os.WriteFile(filepath.Join(ack, "ack-11111111.json"), []byte(`{"v":1,"id":"ack-22222222","discovery":"d","match":{"ids":["a"]}}`), 0o600)
	os.WriteFile(filepath.Join(ack, "ack-33333333.json"), []byte(`{"v":1,"id":"ack-33333333","discovery":"d","match":{}}`), 0o600)
	acks, problems, err := s.Acks()
	if err != nil || len(acks) != 1 || acks[0].ID != good.ID || len(problems) != 3 {
		t.Fatalf("%v %v %v", acks, problems, err)
	}
}

func TestCopyCarriesTheDiscoveryObjects(t *testing.T) {
	src, srcDir := openTemp(t)
	if err := src.PutDiscovery(&DiscoveryRun{Name: "vms", Status: RunComplete, Items: []Listed{ListedOf(map[string]any{"id": "i-1"}, "")}}); err != nil {
		t.Fatal(err)
	}
	ack, err := src.PutAck(&Ack{Discovery: "vms", Match: AckMatch{IDs: []string{"i-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	dstDir := filepath.Join(t.TempDir(), "other.state")
	n, err := Copy(context.Background(), &LocalBackend{Dir: srcDir}, mustLocal(t, dstDir))
	if err != nil {
		t.Fatal(err)
	}
	if n < 2 {
		t.Errorf("copied %d objects", n)
	}
	dst, err := Open(dstDir)
	if err != nil {
		t.Fatal(err)
	}
	if run, _ := dst.Discovery("vms"); run == nil || len(run.Items) != 1 {
		t.Errorf("the run was not copied: %+v", run)
	}
	if acks, _, _ := dst.Acks(); len(acks) != 1 || acks[0].ID != ack.ID {
		t.Errorf("the acknowledgement was not copied: %v", acks)
	}
	// A state that holds only discovery objects is not empty.
	if empty, err := holdsNothing(context.Background(), &LocalBackend{Dir: dstDir}); err != nil || empty {
		t.Errorf("holdsNothing = %v, %v", empty, err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatal()
	}
}

func mustLocal(t *testing.T, dir string) *LocalBackend {
	t.Helper()
	b, err := NewLocalBackend(dir)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
