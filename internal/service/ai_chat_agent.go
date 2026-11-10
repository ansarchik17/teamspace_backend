package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// Main chat shares the agent's domain services, while retaining its persistent
// conversation, attachment readers, provenance and accounted completion client.
type aiActionDecisionKey struct{}
type aiActionDecision struct {
	Token   string
	Confirm bool
}

func WithAIActionDecision(ctx context.Context, token string, confirm bool) context.Context {
	return context.WithValue(ctx, aiActionDecisionKey{}, aiActionDecision{token, confirm})
}

type aiPendingChatAction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type aiChatActionStore interface {
	Put(context.Context, string, string, time.Duration) error
	Take(context.Context, string) (string, error)
}
type redisChatActionStore struct{ client *redis.Client }

func (s redisChatActionStore) Put(ctx context.Context, key, value string, ttl time.Duration) error {
	return s.client.Set(ctx, key, value, ttl).Err()
}
func (s redisChatActionStore) Take(ctx context.Context, key string) (string, error) {
	return s.client.GetDel(ctx, key).Result()
}

var aiChatWriteTitles = map[string]string{
	"create_calendar_event": "Создать встречу", "set_event_reminder": "Установить напоминание",
	"cancel_calendar_event": "Отменить встречу", "create_kanban_task": "Создать задачу",
	"close_kanban_task": "Закрыть задачу", "send_broadcast": "Отправить сообщение",
	"create_online_meeting": "Создать онлайн-встречу",
}

func (s *SmartAgentService) mainChatTools() []alem.FunctionTool {
	if s == nil {
		return nil
	}
	tools := mainChatToolSchemas()
	if s.fileSearch == nil {
		filtered := tools[:0]
		for _, tool := range tools {
			if tool.Function.Name != "search_file_content" {
				filtered = append(filtered, tool)
			}
		}
		tools = filtered
	}
	return tools
}

func mainChatToolSchemas() []alem.FunctionTool {
	tools := []LLMTool{}
	for _, tool := range getToolsSchema() {
		name := tool.Function.Name
		if name == "web_search" || name == "search_file_content" || aiChatWriteTitles[name] != "" {
			if contract, ok := mainChatActionContracts[name]; ok {
				required := make([]string, 0, len(contract.fields))
				for _, field := range contract.fields {
					required = append(required, field.key)
				}
				tool.Function.Parameters["required"] = required
				if name == "create_kanban_task" {
					properties := tool.Function.Parameters["properties"].(map[string]interface{})
					properties["due_at"] = map[string]string{"type": "string", "description": "Срок задачи: достаточно даты YYYY-MM-DD, ДД.ММ.ГГГГ или конец недели. Дата без времени означает 23:59:59 Asia/Almaty; конец недели — это воскресенье до конца дня. RFC3339 используй, если пользователь задал время. Не требуй ISO 8601 или время от пользователя. Покажи точный вычисленный срок в подтверждении."}
				}
				if chatTargetResolutionTool(name) {
					properties := tool.Function.Parameters["properties"].(map[string]interface{})
					queryKey := "event_query"
					if name == "close_kanban_task" {
						queryKey = "task_query"
					}
					properties[queryKey] = map[string]string{"type": "string", "description": "Точная фраза выбора объекта из сообщения пользователя, например завтра в 10 утра или email участника. Передавай вместе с проверенным ID, даже когда ID известен; не требуй от пользователя повторять полное название или ID."}
				}
				if chatTargetResolutionTool(name) {
					tool.Function.Description += " Найди ID через read_user_data по описанию пользователя. Для выбора достаточно однозначной даты/времени, участника или названия; не требуй технический ID у пользователя. Передай его фразу выбора вместе с найденным ID. Уточняй только неоднозначный выбор или недостающие пользовательские параметры."
				} else {
					tool.Function.Description += " Сначала уточни все обязательные поля у пользователя. Не придумывай значения и не используй значения по умолчанию без его выбора."
				}
			}
			tools = append(tools, tool)
		}
	}
	result := llmToolsToAlem(tools)
	result = append(result, alem.FunctionTool{Type: "function", Function: alem.FunctionDefinition{
		Name: "prepare_email_draft", Description: "Подготовить черновик email без отправки. Сначала уточни адресата, тему и что написать. Не придумывай содержание. Не заменяй email сообщением в мессенджере. Если данные неполны, передай известные поля для уточнения.",
		Parameters: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"recipient", "subject", "body"}, "properties": map[string]any{"recipient": map[string]string{"type": "string"}, "subject": map[string]string{"type": "string"}, "body": map[string]string{"type": "string"}, "content_brief": map[string]string{"type": "string", "description": "Точная цитата из сообщения пользователя о содержании письма. Позволяет сформулировать черновик по заданному содержанию. Сам запрос написать письмо адресату не является содержанием."}}},
	}})
	return result
}

func (s *SmartAgentService) chatActionStore() aiChatActionStore {
	if s.actionStore != nil {
		return s.actionStore
	}
	if s.redisClient != nil {
		return redisChatActionStore{s.redisClient}
	}
	return nil
}

func chatActionKey(ctx context.Context, identity AIDataIdentity, token string) (string, error) {
	if _, err := uuid.Parse(token); err != nil || identity.ChatID == "" {
		return "", fmt.Errorf("invalid action reference")
	}
	key, err := smartAgentSessionStorageKey(ctx, identity.UserID, identity.ChatID)
	return key + ":action:" + token, err
}

// Validate against the same schemas advertised to the model, including unknown
// fields and nested array item types. No model-selected URLs or actor IDs exist.
func validateChatToolArguments(name, raw string, tools []alem.FunctionTool) (map[string]any, error) {
	if len(raw) > 8192 {
		return nil, fmt.Errorf("слишком большие параметры")
	}
	var schema map[string]any
	for _, tool := range tools {
		if tool.Function.Name == name {
			schema = tool.Function.Parameters
			break
		}
	}
	if schema == nil {
		return nil, fmt.Errorf("инструмент недоступен")
	}
	var args map[string]any
	decoder := json.NewDecoder(strings.NewReader(raw))
	if decoder.Decode(&args) != nil || args == nil || decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("некорректные параметры")
	}
	// Normalize legacy map[string]string schema nodes to one JSON representation.
	encoded, _ := json.Marshal(schema)
	schema = nil
	_ = json.Unmarshal(encoded, &schema)
	if _, action := mainChatActionContracts[name]; action {
		// Missing values are a user clarification, not a broken model protocol.
		delete(schema, "required")
	}
	if err := validateChatToolValue(args, schema); err != nil {
		return nil, err
	}
	if _, action := mainChatActionContracts[name]; action {
		if err := chatActionRequiredFields(name, args); err != nil {
			return nil, err
		}
	}
	text := func(key string) string { value, _ := args[key].(string); return strings.TrimSpace(value) }
	for _, key := range []string{"event_id", "task_id", "board_id"} {
		if id := text(key); id != "" {
			if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil {
				return nil, fmt.Errorf("укажите корректный идентификатор из результатов поиска")
			}
		}
	}
	for _, key := range []string{"title", "meeting_name", "query", "question"} {
		if _, exists := args[key]; exists && text(key) == "" {
			return nil, fmt.Errorf("пустое название или поисковый запрос")
		}
	}
	switch name {
	case "cancel_calendar_event", "set_event_reminder":
		if text("event_id") == "" && text("event_query") == "" {
			return nil, fmt.Errorf("укажите конкретную встречу")
		}
	case "create_calendar_event", "create_online_meeting":
		startKey, endKey := "start_time", "end_time"
		if name == "create_online_meeting" {
			startKey, endKey = "from_date", "to_date"
		}
		start, firstErr := time.Parse(time.RFC3339, text(startKey))
		end, lastErr := time.Parse(time.RFC3339, text(endKey))
		if firstErr != nil || lastErr != nil || !end.After(start) {
			return nil, fmt.Errorf("укажите начало и окончание с часовым поясом; окончание должно быть позже начала")
		}
	case "send_broadcast":
		if recipients, _ := args["recipient_user_ids"].([]any); len(recipients) == 0 || text("message") == "" {
			return nil, fmt.Errorf("укажите сообщение и получателей")
		}
	}
	if minutes, ok := args["minutes_before"].(float64); ok && (minutes < 0 || minutes > 525600) {
		return nil, fmt.Errorf("некорректное время напоминания")
	}
	return args, nil
}

func validateChatToolValue(value any, schema map[string]any) error {
	invalid := fmt.Errorf("неверные или неполные параметры действия")
	switch schema["type"] {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return invalid
		}
		properties, _ := schema["properties"].(map[string]any)
		required, _ := schema["required"].([]any)
		for _, field := range required {
			if v, exists := object[fmt.Sprint(field)]; !exists || v == "" || v == nil {
				return invalid
			}
		}
		for key, val := range object {
			child, ok := properties[key].(map[string]any)
			if !ok {
				return invalid
			}
			if err := validateChatToolValue(val, child); err != nil {
				return err
			}
		}
	case "string":
		if _, ok := value.(string); !ok {
			return invalid
		}
	case "integer":
		if number, ok := value.(float64); !ok || math.Trunc(number) != number {
			return invalid
		}
	case "array":
		items, ok := value.([]any)
		if !ok || len(items) > 100 {
			return invalid
		}
		child, _ := schema["items"].(map[string]any)
		for _, item := range items {
			if err := validateChatToolValue(item, child); err != nil {
				return err
			}
		}
	}
	if values, ok := schema["enum"].([]any); ok {
		for _, candidate := range values {
			if candidate == value {
				return nil
			}
		}
		return invalid
	}
	return nil
}

func (s *SmartAgentService) runMainChatTool(ctx context.Context, identity AIDataIdentity, call alem.ToolCall, userTexts ...string) (AIDataResult, []alem.DataCard, bool) {
	if call.Function.Name == "send_broadcast" && chatEmailRequested(strings.Join(userTexts, "\n")) {
		return AIDataResult{Status: "email_draft_required", Message: "Пользователь просит email. Используй prepare_email_draft, уточни недостающие тему и содержание. Не отправляй сообщение в мессенджере вместо письма."}, nil, false
	}
	args, err := validateChatToolArguments(call.Function.Name, call.Function.Arguments, s.mainChatTools())
	if err != nil {
		var details *chatActionDetailsRequired
		if errors.As(err, &details) {
			var partial map[string]any
			_ = json.Unmarshal([]byte(call.Function.Arguments), &partial)
			if inputErr := chatActionUserFields(call.Function.Name, partial, strings.Join(userTexts, "\n"), time.Now()); inputErr != nil {
				return AIDataResult{Status: "clarification_required", Message: inputErr.Error()}, nil, false
			}
			if call.Function.Name == "prepare_email_draft" && chatEmailBriefSupplied(actionString(partial, "content_brief"), actionString(partial, "recipient"), strings.Join(userTexts, "\n")) {
				return AIDataResult{Status: "draft_content_required", Message: "Пользователь уже задал содержание письма. Сформулируй тему и текст черновика по его content_brief и вызови prepare_email_draft ещё раз с заполненными subject и body. Не проси пользователя повторять заданное содержание и не отправляй письмо."}, nil, false
			}
			return AIDataResult{Status: "clarification_required", Message: details.Error()}, nil, false
		}
		return AIDataResult{Status: "invalid_request", Message: err.Error()}, nil, false
	}
	name := call.Function.Name
	if _, action := mainChatActionContracts[name]; action {
		if err := chatActionUserFields(name, args, strings.Join(userTexts, "\n"), time.Now()); err != nil {
			return AIDataResult{Status: "clarification_required", Message: err.Error()}, nil, false
		}
	}
	if name == "prepare_email_draft" {
		card := alem.DataCard{ID: "email-draft-" + uuid.NewString(), SourceID: "email-draft", Title: "Черновик письма", Service: "Team AI", Status: "draft", Fields: []alem.DataCardField{{Label: "Кому", Value: actionString(args, "recipient")}, {Label: "Тема", Value: actionString(args, "subject")}, {Label: "Текст", Value: actionString(args, "body")}}}
		return AIDataResult{Status: "draft_prepared", Message: "Черновик письма подготовлен. Письмо не отправлено."}, []alem.DataCard{card}, false
	}
	if title := aiChatWriteTitles[name]; title != "" {
		if _, known := mainChatActionContracts[name]; !known {
			return AIDataResult{Status: "invalid_request", Message: "Действие не поддерживается."}, nil, false
		}
		store := s.chatActionStore()
		if store == nil {
			return AIDataResult{Status: "unavailable", Message: "Хранилище подтверждений недоступно. Действие не выполнено."}, nil, false
		}
		token := uuid.NewString()
		key, err := chatActionKey(ctx, identity, token)
		if err != nil {
			return AIDataResult{Status: "forbidden", Message: "Не удалось проверить владельца действия."}, nil, false
		}
		ttl := 5 * time.Minute
		if s.cfg != nil && s.cfg.Agent.ConfirmationTTL > 0 {
			ttl = s.cfg.Agent.ConfirmationTTL
		}
		fields := chatActionFields(args)
		if (name == "cancel_calendar_event" || name == "set_event_reminder") && s.calendarService != nil {
			eventID, parseErr := uuid.Parse(actionString(args, "event_id"))
			if parseErr != nil {
				return AIDataResult{Status: "invalid_request", Message: "Некорректный ID встречи. Действие не выполнено."}, nil, false
			}
			event, readErr := s.calendarService.GetEvent(ctx, identity.UserID, eventID)
			if readErr != nil || event == nil {
				return AIDataResult{Status: "unavailable", Message: "Не удалось проверить встречу. Действие не выполнено."}, nil, false
			}
			fields = chatCalendarActionFields(args, event)
		}
		validated, _ := json.Marshal(args)
		payload, _ := json.Marshal(aiPendingChatAction{Name: name, Arguments: string(validated)})
		if err := store.Put(ctx, key, string(payload), ttl); err != nil {
			return AIDataResult{Status: "unavailable", Message: "Не удалось подготовить подтверждение. Действие не выполнено."}, nil, false
		}
		card := alem.DataCard{ID: "action-" + token, SourceID: "agent-action", Title: title, Service: "Team AI", Status: "pending", Fields: fields, Confirmation: &alem.ActionConfirmation{Token: token, ExpiresAt: time.Now().Add(ttl).UTC().Format(time.RFC3339)}}
		return AIDataResult{Status: "confirmation_required", Message: "Проверьте параметры и подтвердите действие."}, []alem.DataCard{card}, true
	}
	if name == "web_search" {
		return s.searchMainChatWeb(ctx, identity, args["query"].(string))
	}
	if name == "search_file_content" {
		ids := []string{}
		if values, ok := args["file_ids"].([]any); ok {
			for _, value := range values {
				ids = append(ids, value.(string))
			}
		}
		result, err := s.fileSearch.Search(ctx, identity.UserID, domain.DriveSearchQueryRequest{Question: args["question"].(string), FileIDs: ids, Limit: 6, ChunksPerFile: 4})
		if err != nil || result == nil {
			return AIDataResult{Status: "unavailable", Message: "Поиск по содержимому файлов недоступен."}, nil, false
		}
		result, truncated := boundFileEvidence(result)
		if !fileEvidenceFitsEnvelope(result) {
			return fileEvidenceTooLarge(), nil, false
		}
		return AIDataResult{Status: "ok", Data: result, Truncated: truncated || result.Incomplete || result.Indexing}, mainChatFileCards(result), false
	}
	return AIDataResult{Status: "invalid_request"}, nil, false
}

func mainChatFileCards(result *domain.DriveSearchQueryResponse) []alem.DataCard {
	cards := []alem.DataCard{}
	for _, source := range result.Sources {
		id, err := uuid.Parse(source.FileID)
		if err != nil || id == uuid.Nil {
			continue
		}
		card := alem.DataCard{Title: source.FileName, Service: "Team Drive", Href: "/web/drive/editor/" + id.String()}
		if source.FolderPath != "" {
			card.Fields = append(card.Fields, alem.DataCardField{Label: "Папка", Value: source.FolderPath})
		}
		cards = append(cards, card)
	}
	return cards
}

func chatActionFields(args map[string]any) []alem.DataCardField {
	labels := map[string]string{"title": "Название", "description": "Описание", "start_time": "Начало", "end_time": "Окончание", "attendee_ids": "Участники (ID)", "event_id": "Встреча (ID)", "event_query": "Выбор встречи", "minutes_before": "За сколько минут напомнить", "priority": "Приоритет", "estimate_hours": "Оценка, часов", "assignee_ids": "Исполнители (ID)", "due_at": "Срок", "board_id": "Доска (ID)", "task_id": "Задача (ID)", "task_query": "Задача", "message": "Сообщение", "recipient_user_ids": "Получатели (ID)", "meeting_name": "Название", "from_date": "Начало", "to_date": "Окончание"}
	labels["participant_query"] = "Кому"
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fields := []alem.DataCardField{}
	for _, key := range keys {
		label := labels[key]
		if label == "" {
			label = key
		}
		value := fmt.Sprint(args[key])
		if key == "due_at" {
			if due, err := time.Parse(time.RFC3339, value); err == nil {
				value = due.In(time.FixedZone("Asia/Almaty", 5*60*60)).Format("02.01.2006 15:04:05") + " (Asia/Almaty)"
			}
		}
		fields = append(fields, alem.DataCardField{Label: label, Value: value})
	}
	return fields
}

func mainChatActionResponse(answer string, cards []alem.DataCard) *alem.ChatCompletionResponse {
	return &alem.ChatCompletionResponse{Choices: []alem.Choice{{Message: alem.Message{Role: "assistant", Content: answer}, FinishReason: "stop"}}, DataCards: cards}
}

func (s *SmartAgentService) resolveChatAction(ctx context.Context, identity AIDataIdentity, decision aiActionDecision) *alem.ChatCompletionResponse {
	store := s.chatActionStore()
	key, err := chatActionKey(ctx, identity, decision.Token)
	if err != nil || store == nil {
		return mainChatActionResponse("Не удалось проверить подтверждение. Действие не выполнено.", nil)
	}
	// Atomic consume prevents duplicate submissions and cross-chat/user replay.
	raw, err := store.Take(ctx, key)
	if err != nil {
		return mainChatActionResponse("Подтверждение истекло или уже использовано. При необходимости запросите действие заново.", nil)
	}
	if !decision.Confirm {
		return mainChatActionResponse("Действие отменено.", nil)
	}
	var action aiPendingChatAction
	if json.Unmarshal([]byte(raw), &action) != nil || aiChatWriteTitles[action.Name] == "" {
		return mainChatActionResponse("Некорректное действие. Ничего не выполнено.", nil)
	}
	if _, err := validateChatToolArguments(action.Name, action.Arguments, s.mainChatTools()); err != nil {
		return mainChatActionResponse("Параметры действия недействительны. Ничего не выполнено.", nil)
	}
	text, _ := s.executeTool(ctx, identity.UserID, action.Name, action.Arguments)
	return mainChatActionResponse(text, nil)
}

// Retrieval only: SearXNG returns public snippets and URLs, never an unaccounted
// generated answer. The caller's paid model synthesizes the final response.
func (s *SmartAgentService) searchMainChatWeb(ctx context.Context, identity AIDataIdentity, query string) (AIDataResult, []alem.DataCard, bool) {
	unavailable := AIDataResult{Status: "unavailable", Message: "Поиск в интернете сейчас недоступен. Свежие сведения не проверены."}
	if s.cfg == nil || strings.TrimSpace(s.cfg.Agent.WebSearchURL) == "" || strings.TrimSpace(query) == "" || (strings.TrimSpace(identity.Authorization) == "" && strings.TrimSpace(identity.Cookie) == "") {
		return unavailable, nil, false
	}
	endpoint, err := url.Parse(s.cfg.Agent.WebSearchURL)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return unavailable, nil, false
	}
	params := endpoint.Query()
	params.Set("query", query)
	params.Set("limit", "6")
	endpoint.RawQuery = params.Encode()
	ctx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return unavailable, nil, false
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", identity.Authorization)
	if identity.Cookie != "" {
		req.Header.Set("Cookie", identity.Cookie)
	}
	client := &http.Client{Timeout: 18 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if s.httpClient != nil {
		client.Transport = s.httpClient.Transport
	}
	resp, err := client.Do(req)
	if err != nil {
		return unavailable, nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return unavailable, nil, false
	}
	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&payload) != nil {
		return unavailable, nil, false
	}
	results := []map[string]string{}
	cards := []alem.DataCard{}
	for _, item := range payload.Results {
		parsed, err := url.Parse(item.URL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil {
			continue
		}
		snippet := []rune(item.Content)
		if len(snippet) > 1800 {
			snippet = snippet[:1800]
		}
		results = append(results, map[string]string{"title": item.Title, "url": item.URL, "content": string(snippet)})
		cards = append(cards, alem.DataCard{ID: fmt.Sprintf("web-%d", len(cards)+1), Title: item.Title, Service: parsed.Hostname(), ExternalURL: item.URL})
		if len(results) >= 6 {
			break
		}
	}
	return AIDataResult{Status: "ok", Data: results}, cards, false
}
