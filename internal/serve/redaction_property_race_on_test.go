//go:build race

package serve

// rpDefaultSeeds is how many seeds the property test runs when RP_SEEDS does not say: the race detector makes each estate
// several times dearer, and the test stays under 20 seconds.
const rpDefaultSeeds = 60
