<!-- Generated from docs/how-to/seal-the-state.md by tools/skillgen. Do not edit. -->

# Seal the state to a list of readers

Encrypt the attributes, outputs and stored plans of a state in a bucket to a list of readers that the repository keeps, add and remove a reader, and let a CI job read.

A state holds the attributes of every resource: passwords, keys and tokens that a provider returned. Anyone who can read the bucket can read them. This page seals the state, so that the bucket, a backup or an old version of an object shows addresses and types and no value. A list of readers, kept in the repository, decides who can open it.

> [!NOTE]
> Every command below runs against `tools/fakes3`, the in-memory S3 server in pravo's source, on port 9000, and the three people are three directories on one machine. The state is created with `pravo state import-tfstate` so that the page needs no provider. Sealing stops at format 3: there are no roles and no signatures, and [what sealing protects](../concepts/sealed-state.md) says what that leaves open.

## 1. Make an identity

An identity is a reader key, which opens the state, and a signing key. `pravo id new` makes both and seals the reader key under a passphrase. It touches no state, and anyone can run it at any time. On a terminal it asks for the passphrase twice. This page sets `PRAVO_PASSPHRASE` so that the command runs in a script.

```sh
mkdir -p people
pravo id new --name ada
```

Output:

```text
Identity ada created
  signing  ssh-ed25519 SHA256:<fingerprint>  new key, kept in the identity file
  reader   x25519:<key>  sealed with a passphrase in people/ada/id/ada

Give whoever keeps .pravo/access/readers.txt this line, and nothing else:

  ada ssh-ed25519 <ssh-key> x25519:<key>
```

The identity is a file in `~/.config/pravo/id`, apart from `~/.pravo`, which is the provider cache and may be deleted. The line at the end is public. To reuse an SSH key as the signing key, add `--ssh ~/.ssh/id_ed25519`.

## 2. Have a state to seal

Point the root at the bucket and give it some records. The `terraform.tfstate` below stands for a state that an earlier tool wrote, with a password in it.

File `main.cue`:

```cue
package ws

$settings: state: {
	backend:    "s3"
	bucket:     "northwind-state"
	prefix:     "prod"
	region:     "eu-west-1"
	endpoint:   "http://127.0.0.1:9000"
	path_style: true
}
```

```sh
pravo state import-tfstate terraform.tfstate
```

Output:

```text
Imported 2 resources and 1 outputs from terraform.tfstate into s3://northwind-state/prod
```

The bucket now holds the password in plain text:

```sh
curl -s "$FAKES3_URL/northwind-state/prod/resources/aws_db_instance.main.json" | grep master_password
```

Output:

```text
    "master_password": "example-password",
```

## 3. Write the readers file

Dan and Eve run the same command on their machines and send their lines. The repository keeps the lines in `.pravo/access/readers.txt`, one for each reader. This file, reviewed like any other change, is the whole access policy: whoever can merge a change to it decides who may read.

```sh
mkdir -p .pravo/access
pravo id show | tail -n 1 | sed 's/^ *//' > .pravo/access/readers.txt
for p in dan eve; do
  PRAVO_CONFIG_DIR=people/$p pravo id new --name $p | tail -n 1 | sed 's/^ *//' >> .pravo/access/readers.txt
done
cat .pravo/access/readers.txt
```

Output:

```text
ada ssh-ed25519 <ssh-key> x25519:<key>
dan ssh-ed25519 <ssh-key> x25519:<key>
eve ssh-ed25519 <ssh-key> x25519:<key>
```

Writers take the list from the checked-out repository and never from the bucket, so someone who can only write to the bucket cannot add themselves. The reference page describes [the format of the file](../reference/protect.md#the-readers-file).

From here on the repository expects the state to be sealed. A command that writes a state that is not sealed stops with exit code 5 until step 5 has run, and the `Sealed` line of `pravo protect status` ends with `the repository expects a sealed state`. The same check notices a state that someone lowered to an earlier format in the bucket.

## 4. Check that the store is ready

Before it changes anything, `pravo protect check` tests the store harder than the probe that every command makes: eight writers race to replace one object and exactly one must win. It also lists what is running now. It reads and writes nothing in the state.

```sh
pravo protect check
```

Output:

```text
Store            s3://northwind-state/prod  (eu-west-1)
Conditional      create-only ok · replace ok · delete ok
                 8 concurrent replaces: 1 winner · listing sees a new key at once
State            2 records · format 1 · last written by pravo <version>
Readers file     .pravo/access/readers.txt · 3 readers (ada dan eve)
Running          0 transactions · 0 locks held
Machines         every writer in the last 30 days used pravo >= <version>
Ready to seal. Next: pravo protect init --encrypt
```

A store that ignores conditional writes is refused, because the records are rewritten as objects that are never overwritten, and that needs create-only writes to be real. A state in a local directory is refused too: its compare-and-swap is a lock of one user. If a command is running or a lock is held, the check says `Not ready` and exits 4. Announce a short freeze, then continue.

## 5. Seal the state

`pravo protect init --encrypt` is the only step that changes the store. It shows what it will do and asks for a yes. `--yes` answers the question in a script.

```sh
pravo protect init --encrypt --yes
```

Output:

```text
Readers  .pravo/access/readers.txt  (ada dan eve)
This will:
  1  write 2 records as immutable objects  rec/<sha256>
  2  encrypt their attributes and private data to 3 readers (ada dan eve)
  3  seal outputs.json, stored-plan values and the values in transaction command lines the same way
  4  set the state to format 3. Earlier versions of pravo will refuse it
store check passed
2 records sealed in <time> s
outputs sealed · recipients written to meta.json

The state is sealed to ada dan eve.

Commit the readers file. Writers read the list from git:
  .pravo/access/readers.txt
No recovery recipient. If every reader loses their key, the state cannot be read.
```

The command refuses to run if your identity is not on the list, because sealing to a list you are not on would lock you out. A run that is interrupted leaves the state sealing; run the same command again to finish.

The line about a recovery recipient is a fact and not a step: there is none. If every reader loses their key, nothing can read the state.

## 6. Commit the readers file

From now on, merging a change to this file is the act of granting or removing read access.

```sh
git add .pravo/access/readers.txt
git commit -m "Seal the production state"
git push
```

Every pravo run that writes the state compares the file in the repository with the readers recorded in the state. If they differ, the run says so and refuses, so an edit to the file is never silently ignored.

## 7. See where you stand

`pravo protect status` is the command to run when you are unsure. It opens the state with your identity, checks every record against its name and prints one screen.

```sh
pravo protect status
```

Output:

```text
Sealed       on · format 3 · 3 readers (ada dan eve) · recovery none
Store        s3://northwind-state/prod
Readers      .pravo/access/readers.txt matches the store
You          ada · reader key x25519:<key>
Roles        off
Unsettled    none
```

## 8. A leaked bucket

Someone copies the bucket. The password is in none of its objects, and a person with no identity can list what exists but cannot read a value.

```sh
for k in $(curl -s "$FAKES3_URL/northwind-state?list-type=2&prefix=prod/" | grep -o '<Key>[^<]*' | sed 's/<Key>//'); do
  curl -s "$FAKES3_URL/northwind-state/$k"
done | grep -c example-password || true
```

Output:

```text
0
```

```sh
PRAVO_CONFIG_DIR=nobody pravo state list
PRAVO_CONFIG_DIR=nobody pravo state show aws_db_instance.main; echo "exit=$?"
```

Output:

```text
aws_db_instance.main
aws_vpc.main

Error: you are not a reader of this state (no identity found).
  Readers are listed in .pravo/access/readers.txt. Ask one of them to add yours.
exit=5
```

Addresses, types, names, dependencies and revisions stay readable. That is what keeps the graph, the locks and the viewer working, and it is the price of it. An old backup or a previous version of an object shows the same: it opens with the keys that were in force then.

## 9. Let a CI job read

A job that plans and applies must open the state, so it is a reader with a key of its own. A job cannot type a passphrase, so its identity file is not sealed: `--ci` writes both private keys to a file, which you store as the CI secret `PRAVO_IDENTITY` and then delete. Use one identity for each job, and never a person's identity.

```sh
pravo id new --ci --name deploy-bot --out deploy-bot.identity
```

Output:

```text
Identity deploy-bot created for a job. It has no passphrase: protect the file.
  private  deploy-bot.identity   (store it as the CI secret PRAVO_IDENTITY, then delete this file)
  public   deploy-bot ssh-ed25519 <ssh-key> x25519:<key>
```

Add the public line to the readers file, commit it, and let an existing reader run `rewrap`. It locks the existing data keys for the new reader and rewrites `meta.json`. It does not rewrite the records. Only an existing reader can run it, because only a reader holds a data key to lock.

```sh
PRAVO_IDENTITY="$(cat deploy-bot.identity)" pravo id show | tail -n 1 | sed 's/^ *//' >> .pravo/access/readers.txt
pravo protect rewrap
```

Output:

```text
Readers in .pravo/access/readers.txt: ada dan eve deploy-bot
Wrapped the data keys for deploy-bot (1 reader group, 1 new wrapped key)
meta.json updated
```

The job opens the state with its own identity:

```sh
PRAVO_CONFIG_DIR=nobody PRAVO_IDENTITY="$(cat deploy-bot.identity)" pravo protect status | grep '^You'
```

Output:

```text
You          deploy-bot · reader key x25519:<key>
```

## 10. Remove a reader

Say Eve leaves. Delete her line from the file, commit it and run `rewrap`.

```sh
grep -v '^eve ' .pravo/access/readers.txt > readers.new && mv readers.new .pravo/access/readers.txt
pravo protect rewrap
```

Output:

```text
Readers in .pravo/access/readers.txt: ada dan deploy-bot  (eve removed)
New data keys for 1 reader group. Records are sealed again as they are next written.
```

Removal has three parts, and the last is yours.

- The line stops **future** records from being sealed to Eve. It cannot make Eve forget what Eve read, and a copy of the bucket that was made before is still readable with Eve's key.
- `pravo protect rewrap --all` seals every record, the outputs and the stored plans again under the new key now, instead of when each is next written. It refuses, with exit code 4, while a command is running or a lock is held, as `pravo protect init` does. It does not remove the old group from `meta.json`, and the last record of a resource that was destroyed stays in the bucket, readable by the readers of its group.
- Replace the secrets Eve could read where they live: in the cloud, in the application, in a secret manager.

```sh
pravo protect rewrap --all
```

Output:

```text
Readers in .pravo/access/readers.txt: ada dan deploy-bot
Sealed 2 records again in <time> s.
```

## What next

- [pravo id and pravo protect](../reference/protect.md) lists every flag, the exit codes and the format of the readers file.
- [What sealing protects](../concepts/sealed-state.md) lists what the seal covers, what it does not, and the risks that remain.
- [CI](ci.md) runs plan and apply as separate jobs. Give the job `PRAVO_IDENTITY` as in step 9.
- [Exit codes](../reference/exit-codes.md#7-untrusted) describes code 7.
