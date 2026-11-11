package state

// The kinds of object that make up a state, in one list. Copy takes its steps from it and
// holdsNothing asks it which kinds count as content, so a kind that is added here is
// copied and counted, and one that is not here is neither: there was a list in each of the
// two, and the lists did not agree.

// kindCopy says how Copy carries a kind from one backend to another.
type kindCopy int

const (
	// copyPlain puts each object over whatever the destination holds.
	copyPlain kindCopy = iota
	// copyPlainOptional is copyPlain where an object that went since the listing is not
	// part of the copy: a stored plan is removed on its own schedule by a collection, as is a
	// record that was superseded, and a discovery object that someone withdrew.
	copyPlainOptional
	// copyHistory is a signed history of versions that is never rewritten: a version that the
	// destination holds with other bytes, and one that the source lacks, are refused.
	copyHistory
	// copySecretVersions is the versions of the secrets, which are merged by transaction.
	copySecretVersions
)

type stateKind struct {
	prefix string
	copy   kindCopy
	// content is true for a kind whose objects mean that the state has been written to. A
	// state that holds none is what a store that was opened and never written holds.
	content bool
}

// stateKinds is in the order of the copy: the records come before the pointers that name
// them, and the histories after what they judge.
var stateKinds = []stateKind{
	{"rec", copyPlainOptional, true},
	{"resources", copyPlain, true},
	{"txn", copyPlain, false},
	{KindDir, copyPlain, false},
	{"plans", copyPlainOptional, true},
	{DiscoveryRunDir, copyPlainOptional, true},
	{RunDir, copyPlainOptional, true},
	{PromotionDir, copyPlainOptional, true},
	{policyDir, copyHistory, true},
	{rolesDir, copyHistory, true},
	{environmentDir, copyHistory, true},
	{secretDir, copySecretVersions, true},
}
