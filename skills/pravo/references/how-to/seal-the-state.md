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

From here on the repository expects the state to be sealed. A command that writes a state that is not sealed stops with exit code 5 until step 5 has run, and the `Sealed` line of `pravo protect status` ends with `the repository expects a sealed state`. The same check notices a state that someone lowered to an earlier format in the bucket, and a state that does not exist yet: a command that writes stops with exit code 5 and creates nothing, `pravo protect status` says `no state yet` for it, and `pravo protect init --encrypt` creates the state sealed.

The readers file covers every root below the directory that holds `.pravo`, because a command looks for it in `.pravo/access` of the root and then of each parent directory up to the one that holds `.git`. A root in the repository that is not meant to be sealed names another directory with `--access-dir` or `$PRAVO_ACCESS_DIR`, and is then not expected to be sealed.

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
Add one with: pravo protect rewrap --recovery 3/5
```

The command refuses to run if your identity is not on the list, because sealing to a list you are not on would lock you out. A run that is interrupted leaves the state sealing; run the same command again to finish.

The last two lines are a fact and not a step: this state has no recovery recipient, and if every reader loses their key, nothing can read it. [Section 11](#11-add-a-recovery-recipient) adds one. `pravo protect init --encrypt --recovery 3/5` sets one in the step that seals.

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

A job that plans and applies must open the state, so it is a reader with a key of its own. A job cannot type a passphrase, so its identity file is not sealed: `--ci` writes both private keys to a file, which you store as the CI secret `PRAVO_IDENTITY` and then delete. Use one identity for each job, and never a person's identity. `--ci` requires `--name`, and the name is the name of the job.

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

Removal has four parts, and the last is yours.

- The line stops **future** records from being sealed to Eve. It cannot make Eve forget what Eve read, and a copy of the bucket that was made before is still readable with Eve's key.
- `pravo protect rewrap --all` seals every record, the outputs and the stored plans again under the new key now, instead of when each is next written. It refuses, with exit code 4, while a command is running or a lock is held, as `pravo protect init` does. It leaves the old group in `meta.json`.
- `pravo protect gc` drops the old group from `meta.json` once nothing is sealed in it, and deletes the records that no pointer names, among them the last record of a resource that was destroyed. Eve's key then opens nothing that is left in the bucket.
- Replace the secrets Eve could read where they live: in the cloud, in the application, in a secret manager.

```sh
pravo protect rewrap --all
```

Output:

```text
Readers in .pravo/access/readers.txt: ada dan deploy-bot
Sealed 2 records again in <time> s.
```

Now nothing is sealed in the first group, and `gc` drops it once the readers have been the same for 65 seconds: a command that opened the state before the change still seals in the old group until then. `pravo protect gc --dry-run` says what it would do and changes nothing.

```sh
sleep 66
pravo protect gc
```

Output:

```text
No record is garbage: a pointer names every record.
Dropped group g1: nothing refers to it.
```

`gc` needs the state at rest, as `rewrap --all` does, and the readers file as any command that writes the state. A record that no pointer names is marked by the first `gc` that finds it 24 hours old, and deleted by one that runs at least 15 minutes after that, so the records of earlier revisions and of resources that were destroyed go the day after they were replaced, on the second run. Run `gc` again then. A group that still has something sealed in it is kept, and the command says what.

## 11. Add a recovery recipient

Until now, the state can be read by its readers and by no one else. If all three lose their keys, or forget their passphrases, nobody can read it again. A **recovery recipient** is one more reader whose key nobody holds whole: it is made when you set it, split into N shares and forgotten. Any K of the shares rebuild it. `pravo protect init --encrypt --recovery 2/3` sets it in the step that seals; for a state that is sealed already, `rewrap` does.

```sh
pravo protect rewrap --recovery 2/3
```

Output:

```text
Readers in .pravo/access/readers.txt: ada dan deploy-bot
meta.json updated

Recovery: 3 shares written (any 2 rebuild the recovery key):
  ./recovery-shares/share-1-of-3.txt  …  share-3-of-3.txt
Do not commit them. Give each to a different person, offline. They are written once.
Commit the recovery recipient with the readers file:
  .pravo/access/recovery.txt
```

The command wrote three files in `recovery-shares/` with mode 0600, and the public key and the threshold to `.pravo/access/recovery.txt`. The shares are written once. Give each to a different person, keep it offline, and do not commit it. Commit the recovery file with the readers file, so that a writer who has the repository can tell a state that lost its recovery from one that never had it.

```sh
ls recovery-shares
cat .pravo/access/recovery.txt
pravo protect status | grep -E '^(Sealed|Recovery)'
```

Output:

```text
share-1-of-3.txt
share-2-of-3.txt
share-3-of-3.txt
# The recovery recipient of the state: any 2 of 3 shares rebuild its key.
x25519:<key> 2/3
Sealed       on · format 3 · 3 readers (ada dan deploy-bot) · recovery 2 of 3
Recovery     2 of 3 shares · key x25519:<key>… · .pravo/access/recovery.txt matches the state
```

Two shares are enough to read the state. That is a decision about whom to trust together and not a delay: pravo has no signature and no waiting time for it, so two holders who meet can read every value at once. Choose K higher than the number of holders who could act together, and N high enough that losing a share does not lose the key.

## 12. Recover a state

Say Ada, Dan and the job have all lost their keys. Someone makes a new identity, and the list of readers names it in place of the lost ones.

```sh
PRAVO_CONFIG_DIR=people/fay pravo id new --name fay > /dev/null
PRAVO_CONFIG_DIR=people/fay pravo id show | tail -n 1 | sed 's/^ *//' > .pravo/access/readers.txt
cat .pravo/access/readers.txt
```

Output:

```text
fay ssh-ed25519 <ssh-key> x25519:<key>
```

Two holders bring their share files. `pravo protect recover` rebuilds the key in memory, checks it against the public key in the state, and shows what it will do.

```sh
PRAVO_CONFIG_DIR=people/fay pravo protect recover --share recovery-shares/share-1-of-3.txt --share recovery-shares/share-3-of-3.txt --yes
```

Output:

```text
Recovery key rebuilt from 2 shares.
Readers  .pravo/access/readers.txt  (fay)
This will:
  1  lock the data keys for fay
  2  remove ada dan deploy-bot, and make a new data key
meta.json updated
New data keys for 1 reader group. Records are sealed again as they are next written.
The recovery key was rebuilt on this machine. Replace it: pravo protect rewrap --recovery K/N
```

Fay is the reader now, and the old readers are not. The key was rebuilt on one machine, so it has been seen in one place: replace it, which makes a new key and three new shares, and the old shares open nothing that is written afterwards.

`rewrap` asks before it replaces or removes a recovery, because the shares that were given out stop working for new records. `--yes` answers the question, as it does for `init`.

```sh
rm -r recovery-shares
PRAVO_CONFIG_DIR=people/fay pravo protect rewrap --recovery 2/3 --yes
```

Output:

```text
The recovery key will be replaced (2 of 3 shares, key x25519:<key>…). The shares that were given out open nothing that is written from now on.
Readers in .pravo/access/readers.txt: fay
meta.json updated

Recovery: 3 shares written (any 2 rebuild the recovery key):
  ./recovery-shares/share-1-of-3.txt  …  share-3-of-3.txt
Do not commit them. Give each to a different person, offline. They are written once.
Commit the recovery recipient with the readers file:
  .pravo/access/recovery.txt
The old shares open nothing that is written from now on. Collect them and destroy them.
```

## 13. Take the seal off

Sealing is not a one-way door. `pravo protect unseal` opens every record, the outputs and the stored plans with your key, writes them in the open and removes the sealed part of `meta.json`. The state keeps its format and its lineage: it is the same state, without the encryption. After it, anyone who can read the bucket can read every value.

The command shows what it will do and asks. This page is a script, so `--yes` answers.

```sh
PRAVO_CONFIG_DIR=people/fay pravo protect unseal --yes
```

Output:

```text
State    s3://northwind-state/prod  (sealed)
This will:
  1  open 2 records, the outputs and the stored plans with your key (fay)
  2  write them in the open, and delete the sealed records
  3  take the sealed part out of meta.json; the format stays
Afterwards anyone who can read the bucket can read every value. The readers (fay) lose nothing, and the bucket loses the seal.
2 records unsealed in <time> s
outputs unsealed · the sealed part of meta.json is gone

The state is not sealed any more (format 3).
The repository still expects a sealed state, and writers refuse this one (exit code 5) until these are removed:
  .pravo/access/readers.txt
  .pravo/access/recovery.txt
Remove them and commit the deletion, or run this again with --remove-files.
Destroy the recovery shares, if there were any: they open nothing now.
```

The repository still says that the state is sealed. A command that writes refuses the unsealed state until the readers file and the recovery file are gone, which is the same check that catches a bucket whose `meta.json` was edited.

```sh
PRAVO_CONFIG_DIR=people/fay pravo state rm aws_db_instance.main; echo "exit=$?"
```

Output:

```text
Error: the repository lists readers in .pravo/access/readers.txt, but the state is not sealed: it has not been sealed yet, or its meta.json was changed. Run pravo protect status; to seal it, pravo protect init --encrypt; if the state is not meant to be sealed, remove the readers file.
exit=5
```

`--remove-files` deletes both files. Commit the deletion, and destroy the recovery shares.

```sh
PRAVO_CONFIG_DIR=people/fay pravo protect unseal --remove-files
PRAVO_CONFIG_DIR=people/fay pravo protect status | grep -E '^(Sealed|Readers)'
```

Output:

```text
The state is not sealed.
Removed .pravo/access/readers.txt; commit the deletion.
Removed .pravo/access/recovery.txt; commit the deletion.
Sealed       off · format 3 · immutable records
Readers      file not found (looked in .pravo/access)
```

A state that is unsealed is sealed again with `pravo protect init --encrypt`, to the same readers or to others. The groups of the new seal carry on from the number where the first seal stopped.

## What next

- [pravo id and pravo protect](../reference/protect.md) lists every flag, the exit codes and the format of the readers file.
- [What sealing protects](../concepts/sealed-state.md) lists what the seal covers, what it does not, and the risks that remain.
- [CI](ci.md) runs plan and apply as separate jobs. Give the job `PRAVO_IDENTITY` as in step 9.
- [protect unseal](../reference/protect.md#protect-unseal) lists what the unseal does, in what order, and what it cannot restore.
- [The recovery recipient](../reference/protect.md#the-recovery-recipient) says what the files hold, what the writers check and what to do after a recovery.
- [Exit codes](../reference/exit-codes.md#7-untrusted) describes code 7.
