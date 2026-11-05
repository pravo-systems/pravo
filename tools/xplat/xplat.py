#!/usr/bin/env python3
"""Cross-platform behaviour of pravo: one suite, run on Linux, macOS and Windows.

    python3 tools/xplat/xplat.py --pravo bin/pravo --out out/linux [--repeat 3] [--only lifecycle,names]

Every case ends in PASS, FAIL (with a reason) or SKIP. A case also records
observations: normalised facts (exit codes, output lines, file hashes, the names
of the files in the state). compare.py sets the observations of the systems side
by side: a key that differs between systems is a finding unless the case marked it
"local" (a fact that depends on the system by nature, such as a path). With
--repeat N the suite runs N times and every repeat must record the same
observations as the first, which is the flake check.

The suite needs only the standard library and the network, once, to fetch four
providers (random, local, null, time). It touches nothing outside its --out
directory and a scratch directory under it.
"""
import argparse
import json
import os
import re
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
import traceback
import hashlib
import http.client

WINDOWS = os.name == "nt"
MAC = sys.platform == "darwin"
SYSTEM = "windows" if WINDOWS else "darwin" if MAC else "linux"

HEADER = '''package x

import (
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
	nl "pravo.dev/providers/hashicorp/null:null_"
	tm "pravo.dev/providers/hashicorp/time"
)

$local:  loc.#Provider
$random: rnd.#Provider
$null:   nl.#Provider
$time:   tm.#Provider

'''

# A small estate with a deterministic answer: the integer has a seed.
LIFE = HEADER + '''r: $random.#Resource.random_integer & {min: 1, max: 1000000, seed: "xplat"}
f: $local.#Resource.local_file & {filename: "out/f.txt", content: "n=\\(r.result)\\nsecond line\\n"}
n: $null.#Resource.null_resource & {triggers: v: "\\(r.result)"}
'''

# One resource that takes a while, and a tag that makes the next apply touch it again.
SLOW = HEADER + '''gen:  *"0" | string @tag(gen)
wait: *"6s" | string @tag(wait)
s: $time.#Resource.time_sleep & {create_duration: wait, triggers: v: gen}
'''


def sha(b):
    return hashlib.sha256(b).hexdigest()[:16]


class Skip(Exception):
    pass


class R:
    def __init__(self, rc, out, err):
        self.rc, self.out, self.err = rc, out, err

    @property
    def all(self):
        return self.out + self.err


class Ctx:
    def __init__(self, pravo, root, name):
        self.pravo = pravo
        self.root = root
        self.name = name
        self.obs_ = {}
        self.local_ = set()
        self.fails = []
        self.home = os.path.join(root, "home é")
        self.tmp = os.path.join(root, "tmp dir")
        for d in (self.home, self.tmp):
            os.makedirs(d, exist_ok=True)
        self.base = None
        self.procs = []

    # ---- environment
    def env(self, extra=None):
        keep = ["PATH", "SystemRoot", "SYSTEMDRIVE", "SystemDrive", "COMSPEC", "ComSpec", "PATHEXT", "WINDIR", "windir",
                "PROCESSOR_ARCHITECTURE", "NUMBER_OF_PROCESSORS", "OS", "LANG", "LC_ALL"]
        e = {k: os.environ[k] for k in keep if k in os.environ}
        e["HOME"] = self.home
        e["USERPROFILE"] = self.home
        e["APPDATA"] = os.path.join(self.home, "AppData", "Roaming")
        e["LOCALAPPDATA"] = os.path.join(self.home, "AppData", "Local")
        for k in ("TMPDIR", "TMP", "TEMP"):
            e[k] = self.tmp
        e["NO_COLOR"] = "1"
        e["PRAVO_ACTOR"] = "xplat"
        if extra:
            e.update(extra)
        return e

    def popen(self, args, cwd, extra=None):
        kw = {}
        if WINDOWS:
            kw["creationflags"] = subprocess.CREATE_NEW_PROCESS_GROUP
        p = subprocess.Popen([self.pravo] + args, cwd=cwd, env=self.env(extra), stdout=subprocess.PIPE, stderr=subprocess.PIPE, **kw)
        self.procs.append(p)
        return p

    def run(self, args, cwd=None, extra=None, timeout=240, stdin=None):
        p = subprocess.run([self.pravo] + args, cwd=cwd, env=self.env(extra), capture_output=True, timeout=timeout,
                           input=stdin if stdin is None or isinstance(stdin, bytes) else stdin.encode())
        return R(p.returncode, p.stdout.decode("utf-8", "replace"), p.stderr.decode("utf-8", "replace"))

    def reap(self):
        for p in self.procs:
            if p.poll() is None:
                p.kill()
            try:
                p.communicate(timeout=10)
            except Exception:
                pass

    # ---- normal form of a text
    def norm(self, s, root=None):
        for r in {self.root, os.path.realpath(self.root)} | ({root} if root else set()):
            for form in (r, r.replace("\\", "/"), r.replace("\\", "\\\\")):
                s = s.replace(form, "<W>")
        # a path that begins <W> keeps one kind of slash
        s = re.sub(r"<W>[^\s\"'`:]*", lambda m: m.group(0).replace("\\\\", "/").replace("\\", "/"), s)
        s = s.replace("\r\n", "\n")
        s = re.sub(r"\d{8}T\d{6}Z-[0-9a-f]{6}", "<txn>", s)
        s = re.sub(r"\d{4}-\d\d-\d\d[T ]\d\d:\d\d:\d\d(\.\d+)?(Z| ?[+-]\d\d:?\d\d)?", "<time>", s)
        s = re.sub(r"\b(after|in|for) [0-9.]+(ms|s|m)\b", r"\1 <dur>", s)
        s = re.sub(r"\[id=[0-9a-f]{40}\]", "[id=<sha1>]", s)
        s = re.sub(r"\[id=\d{15,}\]", "[id=<num>]", s)
        s = re.sub(r"pid \d+", "pid <pid>", s)
        s = re.sub(r'(\bid:\s+")\d{15,}"', r'\1<num>"', s)
        s = re.sub(r"x25519:[A-Za-z0-9_-]{20,}", "x25519:<key>", s)
        s = re.sub(r"\bon [A-Za-z0-9._-]+ since", "on <host> since", s)
        s = re.sub(r"\bon host [A-Za-z0-9._-]+\)", "on host <host>)", s)
        s = re.sub(r"\b\d+(\.\d+)?s ago\b", "<ago>", s)
        s = re.sub(r"\b\d+(\.\d+)?(ms|s)\b$", "<dur>", s, flags=re.M)
        return s.strip()

    def lines(self, s):
        """Sorted normalised lines: the order of parallel work is not a fact."""
        return sorted(l.rstrip() for l in self.norm(s).split("\n") if l.strip())

    # ---- results
    def obs(self, key, value, local=False):
        k = f"{self.name}.{key}"
        self.obs_[k] = value
        if local:
            self.local_.add(k)

    def check(self, cond, msg):
        if not cond:
            self.fails.append(msg)
        return cond

    def expect(self, r, rc, what):
        ok = r.rc == rc
        if not ok:
            self.fails.append(f"{what}: exit {r.rc}, wanted {rc}: " + self.norm(r.all)[-300:].replace("\n", " | "))
        return ok

    # ---- directories
    def init_base(self):
        """One directory that ran `pravo init` with the four providers; cases copy its modules and lock file."""
        b = os.path.join(self.root, "base")
        os.makedirs(b)
        write(os.path.join(b, "main.cue"), LIFE)
        t0 = time.time()
        r = self.run(["init"], cwd=b, timeout=600)
        for _ in range(2):  # GitHub answers a 504 now and then, after pravo's own tries: that is not what is measured here
            if r.rc == 0 or "504" not in r.all and "502" not in r.all and "503" not in r.all:
                break
            time.sleep(20)
            r = self.run(["init"], cwd=b, timeout=600)
        self.base_init = (r, time.time() - t0)
        self.base = b
        return r

    def new(self, name, spec=None, files=None):
        d = os.path.join(self.root, name)
        os.makedirs(d)
        for item in ("cue.mod", "pravo.lock.cue"):
            s = os.path.join(self.base, item)
            if os.path.isdir(s):
                shutil.copytree(s, os.path.join(d, item))
            else:
                shutil.copy(s, d)
        if spec is not None:
            write(os.path.join(d, "main.cue"), spec)
        for rel, body in (files or {}).items():
            write(os.path.join(d, rel), body)
        return d


def write(path, text, newline="\n", bom=False):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    data = text.replace("\n", newline).encode("utf-8")
    if bom:
        data = b"\xef\xbb\xbf" + data
    with open(path, "wb") as f:
        f.write(data)


def read(path):
    with open(path, "rb") as f:
        return f.read()


def tree(d, skip=()):
    out = []
    for dp, dn, fn in os.walk(d):
        for n in fn:
            p = os.path.relpath(os.path.join(dp, n), d).replace("\\", "/")
            if not any(p.startswith(s) for s in skip):
                out.append(p)
    return sorted(out)


def state_tree(d, c):
    """The names of the files of the state, the ones that depend on a run made equal."""
    names = tree(os.path.join(d, "pravo.state"))
    return [re.sub(r"\d{8}T\d{6}Z-[0-9a-f]{6}", "<txn>", n) for n in names]


def provider_processes():
    """Names of the provider processes that run now."""
    try:
        if WINDOWS:
            out = subprocess.run(["tasklist", "/FO", "CSV", "/NH"], capture_output=True, text=True).stdout
            names = [l.split('","')[0].strip('"') for l in out.splitlines() if l.strip()]
        else:
            out = subprocess.run(["ps", "-A", "-o", "comm="], capture_output=True, text=True).stdout
            names = [l.strip() for l in out.splitlines()]
    except Exception:
        return None
    return sorted(n for n in names if n.lower().startswith("terraform-provi"))


def wait_until(fn, seconds, step=0.2):
    end = time.time() + seconds
    while time.time() < end:
        v = fn()
        if v:
            return v
        time.sleep(step)
    return fn()


def interrupt(p):
    if WINDOWS:
        p.send_signal(signal.CTRL_BREAK_EVENT)
    else:
        p.send_signal(signal.SIGINT)


# ======================================================================== cases

def case_init(c):
    r, secs = c.base_init
    if not c.expect(r, 0, "pravo init"):
        raise Skip("init failed: " + c.norm(r.all)[-600:])
    lock = read(os.path.join(c.base, "pravo.lock.cue")).decode()
    keys = sorted(set(re.findall(r'"((?:binary:)?[a-z0-9]+_[a-z0-9]+)":', lock)))
    c.obs("lock.platforms", keys, local=True)
    goos = {"linux": "linux", "darwin": "darwin", "windows": "windows"}[SYSTEM]
    c.check(any(k.startswith(f"binary:{goos}_") for k in keys), f"the lock file has no binary hash for {goos}: {keys}")
    c.obs("lock.has_archive_hash", any(not k.startswith("binary:") for k in keys), local=True)
    c.obs("providers", c.lines(re.sub(r"\(.*\)", "", "\n".join(l for l in r.all.split("\n") if "=" in l))))
    # a second init finds everything in place
    r2 = c.run(["init"], cwd=c.base)
    c.expect(r2, 0, "second pravo init")
    c.obs("init2.text", c.lines(re.sub(r"\(\d.*?\)", "(…)", r2.all).replace("\\", "/")))
    # the lock file says the same after the second run
    c.check(read(os.path.join(c.base, "pravo.lock.cue")).decode() == lock, "a second init rewrote the lock file")


def case_lifecycle(c):
    d = c.new("life", LIFE)
    tmp_before = set(os.listdir(c.tmp))
    r = c.run(["validate"], cwd=d)
    c.expect(r, 0, "validate")
    r = c.run(["plan", "--detailed-exitcode"], cwd=d)
    c.expect(r, 2, "plan")
    c.obs("plan.lines", c.lines(r.out))
    r = c.run(["apply", "--auto-approve"], cwd=d)
    c.expect(r, 0, "apply")
    c.obs("apply.lines", [l for l in c.lines(r.out) if not "Creating" in l and not "Creation complete" in l])
    c.obs("apply.created", sorted(re.findall(r"^(\w+): Creation complete", c.norm(r.out), flags=re.M)))
    f = os.path.join(d, "out", "f.txt")
    ok = os.path.exists(f)
    c.check(ok, "local_file did not write out/f.txt")
    if ok:
        b = read(f)
        c.obs("file.bytes", b.decode("utf-8", "replace"))
        c.check(b"\r" not in b, "the written file holds a carriage return")
    r = c.run(["plan", "--detailed-exitcode"], cwd=d)
    c.expect(r, 0, "second plan")
    c.obs("plan2.lines", c.lines(r.out))
    r = c.run(["state", "list"], cwd=d)
    c.obs("state.list", c.lines(r.out))
    r = c.run(["state", "show", "r"], cwd=d)
    c.obs("state.show.r", c.lines(re.sub(r"refreshed.*", "", r.out)))
    c.obs("state.tree", state_tree(d, c))
    pj = c.run(["plan", "--json"], cwd=d)
    try:
        j = json.loads(pj.out)
        c.obs("plan.json.keys", sorted(j))
        c.obs("plan.json.stats", j.get("stats"))
        c.obs("plan.json.providers", j.get("providers"))
    except Exception as e:
        c.check(False, f"plan --json is not JSON: {e}")
    # change the world behind the tool's back
    write(f, "tampered\n")
    r = c.run(["drift"], cwd=d)
    c.expect(r, 2, "drift")
    c.obs("drift.lines", c.lines(r.out))
    r = c.run(["plan", "--refresh=all", "--detailed-exitcode"], cwd=d)
    c.expect(r, 2, "plan --refresh=all after the file changed")
    r = c.run(["apply", "--auto-approve", "--refresh=all"], cwd=d)
    c.expect(r, 0, "apply that puts the file back")
    c.check(read(f).decode() == "n=" + re.search(r"result:\s+(\d+)", c.run(["state", "show", "r"], cwd=d).out).group(1) + "\nsecond line\n",
            "the file was not put back as written")
    # an outside deletion
    os.remove(f)
    r = c.run(["drift"], cwd=d)
    c.expect(r, 2, "drift after a deletion")
    r = c.run(["apply", "--auto-approve", "--refresh=all"], cwd=d)
    c.expect(r, 0, "apply that recreates the file")
    c.check(os.path.exists(f), "the deleted file was not recreated")
    # destroy, and destroy again
    r = c.run(["destroy", "--auto-approve"], cwd=d)
    c.expect(r, 0, "destroy")
    c.obs("destroy.lines", [l for l in c.lines(r.out) if "Destruction complete" not in l])
    c.check(not os.path.exists(f), "destroy left out/f.txt")
    r = c.run(["destroy", "--auto-approve"], cwd=d)
    c.expect(r, 0, "second destroy")
    c.obs("destroy2.lines", c.lines(r.out))
    c.obs("state.tree.after", [n for n in state_tree(d, c) if not n.startswith(("txn/", "kinds/"))])
    left = sorted(set(os.listdir(c.tmp)) - tmp_before)
    c.obs("tmp.leftover", left, local=True)
    c.check(not left, f"files left in the temporary directory: {left[:5]}")


def case_crlf(c):
    """The same specification with LF, with CRLF, with a byte order mark, and with mixed endings."""
    body = LIFE.replace('content: "n=\\(r.result)\\nsecond line\\n"', 'content: """\n\t\tmulti\n\t\tline \\(r.result)\n\t\t"""')
    results = {}
    for name, nl_, bom in (("lf", "\n", False), ("crlf", "\r\n", False), ("bom", "\n", True), ("crlfbom", "\r\n", True)):
        d = c.new("crlf-" + name, None)
        write(os.path.join(d, "main.cue"), body, newline=nl_, bom=bom)
        r = c.run(["apply", "--auto-approve"], cwd=d)
        if not c.expect(r, 0, f"apply with {name} source"):
            continue
        b = read(os.path.join(d, "out", "f.txt"))
        results[name] = b
        c.obs(f"file.{name}", b.decode("utf-8", "replace"))
        r = c.run(["plan", "--detailed-exitcode"], cwd=d)
        c.expect(r, 0, f"plan after apply with {name} source")
        r = c.run(["fmt", "--check"], cwd=d)
        c.obs(f"fmt-check.{name}", [r.rc, c.lines(r.all)])
    if "lf" in results:
        for k, v in results.items():
            c.check(v == results["lf"], f"{k} source made another file than lf: {v!r} against {results['lf']!r}")
    # mixed endings in one file
    d = c.new("crlf-mixed", None)
    text = body.split("\n")
    raw = "\r\n".join(text[:10]) + "\n" + "\r\n".join(text[10:12]) + "\n" + "\n".join(text[12:])
    with open(os.path.join(d, "main.cue"), "wb") as fh:
        fh.write(raw.encode())
    r = c.run(["apply", "--auto-approve"], cwd=d)
    c.expect(r, 0, "apply with mixed endings")
    if "lf" in results and os.path.exists(os.path.join(d, "out", "f.txt")):
        c.check(read(os.path.join(d, "out", "f.txt")) == results["lf"], "mixed endings made another file than lf")
    # fmt writes which ending?
    d = c.new("crlf-fmt", None)
    write(os.path.join(d, "main.cue"), body.replace("    ", "\t"), newline="\r\n")
    r = c.run(["fmt"], cwd=d)
    b = read(os.path.join(d, "main.cue"))
    c.obs("fmt.rc", r.rc)
    c.obs("fmt.crlf_left", b.count(b"\r\n"), local=False)


def case_names(c):
    """Names in the specification that a file system treats specially."""
    names = ["con", "prn", "aux", "nul", "com1", "lpt1", "CON2", "a b", "a:b", "a/b", "a\\\\b", "q?*", "x.", "tail ", "é", "ß", "日本", "😀", "a" * 150]
    body = HEADER
    for i, n in enumerate(names):
        body += f'"{n}": $null.#Resource.null_resource & {{triggers: v: "{i}"}}\n'
    d = c.new("names", body)
    r = c.run(["apply", "--auto-approve"], cwd=d)
    c.obs("apply.rc", r.rc)
    c.obs("apply.last", c.lines(r.out)[-1:] if r.rc == 0 else c.lines(r.err)[:3])
    if c.expect(r, 0, "apply of resources with awkward names"):
        r = c.run(["state", "list"], cwd=d)
        got = sorted(l for l in r.out.split("\n") if l.strip())
        c.obs("state.list.count", len(got))
        c.check(len(got) == len(names), f"{len(got)} resources in the state, wanted {len(names)}")
        r = c.run(["plan", "--detailed-exitcode"], cwd=d)
        c.expect(r, 0, "plan after apply")
        r = c.run(["plan", "--refresh=all", "--detailed-exitcode"], cwd=d)
        c.expect(r, 0, "refresh=all after apply")
        c.obs("state.files", state_tree(d, c))
        r = c.run(["destroy", "--auto-approve"], cwd=d)
        c.expect(r, 0, "destroy")
    # two names that differ only in case, or in Unicode form: refused before anything is written
    d = c.new("names-case", HEADER + 'Foo: $null.#Resource.null_resource & {triggers: v: "1"}\nfoo: $null.#Resource.null_resource & {triggers: v: "2"}\n')
    r = c.run(["apply", "--auto-approve"], cwd=d)
    c.obs("case.rc", r.rc)
    c.obs("case.msg", c.lines(r.all)[:2])
    c.check(r.rc != 0, "names that differ only in case were accepted")
    c.check(not os.path.exists(os.path.join(d, "pravo.state", "resources")) or not os.listdir(os.path.join(d, "pravo.state", "resources")),
            "a resource was written although the names collide")
    d = c.new("names-unicode", HEADER + '"\u00e9": $null.#Resource.null_resource & {triggers: v: "1"}\n"e\u0301": $null.#Resource.null_resource & {triggers: v: "2"}\n')
    r = c.run(["apply", "--auto-approve"], cwd=d)
    c.obs("unicode.rc", r.rc)
    c.obs("unicode.msg", c.lines(r.all)[:2])
    if r.rc == 0:
        n = len(c.run(["state", "list"], cwd=d).out.split())
        c.check(n == 2, f"two names that a file system may treat as one: {n} records in the state")
        c.obs("unicode.records", n)


def case_paths(c):
    """Files that a provider writes at awkward places, and a specification in a directory with such a name."""
    long_dir = "/".join(["d" * 40] * 7)  # about 290 characters below the working directory
    targets = {
        "plain": "out/a.txt",
        "space": "out dir/a b.txt",
        "unicode": "out/é/日本 😀.txt",
        "dots": "out/.hidden",
        "long": long_dir + "/f.txt",
        "backslash": "out\\\\back.txt",
    }
    body = HEADER
    for k, p in targets.items():
        body += f'{k}: $local.#Resource.local_file & {{filename: "{p}", content: "{k}\\n"}}\n'
    d = c.new("paths", body)
    r = c.run(["apply", "--auto-approve"], cwd=d)
    c.obs("apply.rc", r.rc)
    c.obs("apply.errors", sorted(set(re.findall(r"^Error: (\w+)", c.norm(r.all), flags=re.M))) if r.rc else [])
    for k, p in targets.items():
        ex = os.path.exists(os.path.join(d, *p.replace("\\\\", "/").split("/"))) if "\\\\" not in p else os.path.exists(os.path.join(d, p.replace("\\\\", "\\")))
        c.obs(f"exists.{k}", ex, local=(k in ("backslash", "long")))
    if r.rc == 0:
        r = c.run(["plan", "--detailed-exitcode"], cwd=d)
        c.expect(r, 0, "plan after apply")
        r = c.run(["destroy", "--auto-approve"], cwd=d)
        c.expect(r, 0, "destroy")
        c.check(not os.path.exists(os.path.join(d, "out dir", "a b.txt")), "destroy left a file")
    else:
        # a failed apply must leave a state that a second run can use: the files that were written are in it
        r2 = c.run(["plan"], cwd=d)
        c.obs("failed.plan.rc", r2.rc)
        c.obs("failed.plan.text", c.lines(r2.all)[-3:])
        c.check(r2.rc in (0, 2), f"plan after a failed apply exits {r2.rc}")
        r3 = c.run(["destroy", "--auto-approve"], cwd=d)
        c.obs("failed.destroy.rc", r3.rc)
        left = [k for k, p in targets.items() if "\\\\" not in p and os.path.exists(os.path.join(d, *p.split("/")))]
        c.obs("failed.left", left)
        c.check(not left, f"destroy after a failed apply left files: {left}")
    # a specification in a directory with spaces and non-ASCII letters, reached with -C and a relative path
    d2 = os.path.join(c.root, "spec dir é")
    shutil.copytree(c.new("tmp-spec", LIFE), d2)
    r = c.run(["-C", d2, "apply", "--auto-approve"], cwd=c.root)
    c.expect(r, 0, "apply with -C to a directory with spaces")
    r = c.run(["-C", os.path.relpath(d2, c.root), "plan", "--detailed-exitcode"], cwd=c.root)
    c.expect(r, 0, "plan with -C and a relative path")
    c.check(os.path.exists(os.path.join(d2, "out", "f.txt")), "the file is not under the directory that -C names")
    # the directory that pravo found on its own: state under the specification, not under the working directory
    c.check(os.path.isdir(os.path.join(d2, "pravo.state")), "no pravo.state next to the specification")
    c.check(not os.path.exists(os.path.join(c.root, "pravo.state")), "pravo.state in the working directory instead")


def case_split_files(c):
    """A package of several files, a hidden helper and a sub-package; the plan must not depend on how it is cut."""
    one = c.new("split-one", LIFE)
    two = c.new("split-two", None)
    head, rest = LIFE.split("r: ", 1)
    write(os.path.join(two, "a.cue"), head)
    write(os.path.join(two, "z.cue"), "package x\n\nr: " + rest)
    for d in (one, two):
        r = c.run(["apply", "--auto-approve"], cwd=d)
        c.expect(r, 0, "apply " + os.path.basename(d))
    a = c.run(["state", "list"], cwd=one).out
    b = c.run(["state", "list"], cwd=two).out
    c.check(a == b, "the same resources in one file and in two make different states")
    fa, fb = read(os.path.join(one, "out", "f.txt")), read(os.path.join(two, "out", "f.txt"))
    c.check(fa == fb, "the same resources in one file and in two make different files")
    c.obs("sameplan", c.run(["plan"], cwd=two).out.strip().split("\n")[0])


def case_locks(c):
    """Two commands at once, and a command that is killed while it holds a lock."""
    d = c.new("locks", SLOW)
    r = c.run(["apply", "--auto-approve", "-t", "wait=1s"], cwd=d)
    c.expect(r, 0, "first apply")
    a = c.popen(["apply", "--auto-approve", "-t", "gen=1", "-t", "wait=8s"], d)
    held = wait_until(lambda: "locked by" in c.run(["state", "locks"], cwd=d).out, 30)
    c.check(held, "no lock to be seen while an apply runs")
    t0 = time.time()
    b = c.run(["apply", "--auto-approve", "-t", "gen=1", "-t", "wait=8s"], cwd=d)
    took = time.time() - t0
    c.obs("second.rc_is_zero", b.rc == 0)
    c.obs("second.lines", c.lines(b.all)[:6])
    c.check(b.rc != 0, "a second apply at the same time exited 0")
    c.check(took < 60, f"the second apply took {took:.0f} s to say no")
    out, err = a.communicate(timeout=60)
    c.check(a.returncode == 0, f"the first apply exited {a.returncode}: {err.decode('utf-8','replace')[-200:]}")
    r = c.run(["state", "locks"], cwd=d)
    c.obs("locks.after", c.lines(r.out))
    c.check("locked by" not in r.out, "a lock is left after both commands ended")

    # hard kill while a lock is held
    before = provider_processes()
    a = c.popen(["apply", "--auto-approve", "-t", "gen=2", "-t", "wait=30s"], d)
    held = wait_until(lambda: "locked by" in c.run(["state", "locks"], cwd=d).out, 30)
    c.check(held, "no lock to be seen before the kill")
    seen = provider_processes()
    a.kill()
    a.communicate(timeout=30)
    gone = wait_until(lambda: provider_processes() == before, 20, 0.5) if before is not None else None
    c.obs("kill.provider_orphans", None if before is None else (provider_processes() or []), local=False)
    c.check(before is None or gone, f"a provider process runs on after pravo was killed: {provider_processes()} (before: {before}, during: {seen})")
    r = c.run(["state", "locks"], cwd=d)
    c.obs("kill.locks", c.lines(r.out))
    r = c.run(["plan", "--detailed-exitcode"], cwd=d, extra=None)
    c.obs("kill.plan.rc", r.rc)
    c.obs("kill.plan.lines", c.lines(r.all)[-6:])
    r = c.run(["apply", "--auto-approve", "-t", "gen=2", "-t", "wait=1s"], cwd=d)
    c.obs("kill.apply2.rc", r.rc)
    c.obs("kill.apply2.lines", c.lines(r.all)[:8])
    if r.rc != 0:
        # the message names a way out; follow it
        r1 = c.run(["state", "unlock", "--stale"], cwd=d)
        c.obs("kill.unlock.rc", r1.rc)
        c.obs("kill.unlock.lines", c.lines(r1.all)[:6])
        r = c.run(["apply", "--auto-approve", "-t", "gen=2", "-t", "wait=1s", "--allow-create", "s"], cwd=d)
        c.expect(r, 0, "apply after unlock and --allow-create")
    r = c.run(["plan", "--detailed-exitcode", "-t", "gen=2", "-t", "wait=1s"], cwd=d)
    c.expect(r, 0, "the estate converges after the kill")
    c.check("locked by" not in c.run(["state", "locks"], cwd=d).out, "a lock stays after the recovery")


def case_interrupt(c):
    """Ctrl-C (SIGINT, CTRL_BREAK on Windows) in the middle of an apply. The first asks the run to finish what is in flight,
    the second asks the providers to stop (the text of the first says so)."""
    d = c.new("interrupt", SLOW)
    r = c.run(["apply", "--auto-approve", "-t", "wait=1s"], cwd=d)
    c.expect(r, 0, "first apply")
    before = provider_processes()
    a = c.popen(["apply", "--auto-approve", "-t", "gen=1", "-t", "wait=60s"], d)
    held = wait_until(lambda: "locked by" in c.run(["state", "locks"], cwd=d).out, 30)
    c.check(held, "no lock to be seen before the interrupt")
    time.sleep(1.0)
    interrupt(a)
    time.sleep(2.0)
    c.obs("first_interrupt.still_running", a.poll() is None)
    c.check(a.poll() is None, "the first interrupt ended the run at once; it should wait for what is in flight")
    t0 = time.time()
    interrupt(a)
    try:
        out, err = a.communicate(timeout=60)
    except subprocess.TimeoutExpired:
        a.kill()
        out, err = a.communicate()
        c.check(False, "pravo did not end within 60 s of the second interrupt")
    took = time.time() - t0
    c.obs("exit.nonzero", a.returncode != 0)
    c.check(a.returncode != 0, "an interrupted apply exited 0")
    c.check(took < 30, f"pravo took {took:.0f} s to end after the second interrupt")
    text = out.decode("utf-8", "replace") + err.decode("utf-8", "replace")
    c.obs("exit.text", [l for l in c.lines(text) if "nterrupt" in l or "Error" in l or "stop" in l.lower()])
    gone = wait_until(lambda: provider_processes() == before, 15, 0.5) if before is not None else True
    c.check(gone, f"a provider process runs on after the interrupt: {provider_processes()}")
    r = c.run(["state", "locks"], cwd=d)
    # a create that was stopped may or may not have reached the cloud, so the lock stays on purpose: what matters is that
    # the message says what to do and that the advice works, the same on every system
    c.obs("locks.after", c.lines(r.out))
    r = c.run(["apply", "--auto-approve", "-t", "gen=1", "-t", "wait=1s"], cwd=d)
    c.obs("again.rc", r.rc)
    c.obs("again.lines", [l for l in c.lines(r.all) if "Creat" not in l and "Replac" not in l][:8])
    if r.rc != 0:
        r = c.run(["apply", "--auto-approve", "-t", "gen=1", "-t", "wait=1s", "--allow-create", "s"], cwd=d)
    c.expect(r, 0, "apply after the interrupt")
    r = c.run(["plan", "--detailed-exitcode", "-t", "gen=1", "-t", "wait=1s"], cwd=d)
    c.expect(r, 0, "plan after the recovery")


def case_stored_plan(c):
    d = c.new("plans", LIFE)
    r = c.run(["plan", "--out", "the.plan"], cwd=d)
    c.expect(r, 0, "plan --out")
    p = os.path.join(d, "the.plan")
    raw = read(p) if os.path.exists(p) else b""
    c.check(len(raw) > 0, "no plan file")
    c.obs("plan.starts_with", raw[:1].decode("latin-1"), local=False)
    c.obs("plan.cr_count", raw.count(b"\r"))
    bad = {
        "crlf": raw.replace(b"\n", b"\r\n"),
        "truncated": raw[: len(raw) // 2],
        "empty": b"",
        "garbage": b"\x00\x01\x02" * 10,
        "flipped": raw[:100] + bytes([raw[100] ^ 1]) + raw[101:] if len(raw) > 101 else raw,
    }
    for name, data in bad.items():
        d2 = os.path.join(c.root, "plans-" + name)
        shutil.copytree(d, d2)
        with open(os.path.join(d2, name + ".plan"), "wb") as fh:
            fh.write(data)
        r = c.run(["apply", name + ".plan"], cwd=d2)
        wrote = os.path.exists(os.path.join(d2, "out", "f.txt"))
        c.obs(f"apply-{name}.rc_nonzero", r.rc != 0)
        c.obs(f"apply-{name}.wrote", wrote)
        if name != "flipped":  # which byte breaks which parser is not a fact about the system
            c.obs(f"apply-{name}.msg", c.lines(r.all)[:3])
        if name == "crlf":
            c.check(r.rc == 0 or not wrote, "a plan file with CRLF failed halfway")
        else:
            c.check(r.rc != 0 and not wrote, f"apply of a {name} plan file: exit {r.rc}, wrote {wrote}")
    r = c.run(["apply", "the.plan"], cwd=d)
    c.expect(r, 0, "apply of the saved plan")
    c.check(os.path.exists(os.path.join(d, "out", "f.txt")), "the saved plan made no file")
    r = c.run(["show", "the.plan"], cwd=d)
    c.obs("show.rc", r.rc)
    # a saved plan applied twice or after the world changed
    r = c.run(["apply", "the.plan"], cwd=d)
    c.obs("again.rc", r.rc)
    c.obs("again.lines", c.lines(r.all)[:3])
    c.check("Creat" not in r.out, "a plan file was applied a second time")
    c.check(len(c.run(["state", "list"], cwd=d).out.split()) == 3, "the state changed after the second apply of a plan file")


def case_identity(c):
    """Identity files, the sealed state, and a secret, under this system's directories."""
    d = c.new("seal", LIFE)
    pw = "xplat-passphrase-" + "k" * 12
    env = {"PRAVO_PASSPHRASE": pw, "PRAVO_IDENTITY_PASSPHRASE": pw}
    r = c.run(["id", "new", "--name", "ada"], cwd=d, extra=env)
    c.obs("id.new.rc", r.rc)
    if not c.expect(r, 0, "pravo id new"):
        return
    expect = os.path.join(c.home, "AppData", "Roaming", "pravo", "id", "ada") if WINDOWS else os.path.join(c.home, ".config", "pravo", "id", "ada")
    c.check(os.path.exists(expect), f"the identity is not at {expect}")
    c.obs("id.file_exists", os.path.exists(expect))
    if os.path.exists(expect) and not WINDOWS:
        mode = os.stat(expect).st_mode & 0o777
        c.obs("id.mode", oct(mode), local=True)
        c.check(mode & 0o077 == 0, f"the identity file is readable by others: {oct(mode)}")
    line = [l for l in r.out.split("\n") if l.strip().startswith(("ssh-", "pravo", "age", "ada"))]
    r2 = c.run(["id", "show", "--identity", "ada"], cwd=d, extra=env)
    c.expect(r2, 0, "pravo id show")
    pub = [l for l in (r2.out + r.out).split("\n") if re.match(r"^\s*ada ssh-ed25519 \S+ x25519:\S+$", l)]
    c.check(bool(pub), "no line for the readers file in the output of id new/show")
    if not pub:
        return
    write(os.path.join(d, ".pravo", "access", "readers.txt"), pub[0].strip() + "\n")
    r = c.run(["protect", "init", "--encrypt", "--allow-local", "--yes", "--identity", "ada"], cwd=d, extra=env)
    c.expect(r, 0, "protect init --encrypt")
    r = c.run(["apply", "--auto-approve", "--identity", "ada"], cwd=d, extra=env)
    c.expect(r, 0, "apply on a sealed state")
    r = c.run(["protect", "status", "--identity", "ada"], cwd=d, extra=env)
    c.expect(r, 0, "protect status")
    c.obs("status.lines", c.lines(r.out)[:8])
    # nothing readable in the state: the value of the integer and the file's content
    r = c.run(["state", "show", "r", "--identity", "ada"], cwd=d, extra=env)
    m = re.search(r"result:\s+(\d+)", r.out)
    c.check(bool(m), "state show cannot read the sealed record with the identity")
    if m:
        hits = []
        for dp, dn, fn in os.walk(os.path.join(d, "pravo.state")):
            for n in fn:
                data = read(os.path.join(dp, n))
                if b"second line" in data or (b'"result"' in data and m.group(1).encode() in data):
                    hits.append(os.path.relpath(os.path.join(dp, n), d))
        c.check(not hits, f"plain values in the sealed state: {hits[:3]}")
    r = c.run(["state", "show", "r", "--identity", "none"], cwd=d, extra=env)
    c.obs("noidentity.rc_is_zero", r.rc == 0)
    c.check("result:" not in r.out, "a record's value is shown without an identity")
    # a secret
    canary = "CANARY-" + "q" * 20
    r = c.run(["secret", "set", "db/pass", "--identity", "ada"], cwd=d, extra=env, stdin=canary + "\n")
    c.expect(r, 0, "secret set")
    r = c.run(["secret", "get", "db/pass", "--identity", "ada"], cwd=d, extra=env)
    c.expect(r, 0, "secret get")
    c.check(r.out == canary or r.out.strip() == canary, f"secret get printed more than the value on stdout: {c.norm(r.out)[:160]!r}")
    leaks = []
    for root_, dn, fn in os.walk(d):
        for n in fn:
            if canary.encode() in read(os.path.join(root_, n)):
                leaks.append(os.path.relpath(os.path.join(root_, n), d))
    for n in os.listdir(c.tmp):
        p = os.path.join(c.tmp, n)
        if os.path.isfile(p) and canary.encode() in read(p):
            leaks.append("tmp/" + n)
    c.check(not leaks, f"the secret is in files: {leaks[:3]}")
    r = c.run(["secret", "list", "--identity", "ada"], cwd=d, extra=env)
    c.check(canary not in r.all, "secret list shows the value")
    # the passphrase in a file or the wrong passphrase
    r = c.run(["state", "show", "r", "--identity", "ada"], cwd=d, extra={"PRAVO_PASSPHRASE": "wrong", "PRAVO_IDENTITY_PASSPHRASE": "wrong"})
    c.obs("wrong_passphrase.rc_nonzero", r.rc != 0)
    c.check(r.rc != 0 and "result:" not in r.out, "a wrong passphrase opened the state")
    c.obs("wrong_passphrase.msg", c.lines(r.all)[:2])


def case_serve(c):
    """The viewer: starts, answers on loopback, refuses paths that leave its tree, and stops on interrupt."""
    d = c.new("serve", LIFE)
    r = c.run(["apply", "--auto-approve"], cwd=d)
    c.expect(r, 0, "apply")
    p = c.popen(["serve", "--no-open", "--port", "0"], d)
    url = None
    buf = b""
    end = time.time() + 60
    import threading, queue
    q = queue.Queue()

    def pump(stream):
        for line in iter(stream.readline, b""):
            q.put(line)

    for s in (p.stdout, p.stderr):
        threading.Thread(target=pump, args=(s,), daemon=True).start()
    lines = []
    while time.time() < end and not url:
        try:
            l = q.get(timeout=1).decode("utf-8", "replace")
        except queue.Empty:
            if p.poll() is not None:
                break
            continue
        lines.append(l)
        m = re.search(r"https?://127\.0\.0\.1:(\d+)/?\S*", l)
        if m:
            url = m.group(0).strip()
    if not c.check(url is not None, "serve printed no address: " + "".join(lines)[-200:]):
        return
    m = re.match(r"https?://127\.0\.0\.1:(\d+)(/\S*)?", url)
    port, rest = int(m.group(1)), m.group(2) or "/"
    tok = rest.split("token=", 1)[1] if "token=" in rest else ""
    c.obs("url.shape", re.sub(r"[A-Za-z0-9_-]{16,}", "<token>", re.sub(r":\d+", ":<port>", url)))
    cookie = {"Cookie": f"pravo_serve_{port}={tok}"}

    def get(path, headers=None):
        conn = http.client.HTTPConnection("127.0.0.1", port, timeout=10)
        conn.request("GET", path, headers=headers or {})
        resp = conn.getresponse()
        body = resp.read()
        conn.close()
        return resp.status, body

    st, body = get(rest)
    c.obs("url.status", st)
    c.check(st in (200, 303), f"GET {rest} answered {st}")
    st, body = get("/api/graph")
    c.obs("api.without_cookie", st)
    c.check(st in (401, 403), f"/api/graph without the token answered {st}")
    st, body = get("/api/graph", cookie)
    c.obs("api.with_cookie", st)
    c.check(st == 200, f"/api/graph with the token answered {st}")
    try:
        g = json.loads(body)
        c.obs("api.graph.keys", sorted(g) if isinstance(g, dict) else type(g).__name__)
    except Exception:
        pass
    secret_markers = (b"package x", b'"serial"', b"[fonts]", b"[extensions]", b"root:x:", b"module:")
    attempts = [
        "/..%5c..%5cpravo.state%5cmeta.json", "/%2e%2e/%2e%2e/pravo.state/meta.json", "/..\\..\\pravo.state\\meta.json",
        "/assets/..%5c..%5c..%5cmain.cue", "/%2e%2e%5c%2e%2e%5cmain.cue", "/C:/Windows/win.ini", "/c%3a%5cwindows%5cwin.ini",
        "/assets/../../../main.cue", "//..//..//main.cue", "/assets/%5c..%5c..%5cmain.cue",
        "/api/trace?id=..%5C..%5C..%5Cmain.cue", "/api/trace?id=../../../main.cue", "/api/trace?id=..%2f..%2f..%2fmain.cue",
        "/api/trace?id=C:%5CWindows%5Cwin.ini", "/api/plan?id=..%5C..%5Cmain.cue", "/api/plan?id=../../main.cue",
        "/api/source?addr=..%5Cmain.cue", "/api/detail?addr=..%5C..%5Cmain.cue", "/api/detail?addr=../../main.cue",
    ]
    leaked, odd = [], []
    for a in attempts:
        try:
            st, body = get(a, cookie)
        except Exception as e:
            odd.append(a)
            continue
        if st == 200 and any(mk in body for mk in secret_markers):
            leaked.append(a)
        if st >= 500:
            odd.append(f"{a} -> {st}")
    c.obs("traversal.leaks", sorted(set(leaked)))
    c.obs("traversal.server_errors", sorted(odd))
    c.check(not leaked, f"the viewer served files outside its tree: {sorted(set(leaked))}")
    c.check(not odd, f"the viewer failed on {odd[:3]}")
    interrupt(p)
    try:
        p.wait(timeout=30)
        stopped = True
    except subprocess.TimeoutExpired:
        p.kill()
        stopped = False
    c.check(stopped, "serve did not end within 30 s of the interrupt")
    c.obs("stopped", stopped)
    # the port is free again
    s = socket.socket()
    try:
        s.settimeout(2)
        free = s.connect_ex(("127.0.0.1", port)) != 0
    finally:
        s.close()
    c.check(free, "the viewer's port still answers after the interrupt")


def case_examples(c):
    """The examples of this repository, as the system checked them out (CRLF on a Windows checkout), with the lock
    file that was made on another system."""
    here = os.path.dirname(os.path.abspath(__file__))
    ex = os.path.normpath(os.path.join(here, "..", "..", "examples"))
    if not os.path.isdir(ex):
        raise Skip("no examples directory next to the suite")
    for name in sorted(os.listdir(ex)):
        src = os.path.join(ex, name)
        if not os.path.exists(os.path.join(src, "main.cue")):
            continue
        d = os.path.join(c.root, "ex-" + name)
        shutil.copytree(src, d)
        lock0 = read(os.path.join(d, "pravo.lock.cue")).decode() if os.path.exists(os.path.join(d, "pravo.lock.cue")) else ""
        c.obs(f"{name}.crlf_in_checkout", b"\r\n" in read(os.path.join(d, "main.cue")), local=True)
        r = c.run(["fmt", "--check"], cwd=d)
        c.obs(f"{name}.fmt_check.rc", r.rc)
        c.check(r.rc == 0, f"{name}: fmt --check on the checked out files: exit {r.rc}: {c.norm(r.all)[:200]}")
        r = c.run(["init"], cwd=d, timeout=600)
        c.obs(f"{name}.init.rc", r.rc)
        if not c.expect(r, 0, f"{name}: init"):
            continue
        lock1 = read(os.path.join(d, "pravo.lock.cue")).decode()
        have = sorted(set(re.findall(r'"((?:binary:)?[a-z0-9]+_[a-z0-9]+)":', lock1)))
        goos = SYSTEM
        c.check(any(k.startswith(f"binary:{goos}_") for k in have), f"{name}: the lock file after init has no binary hash for {goos}")
        old = sorted(set(re.findall(r'"((?:binary:)?[a-z0-9]+_[a-z0-9]+)":', lock0)))
        c.check(set(old) <= set(have), f"{name}: init dropped hashes of other systems from the lock file: {sorted(set(old) - set(have))}")
        r = c.run(["validate"], cwd=d)
        c.obs(f"{name}.validate.rc", r.rc)
        c.expect(r, 0, f"{name}: validate")
        r = c.run(["plan", "--detailed-exitcode"], cwd=d)
        c.obs(f"{name}.plan.rc", r.rc)
        c.obs(f"{name}.plan.summary", [l for l in c.lines(r.out) if l.startswith(("Plan:", "No changes"))])
        c.check(r.rc in (0, 2), f"{name}: plan exits {r.rc}: {c.norm(r.all)[:200]}")


def case_mcp(c):
    """pravo mcp over standard input and output: one JSON object a line, whatever the system's line ending."""
    d = c.new("mcp", LIFE)
    msgs = [
        {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2024-11-05", "capabilities": {}, "clientInfo": {"name": "xplat", "version": "1"}}},
        {"jsonrpc": "2.0", "method": "notifications/initialized"},
        {"jsonrpc": "2.0", "id": 2, "method": "tools/list"},
        {"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": {"name": "pravo_validate", "arguments": {}}},
        {"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": {"name": "pravo_plan", "arguments": {"out": "m.plan"}}},
        {"jsonrpc": "2.0", "id": 5, "method": "tools/call", "params": {"name": "pravo_apply_plan", "arguments": {"planfile": "m.plan"}}},
        {"jsonrpc": "2.0", "id": 6, "method": "tools/call", "params": {"name": "pravo_state_query", "arguments": {"expression": "resources.r.attributes.result"}}},
        {"jsonrpc": "2.0", "id": 7, "method": "tools/call", "params": {"name": "pravo_locks", "arguments": {}}},
    ]
    data = ("\n".join(json.dumps(m) for m in msgs) + "\n").encode()
    r = subprocess.run([c.pravo, "mcp"], cwd=d, env=c.env(), input=data, capture_output=True, timeout=240)
    c.obs("rc", r.returncode)
    raw = r.stdout
    c.check(b"\r" not in raw, "the MCP server wrote a carriage return")
    got = {}
    for l in raw.decode("utf-8", "replace").split("\n"):
        if not l.strip():
            continue
        try:
            j = json.loads(l)
            got[j.get("id")] = j
        except Exception:
            c.check(False, f"a line of the MCP server is not JSON: {l[:80]!r}")
    c.obs("ids", sorted(k for k in got if k is not None))
    if 2 in got:
        c.obs("tools", sorted(t["name"] for t in got[2]["result"]["tools"]))
    for i in (3, 4, 5, 6, 7):
        j = got.get(i)
        if not c.check(j is not None, f"no answer to request {i}"):
            continue
        c.obs(f"call{i}.isError", bool(j.get("result", {}).get("isError")))
        c.check(not j.get("result", {}).get("isError"), f"request {i} failed: {json.dumps(j)[:300]}")
    c.check(os.path.exists(os.path.join(d, "out", "f.txt")), "pravo_apply_plan made no file")
    if 6 in got:
        c.obs("query", re.findall(r"\d+", json.dumps(got[6])))
    c.obs("stderr", c.lines(r.stderr.decode("utf-8", "replace"))[:3])


def case_hammer(c):
    """Several commands change one state at once: the lock, the rename and the retry of the system under load."""
    n = 6
    body = HEADER + 'gen: *"0" | string @tag(gen)\n' + "".join(f'n{i}: $null.#Resource.null_resource & {{triggers: v: gen}}\n' for i in range(n))
    d = c.new("hammer", body)
    r = c.run(["apply", "--auto-approve"], cwd=d)
    c.expect(r, 0, "first apply")
    for rnd_ in range(1, 4):
        r = c.run(["plan", "-t", f"gen={rnd_}", "--out", "p.plan"], cwd=d)
        c.expect(r, 0, f"plan round {rnd_}")
        procs = [c.popen(["apply", "p.plan", "--only", f"n{i}"], d) for i in range(n)]
        # and readers at the same time
        readers = [c.popen(["state", "list"], d) for _ in range(3)] + [c.popen(["plan", "--detailed-exitcode", "-t", f"gen={rnd_}"], d)]
        rcs = []
        for p in procs:
            out, err = p.communicate(timeout=180)
            rcs.append((p.returncode, err.decode("utf-8", "replace")[-200:] + out.decode("utf-8", "replace")[-200:]))
        rrcs = []
        for p in readers:
            out, err = p.communicate(timeout=180)
            rrcs.append(p.returncode)
        bad = [x for x in rcs if x[0] != 0]
        c.obs(f"round{rnd_}.writers_failed", len(bad))
        c.check(not bad, f"round {rnd_}: {len(bad)} of {n} writers failed: {c.norm(bad[0][1]) if bad else ''}")
        c.obs(f"round{rnd_}.readers", sorted(set(rrcs)) if all(x in (0, 2) for x in rrcs) else rrcs)
        c.check(all(x in (0, 2) for x in rrcs), f"round {rnd_}: a reader failed: {rrcs}")
        r = c.run(["plan", "-t", f"gen={rnd_}", "--detailed-exitcode"], cwd=d)
        c.check(r.rc == 0, f"round {rnd_}: the plan after the writers exits {r.rc}: {c.norm(r.all)[-300:]}")
        r = c.run(["state", "log"], cwd=d)
        c.check("locked" not in c.run(["state", "locks"], cwd=d).out, f"round {rnd_}: a lock is left")
    r = c.run(["destroy", "--auto-approve"], cwd=d)
    c.expect(r, 0, "destroy")
    r = c.run(["state", "list"], cwd=d)
    c.check(r.out.strip() == "", f"records left after destroy: {r.out.split()[:3]}")
    c.obs("tree.after", [n_ for n_ in state_tree(d, c) if not n_.startswith(("txn/", "kinds/"))])


def case_coldinit(c):
    """Three `pravo init` at once on a cold provider cache: the providers are installed by whichever gets there first."""
    home = os.path.join(c.root, "cold home")
    old_home = c.home
    c.home = home
    os.makedirs(home)
    try:
        dirs = []
        for i in range(3):
            d = os.path.join(c.root, f"cold{i}")
            os.makedirs(d)
            write(os.path.join(d, "main.cue"), LIFE)
            dirs.append(d)
        procs = [c.popen(["init"], d) for d in dirs]
        outs = []
        for p in procs:
            out, err = p.communicate(timeout=600)
            outs.append((p.returncode, out.decode("utf-8", "replace") + err.decode("utf-8", "replace")))
        bad = [o for o in outs if o[0] != 0]
        c.obs("init.failed", len(bad))
        c.check(not bad, f"{len(bad)} of 3 concurrent inits failed: {c.norm(bad[0][1])[-300:] if bad else ''}")
        locks = [read(os.path.join(d, "pravo.lock.cue")) for d in dirs if os.path.exists(os.path.join(d, "pravo.lock.cue"))]
        if not c.check(len(set(locks)) <= 1 and len(locks) == 3, "the lock files of three inits of one specification differ"):
            import difflib
            a_, b_ = ([x for x in locks if x != locks[0]][:1] + [locks[0]])[:2] if len(locks) > 1 else (b"", b"")
            c.fails.append("diff: " + " | ".join(l for l in difflib.unified_diff(b_.decode().split("\n"), a_.decode().split("\n"), lineterm="", n=0) if not l.startswith(("---", "+++", "@@"))))
        for d in dirs:
            lk = read(os.path.join(d, "pravo.lock.cue")).decode() if os.path.exists(os.path.join(d, "pravo.lock.cue")) else ""
            c.check(lk.count('"signer"') == 4, f"the lock file names {lk.count(chr(34) + 'signer' + chr(34))} signing keys for 4 providers")
            r = c.run(["plan", "--detailed-exitcode"], cwd=d)
            c.expect(r, 2, "plan after a concurrent init")
        # the cache holds one install of each provider and no half installs
        found = []
        for dp, dn, fn in os.walk(home):
            for n in dn:
                if n.startswith(".install-"):
                    found.append(os.path.relpath(os.path.join(dp, n), home))
        c.obs("half_installs", found)
        c.check(not found, f"half installs are left in the cache: {found[:3]}")
    finally:
        c.home = old_home


def case_skill(c):
    """The skill is embedded in the binary: the same bytes everywhere, written under this system's home directory."""
    d = c.new("skill", LIFE)
    r = c.run(["skill", "list"], cwd=d)
    c.expect(r, 0, "skill list")
    listed = sorted(l.split()[0].replace("\\", "/") for l in r.out.splitlines() if l.strip() and not l.startswith(" "))
    c.obs("list.count", len(listed))
    r = c.run(["skill", "print"], cwd=d)
    c.expect(r, 0, "skill print")
    c.obs("print.sha", sha(r.out.encode("utf-8")))
    c.check("\r" not in r.out, "skill print holds a carriage return")
    target = os.path.join(c.home, ".claude", "skills", "pravo")
    r = c.run(["skill", "install"], cwd=d)
    c.expect(r, 0, "skill install")
    c.check(os.path.exists(os.path.join(target, "SKILL.md")), f"no SKILL.md under {target}")
    if os.path.exists(os.path.join(target, "SKILL.md")):
        c.obs("installed.sha", sha(read(os.path.join(target, "SKILL.md"))))
        c.check(b"\r" not in read(os.path.join(target, "SKILL.md")), "the installed SKILL.md holds a carriage return")
        files = tree(target)
        c.obs("installed.count", len(files))
        h = hashlib.sha256()
        for f in files:
            h.update(f.encode() + b"\0" + read(os.path.join(target, *f.split("/"))))
        c.obs("installed.tree_sha", h.hexdigest()[:16])
    # again, and into a directory with spaces, and over a directory that holds something else
    r = c.run(["skill", "install"], cwd=d)
    c.expect(r, 0, "second skill install")
    other = os.path.join(c.root, "skills dir é")
    r = c.run(["skill", "install", other], cwd=d)
    c.expect(r, 0, "skill install into a directory with a space")
    foreign = os.path.join(c.root, "foreign", "pravo")
    os.makedirs(foreign)
    write(os.path.join(foreign, "mine.txt"), "mine\n")
    r = c.run(["skill", "install", os.path.join(c.root, "foreign")], cwd=d)
    c.obs("foreign.rc", r.rc)
    c.check(r.rc != 0 and os.path.exists(os.path.join(foreign, "mine.txt")), "install touched a directory named pravo that holds something else")


def case_agent(c):
    """An identity opened by the ssh-agent: a socket on Linux and macOS, the OpenSSH named pipe on Windows."""
    if WINDOWS:
        sysdir = os.path.join(os.environ.get("SystemRoot", r"C:\Windows"), "System32", "OpenSSH")
        keygen, add = os.path.join(sysdir, "ssh-keygen.exe"), os.path.join(sysdir, "ssh-add.exe")
        if not (os.path.exists(keygen) and os.path.exists(add)):
            raise Skip("no OpenSSH client in " + sysdir)
        ps = subprocess.run(["powershell", "-NoProfile", "-Command",
                             "Get-Service ssh-agent | Set-Service -StartupType Manual; Start-Service ssh-agent; (Get-Service ssh-agent).Status"],
                            capture_output=True, text=True, timeout=60)
        c.obs("service.start", ps.stdout.strip().split("\n")[-1:] , local=True)
        if "Running" not in ps.stdout:
            raise Skip("the ssh-agent service does not start: " + (ps.stdout + ps.stderr)[-300:])
        agent_env = {}
        sock = None
    else:
        keygen, add = shutil.which("ssh-keygen"), shutil.which("ssh-add")
        agent = shutil.which("ssh-agent")
        if not (keygen and add and agent):
            raise Skip("no OpenSSH client")
        sockdir = tempfile.mkdtemp(prefix="xa", dir="/tmp")  # a unix socket path must stay short
        sock = os.path.join(sockdir, "agent.sock")
        ag = subprocess.Popen([agent, "-D", "-a", sock], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        c.procs.append(ag)
        if not wait_until(lambda: os.path.exists(sock), 10):
            raise Skip("ssh-agent made no socket")
        agent_env = {"PRAVO_SSH_AUTH_SOCK": sock}
    d = c.new("agent", LIFE)
    key = os.path.join(c.root, "agentkey")
    r = subprocess.run([keygen, "-q", "-t", "ed25519", "-N", "", "-C", "xplat", "-f", key], capture_output=True, text=True, timeout=60)
    if r.returncode != 0:
        raise Skip("ssh-keygen: " + r.stderr[-200:])
    addenv = dict(os.environ)
    if sock:
        addenv["SSH_AUTH_SOCK"] = sock
    r = subprocess.run([add, key], capture_output=True, text=True, timeout=60, env=addenv)
    if r.returncode != 0:
        raise Skip("ssh-add: " + (r.stdout + r.stderr)[-200:])
    try:
        r = c.run(["id", "new", "--name", "ada", "--unlock", "agent"], cwd=d, extra=agent_env)
        c.obs("id.new.rc", r.rc)
        if not c.expect(r, 0, "id new --unlock agent"):
            return
        r2 = c.run(["id", "show", "--identity", "ada"], cwd=d, extra=agent_env)
        c.expect(r2, 0, "id show")
        pub = [l for l in (r2.out + r.out).split("\n") if re.match(r"^\s*ada ssh-ed25519 \S+ x25519:\S+$", l)]
        if not c.check(bool(pub), "no readers line"):
            return
        write(os.path.join(d, ".pravo", "access", "readers.txt"), pub[0].strip() + "\n")
        r = c.run(["protect", "init", "--encrypt", "--allow-local", "--yes", "--identity", "ada"], cwd=d, extra=agent_env)
        c.expect(r, 0, "protect init with an agent identity")
        r = c.run(["apply", "--auto-approve", "--identity", "ada"], cwd=d, extra=agent_env)
        c.expect(r, 0, "apply with an agent identity")
        r = c.run(["state", "show", "r", "--identity", "ada"], cwd=d, extra=agent_env)
        c.check("result:" in r.out, "the sealed record cannot be read through the agent")
        # an address with no agent behind it: a clear refusal, soon
        dead = os.path.join(c.root, "no-agent.sock") if not WINDOWS else r"\\.\pipe\xplat-no-such-agent"
        t0 = time.time()
        r = c.run(["state", "show", "r", "--identity", "ada"], cwd=d, extra={"PRAVO_SSH_AUTH_SOCK": dead}, timeout=120)
        took = time.time() - t0
        c.obs("dead.rc_nonzero", r.rc != 0)
        c.obs("dead.msg", c.lines(r.all)[:2], local=True)
        c.check(r.rc != 0 and "result:" not in r.out, "the identity opened without its agent")
        c.check(took < 30, f"a missing agent took {took:.0f} s to be refused")
        c.check("agent" in r.all.lower(), "the refusal does not mention the agent: " + c.norm(r.all)[:200])
    finally:
        subprocess.run([add, "-D"], capture_output=True, timeout=60, env=addenv)


def case_json_exitcodes(c):
    """The numbers and the shapes that CI depends on."""
    d = c.new("machine", LIFE)
    r = c.run(["plan", "--detailed-exitcode", "--json"], cwd=d)
    c.obs("plan.rc", r.rc)
    c.check(r.rc == 2, f"plan --detailed-exitcode on an empty state exits {r.rc}")
    try:
        j = json.loads(r.out)
        c.obs("plan.keys", sorted(j))
        c.obs("plan.summary", j.get("summary"))
        c.obs("plan.change.keys", sorted({k for ch in j.get("changes", []) for k in ch}))
    except Exception as e:
        c.check(False, f"plan --json: {e}")
    r = c.run(["plan", "--markdown"], cwd=d)
    c.expect(r, 0, "plan --markdown")
    c.obs("markdown", c.lines(r.out))
    c.check("\r" not in r.out, "markdown holds a carriage return")
    r = c.run(["apply", "--auto-approve", "--json"], cwd=d)
    c.expect(r, 0, "apply --json")
    kinds = []
    for l in r.out.splitlines():
        l = l.strip()
        if l.startswith("{"):
            try:
                kinds.append(sorted(json.loads(l)))
            except Exception:
                pass
    c.obs("apply.json.lines", len(r.out.splitlines()), local=True)
    shapes = {}
    for k in kinds:
        if "kind" in k:
            shapes.setdefault("kind", set()).update(k)
    c.obs("apply.json.keys", sorted(shapes.get("kind", [])))
    r = c.run(["plan", "--detailed-exitcode"], cwd=d)
    c.expect(r, 0, "plan with no change")
    r = c.run(["nonsense"], cwd=d)
    c.obs("unknown.rc", r.rc)
    r = c.run(["plan", "-C", os.path.join(d, "missing")], cwd=d)
    c.obs("missing_dir.rc", r.rc)
    c.obs("missing_dir.msg", c.lines(r.all)[:2], local=True)  # the system's own words for "no such file"
    c.check(r.rc not in (0, 2), "plan of a missing directory exits like success or change")
    r = c.run(["eval"], cwd=d)
    c.expect(r, 0, "eval")
    c.obs("eval.r", re.findall(r"result:\s+(\d+)", r.out))
    c.check("\r" not in r.out, "eval prints a carriage return")


CASES = [
    ("init", case_init), ("lifecycle", case_lifecycle), ("crlf", case_crlf), ("names", case_names), ("paths", case_paths),
    ("split", case_split_files), ("locks", case_locks), ("interrupt", case_interrupt), ("plans", case_stored_plan),
    ("seal", case_identity), ("serve", case_serve), ("machine", case_json_exitcodes), ("examples", case_examples), ("mcp", case_mcp), ("hammer", case_hammer), ("coldinit", case_coldinit), ("skill", case_skill), ("agent", case_agent),
]


def run_suite(pravo, scratch, only):
    results = {}
    root = os.path.realpath(tempfile.mkdtemp(prefix="x plat é ", dir=scratch))
    base = Ctx(pravo, root, "base")
    t0 = time.time()
    r = base.init_base()
    out = {"cases": {}, "obs": {}, "local": []}
    for name, fn in CASES:
        if only and name not in only:
            continue
        c = Ctx(pravo, root, name)
        c.base, c.base_init = base.base, base.base_init
        c.home, c.tmp = base.home, base.tmp  # the provider cache lives in the home and is shared
        t = time.time()
        status, why = "PASS", ""
        try:
            if name != "init" and r.rc != 0:
                raise Skip("pravo init failed")
            fn(c)
            if c.fails:
                status, why = "FAIL", " ;; ".join(c.fails)
        except Skip as e:
            status, why = "SKIP", str(e)
        except subprocess.TimeoutExpired as e:
            status, why = "FAIL", f"timeout: {e.cmd}"
        except Exception:
            status, why = "FAIL", "exception: " + traceback.format_exc().strip().split("\n")[-1] + " @ " + traceback.format_exc().strip().split("\n")[-3].strip()
        finally:
            c.reap()
        out["cases"][name] = {"status": status, "why": why, "seconds": round(time.time() - t, 1)}
        out["obs"].update(c.obs_)
        out["local"] += sorted(c.local_)
        print(f"  {status:4} {name:10} {time.time() - t:6.1f}s  {why[:300]}", flush=True)
    out["seconds"] = round(time.time() - t0, 1)
    shutil.rmtree(root, ignore_errors=True)
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--pravo", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--repeat", type=int, default=1)
    ap.add_argument("--only", default="")
    ap.add_argument("--label", default=SYSTEM)
    a = ap.parse_args()
    pravo = os.path.abspath(a.pravo)
    out = os.path.abspath(a.out)
    os.makedirs(out, exist_ok=True)
    scratch = os.path.join(out, "scratch")
    os.makedirs(scratch, exist_ok=True)
    v = subprocess.run([pravo, "version"], capture_output=True, text=True).stdout.strip().split("\n")[0]
    only = {s for s in a.only.split(",") if s}
    runs = []
    for i in range(a.repeat):
        print(f"== {a.label} run {i + 1} of {a.repeat}: {v}", flush=True)
        runs.append(run_suite(pravo, scratch, only))
    shutil.rmtree(scratch, ignore_errors=True)
    # flakes: every repeat must agree with the first on every observation and every status
    flakes = []
    for i, r in enumerate(runs[1:], start=2):
        for k in sorted(set(runs[0]["obs"]) | set(r["obs"])):
            if runs[0]["obs"].get(k) != r["obs"].get(k):
                flakes.append({"run": i, "key": k, "first": runs[0]["obs"].get(k), "this": r["obs"].get(k)})
        for k in runs[0]["cases"]:
            if runs[0]["cases"][k]["status"] != r["cases"].get(k, {}).get("status"):
                flakes.append({"run": i, "case": k, "first": runs[0]["cases"][k], "this": r["cases"].get(k)})
    statuses = {}
    for r in runs:
        for k, v_ in r["cases"].items():
            statuses.setdefault(k, []).append(v_["status"])
    doc = {"system": a.label, "version": v, "python": sys.version.split()[0], "runs": runs, "flakes": flakes, "statuses": statuses}
    with open(os.path.join(out, "results.json"), "w", encoding="utf-8") as fh:
        json.dump(doc, fh, indent=1, sort_keys=True, ensure_ascii=True)
    print()
    for k, v_ in statuses.items():
        print(f"{k:10} " + " ".join(s[0] for s in v_))
    bad = [k for k, v_ in statuses.items() if "FAIL" in v_]
    print(f"\n{a.label}: {sum(len(v_) for v_ in statuses.values())} case runs, {sum(s == 'PASS' for v_ in statuses.values() for s in v_)} passed, "
          f"{sum(s == 'FAIL' for v_ in statuses.values() for s in v_)} failed, {sum(s == 'SKIP' for v_ in statuses.values() for s in v_)} skipped, {len(flakes)} observations that changed between repeats")
    sys.exit(1 if bad or flakes else 0)


if __name__ == "__main__":
    main()
