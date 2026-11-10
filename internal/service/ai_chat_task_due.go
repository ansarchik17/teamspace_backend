package service

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var chatTaskWeekEnd = regexp.MustCompile(`(?i)(?:конец|конца|концу)\s+(?:этой\s+|текущей\s+)?недели|\bend of (?:this |the )?week\b`)

func chatTaskEndOfDay(day time.Time) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), 23, 59, 59, 0, day.Location())
}

func chatTaskWeekDeadline(now time.Time) time.Time {
	now = now.In(time.FixedZone("Asia/Almaty", 5*60*60))
	return chatTaskEndOfDay(now.AddDate(0, 0, (7-int(now.Weekday()))%7))
}

func parseChatTaskDue(raw string, now time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if due, err := time.Parse(time.RFC3339, raw); err == nil {
		return due, nil
	}
	zone := time.FixedZone("Asia/Almaty", 5*60*60)
	for _, format := range []string{"2006-01-02", "02.01.2006", "2.1.2006", "02/01/2006"} {
		if due, err := time.ParseInLocation(format, raw, zone); err == nil {
			return chatTaskEndOfDay(due), nil
		}
	}
	for _, phrase := range []string{"конец недели", "до конца недели", "к концу недели", "конец этой недели", "конец текущей недели", "end of week", "end of this week"} {
		if strings.EqualFold(raw, phrase) {
			return chatTaskWeekDeadline(now), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid task deadline")
}

// Task deadlines can be dates. Exact times remain optional; when no matching
// clock was supplied by the user, show the explicit end-of-day policy in preview.
func chatTaskDateInUserText(text string, due, now time.Time) bool {
	if chatActionDateInUserText(text, due, now) {
		return true
	}
	return chatTaskWeekEnd.MatchString(text) && due.In(time.FixedZone("Asia/Almaty", 5*60*60)).Format("2006-01-02") == chatTaskWeekDeadline(now).Format("2006-01-02")
}

var chatTaskExplicitClock = regexp.MustCompile(`(?:[0-9]{1,2}:[0-9]{2})|(?:^|\s)(?:в|к|до|at)\s+[0-9]{1,2}(?:\s|$)`)

func chatTaskDeadlineSourceText(userText string, due, now time.Time) string {
	lines := strings.Split(userText, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if chatTaskDateInUserText(strings.ToLower(lines[i]), due, now) {
			return strings.Join(lines[i:], "\n")
		}
	}
	return userText
}
