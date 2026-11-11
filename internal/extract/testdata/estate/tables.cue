package estate

// Tables. Translated from tables.tf.

sessions: $aws.#Resource.aws_dynamodb_table & {
	name:         "acme-sessions"
	billing_mode: "PAY_PER_REQUEST"
	hash_key:     "id"
	attribute: [{name: "id", type: "S"}]
	tags: {
		Team:      "web"
		ManagedBy: "terraform"
	}
}

users: $aws.#Resource.aws_dynamodb_table & {
	name:         "acme-users"
	billing_mode: "PAY_PER_REQUEST"
	hash_key:     "id"
	attribute: [{name: "id", type: "S"}]
	point_in_time_recovery: [{enabled: true}]
	tags: {
		Team:      "web"
		ManagedBy: "terraform"
	}
}

jobs: $aws.#Resource.aws_dynamodb_table & {
	name:         "acme-jobs"
	billing_mode: "PAY_PER_REQUEST"
	hash_key:     "id"
	attribute: [{name: "id", type: "S"}]
	tags: {
		Team:      "platform"
		ManagedBy: "terraform"
	}
}

locks: $aws.#Resource.aws_dynamodb_table & {
	name:         "acme-locks"
	billing_mode: "PAY_PER_REQUEST"
	hash_key:     "id"
	attribute: [{name: "id", type: "S"}]
	tags: {
		Team:      "platform"
		ManagedBy: "terraform"
	}
}

exports_index: $aws.#Resource.aws_dynamodb_table & {
	name:         "acme-exports-index"
	billing_mode: "PAY_PER_REQUEST"
	hash_key:     "id"
	attribute: [{name: "id", type: "S"}]
	tags: {
		Team:      "data"
		ManagedBy: "terraform"
	}
}

features: $aws.#Resource.aws_dynamodb_table & {
	name:         "acme-features"
	billing_mode: "PAY_PER_REQUEST"
	hash_key:     "id"
	attribute: [{name: "id", type: "S"}]
	tags: {
		Team:      "web"
		ManagedBy: "terraform"
	}
}

events: $aws.#Resource.aws_dynamodb_table & {
	name:         "acme-events"
	billing_mode: "PAY_PER_REQUEST"
	hash_key:     "stream"
	range_key:    "at"
	attribute: [{name: "stream", type: "S"}, {name: "at", type: "S"}]
	tags: {
		Team:      "data"
		ManagedBy: "terraform"
	}
}

audit_trail: $aws.#Resource.aws_dynamodb_table & {
	name:         "acme-audit-trail"
	billing_mode: "PAY_PER_REQUEST"
	hash_key:     "actor"
	range_key:    "at"
	attribute: [{name: "actor", type: "S"}, {name: "at", type: "S"}]
	point_in_time_recovery: [{enabled: true}]
	tags: {
		Team:      "security"
		ManagedBy: "terraform"
	}
}
