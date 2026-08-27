package tui

import "time"

// stepClock is shared by deterministic keyboard and pointer admission tests.
type stepClock struct{ at time.Time }

func (c *stepClock) now() time.Time { return c.at }

func (c *stepClock) advance(duration time.Duration) { c.at = c.at.Add(duration) }

