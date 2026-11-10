# AI accounting operational metrics

AI accounting metrics are exported by the existing `/metrics` endpoint when `METRICS_ENABLED` is enabled. The endpoint uses the application's existing authentication middleware; it is not public. No external monitoring service is configured here. Apply the AI accounting and reconciliation migrations before using database-backed gauges.

## Metrics

| Metric | Type | Meaning |
|---|---|---|
| `messenger_ai_accounting_operations_total{outcome}` | counter | Committed logical-attempt outcomes: `success`, `confirmed_not_executed`, or `unknown`. Idempotent settlement replays do not increment it. |
| `messenger_ai_accounting_quota_denials_total{scope}` | counter | Admission denied by configured financial limits. `scope` is one of `company`, `weekly`, `five_hour`, `service`, `account`. |
| `messenger_ai_accounting_reservation_errors_total{kind}` | counter | Non-quota reservation failures classified as `database`, `invalid`, `conflict`, `calculation`, or `unknown`. |
| `messenger_ai_accounting_settlement_errors_total{kind}` | counter | Settlement/failure-journal errors with the same bounded `kind` values. |
| `messenger_ai_accounting_reconciliation_total{outcome}` | counter | Committed reconciliation results: `reconciled_usage`, `reconciled_not_executed`, `reconciliation_deferred`, `reconciliation_manual`, or `reconciliation_error`. A lost lease is not counted as a result. |
| `messenger_ai_accounting_unresolved_reservations` | gauge | Current global number of active reservations, including unknown provider outcomes. |
| `messenger_ai_accounting_unknown_usage_reservations` | gauge | Current global number of active reservations whose attempt has an unknown provider usage result. This surfaces unresolved settlement failures after request traffic stops. |
| `messenger_ai_accounting_stale_reservations` | gauge | Current global number of active reservations older than `AI_RECONCILIATION_STALE_AFTER`. |
| `messenger_ai_accounting_oldest_reservation_age_seconds` | gauge | Age of the oldest active reservation. |
| `messenger_ai_accounting_db_sample_success` | gauge | `1` when the latest database gauge sample succeeded, otherwise `0`. Failed samples retain the last known counts. |

Counters record lifecycle events only after the corresponding durable transition is applied. Reconciliation outcome counters are updated only after a successful commit and only when that claim changed the durable job. Replaying a completed job cannot add another outcome or charge. Database totals are gauges, never counters reconstructed from a query.

All AI metric labels are selected from fixed enums. They contain no user, company, operation, provider request, model, URL, key, prompt, or error-message values. The database sampler runs periodically even when there are no new AI requests. Every API instance can expose the same database-wide gauge snapshot; in dashboards and alerts, collapse the scrape target dimension with `max`, never `sum`. Process event counters are instance-local, so use `sum` when combining their rates across targets.

## Alert examples

Examples use Prometheus syntax; adapt labels and durations to the deployment's scrape interval and incident policy.

```promql
max without (instance, pod) (messenger_ai_accounting_stale_reservations) > 0
```

Alert on any long-held reservation; the stale threshold is controlled by `AI_RECONCILIATION_STALE_AFTER` (default `15m`).

```promql
max without (instance, pod) (messenger_ai_accounting_unknown_usage_reservations) > 0
```

Alert while unknown-usage attempts remain unresolved, including when no new AI request is arriving to refresh an event counter.

```promql
max without (instance, pod) (messenger_ai_accounting_db_sample_success) == 0
```

Alert when the background sampler cannot read global accounting state. The last good gauge values remain exposed until a later sample succeeds.

```promql
sum(rate(messenger_ai_accounting_settlement_errors_total[5m])) > 0
```

Alert on recent settlement errors, including failures that leave a durable unresolved hold. Pair it with the stale-reservation gauge because an error can occur before a hold becomes stale.

```promql
sum(rate(messenger_ai_accounting_operations_total{outcome="unknown"}[10m])) > 0
```

Alert on new ambiguous provider outcomes. An unknown outcome remains charged as unresolved capacity until confirmed usage or confirmed non-execution is reconciled.

```promql
sum(rate(messenger_ai_accounting_reconciliation_total{outcome="reconciliation_error"}[5m])) > 0
```

Alert when reconciliation storage/worker operations are failing. `reconciliation_manual` is an operational review queue and should also have an alert or dashboard panel.
