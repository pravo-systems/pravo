#!/usr/bin/env python3
"""Sets the results of tools/xplat/xplat.py from several systems side by side.

    python3 tools/xplat/compare.py linux=out/linux/results.json windows=out/windows/results.json macos=out/macos/results.json

The first system is the reference. Prints, for each case, the status on each system, then every
observation that differs between systems, and exits 1 when a case failed anywhere, an
observation differs (a key that the case marked local is only listed), or a repeat disagreed with
its first run.
"""
import json
import sys


def main():
    systems = []
    for a in sys.argv[1:]:
        name, path = a.split("=", 1)
        try:
            with open(path) as f:
                systems.append((name, json.load(f)))
        except Exception as e:
            print(f"{name}: no results ({e})")
            systems.append((name, None))
    names = [n for n, d in systems]
    ok = True
    print("| case | " + " | ".join(names) + " |")
    print("|---|" + "---|" * len(names))
    cases = []
    for n, d in systems:
        if d:
            for c in d["statuses"]:
                if c not in cases:
                    cases.append(c)
    for c in cases:
        row = []
        for n, d in systems:
            if not d:
                row.append("missing")
                ok = False
                continue
            st = d["statuses"].get(c)
            if not st:
                row.append("-")
                continue
            row.append(f"{st.count('PASS')}P {st.count('FAIL')}F" + (f" {st.count('SKIP')}S" if "SKIP" in st else ""))
            if "FAIL" in st:
                ok = False
        print(f"| {c} | " + " | ".join(row) + " |")
    print()
    for n, d in systems:
        if not d:
            continue
        for c in cases:
            for i, run in enumerate(d["runs"], 1):
                v = run["cases"].get(c)
                if v and v["status"] == "FAIL":
                    print(f"FAIL {n} {c} (run {i}): {v['why']}")
                    break
    print()
    ref_name, ref = next(((n, d) for n, d in systems if d), (None, None))
    diffs = 0
    local = set()
    for n, d in systems:
        if d:
            for r in d["runs"]:
                local |= set(r["local"])
    if ref:
        keys = set()
        for n, d in systems:
            if d:
                keys |= set(d["runs"][0]["obs"])
        for k in sorted(keys):
            vals = {n: d["runs"][0]["obs"].get(k, "<absent>") for n, d in systems if d}
            if len({json.dumps(v, sort_keys=True) for v in vals.values()}) > 1:
                tag = "local" if k in local else "DIFF"
                if tag == "DIFF":
                    diffs += 1
                    ok = False
                print(f"{tag} {k}")
                for n, v in vals.items():
                    print(f"    {n:8} {json.dumps(v, ensure_ascii=False)[:600]}")
    print()
    for n, d in systems:
        if d and d["flakes"]:
            print(f"{n}: {len(d['flakes'])} observations changed between repeats:")
            for f in d["flakes"][:20]:
                print("   ", json.dumps(f, ensure_ascii=False)[:500])
            ok = False
    for n, d in systems:
        if d:
            tot = sum(len(v) for v in d["statuses"].values())
            print(f"{n}: {d['version']}, {len(d['runs'])} run(s), {tot} case runs, "
                  f"{sum(s == 'PASS' for v in d['statuses'].values() for s in v)} passed, "
                  f"{sum(s == 'FAIL' for v in d['statuses'].values() for s in v)} failed, "
                  f"{sum(s == 'SKIP' for v in d['statuses'].values() for s in v)} skipped, {len(d['flakes'])} flakes")
    print(f"\n{diffs} observations differ between systems")
    sys.exit(0 if ok else 1)


main()
