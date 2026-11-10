package service

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/west6ide/workspace-backend/internal/alem"
)

type chatActionDetailsRequired struct{ fields []string }

var chatActionRussianDate = regexp.MustCompile(`(?:^|[^0-9])([0-9]{1,2})\s+(января|февраля|марта|апреля|мая|июня|июля|августа|сентября|октября|ноября|декабря)(?:\s+([0-9]{4}))?`)

func (e *chatActionDetailsRequired) Error() string {
	return "Чтобы подготовить действие, уточните: " + strings.Join(e.fields, ", ") + ". После этого продолжу подготовку."
}

// Capture only actual user turns before any model/tool output is appended.
// Assistant suggestions, directory records and document instructions cannot
// supply action parameters on the user's behalf.
func chatActionUserText(messages []alem.Message) string {
	var turns []string
	for _, message := range messages {
		if message.Role != "user" {
			continue
		}
		if text, ok := message.Content.(string); ok {
			turns = append(turns, text)
		} else if parts, ok := message.Content.([]alem.ContentPart); ok {
			for _, part := range parts {
				if part.Type == "text" {
					turns = append(turns, part.Text)
				}
			}
		}
	}
	return strings.Join(turns, "\n")
}

func normalizedActionText(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

func chatActionDateInUserText(text string, due, now time.Time) bool {
	if strings.Contains(text, strings.ToLower(due.Format(time.RFC3339))) {
		return true
	}
	zone := time.FixedZone("Asia/Almaty", 5*60*60)
	due, now = due.In(zone), now.In(zone)
	for _, format := range []string{"2006-01-02", "02.01.2006", "2.1.2006", "02/01/2006"} {
		if strings.Contains(text, due.Format(format)) {
			return true
		}
	}
	months := []string{"января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"}
	for _, match := range chatActionRussianDate.FindAllStringSubmatch(text, -1) {
		day, _ := strconv.Atoi(match[1])
		year := now.Year()
		if match[3] != "" {
			year, _ = strconv.Atoi(match[3])
		}
		if day == due.Day() && match[2] == months[int(due.Month())-1] && year == due.Year() {
			return true
		}
	}
	for _, word := range strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) }) {
		for token, days := range map[string]int{"сегодня": 0, "завтра": 1, "послезавтра": 2, "today": 0, "tomorrow": 1, "бүгін": 0, "ертең": 1} {
			if word == token && due.Format("2006-01-02") == now.AddDate(0, 0, days).Format("2006-01-02") {
				return true
			}
		}
	}
	return false
}
