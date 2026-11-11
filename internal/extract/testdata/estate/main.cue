package estate

import aws "pravo.dev/providers/hashicorp/aws"

endpoint: *"http://127.0.0.1:7301" | string @tag(endpoint)

$aws: aws.#Provider & {
	$version:                    "= 5.80.0"
	region:                      "eu-north-1"
	access_key:                  "test"
	secret_key:                  "test"
	skip_credentials_validation: true
	skip_metadata_api_check:     "true"
	skip_requesting_account_id:  true
	s3_use_path_style:           true
	endpoints: [{
		s3:       endpoint
		iam:      endpoint
		ec2:      endpoint
		sts:      endpoint
		dynamodb: endpoint
		ssm:      endpoint
	}]
}
