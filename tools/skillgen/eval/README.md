# Evaluating the skill

The tests of the repository check that the skill is current and that `SKILL.md` names real commands. They do not check that the skill helps. This directory holds a small evaluation that does: three tasks that an agent does with and without the skill, and a script that checks what the agent left behind with the real binary. Run it before a release, with an agent that can run shell commands.

## Setup

```sh
go build -o bin/pravo ./cmd/pravo
export PRAVO=$PWD/bin/pravo
mkdir -p eval && cd eval
$PRAVO skill install skillhome                 # the skill that the "with" arm reads
mkdir -p t1/with t1/without t2/with t2/without
```

Task 3 starts from a root that has been applied:

```sh
mkdir t3-base && cat > t3-base/main.cue <<'CUE'
package mascots

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider & {$version: "~> 3.6"}

pet: $random.#Resource.random_pet & {length: 2, prefix: "team"}
CUE
export PRAVO_PASSPHRASE="an eval passphrase" PRAVO_CONFIG_DIR=$PWD/people/ada
mkdir -p t3-base/.pravo/access
$PRAVO -C t3-base id new --name ada | grep '^  ada ssh' | sed 's/^  //' > t3-base/.pravo/access/readers.txt
(cd t3-base && $PRAVO init && $PRAVO protect init --encrypt --yes --allow-local && $PRAVO apply --auto-approve)
for arm in with without; do mkdir -p t3/$arm && cp -R t3-base t3/$arm/root; done
```

## The arms

Give each arm a fresh agent, its directory (`tN/with` or `tN/without`), the path of the binary, and these rules: use only the random and local providers, do not read the repository or its documentation, do not use a system temporary directory, never run `pravo destroy`. The "without" arm may run `pravo --help` and `pravo COMMAND --help`. The "with" arm is also told to read `skillhome/pravo/SKILL.md` first and to open the files it names.

## The tasks

1. **A fleet.** Write a root that declares the random and local providers; for each of the regions eu, us and ap creates two `random_pet` resources (the prefix is the region, length 2) and one `local_file` named `out/<region>.txt` whose content lists the two pet ids, one per line; and an output named `files` that lists the three file names. Run init, validate and plan. Do not apply.
2. **A module.** Write a reusable CUE definition for a service with inputs `name` (a string) and `port` (an integer greater than 1024), holding a `random_password` (length 24, no special characters) and a `local_file` named `out/<name>.json` that records the port. Instantiate it as `api` (8080) and `web` (3000). Run init and validate, and apply with auto approval. Then change web's port to 3001, save the plan to `plan.pravo` and show it. Do not apply the second change.
3. **A rename and a policy.** The directory has `root/`, applied, with one `random_pet` named `pet`, whose state is sealed to one reader; the passphrase and the identity directory are in `PRAVO_PASSPHRASE` and `PRAVO_CONFIG_DIR`. Rename the resource to `mascot` so that pravo keeps the existing object and plans no change. Add a policy to the root that refuses a plan that deletes or replaces a managed resource, and apply it. Run the plan for `root/`.

## The check

```sh
for t in t1 t2 t3; do for arm in with without; do
  echo "== $t $arm"; sh tools/skillgen/eval/verify.sh $t eval/$t/$arm
done; done
```

`verify.sh` prints a `PASS` or `FAIL` line for each property: the root loads, the plan has nine creations and the output, the module rejects port 80, the saved plan has a change and the second change was not applied, the rename plans nothing and the state holds the policy, and the policy refuses a destroy with exit code 5. Count what each agent did as well: the number of `pravo` commands, the number that failed, and the wall time. The result that matters is the difference between the arms, which a pass count alone hides.

## Result on 2026-10-22

All six runs passed. The skill changed how the agents got there.

| Task | Tool calls with | Tool calls without | Wall time with, without | What the agent without the skill ran into |
|---|---|---|---|---|
| 1. A fleet | 14 | 20 | 45 s, 92 s | six failed commands: the shape of a provider, the import path, `schema` before a root loads |
| 2. A module | 11 | 26 | 46 s, 136 s | about a dozen probes of the provider syntax, and `mod init` before `init` |
| 3. A rename and a policy | 14 | 13 | 53 s, 70 s | nothing: the plan hints at `$from` |

With the skill, no agent had a failed `pravo` command except the deliberate checks. Without it, the agents found the provider syntax from error messages and the generated packages. Tokens were about the same (the skill costs a read), and the runs with the skill finished in a half and a third of the time on tasks 1 and 2. The tasks are small and use three providers; they do not show how the skill does on a large AWS estate.

The runs without the skill also showed what the tool itself should say. A bare `$providers: {...}` struct validates with no providers and no warning, `pravo schema` prints nothing useful until a root that declares the provider loads, and the error for an import that does not exist does not suggest `pravo.dev/providers/hashicorp/random`.
