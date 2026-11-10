// ai-accounting is an operator-only tool. It exposes no HTTP routes and never
// calls a provider. Its DB credentials are separate from public authentication.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/west6ide/workspace-backend/internal/service"
)

type manualUsage struct {
	Input     *int64 `json:"input_tokens"`
	Output    *int64 `json:"output_tokens"`
	Total     *int64 `json:"total_tokens"`
	Cached    *int64 `json:"cached_input_tokens"`
	Reasoning *int64 `json:"reasoning_output_tokens"`
}
type resolutionEvidence struct {
	Organization      uuid.UUID    `json:"organization_id"`
	Attempt           uuid.UUID    `json:"attempt_id"`
	Operator          string       `json:"operator"`
	Reference         string       `json:"evidence_reference"`
	ProviderConfirmed bool         `json:"provider_confirmed"`
	Outcome           string       `json:"outcome"`
	Provider          string       `json:"provider_id"`
	Endpoint          string       `json:"endpoint_kind"`
	Model             string       `json:"actual_model"`
	Adapter           string       `json:"usage_adapter_version"`
	RequestID         *string      `json:"provider_request_id"`
	Usage             *manualUsage `json:"usage"`
}

var evidenceID = regexp.MustCompile(`^[A-Za-z0-9._:@/-]{1,80}$`)

func readEvidence(r io.Reader) (resolutionEvidence, error) {
	var e resolutionEvidence
	decoder := json.NewDecoder(io.LimitReader(r, 65537))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&e); err != nil {
		return e, errors.New("invalid evidence JSON")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return e, errors.New("evidence must contain one JSON object")
	}
	if e.Attempt == uuid.Nil || !evidenceID.MatchString(e.Operator) || !evidenceID.MatchString(e.Reference) || !e.ProviderConfirmed || e.Provider == "" || e.Endpoint == "" {
		return e, errors.New("provider confirmation, operator, reference and exact attempt identity are required")
	}
	if e.Outcome != "usage" && e.Outcome != "not_executed" {
		return e, errors.New("only confirmed usage or confirmed non-execution can resolve a hold")
	}
	if e.Outcome == "usage" {
		if e.Usage == nil || e.Usage.Input == nil || e.Usage.Output == nil || e.Model == "" || e.Adapter == "" {
			return e, errors.New("complete confirmed usage, actual model and adapter version are required")
		}
	} else if e.Usage != nil {
		return e, errors.New("non-execution evidence must not contain usage")
	}
	return e, nil
}

func (e resolutionEvidence) failure(job domain.AIReconciliationJob) (domain.AIFailureRequest, error) {
	if job.OrganizationID != e.Organization || job.AttemptID != e.Attempt || job.ProviderID != e.Provider || job.EndpointKind != e.Endpoint || (job.ProviderRequestID != nil && (e.RequestID == nil || *job.ProviderRequestID != *e.RequestID)) {
		return domain.AIFailureRequest{}, errors.New("evidence does not match the journal attempt")
	}
	reference := "operator/" + e.Operator + ":" + e.Reference
	req := domain.AIFailureRequest{OrganizationID: e.Organization, AttemptID: e.Attempt, EventID: uuid.NewSHA1(e.Attempt, []byte("manual-resolution:"+reference)), Outcome: domain.AIFailureNotExecuted, Reason: "provider_error", EvidenceSource: "provider_confirmation", EvidenceReference: &reference}
	if e.Outcome == "usage" {
		if e.Adapter != job.UsageAdapterVersion {
			return req, errors.New("adapter version does not match the pinned attempt")
		}
		usage := domain.AICreditUsage{Status: "verified", InputTokens: e.Usage.Input, OutputTokens: e.Usage.Output, TotalTokens: e.Usage.Total, CachedInput: e.Usage.Cached, ReasoningOutput: e.Usage.Reasoning}
		if _, err := domain.CalculateAICredits(domain.AICreditModelKey{ProviderID: e.Provider, EndpointKind: e.Endpoint, ModelID: e.Model, PolicyVersion: job.PolicyVersion}, usage); err != nil {
			return req, err
		}
		req.Outcome = domain.AIFailureCharged
		req.Usage = &domain.AISettleRequest{OrganizationID: e.Organization, AttemptID: e.Attempt, ActualModel: e.Model, UsageAdapterVersion: e.Adapter, UsageSource: "manual_provider_confirmation", ProviderRequestID: e.RequestID, Usage: usage, ReportedInputTokens: e.Usage.Input, ReportedOutputTokens: e.Usage.Output, ReportedTotalTokens: e.Usage.Total}
	}
	return req, nil
}

func run(args []string, out io.Writer) error {
	if len(args) > 0 && args[0] == "defaults" {
		return runDefaults(args[1:], out)
	}
	if len(args) == 0 {
		return errors.New("usage: ai-accounting preflight|inspect|resolve|reconcile --database-name NAME [--organization UUID --attempt UUID] [--evidence FILE] [--apply]")
	}
	command := args[0]
	if command != "preflight" && command != "inspect" && command != "resolve" && command != "reconcile" {
		return errors.New("unknown command")
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(out)
	dbName := flags.String("database-name", "", "required exact database name")
	org := flags.String("organization", "", "organization UUID for inspect")
	attempt := flags.String("attempt", "", "attempt UUID for inspect")
	file := flags.String("evidence", "", "JSON evidence file for resolve")
	apply := flags.Bool("apply", false, "commit resolve/reconcile; default is read-only")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *dbName == "" {
		return errors.New("exact --database-name is required; unexpected positional arguments are forbidden")
	}
	if command != "inspect" && (*org != "" || *attempt != "") {
		return errors.New("--organization and --attempt are only valid for inspect; resolve uses the bound evidence identity")
	}
	if (command != "resolve" && *file != "") || ((command == "inspect" || command == "preflight") && *apply) {
		return errors.New("flags do not match the selected command")
	}
	var evidence resolutionEvidence
	var orgID, attemptID uuid.UUID
	if command == "resolve" {
		f, err := os.Open(*file)
		if err != nil {
			return errors.New("cannot open evidence file")
		}
		defer f.Close()
		evidence, err = readEvidence(f)
		if err != nil {
			return err
		}
		orgID, attemptID = evidence.Organization, evidence.Attempt
	}
	if command == "inspect" {
		var err error
		orgID, err = uuid.Parse(*org)
		if err != nil {
			return errors.New("invalid organization UUID")
		}
		attemptID, err = uuid.Parse(*attempt)
		if err != nil {
			return errors.New("invalid attempt UUID")
		}
	}
	dsn := os.Getenv("AI_OPS_DATABASE_URL")
	if dsn == "" {
		return errors.New("AI_OPS_DATABASE_URL must be explicitly supplied by the operator")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || cfg.ConnConfig.Database != *dbName {
		return errors.New("invalid connection settings or database-name mismatch")
	}
	cfg.MaxConns = 2
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return errors.New("cannot initialize operator database connection")
	}
	defer db.Close()
	var actualDB string
	if err = db.QueryRow(ctx, `SELECT current_database()`).Scan(&actualDB); err != nil || actualDB != *dbName {
		return errors.New("cannot verify target database identity")
	}
	repo := repository.NewAIReservationRepository(db, nil)
	encode := json.NewEncoder(out)
	switch command {
	case "preflight":
		report, e := repo.AIReadiness(ctx, 15*time.Minute)
		if e != nil {
			return errors.New("readiness query failed; verify schema and operator permissions")
		}
		if e = encode.Encode(report); e != nil {
			return e
		}
		if !report.ReadyForEnforcement() {
			return errors.New("database is not ready for enforcement; no settings changed")
		}
		return nil
	case "inspect", "resolve":
		job, e := repo.InspectAIAttempt(ctx, orgID, attemptID)
		if e != nil {
			return errors.New("attempt not found in selected organization or metadata unavailable")
		}
		if command == "inspect" {
			return encode.Encode(job)
		}
		req, e := evidence.failure(job)
		if e != nil {
			return e
		}
		if !*apply {
			return encode.Encode(map[string]any{"dry_run": true, "attempt_id": attemptID, "outcome": req.Outcome, "reservation_status": job.ReservationStatus})
		}
		result, e := service.NewAIAccountingService(repo).Fail(ctx, req)
		if e != nil {
			return errors.New("resolution rejected or commit failed; inspect the attempt and retry the identical evidence only")
		}
		return encode.Encode(result)
	case "reconcile":
		if !*apply {
			return encode.Encode(map[string]any{"dry_run": true, "provider_lookup_available": false, "message": "--apply schedules and processes the local reconciliation queue; unknown calls remain held for manual resolution"})
		}
		worker, e := service.NewAIReconciliationWorker(repo, config.AIReconciliationConfig{Enabled: true, Interval: time.Minute, StaleAfter: 15 * time.Minute, LeaseDuration: 2 * time.Minute, LookupTimeout: 30 * time.Second, RetryDelay: 5 * time.Minute, BatchSize: 25, MaxClaims: 10}, nil, nil)
		if e != nil {
			return e
		}
		stats, e := worker.RunOnce(ctx)
		if e != nil {
			return errors.New("reconciliation failed; holds were not waived")
		}
		return encode.Encode(stats)
	}
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
