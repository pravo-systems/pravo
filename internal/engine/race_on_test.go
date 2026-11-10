//go:build race

package engine

// raceOn says that the tests run with the race detector, which makes an
// evaluation of CUE several times as slow and finds nothing in one: an
// evaluation is the work of one goroutine.
const raceOn = true
