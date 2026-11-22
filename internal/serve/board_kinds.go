package serve

// The kinds of thing the board draws. Three are described by the schema of a provider and written through a member of
// its struct ($aws.#Data.aws_vpc); six are values that pravo reads for itself, written from a definition of the package
// pravo.dev/pravo (pravo.#Env); a template is written from its definition.

// boardSchemaKind is a kind that the schema of a provider describes.
type boardSchemaKind struct {
	// kind is the word of the API, mode the mode of the schema and of the graph, member the member of the provider struct.
	kind, mode, member string
}

var boardSchemaKinds = []boardSchemaKind{
	{"resource", "managed", "#Resource"},
	{"data", "data", "#Data"},
	{"ephemeral", "ephemeral", "#Ephemeral"},
}

// schemaKindOf is the schema kind of a word of the API: "" is a resource.
func schemaKindOf(kind string) (boardSchemaKind, bool) {
	if kind == "" {
		kind = "resource"
	}
	for _, k := range boardSchemaKinds {
		if k.kind == kind {
			return k, true
		}
	}
	return boardSchemaKind{}, false
}

// schemaKindOfMode is the schema kind of a mode of the graph or of a member of a provider struct.
func schemaKindOfMode(mode string) (boardSchemaKind, bool) {
	for _, k := range boardSchemaKinds {
		if k.mode == mode || k.member == mode {
			return k, true
		}
	}
	return boardSchemaKind{}, false
}

// boardBuiltin is a value that pravo reads for itself, drawn from a definition of pravo.dev/pravo.
type boardBuiltin struct {
	kind, def, title, words, label, doc string
	// reads are the attributes another thing reads of it
	reads  []string
	fields []BoardField
}

var boardBuiltins = []boardBuiltin{
	{kind: "output", def: "#Output", title: "Output", words: "output outputs publish export value", label: "out",
		doc: "A value the root publishes: recorded in state at apply, printed by pravo output.",
		fields: []BoardField{
			{Name: "value", Kind: "expr", Type: "any", Required: true, Placeholder: "pet.id", Doc: "The value: any CUE expression. Never an ephemeral value."},
			{Name: "description", Kind: "text", Type: "string", Doc: "What the value is for."},
			{Name: "sensitive", Kind: "bool", Type: "bool", Doc: "Hide the value where outputs are listed. It is still recorded in state."},
		}},
	{kind: "env", def: "#Env", title: "Environment variable", words: "env environment variable variables var vars", label: "env", reads: []string{"value"},
		doc: "An environment variable, read when the run starts. An ordinary value, recorded where it is used.",
		fields: []BoardField{
			{Name: "name", Kind: "text", Type: "string", Required: true, Placeholder: "AWS_REGION", Doc: "Name of the environment variable."},
			{Name: "default", Kind: "text", Type: "string", Doc: "Used when the variable is unset. Without it, an unset variable is an error."},
		}},
	{kind: "secret_env", def: "#SecretEnv", title: "Secret environment variable", words: "secret env environment variable password token credential", label: "secret_env", reads: []string{"value"},
		doc: "An environment variable that holds a secret. Ephemeral: never in state or a saved plan. Usable in provider configuration, write-only attributes and ephemeral resources.",
		fields: []BoardField{
			{Name: "name", Kind: "text", Type: "string", Required: true, Placeholder: "DB_PASSWORD", Doc: "Name of the environment variable."},
		}},
	{kind: "secret", def: "#Secret", title: "Sealed secret", words: "secret sealed password token credential vault", label: "secret", reads: []string{"value"},
		doc: "A secret kept in the sealed state, set with pravo secret set. Ephemeral, as a secret environment variable.",
		fields: []BoardField{
			{Name: "name", Kind: "text", Type: "string", Required: true, Placeholder: "prod/db_password", Doc: "Name of the secret."},
			{Name: "version", Kind: "number", Type: "number", Doc: "Version to read. Without it, the latest."},
		}},
	{kind: "secret_version", def: "#SecretVersion", title: "Secret version", words: "secret version", label: "secret_version", reads: []string{"version"},
		doc: "The version of a sealed secret that a run reads. Wire it to the version attribute beside a write-only attribute.",
		fields: []BoardField{
			{Name: "name", Kind: "text", Type: "string", Required: true, Placeholder: "prod/db_password", Doc: "Name of the secret."},
		}},
	{kind: "remote_outputs", def: "#RemoteOutputs", title: "Remote outputs", words: "remote outputs state root terraform_remote_state", label: "remote",
		doc: "The outputs another root has recorded, read on every plan as NAME.outputs.KEY.",
		fields: []BoardField{
			{Name: "state", Kind: "expr", Type: "struct", Required: true, Placeholder: `{path: "../network"}`, Doc: "Where the other root keeps its state: a directory relative to this root, or S3 settings."},
		}},
}

// boardBuiltinOf is the built-in of a kind, or nil.
func boardBuiltinOf(kind string) *boardBuiltin {
	for i := range boardBuiltins {
		if boardBuiltins[i].kind == kind {
			return &boardBuiltins[i]
		}
	}
	return nil
}

// form is the form of a built-in.
func (b *boardBuiltin) form() *BoardType {
	out := &BoardType{Version: APIVersion, Kind: b.kind, Type: b.kind, Title: b.title, Def: "pravo." + b.def, Doc: b.doc, Fields: append([]BoardField(nil), b.fields...),
		Reads: append([]string{}, b.reads...), Label: b.label}
	return out
}

// kindOf is the kind of a thing drawn: its Kind, else a template or a resource, as its fields say.
func (it BoardItem) kindOf() string {
	switch {
	case it.Kind != "":
		return it.Kind
	case it.Template != "":
		return "template"
	}
	return "resource"
}

// boardKindOfNode is the kind of the board of a node of the specification: its mode, or for a value pravo reads for
// itself, its type.
func boardKindOfNode(mode, typ string) string {
	if boardBuiltinOf(typ) != nil {
		return typ
	}
	if k, ok := schemaKindOfMode(mode); ok {
		return k.kind
	}
	if mode == "output" {
		return "output"
	}
	return ""
}
