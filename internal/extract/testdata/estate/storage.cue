package estate

// Buckets. Translated from storage.tf.

logs: $aws.#Resource.aws_s3_bucket & {
	bucket:        "acme-logs-prod"
	force_destroy: false
	tags: {
		Name: "acme-logs-prod"
		Team: "platform"
		ManagedBy: "terraform"
	}
}

logs_versioning: $aws.#Resource.aws_s3_bucket_versioning & {
	bucket: logs.id
	versioning_configuration: [{status: "Enabled"}]
}

logs_public_access: $aws.#Resource.aws_s3_bucket_public_access_block & {
	bucket:                  logs.id
	block_public_acls:       true
	block_public_policy:     true
	ignore_public_acls:      true
	restrict_public_buckets: true
}

// The auditors read this bucket once a year. Do not empty it.
audit: $aws.#Resource.aws_s3_bucket & {
	bucket:        "acme-audit-prod"
	force_destroy: false
	tags: {
		Name: "acme-audit-prod"
		Team: "security"
		ManagedBy: "terraform"
		Compliance: "soc2"
	}
}

audit_versioning: $aws.#Resource.aws_s3_bucket_versioning & {
	bucket: audit.id
	versioning_configuration: [{status: "Enabled"}]
}

audit_public_access: $aws.#Resource.aws_s3_bucket_public_access_block & {
	bucket:                  audit.id
	block_public_acls:       true
	block_public_policy:     true
	ignore_public_acls:      true
	restrict_public_buckets: true
}

assets: $aws.#Resource.aws_s3_bucket & {
	bucket:        "acme-assets-prod"
	force_destroy: false
	tags: {
		Name: "acme-assets-prod"
		Team: "web"
		ManagedBy: "terraform"
	}
}

assets_versioning: $aws.#Resource.aws_s3_bucket_versioning & {
	bucket: assets.id
	versioning_configuration: [{status: "Enabled"}]
}

assets_public_access: $aws.#Resource.aws_s3_bucket_public_access_block & {
	bucket:                  assets.id
	block_public_acls:       true
	block_public_policy:     true
	ignore_public_acls:      true
	restrict_public_buckets: true
}

uploads: $aws.#Resource.aws_s3_bucket & {
	tags: {
		Name: "acme-uploads-prod"
		Team: "web"
		ManagedBy: "terraform"
	}
	force_destroy: false
	bucket:        "acme-uploads-prod"
}

uploads_versioning: $aws.#Resource.aws_s3_bucket_versioning & {
	bucket: uploads.id
	versioning_configuration: [{status: "Enabled"}]
}

uploads_public_access: $aws.#Resource.aws_s3_bucket_public_access_block & {
	bucket:                  uploads.id
	block_public_acls:       true
	block_public_policy:     true
	ignore_public_acls:      true
	restrict_public_buckets: true
}

backups: $aws.#Resource.aws_s3_bucket & {
	bucket:        "acme-backups-prod"
	force_destroy: false
	tags: {
		Name: "acme-backups-prod"
		Team: "platform"
		ManagedBy: "terraform"
		CostCenter: "4410"
	}
}

backups_versioning: $aws.#Resource.aws_s3_bucket_versioning & {
	bucket: backups.id
	versioning_configuration: [{status: "Enabled"}]
}

backups_public_access: $aws.#Resource.aws_s3_bucket_public_access_block & {
	bucket:                  backups.id
	block_public_acls:       true
	block_public_policy:     true
	ignore_public_acls:      true
	restrict_public_buckets: true
}

exports: $aws.#Resource.aws_s3_bucket & {
	bucket:        "acme-exports-prod"
	force_destroy: false
	tags: {
		Name: "acme-exports-prod"
		Team: "data"
		ManagedBy: "terraform"
	}
}

exports_versioning: $aws.#Resource.aws_s3_bucket_versioning & {
	bucket: exports.id
	versioning_configuration: [{status: "Enabled"}]
}

exports_public_access: $aws.#Resource.aws_s3_bucket_public_access_block & {
	bucket:                  exports.id
	block_public_acls:       true
	block_public_policy:     true
	ignore_public_acls:      true
	restrict_public_buckets: true
}

reports: $aws.#Resource.aws_s3_bucket & {
	bucket:        "acme-reports-prod"
	force_destroy: false
	tags: {
		Name: "acme-reports-prod"
		Team: "data"
		ManagedBy: "terraform"
	}
}

reports_versioning: $aws.#Resource.aws_s3_bucket_versioning & {
	bucket: reports.id
	versioning_configuration: [{status: "Suspended"}]
}

reports_public_access: $aws.#Resource.aws_s3_bucket_public_access_block & {
	bucket:                  reports.id
	block_public_acls:       true
	block_public_policy:     true
	ignore_public_acls:      true
	restrict_public_buckets: true
}

artifacts: $aws.#Resource.aws_s3_bucket & {
	bucket:        "acme-artifacts-prod"
	force_destroy: false
	tags: {
		Name: "acme-artifacts-prod"
		Team: "platform"
		ManagedBy: "terraform"
	}
}

artifacts_versioning: $aws.#Resource.aws_s3_bucket_versioning & {
	bucket: artifacts.id
	versioning_configuration: [{status: "Enabled"}]
}

artifacts_public_access: $aws.#Resource.aws_s3_bucket_public_access_block & {
	bucket:                  artifacts.id
	block_public_acls:       true
	block_public_policy:     true
	ignore_public_acls:      true
	restrict_public_buckets: true
}

media: $aws.#Resource.aws_s3_bucket & {
	bucket:        "acme-media-prod"
	force_destroy: false
	tags: {
		Name: "acme-media-prod"
		Team: "web"
		ManagedBy: "terraform"
	}
}

media_versioning: $aws.#Resource.aws_s3_bucket_versioning & {
	bucket: media.id
	versioning_configuration: [{status: "Enabled"}]
}

media_public_access: $aws.#Resource.aws_s3_bucket_public_access_block & {
	bucket:                  media.id
	block_public_acls:       true
	block_public_policy:     true
	ignore_public_acls:      true
	restrict_public_buckets: true
}

archive: $aws.#Resource.aws_s3_bucket & {
	bucket:        "legacy-archive"
	force_destroy: false
	tags: {
		Name: "legacy-archive"
		Team: "platform"
		ManagedBy: "terraform"
	}
}

archive_versioning: $aws.#Resource.aws_s3_bucket_versioning & {
	bucket: archive.id
	versioning_configuration: [{status: "Enabled"}]
}

archive_public_access: $aws.#Resource.aws_s3_bucket_public_access_block & {
	bucket:                  archive.id
	block_public_acls:       true
	block_public_policy:     true
	ignore_public_acls:      true
	restrict_public_buckets: true
}

billing: $aws.#Resource.aws_s3_bucket & {
	bucket:        "legacy-billing-exports"
	force_destroy: false
	tags: {
		Name: "legacy-billing-exports"
		Team: "finance"
		ManagedBy: "terraform"
		CostCenter: "1200"
	}
}

billing_versioning: $aws.#Resource.aws_s3_bucket_versioning & {
	bucket: billing.id
	versioning_configuration: [{status: "Enabled"}]
}

billing_public_access: $aws.#Resource.aws_s3_bucket_public_access_block & {
	bucket:                  billing.id
	block_public_acls:       true
	block_public_policy:     true
	ignore_public_acls:      true
	restrict_public_buckets: true
}

scratch: $aws.#Resource.aws_s3_bucket & {
	bucket:        "acme-scratch-prod"
	force_destroy: false
	tags: {
		Name: "acme-scratch-prod"
		Team: "data"
		ManagedBy: "terraform"
	}
}

scratch_public_access: $aws.#Resource.aws_s3_bucket_public_access_block & {
	bucket:                  scratch.id
	block_public_acls:       true
	block_public_policy:     false
	ignore_public_acls:      true
	restrict_public_buckets: false
}
