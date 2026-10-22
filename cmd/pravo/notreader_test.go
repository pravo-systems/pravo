package main

import (
	"fmt"
	"strings"
	"testing"
)

// When the identity cannot be chosen, the refusal says why and does not only
// advise to ask a reader to add the identity.
func TestNotReaderErrorSaysWhyNoIdentityWasChosen(t *testing.T) {
	rf := readersFile{}
	plain := notReaderError(rf, nil, fmt.Errorf("%w in ~/.config/pravo/id. Make one with: pravo id new --name NAME", errNoIdentity))
	if !strings.Contains(plain.Error(), "(no identity found).") {
		t.Errorf("plain case changed: %v", plain)
	}
	several := notReaderError(rf, nil, fmt.Errorf("%w: ~/.config/pravo/id holds several identities (ada dan) and none is named like you; choose one with --identity NAME", errNoIdentity))
	if !strings.Contains(several.Error(), "holds several identities (ada dan)") {
		t.Errorf("the reason is lost: %v", several)
	}
}

// An identity that cannot be opened is not a reason to ask to be added.
func TestNotReaderErrorForAnIdentityThatCannotBeOpened(t *testing.T) {
	err := notReaderError(readersFile{}, nil, fmt.Errorf("wrong passphrase, or the identity file is damaged"))
	if !strings.Contains(err.Error(), "wrong passphrase") || strings.Contains(err.Error(), "Ask one of them") {
		t.Errorf("got %v", err)
	}
}
