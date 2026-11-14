package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/west6ide/workspace-backend/internal/alem"
)

type chatActionField struct{ key, label string }
type chatActionContract struct{ fields []chatActionField }

// One registry drives both the model schema and the server's admission checks.
// A new write tool without a contract must never acquire a confirmation token.
var mainChatActionContracts = map[string]chatActionContract{
	"create_kanban_task":    {[]chatActionField{{"title", "название задачи"}, {"priority", "приоритет"}, {"due_at", "срок"}, {"assignee_ids", "исполнителя"}}},
	"create_calendar_event": {[]chatActionField{{"title", "название встречи"}, {"start_time", "дату и время начала"}, {"end_time", "время окончания или длительность"}, {"attendee_ids", "участников (или укажите, что это личное событие)"}}},
	"create_online_meeting": {[]chatActionField{{"meeting_name", "название видеовстречи"}, {"from_date", "дату и время начала"}, {"to_date", "время окончания или длительность"}, {"attendee_ids", "участников"}}},
	"set_event_reminder":    {[]chatActionField{{"event_id", "конкретную встречу"}, {"minutes_before", "за сколько минут напомнить"}}},
	"cancel_calendar_event": {[]chatActionField{{"event_id", "какую встречу отменить"}}},
	"close_kanban_task":     {[]chatActionField{{"task_id", "какую задачу закрыть"}}},
	"prepare_email_draft":   {[]chatActionField{{"recipient", "кому адресовано письмо"}, {"subject", "тему письма"}, {"body", "что написать в письме"}}},
}

func preflightChatActionBatch(calls []alem.ToolCall, schemas []alem.FunctionTool, userText string) (map[string]string, error) {
	prepared := map[string]string{}
	seen := map[string]bool{}
	for _, call := range calls {
		if call.ID == "" || seen[call.ID] || call.Type != "function" {
			return nil, fmt.Errorf("Не удалось однозначно подготовить действия. Уточните запрос.")
		}
		seen[call.ID] = true
		if _, action := mainChatActionContracts[call.Function.Name]; !action {
			continue
		}
		args, err := validateChatToolArguments(call.Function.Name, call.Function.Arguments, schemas)
		if err != nil {
			var details *chatActionDetailsRequired
			if errors.As(err, &details) {
				var partial map[string]any
				_ = json.Unmarshal([]byte(call.Function.Arguments), &partial)
				if sourceErr := chatActionUserFields(call.Function.Name, partial, userText, time.Now()); sourceErr != nil {
					return nil, sourceErr
				}
			}
			return nil, err
		}
		if err := chatActionUserFields(call.Function.Name, args, userText, time.Now()); err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		prepared[call.ID] = string(encoded)
	}
	return prepared, nil
}

func chatActionRequiredFields(name string, args map[string]any) error {
	contract, known := mainChatActionContracts[name]
	if !known {
		return fmt.Errorf("действие не поддерживается")
	}
	missing := []string{}
	for _, field := range contract.fields {
		value, exists := args[field.key]
		if !exists || value == nil {
			missing = append(missing, field.label)
			continue
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
			missing = append(missing, field.label)
		}
		if ids, ok := value.([]any); ok {
			// An explicit empty participant list is allowed only for a personal
			// calendar event; the intent check below still requires that choice.
			if len(ids) == 0 && !(field.key == "attendee_ids" && name == "create_calendar_event") {
				missing = append(missing, field.label)
			}
			for _, id := range ids {
				n, ok := id.(float64)
				if !ok || n <= 0 || n > 9007199254740991 {
					missing = append(missing, field.label)
					break
				}
			}
		}
	}
	if len(missing) > 0 {
		return &chatActionDetailsRequired{missing}
	}
	if name == "create_kanban_task" {
		return chatTaskRequiredFields(args)
	}
	if name == "create_calendar_event" || name == "create_online_meeting" {
		startKey, endKey := "start_time", "end_time"
		if name == "create_online_meeting" {
			startKey, endKey = "from_date", "to_date"
		}
		start, firstErr := time.Parse(time.RFC3339, actionString(args, startKey))
		end, lastErr := time.Parse(time.RFC3339, actionString(args, endKey))
		if firstErr != nil || lastErr != nil || !end.After(start) {
			return &chatActionDetailsRequired{[]string{"дату, время начала и окончания встречи"}}
		}
	}
	if minutes, ok := args["minutes_before"].(float64); ok && (minutes < 0 || minutes > 525600) {
		return &chatActionDetailsRequired{[]string{"за сколько минут напомнить"}}
	}
	return nil
}

func actionString(args map[string]any, field string) string {
	text, _ := args[field].(string)
	return strings.TrimSpace(text)
}

func chatActionPartyField(name string) (chatActionField, bool) {
	switch name {
	case "create_kanban_task":
		return chatActionField{"assignee_ids", "исполнителя"}, true
	case "create_calendar_event", "create_online_meeting":
		return chatActionField{"attendee_ids", "участников встречи"}, true
	}
	return chatActionField{}, false
}

func actionContentInUserText(value, text string) bool {
	normalize := func(value string) string {
		return strings.ToLower(strings.TrimSpace(strings.Trim(value, " \n\t.!?:;\"'«»")))
	}
	value, text = normalize(value), normalize(text)
	return value != "" && strings.Contains(text, value)
}

var genericChatActionTitle = regexp.MustCompile(`^(?:(?:создай|сделай|запланируй|назначь|create|schedule)\s+(?:a\s+)?(?:задач[ау]|встреч[ау]|митинг|task|meeting)(?:\s.*)?|(?:задача|встреча|митинг|task|meeting|event)(?:\s+(?:с|для|for|with)\s+.+)?)$`)

func chatActionTitleInUserText(value, text string) bool {
	if !actionContentInUserText(value, text) {
		return false
	}
	value = strings.Trim(normalizedActionText(value), " .!?\"'«»")
	if !genericChatActionTitle.MatchString(value) {
		return true
	}
	text = normalizedActionText(text)
	if strings.Contains(text, "«"+value+"»") || strings.Contains(text, "\""+value+"\"") {
		return true
	}
	return regexp.MustCompile(`(?:название|назови|назовём|назовем|title)\s*[:—-]?\s*[«"']?` + regexp.QuoteMeta(value) + `(?:$|[^\p{L}])`).MatchString(text)
}

func chatActionUserFields(name string, args map[string]any, userText string, now time.Time) error {
	// Optional prose also needs user input; omitting it is preferable to
	// inventing commitments that would be sent to the underlying service.
	if description := actionString(args, "description"); description != "" && !actionContentInUserText(description, userText) {
		delete(args, "description")
	}
	if name == "create_kanban_task" {
		if err := chatTaskUserFields(args, userText, now); err != nil {
			return err
		}
	}
	missing := []string{}
	checkText := func(field, label string) {
		if !actionContentInUserText(actionString(args, field), userText) {
			missing = append(missing, label)
		}
	}
	switch name {
	case "prepare_email_draft":
		checkText("recipient", "кому адресовано письмо")
		brief := actionString(args, "content_brief")
		canCompose := chatEmailBriefSupplied(brief, actionString(args, "recipient"), userText)
		if !canCompose {
			checkText("subject", "тему письма")
			checkText("body", "что написать в письме")
		}
	case "create_calendar_event", "create_online_meeting":
		title, startKey, endKey := "title", "start_time", "end_time"
		if name == "create_online_meeting" {
			title, startKey, endKey = "meeting_name", "from_date", "to_date"
		}
		if !chatActionTitleInUserText(actionString(args, title), userText) {
			missing = append(missing, "название встречи")
		}
		start, startErr := time.Parse(time.RFC3339, actionString(args, startKey))
		end, endErr := time.Parse(time.RFC3339, actionString(args, endKey))
		if startErr != nil || !chatActionDateInUserText(strings.ToLower(userText), start, now) {
			missing = append(missing, "дату встречи")
		}
		if startErr != nil || !chatActionClockInUserText(userText, start) {
			missing = append(missing, "время начала встречи")
		}
		if endErr != nil || (!chatActionClockInUserText(userText, end) && !chatActionDurationInUserText(userText, end.Sub(start))) {
			missing = append(missing, "время окончания или длительность встречи")
		}
		zone := time.FixedZone("Asia/Almaty", 5*3600)
		if startErr == nil && endErr == nil && start.In(zone).Format("2006-01-02") != end.In(zone).Format("2006-01-02") && !chatActionDateInUserText(strings.ToLower(userText), end, now) {
			missing = append(missing, "дату окончания встречи")
		}
		if ids, _ := args["attendee_ids"].([]any); len(ids) == 0 {
			personal := name == "create_calendar_event" && (strings.Contains(strings.ToLower(userText), "личное") || strings.Contains(strings.ToLower(userText), "личная") || strings.Contains(strings.ToLower(userText), "без участников") || strings.Contains(strings.ToLower(userText), "personal event"))
			if !personal {
				missing = append(missing, "участников встречи")
			}
		}
	case "set_event_reminder":
		if !chatActionTargetInUserText(args, "event_id", "event_query", userText) {
			missing = append(missing, "к какой встрече относится напоминание")
		}
		minutes, ok := args["minutes_before"].(float64)
		before := regexp.MustCompile(`(?:^|[^\p{L}])за\s*|\bbefore\b|бұрын`).MatchString(strings.ToLower(userText))
		atStart := ok && minutes == 0 && strings.Contains(strings.ToLower(userText), "в начале")
		if !atStart && (!ok || !before || !chatActionDurationInUserText(userText, time.Duration(minutes)*time.Minute)) {
			missing = append(missing, "за сколько минут напомнить")
		}
	case "cancel_calendar_event":
		if !chatActionTargetInUserText(args, "event_id", "event_query", userText) {
			missing = append(missing, "какую встречу отменить")
		}
	case "close_kanban_task":
		if !chatActionTargetInUserText(args, "task_id", "task_query", userText) {
			missing = append(missing, "какую задачу закрыть")
		}
	}
	if field, hasParty := chatActionPartyField(name); hasParty {
		if ids, _ := args[field.key].([]any); len(ids) > 0 {
			query := actionString(args, "participant_query")
			explicit := actionContentInUserText(query, userText)
			for _, vague := range []string{"ему", "ей", "его", "ее", "её", "кому", "сотрудник", "получатель", "участники"} {
				if strings.EqualFold(query, vague) {
					explicit = false
				}
			}
			if !explicit {
				missing = append(missing, field.label)
			}
		}
	}
	if len(missing) > 0 {
		return &chatActionDetailsRequired{missing}
	}
	return nil
}

func chatEmailRequested(text string) bool {
	email := false
	for _, line := range strings.Split(strings.ToLower(text), "\n") {
		if strings.Contains(line, "письм") || strings.Contains(line, "email") || strings.Contains(line, "e-mail") {
			email = true
		}
		if strings.Contains(line, "отправь в чат") || strings.Contains(line, "напиши в чат") || strings.Contains(line, "в мессенджере") || strings.Contains(line, "в мессенджер") {
			email = false
		}
	}
	return email
}

func chatActionTargetInUserText(args map[string]any, idKey, queryKey, userText string) bool {
	if id := actionString(args, idKey); id != "" && strings.Contains(userText, id) {
		return true
	}
	query := actionString(args, queryKey)
	for _, generic := range []string{"встреча", "встречу", "задача", "задачу", "это", "эту", "meeting", "task"} {
		if strings.EqualFold(query, generic) {
			return false
		}
	}
	return actionContentInUserText(query, userText)
}

func chatActionClockInUserText(text string, value time.Time) bool {
	text = strings.ToLower(text)
	if strings.Contains(text, strings.ToLower(value.Format(time.RFC3339))) {
		return true
	}
	value = value.In(time.FixedZone("Asia/Almaty", 5*3600))
	// A clock time must be supplied, rather than inferred from a bare day.
	clock := regexp.MustCompile(`(?:^|[^0-9])` + regexp.QuoteMeta(value.Format("15:04")) + `(?:$|[^0-9])`)
	if clock.MatchString(text) {
		return true
	}
	if value.Minute() == 0 {
		return regexp.MustCompile(`(?:^|\s)(?:в|с|до|at)\s+0?` + strconv.Itoa(value.Hour()) + `(?:$|[\s,.!?])`).MatchString(text)
	}
	return false
}

func chatActionDurationInUserText(text string, duration time.Duration) bool {
	if duration < 0 {
		return false
	}
	minutes := int64(duration / time.Minute)
	text = strings.ToLower(text)
	if regexp.MustCompile(`(?:^|[^0-9])` + strconv.FormatInt(minutes, 10) + `\s*(?:минут|мин\b|minutes\b)`).MatchString(text) {
		return true
	}
	if duration == time.Hour && (strings.Contains(text, "на час") || strings.Contains(text, "за час") || strings.Contains(text, "один час")) {
		return true
	}
	if duration == 30*time.Minute && (strings.Contains(text, "полчаса") || strings.Contains(text, "полчас")) {
		return true
	}
	if minutes > 0 && minutes%60 == 0 {
		return regexp.MustCompile(`(?:^|[^0-9])` + strconv.FormatInt(minutes/60, 10) + `\s*(?:час|hours\b)`).MatchString(text)
	}
	return false
}

func chatEmailBriefSupplied(brief, recipient, userText string) bool {
	if !actionContentInUserText(brief, userText) {
		return false
	}
	words := func(text string) []string {
		return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	}
	stop := map[string]bool{}
	for _, word := range strings.Fields("напиши напишите написать письмо письма email mail составь составьте отправь отправить для кому про о об с со пожалуйста ему ей получателю текст тему нужно") {
		stop[word] = true
	}
	recipients := words(recipient)
	for _, word := range words(brief) {
		if stop[word] || len([]rune(word)) < 2 {
			continue
		}
		isRecipient := false
		for _, name := range recipients {
			if strings.HasPrefix(word, name) {
				isRecipient = true
			}
		}
		if !isRecipient {
			return true
		}
	}
	return false
}
