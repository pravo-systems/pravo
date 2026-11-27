package state

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// checkAccount asks STS who the resolved credentials belong to and refuses
// unless that is the account the settings name. It fails closed: a failed
// lookup refuses as well, and nothing falls back to another identity. The
// errors name accounts only, never a key or a token.
func checkAccount(ctx context.Context, ac aws.Config, want string) error {
	out, err := sts.NewFromConfig(ac).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return fmt.Errorf("$settings.state.account is %s, but the account of the credentials could not be checked: sts:GetCallerIdentity failed: %w", want, err)
	}
	if got := aws.ToString(out.Account); got != want {
		return fmt.Errorf("$settings.state.account is %s, but the credentials belong to account %s: refusing to open the state", want, got)
	}
	return nil
}
