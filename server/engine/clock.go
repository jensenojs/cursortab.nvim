package engine

import "time"

// Timer represents a timer that can be stopped.
// This interface allows for mock implementations in tests.
type Timer interface {
	Stop() bool
}

// Clock provides time-related operations.
// This interface enables dependency injection for testing timer behavior.
type Clock interface {
	AfterFunc(d time.Duration, f func()) Timer
	Now() time.Time
}

// SystemClock is the default Clock implementation using the standard library.
var SystemClock Clock = systemClock{}

type systemClock struct{}

func (systemClock) AfterFunc(d time.Duration, f func()) Timer {
	return time.AfterFunc(d, f)
}

func (systemClock) Now() time.Time {
	return time.Now()
}
