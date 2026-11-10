package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestNotebookAccountingIntegration(t *testing.T) {
	for _, scenario := range []string{"chain_success", "chain_last_failure", "model_fallback", "quota_denied"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, db, accounting, org := newAIOperationServiceDB(t)

			// РЎРѕР·РґР°РµРј РїРѕР»СЊР·РѕРІР°С‚РµР»СЏ, РєРѕРјРїР°РЅРёСЋ, Р±СЋРґР¶РµС‚ Рё Р»РёС‡РЅС‹Рµ РєРІРѕС‚С‹
			budgetAmount := int64(10_000_000_000)
			if scenario == "quota_denied" {
				budgetAmount = 0
			}
			_, err := db.Exec(ctx, `
				UPDATE organization_ai_budgets SET weekly_budget_microcredits=$1, max_five_hour_microcredits=$1;
				INSERT INTO users(id, organization_id) SELECT 42, id FROM organizations;
				INSERT INTO organization_ai_user_quotas(organization_id, user_id, weekly_quota_microcredits, five_hour_limit_microcredits)
				SELECT id, 42, $1, $1 FROM organizations;
				CREATE TABLE IF NOT EXISTS notebook_chats (
					id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
					user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
					title TEXT NOT NULL DEFAULT 'Notebook Chat',
					created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
					updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
				);
				CREATE TABLE IF NOT EXISTS notebook_messages (
					id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
					chat_id UUID NOT NULL REFERENCES notebook_chats(id) ON DELETE CASCADE,
					role VARCHAR(50) NOT NULL,
					message_type VARCHAR(50) NOT NULL DEFAULT 'text',
					content TEXT NOT NULL,
					created_at TIMESTAMPTZ NOT NULL DEFAULT now()
				);
			`, pgx.QueryExecModeSimpleProtocol, budgetAmount)
			require.NoError(t, err)

			chatID := uuid.New()
			_, err = db.Exec(ctx, `INSERT INTO notebook_chats(id, user_id, title) VALUES($1, 42, 'Research')`, chatID)
			require.NoError(t, err)

			fileID := uuid.New()
			files := []*domain.DriveFile{{ID: fileID, OwnerID: 42, RealName: "source-1.txt", ObjectName: "drive/source-1.txt", Size: 30}}
			drive := fakeDriveSearchService(map[string][]byte{"drive/source-1.txt": []byte("Р¤Р°РєС‚С‹ РґРѕРєСѓРјРµРЅС‚Р° L1\nР’С‚РѕСЂР°СЏ СЃС‚СЂРѕРєР° L2")})

			requests := []alem.ChatCompletionRequest{}
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/models" {
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(alem.ModelListResponse{
						Data: []alem.Model{
							{ID: "moonshotai/Kimi-K2-Thinking"},
							{ID: DefaultAlemModel},
						},
					})
					return
				}

				var req alem.ChatCompletionRequest
				if json.NewDecoder(r.Body).Decode(&req) != nil {
					w.WriteHeader(400)
					return
				}
				requests = append(requests, req)
				step := len(requests)
				w.Header().Set("Content-Type", "application/json")

				if scenario == "chain_last_failure" && step == 2 {
					w.WriteHeader(503)
					fmt.Fprint(w, `{"error":{"message":"service unavailable on verification"}}`)
					return
				}
				if scenario == "model_fallback" && step == 1 {
					w.WriteHeader(403)
					fmt.Fprint(w, `{"error":{"message":"model access denied"}}`)
					return
				}

				msgContent := "Р§РµСЂРЅРѕРІРёРє РѕС‚РІРµС‚Р° [S1:L1]"
				if step >= 2 {
					msgContent = "Р’РµСЂРёС„РёС†РёСЂРѕРІР°РЅРЅС‹Р№ РѕС‚РІРµС‚ [S1:L1]"
				}

				json.NewEncoder(w).Encode(alem.ChatCompletionResponse{
					ID:      fmt.Sprintf("provider-%d", step),
					Model:   req.Model,
					Choices: []alem.Choice{{Message: alem.Message{Role: "assistant", Content: msgContent}}},
					Usage:   alem.Usage{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3},
				})
			}))
			defer provider.Close()

			originalBaseURL := alem.BaseURL
			alem.BaseURL = provider.URL
			defer func() { alem.BaseURL = originalBaseURL }()

			svc := &NotebookResearchService{
				notebooks: notebookTestReader{owner: 42, files: files},
				drive:     drive,
				llm:       alem.NewClient("synthetic-test-key", 5*time.Second, 0),
				logger:    zap.NewNop(),
			}
			svc.SetPaidAccounting(accounting, alemPayerStub{user: &domain.User{ID: 42, OrganizationID: &org, IsActive: true}})

			opID := uuid.NewString()
			req := NotebookResearchRequest{
				OperationID: opID,
				Question:    "Р§С‚Рѕ РІ РґРѕРєСѓРјРµРЅС‚Рµ?",
				FileIDs:     []uuid.UUID{fileID},
				Mode:        "thinking",
			}

			resp, err := svc.Ask(ctx, 42, chatID, req)

			switch scenario {
			case "chain_success":
				require.NoError(t, err)
				require.NotNil(t, resp)
				require.Len(t, requests, 2, "РіРµРЅРµСЂР°С†РёСЏ + РІРµСЂРёС„РёРєР°С†РёСЏ РґРѕР»Р¶РЅС‹ РІС‹Р·РІР°С‚СЊ РїСЂРѕРІР°Р№РґРµСЂР° СЂРѕРІРЅРѕ 2 СЂР°Р·Р°")

				var count, completed, pending int
				var charged, held int64
				require.NoError(t, db.QueryRow(ctx, `SELECT count(*), count(*) FILTER(WHERE status='settled'), count(*) FILTER(WHERE status='unknown'), coalesce(sum(charged_microcredits),0) FROM ai_usage_attempts`).Scan(&count, &completed, &pending, &charged))
				require.Equal(t, 2, count)
				require.Equal(t, 2, completed)
				require.Equal(t, 0, pending)
				require.Positive(t, charged)

				require.NoError(t, db.QueryRow(ctx, `SELECT coalesce(sum(used_microcredits),0), coalesce(sum(reserved_microcredits),0) FROM organization_ai_weekly_periods WHERE organization_id=$1`, org).Scan(&charged, &held))
				require.Positive(t, charged)
				require.Zero(t, held)

			case "chain_last_failure":
				require.Error(t, err)
				require.Len(t, requests, 2, "СЂРѕР»Рё: 1 С‡РµСЂРЅРѕРІРёРє + 1 РЅРµСѓСЃРїРµС€РЅР°СЏ РІРµСЂРёС„РёРєР°С†РёСЏ")

				var count, completed, pending int
				var charged, held int64
				require.NoError(t, db.QueryRow(ctx, `SELECT count(*), count(*) FILTER(WHERE status='settled'), count(*) FILTER(WHERE status='unknown'), coalesce(sum(charged_microcredits),0) FROM ai_usage_attempts`).Scan(&count, &completed, &pending, &charged))
				require.Equal(t, 2, count)
				require.Equal(t, 1, completed, "РїРµСЂРІС‹Р№ СѓСЃРїРµС€РЅС‹Р№ РІС‹Р·РѕРІ РѕСЃС‚Р°Р»СЃСЏ РѕРїР»Р°С‡РµРЅРЅС‹Рј")
				require.Equal(t, 1, pending, "РЅРµСѓСЃРїРµС€РЅС‹Р№ РІС‚РѕСЂРѕР№ РІС‹Р·РѕРІ Р·Р°С„РёРєСЃРёСЂРѕРІР°РЅ СЃРѕ СЃС‚Р°С‚СѓСЃРѕРј unknown")
				require.Positive(t, charged, "РїРµСЂРІС‹Р№ РІС‹Р·РѕРІ СЃРѕС…СЂР°РЅРёР» СЃРїРёСЃР°РЅРёРµ")

				require.NoError(t, db.QueryRow(ctx, `SELECT coalesce(sum(used_microcredits),0), coalesce(sum(reserved_microcredits),0) FROM organization_ai_weekly_periods WHERE organization_id=$1`, org).Scan(&charged, &held))
				require.Positive(t, charged)
				require.Positive(t, held, "СѓРґРµСЂР¶Р°РЅРёРµ РїРѕ unknown РїРѕРїС‹С‚РєРµ СЃРѕС…СЂР°РЅРµРЅРѕ")

			case "model_fallback":
				require.NoError(t, err)
				require.NotNil(t, resp)
				require.True(t, resp.Fallback)
				require.Len(t, requests, 3, "1 РѕС‚РєР°Р· РјРѕРґРµР»Рё + 1 fallback РіРµРЅРµСЂР°С†РёСЏ + 1 РІРµСЂРёС„РёРєР°С†РёСЏ")

				var count, completed, pending int
				var charged int64
				require.NoError(t, db.QueryRow(ctx, `SELECT count(*), count(*) FILTER(WHERE status='settled'), count(*) FILTER(WHERE status='unknown'), coalesce(sum(charged_microcredits),0) FROM ai_usage_attempts`).Scan(&count, &completed, &pending, &charged))
				require.Equal(t, 3, count)
				require.Equal(t, 2, completed, "2 СѓСЃРїРµС€РЅС‹С… РІС‹Р·РѕРІР° (fallback + РІРµСЂРёС„РёРєР°С†РёСЏ) СЃРїРёСЃР°РЅС‹")
				require.Equal(t, 1, pending, "1 РѕС‚РєР°Р· РїРµСЂРІРѕР№ РјРѕРґРµР»Рё Р·Р°СЂРµРіРёСЃС‚СЂРёСЂРѕРІР°РЅ РєР°Рє unknown")
				require.Positive(t, charged)

			case "quota_denied":
				require.Error(t, err)
				require.Empty(t, requests, "РїСЂРё РѕС‚РєР°Р·Рµ РїРѕ РєРІРѕС‚Рµ РѕР±СЂР°С‰РµРЅРёР№ Рє РїСЂРѕРІР°Р№РґРµСЂСѓ Р±С‹С‚СЊ РЅРµ РґРѕР»Р¶РЅРѕ")

				var count int
				require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_usage_attempts`).Scan(&count))
				require.Equal(t, 0, count)
			}
		})
	}
}
