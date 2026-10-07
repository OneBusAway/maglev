// Package servicedate models a GTFS service date.
package servicedate

import (
	"fmt"
	"time"
)

// Date is a service date's year, month and day, like gtfs-modules' ServiceDate.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// New returns the service date for year, month and day, normalized like time.Date.
func New(year int, month time.Month, day int) Date {
	return Of(time.Date(year, month, day, 12, 0, 0, 0, time.UTC))
}

// Of returns the calendar date t falls on in t's own location.
func Of(t time.Time) Date {
	year, month, day := t.Date()
	return Date{Year: year, Month: month, Day: day}
}

// FromInstant returns the service date t falls on in loc, counting the next day's Start as that day.
func FromInstant(t time.Time, loc *time.Location) Date {
	date := Of(t.In(loc))
	if next := date.AddDays(1); next.Start(loc).Equal(t) {
		return next
	}
	return date
}

// Start is noon less twelve hours, the base for stop-time offsets. Never read a date from it.
func (d Date) Start(loc *time.Location) time.Time {
	return time.Date(d.Year, d.Month, d.Day, 12, 0, 0, 0, loc).Add(-12 * time.Hour)
}

// Midnight is local midnight on d, the serviceDate value Maglev reports.
func (d Date) Midnight(loc *time.Location) time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, loc)
}

// AddDays returns the service date n days after d.
func (d Date) AddDays(n int) Date {
	return New(d.Year, d.Month, d.Day+n)
}

// Weekday returns the day of the week d falls on.
func (d Date) Weekday() time.Weekday {
	return time.Date(d.Year, d.Month, d.Day, 12, 0, 0, 0, time.UTC).Weekday()
}

// String returns d as YYYYMMDD.
func (d Date) String() string {
	return fmt.Sprintf("%04d%02d%02d", d.Year, int(d.Month), d.Day)
}
