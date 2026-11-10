# AI budget system: task 42 verification

This matrix distinguishes durable application behavior from adapters that are intentionally absent. It describes the current checkout and its tests; a passing test is reported only after it has actually run.

## Implementation inventory

| Area | Current implementation | Boundary / placeholder |
|---|---|---|
| Company budgets and employee allocations | PostgreSQL migrations and repositories in `internal/repository/administration_ai_budget*.go` and `administration_ai_user*.go`; service and HTTP handlers in `internal/service/administration_ai_budget.go` and `internal/handler/administration_ai_budget.go`. Admin actions include revision checks and audit writes. | No substitute in-memory budget store is used by repository integration tests. |
| Weekly and five-hour periods | `internal/repository/ai_periods.go` and `internal/domain/ai_periods.go`; periods are opened under transaction locks and use server-side UTC instants. | Provider time is not used to choose accounting period boundaries. |
| Credits and model policy | Integer-only calculation in `internal/domain/ai_credits.go`, with policy versions and exact model keys. | Unknown model or usage is an error; it is not charged as zero. |
| Admission and durable reservations | `internal/repository/ai_reservations.go`; transaction locks, journal idempotency, and durable holds; request orchestration in `internal/service/ai_paid_caller.go`. | Limiter tests may use a fake Redis/lease implementation; persistence and cross-instance admission are covered separately by PostgreSQL tests. |
| Successful, failed, and unknown outcomes | `internal/repository/ai_settlements.go` and `ai_failures.go`; normalized provider usage is committed separately from message history. | Test providers are callbacks/stubs. The verification must not send paid calls to real providers. |
| Retry and reconciliation | Durable jobs and leases in `internal/repository/ai_reconciliation.go`; worker in `internal/service/ai_reconciliation.go`. | **No production provider usage-lookup adapter is registered in `cmd/api/main.go`**. The worker receives an empty lookup registry. Dispatched unknown calls therefore remain `manual`; only trusted local evidence such as a never-dispatched reservation can be closed automatically. Test lookup adapters are fakes, not provider integrations. |
| Observe/enforced switch | Server-owned `AI_ENFORCEMENT_MODE` config in `internal/config/ai_accounting.go` and repository admission policy. | The public API does not control this switch. |
| Metrics | Existing authenticated `/metrics` output in `internal/metrics/collector.go`; DB-wide gauges are periodically sampled. | Per-process event counters reset on process restart; global DB gauges are duplicated across scrapes and must be aggregated with `max`, never `sum`. |
| Employee and frontend surfaces | Employee lifecycle in the repository; personal limit/admin APIs and React screens/repositories in `workspace-front`. | Frontend scripts that transpile isolated modules are contract/UI checks; they are not browser end-to-end tests. |

## Scenario verification

| Scenario | Existing test evidence | Result |
|---|---|---|
| Company budget setup and employee quota distribution | `TestAdministrationAIBudget`, `TestSetAdministrationAIBudget`, `TestAIUserQuotaConcurrentAllocation`, `TestSetAdministrationAIUserQuota`, `TestAdministrationAIUsers`, `TestAIAdministrationHTTP`. | Passed against disposable PostgreSQL. |
| Reserve → provider callback → actual settlement | `TestAIReserveLimitsAndIdempotency`, all `TestAISettle*`, `TestUnifiedAIChatAccountingIntegration`; fake local provider responses, real repository persistence. | Passed against disposable PostgreSQL. |
| Company weekly, employee weekly, five-hour exhaustion and exact period edge | `TestAIReserveLimitsAndIdempotency`, `TestAIPeriodRepository`, `TestAIEnforcementMode_QuotaExceeded`; fixed repository clocks/boundaries. | Passed against disposable PostgreSQL. |
| Active holds across period rollover | `TestAIReservePeriodRolloverAndTransfer`, `TestAISettleAfterTransferAndPeriods`. | Passed against disposable PostgreSQL. |
| Transfer, block, termination, restore | `TestAIEmployeeLifecycle`. | Passed against disposable PostgreSQL. |
| Duplicate delivery, fallback, multi-attempt chain | `TestAIOperationLookupAndAttemptIdentity`, service operation replay tests, `TestAlemChatLaterFailureKeepsPriorSettlements`, Notebook fallback/multi-success tests, Smart Agent tool-loop/fallback/replay tests, unified chat fallback integration. | Passed; HTTP/provider edges use local test doubles. |
| Response persistence failure, settlement rollback, process disconnect/crash recovery | `TestUnifiedAIChatAccountingIntegration` (`save_error`), `TestAISettleRollbackAndService`, `TestAIOperationReplayStates` (crash before dispatch claim), `TestAIPaidHTTPDisconnectDurableSlot`, reconciliation expired-process tests. | Passed against disposable PostgreSQL/local test provider. |
| Parallel allocation and multiple API instances | `TestAIUserQuotaConcurrentAllocation`, `TestAIReserveDurableConcurrencyAcrossInstances`, `TestAIEnforcementMode_MultiInstanceConcurrency`, `TestAIReconciliationConcurrentClaims`, paid-call multi-instance limiter test. | Passed against disposable PostgreSQL. |
| Reconciliation without duplicate charge/release | `TestAIReconciliationWorkers`, repository reconciliation state/lease tests, settlement replay tests. A second pass does not claim terminal jobs or charge/release twice. | Passed against disposable PostgreSQL. |
| Observe mode and switching enforcement | `TestAIEnforcementMode_*`, `TestAIObserveUnknownUsageAndDisabledCompletion`. | Passed against disposable PostgreSQL. |
| Frontend type, localization, and AI budget UI checks | `npx tsc -b`, `npm run i18n:check`, 9 AI limit/admin CJS suites including cross-tab/session synchronization and budget/quota forms. | Passed except stale `test-ai-limits-races.cjs` harness noted below. |

## Executed commands and caveats

- Passed: `go test ./internal/domain -run TestAI`.
- Passed on disposable PostgreSQL: `go test ./internal/repository -run 'Test(AI|AdministrationAI|SetAdministrationAI)'`.
- Passed on disposable PostgreSQL: selected service AI accounting/reconciliation tests, AlemAI/Notebook/Smart Agent/unified chat chains, and `TestAIPaidHTTPDisconnectDurableSlot`.
- Passed on disposable PostgreSQL: `go test ./internal/handler -run TestAIAdministrationHTTP`.
- Passed: AI-only config tests and `go test ./internal/metrics`.
- Passed: frontend `npx tsc -b`, `npm run i18n:check`, and the AI limit/admin scripts except one obsolete harness.
- `scripts/test-ai-limits-races.cjs` fails before assertions because it imports the old hook's `UserAILimitsSyncCoordinator` dependency without providing it. The current hook delegates to the coordinator; the separate `scripts/test-ai-limits-sync.cjs` exercises the real coordinator and passes its superseded-request, session-change, tab-sync, and wake-from-sleep cases. This is a stale test harness, not a confirmed application failure.
- Running the entire `internal/config` package hit two unrelated Windows temp-log cleanup failures (`backend.log` remained open); the AI accounting/reconciliation/limiter config tests pass independently.
- No production provider was called. Provider invocation and response persistence failures used test callbacks, local HTTP servers, triggers, and real temporary PostgreSQL journals.

## Environment

Integration tests must use a disposable PostgreSQL database supplied via `TEST_MESSENGER_DATABASE_URL`; the tests create isolated schemas and drop them at cleanup. This database must never point at production or a shared development database. No paid AI provider is needed. The provider-facing callbacks and usage lookups in these scenarios are test doubles.
