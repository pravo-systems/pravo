package estate

import "encoding/json"

// Service roles. Translated from iam.tf.

ingest_role: $aws.#Resource.aws_iam_role & {
	name:        "svc-ingest"
	path:        "/service/"
	description: "Runs the ingest function"
	assume_role_policy: json.Marshal({
		Version: "2012-10-17"
		Statement: [{
			Effect: "Allow"
			Action: "sts:AssumeRole"
			Principal: Service: "lambda.amazonaws.com"
		}]
	})
	tags: {
		Team:      "data"
		ManagedBy: "terraform"
	}
}

ingest_role_read: $aws.#Resource.aws_iam_role_policy & {
	name: "read-uploads"
	role: ingest_role.id
	policy: json.Marshal({
		Version: "2012-10-17"
		Statement: [{
			Effect: "Allow"
			Action: ["s3:GetObject", "s3:ListBucket"]
			Resource: [uploads.arn, "\(uploads.arn)/*"]
		}]
	})
}

resize_role: $aws.#Resource.aws_iam_role & {
	name:        "svc-resize"
	path:        "/service/"
	description: "Runs the image resizer"
	assume_role_policy: json.Marshal({
		Version: "2012-10-17"
		Statement: [{
			Effect: "Allow"
			Action: "sts:AssumeRole"
			Principal: Service: "lambda.amazonaws.com"
		}]
	})
	tags: {
		Team:      "web"
		ManagedBy: "terraform"
	}
}

resize_role_read: $aws.#Resource.aws_iam_role_policy & {
	name: "read-media"
	role: resize_role.id
	policy: json.Marshal({
		Version: "2012-10-17"
		Statement: [{
			Effect: "Allow"
			Action: ["s3:GetObject", "s3:ListBucket"]
			Resource: [media.arn, "\(media.arn)/*"]
		}]
	})
}

export_role: $aws.#Resource.aws_iam_role & {
	name:        "svc-export"
	path:        "/service/"
	description: "Runs the nightly export"
	assume_role_policy: json.Marshal({
		Version: "2012-10-17"
		Statement: [{
			Effect: "Allow"
			Action: "sts:AssumeRole"
			Principal: Service: "lambda.amazonaws.com"
		}]
	})
	tags: {
		Team:      "data"
		ManagedBy: "terraform"
	}
}

export_role_read: $aws.#Resource.aws_iam_role_policy & {
	name: "read-exports"
	role: export_role.id
	policy: json.Marshal({
		Version: "2012-10-17"
		Statement: [{
			Effect: "Allow"
			Action: ["s3:GetObject", "s3:ListBucket"]
			Resource: [exports.arn, "\(exports.arn)/*"]
		}]
	})
}

report_role: $aws.#Resource.aws_iam_role & {
	name:        "svc-report"
	path:        "/service/"
	description: "Runs the report builder"
	assume_role_policy: json.Marshal({
		Version: "2012-10-17"
		Statement: [{
			Effect: "Allow"
			Action: "sts:AssumeRole"
			Principal: Service: "lambda.amazonaws.com"
		}]
	})
	tags: {
		Team:      "data"
		ManagedBy: "terraform"
	}
}

report_role_read: $aws.#Resource.aws_iam_role_policy & {
	name: "read-reports"
	role: report_role.id
	policy: json.Marshal({
		Version: "2012-10-17"
		Statement: [{
			Effect: "Allow"
			Action: ["s3:GetObject", "s3:ListBucket"]
			Resource: [reports.arn, "\(reports.arn)/*"]
		}]
	})
}

api_role: $aws.#Resource.aws_iam_role & {
	name:        "svc-api"
	path:        "/service/"
	description: "Task role of the API"
	assume_role_policy: json.Marshal({
		Version: "2012-10-17"
		Statement: [{
			Effect: "Allow"
			Action: "sts:AssumeRole"
			Principal: Service: "ecs-tasks.amazonaws.com"
		}]
	})
	tags: {
		Team:      "web"
		ManagedBy: "terraform"
	}
}

api_role_read: $aws.#Resource.aws_iam_role_policy & {
	name: "read-assets"
	role: api_role.id
	policy: json.Marshal({
		Version: "2012-10-17"
		Statement: [{
			Effect: "Allow"
			Action: ["s3:GetObject", "s3:ListBucket"]
			Resource: [assets.arn, "\(assets.arn)/*"]
		}]
	})
}

worker_role: $aws.#Resource.aws_iam_role & {
	name:        "svc-worker"
	path:        "/service/"
	description: "Task role of the workers"
	assume_role_policy: json.Marshal({
		Version: "2012-10-17"
		Statement: [{
			Effect: "Allow"
			Action: "sts:AssumeRole"
			Principal: Service: "ecs-tasks.amazonaws.com"
		}]
	})
	tags: {
		Team:      "platform"
		ManagedBy: "terraform"
	}
}

worker_role_read: $aws.#Resource.aws_iam_role_policy & {
	name: "read-artifacts"
	role: worker_role.id
	policy: json.Marshal({
		Version: "2012-10-17"
		Statement: [{
			Effect: "Allow"
			Action: ["s3:GetObject", "s3:ListBucket"]
			Resource: [artifacts.arn, "\(artifacts.arn)/*"]
		}]
	})
}

backup_role: $aws.#Resource.aws_iam_role & {
	name:        "svc-backup"
	path:        "/service/"
	description: "Used by the backup plan"
	assume_role_policy: json.Marshal({
		Version: "2012-10-17"
		Statement: [{
			Effect: "Allow"
			Action: "sts:AssumeRole"
			Principal: Service: "backup.amazonaws.com"
		}]
	})
	tags: {
		Team:      "platform"
		ManagedBy: "terraform"
	}
}

backup_role_read: $aws.#Resource.aws_iam_role_policy & {
	name: "read-backups"
	role: backup_role.id
	policy: json.Marshal({
		Version: "2012-10-17"
		Statement: [{
			Effect: "Allow"
			Action: ["s3:GetObject", "s3:ListBucket"]
			Resource: [backups.arn, "\(backups.arn)/*"]
		}]
	})
}

audit_role: $aws.#Resource.aws_iam_role & {
	name:        "svc-audit"
	path:        "/service/"
	description: "Instance role of the audit collector"
	assume_role_policy: json.Marshal({
		Version: "2012-10-17"
		Statement: [{
			Effect: "Allow"
			Action: "sts:AssumeRole"
			Principal: Service: "ec2.amazonaws.com"
		}]
	})
	tags: {
		Team:      "security"
		ManagedBy: "terraform"
	}
}

audit_role_read: $aws.#Resource.aws_iam_role_policy & {
	name: "read-audit"
	role: audit_role.id
	policy: json.Marshal({
		Version: "2012-10-17"
		Statement: [{
			Effect: "Allow"
			Action: ["s3:GetObject", "s3:ListBucket"]
			Resource: [audit.arn, "\(audit.arn)/*"]
		}]
	})
}

shipper_role: $aws.#Resource.aws_iam_role & {
	name:        "svc-log-shipper"
	path:        "/service/"
	description: "Instance role of the log shipper"
	assume_role_policy: json.Marshal({
		Version: "2012-10-17"
		Statement: [{
			Effect: "Allow"
			Action: "sts:AssumeRole"
			Principal: Service: "ec2.amazonaws.com"
		}]
	})
	tags: {
		Team:      "platform"
		ManagedBy: "terraform"
	}
}

shipper_role_read: $aws.#Resource.aws_iam_role_policy & {
	name: "read-logs"
	role: shipper_role.id
	policy: json.Marshal({
		Version: "2012-10-17"
		Statement: [{
			Effect: "Allow"
			Action: ["s3:GetObject", "s3:ListBucket"]
			Resource: [logs.arn, "\(logs.arn)/*"]
		}]
	})
}

// The pipeline signs in with a web identity; nothing else here looks like it.
deploy_role: $aws.#Resource.aws_iam_role & {
	name:                 "github-deploy"
	max_session_duration: 7200
	assume_role_policy: json.Marshal({
		Version: "2012-10-17"
		Statement: [{
			Effect: "Allow"
			Action: "sts:AssumeRoleWithWebIdentity"
			Principal: Federated: "arn:aws:iam::123456789012:oidc-provider/token.actions.githubusercontent.com"
			Condition: StringLike: "token.actions.githubusercontent.com:sub": "repo:acme/infra:*"
		}]
	})
	tags: Purpose: "ci"
}
