<!-- Generated from docs/concepts/the-assistant.md by tools/skillgen. Do not edit. -->

# The assistant

What the read-only assistant of the viewer is, which reads it has and which it has not, what leaves the machine and what never does, how values stay withheld, and what an answer can and cannot be trusted for.

The assistant lets you ask the viewer a question in words. It is a language model, run by a provider that you choose, that reads the same views the page shows and answers with links back into the page. This page explains what it can read, what it can never do, what leaves the machine and how far an answer can be trusted. [Ask questions about your estate](../how-to/ask-questions-about-your-estate.md) turns it on, and [`pravo serve`](../reference/serve.md#the-assistant) lists the flags.

## Off by default

A viewer that was not started with a provider named has no assistant. The page has no Ask control, the server has no route for it, and nothing connects to anything. A provider is named with `PRAVO_CHAT` in the environment or `--chat` on the command, and a key alone does not name one. The key is read from the environment of the `pravo serve` process and from nowhere else: not from the specification, not from the state, not from a file of the project and not from the browser. It stays in the process. The page never holds it, and no response carries it.

## The model reads what the page reads

The model is offered twelve tools, and each is one of the reads that the page makes for itself: the overview of the estate, a search, one resource and the lines that write it, the stored plans and one plan, the policies, the discoveries, the secrets, the protection of the state and the recorded runs. A tool does not reach the engine or the state. It is a call to a fixed read path of the viewer, made in the server with the token of the run, so a value goes through the same redaction as a value on its way to the page. [The viewer](the-viewer.md#withheld-values) says what that withholds: what the provider schema marks sensitive or write-only, what an attribute named like a secret holds, what contains a string that the state keeps in a sensitive attribute, and the values of a sensitive output. A secret has a name and a version and never a value, because the viewer never reads one. A record that the viewer cannot open reaches the model with its address, its type and its dependencies, and nothing else.

The model is told what the markers mean, so that it says that a value is withheld and does not guess it.

## What it cannot do

It has no tool that applies, approves, stores, collects, locks, plans, refreshes or adopts, that runs a discovery, or that writes or reads a secret. The set is a table that the server compiles in. A name that the model asks for and that is not in the table is refused, and nothing runs. The part of the viewer that holds the tools imports nothing that writes, and a test says so. A test also plays a model that tries every one of those actions by name, and by text planted in a tag, a plan reason and a span, and checks that the project and the state hold the same bytes afterwards and that nothing was written to the bucket.

A command that would help is text. The page draws it in a box that copies it, and says that this page runs nothing. The person who reads it runs it, with their own identity, in their own terminal, where the policies and the approvals that apply to them apply.

A question is bounded. It makes at most twelve reads, sends at most 128 KiB of their results, and a single result is cut at 32 KiB. A conversation holds at most 40 turns of at most 4000 characters, and a body at most 64 KiB. At most four questions run at the same time, and the server answers a fifth with an error.

## What leaves the machine

The provider receives, for each question, the instructions to the model, the conversation, the definitions of the tools and the results of the reads that the model asked for. There is no upload of the estate: the model finds what it needs by asking for it.

What a result holds is what the page shows, after the redaction above: the addresses and the types of resources, the values that are not sensitive, the lines of the specification with the literals of sensitive attributes masked, the text of plans and policies, the names of runs and the names and versions of secrets. These are not secret to the viewer, but they can be confidential to the company. That is why the provider is your choice, why a model on your own machine is a supported one, and why each answer lists what it sent.

What is never sent: the value of a secret, a value that the page withholds, the private blob of a record, the configuration of a provider, a credential, the state as a whole, the key of the assistant and the token of the viewer.

The connection goes to the one endpoint that you named. It has to be `https`, or `http` on this machine. A redirect to another host is refused. The standard proxy variables apply. The viewer sends no telemetry to anyone, and the provider's own terms about the retention of what it receives are the provider's and not pravo's.

Under each answer, **Sent to the model** lists every read with the exact text that was put in the request. The claim above can be read there, and not only taken on trust.

## What an answer is worth

An answer is the model's reading of the views, and it can be wrong. Three things limit the damage.

- A name in backticks becomes a link only when the estate has it, so a resource that the model made up is plain text.
- Everything in a result comes from the estate, and the instructions to the model say so: the text of a tag, a description, a plan reason or a span attribute is data to read and not an order to follow. A text that tries to give an order can mislead an answer, and it cannot do more, since there is no tool that changes anything.
- The page keeps the conversation, and the server and the state keep none. Reloading the page, or New chat, ends it.

Check an answer against the node it names. The inspector shows the value, the source and the history that the answer rests on.

## Recap

- The assistant is off until the provider is named, and the key lives in the environment of the process and nowhere else.
- It reads through the viewer's own read paths, so it sees what the page shows, with the same values withheld, and it has no tool that writes.
- A question is bounded, and each answer lists every read and the text sent to the provider for it.
- An answer can be wrong. Its links are real, and the page it links to is the evidence.
