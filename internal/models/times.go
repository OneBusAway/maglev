package models

import (
	"strconv"
	"time"
)

// ModelTime wraps time.Time and serializes to/from JSON as Unix milliseconds.
// The zero time value time.Time is serialized as 0, although that's technically
// incorrect as Unix and golang use different epochs.
type ModelTime struct {
	time.Time
}

func NewModelTime(t time.Time) ModelTime {
	return ModelTime{Time: t}
}

func (t ModelTime) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte("0"), nil
	}
	return strconv.AppendInt(nil, t.UnixMilli(), 10), nil
}

func (t *ModelTime) UnmarshalJSON(data []byte) error {
	ms, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		return err
	}
	if ms == 0 {
		t.Time = time.Time{}
		return nil
	}
	t.Time = time.UnixMilli(ms)
	return nil
}

// ModelDuration wraps time.Duration and serializes to/from JSON as seconds.
type ModelDuration struct {
	time.Duration
}

func NewModelDuration(d time.Duration) ModelDuration {
	return ModelDuration{Duration: d}
}

func (d ModelDuration) MarshalJSON() ([]byte, error) {
	return strconv.AppendInt(nil, int64(d.Duration/time.Second), 10), nil
}

func (d *ModelDuration) UnmarshalJSON(data []byte) error {
	s, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		return err
	}
	d.Duration = time.Duration(s) * time.Second
	return nil
}
