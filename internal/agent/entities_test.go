package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestSimpleEntityExtractor_TaskRequestUsesTodayPeriodAndTimezone(t *testing.T) {
	extractor := NewSimpleEntityExtractor()

	entities, err := extractor.Extract(context.Background(), &domain.AgentChatRequest{
		Message:  "Покажи мои задачи на сегодня",
		Timezone: "Asia/Almaty",
	}, &domain.AgentIntentResult{Name: domain.AgentIntentGetTasks}, nil)

	require.NoError(t, err)
	require.Equal(t, "today", entities["period"])
	require.Equal(t, "Asia/Almaty", entities["timezone"])
	require.NotNil(t, entities["timezone_offset_minutes"])
}

func TestSimpleEntityExtractor_ReminderUsesTomorrowPeriod(t *testing.T) {
	extractor := NewSimpleEntityExtractor()

	entities, err := extractor.Extract(context.Background(), &domain.AgentChatRequest{
		Message:  "Создай напоминание завтра в 09:00",
		Timezone: "Asia/Almaty",
	}, &domain.AgentIntentResult{Name: domain.AgentIntentCreateReminder}, nil)

	require.NoError(t, err)
	require.Equal(t, "tomorrow", entities["period"])
	require.NotEmpty(t, entities["remind_at"])
}

func TestSimpleEntityExtractor_WebSearchNormalizesQuery(t *testing.T) {
	extractor := NewSimpleEntityExtractor()

	entities, err := extractor.Extract(context.Background(), &domain.AgentChatRequest{
		Message: "Найди в интернете кто такой Жаслан Мадиев",
	}, &domain.AgentIntentResult{Name: domain.AgentIntentWebSearch}, nil)

	require.NoError(t, err)
	require.Equal(t, "кто такой Жаслан Мадиев", entities["query"])
}
