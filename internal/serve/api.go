// Package serve is the read-only web viewer behind "pravo serve".
//
// This file is the contract between the Go server and the browser interface in
// web/. Every type here is sent as JSON. The server never sends a value that is
// marked sensitive, a private blob, a provider-configuration digest or a file
// that the evaluation did not load: see README.md.
package serve

import (
	"time"

	"github.com/pravo-systems/pravo/internal/engine"
)

// APIVersion is the layout version of every response in this file.
const APIVersion = 1

// Graph is GET /api/graph: everything the graph view and the namespace tree need,
// without attribute values.
type Graph struct {
	Version int `json:"version"`
	// Revision changes whenever anything in the response would. The page
	// compares it with the one in an event from /api/events.
	Revision   string      `json:"revision"`
	Project    Project     `json:"project"`
	Nodes      []Node      `json:"nodes"`
	Edges      []Edge      `json:"edges"`
	Namespaces []Namespace `json:"namespaces"`
	Outputs    []Output    `json:"outputs"`
	Plan       *PlanInfo   `json:"plan,omitempty"`
	// Warnings are sentences for the reader: a provider schema that is not
	// cached, a specification that no longer loads, a plan that is stale.
	Warnings []string `json:"warnings,omitempty"`
}

// Project describes what is being viewed.
type Project struct {
	Dir     string `json:"dir"`
	Package string `json:"package,omitempty"`
	// Version is the version of the pravo binary.
	Version   string         `json:"version"`
	State     StateInfo      `json:"state"`
	Providers []ProviderInfo `json:"providers,omitempty"`
	// SpecError is set when the specification does not load. The viewer then
	// shows the state alone: every node has Presence "state".
	SpecError string    `json:"spec_error,omitempty"`
	LoadedAt  time.Time `json:"loaded_at"`
}

// StateInfo describes the state that is read.
type StateInfo struct {
	Location string `json:"location"`
	// Backend is "local", "s3" or "http".
	Backend string `json:"backend"`
	Lineage string `json:"lineage,omitempty"`
	Format  int    `json:"format,omitempty"`
	Records int    `json:"records"`
	Locks   int    `json:"locks"`
	// Sealed is set when the state is sealed (format 3): see Access.
	Sealed bool `json:"sealed,omitempty"`
}

// ProviderInfo is a provider the specification declares or the state records.
type ProviderInfo struct {
	Local   string `json:"local"`
	Source  string `json:"source,omitempty"`
	Version string `json:"version,omitempty"`
	// Instances are the configured instances of the provider, sorted by key: "aws", or "aws.$west" and "aws.$east"
	// when a root configures the provider twice. Count is the number of nodes that use an instance.
	Instances []ProviderInstance `json:"instances,omitempty"`
}

// ProviderInstance is one configured instance of a provider.
type ProviderInstance struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// Node is one resource, data source, ephemeral resource, function call or built-in
// value, or one output of the root: the other end of the data flow, a value that
// the root publishes. An output is not a resource. It has no record, no history,
// no lock and no refresh time, and no resource count (Namespace.Resources, the
// "planned N of M" of a plan) includes it.
type Node struct {
	// Addr is the CUE path of the node, such as "services.api.secret". It is
	// opaque to the page: it is a key, never split. Use Namespace for the parent.
	Addr string `json:"addr"`
	// Mode is "managed", "data", "ephemeral", "call", "builtin" or "output". The
	// Type of an output is "output".
	Mode string `json:"mode"`
	Type string `json:"type"`
	// Name is the last label of the address.
	Name string `json:"name"`
	// Namespace is the Path of the namespace the node sits in, "" for the root.
	Namespace string `json:"namespace"`
	// Provider is the provider key: "aws" or "aws.west".
	Provider string `json:"provider,omitempty"`
	// Presence is "both" (declared and recorded), "spec" (declared, not yet
	// created) or "state" (recorded, no longer declared). An ephemeral resource,
	// a function call and a built-in value are never recorded: they are "both"
	// while the specification declares them, and are not pending a creation. For an
	// output the record is its value in the outputs of the state: "spec" is an
	// output that no apply has published yet, "state" one that the specification
	// no longer declares and the next apply would remove.
	Presence string `json:"presence"`
	// Action is the action of the loaded plan for the node: "create", "update",
	// "replace", "delete", "read", "move", "import" or "no-op". It is "" when no
	// plan is loaded or the plan did not run the node. It is always "" for an
	// output: a plan lists the value of every output and does not say which one
	// it changes.
	Action string `json:"action,omitempty"`
	// Drifted and Gone are the marks of the record: a read found that the real
	// object differs, or is absent.
	Drifted bool `json:"drifted,omitempty"`
	Gone    bool `json:"gone,omitempty"`
	// Locked is set when a lock or reservation holds the node.
	Locked bool `json:"locked,omitempty"`
	// Sealed is set when the record is sealed and this process cannot open it: the
	// address, type, dependencies and revision are shown, no value is.
	Sealed bool `json:"sealed,omitempty"`
	// Plans lists the stored plans (see StoredPlan) that can still be applied, all
	// of them or in part, and have a change left on the node, newest first. Overlap
	// is set when two or more can apply that change: applying one makes the other
	// stale. A change that a partly stale plan cannot apply is listed in Plans and
	// does not count for Overlap. Both are as of the revision of the graph.
	Plans   []string `json:"plans,omitempty"`
	Overlap bool     `json:"overlap,omitempty"`
	// Sensitive counts the attributes of the record whose values are withheld,
	// the ones that hold a value and not null. For an output it is 1 when the
	// value is withheld: the output is marked sensitive, or its value holds a
	// sensitive value of the state.
	Sensitive int `json:"sensitive,omitempty"`
	// NoSchema is set when the schema of the node's type is not cached, so that
	// every value of the record is withheld and Sensitive says nothing.
	NoSchema    bool       `json:"no_schema,omitempty"`
	Revision    int        `json:"revision,omitempty"`
	RefreshedAt *time.Time `json:"refreshed_at,omitempty"`
	// Source is where the node is written in the specification: the field that
	// holds it. It is nil for a node that is only in the state.
	Source *Ref `json:"source,omitempty"`
}

// Edge says that Dependent depends on Dependency: data flows from Dependency to
// Dependent.
type Edge struct {
	Dependency string `json:"dependency"`
	Dependent  string `json:"dependent"`
}

// Namespace is a struct of the specification that contains resources: an instance
// of a definition, a package instance, or a plain struct. The address of every
// node below it starts with its Path.
type Namespace struct {
	Path   string `json:"path"`
	Parent string `json:"parent"`
	Name   string `json:"name"`
	// Resources counts the nodes below the namespace, nested namespaces included, the
	// outputs not. Outputs counts the outputs below it.
	Resources int `json:"resources"`
	Outputs   int `json:"outputs,omitempty"`
	Depth     int `json:"depth"`
	// Declared is where the instance is written. Definition is where the
	// definition it instantiates is written, when that can be told.
	Declared   *Ref `json:"declared,omitempty"`
	Definition *Ref `json:"definition,omitempty"`
}

// Ref is a position in a file of the evaluation. File is the display path: a
// path relative to the module root, "cache/<module>@<version>/..." for a
// dependency module, or "generated/..." for a generated package.
type Ref struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"col,omitempty"`
	// EndLine is the last line of the declaration, when known.
	EndLine int `json:"end_line,omitempty"`
}

// Output is a declared or recorded output of the root. A sensitive output has
// no value. Every output is a Node too (Mode "output", Node.Addr = Name).
type Output struct {
	Name      string `json:"name"`
	Sensitive bool   `json:"sensitive,omitempty"`
	// Redacted is "" when the value is sent, else "sensitive" (the output is
	// marked so), "derived" (the value contains a sensitive value of the
	// state) or "sealed" (the state is sealed and this process cannot open the
	// value). Sensitive is set in all three cases.
	Redacted string `json:"redacted,omitempty"`
	Value    any    `json:"value,omitempty"`
	Unknown  bool   `json:"unknown,omitempty"`
}

// PlanInfo is the saved plan the viewer was started with (--plan), or a stored
// plan (PlanOverlay).
type PlanInfo struct {
	// File is the path of the plan file, or the id of a stored plan, which ID names
	// too. Status is the status of a stored plan, as StoredPlan.Status says it.
	File    string    `json:"file"`
	ID      string    `json:"id,omitempty"`
	Status  string    `json:"status,omitempty"`
	Created time.Time `json:"created"`
	Actor   string    `json:"actor,omitempty"`
	// Stale reports that the plan can no longer be applied; Reasons say why. A
	// partly stale plan is not stale: it can be applied in part, and the list of
	// stored plans (StoredPlan.Conflicts) says what cannot.
	Stale   bool     `json:"stale,omitempty"`
	Reasons []string `json:"reasons,omitempty"`
	Summary struct {
		Add         int `json:"add"`
		Change      int `json:"change"`
		Destroy     int `json:"destroy"`
		Read        int `json:"read"`
		BlastRadius int `json:"blast_radius"`
	} `json:"summary"`
	// Planned is the number of nodes the plan ran, Total the number it could
	// have run: "planned 3 of 13". Applied counts those of the planned nodes
	// that an apply from the plan file has made already.
	Planned int `json:"planned"`
	Total   int `json:"total"`
	// Policies are the changes the plan makes to the policies (see StoredPlan.Policies).
	Policies []PlanPolicy `json:"policies,omitempty"`
	Applied  int          `json:"applied,omitempty"`
	// Hints are the hints of the plan, such as a create and a delete that look
	// like one resource under a new address, worked out as the engine does.
	Hints []string `json:"hints,omitempty"`
	// Signatures says who stored a stored plan of a sealed state, as the readers file
	// judges the signatures. Nil for a plan file, and for a state that is not sealed.
	Signatures *PlanSignatures `json:"signatures,omitempty"`
	// Approvals says who approved a stored plan of a sealed state ("pravo plan approve"), and what
	// the policies in force ask of it. Nil for a plan file, a state that is not sealed, and a plan
	// that nobody approved and no policy asks approvals for.
	Approvals *PlanApprovals `json:"approvals,omitempty"`
}

// PlanApprovals are the approvals of a stored plan, as the readers file judges them, and what
// the policies in force ask of the plan before it is applied. Note is set when they could not
// be checked, and says why.
type PlanApprovals struct {
	Items []PlanApproval `json:"items"`
	// Needs is what the plan must have approved, each need on its own, as "pravo apply" counts
	// them. A plan that can no longer be applied, or that the policies in force refuse, has none.
	Needs []PlanApprovalNeed `json:"needs,omitempty"`
	Note  string             `json:"note,omitempty"`
}

// PlanApprovalNeed is one thing the policies ask: Min distinct keys of Role ("writer" or "admin")
// or above must have approved the plan, and not the key that stored it unless Self. By names the
// policy versions that ask. Have counts the approvals that meet it, and Met says whether enough
// do. An admin applies without them.
type PlanApprovalNeed struct {
	Min  int      `json:"min"`
	Role string   `json:"role"`
	Self bool     `json:"self,omitempty"`
	By   []string `json:"by"`
	Have int      `json:"have"`
	Met  bool     `json:"met"`
}

// PlanApproval is one approval. Status is "verified", "key_not_in_file" or "invalid"; Role is the
// role the readers file gives the key of an approval that verified.
type PlanApproval struct {
	Status  string    `json:"status"`
	Signer  string    `json:"signer,omitempty"`
	Claimed string    `json:"claimed,omitempty"`
	Role    string    `json:"role,omitempty"`
	At      time.Time `json:"at,omitempty"`
}

// PlanSignatures are the signatures of a stored plan. No Items and no Note is a plan with
// none. Note is set when they could not be checked, and says why.
type PlanSignatures struct {
	Items []PlanSignature `json:"items"`
	Note  string          `json:"note,omitempty"`
}

// PlanSignature is one signature of a stored plan. Status is "verified", "key_not_in_file"
// or "invalid". Signer is the name the readers file gives the key of a signature that
// verified, and Claimed the name an object gives itself.
type PlanSignature struct {
	Status  string    `json:"status"`
	Signer  string    `json:"signer,omitempty"`
	Claimed string    `json:"claimed,omitempty"`
	At      time.Time `json:"at,omitempty"`
}

// Detail is GET /api/detail?addr=...: one node with its attributes. Values
// that are sensitive are absent, with Redacted saying why. For an output the
// only attributes are "value" (Type "output": withheld when the output is
// sensitive or its value holds a sensitive value of the state, Unknown when no
// apply has recorded it) and "description" when the output has one; it has no
// Provider, History, Lock or Times, and its schema is always known.
type Detail struct {
	Version  int         `json:"version"`
	Node     Node        `json:"node"`
	Provider ProviderRef `json:"provider"`
	Attrs    []Attr      `json:"attrs"`
	// SchemaKnown is false when the provider schema could not be read. Every
	// value is then withheld, since nothing says which are sensitive.
	SchemaKnown bool `json:"schema_known"`
	// Schema lists the attributes of the type of a node that has no record to
	// take them from, with no values: an ephemeral resource, a built-in
	// environment variable, a node that is declared and not created yet. A node
	// with a record has none: its Attrs follow the schema already.
	Schema    []Attr   `json:"schema,omitempty"`
	DependsOn []string `json:"depends_on"`
	UsedBy    []string `json:"used_by"`
	// Reach lists everything a change to the node can reach: the transitive
	// dependents recorded in the state.
	Reach   []string       `json:"reach,omitempty"`
	Change  *Change        `json:"change,omitempty"`
	History []HistoryEntry `json:"history,omitempty"`
	// SecretVersions are the versions of a secret, newest first: the History of a secret node, which no
	// transaction writes as a resource.
	SecretVersions []SecretVersion `json:"secret_versions,omitempty"`
	Lock           *LockInfo       `json:"lock,omitempty"`
	Times          Times           `json:"times"`
	Notes          []string        `json:"notes,omitempty"`
}

// ProviderRef is the provider instance of a node.
type ProviderRef struct {
	Key      string `json:"key"`
	Source   string `json:"source,omitempty"`
	Version  string `json:"version,omitempty"`
	Protocol string `json:"protocol,omitempty"`
}

// Attr is one attribute of the record, in schema order. A struct, list, set or
// map has Children (named "0", "1", ... for a list) and no Value; a scalar has
// Value.
type Attr struct {
	Name string `json:"name"`
	// Type is the schema type as text: "string", "number", "bool",
	// "list(string)", "object", "set(object)", ...
	Type string `json:"type,omitempty"`
	// Kind is "required", "optional" or "computed" (computed and optional is
	// "optional,computed").
	Kind       string `json:"kind,omitempty"`
	Sensitive  bool   `json:"sensitive,omitempty"`
	Deprecated bool   `json:"deprecated,omitempty"`
	// Redacted is "" when the value is sent, else why it is not: "sensitive"
	// (the schema marks it), "name" (the name says secret, password, token and
	// the like), "schema" (no schema, or the schema does not know the
	// attribute), "write-only" or "derived" (the text contains a value that the
	// state holds in a sensitive attribute: a file whose content embeds a
	// password). A value that is null is never withheld: null says nothing.
	Redacted    string `json:"redacted,omitempty"`
	Unknown     bool   `json:"unknown,omitempty"`
	Null        bool   `json:"null,omitempty"`
	Value       any    `json:"value,omitempty"`
	Description string `json:"description,omitempty"`
	Children    []Attr `json:"children,omitempty"`
	// Truncated counts elements left out of Children of a long list or map, or,
	// for a string, the bytes left out of Value.
	Truncated int `json:"truncated,omitempty"`
}

// Change is the loaded plan's decision for a node.
type Change struct {
	Action string `json:"action"`
	// Applied is set when an apply from the plan file has made the change.
	Applied         bool         `json:"applied,omitempty"`
	Reasons         []string     `json:"reasons,omitempty"`
	RequiresReplace []string     `json:"requires_replace,omitempty"`
	Attrs           []AttrChange `json:"attrs,omitempty"`
}

// AttrChange is one attribute that a plan changes. Before and After are absent
// where the value is withheld (BeforeRedacted, AfterRedacted) or not known
// until apply (AfterUnknown).
type AttrChange struct {
	Path           string `json:"path"`
	Before         any    `json:"before,omitempty"`
	After          any    `json:"after,omitempty"`
	BeforeRedacted bool   `json:"before_redacted,omitempty"`
	AfterRedacted  bool   `json:"after_redacted,omitempty"`
	AfterUnknown   bool   `json:"after_unknown,omitempty"`
}

// SecretVersion is one version of a secret. Nothing in it is secret.
type SecretVersion struct {
	Version int       `json:"version"`
	Kind    string    `json:"kind"` // "value", "removed" or "purged"
	At      time.Time `json:"at"`
	By      string    `json:"by,omitempty"`
	Trust   string    `json:"trust,omitempty"`
	Problem string    `json:"problem,omitempty"`
}

// HistoryEntry is a transaction that wrote the node.
type HistoryEntry struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Status    string    `json:"status"`
	Time      time.Time `json:"time"`
	User      string    `json:"user,omitempty"`
	Actor     string    `json:"actor,omitempty"`
	Command   string    `json:"command,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Action    string    `json:"action,omitempty"`
	RevBefore int       `json:"rev_before,omitempty"`
	RevAfter  int       `json:"rev_after,omitempty"`
	// Plan is the stored plan an apply ran from, and GitRevision the revision of
	// the specification it ran against, as the transaction recorded them.
	Plan        string `json:"plan,omitempty"`
	GitRevision string `json:"git_revision,omitempty"`
	// Attrs are the attributes the change altered, as the transaction recorded
	// them, redacted as a plan's are. AttrsMore counts those left out for room.
	// AttrsOmitted says the transaction kept none because the change altered too
	// much, and AttrsSealed that a sealed state keeps them for readers and this
	// viewer holds no key. A transaction recorded before diffs were kept says
	// none of this.
	Attrs        []AttrChange `json:"attrs,omitempty"`
	AttrsMore    int          `json:"attrs_more,omitempty"`
	AttrsOmitted bool         `json:"attrs_omitted,omitempty"`
	AttrsSealed  bool         `json:"attrs_sealed,omitempty"`
	// Signature is what the signatures of the transaction come to: "verified",
	// "key_not_in_file", "invalid" or "unsigned", absent when they are not checked.
	// Signer is the name the readers file gives the key of a signature that verified.
	Signature string `json:"signature,omitempty"`
	Signer    string `json:"signer,omitempty"`
}

// LockInfo is the lock or reservation that holds a node.
type LockInfo struct {
	Kind    string     `json:"kind"`
	User    string     `json:"user,omitempty"`
	Host    string     `json:"host,omitempty"`
	Command string     `json:"command,omitempty"`
	Actor   string     `json:"actor,omitempty"`
	Since   time.Time  `json:"since"`
	Expires *time.Time `json:"expires,omitempty"`
}

// Times are the times recorded on a node.
type Times struct {
	AppliedAt   *time.Time `json:"applied_at,omitempty"`
	RefreshedAt *time.Time `json:"refreshed_at,omitempty"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
	DriftedAt   *time.Time `json:"drifted_at,omitempty"`
}

// Source is GET /api/source?addr=...: where a node comes from.
type Source struct {
	Version int    `json:"version"`
	Addr    string `json:"addr"`
	// Blocks are the places that write the node, outermost last: the definition
	// of the resource, then each instance that fills it in.
	Blocks []SourceBlock `json:"blocks"`
	// Contributions lists everything that shapes the node, files and not: see
	// Contribution.
	Contributions []Contribution `json:"contributions"`
	// Generated counts the nodes written by the same lines (a comprehension or
	// a definition instantiated several times), this node included.
	Generated int      `json:"generated,omitempty"`
	Notes     []string `json:"notes,omitempty"`
}

// SourceBlock is a run of lines of one file.
type SourceBlock struct {
	// Role is "definition" (where the resource struct is written) or "instance"
	// (where a definition it comes from is instantiated).
	Role  string `json:"role"`
	Label string `json:"label,omitempty"`
	File  string `json:"file"`
	// Kind is "file", "cache" (a dependency module), "generated" or "builtin".
	Kind      string `json:"kind"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	// Modified is the modification time of the file, so the page can say that
	// it changed after the state was written.
	Modified *time.Time   `json:"modified,omitempty"`
	Lines    []SourceLine `json:"lines"`
}

// SourceLine is one line. Mark is "", "focus" (the lines of the node itself),
// "changed" (an attribute the loaded plan changes) or "sensitive" (a literal
// assigned to a sensitive attribute, masked in Text).
type SourceLine struct {
	No   int    `json:"no"`
	Text string `json:"text"`
	Mark string `json:"mark,omitempty"`
	// Dim marks context lines around the block.
	Dim bool `json:"dim,omitempty"`
}

// Contribution is one thing that shapes a node.
type Contribution struct {
	// Kind is "file", "cache", "tag" (a -t value), "generated" (a provider
	// package), "builtin" (a pravo package) or "policy".
	Kind  string `json:"kind"`
	Where string `json:"where"`
	Note  string `json:"note"`
}

// Search is GET /api/search?q=...: the nodes and namespaces that match, for the
// palette. The page also searches the graph it holds; this exists for a graph
// that was cut down.
type Search struct {
	Version    int         `json:"version"`
	Nodes      []Node      `json:"nodes"`
	Namespaces []Namespace `json:"namespaces"`
}

// Fields is GET /api/fields?q=path=value: the nodes whose recorded attributes
// hold the value at the path. A path is dotted ("tags.env", "rule.0.cidr"), a
// path with no dot also names a field of that name anywhere ("region"), and *
// stands for any run of characters in a path or a value. A path with no value
// ("region") matches the nodes that hold the field. Matching ignores case and a
// value is matched whole. Only what the record view sends can be searched: a
// withheld value never matches, and the nodes whose values are withheld whole
// (a sealed record, a type whose schema is not cached) are counted in Withheld.
type Fields struct {
	Version int      `json:"version"`
	Addrs   []string `json:"addrs"`
	// Indexed is the number of nodes whose values were searched, Withheld the
	// number that could not be.
	Indexed  int `json:"indexed"`
	Withheld int `json:"withheld"`
}

// PlanCounts are the actions of a plan, as the plan prints them: a replace
// counts as an add and a destroy.
type PlanCounts struct {
	Add     int `json:"add"`
	Change  int `json:"change"`
	Destroy int `json:"destroy"`
	Read    int `json:"read"`
}

// PlanPolicy is a change that a plan makes to a policy: the version it writes. It is a
// change of the plan like a resource's, and the page counts it with them; it is not a
// node of the graph, and Name is the name that the Policies section lists.
type PlanPolicy struct {
	Name string `json:"name"`
	// Change is "new", "changed" or "removed".
	Change string `json:"change"`
	// Base is the number of the version in force when the plan was made, 0 for none.
	Base int `json:"base"`
}

// PlanChange is a resource that a stored plan changes.
type PlanChange struct {
	Addr string `json:"addr"`
	// Action is "create", "update", "replace", "delete", "read", "move" or
	// "import".
	Action string `json:"action"`
	// Applied is set when the change was applied already from the plan: the plan
	// has nothing left to do on the resource, so it does not count for an overlap.
	Applied bool `json:"applied,omitempty"`
	// Conflict is set, for a partly stale plan, on a change that is left and that
	// cannot be applied now: StoredPlan.Conflicts says why. Such a change does not
	// count for an overlap either.
	Conflict bool `json:"conflict,omitempty"`
}

// PlanConflict is a change of a partly stale plan that cannot be applied now: the
// state moved on under the record that it expects, or it has to be applied with a
// change that cannot be. Reasons are masked against the sensitive values of the
// state and clipped to 300 characters, as the reasons of a stale plan are.
type PlanConflict struct {
	Addr    string   `json:"addr"`
	Reasons []string `json:"reasons"`
}

// PlanOverlap is another open stored plan that has a change left to apply on a
// resource that a plan has a change left on. The relation is symmetric. Count is
// the number of such resources, Addrs the first of them (at most 20), Truncated
// how many are left out.
type PlanOverlap struct {
	ID        string   `json:"id"`
	Count     int      `json:"count"`
	Addrs     []string `json:"addrs"`
	Truncated int      `json:"truncated,omitempty"`
}

// StoredPlan is a plan that was stored in the state with "pravo plan --store",
// where everyone who can read the state finds it.
type StoredPlan struct {
	ID string `json:"id"`
	// Status is "pending" (it can be applied), "partial" (some of its changes were
	// applied and the rest can be), "partly-stale" (the state moved on under some
	// of the changes that are left and the rest can be applied by name:
	// Conflicts say which and why, ApplyOnly and ApplyLive the part that can be),
	// "stale" (the state moved on, Reasons say how), "applied" (every change was
	// applied) or "expired" (it was not applied before it expired). Open is set for
	// pending, partial and partly-stale: those can be applied, all of it or in part,
	// and two of them that can apply a change on one resource overlap.
	// Author is clipped to 4 KiB and masked against the sensitive values of the
	// state. The text that the author wrote with the plan (the reason, the command
	// line, the git revision) is not in a listing: it can quote a value that the
	// plan writes and the state does not hold yet, which only the plan says, so
	// PlanOverlay carries it, masked against the plan's own values.
	Status  string     `json:"status"`
	Open    bool       `json:"open,omitempty"`
	Author  string     `json:"author"`
	Created time.Time  `json:"created"`
	Expires time.Time  `json:"expires"`
	Destroy bool       `json:"destroy,omitempty"`
	Summary PlanCounts `json:"summary"`
	// Planned is the number of changes, Applied how many of them were applied
	// already. Applying is set while an apply from the plan runs.
	Planned  int  `json:"planned"`
	Applied  int  `json:"applied,omitempty"`
	Applying bool `json:"applying,omitempty"`
	// Policies are the changes the plan makes to the policies, which a plan can make
	// without changing a resource.
	Policies []PlanPolicy `json:"policies,omitempty"`
	// Reasons say why a stale plan can no longer be applied: the first 20, each
	// clipped to 300 characters; ReasonsOmitted counts the others. A partly stale
	// plan has none: its reasons are those of Conflicts.
	Reasons        []string `json:"reasons,omitempty"`
	ReasonsOmitted int      `json:"reasons_omitted,omitempty"`
	// Conflicts, for a partly stale plan, lists the changes that cannot be applied
	// now, by address: the first 20, with the reasons; ConflictsOmitted counts the
	// others. Unapplied is how many changes the plan has left, those included.
	Conflicts        []PlanConflict `json:"conflicts,omitempty"`
	ConflictsOmitted int            `json:"conflicts_omitted,omitempty"`
	Unapplied        int            `json:"unapplied,omitempty"`
	// ApplyOnly, for a partly stale plan, is the part that can be applied, sorted:
	// the changes that are left, that do not conflict and that need nothing that
	// does, with the changes they need, up to 2000 (ApplyOnlyOmitted counts the
	// others). It is a closed selection for "pravo apply --only". ApplyLive is the
	// command that applies it, "pravo apply ID --only a,b,c" as it is typed; it is
	// absent when the part has no change or the command would be longer than 16 KiB.
	ApplyOnly        []string `json:"apply_only,omitempty"`
	ApplyOnlyOmitted int      `json:"apply_only_omitted,omitempty"`
	ApplyLive        string   `json:"apply_live,omitempty"`
	// Touches lists the resources the plan changes, by address, up to 2000;
	// Truncated counts those left out. Overlaps lists the other open plans that
	// change one of them.
	Touches   []PlanChange  `json:"touches"`
	Truncated int           `json:"truncated,omitempty"`
	Overlaps  []PlanOverlap `json:"overlaps,omitempty"`
	// Apply is the command that applies the plan, all that is left of it. A partly
	// stale plan is refused by it: ApplyLive is the command for that plan.
	Apply string `json:"apply"`
}

// PlanList is GET /api/plans: the plans stored in the state, newest first.
type PlanList struct {
	Version int `json:"version"`
	// Revision is the revision of the graph that the list goes with.
	Revision string       `json:"revision"`
	Plans    []StoredPlan `json:"plans"`
}

// PlanNode is what a plan does to a resource.
type PlanNode struct {
	Addr   string `json:"addr"`
	Action string `json:"action"`
	// Applied is set when the change was applied already.
	Applied bool `json:"applied,omitempty"`
}

// PlanOverlay is GET /api/plan?id=...: what a stored plan lays over the graph, as
// the plan file of --plan does. Plan is the PlanInfo that Graph.Plan has in that
// case, Nodes the action of each node the plan ran (Node.Action; a node that is
// not listed was not read). GET /api/detail and GET /api/source take the same id
// as plan=... and answer for that plan: the Change of the node, the lines marked.
// plan=none asks for no plan at all.
type PlanOverlay struct {
	Version  int        `json:"version"`
	Revision string     `json:"revision"`
	Plan     PlanInfo   `json:"plan"`
	Nodes    []PlanNode `json:"nodes"`
	Warnings []string   `json:"warnings,omitempty"`
	// Reason, Command and GitRevision are what the author wrote with the plan,
	// each clipped to 4 KiB and masked against the sensitive values of the state
	// and the ones the plan would write. Command has the values of -t and of
	// flags that look secret withheld.
	Reason      string `json:"reason,omitempty"`
	Command     string `json:"command,omitempty"`
	GitRevision string `json:"git_revision,omitempty"`
}

// Access is GET /api/access: how the state is protected, who can read it, what
// the readers file says, and commands to copy that change it. The page shows it
// and runs nothing. A reader is a name and public keys; the response holds no
// secret, no key of the identity and no line of the readers file as written.
type Access struct {
	Version int `json:"version"`
	// Revision is the revision of the graph that the document goes with.
	Revision string `json:"revision"`
	// State is "off" (format 1), "immutable" (format 2: records are stored once and
	// never overwritten), "sealed" (format 3) or "migrating" (a seal started and
	// has not finished). Whether this process can open the values is You, not State.
	State    string `json:"state"`
	Format   int    `json:"format"`
	Location string `json:"location"`
	// Backend is "local", "s3" or "http".
	Backend string `json:"backend"`
	// Summary is the sentence for the state.
	Summary string `json:"summary"`
	// Readers lists the readers of the store and of the readers file, the file's
	// first. A name that does not match the rule for names is listed with NameOK
	// false and appears in no suggestion.
	Readers []AccessReader `json:"readers"`
	File    AccessFile     `json:"file"`
	// You is absent unless the state is sealed or migrating.
	You *AccessYou `json:"you,omitempty"`
	// Records counts the records. Its Text is the one sentence about them that
	// the banner shows, absent when the state holds none.
	Records AccessRecords `json:"records"`
	// Groups lists the groups of data keys. It is absent unless the state is
	// sealed and has more than one, which a removal of a reader makes; GroupsText
	// is then the sentence that says why and what sealing the records again does.
	Groups     []AccessGroup    `json:"groups,omitempty"`
	GroupsText string           `json:"groups_text,omitempty"`
	Migration  *AccessMigration `json:"migration,omitempty"`
	// SingleReader is set when the state is sealed and exactly one reader holds a
	// lock in the current group: nobody can read the state if that reader loses
	// their key, since there is no recovery recipient. The page shows it under the
	// readers.
	SingleReader *AccessWarning `json:"single_reader,omitempty"`
	// Recovery is the recovery recipient of the state: any K of N shares rebuild
	// its key. It is absent when the state has none.
	Recovery *AccessRecovery `json:"recovery,omitempty"`
	// Roles are the roles of the keys. It is absent unless the state is sealed.
	Roles *AccessRoles `json:"roles,omitempty"`
	// Issues are what needs attention, worst first. AllClear is the sentence for a sealed state
	// that has none, empty when there are some.
	Issues   []AccessIssue `json:"issues"`
	AllClear string        `json:"all_clear,omitempty"`
	// LastChange is the newest entry of the history of the seal.
	LastChange  *AccessChange `json:"last_change,omitempty"`
	Suggestions []Suggestion  `json:"suggestions"`
	// Problems are sentences about what could not be read.
	Problems []string `json:"problems,omitempty"`
}

// AccessReader is a reader that the store, the readers file or both list. Readers
// are matched by reader key, as the locks of the store are.
type AccessReader struct {
	// Name is clipped and has control and direction characters removed.
	Name   string `json:"name"`
	NameOK bool   `json:"name_ok"`
	// Key is the public reader key; KeyShort is the form shown.
	Key      string `json:"key"`
	KeyShort string `json:"key_short"`
	// Fingerprint is the SHA256 fingerprint of the signing key, "" where the reader
	// is known from the store alone.
	Fingerprint string `json:"fingerprint,omitempty"`
	InStore     bool   `json:"in_store"`
	InFile      bool   `json:"in_file"`
	// You marks the reader whose key is the identity of this process.
	You bool `json:"you,omitempty"`
	// Line is the line of the readers file, 0 for a reader that is not in it.
	Line int `json:"line,omitempty"`
	// Role is what the key may do, "admin", "writer" or "reader", when the roles are on.
	Role string `json:"role,omitempty"`
	// Status is "active" (in the list and in the state), "to-add" (in the list, not let in yet) or
	// "to-remove" (off the list, still holds a key). It is absent when the state is not sealed.
	// StatusText is the words for it.
	Status     string `json:"status,omitempty"`
	StatusText string `json:"status_text,omitempty"`
}

// AccessRoles are the roles of a sealed state. State is "off", "file" (the readers file decides),
// "pinned" (the roles in the state decide) or "untrusted" (the chain in the state does not
// verify). Text is the sentence for it. What is long, rare or complex about them, the versions
// and the vouched transactions, is left to pravo protect status.
type AccessRoles struct {
	State string `json:"state"`
	Text  string `json:"text"`
	// Differs is set when the readers file asks for other roles than the state holds.
	Differs bool `json:"differs,omitempty"`
	// You is the role of this process, absent when it has none known.
	You string `json:"you,omitempty"`
}

// AccessIssue is something that needs a person's attention: the page lists these and nothing else
// that is about how well the state is kept. Level is "bad" (the state cannot be trusted or changed
// until it is dealt with) or "chg" (it works, and should be mended). Suggestion is the ID of the
// card that says what to do, absent when the answer is in Text.
type AccessIssue struct {
	Level      string `json:"level"`
	Title      string `json:"title"`
	Text       string `json:"text"`
	Suggestion string `json:"suggestion,omitempty"`
	// Items are the records of an issue about records, up to 20, and More the number left out.
	Items []AccessIssueItem `json:"items,omitempty"`
	More  int               `json:"more,omitempty"`
}

// AccessIssueItem is a record and why it is in doubt.
type AccessIssueItem struct {
	Addr string `json:"addr"`
	Why  string `json:"why"`
}

// AccessFile is what the readers file says in relation to the store.
type AccessFile struct {
	// Verdict is "match" (file and store list the same readers), "differs",
	// "missing" (no file), "invalid" (the file does not load),
	// "expected-sealed" (the file loads and the state is not sealed: it has not been
	// sealed yet, or its meta.json was changed) or "none" (the state is not sealed and the file adds
	// nothing, so nothing is compared).
	Verdict string `json:"verdict"`
	// Path is where the file is looked for.
	Path string `json:"path"`
	Text string `json:"text"`
	// Count is the number of readers of the file when it loads.
	Count int `json:"count"`
	// Added are the readers in the file and not in the store, Removed the ones in
	// the store and not in the file: rewrap makes the store match the file.
	Added   []string          `json:"added,omitempty"`
	Removed []string          `json:"removed,omitempty"`
	Errors  []AccessLineError `json:"errors,omitempty"`
}

// AccessLineError is a line of the readers file that does not load. It carries the
// number of the line and a reason, and never the text of the line.
type AccessLineError struct {
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

// AccessYou is the identity of this process.
type AccessYou struct {
	// State is "reader", "not-reader", "no-identity" or "locked".
	State string `json:"state"`
	Name  string `json:"name,omitempty"`
	// Text is the sentence for the state of the identity.
	Text string `json:"text"`
}

// AccessRecords counts the records of the state. Sealed records are encrypted;
// Plain ones are in a sealed state and are not (written before the seal and not
// reached since); Unreadable ones are sealed under a group that this process holds
// no key for, or all of them when it holds none. Text is the sentence about them
// that the page shows, worded by the server: that all are sealed, how many are not
// sealed yet, how many this key cannot open, or how many there are in a state that
// is not sealed.
type AccessRecords struct {
	Total      int    `json:"total"`
	Sealed     int    `json:"sealed"`
	Plain      int    `json:"plain"`
	Unreadable int    `json:"unreadable"`
	Text       string `json:"text,omitempty"`
}

// AccessGroup is a group of data keys: records sealed under it, and the readers
// that hold a lock for it.
type AccessGroup struct {
	ID      string `json:"id"`
	Current bool   `json:"current,omitempty"`
	Locks   int    `json:"locks"`
	Records int    `json:"records"`
}

// AccessMigration is a seal that started and has not finished.
type AccessMigration struct {
	Started time.Time `json:"started"`
	By      string    `json:"by"`
	Done    int       `json:"done"`
	Total   int       `json:"total"`
	// Unseal is set when the seal is being taken off.
	Unseal bool `json:"unseal,omitempty"`
}

// AccessRecovery says how the recovery of a sealed state stands. Text is the
// sentence the page shows. Warning is set when the recovery does not hold: a group
// has no lock for its key, or the recovery file of the repository says another
// thing.
type AccessRecovery struct {
	K        int    `json:"k"`
	N        int    `json:"n"`
	Key      string `json:"key"`
	KeyShort string `json:"key_short"`
	Text     string `json:"text"`
	Warning  string `json:"warning,omitempty"`
}

// AccessWarning is a risk of the state, in a sentence, with the suggestion that
// deals with it. Action is the label of the button that opens that suggestion,
// and Suggestion its ID; the page offers the button only if the suggestion is in
// the list.
type AccessWarning struct {
	Text       string `json:"text"`
	Action     string `json:"action"`
	Suggestion string `json:"suggestion"`
}

// AccessChange is the last change of the seal. By is a label that the machine of
// the change gave itself, not a proof. Text is the item the banner shows: the time
// in UTC and what was done, such as "2026-10-23 08:03 UTC · sealed by marcinw".
type AccessChange struct {
	At      time.Time `json:"at"`
	By      string    `json:"by"`
	What    string    `json:"what"`
	Added   []string  `json:"added,omitempty"`
	Removed []string  `json:"removed,omitempty"`
	Text    string    `json:"text"`
}

// Suggestion is a change that the page offers as text to copy. The server runs
// none of it. Steps are in the order they are done.
type Suggestion struct {
	// ID is "seal", "check-downgrade", "finish", "identity", "rewrap", "add-reader",
	// "remove-reader:NAME", "reseal", "prune" or "status".
	ID    string `json:"id"`
	Title string `json:"title"`
	Why   string `json:"why"`
	// Who says whose machine the steps are for, such as "a current member".
	Who string `json:"who,omitempty"`
	// Kind is set on the steps that are about one member of the state, which the page shows with that
	// member and not in the list: "member-add", "member-remove" or "member-role". Member is the name
	// of the member the last two are for.
	Kind   string `json:"kind,omitempty"`
	Member string `json:"member,omitempty"`
	// Primary marks the next step for the state shown.
	Primary bool             `json:"primary,omitempty"`
	Steps   []SuggestionStep `json:"steps"`
}

// SuggestionStep is one step of a Suggestion. Kind is "command" (one line, no
// newline, for a shell), "add-line" or "remove-line" (a line of Text to add to or
// remove from File, which the person edits by hand) or "note" (a sentence). A
// command that has parts to fill in carries them as unquoted angle brackets, so
// that a paste that was not filled in fails in the shell, and Placeholder is set.
type SuggestionStep struct {
	Kind        string `json:"kind"`
	Text        string `json:"text"`
	File        string `json:"file,omitempty"`
	Placeholder bool   `json:"placeholder,omitempty"`
}

// Event is the data of a server-sent event on /api/events. The event name is
// "change" and the page fetches /api/graph again, and the open node, when the
// Revision differs from the one it holds.
type Event struct {
	Revision string `json:"revision"`
	// What is any of "state", "spec", "plan", "locks", "plans", "access",
	// "discovery", "traces". "traces" is the directory of the recorded runs. "plan" is the plan file of --plan; "plans" are the plans stored
	// in the state: one appeared, was collected, was applied from, or passed its
	// expiry; "access" is the readers file; "discovery" is what the discoveries
	// found, which "pravo discover" writes.
	What []string `json:"what"`
}

// Discovery is GET /api/discovery: the discoveries of the specification, what
// their last runs found, and what each found object is to the state. Objects that
// a record of the state already is are counted and not listed. The page shows the
// rest, in a section of its own: nothing of it is in the graph.
type Discovery struct {
	Version  int    `json:"version"`
	Revision string `json:"revision"`
	// SpecOK is false when the specification does not load. A run cannot be
	// compared with a scope then: every Freshness is "unknown".
	SpecOK bool `json:"spec_ok"`
	// Unmanaged counts the objects that ask to be adopted, in every discovery.
	Unmanaged   int             `json:"unmanaged"`
	Discoveries []DiscoveryInfo `json:"discoveries"`
	Warnings    []string        `json:"warnings,omitempty"`
	// Command runs the discoveries that have not run, or whose scope changed. The
	// page copies it and runs nothing.
	Command string `json:"command"`
}

// DiscoveryInfo is one discovery.
type DiscoveryInfo struct {
	Name string `json:"name"`
	// Type is the list resource type, Provider the source of its provider and
	// Instance the provider instance it lists through.
	Type     string `json:"type"`
	Provider string `json:"provider"`
	Instance string `json:"instance,omitempty"`
	// Declared is false for a discovery that only the state holds a run of.
	Declared bool `json:"declared"`
	// Freshness is "current" (the last run listed in the scope that is written
	// now), "stale" (the scope, the limit or the provider changed since: the run
	// answers the old question), "never", "broken" (the scope cannot be listed
	// with: see Problem), "undeclared" or "unknown" (the specification does not
	// load).
	Freshness string `json:"freshness"`
	Problem   string `json:"problem,omitempty"`
	// Scope is the scope that is written now, or the scope of the last run for a
	// discovery that is not declared.
	Scope  []Field `json:"scope"`
	Limit  int64   `json:"limit,omitempty"`
	Source *Ref    `json:"source,omitempty"`
	// Run is the last run, nil for a discovery that has not run.
	Run    *RunInfo        `json:"run,omitempty"`
	Counts DiscoveryCounts `json:"counts"`
	// Unreadable counts the records of the type that could not be opened, so that
	// objects they are may be listed as unmanaged.
	Unreadable int `json:"unreadable,omitempty"`
	// Items are the objects of the last run that are not managed: unmanaged first,
	// then adopting and acknowledged, each by name. At most 2000 are listed;
	// ItemsOmitted counts the rest.
	Items        []DiscoveredInfo `json:"items"`
	ItemsOmitted int              `json:"items_omitted,omitempty"`
	// Acks are the entries of $ack of the discovery.
	Acks []AckInfo `json:"acks,omitempty"`
	// Command runs this discovery. AckAll is the CUE of an $ack entry for the objects
	// that are unmanaged now, with a reason for the person to give; it is empty when
	// none is unmanaged or there are too many to name one by one.
	Command string `json:"command"`
	AckAll  string `json:"ack_all,omitempty"`
}

// RunInfo is a run of a discovery.
type RunInfo struct {
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	Actor    string    `json:"actor,omitempty"`
	// Status is "complete", or "truncated" when the run stopped at its limit.
	Status string `json:"status"`
	// Total is the number of objects the run found, managed ones included.
	Total    int      `json:"total"`
	Warnings []string `json:"warnings,omitempty"`
}

// DiscoveryCounts counts the objects of the last run by what they are.
type DiscoveryCounts struct {
	Unmanaged    int `json:"unmanaged"`
	Adopting     int `json:"adopting"`
	Acknowledged int `json:"acknowledged"`
	Managed      int `json:"managed"`
}

// DiscoveredInfo is an object that a discovery found and that no record is.
type DiscoveredInfo struct {
	// Key is the whole identity as one line and names the object; ID is what a
	// person calls it.
	Key  string `json:"key"`
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Status is "unmanaged" (it asks to be adopted), "adopting" (a resource names it
	// in $import) or "acknowledged" (an acknowledgement covers it).
	Status string `json:"status"`
	// By is the address of the resource that adopts the object, or the ref of the
	// $ack entry that covers it; Reason is the reason of the acknowledgement.
	By       string  `json:"by,omitempty"`
	Reason   string  `json:"reason,omitempty"`
	Identity []Field `json:"identity,omitempty"`
}

// AckInfo is an entry of the $ack list of a discovery, as the specification has it.
type AckInfo struct {
	// Ref names the entry: "$ack[0]".
	Ref   string   `json:"ref"`
	IDs   []string `json:"ids,omitempty"`
	Names []string `json:"names,omitempty"`
	// Reason is what the entry says of why the objects are left alone.
	Reason string `json:"reason,omitempty"`
	// Covers is how many objects of the last run it covers, -1 when there is none.
	Covers int `json:"covers"`
}

// Field is a name and a value as text.
type Field struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Adopt is GET /api/adopt?discovery=NAME&key=KEY: what a person does to bring one
// discovered object under management. The server builds it from the last run and
// the specification and asks no provider.
type Adopt struct {
	Discovery string `json:"discovery"`
	Key       string `json:"key"`
	ID        string `json:"id"`
	Status    string `json:"status"`
	// Snippet is CUE: a resource that names the object in $import.
	Snippet string `json:"snippet"`
	// Ack is the CUE of an $ack entry that says the object is known and is meant to
	// stay as it is, with a reason for the person to give. Only for an unmanaged one.
	Ack string `json:"ack,omitempty"`
	// Steps follow the snippet: commands to copy, and notes. A step with
	// Placeholder has a part, in angle brackets, for the person to fill in.
	Steps []SuggestionStep `json:"steps"`
}

// TraceList is GET /api/traces: the runs that pravo recorded in the project, newest
// first. A run is recorded when a command did some work (a plan, an apply, a drift
// check) and the record was turned on with PRAVO_TRACES=on; the file is
// .pravo/traces/<id>.json of the directory the command ran in (or $PRAVO_TRACE_DIR).
// The viewer reads the files and writes none.
type TraceList struct {
	Version int `json:"version"`
	// Dir is the directory the runs are read from, as the page shows it.
	Dir    string      `json:"dir"`
	Traces []TraceInfo `json:"traces"`
	// Omitted counts the runs left out of the list because it is bounded.
	Omitted int `json:"omitted,omitempty"`
	// Unreadable counts the files that are not runs of this format.
	Unreadable int `json:"unreadable,omitempty"`
}

// TraceInfo is one run in the list.
type TraceInfo struct {
	// ID names the run: <trace id>-<span id>, 32 and 16 hexadecimal digits.
	ID string `json:"id"`
	// Command is the command that ran, such as "pravo apply".
	Command string `json:"command"`
	// Line is the whole command line, "pravo plan -t env=prod --reason ...", as it was typed, with the values of the tags
	// and of the flags that carry secrets masked, as in a stored plan. It is absent for a run recorded without one.
	Line string `json:"line,omitempty"`
	// Start is when the run began, RFC 3339.
	Start      string `json:"start"`
	DurationUS int64  `json:"duration_us"`
	// Exit is the exit code of the command, -1 when the run does not say.
	Exit int `json:"exit"`
	// Status is "ok" or "error": error when a span failed.
	Status string `json:"status"`
	// SpanCount, Errors and Resources count the spans, the failed spans and the
	// distinct resources that the run planned or applied.
	SpanCount int `json:"span_count"`
	Errors    int `json:"errors"`
	Resources int `json:"resources"`
	// Txn is the transaction the run wrote, if it applied.
	Txn    string `json:"txn,omitempty"`
	Actor  string `json:"actor,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Trace is GET /api/trace?id=ID: the spans of one run.
type Trace struct {
	Version int `json:"version"`
	TraceInfo
	TraceID string `json:"trace_id"`
	// Dropped counts the provider calls and state requests left out of a very large run when it was recorded.
	Dropped int `json:"dropped,omitempty"`
	// Omitted counts the spans left out of this answer because it is bounded.
	Omitted int         `json:"omitted,omitempty"`
	Spans   []TraceSpan `json:"spans"`
}

// TraceSpan is one span. Times are microseconds from the start of the run. Kind is
// "command", "engine", "phase", "setup", "resource", "provider", "state", "mcp" or "other". A span's
// parent is in the run unless the run continued the trace of its caller.
type TraceSpan struct {
	ID      string         `json:"id"`
	Parent  string         `json:"parent,omitempty"`
	Name    string         `json:"name"`
	Kind    string         `json:"kind"`
	Status  string         `json:"status,omitempty"`
	Message string         `json:"message,omitempty"`
	StartUS int64          `json:"start_us"`
	DurUS   int64          `json:"dur_us"`
	Attrs   map[string]any `json:"attrs,omitempty"`
	Events  []TraceEvent   `json:"events,omitempty"`
}

// TraceEvent is a debug event on a span.
type TraceEvent struct {
	Name  string         `json:"name"`
	AtUS  int64          `json:"at_us"`
	Attrs map[string]any `json:"attrs,omitempty"`
}

// Policies is the answer of GET /api/policies: the policies that the state holds, as
// versions, and what the specification declares of them. A policy is a rule that every
// plan is judged by when it is applied; the state keeps its versions, signed, and a
// transaction lists the versions that judged it. The page reads this and runs nothing.
type Policies struct {
	Version  int    `json:"version"`
	Revision string `json:"revision"`
	// SpecOK is false when the specification does not load: a policy cannot be compared
	// with what it declares then.
	SpecOK bool `json:"spec_ok"`
	// Checked says whether the signatures of the versions were checked against a readers
	// file. Without one the versions are shown and no signature is vouched for.
	Checked bool `json:"checked"`
	// InForce counts the policies that judge the next apply, and Pending the ones whose
	// text in the specification differs from the state, or that the specification
	// removes or adds: what the next apply writes.
	InForce  int          `json:"in_force"`
	Pending  int          `json:"pending"`
	Policies []PolicyInfo `json:"policies"`
	// Problem is set when the versions could not be read or do not verify.
	Problem string `json:"problem,omitempty"`
	// Command is what writes the pending changes: a plan, then an apply.
	Command string `json:"command"`
}

// PolicyInfo is one policy.
type PolicyInfo struct {
	Name string `json:"name"`
	// Status is "in-force", "pending" (the specification has other text than the head
	// version), "new" (declared, with no version in force), "removing" (in force, and
	// the specification no longer declares it) or "removed" (the head version is a
	// removal).
	Status   string `json:"status"`
	Declared bool   `json:"declared"`
	// Description is what the specification says the policy is for; empty when it says nothing or does not declare the policy.
	Description string `json:"description,omitempty"`
	// Source is where the specification writes the policy.
	Source *Ref `json:"source,omitempty"`
	// Written is the policy as the specification writes it, with the lines around it kept out; nil when the specification does not load or the file cannot be read.
	Written *SourceBlock `json:"written,omitempty"`
	// Head is the number of the latest version, 0 when the state has none.
	Head int `json:"head"`
	// Rule is the text the specification declares, when it differs from the head.
	Rule string `json:"rule,omitempty"`
	// Judged counts the transactions that were judged by any version of the policy.
	Judged int `json:"judged"`
	// Versions are newest first.
	Versions []PolicyVersionInfo `json:"versions"`
}

// PolicyVersionInfo is one version of a policy.
type PolicyVersionInfo struct {
	Seq    int       `json:"seq"`
	Ref    string    `json:"ref"`
	Digest string    `json:"digest"`
	Text   string    `json:"text,omitempty"`
	At     time.Time `json:"at"`
	// Removal is set when the version removes the policy.
	Removal bool `json:"removal,omitempty"`
	// Signature is "verified", "role_changed", "key_not_in_file", "invalid" or "unsigned", or empty when
	// no readers file was there to check it against.
	Signature string `json:"signature,omitempty"`
	Signer    string `json:"signer,omitempty"`
	// Judged counts the transactions that this version judged, and Runs lists the newest.
	Judged int         `json:"judged"`
	Runs   []JudgedRun `json:"runs,omitempty"`
}

// JudgedRun is a transaction that a version of a policy judged.
type JudgedRun struct {
	ID     string    `json:"id"`
	Kind   string    `json:"kind"`
	Status string    `json:"status"`
	Time   time.Time `json:"time"`
}

// Secrets is the answer of GET /api/secrets: the secrets that the sealed state keeps, by name,
// with the latest version of each and who wrote it, the nodes of the specification that read
// them, and what is wrong. It holds no value and no note: the viewer asks for no key to build
// it, and what it lists is the clear part of the versions and the transactions that wrote
// them. The page reads this and runs nothing.
type Secrets struct {
	Version  int    `json:"version"`
	Revision string `json:"revision"`
	// SpecOK is false when the specification does not load: the nodes that read a secret
	// cannot be listed then.
	SpecOK bool `json:"spec_ok"`
	// Sealed is false when the state is not sealed, which cannot hold a secret.
	Sealed bool `json:"sealed"`
	// Checked says whether the signatures of the versions were judged against a readers file
	// and the roles. Without one the versions are listed and none is vouched for.
	Checked bool `json:"checked"`
	// Secrets lists what the state holds, by name. A secret that the specification reads and
	// the state lacks is an issue and not a row.
	Secrets []SecretEntry `json:"secrets"`
	// Issues are what needs attention, worst first. Problems counts them: it is the number on
	// the tab. AllClear is the sentence for a state that holds secrets and has no issue.
	Issues   []SecretIssue `json:"issues"`
	Problems int           `json:"problems"`
	AllClear string        `json:"all_clear,omitempty"`
	// Note is a sentence about what was left unchecked, shown quietly.
	Note string `json:"note,omitempty"`
	// Unresolved lists the nodes that read a secret whose name is not known before a command
	// runs, so that no row can say they use it.
	Unresolved []string `json:"unresolved,omitempty"`
}

// SecretEntry is one secret of the state.
type SecretEntry struct {
	// Name is the name as it was written; one that is not a valid name for a secret is cleaned
	// up for display and has no command.
	Name string `json:"name"`
	// Status is "set" (the latest version holds a value) or "removed" (the latest version
	// removed the secret).
	Status string `json:"status"`
	// Version is the number of the latest version, and Versions the number of all of them, the
	// removals and the purged ones included.
	Version  int `json:"version"`
	Versions int `json:"versions"`
	// At is when the latest version was written, as its signature has it.
	At time.Time `json:"at"`
	// By is who signed the latest version: the name the readers file gives the key, or, when
	// Trust is "unchecked", the name the signature gives itself, which proves nothing.
	By string `json:"by,omitempty"`
	// Trust is what the transactions make of the latest version: "ok", "vouched", "unchecked",
	// "unconfirmed" or "problem". It is empty when the signatures were not checked.
	Trust string `json:"trust,omitempty"`
	// Bad counts the versions of the secret that no transaction of someone who may write it
	// accounts for.
	Bad int `json:"bad,omitempty"`
	// UsedBy lists the nodes of the specification that read the secret, up to 40, and UsedMore
	// the number left out.
	UsedBy   []SecretUse `json:"used_by"`
	UsedMore int         `json:"used_more,omitempty"`
}

// SecretUse is a node that reads a secret.
type SecretUse struct {
	Addr string `json:"addr"`
	// Pin is the version the node asks for, absent for the latest.
	Pin int `json:"pin,omitempty"`
	// Number is set for a node that reads the number of the version and not the value.
	Number bool `json:"number,omitempty"`
}

// SecretIssue is something about the secrets that needs a person's attention. Level is "bad" (a
// command that reads the secret stops, or the state cannot be trusted) or "chg" (it works, and
// should be mended). Kind is "unsealed", "unreadable", "roles", "unset", "untrusted",
// "unchecked" or "unchecked-all".
type SecretIssue struct {
	Kind  string `json:"kind"`
	Level string `json:"level"`
	Title string `json:"title"`
	Text  string `json:"text"`
	// Items are the secrets or the versions of an issue, up to 20, and More the number left out.
	Items []SecretIssueItem `json:"items,omitempty"`
	More  int               `json:"more,omitempty"`
}

// SecretIssueItem is a secret, or a version of one, and what is wrong with it.
type SecretIssueItem struct {
	Name    string `json:"name"`
	Version int    `json:"version,omitempty"`
	Why     string `json:"why"`
	// UsedBy lists the nodes that read it.
	UsedBy []string `json:"used_by,omitempty"`
	// Command is what to copy and run about it; absent when there is none to give.
	Command string `json:"command,omitempty"`
}

// Studio is what the policy studio starts from: the plans a rule can be tried on, the
// policies that can be started from, the document a rule is written against and some
// rules to begin with. It is read from the state and the project and runs no rule.
type Studio struct {
	Version  int    `json:"version"`
	Revision string `json:"revision"`
	// SpecOK is false when the specification does not load: the values of a plan
	// cannot be decoded then, so no rule can be tried on one.
	SpecOK   bool           `json:"spec_ok"`
	Plans    []StudioPlan   `json:"plans"`
	Policies []StudioPolicy `json:"policies"`
	// State is the state as something a rule can be tried on.
	State *StudioState `json:"state,omitempty"`
	// Schema is the CUE of #Policy, #Plan, #Action and #Change: the document a rule is judged against.
	Schema string `json:"schema"`
	// Fields is the same document as a tree, for completing what is typed.
	Fields   []SchemaField   `json:"fields"`
	Examples []StudioExample `json:"examples"`
	// Skipped says which plan files were found and could not be read.
	Note string `json:"note,omitempty"`
}

// StudioState is the state as a plan that updates every resource to what it holds. Trying a rule on Ref says which
// resources would be refused if a plan touched them.
type StudioState struct {
	Ref string `json:"ref"`
	// Resources counts the resources whose values can be read, which the rule is tried on.
	Resources int `json:"resources"`
}

// StudioPlan is a plan a rule can be tried on: one stored in the state, or a plan file of the project.
type StudioPlan struct {
	// Ref names it in a request: the id of a stored plan, or "file:" and the path of a plan file.
	Ref  string `json:"ref"`
	Kind string `json:"kind"` // "stored" or "file"
	// Label is what a person calls it: the id, or the path relative to the project.
	Label string `json:"label"`
	// Status is where a stored plan stands (pending, partial, partly-stale, stale, applied or expired); a file has none.
	Status  string    `json:"status,omitempty"`
	Open    bool      `json:"open"`
	Author  string    `json:"author,omitempty"`
	Created time.Time `json:"created"`
	Add     int       `json:"add"`
	Change  int       `json:"change"`
	Destroy int       `json:"destroy"`
	// Sealed is set for a stored plan this identity cannot open.
	Sealed bool `json:"sealed,omitempty"`
}

// StudioPolicy is a policy to start from or to compare with.
type StudioPolicy struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	// Description is what the specification says the policy is for; empty when it says nothing or does not declare the policy.
	Description string `json:"description,omitempty"`
	// InForce is the text of the version in force, and Declared the text the specification declares; empty when there is none.
	InForce  string `json:"in_force,omitempty"`
	Declared string `json:"declared,omitempty"`
}

// StudioExample is a rule to start from.
type StudioExample struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	About string `json:"about"`
	Rule  string `json:"rule"`
	// Policy is the rule as a policy, the text the editor starts from.
	Policy string `json:"policy"`
}

// StudioTry is what a rule says of plans.
type StudioTry struct {
	Version int `json:"version"`
	// Check is why the text cannot be stored as it is, or nil. A file that compiles is tried on plans even so.
	Check *engine.RuleProblem `json:"check,omitempty"`
	// Policies are the policies that the text declares.
	Policies []engine.DraftPolicy `json:"policies"`
	Plans    []TriedPlan          `json:"plans"`
	Took     int64                `json:"took_ms"`
}

// TriedPlan is what the policies of the text say of one plan.
type TriedPlan struct {
	Ref string `json:"ref"`
	// State is "judged", "not-judged" (the plan changes no resource, so no policy judges it), "sealed", "unreadable" or "unknown".
	State string `json:"state"`
	Note  string `json:"note,omitempty"`
	// Changes counts the changes of the plan's document.
	Changes int                `json:"changes"`
	Draft   *engine.RuleResult `json:"draft,omitempty"`
}

// StudioDoc is the document of one plan as a rule sees it.
type StudioDoc struct {
	Version int    `json:"version"`
	Ref     string `json:"ref"`
	// Head is the document without its changes, as JSON.
	Head string `json:"head"`
	// Outline lists the changes in order: Index is the n of changes.n in an error.
	Outline []DocChange `json:"outline"`
	// Change is the JSON of the change that was asked for, if one was.
	Change    string `json:"change,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// DocChange is one change in the outline of a document.
type DocChange struct {
	Index  int    `json:"index"`
	Addr   string `json:"addr"`
	Action string `json:"action"`
	Type   string `json:"type"`
}
