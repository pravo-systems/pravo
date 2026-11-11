package estate

// Network. Translated from network.tf.

vpc: $aws.#Resource.aws_vpc & {
	cidr_block: "10.20.0.0/16"
	tags: Name: "acme-prod"
}

subnet_a: $aws.#Resource.aws_subnet & {
	vpc_id:     vpc.id
	cidr_block: "10.20.1.0/24"
	tags: Name: "acme-prod-a"
}

subnet_b: $aws.#Resource.aws_subnet & {
	vpc_id:     vpc.id
	cidr_block: "10.20.2.0/24"
	tags: Name: "acme-prod-b"
}

web_sg: $aws.#Resource.aws_security_group & {
	name:        "acme-web"
	description: "HTTPS from anywhere"
	vpc_id:      vpc.id
	ingress: [{from_port: 443, to_port: 443, protocol: "tcp", cidr_blocks: ["0.0.0.0/0"]}]
	tags: Name: "acme-web"
}

api_sg: $aws.#Resource.aws_security_group & {
	name:        "acme-api"
	description: "API from the VPC"
	vpc_id:      vpc.id
	ingress: [{from_port: 8080, to_port: 8080, protocol: "tcp", cidr_blocks: ["10.20.0.0/16"]}]
	tags: Name: "acme-api"
}

db_sg: $aws.#Resource.aws_security_group & {
	name:        "acme-db"
	description: "Postgres from the VPC"
	vpc_id:      vpc.id
	ingress: [{from_port: 5432, to_port: 5432, protocol: "tcp", cidr_blocks: ["10.20.0.0/16"]}]
	tags: Name: "acme-db"
}

cache_sg: $aws.#Resource.aws_security_group & {
	name:        "acme-cache"
	description: "Redis from the VPC"
	vpc_id:      vpc.id
	ingress: [{from_port: 6379, to_port: 6379, protocol: "tcp", cidr_blocks: ["10.20.0.0/16"]}]
	tags: Name: "acme-cache"
}

metrics_sg: $aws.#Resource.aws_security_group & {
	name:        "acme-metrics"
	description: "Metrics from the VPC"
	vpc_id:      vpc.id
	ingress: [{from_port: 9090, to_port: 9090, protocol: "tcp", cidr_blocks: ["10.20.0.0/16"]}]
	tags: Name: "acme-metrics"
}

bastion_sg: $aws.#Resource.aws_security_group & {
	name:        "acme-bastion"
	description: "SSH from the office, and ping"
	vpc_id:      vpc.id
	ingress: [
		{from_port: 22, to_port: 22, protocol: "tcp", cidr_blocks: ["198.51.100.0/24"]},
		{from_port: -1, to_port: -1, protocol: "icmp", cidr_blocks: ["198.51.100.0/24"]},
	]
	tags: Name: "acme-bastion"
}
