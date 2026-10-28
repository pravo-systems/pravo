package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"cuelang.org/go/cue"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

const (
	secretOne = "first-planted-secret-value"
	secretTwo = "second-planted-secret-value"
)

// secretHarness is a harness whose state is sealed to one identity, so that it can keep
// secrets, and the options that make a run read them with the readers file of that identity.
func secretHarness(t *testing.T) (*harness, Options) {
	t.Helper()
	ctx := context.Background()
	h := newHarness(t)
	id := state.NewStoreIdentity
	if id == nil {
		var err error
		if id, err = protect.NewIdentity("ada", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := h.state.Upgrade(ctx, state.UpgradeOptions{
			To: state.FormatSealed, Readers: []protect.Reader{id.AsReader()}, Identity: id, By: "ada",
		}); err != nil {
			t.Fatal(err)
		}
		st, err := state.OpenBackendWith(ctx, h.backend, state.Options{Identity: id})
		if err != nil {
			t.Fatal(err)
		}
		h.state = st
	}
	return h, Options{Readers: []protect.Reader{id.AsReader()}}
}

func (h *harness) setSecret(name, value string) state.SecretInfo {
	h.t.Helper()
	info, err := h.state.SetSecret(context.Background(), name, []byte(value), "", "pravo secret set "+name)
	if err != nil {
		h.t.Fatalf("setting %s: %v", name, err)
	}
	return info
}

// leaks lists where the text is found in what a run leaves behind: the records, the
// transactions, the outputs, the plan, the saved plan, and the plan that is stored.
func (h *harness) leaks(t *testing.T, e *Engine, plan *Plan, text string) []string {
	t.Helper()
	var found []string
	look := func(what string, data any) {
		b, err := json.Marshal(data)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if strings.Contains(string(b), text) {
			found = append(found, what)
		}
	}
	for _, addr := range h.state.Addresses() {
		look("the record of "+addr, h.state.Get(addr))
	}
	txns, err := h.state.Transactions()
	if err != nil {
		t.Fatal(err)
	}
	look("the transactions", txns)
	look("the outputs", h.state.Outputs())
	if plan != nil {
		for _, ch := range plan.Changes {
			look("the plan change "+ch.Addr, []any{ch.Config.String(), ch.Prior.String(), ch.Planned.String(), ch.Reasons, ch.Warnings})
		}
		look("the plan statistics", plan.Stats)
		file := filepath.Join(t.TempDir(), "p.plan")
		if err := e.SavePlan(file, plan, nil, false); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), text) {
			found = append(found, "the plan file")
		}
	}
	return found
}

// A secret of the sealed state reaches the provider through its configuration, and
// nothing a run leaves behind holds it: not a record, a transaction or a plan.
func TestSecretReachesTheProviderAndLeavesNoTrace(t *testing.T) {
	ctx := context.Background()
	h, opts := secretHarness(t)
	h.setSecret("prod/token", secretOne)
	s := spec{
		cue:       "sec: {name: \"prod/token\", value: string}\nprov: {region: sec.value}\na: " + thing("a", `content: "x"`) + "\n",
		resources: []string{"a"}, secrets: []string{"sec"}, providers: map[string]string{"fake": "prov"},
	}
	e := h.engine(s, opts)
	plan, err := e.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := h.fake.regions(); len(got) == 0 || got[len(got)-1] != secretOne {
		t.Fatalf("the provider was configured with %q, want it to end with the secret", got)
	}
	if _, err := e.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if got := h.leaks(t, e, plan, secretOne); len(got) > 0 {
		t.Errorf("the secret is in %s", strings.Join(got, ", "))
	}
	if n := h.state.Get("sec"); n != nil {
		t.Errorf("the secret has a record: %+v", n)
	}
	if d := h.state.Get("a").ProviderConfig; d != "" {
		t.Errorf("the record of a holds the digest %q of a configuration made of a secret", d)
	}
	// The next run reads the new version. A new version of a secret that nothing records
	// does not change the plan by itself: only the node that is planned for another reason
	// is planned with the provider configured by it.
	h.setSecret("prod/token", secretTwo)
	e = h.engine(s, opts)
	plan, err = e.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if add, change, destroy, _ := plan.Counts(); add+change+destroy != 0 {
		t.Errorf("a new secret, used for the provider only, adds %d, changes %d and destroys %d", add, change, destroy)
	}
	s.cue = strings.Replace(s.cue, `content: "x"`, `content: "y"`, 1)
	e = h.engine(s, opts)
	plan, err = e.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if add, change, destroy, _ := plan.Counts(); add != 0 || change != 1 || destroy != 0 {
		t.Fatalf("the plan adds %d, changes %d and destroys %d, want one change", add, change, destroy)
	}
	if got := h.fake.regions(); got[len(got)-1] != secretTwo {
		t.Errorf("the provider was configured with %q, want the second secret last", got)
	}
	if _, err := e.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if got := h.leaks(t, e, plan, secretTwo); len(got) > 0 {
		t.Errorf("the second secret is in %s", strings.Join(got, ", "))
	}
}

// A secret is ephemeral: it may be used in provider configuration, a write-only attribute
// and another ephemeral resource, and in nothing that is recorded.
func TestSecretIsKeptOutOfWhatIsRecorded(t *testing.T) {
	ctx := context.Background()
	for name, s := range map[string]spec{
		"an attribute of a resource": {
			cue:       "sec: {name: \"prod/token\", value: string}\na: " + thing("a", `content: sec.value`) + "\n",
			resources: []string{"a"}, secrets: []string{"sec"},
		},
		"an attribute built from it": {
			cue:       "sec: {name: \"prod/token\", value: string}\na: " + thing("a", `content: "token-\(sec.value)"`) + "\n",
			resources: []string{"a"}, secrets: []string{"sec"},
		},
		"an output": {
			cue:       "sec: {name: \"prod/token\", value: string}\nshown: {value: sec.value}\n",
			outputs:   []string{"shown"},
			secrets:   []string{"sec"},
			resources: nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			h, opts := secretHarness(t)
			h.setSecret("prod/token", secretOne)
			e := h.engine(s, opts)
			_, err := e.Plan(ctx)
			if err == nil {
				t.Fatal("the plan was made")
			}
			if strings.Contains(err.Error(), secretOne) {
				t.Errorf("the error holds the secret: %v", err)
			}
			if !strings.Contains(err.Error(), "sec") || !strings.Contains(err.Error(), "ephemeral") {
				t.Errorf("the error does not say why: %v", err)
			}
		})
	}
}

// The number of the version is no secret: it is an ordinary value, and a new version of the
// secret changes what is built from it, so that the plan changes the resource that holds
// the number and the provider reads the new value.
func TestSecretVersionRotatesWhatReadsIt(t *testing.T) {
	ctx := context.Background()
	h, opts := secretHarness(t)
	h.setSecret("prod/token", secretOne)
	s := spec{
		cue: "sec: {name: \"prod/token\", value: string}\nrev: {name: \"prod/token\", version: int}\n" +
			"prov: {region: sec.value}\na: " + thing("a", `content: "version-\(rev.version)"`) + "\n",
		resources: []string{"a"}, secrets: []string{"sec"}, secretVersions: []string{"rev"}, providers: map[string]string{"fake": "prov"},
	}
	h.apply2(t, s, opts)
	if got := h.state.Get("a"); got == nil || !strings.Contains(string(got.Attributes), "version-1") {
		t.Fatalf("the record of a after the first version: %+v", got)
	}

	// Nothing changed: nothing to do.
	e := h.engine(s, opts)
	plan, err := e.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if add, change, destroy, _ := plan.Counts(); add+change+destroy != 0 {
		t.Fatalf("with no new version the plan adds %d, changes %d and destroys %d", add, change, destroy)
	}

	h.setSecret("prod/token", secretTwo)
	e = h.engine(s, opts)
	plan, err = e.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if add, change, destroy, _ := plan.Counts(); add != 0 || change != 1 || destroy != 0 {
		t.Fatalf("with a new version the plan adds %d, changes %d and destroys %d, want one change", add, change, destroy)
	}
	if got := h.fake.regions(); got[len(got)-1] != secretTwo {
		t.Errorf("the provider was configured with %q, want the second secret last", got)
	}
	if _, err := e.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if got := h.state.Get("a"); !strings.Contains(string(got.Attributes), "version-2") {
		t.Errorf("the record of a after the second version: %s", got.Attributes)
	}
	if got := h.leaks(t, e, plan, secretTwo); len(got) > 0 {
		t.Errorf("the secret is in %s", strings.Join(got, ", "))
	}
}

// apply2 plans and applies the specification with these options.
func (h *harness) apply2(t *testing.T, s spec, opts Options) *ApplyResult {
	t.Helper()
	e := h.engine(s, opts)
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	res, err := e.Apply(context.Background(), plan)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return res
}

// A node can ask for a version. The version it names is read, whatever was set since, and a
// version that does not exist, or was purged, stops the run and says so.
func TestSecretAskedForAVersion(t *testing.T) {
	ctx := context.Background()
	h, opts := secretHarness(t)
	h.setSecret("prod/token", secretOne)
	h.setSecret("prod/token", secretTwo)

	read := func(version string) (string, error) {
		s := spec{
			cue:       "sec: {name: \"prod/token\", " + version + "value: string}\nprov: {region: sec.value}\na: " + thing("a", `content: "x"`) + "\n",
			resources: []string{"a"}, secrets: []string{"sec"}, providers: map[string]string{"fake": "prov"},
		}
		_, err := h.engine(s, opts).Plan(ctx)
		if err != nil {
			return "", err
		}
		regions := h.fake.regions()
		return regions[len(regions)-1], nil
	}
	if got, err := read("version: 1, "); err != nil || got != secretOne {
		t.Errorf("version 1 read %q, %v", got, err)
	}
	if got, err := read("version: 2, "); err != nil || got != secretTwo {
		t.Errorf("version 2 read %q, %v", got, err)
	}
	if got, err := read(""); err != nil || got != secretTwo {
		t.Errorf("no version read %q, %v", got, err)
	}
	if _, err := read("version: 3, "); err == nil || !strings.Contains(err.Error(), "versions 1 to 2") {
		t.Errorf("a version that is not there: %v", err)
	}
	if _, err := h.state.PurgeSecret(ctx, "prod/token", 1, "pravo secret purge prod/token"); err != nil {
		t.Fatal(err)
	}
	if _, err := read("version: 1, "); err == nil || !strings.Contains(err.Error(), "purged") || !strings.Contains(err.Error(), "leave the version out") {
		t.Errorf("a version that was purged: %v", err)
	}
	if got, err := read(""); err != nil || got != secretTwo {
		t.Errorf("the latest after a purge read %q, %v", got, err)
	}
}

// A secret that is not there, or was removed, stops the run with what to do about it, and the
// error has not the name of anything but the secret.
func TestSecretThatIsNotThereStopsTheRun(t *testing.T) {
	ctx := context.Background()
	h, opts := secretHarness(t)
	s := spec{
		cue:       "sec: {name: \"prod/token\", value: string}\nprov: {region: sec.value}\na: " + thing("a", `content: "x"`) + "\n",
		resources: []string{"a"}, secrets: []string{"sec"}, providers: map[string]string{"fake": "prov"},
	}
	_, err := h.engine(s, opts).Plan(ctx)
	if err == nil || !strings.Contains(err.Error(), "no secret named prod/token") || !strings.Contains(err.Error(), "pravo secret set prod/token") {
		t.Errorf("a secret that was never set: %v", err)
	}
	h.setSecret("prod/token", secretOne)
	if _, err := h.state.RemoveSecret(ctx, "prod/token", "pravo secret rm prod/token"); err != nil {
		t.Fatal(err)
	}
	_, err = h.engine(s, opts).Plan(ctx)
	if err == nil || !strings.Contains(err.Error(), "was removed") || !strings.Contains(err.Error(), "pravo secret set prod/token") {
		t.Errorf("a secret that was removed: %v", err)
	}
	h.setSecret("prod/token", secretTwo)
	if _, err := h.engine(s, opts).Plan(ctx); err != nil {
		t.Errorf("a secret that was set again: %v", err)
	}
	if got := h.fake.called("ApplyResourceChange"); got != 0 {
		t.Errorf("a run that could not read its secret changed %d resources", got)
	}
}

// A state that is not sealed keeps no secrets, so a run that names one is refused and the
// refusal is the exit code of one.
func TestSecretNeedsASealedState(t *testing.T) {
	unsealedStart(t)
	h := newHarness(t)
	s := spec{
		cue:       "sec: {name: \"prod/token\", value: string}\nprov: {region: sec.value}\na: " + thing("a", `content: "x"`) + "\n",
		resources: []string{"a"}, secrets: []string{"sec"}, providers: map[string]string{"fake": "prov"},
	}
	_, err := h.engine(s, Options{}).Plan(context.Background())
	if err == nil {
		t.Fatal("the plan was made")
	}
	if !errors.Is(err, state.ErrSecretsNeedSeal) || codeOf(err) != ExitRefused || !strings.Contains(err.Error(), "pravo protect init --encrypt") {
		t.Errorf("exit code %d for: %v", codeOf(err), err)
	}
}

// The error of a secret says where the secret is, and carries the exit code of what is wrong
// with it, whatever sits around it.
func TestSecretErrorsKeepTheirCodes(t *testing.T) {
	e := &Engine{}
	r := &config.Resource{Mode: "ephemeral", Type: "secret", Name: "sec", Path: cue.ParsePath("sec"), Builtin: true}
	for _, c := range []struct {
		err  error
		code ExitCode
	}{
		{state.ErrSecretsNeedSeal, ExitRefused},
		{state.ErrUntrusted, ExitUntrusted},
		{errors.Join(errors.New("the version 2"), state.ErrUntrusted), ExitUntrusted},
	} {
		err := e.secretError(r, "prod/token", c.err)
		if got := codeOf(err); got != c.code {
			t.Errorf("%v: exit code %d, want %d", c.err, got, c.code)
		}
		if !strings.Contains(err.Error(), "sec") {
			t.Errorf("%v: the error does not say which node: %v", c.err, err)
		}
	}
}

// A run reads one version of a secret: the one that the first node to name it settles, even
// when the secret is set while the run goes on. The next run reads the new one.
func TestSecretRunReadsOneVersion(t *testing.T) {
	ctx := context.Background()
	h, opts := secretHarness(t)
	h.setSecret("prod/token", secretOne)
	e := h.engine(spec{cue: "x: 1\n"}, opts)
	rules := state.RecordRules{Readers: opts.Readers}

	e.resetRun(true)
	first, err := e.secretVersion(ctx, "prod/token", 0, rules)
	if err != nil || first != 1 {
		t.Fatalf("the first node reads version %d, %v", first, err)
	}
	h.setSecret("prod/token", secretTwo)
	if again, err := e.secretVersion(ctx, "prod/token", 0, rules); err != nil || again != 1 {
		t.Errorf("another node of the same run reads version %d, %v; want 1", again, err)
	}
	if pinned, err := e.secretVersion(ctx, "prod/token", 2, rules); err != nil || pinned != 2 {
		t.Errorf("a node that asks for version 2 reads %d, %v", pinned, err)
	}
	e.resetRun(false)
	if next, err := e.secretVersion(ctx, "prod/token", 0, rules); err != nil || next != 2 {
		t.Errorf("the next run reads version %d, %v; want 2", next, err)
	}
}

// Nodes of one run that name a secret all read the version that the first settles, even
// when they run together.
func TestSecretNodesRunningTogetherAgree(t *testing.T) {
	ctx := context.Background()
	h, opts := secretHarness(t)
	h.setSecret("prod/token", secretOne)
	e := h.engine(spec{cue: "x: 1\n"}, opts)
	rules := state.RecordRules{Readers: opts.Readers}
	e.resetRun(true)
	var wg sync.WaitGroup
	got := make([]int, 16)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i == 8 {
				if _, err := h.state.SetSecret(ctx, "prod/token", []byte(secretTwo), "", "pravo secret set prod/token"); err != nil {
					t.Error(err)
				}
			}
			got[i], _ = e.secretVersion(ctx, "prod/token", 0, rules)
		}()
	}
	wg.Wait()
	for _, v := range got {
		if v != got[0] {
			t.Fatalf("the nodes of one run read the versions %v", got)
		}
	}
}

// The plan file of a run that uses a secret for the provider's configuration holds no
// value of it and stays applicable: the apply reads the secret again.
func TestSavedPlanOfASecretRunHoldsNoSecret(t *testing.T) {
	ctx := context.Background()
	h, opts := secretHarness(t)
	h.setSecret("prod/token", secretOne)
	s := spec{
		cue:       "sec: {name: \"prod/token\", value: string}\nprov: {region: sec.value}\na: " + thing("a", `content: "x"`) + "\n",
		resources: []string{"a"}, secrets: []string{"sec"}, providers: map[string]string{"fake": "prov"},
	}
	e := h.engine(s, opts)
	plan, err := e.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "p.plan")
	if err := e.SavePlan(file, plan, nil, false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secretOne) {
		t.Errorf("the plan file holds the secret")
	}
	if plan.Stats.Opened != 0 {
		t.Errorf("a secret that is read from the state counts as %d opened ephemeral resources", plan.Stats.Opened)
	}
}

// codeOf is the exit code of an error: that of the code it carries, or 1.
func codeOf(err error) ExitCode {
	var ce *CodedError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return ExitError
}

// A value that the specification does not accept for a secret is an error that does not show
// the value.
func TestSecretThatFailsAConstraintIsNotShown(t *testing.T) {
	h, opts := secretHarness(t)
	h.setSecret("prod/token", secretOne)
	s := spec{
		cue:       "sec: {name: \"prod/token\", value: =~\"^[0-9]+$\"}\nprov: {region: sec.value}\na: " + thing("a", `content: "x"`) + "\n",
		resources: []string{"a"}, secrets: []string{"sec"}, providers: map[string]string{"fake": "prov"},
	}
	_, err := h.engine(s, opts).Plan(context.Background())
	if err == nil {
		t.Fatal("the plan was made")
	}
	if strings.Contains(err.Error(), secretOne) || strings.Contains(err.Error(), "planted") {
		t.Errorf("the error shows the secret: %v", err)
	}
	if !strings.Contains(err.Error(), "sec") || !strings.Contains(err.Error(), "not shown") {
		t.Errorf("the error does not say which node, or that the value is not shown: %v", err)
	}
}
