package estate

// Configuration values. Translated from config.tf.

api_db_host: $aws.#Resource.aws_ssm_parameter & {
	name:  "/acme/prod/api/db_host"
	type:  "String"
	value: "db.internal.acme.example"
	tags: ManagedBy: "terraform"
}

api_db_name: $aws.#Resource.aws_ssm_parameter & {
	name:  "/acme/prod/api/db_name"
	type:  "String"
	value: "api"
	tags: ManagedBy: "terraform"
}

api_log_level: $aws.#Resource.aws_ssm_parameter & {
	name:  "/acme/prod/api/log_level"
	type:  "String"
	value: "info"
	tags: ManagedBy: "terraform"
}

api_feature_flags: $aws.#Resource.aws_ssm_parameter & {
	name:  "/acme/prod/api/feature_flags"
	type:  "StringList"
	value: "search,export,beta-ui"
	tags: ManagedBy: "terraform"
}

worker_queue: $aws.#Resource.aws_ssm_parameter & {
	name:  "/acme/prod/worker/queue"
	type:  "String"
	value: "jobs-prod"
	tags: ManagedBy: "terraform"
}

worker_concurrency: $aws.#Resource.aws_ssm_parameter & {
	name:  "/acme/prod/worker/concurrency"
	type:  "String"
	value: "8" // raised from 4 in March
	tags: ManagedBy: "terraform"
}

worker_log_level: $aws.#Resource.aws_ssm_parameter & {
	name:  "/acme/prod/worker/log_level"
	type:  "String"
	value: "warn"
	tags: ManagedBy: "terraform"
}

export_schedule: $aws.#Resource.aws_ssm_parameter & {
	name:  "/acme/prod/export/schedule"
	type:  "String"
	value: "cron(0 2 * * ? *)"
	description: "When the nightly export runs"
	tags: ManagedBy: "terraform"
}

export_target: $aws.#Resource.aws_ssm_parameter & {
	name:  "/acme/prod/export/target"
	type:  "String"
	value: "s3://acme-exports-prod/nightly"
	tags: ManagedBy: "terraform"
}

report_recipients: $aws.#Resource.aws_ssm_parameter & {
	name:  "/acme/prod/report/recipients"
	type:  "StringList"
	value: "ops@acme.example,finance@acme.example"
	description: "Who gets the weekly report"
	tags: ManagedBy: "terraform"
}

report_template: $aws.#Resource.aws_ssm_parameter & {
	name:  "/acme/prod/report/template"
	type:  "String"
	value: "weekly-v3"
	tier: "Advanced"
	tags: ManagedBy: "terraform"
}

resize_sizes: $aws.#Resource.aws_ssm_parameter & {
	name:  "/acme/prod/resize/sizes"
	type:  "StringList"
	value: "320,640,1280"
	tags: ManagedBy: "terraform"
}
