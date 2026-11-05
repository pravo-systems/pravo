#!/usr/bin/env python3
"""A state made on one system and used on another: a team with a laptop on Windows and CI on Linux.

    python3 tools/xplat/estate.py make  --pravo P --out DIR           # writes DIR/estate.tar
    python3 tools/xplat/estate.py use   --pravo P --in estate.tar --out DIR   # reads it, changes it, writes DIR/back.tar
    python3 tools/xplat/estate.py back  --pravo P --in back.tar --out DIR     # reads what the other system wrote, destroys it

Each stage ends in PASS or FAIL lines and exits 1 on a failure. The project is committed the way a team does:
the specification, the lock file and the state, and not the generated schemas (cue.mod/gen), so every
system runs `pravo init` first, and that has to add the hashes of its own system to the lock file and keep the others.
"""
import argparse
import os
import re
import shutil
import sys
import tarfile
import tempfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import xplat  # noqa: E402
from xplat import Ctx, read, write, LIFE  # noqa: E402


def pack(d, tarpath):
    with tarfile.open(tarpath, "w") as t:
        for dp, dn, fn in os.walk(d):
            dn[:] = [x for x in dn if not (os.path.relpath(os.path.join(dp, x), d).replace("\\", "/") == "cue.mod/gen")]
            for n in fn:
                p = os.path.join(dp, n)
                t.add(p, arcname=os.path.relpath(p, d).replace("\\", "/"), recursive=False)


def unpack(tarpath, d):
    with tarfile.open(tarpath) as t:
        try:
            t.extractall(d, filter="data")
        except TypeError:
            t.extractall(d)


def platforms(d):
    lock = read(os.path.join(d, "pravo.lock.cue")).decode()
    return sorted(set(re.findall(r'"binary:([a-z0-9]+_[a-z0-9]+)":', lock)))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("stage", choices=["make", "use", "back"])
    ap.add_argument("--pravo", required=True)
    ap.add_argument("--in", dest="inp")
    ap.add_argument("--out", required=True)
    a = ap.parse_args()
    os.makedirs(a.out, exist_ok=True)
    root = os.path.realpath(tempfile.mkdtemp(prefix="estate ", dir=a.out))
    c = Ctx(os.path.abspath(a.pravo), root, a.stage)
    fails = []

    def check(cond, msg):
        print(("PASS " + msg.split(":")[0]) if cond else ("FAIL " + msg), flush=True)
        if not cond:
            fails.append(msg)

    def run(args, d, **kw):
        r = c.run(args, cwd=d, **kw)
        return r

    d = os.path.join(root, "estate")
    if a.stage == "make":
        os.makedirs(d)
        write(os.path.join(d, "main.cue"), LIFE)
        r = run(["init"], d, timeout=600)
        check(r.rc == 0, "init")
        r = run(["apply", "--auto-approve"], d)
        check(r.rc == 0, f"apply: {c.norm(r.all)[-200:]}")
        check(os.path.exists(os.path.join(d, "out", "f.txt")), "the file was written")
        print("platforms in the lock file:", platforms(d))
        pack(d, os.path.join(a.out, "estate.tar"))
    elif a.stage == "use":
        unpack(a.inp, d)
        before = platforms(d)
        r = run(["init"], d, timeout=600)
        check(r.rc == 0, f"init on {xplat.SYSTEM}: {c.norm(r.all)[-200:]}")
        after = platforms(d)
        print("platforms before and after init:", before, after)
        check(set(before) <= set(after), "init kept the hashes that the other system wrote")
        check(any(p.startswith(xplat.SYSTEM + "_") for p in after), f"init added the hash of {xplat.SYSTEM}")
        r = run(["plan", "--detailed-exitcode", "--refresh=all"], d)
        check(r.rc == 0, f"plan --refresh=all of a state written on another system exits 0, got {r.rc}: {c.norm(r.all)[-300:]}")
        f = os.path.join(d, "out", "f.txt")
        r = run(["state", "list"], d)
        check(sorted(r.out.split()) == ["f", "n", "r"], f"state list: {r.out.split()}")
        # change the specification here and apply
        spec = read(os.path.join(d, "main.cue")).decode().replace("second line", "third line")
        write(os.path.join(d, "main.cue"), spec)
        r = run(["apply", "--auto-approve"], d)
        check(r.rc == 0, f"apply of a change: {c.norm(r.all)[-300:]}")
        check(os.path.exists(f) and b"third line" in read(f), "the changed file was written")
        check(b"\r" not in read(f), "the file holds a carriage return")
        r = run(["plan", "--detailed-exitcode"], d)
        check(r.rc == 0, f"plan after the change exits 0, got {r.rc}")
        pack(d, os.path.join(a.out, f"back-{xplat.SYSTEM}.tar"))
    else:
        unpack(a.inp, d)
        r = run(["init"], d, timeout=600)
        check(r.rc == 0, f"init: {c.norm(r.all)[-200:]}")
        r = run(["plan", "--detailed-exitcode", "--refresh=all"], d)
        check(r.rc == 0, f"plan --refresh=all of a state written on another system exits 0, got {r.rc}: {c.norm(r.all)[-300:]}")
        r = run(["state", "log"], d)
        check(r.rc == 0 and len(re.findall(r"^\S+\s+apply\s+applied", r.out, flags=re.M)) == 2, f"two applies in the log: {c.norm(r.out)[:300]}")
        r = run(["destroy", "--auto-approve"], d)
        check(r.rc == 0, f"destroy: {c.norm(r.all)[-200:]}")
        check(not os.path.exists(os.path.join(d, "out", "f.txt")), "destroy removed the file")
        check(run(["state", "list"], d).out.strip() == "", "the state is empty")
    shutil.rmtree(root, ignore_errors=True)
    sys.exit(1 if fails else 0)


main()
