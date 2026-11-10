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
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

func TestUnifiedAIChatAccountingIntegration(t *testing.T) {
	for _, scenario := range []string{"success", "provider_failure", "model_fallback", "save_error", "quota_denied"} {
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
				CREATE TABLE IF NOT EXISTS unified_ai_chats (
					id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
					user_id BIGINT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
					title TEXT NOT NULL DEFAULT 'AI Assistant',
					model_name TEXT NOT NULL DEFAULT 'openai/gpt-oss-120b',
					created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
					updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
				);
				CREATE TABLE IF NOT EXISTS unified_ai_messages (
					id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
					chat_id UUID NOT NULL REFERENCES unified_ai_chats(id) ON DELETE CASCADE,
					role VARCHAR(50) NOT NULL,
					content TEXT NOT NULL,
					file_url TEXT,
					tokens_usage INT DEFAULT 0,
					created_at TIMESTAMPTZ NOT NULL DEFAULT now()
				);
			`, pgx.QueryExecModeSimpleProtocol, budgetAmount)
			require.NoError(t, err)

			chatID := uuid.New()
			initialModel := DefaultAlemModel
			if scenario == "model_fallback" {
				initialModel = "moonshotai/Kimi-K2-Thinking"
			}
			_, err = db.Exec(ctx, `INSERT INTO unified_ai_chats(id, user_id, model_name) VALUES($1, 42, $2)`, chatID, initialModel)
			require.NoError(t, err)

			requests := []alem.ChatCompletionRequest{}
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req alem.ChatCompletionRequest
				if json.NewDecoder(r.Body).Decode(&req) != nil {
					w.WriteHeader(400)
					return
				}
				requests = append(requests, req)
				step := len(requests)
				w.Header().Set("Content-Type", "application/json")

				if scenario == "provider_failure" {
					w.WriteHeader(503)
					fmt.Fprint(w, `{"error":{"message":"temporarily unavailable"}}`)
					return
				}
				if scenario == "model_fallback" && step == 1 {
					w.WriteHeader(403)
					fmt.Fprint(w, `{"error":{"message":"model access denied"}}`)
					return
				}

				msg := alem.Message{Role: "assistant", Content: "РћС‚РІРµС‚ РЅР° РІРѕРїСЂРѕСЃ РїРѕР»СЊР·РѕРІР°С‚РµР»СЏ."}
				json.NewEncoder(w).Encode(alem.ChatCompletionResponse{
					ID:      fmt.Sprintf("provider-%d", step),
					Model:   req.Model,
					Choices: []alem.Choice{{Message: msg}},
					Usage:   alem.Usage{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3},
				})
			}))
			defer provider.Close()

			originalBaseURL := alem.BaseURL
			alem.BaseURL = provider.URL
			defer func() { alem.BaseURL = originalBaseURL }()

			unifiedRepo := repository.NewUnifiedAIRepository(db, zap.NewNop())
			svc := NewUnifiedAIService(&config.Config{}, unifiedRepo, nil, zap.NewNop())
			svc.client = alem.NewClient("synthetic-test-key", 5*time.Second, 0)
			svc.SetPaidAccounting(accounting, alemPayerStub{user: &domain.User{ID: 42, OrganizationID: &org, IsActive: true}})

			if scenario == "save_error" {
				// Р”РѕР±Р°РІР»СЏРµРј С‚СЂРёРіРіРµСЂ, РєРѕС‚РѕСЂС‹Р№ Р·Р°РїСЂРµС‰Р°РµС‚ Р·Р°РїРёСЃСЊ СЂРѕР»Рё assistant РІ unified_ai_messages
				_, err = db.Exec(ctx, `
					CREATE OR REPLACE FUNCTION fail_assistant_insert() RETURNS trigger AS $$
					BEGIN
						IF NEW.role = 'assistant' THEN
							RAISE EXCEPTION 'disk full: simulated failure';
						END IF;
						RETURN NEW;
					END;
					$$ LANGUAGE plpgsql;
					CREATE TRIGGER trig_fail_assistant BEFORE INSERT ON unified_ai_messages
					FOR EACH ROW EXECUTE FUNCTION fail_assistant_insert();
				`)
				require.NoError(t, err)
			}

			resp, err := svc.SendMessage(ctx, chatID.String(), 42, "РњРѕР№ С‚РµСЃС‚РѕРІС‹Р№ РІРѕРїСЂРѕСЃ", nil)

			if scenario == "success" {
				require.NoError(t, err)
				require.NotNil(t, resp)
				require.Len(t, requests, 1)

				// РџСЂРѕРІРµСЂРєР° СЃС‡РµС‚С‡РёРєРѕРІ РїРѕРїС‹С‚РѕРє РІ Р‘Р”
				var count, completed, pending int
				var charged, held int64
				require.NoError(t, db.QueryRow(ctx, `SELECT count(*), count(*) FILTER(WHERE status='settled'), count(*) FILTER(WHERE status='unknown'), coalesce(sum(charged_microcredits),0) FROM ai_usage_attempts`).Scan(&count, &completed, &pending, &charged))
				require.Equal(t, 1, count)
				require.Equal(t, 1, completed)
				require.Equal(t, 0, pending)
				require.Equal(t, int64(5000), charged)

				// РџСЂРѕРІРµСЂРєР° Р·РµСЂРєР°Р»СЊРЅС‹С… СЃС‡РµС‚С‡РёРєРѕРІ РїРµСЂРёРѕРґР°
				require.NoError(t, db.QueryRow(ctx, `SELECT coalesce(sum(used_microcredits),0), coalesce(sum(reserved_microcredits),0) FROM organization_ai_weekly_periods WHERE organization_id=$1`, org).Scan(&charged, &held))
				require.Equal(t, int64(5000), charged)
				require.Zero(t, held)

				// РџСЂРѕРІРµСЂРєР° СЃРѕРѕР±С‰РµРЅРёР№ РІ Р‘Р”
				var msgCount int
				require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM unified_ai_messages WHERE chat_id=$1`, chatID).Scan(&msgCount))
				require.Equal(t, 2, msgCount)
			} else if scenario == "save_error" {
				// РЈСЃРїРµС€РЅС‹Р№ РїР»Р°С‚РЅС‹Р№ РІС‹Р·РѕРІ СЃ РїРѕСЃР»РµРґСѓСЋС‰РµР№ РѕС€РёР±РєРѕР№ Р·Р°РїРёСЃРё СЃРѕРѕР±С‰РµРЅРёСЏ РѕСЃС‚Р°С‘С‚СЃСЏ РѕРїР»Р°С‡РµРЅРЅС‹Рј!
				require.Error(t, err)
				require.Nil(t, resp)
				require.Len(t, requests, 1)

				// Р Р°СЃС…РѕРґ РІ Р‘Р” Р”РћР›Р–Р•Рќ Р‘Р«РўР¬ РћРџР›РђР§Р•Рќ
				var count, completed int
				var charged, held int64
				require.NoError(t, db.QueryRow(ctx, `SELECT count(*), count(*) FILTER(WHERE status='settled'), coalesce(sum(charged_microcredits),0) FROM ai_usage_attempts`).Scan(&count, &completed, &charged))
				require.Equal(t, 1, count)
				require.Equal(t, 1, completed)
				require.Equal(t, int64(5000), charged)

				require.NoError(t, db.QueryRow(ctx, `SELECT coalesce(sum(used_microcredits),0), coalesce(sum(reserved_microcredits),0) FROM organization_ai_weekly_periods WHERE organization_id=$1`, org).Scan(&charged, &held))
				require.Equal(t, int64(5000), charged)
				require.Zero(t, held)

				// Р’ СЃРѕРѕР±С‰РµРЅРёСЏС… С‚РѕР»СЊРєРѕ 1 (user), assistant РЅРµ СЃРѕС…СЂР°РЅРёР»СЃСЏ РёР·-Р·Р° РѕС€РёР±РєРё С‚СЂРёРіРіРµСЂР°
				var msgCount int
				require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM unified_ai_messages WHERE chat_id=$1`, chatID).Scan(&msgCount))
				require.Equal(t, 1, msgCount)
			} else if scenario == "provider_failure" {
				require.Error(t, err)
				require.Len(t, requests, 1)

				var count, completed, pending int
				var charged, held int64
				require.NoError(t, db.QueryRow(ctx, `SELECT count(*), count(*) FILTER(WHERE status='settled'), count(*) FILTER(WHERE status='unknown'), coalesce(sum(charged_microcredits),0) FROM ai_usage_attempts`).Scan(&count, &completed, &pending, &charged))
				require.Equal(t, 1, count)
				require.Equal(t, 0, completed)
				require.Equal(t, 1, pending)
				require.Zero(t, charged)

				require.NoError(t, db.QueryRow(ctx, `SELECT coalesce(sum(used_microcredits),0), coalesce(sum(reserved_microcredits),0) FROM organization_ai_weekly_periods WHERE organization_id=$1`, org).Scan(&charged, &held))
				require.Zero(t, charged)
				require.Positive(t, held, "unknown dispatch must retain the hold")
			} else if scenario == "model_fallback" {
				require.NoError(t, err)
				require.Len(t, requests, 2)

				var count, completed, pending int
				var charged, held int64
				require.NoError(t, db.QueryRow(ctx, `SELECT count(*), count(*) FILTER(WHERE status='settled'), count(*) FILTER(WHERE status='unknown'), coalesce(sum(charged_microcredits),0) FROM ai_usage_attempts`).Scan(&count, &completed, &pending, &charged))
				require.Equal(t, 2, count)
				require.Equal(t, 1, completed)
				require.Equal(t, 1, pending)
				require.Equal(t, int64(5000), charged)

				require.NoError(t, db.QueryRow(ctx, `SELECT coalesce(sum(used_microcredits),0), coalesce(sum(reserved_microcredits),0) FROM organization_ai_weekly_periods WHERE organization_id=$1`, org).Scan(&charged, &held))
				require.Equal(t, int64(5000), charged)
				require.Positive(t, held, "failed attempt retains hold for reconciliation")

				// РџСЂРѕРІРµСЂСЏРµРј, С‡С‚Рѕ РјРѕРґРµР»СЊ РІ unified_ai_chats РѕР±РЅРѕРІР»РµРЅР° РЅР° fallback РјРѕРґРµР»СЊ
				var updatedModel string
				require.NoError(t, db.QueryRow(ctx, `SELECT model_name FROM unified_ai_chats WHERE id=$1`, chatID).Scan(&updatedModel))
				require.Equal(t, DefaultAlemModel, updatedModel)
			} else if scenario == "quota_denied" {
				require.Error(t, err)
				var denied *domain.AIReservationDenied
				require.ErrorAs(t, err, &denied)
				require.Zero(t, len(requests), "РїСЂРѕРІР°Р№РґРµСЂ РЅРµ РґРѕР»Р¶РµРЅ РІС‹Р·С‹РІР°С‚СЊСЃСЏ РїСЂРё РёСЃС‡РµСЂРїР°РЅРёРё Р±СЋРґР¶РµС‚Р°")

				var count int
				require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_usage_attempts`).Scan(&count))
				require.Zero(t, count)
			}
		})
	}
}
