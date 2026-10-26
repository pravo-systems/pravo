package serve

// studioExamples are the rules the studio offers to start from. Each is a complete
// rule: the text a policy stores, which says what must hold of the document of a plan. A
// test checks that each one can be stored.
var studioExamples = []StudioExample{
	{
		ID:    "no-destroy",
		Title: "Destroy nothing",
		About: "A plan may not destroy anything. A replacement counts as a destruction.",
		Rule:  "summary: destroy: 0\n",
	},
	{
		ID:    "small-plans",
		Title: "Keep plans small",
		About: "One plan adds at most 25 resources and destroys at most 5. The limit binds one plan, not the sum of several.",
		Rule:  "summary: {\n\tadd:     <=25\n\tdestroy: <=5\n\t...\n}\n",
	},
	{
		ID:    "keepsakes",
		Title: "Keep some types",
		About: "Resources of these types are never deleted or replaced. error() puts your own words in the refusal. The fields the guard reads are declared first, because a CUE scope is lexical.",
		Rule: `changes: [...{
	addr:   string
	mode:   string
	type:   string
	action: string
	if mode == "managed" && (type == "aws_kms_key" || type == "aws_dynamodb_table") && (action == "delete" || action == "replace") {
		error("\(addr): \(type) may not be \(action)d")
	}
}]
`,
	},
	{
		ID:    "tags",
		Title: "Require a tag",
		About: "Every change that creates or updates a bucket carries a tag. A constraint on after is strict: a tag that is missing, null or unknown until apply is a refusal.",
		Rule: `changes: [...{
	type:   string
	action: string
	if type == "aws_s3_bucket" && (action == "create" || action == "update") {
		after: tags: ManagedBy: "pravo"
	}
}]
`,
	},
	{
		ID:    "names",
		Title: "Name what is created",
		About: "A new bucket is named for the project. A pattern constrains the value, and does not require that it is set unless the guard puts it there.",
		Rule: `changes: [...{
	type:   string
	action: string
	if type == "aws_s3_bucket" && action == "create" {
		after: bucket: =~"^pravo-"
	}
}]
`,
	},
	{
		ID:    "providers",
		Title: "Pin a provider's major version",
		About: "Every provider the plan used is on version 2. A map of every name is constrained with a template, so a provider the plan does not use is not asked about.",
		Rule:  "providers: [string]: =~\"^2\\\\.\"\n",
	},
}
