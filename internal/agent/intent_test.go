package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestKeywordIntentResolver_ResolveReminder(t *testing.T) {
	resolver := NewKeywordIntentResolver(0.75)

	intent, err := resolver.Resolve(context.Background(), &domain.AgentChatRequest{
		Message: "Создай напоминание завтра в 09:00",
	}, nil, nil)

	require.NoError(t, err)
	require.Equal(t, domain.AgentIntentCreateReminder, intent.Name)
	require.True(t, intent.RequiresConfirmation)
	require.GreaterOrEqual(t, intent.Confidence, 0.75)
}

func TestKeywordIntentResolver_LowConfidence(t *testing.T) {
	resolver := NewKeywordIntentResolver(0.75)

	intent, err := resolver.Resolve(context.Background(), &domain.AgentChatRequest{
		Message: "ага",
	}, nil, nil)

	require.NoError(t, err)
	require.NotEmpty(t, intent.SuggestedActions)
	require.Less(t, intent.Confidence, 0.75)
}

func TestKeywordIntentResolver_ResolveMyTasks(t *testing.T) {
	resolver := NewKeywordIntentResolver(0.75)

	intent, err := resolver.Resolve(context.Background(), &domain.AgentChatRequest{
		Message: "Покажи мои задачи на сегодня",
	}, nil, nil)

	require.NoError(t, err)
	require.Equal(t, domain.AgentIntentGetTasks, intent.Name)
	require.False(t, intent.RequiresConfirmation)
}

func TestKeywordIntentResolver_ResolveMyCalendar(t *testing.T) {
	resolver := NewKeywordIntentResolver(0.75)

	intent, err := resolver.Resolve(context.Background(), &domain.AgentChatRequest{
		Message: "Что у меня в календаре сегодня",
	}, nil, nil)

	require.NoError(t, err)
	require.Equal(t, domain.AgentIntentGetCalendar, intent.Name)
	require.False(t, intent.RequiresConfirmation)
}

func TestKeywordIntentResolver_ResolveNotes(t *testing.T) {
	resolver := NewKeywordIntentResolver(0.75)

	intent, err := resolver.Resolve(context.Background(), &domain.AgentChatRequest{
		Message: "Покажи мои заметки по проекту",
	}, nil, nil)

	require.NoError(t, err)
	require.Equal(t, domain.AgentIntentGetNotes, intent.Name)
	require.False(t, intent.RequiresConfirmation)
}

func TestKeywordIntentResolver_ResolveOnlineMeeting(t *testing.T) {
	resolver := NewKeywordIntentResolver(0.75)

	intent, err := resolver.Resolve(context.Background(), &domain.AgentChatRequest{
		Message: "Создай онлайн-встречу завтра в 15:00 15:30",
	}, nil, nil)

	require.NoError(t, err)
	require.Equal(t, domain.AgentIntentCreateMeeting, intent.Name)
	require.True(t, intent.RequiresConfirmation)
}

func TestKeywordIntentResolver_ResolveWebSearch(t *testing.T) {
	resolver := NewKeywordIntentResolver(0.75)

	intent, err := resolver.Resolve(context.Background(), &domain.AgentChatRequest{
		Message: "Найди в интернете кто такой Жаслан Мадиев",
	}, nil, nil)

	require.NoError(t, err)
	require.Equal(t, domain.AgentIntentWebSearch, intent.Name)
	require.False(t, intent.RequiresConfirmation)
}
