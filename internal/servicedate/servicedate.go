// Package servicedate answers when a GTFS service day begins.
package servicedate

import "time"

// Start returns the instant the service day begins in loc: noon on that date less
// twelve hours. On a day with no DST transition that is local midnight, and on the two
// transition days it sits an hour either side of it. GTFS measures stop_times from that
// instant, and Java OBA's ServiceDate builds it the same way, so a stop time of
// 08:00:00 lands at 08:00 local on every day of the year.
func Start(year int, month time.Month, day int, loc *time.Location) time.Time {
	return time.Date(year, month, day, 12, 0, 0, 0, loc).Add(-12 * time.Hour)
}

// StartOf returns the start of the service day that t falls on, in t's own location.
func StartOf(t time.Time) time.Time {
	year, month, day := t.Date()
	return Start(year, month, day, t.Location())
}
