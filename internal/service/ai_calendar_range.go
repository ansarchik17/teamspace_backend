package service

import (
	"fmt"
	"net/url"
	"time"
)

// Calendar dates denote inclusive local days; RFC3339 timestamps retain the
// API's exclusive upper bound. This is an AI adapter contract, not an API change.
func normalizeAICalendarRange(params url.Values) error {
	zone := params.Get("timezone")
	if zone == "" {
		zone = "Asia/Almaty"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return fmt.Errorf("invalid calendar timezone")
	}
	parse := func(raw string, lastDay bool) (time.Time, error) {
		if len(raw) == len("2006-01-02") {
			day, err := time.ParseInLocation("2006-01-02", raw, loc)
			if err != nil {
				return time.Time{}, err
			}
			if lastDay {
				day = day.AddDate(0, 0, 1)
			}
			return day, nil
		}
		return time.Parse(time.RFC3339, raw)
	}
	from, err := parse(params.Get("from"), false)
	if err != nil {
		return fmt.Errorf("calendar from must be YYYY-MM-DD or RFC3339")
	}
	to, err := parse(params.Get("to"), true)
	if err != nil {
		return fmt.Errorf("calendar to must be YYYY-MM-DD or RFC3339")
	}
	if !to.After(from) {
		return fmt.Errorf("calendar to must be after from; dates include the whole final day")
	}
	params.Set("from", from.Format(time.RFC3339Nano))
	params.Set("to", to.Format(time.RFC3339Nano))
	return nil
}
