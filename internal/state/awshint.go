package state

import (
	"errors"
	"fmt"
	"regexp"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

// awsLoginDocs is where the refusal is explained.
const awsLoginDocs = "docs/concepts/state.md#signing-in-with-aws-login-through-a-role"

// AWSSourceProfileHint explains a refusal of the AWS library that reads as
// "failed to load assume role ROLE, of profile SOURCE, <nil>". The library
// reads a profile that was signed in with `aws login` when it is used on its
// own, but refuses it as the source_profile of a role profile, and says nothing
// of why. The hint names the role and the source profile, gives the likely
// cause and the way around it. It returns "" for any other error. Pravo does
// not retry and does not use another identity: the command still fails.
func AWSSourceProfileHint(err error) string {
	var e awsconfig.SharedConfigAssumeRoleError
	if errors.As(err, &e) && e.Err == nil {
		return awsSourceProfileHint(e.RoleARN, e.Profile)
	}
	return ""
}

// providerAssumeRole matches the same refusal as a provider reports it, in the
// text of its error. Only the whole sentence with the nil cause matches.
var providerAssumeRole = regexp.MustCompile(`failed to load assume role (\S+), of profile (\S+), <nil>`)

// AWSSourceProfileHintFromText is AWSSourceProfileHint for an error that only
// came back as text, as a provider's does.
func AWSSourceProfileHintFromText(msg string) string {
	if m := providerAssumeRole.FindStringSubmatch(msg); m != nil {
		return awsSourceProfileHint(m[1], m[2])
	}
	return ""
}

func awsSourceProfileHint(role, source string) string {
	return fmt.Sprintf("the AWS library could not use profile %q as the source_profile of role %s. "+
		"The likely cause is that profile %q is an `aws login` session (or another sign-in), "+
		"which the AWS Go library cannot yet use as a source_profile. "+
		"Give the role profile a source_profile that gets its keys from the CLI: "+
		"credential_process = aws configure export-credentials --profile %s --format process. "+
		"See %s",
		source, role, source, source, awsLoginDocs)
}
