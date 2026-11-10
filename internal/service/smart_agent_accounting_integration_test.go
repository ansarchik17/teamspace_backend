package service

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestSmartAgentAccountingIntegration(t *testing.T) {
	for _, scenario := range []string{"tool_loop_success", "model_fallback", "quota_denied", "replay_conflict"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, db, _, org := newAIOperationServiceDB(t)

			budgetAmount := int64(10_000_000_000)
			if scenario == "quota_denied" {
				budgetAmount = 0
			}

			_, err := db.Exec(ctx, `
				UPDATE organization_ai_budgets SET weekly_budget_microcredits=$1, max_five_hour_microcredits=$1;
				INSERT INTO users(id, organization_id) SELECT 42, id FROM organizations;
				INSERT INTO organization_ai_user_quotas(organization_id, user_id, weekly_quota_microcredits, five_hour_limit_microcredits)
				SELECT id, 42, $1, $1 FROM organizations;
			`, pgx.QueryExecModeSimpleProtocol, budgetAmount)
			require.NoError(t, err)

			providerStep := 0
			providerCalls := 0
			var receivedRequests []alem.ChatCompletionRequest

			providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				providerCalls++
				providerStep++

				var req alem.ChatCompletionRequest
				_ = json.NewDecoder(r.Body).Decode(&req)
				receivedRequests = append(receivedRequests, req)

				w.Header().Set("Content-Type", "application/json")

				if scenario == "model_fallback" && providerStep == 1 {
					w.WriteHeader(http.StatusForbidden)
					_ = json.NewEncoder(w).Encode(alem.ErrorResponse{
						Error: alem.ErrorDetails{
							Code:    "model_access_denied",
							Message: "model access denied by policy",
						},
					})
					return
				}

				if scenario == "tool_loop_success" && providerStep == 1 {
					// РЁР°Рі 1 РІРѕР·РІСЂР°С‰Р°РµС‚ tool_call
					_ = json.NewEncoder(w).Encode(map[string]any{
						"id":    "step1-" + uuid.NewString(),
						"model": req.Model,
						"choices": []map[string]any{
							{
								"finish_reason": "tool_calls",
								"message": map[string]any{
									"role": "assistant",
									"tool_calls": []map[string]any{
										{
											"id":   "tc-1",
											"type": "function",
											"function": map[string]any{
												"name":      "search_users",
												"arguments": `{"query":"User"}`,
											},
										},
									},
								},
							},
						},
						"usage": map[string]any{
							"prompt_tokens":     150,
							"completion_tokens": 25,
							"total_tokens":      175,
						},
					})
					return
				}

				// Р¤РёРЅР°Р»СЊРЅС‹Р№ РѕС‚РІРµС‚
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id":    "resp-final-" + uuid.NewString(),
					"model": req.Model,
					"choices": []map[string]any{
						{
							"finish_reason": "stop",
							"message": map[string]any{
								"role":    "assistant",
								"content": "РћС‚РІРµС‚ Р°СЃСЃРёСЃС‚РµРЅС‚Р° СѓСЃРїРµС€РЅРѕ СЃРіРµРЅРµСЂРёСЂРѕРІР°РЅ.",
							},
						},
					},
					"usage": map[string]any{
						"prompt_tokens":     200,
						"completion_tokens": 40,
						"total_tokens":      240,
					},
				})
			}))
			defer providerServer.Close()

			userRepo := &smartAgentUserRepoStub{
				user: &domain.User{
					ID:             42,
					FirstName:      "Test",
					LastName:       "Minister",
					Email:          "minister@gov.kz",
					IsActive:       true,
					OrganizationID: &org,
				},
			}

			accountRepo := repository.NewAIReservationRepository(db, nil)
			paidCaller := NewAIPaidCallerService(accountRepo)

			alemCfg := config.AlemConfig{
				APIKey:         "test-api-key",
				RequestTimeout: 10 * time.Second,
				MaxRetries:     0,
			}
			cfg := &config.Config{
				Alem: alemCfg,
			}

			// РЎРѕР·РґР°РµРј РєР»РёРµРЅС‚ СЃ Р±Р°Р·РѕРІС‹Рј URL С‚РµСЃС‚РѕРІРѕРіРѕ СЃРµСЂРІРµСЂР°
			testClient := alem.NewClient("test-api-key", 10*time.Second, 0)
			// РџРµСЂРµРЅР°РїСЂР°РІР»СЏРµРј Р±Р°Р·РѕРІС‹Р№ URL РґР»СЏ С‚РµСЃС‚РёСЂРѕРІР°РЅРёСЏ С‡РµСЂРµР· completer
			customCompleter := &openAICompletionClient{
				apiKey:     "test-api-key",
				baseURL:    providerServer.URL,
				httpClient: providerServer.Client(),
			}

			svc := &SmartAgentService{
				cfg:        cfg,
				logger:     zap.NewNop(),
				userRepo:   userRepo,
				completer:  customCompleter,
				httpClient: providerServer.Client(),
			}
			svc.SetPaidAccounting(paidCaller, userRepo)
			if scenario == "model_fallback" {
				svc.SetDefaultModel("deepseek-ai/DeepSeek-V3.2")
			} else {
				svc.SetDefaultModel(DefaultAlemModel)
			}

			opID := "op-test-" + scenario + "-" + uuid.NewString()
			req := &SmartAgentChatRequest{
				Message:     "РџСЂРёРІРµС‚, РїРѕРјРѕРіРё РјРЅРµ СЃ РєР°Р»РµРЅРґР°СЂРµРј",
				SessionID:   uuid.NewString(),
				OperationID: opID,
			}

			switch scenario {
			case "tool_loop_success":
				resp, err := svc.ProcessChat(ctx, 42, req)
				require.NoError(t, err)
				require.NotNil(t, resp)
				require.Equal(t, "РћС‚РІРµС‚ Р°СЃСЃРёСЃС‚РµРЅС‚Р° СѓСЃРїРµС€РЅРѕ СЃРіРµРЅРµСЂРёСЂРѕРІР°РЅ.", resp.Reply)
				require.Equal(t, 2, providerCalls)

				// РџСЂРѕРІРµСЂСЏРµРј Р·Р°РїРёСЃРё РІ Р‘Р”: СЂРѕРІРЅРѕ 2 settled СЂРµР·РµСЂРІР°С†РёРё
				rows, err := db.Query(ctx, `SELECT o.idempotency_key, a.status, a.charged_microcredits FROM ai_usage_operations o JOIN ai_usage_attempts a ON a.operation_id=o.id WHERE o.idempotency_key LIKE $1 ORDER BY o.idempotency_key ASC`, "smart_agent:"+opID+":%")
				require.NoError(t, err)
				defer rows.Close()

				var count int
				for rows.Next() {
					var idKey, status string
					var charged int64
					require.NoError(t, rows.Scan(&idKey, &status, &charged))
					require.Equal(t, "settled", status)
					require.Greater(t, charged, int64(0))
					count++
				}
				require.Equal(t, 2, count)

			case "model_fallback":
				resp, err := svc.ProcessChat(ctx, 42, req)
				require.NoError(t, err)
				require.NotNil(t, resp)
				require.Equal(t, 2, providerCalls)

				// Р’ Р‘Р” РґРѕР»Р¶РЅРѕ Р±С‹С‚СЊ: 1 unknown, 1 settled
				rows, err := db.Query(ctx, `SELECT o.idempotency_key, a.status, (SELECT outcome FROM ai_usage_failure_events f WHERE f.attempt_id=a.id ORDER BY f.created_at DESC LIMIT 1) FROM ai_usage_operations o JOIN ai_usage_attempts a ON a.operation_id=o.id WHERE o.idempotency_key LIKE $1 ORDER BY o.idempotency_key ASC`, "smart_agent:"+opID+":%")
				require.NoError(t, err)
				defer rows.Close()

				var statuses []string
				for rows.Next() {
					var idKey, status string
					var failureOutcome *string
					require.NoError(t, rows.Scan(&idKey, &status, &failureOutcome))
					statuses = append(statuses, status)
				}
				require.Len(t, statuses, 2)
				require.Equal(t, "unknown", statuses[0])
				require.Equal(t, "settled", statuses[1])

			case "quota_denied":
				_, err := svc.ProcessChat(ctx, 42, req)
				require.Error(t, err)
				var denied *domain.AIReservationDenied
				require.True(t, errors.As(err, &denied))
				require.Zero(t, providerCalls)

				var count int
				err = db.QueryRow(ctx, `SELECT count(*) FROM ai_usage_operations WHERE idempotency_key LIKE $1`, "smart_agent:"+opID+":%").Scan(&count)
				require.NoError(t, err)
				require.Zero(t, count)

			case "replay_conflict":
				// РџРµСЂРІС‹Р№ Р·Р°РїСѓСЃРє СѓСЃРїРµС€РµРЅ
				resp, err := svc.ProcessChat(ctx, 42, req)
				require.NoError(t, err)
				require.NotNil(t, resp)
				initialCalls := providerCalls

				// РџРѕРІС‚РѕСЂРЅС‹Р№ Р·Р°РїСѓСЃРє СЃ С‚РµРј Р¶Рµ OperationID
				_, err = svc.ProcessChat(ctx, 42, req)
				require.Error(t, err)
				require.ErrorIs(t, err, domain.ErrAIReservationConflict)
				// Р§РёСЃР»Рѕ РІС‹Р·РѕРІРѕРІ РїСЂРѕРІР°Р№РґРµСЂР° РЅРµ РёР·РјРµРЅРёР»РѕСЃСЊ!
				require.Equal(t, initialCalls, providerCalls)
			}
			_ = testClient
			_ = strings.Clone("")
		})
	}
}
