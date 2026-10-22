// Package serve is the read-only web viewer behind "pravo serve".
//
// This file is the contract between the Go server and the browser interface in
// web/. Every type here is sent as JSON. The server never sends a value that is
// marked sensitive, a private blob, a provider-configuration digest or a file
// that the evaluation did not load: see README.md.
package serve

import "time"

// APIVersion is the layout version of every response in this file.
const APIVersion = 1

// Graph is GET /api/graph: everything the graph view and the module tree need,
// without attribute values.
type Graph struct {
	Version int `json:"version"`
	// Revision changes whenever anything in the response would. The page
	// compares it with the one in an event from /api/events.
	Revision string    `json:"revision"`
	Project  Project   `json:"project"`
	Nodes    []Node    `json:"nodes"`
	Edges    []Edge    `json:"edges"`
	Modules  []Module  `json:"modules"`
	Outputs  []Output  `json:"outputs"`
	Plan     *PlanInfo `json:"plan,omitempty"`
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
	// Policies are the --policy files the viewer was started with.
	Policies []string `json:"policies,omitempty"`
	// SpecError is set when the specification does not load. The viewer then
	// shows the state alone: every node has Presence "state".
	SpecError string    `json:"spec_error,omitempty"`
	LoadedAt  time.Time `json:"loaded_at"`
}

// StateInfo describes the state that is read.
type StateInfo struct {
	Location string `json:"location"`
	// Backend is "local" or "s3".
	Backend string `json:"backend"`
	Lineage string `json:"lineage,omitempty"`
	Format  int    `json:"format,omitempty"`
	Records int    `json:"records"`
	Locks   int    `json:"locks"`
}

// ProviderInfo is a provider the specification declares or the state records.
type ProviderInfo struct {
	Local   string `json:"local"`
	Source  string `json:"source,omitempty"`
	Version string `json:"version,omitempty"`
}

// Node is one resource, data source, ephemeral resource or function call.
type Node struct {
	// Addr is the CUE path of the node, such as "services.api.secret". It is
	// opaque to the page: it is a key, never split. Use Module for the parent.
	Addr string `json:"addr"`
	// Mode is "managed", "data", "ephemeral", "call" or "builtin".
	Mode string `json:"mode"`
	Type string `json:"type"`
	// Name is the last label of the address.
	Name string `json:"name"`
	// Module is the Path of the module the node sits in, "" for the root.
	Module string `json:"module"`
	// Provider is the provider key: "aws" or "aws.west".
	Provider string `json:"provider,omitempty"`
	// Presence is "both" (declared and recorded), "spec" (declared, not yet
	// created) or "state" (recorded, no longer declared). An ephemeral resource,
	// a function call and a built-in value are never recorded: they are "both"
	// while the specification declares them, and are not pending a creation.
	Presence string `json:"presence"`
	// Action is the action of the loaded plan for the node: "create", "update",
	// "replace", "delete", "read", "move", "import" or "no-op". It is "" when no
	// plan is loaded or the plan did not run the node.
	Action string `json:"action,omitempty"`
	// Drifted and Gone are the marks of the record: a read found that the real
	// object differs, or is absent.
	Drifted bool `json:"drifted,omitempty"`
	Gone    bool `json:"gone,omitempty"`
	// Locked is set when a lock or reservation holds the node.
	Locked bool `json:"locked,omitempty"`
	// Plans lists the stored plans (see StoredPlan) that can still be applied and
	// change the node, newest first. Overlap is set when two or more do: applying
	// one makes the other stale. Both are as of the revision of the graph.
	Plans   []string `json:"plans,omitempty"`
	Overlap bool     `json:"overlap,omitempty"`
	// Sensitive counts the attributes of the record whose values are withheld,
	// the ones that hold a value and not null.
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

// Module is a struct of the specification that contains resources: an instance
// of a definition, a package instance, or a plain struct. The address of every
// node below it starts with its Path.
type Module struct {
	Path   string `json:"path"`
	Parent string `json:"parent"`
	Name   string `json:"name"`
	// Resources counts the nodes below the module, nested modules included.
	Resources int `json:"resources"`
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
// no value.
type Output struct {
	Name      string `json:"name"`
	Sensitive bool   `json:"sensitive,omitempty"`
	// Redacted is "" when the value is sent, else "sensitive" (the output is
	// marked so) or "derived" (the value contains a sensitive value of the
	// state). Sensitive is set in both cases.
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
	// Stale reports that the plan can no longer be applied; Reasons say why.
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
	Applied int `json:"applied,omitempty"`
	// Hints are the hints of the plan, such as a create and a delete that look
	// like one resource under a new address, worked out as the engine does.
	Hints []string `json:"hints,omitempty"`
}

// Detail is GET /api/detail?addr=...: one node with its attributes. Values
// that are sensitive are absent, with Redacted saying why.
type Detail struct {
	Version  int         `json:"version"`
	Node     Node        `json:"node"`
	Provider ProviderRef `json:"provider"`
	Attrs    []Attr      `json:"attrs"`
	// SchemaKnown is false when the provider schema could not be read. Every
	// value is then withheld, since nothing says which are sensitive.
	SchemaKnown bool     `json:"schema_known"`
	DependsOn   []string `json:"depends_on"`
	UsedBy      []string `json:"used_by"`
	// Reach lists everything a change to the node can reach: the transitive
	// dependents recorded in the state.
	Reach   []string       `json:"reach,omitempty"`
	Change  *Change        `json:"change,omitempty"`
	History []HistoryEntry `json:"history,omitempty"`
	Lock    *LockInfo      `json:"lock,omitempty"`
	Times   Times          `json:"times"`
	Notes   []string       `json:"notes,omitempty"`
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

// Search is GET /api/search?q=...: the nodes and modules that match, for the
// palette. The page also searches the graph it holds; this exists for a graph
// that was cut down.
type Search struct {
	Version int      `json:"version"`
	Nodes   []Node   `json:"nodes"`
	Modules []Module `json:"modules"`
}

// PlanCounts are the actions of a plan, as the plan prints them: a replace
// counts as an add and a destroy.
type PlanCounts struct {
	Add     int `json:"add"`
	Change  int `json:"change"`
	Destroy int `json:"destroy"`
	Read    int `json:"read"`
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
	// applied and the rest can be), "stale" (the state moved on, Reasons say how),
	// "applied" (every change was applied) or "expired" (it was not applied before
	// it expired). Open is set for pending and partial: those can be applied, and
	// two of them that change one resource overlap.
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
	// Reasons say why a stale plan can no longer be applied: the first 20, each
	// clipped to 300 characters; ReasonsOmitted counts the others.
	Reasons        []string `json:"reasons,omitempty"`
	ReasonsOmitted int      `json:"reasons_omitted,omitempty"`
	// Touches lists the resources the plan changes, by address, up to 2000;
	// Truncated counts those left out. Overlaps lists the other open plans that
	// change one of them.
	Touches   []PlanChange  `json:"touches"`
	Truncated int           `json:"truncated,omitempty"`
	Overlaps  []PlanOverlap `json:"overlaps,omitempty"`
	// Apply is the command that applies the plan.
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

// Event is the data of a server-sent event on /api/events. The event name is
// "change" and the page fetches /api/graph again, and the open node, when the
// Revision differs from the one it holds.
type Event struct {
	Revision string `json:"revision"`
	// What is any of "state", "spec", "plan", "locks", "plans". "plan" is the plan
	// file of --plan; "plans" are the plans stored in the state: one appeared, was
	// collected, was applied from, or passed its expiry.
	What []string `json:"what"`
}
