---
title: Store secrets in the sealed state
description: Keep a secret in the sealed state, hand it to a provider with pravo.#Secret, rotate it with pravo.#SecretVersion, and see who set each version.
order: 13
minutes: 20
needs: A sealed state in a bucket, as in [Seal the state to a list of readers](seal-the-state.md), and [Keep secrets out of the state](secrets.md) for what a run may do with a secret. This page uses the in-memory bucket from pravo's source.
done_when: "`pravo secret list` shows the secret, a provider receives its value, and neither the bucket nor a plan file holds a copy of it."
level: intermediate
group: Operate
services:
  - fakes3:9000
env:
  AWS_ACCESS_KEY_ID: test
  AWS_SECRET_ACCESS_KEY: test
  PRAVO_PASSPHRASE: an example passphrase
  PRAVO_CONFIG_DIR: people/ada
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: 'ssh-ed25519 SHA256:[A-Za-z0-9+/]{43}'
    replace: 'ssh-ed25519 SHA256:<fingerprint>'
  - pattern: 'AAAAC3NzaC1lZDI1NTE5AAAA[A-Za-z0-9+/]{44}'
    replace: '<ssh-key>'
  - pattern: 'x25519:[A-Za-z0-9_-]{43}'
    replace: 'x25519:<key>'
  - pattern: 'roles-genesis [0-9a-f]{64}'
    replace: 'roles-genesis <sha256>'
  - pattern: '[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}'
    replace: '<txn>'
  - pattern: '[0-9]{4}-[0-9]{2}-[0-9]{2} [0-9:]{8}( +[0-9.]+(µs|ms|s))?'
    replace: '<time>'
  - pattern: '\d+[smh] ago +'
    replace: '<age> ago  '
  - pattern: 'SET +BY'
    replace: 'SET        BY'
  - pattern: '\[id=[^\]]+\]'
    replace: '[id=<id>]'
  - pattern: 'after [0-9.]+(µs|ms|s)'
    replace: 'after <time>'
keywords: "vault sops password token credential private key rotate version encrypted store secret manager"
---

A secret is a named value that the sealed state keeps for its readers: a database password, an API token, a private key. Each time you set it, it gains a version. A run reads it with `pravo.#Secret` and hands it to a provider, and the value reaches no record of the state, no plan and no log. The bucket holds the value sealed to the same readers as the records, and the transactions of the state say who set each version.

Everyone who can read the state can read every secret in it. A secret for other readers belongs in another state, with another list of readers.

> [!NOTE]
> Every command below runs against `tools/fakes3`, the in-memory S3 server in pravo's source, on port 9000, and the three people are three directories on one machine. The page uses the `tls` provider, which `pravo init` downloads.

## 1. Seal a state with roles

[Seal the state to a list of readers](seal-the-state.md) explains this step. Here it is in short: three people, each with an identity, listed in `.pravo/access/readers.txt` with a role. Ada is an admin, Dan a writer and Eve a reader. The roles decide who may set a secret, who may remove one and who may only read.

```cue file=main.cue
package vault

$settings: state: {
	backend:    "s3"
	bucket:     "northwind-state"
	prefix:     "prod"
	region:     "eu-west-1"
	endpoint:   "http://127.0.0.1:9000"
	path_style: true
}
```

```sh run
mkdir -p people .pravo/access
pravo id new --name ada > /dev/null
for p in dan eve; do PRAVO_CONFIG_DIR=people/$p pravo id new --name $p > /dev/null; done
{
  pravo id show | tail -n 1 | sed 's/^ *//; s/$/ role:admin/'
  PRAVO_CONFIG_DIR=people/dan pravo id show | tail -n 1 | sed 's/^ *//; s/$/ role:writer/'
  PRAVO_CONFIG_DIR=people/eve pravo id show | tail -n 1 | sed 's/^ *//; s/$/ role:reader/'
} > .pravo/access/readers.txt
cat .pravo/access/readers.txt
```

```text out
ada ssh-ed25519 <ssh-key> x25519:<key> role:admin
dan ssh-ed25519 <ssh-key> x25519:<key> role:writer
eve ssh-ed25519 <ssh-key> x25519:<key> role:reader
```

Seal the state, then lock the roles in the state, so that a reader cannot make themselves an admin by editing the file in their checkout.

```sh run
pravo init > /dev/null
pravo protect init --encrypt --yes | tail -n 7
pravo protect roles --yes
```

```text out
The state is sealed to ada dan eve.

Commit the readers file. Writers read the list from git:
  .pravo/access/readers.txt
No recovery recipient. If every reader loses their key, the state cannot be read.
Add one with: pravo protect rewrap --recovery 3/5
  ada: admin
  dan: writer
  eve: reader
Stored the roles as version 1.
Added roles-genesis <sha256> to .pravo/access/readers.txt. Commit it: until it is in the repository the roles of the file decide.
```

Commit `.pravo/access/readers.txt` with the `roles-genesis` line. A state that holds secrets is in format 5, which earlier versions of pravo refuse to open. The first `pravo secret set` raises the format, and a state with no secret stays as it is.

## 2. Set a secret

A writer sets a secret. The value is never an argument, so that the shell history and the list of processes do not hold it. `secret set` reads it from standard input, from a file with `--file`, from an environment variable with `--from-env`, or on a terminal from a prompt that does not show it.

```sh run
printf %s 'correct-horse-battery' | PRAVO_CONFIG_DIR=people/dan pravo secret set prod/db_password --note "first password"
pravo secret list
```

```text out
Set prod/db_password, version 1.
NAME              VERSION  SET        BY
prod/db_password        1  <age> ago  dan
```

A name is words of letters, digits, dots, dashes and underscores, with a slash between parts. `list` shows no value and needs no key. Setting a name again writes a new version and keeps the old ones.

Eve is a reader. She cannot set a secret:

```sh run exit=5
printf %s 'x' | PRAVO_CONFIG_DIR=people/eve pravo secret set prod/db_password
```

```text out
Error: your identity is a reader in .pravo/access/readers.txt, and a writer is needed to change the state.
```

## 3. Read a secret

Every reader can read. `get` prints the bare value, so that a script can use it.

```sh run
PRAVO_CONFIG_DIR=people/eve pravo secret get prod/db_password
```

```text out
correct-horse-battery
```

The bucket holds the name, the number of the version and the time in the clear, and the value sealed. Anyone who can list the bucket sees that a secret exists and when it changed. Nobody without a key of a reader sees what it holds.

```sh run
curl -s "$FAKES3_URL/northwind-state?list-type=2&prefix=prod/secret/" | grep -o '<Key>[^<]*' | sed 's/<Key>prod\///'
for k in $(curl -s "$FAKES3_URL/northwind-state?list-type=2&prefix=prod/" | grep -o '<Key>[^<]*' | sed 's/<Key>//'); do
  curl -s "$FAKES3_URL/northwind-state/$k"
done | grep -c correct-horse || true
```

```text out
secret/prod%002Fdb_password@00000001.json
0
```

## 4. Hand a secret to a provider

A run reads a secret as `pravo.#Secret`. It is an ephemeral value, like `pravo.#SecretEnv` in [Keep secrets out of the state](secrets.md): it may go into the configuration of a provider, into a write-only attribute and into another ephemeral resource, and nowhere that is recorded.

The `tls` provider signs a certificate with a private key that it receives in `private_key_pem_wo`, a write-only attribute. The provider never stores it, so it cannot see that the key changed. Its `private_key_pem_wo_version` is the number that tells it. `pravo.#SecretVersion` is that number: the version of the secret that the run reads, as an ordinary value.

```cue file=secrets.cue title="secrets.cue" hl="9-10,13-14"
package vault

import (
	"pravo.dev/pravo"
	tlsp "pravo.dev/providers/hashicorp/tls"
)

$tls: tlsp.#Provider

signing_key: pravo.#Secret & {name: "prod/signing_key"}
signing_rev: pravo.#SecretVersion & {name: "prod/signing_key"}

cert: $tls.#Resource.tls_self_signed_cert & {
	private_key_pem_wo:         signing_key.value
	private_key_pem_wo_version: signing_rev.version
	subject: [{common_name: "example.test"}]
	validity_period_hours: 24
	allowed_uses: ["digital_signature"]
}
```

The secret does not exist yet. A run stops before any provider starts, and says what to run.

```sh run exit=1
pravo init > /dev/null
PRAVO_CONFIG_DIR=people/dan pravo plan
```

```text out
Error: signing_key: the state has no secret named prod/signing_key. Set it with: pravo secret set prod/signing_key
```

Dan sets the key. Any PEM private key works; this block makes one with `openssl` and passes it on standard input, so no file holds it.

```sh run
openssl ecparam -name prime256v1 -genkey -noout | PRAVO_CONFIG_DIR=people/dan pravo secret set prod/signing_key --note "issued for the example"
PRAVO_CONFIG_DIR=people/dan pravo plan -out change.plan | grep -E '^(Plan|Saved)'
PRAVO_CONFIG_DIR=people/dan pravo apply change.plan | grep '^Apply complete'
```

```text out
Set prod/signing_key, version 1.
Plan: 1 to add, 0 to change, 0 to destroy.
Saved the plan to change.plan. To apply exactly these actions, run:
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
```

Look for the key in the state, in the plan file and in the bucket:

```sh run
PRAVO_CONFIG_DIR=people/dan pravo state show cert | grep private_key_pem_wo
grep -c 'PRIVATE KEY' change.plan || true
for k in $(curl -s "$FAKES3_URL/northwind-state?list-type=2&prefix=prod/" | grep -o '<Key>[^<]*' | sed 's/<Key>//'); do
  curl -s "$FAKES3_URL/northwind-state/$k"
done | grep -c 'PRIVATE KEY' || true
```

```text out
	private_key_pem_wo:         null
	private_key_pem_wo_version: 1
0
0
```

The record holds null for the write-only attribute and the number of the version. The plan file is sealed like the state, and the bucket holds the certificate and no key.

## 5. Break it: a secret in a recorded place

An output is recorded in the state, so a secret may not be an output.

```cue file=leak.cue title="leak.cue" status=rejected hl="5"
package vault

import "pravo.dev/pravo"

shown: pravo.#Output & {value: signing_key.value}
```

```sh run exit=1
PRAVO_CONFIG_DIR=people/dan pravo plan
```

```text out
Error: output shown references signing_key; outputs are recorded in state, so ephemeral values cannot be used in them
```

An ordinary attribute is refused the same way, and the check runs before any provider does. `pravo.#SecretVersion` may go anywhere, since a number is not secret.

```sh run
rm leak.cue
```

## 6. Rotate a secret

Setting the name again is the rotation. The new version is the one that the next run reads, and the number of the version changes the attribute that tells the provider to take the new value.

```sh run
openssl ecparam -name prime256v1 -genkey -noout | PRAVO_CONFIG_DIR=people/dan pravo secret set prod/signing_key --note "rotated after the audit"
PRAVO_CONFIG_DIR=people/dan pravo plan | grep -E 'private_key_pem_wo_version|^Plan'
```

```text out
Set prod/signing_key, version 2.
  ~     private_key_pem_wo_version: 1 -> 2 # forces replacement
Plan: 1 to add, 0 to change, 1 to destroy.
```

The certificate is replaced, and nothing else changes in the plan. Apply it.

```sh run
PRAVO_CONFIG_DIR=people/dan pravo apply -auto-approve | grep '^Apply complete'
```

```text out
Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <txn>]
```

A `pravo.#Secret` reads the latest version at every plan and apply. To hold a node at one version, write `version: 1` in it. The nodes of a run that read the same secret without a version read the same one, so a version that is set while a run goes changes the next run and not this one.

A plan file and the apply of it are two runs, and the apply reads the secret again. When a new version was set in between and the specification reads its number, the provider's plan differs from the saved one: the apply stops with exit code 3, as for any stale plan, and you make the plan again.

## 7. See who set which version

`secret history` lists the versions of a secret, newest first, with when each was set, who set it and the note, which is sealed with the value and so is shown to readers only.

```sh run
pravo secret history prod/signing_key
```

```text out
prod/signing_key
  v2    <time>  dan          value
        rotated after the audit
  v1    <time>  dan          value
        issued for the example
```

Every `secret` command is a transaction of the state, signed by the identity that ran it, as `apply` is. `state log` shows them next to the applies, and `protect status` counts the versions that a signed transaction of a writer accounts for.

```sh run
pravo state log | grep -B1 -A3 'pravo secret set prod/signing_key'
pravo protect status | grep -E '^(Roles|Secrets)'
```

```text out
<txn>  secret   applied   <time>
    pravo secret set prod/signing_key
    signed by dan (verified)
    set prod/signing_key, version 2
    set      secret:prod/signing_key  v1 -> v2
--
<txn>  secret   applied   <time>
    pravo secret set prod/signing_key
    signed by dan (verified)
    set prod/signing_key, version 1
    set      secret:prod/signing_key  v1
Roles        on · 1 admin · 1 writer · 1 reader · pinned, version 1 in the state · you are an admin
Secrets      2 secrets · 3 versions · 3 bound to a transaction of a writer
```

A version that no signed transaction of a writer accounts for is refused when a command reads it, with exit code 7. That is what stops a reader, who holds the data key, from putting a value in the bucket that a writer never set.

## 8. Use a secret in a command

`pravo secret exec` runs a command with secrets in its environment. The values are not written to a file, only the command sees them, and the exit code is that of the command. Everything after `--` belongs to the command.

```sh run
PRAVO_CONFIG_DIR=people/eve pravo secret exec --env PGPASSWORD=prod/db_password -- sh -c 'echo "PGPASSWORD has ${#PGPASSWORD} characters"'
```

```text out
PGPASSWORD has 21 characters
```

## 9. Remove a secret and forget its old values

A writer sets, and an admin removes. Dan cannot remove the password, and Ada can.

```sh run exit=5
PRAVO_CONFIG_DIR=people/dan pravo secret rm prod/db_password --yes
```

```text out
Error: your identity is a writer in .pravo/access/readers.txt, and an admin is needed to remove a secret.
```

```sh run
pravo secret rm prod/db_password --yes
pravo secret list
```

```text out
Removed prod/db_password, version 2. Its values stay in the state until pravo secret purge prod/db_password --keep 0.
NAME              VERSION  SET        BY
prod/db_password        2  <age> ago  ada  removed
prod/signing_key        2  <age> ago  dan
```

A removal is a version of its own. The values stay in the state until an admin forgets them, so that the history shows what was there. A run that reads the removed secret stops before it changes anything.

```cue file=uses_db.cue title="uses_db.cue"
package vault

import "pravo.dev/pravo"

db: pravo.#Secret & {name: "prod/db_password"}
```

```sh run exit=1
PRAVO_CONFIG_DIR=people/dan pravo plan
```

```text out
Error: db: the secret prod/db_password was removed. Set it again with: pravo secret set prod/db_password
```

```sh run
rm uses_db.cue
```

`secret purge` forgets the old values of a secret and keeps the newest ones. The versions stay in the history, without their values.

```sh run
pravo secret purge prod/signing_key --yes
pravo secret history prod/signing_key
PRAVO_CONFIG_DIR=people/eve pravo secret get prod/signing_key --version 1; echo "exit=$?"
```

```text out
Forgot 1 value of prod/signing_key.
prod/signing_key
  v2    <time>  dan          value
        rotated after the audit
  v1    <time>  dan          purged

Error: prod/signing_key version 1: the value of this version was purged
exit=1
```

A purge removes the values from the state. A bucket that keeps earlier versions of its objects keeps these too, and everyone who could read the secret has read it. A value that leaked needs a new one.

## 10. When a reader leaves

Removing a reader from the file stops future versions from being sealed to them. It cannot take back what they read, and every secret they could open is known to them. [Step 10 of sealing the state](seal-the-state.md#10-remove-a-reader) shows the removal. For the secrets, four things follow it.

1. Remove the line, commit the file and run `pravo protect rewrap`. Versions written from now on are sealed to the readers who are left.
2. Replace each secret the person could read at its source, such as the database or the provider of the token, and set the new value with `pravo secret set NAME`.
3. Forget the old values with `pravo secret purge NAME`.
4. Run `pravo protect rewrap --all` and `pravo protect gc`. The first seals what is left under the new key, and the second drops the group the person was in.

`pravo secret history NAME` shows the versions that existed while they were a reader.

## 11. See the secrets in the viewer

`pravo serve` has a Secrets section, opened with `e`. It lists each secret with its latest version, who set it and when, and which nodes of the specification read it. It shows no value: the server holds no key for the values, and it reads the part of the objects that is in the clear and the transactions that wrote them.

It also says what is wrong. A secret that the specification reads and the state does not hold, a version that no signed transaction of a writer accounts for and a state that is not sealed are flagged, worst first, each with the command that mends it to copy. A state with nothing to flag shows one line, `all clear`. The tab carries a number only when something needs action. [The Secrets section](../reference/viewer-sections.md#the-secrets-section) lists the parts.

The nodes that read a secret are in the graph, in the first column with the environment variables. A node's inspector says that its value is read from the sealed state and is never recorded.

## What next

- [pravo secret](../reference/secret.md) lists every flag, the roles, the exit codes and the objects in the bucket.
- [Secrets and ephemeral values](../concepts/secrets-and-ephemeral-values.md) says where each kind of value may go and where it is recorded.
- [Signatures and roles](../concepts/signatures-and-roles.md#secrets) says what the seal covers for secrets and what it leaves open.
- [Roles](../reference/protect.md#roles) says what a reader, a writer and an admin may do.
