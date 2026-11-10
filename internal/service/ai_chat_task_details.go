package service

import (
	"strings"
	"time"
	"unicode"
)

func chatTaskRequiredFields(args map[string]any) error {
	missing := []string{}
	for _, field := range []struct{ key, label string }{{"title", "название"}, {"priority", "приоритет (низкий, средний, высокий или критический)"}, {"due_at", "срок выполнения (дата)"}} {
		text, _ := args[field.key].(string)
		if strings.TrimSpace(text) == "" {
			missing = append(missing, field.label)
		}
	}
	ids, _ := args["assignee_ids"].([]any)
	if len(ids) == 0 {
		missing = append(missing, "исполнителя")
	}
	if len(missing) > 0 {
		return &chatActionDetailsRequired{missing}
	}
	due, err := parseChatTaskDue(args["due_at"].(string), time.Now())
	if err != nil {
		return &chatActionDetailsRequired{[]string{"дату срока выполнения"}}
	}
	args["due_at"] = due.Format(time.RFC3339)
	for _, id := range ids {
		n, ok := id.(float64)
		if !ok || n <= 0 || n > 9007199254740991 {
			return &chatActionDetailsRequired{[]string{"исполнителя из доступного списка пользователей"}}
		}
	}
	return nil
}

func chatTaskUserFields(args map[string]any, userText string, now time.Time) error {
	text := normalizedActionText(userText)
	missing := []string{}
	rawTitle, _ := args["title"].(string)
	title := normalizedActionText(rawTitle)
	if title == "" || !chatActionTitleInUserText(title, userText) {
		missing = append(missing, "название задачи вашими словами")
	}
	priority, _ := args["priority"].(string)
	if !chatTaskPriorityInUserText(userText, priority) {
		missing = append(missing, "приоритет (низкий, средний, высокий или критический)")
	}
	rawDue, _ := args["due_at"].(string)
	due, dueErr := parseChatTaskDue(rawDue, now)
	if dueErr != nil || !chatTaskDateInUserText(text, due, now) {
		missing = append(missing, "срок выполнения (дату)")
	}
	if dueErr == nil && chatTaskDateInUserText(text, due, now) {
		sourceText := chatTaskDeadlineSourceText(userText, due, now)
		if !chatActionClockInUserText(sourceText, due) {
			if chatTaskExplicitClock.MatchString(strings.ToLower(sourceText)) {
				missing = append(missing, "время срока выполнения из вашего сообщения")
			} else {
				due = chatTaskEndOfDay(due.In(time.FixedZone("Asia/Almaty", 5*60*60)))
			}
		}
		args["due_at"] = due.Format(time.RFC3339)
	}
	if ids, _ := args["assignee_ids"].([]any); len(ids) == 0 {
		missing = append(missing, "исполнителя")
	}
	if len(missing) > 0 {
		return &chatActionDetailsRequired{missing}
	}
	return nil
}

func chatTaskPriorityInUserText(text, expected string) bool {
	stems := map[string][]string{
		"low": {"low", "низк", "төмен"}, "medium": {"medium", "средн", "обычн", "орта"},
		"high": {"high", "высок", "срочн", "жоғары"}, "critical": {"critical", "критич", "шұғыл"},
	}
	lines := strings.Split(strings.ToLower(text), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		words := strings.FieldsFunc(lines[index], func(r rune) bool { return !unicode.IsLetter(r) })
		labelled := false
		for _, word := range words {
			if strings.HasPrefix(word, "приоритет") || word == "priority" || strings.HasPrefix(word, "басымдық") {
				labelled = true
			}
		}
		// A bare follow-up answer or a comma-separated field value is explicit;
		// words appearing inside a task title or directory record are not.
		selected := map[string]bool{}
		for position, word := range words {
			if position > 0 && (words[position-1] == "не" || words[position-1] == "not") {
				continue
			}
			for priority, aliases := range stems {
				for _, alias := range aliases {
					if word != alias && !(len([]rune(alias)) >= 4 && strings.HasPrefix(word, alias)) {
						continue
					}
					plainValue := false
					for _, field := range strings.FieldsFunc(lines[index], func(r rune) bool { return r == ',' || r == ';' }) {
						if strings.Trim(strings.TrimSpace(field), ".!?\"'«»") == word {
							plainValue = true
						}
					}
					if labelled || plainValue {
						selected[priority] = true
					}
				}
			}
		}
		if len(selected) > 0 {
			return len(selected) == 1 && selected[expected]
		}
	}
	return false
}
