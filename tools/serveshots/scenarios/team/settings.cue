package platform

import "pravo.dev/pravo"

// The state is in a bucket, so that everyone on the team reads the same one.
// The endpoint is the S3-compatible server that the shots run against.
$settings: pravo.#Settings & {
	state: pravo.#S3State & {
		backend:    "s3"
		bucket:     "platform-state"
		prefix:     "platform"
		region:     "us-east-1"
		endpoint:   "@FAKES3@"
		path_style: true
	}
}
