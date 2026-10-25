package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cueerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/parser"
	cuejson "cuelang.org/go/encoding/json"
	"go.opentelemetry.io/otel/attribute"

	"github.com/pravo-systems/pravo/internal/telemetry"
)

// Policy is a CUE file that a plan must satisfy. Whoever runs pravo names it
// with --policy; the specification has no way to name, weaken or exempt one.
// A plan satisfies a policy when the file unifies with the plan document, the
// data that "pravo plan --json" prints, and the result is concrete: a value the
// policy requires and the plan lacks, or that is unknown until apply, is a
// violation.
type Policy struct {
	// Path is the file as the invoker wrote it.
	Path string
	// Digest is the hex sha256 of the file.
	Digest string

	src   []byte
	value cue.Value
}

// ShortDigest is the first 12 hex digits of the digest.
func (p *Policy) ShortDigest() string { return p.Digest[:12] }

func (p *Policy) label() string { return fmt.Sprintf("%s (sha256:%s)", p.Path, p.ShortDigest()) }

// LoadPolicyFiles reads and checks the policy files before anything else
// happens, so that a policy that cannot be used refuses the run before a
// provider is installed, the lock is written or the state is opened. A file
// must exist, compile on its own, import only the standard library, and not sit
// in rootDir, whose .cue files are the specification.
func LoadPolicyFiles(paths []string, rootDir string) ([]*Policy, error) {
	var out []*Policy
	absRoot, _ := filepath.Abs(rootDir)
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			return nil, refused("--policy needs a file; the value is empty")
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, refused("policy %s: %v", path, err)
		}
		abs, _ := filepath.Abs(path)
		if filepath.Dir(abs) == absRoot {
			return nil, refused("policy %s sits in the root directory, whose .cue files are the specification; keep policies outside it", path)
		}
		file, err := parser.ParseFile(path, src)
		if err != nil {
			return nil, refused("policy %s: %v", path, cueerrors.Details(err, nil))
		}
		for imp := range file.ImportSpecs() {
			ip, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return nil, refused("policy %s: %v", path, err)
			}
			if i := strings.IndexByte(ip, ':'); i >= 0 {
				ip = ip[:i]
			}
			if strings.Contains(strings.SplitN(ip, "/", 2)[0], ".") {
				return nil, refused("policy %s imports %q; a policy is a single file and may import only the CUE standard library", path, ip)
			}
		}
		v := cuecontext.New().CompileBytes(src, cue.Filename(path))
		if err := v.Err(); err != nil {
			return nil, refused("policy %s does not compile: %s", path, cueerrors.Details(err, nil))
		}
		sum := sha256.Sum256(src)
		out = append(out, &Policy{Path: path, Digest: hex.EncodeToString(sum[:]), src: src})
	}
	return out, nil
}

func refused(format string, args ...any) error {
	return WithCode(ExitRefused, fmt.Errorf(format, args...))
}

// SetPolicies builds the policies in the runtime of the root, since values
// unify only within one runtime. It runs after the root is loaded and before
// the first plan.
func (e *Engine) SetPolicies(policies []*Policy) error {
	for _, p := range policies {
		v := e.Root.Ctx.CompileBytes(p.src, cue.Filename(p.Path))
		if err := v.Err(); err != nil {
			return refused("policy %s does not compile: %s", p.Path, cueerrors.Details(err, nil))
		}
		p.value = v
	}
	e.policies = policies
	return nil
}

// HasPolicies reports whether any policy judges this run.
func (e *Engine) HasPolicies() bool { return len(e.policies) > 0 }

// CheckPolicy judges a plan.
func (e *Engine) CheckPolicy(plan *Plan) error {
	if !e.HasPolicies() {
		return nil
	}
	return e.CheckPolicyView(e.NewView(plan, ViewOptions{Skipped: true}))
}

// CheckPolicyView judges a plan document. The caller prints the same document,
// so that what is judged is what the reader saw. Each policy is judged on its
// own and all must hold. A document that does not list the nodes the plan left
// alone is judged with an entry for each of them: a policy sees every node of
// the specification, whether it comes from the plan of this run or from a plan
// file.
func (e *Engine) CheckPolicyView(view *View) error {
	if !e.HasPolicies() {
		return nil
	}
	_, end := telemetry.Span(e.traceCtx(), "engine check policy", attribute.Int("pravo.policies", len(e.policies)))
	err := e.judge(view)
	end(err)
	return err
}

func (e *Engine) judge(view *View) error {
	if !view.skipped {
		judged := *view
		judged.Changes = e.withSkipped(view.Changes)
		view = &judged
	}
	data, err := json.Marshal(view)
	if err != nil {
		return err
	}
	expr, err := cuejson.Extract("plan", data)
	if err != nil {
		return err
	}
	pv := e.Root.Ctx.BuildExpr(expr)
	if err := pv.Err(); err != nil {
		return err
	}
	for _, p := range e.policies {
		unified := p.value.Unify(pv)
		if err := unified.Validate(cue.Concrete(true)); err != nil {
			return refused("the plan violates the policy %s:\n%s", p.label(), cueerrors.Details(err, nil))
		}
	}
	for _, p := range e.policies {
		e.Opts.Progress("Policy %s is satisfied.", p.label())
	}
	return nil
}
