package state

import "time"

// SetLockClock replaces the clock of the locks and the random part of their
// pauses, and returns the function that puts them back.
func SetLockClock(clock func() time.Time, jitter func(time.Duration) time.Duration) func() {
	c, j := lockClock, lockJitter
	lockClock, lockJitter = clock, jitter
	return func() { lockClock, lockJitter = c, j }
}

// OutputsLockStale is the time after which the lock on the outputs is reclaimed.
func OutputsLockStale() time.Duration { return outputsLockStale }
