// Package clock mengabstraksi waktu supaya logika bisnis mudah di-test.
package clock

import "time"

type Clock interface {
	Now() time.Time
}

type System struct{}

func (System) Now() time.Time { return time.Now().UTC() }
