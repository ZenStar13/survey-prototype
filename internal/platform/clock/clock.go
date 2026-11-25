// Package clock abstracts time so application services stay deterministic in tests.
package clock

import "time"

type Clock interface {
	Now() time.Time
}

// System is the real clock, always in UTC.
type System struct{}

func (System) Now() time.Time { return time.Now().UTC() }

// Func adapts a function to Clock (handy in tests).
type Func func() time.Time

func (f Func) Now() time.Time { return f() }
