package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/auth"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (h *OrganizationHandler) IFormOperation(c *gin.Context, identity *AuthHandler) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
	var op domain.IFormOperation
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&op) != nil || !op.Valid() || !strings.HasPrefix(op.Subject, "workspace:") {
		h.respondError(c, apperrors.Validation("Некорректная операция iForm"))
		return
	}
	actor, err := strconv.ParseInt(strings.TrimPrefix(op.Subject, "workspace:"), 10, 64)
	if err != nil || actor <= 0 || serviceSubject(actor) != op.Subject {
		c.Status(400)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
	defer cancel()
	snapshot, err := h.service.IFormContext(ctx, actor, op.OrganizationID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	if !iformOperationAllowed(snapshot, op) {
		h.respondError(c, apperrors.Forbidden("Недостаточно прав для этой операции iForm"))
		return
	}
	response, err := h.service.IFormOperationReceipt(ctx, actor, op, func() (json.RawMessage, error) { return h.executeIFormOperation(ctx, identity, actor, op) })
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store, private")
	c.Data(200, "application/json", response)
}

func iformHRAllowed(snapshot *domain.IFormContext, op domain.IFormOperation) bool {
	if snapshot == nil || snapshot.OrganizationID != op.OrganizationID || snapshot.Actor != op.Subject {
		return false
	}
	for _, member := range snapshot.Members {
		if member.ID == op.Subject && member.Active {
			for _, role := range member.Roles {
				if role == "HR" || role == "TENANT_ADMIN" {
					return true
				}
			}
		}
	}
	return false
}

// Employee-published document flows keep the actual actor and normal ODO ACLs.
// Other corporate operations, including onboarding, retain the HR boundary.
func iformOperationAllowed(snapshot *domain.IFormContext, op domain.IFormOperation) bool {
	if op.ProcessInitiation != nil {
		if snapshot == nil || snapshot.OrganizationID != op.OrganizationID || snapshot.Actor != op.Subject || !op.ProcessInitiation.Valid() || op.EmployeeDocumentFlow {
			return false
		}
		for _, member := range snapshot.Members {
			if member.ID != op.Subject || !op.ProcessInitiation.Allows(member) {
				continue
			}
			switch op.Operation {
			case "workspace.odo.create", "workspace.odo.wait", "workspace.sd.create", "workspace.sd.wait":
				return true
			case "workspace.employee.create":
				return op.ProcessInitiation.Mode == "HR" && iformHRAllowed(snapshot, op)
			}
		}
		return false
	}
	if iformHRAllowed(snapshot, op) {
		return true
	}
	if snapshot == nil || snapshot.OrganizationID != op.OrganizationID || snapshot.Actor != op.Subject || !op.EmployeeDocumentFlow || (op.Operation != "workspace.odo.create" && op.Operation != "workspace.odo.wait") {
		return false
	}
	for _, member := range snapshot.Members {
		if member.ID == op.Subject && member.Active {
			for _, role := range member.Roles {
				if role == "EMPLOYEE" {
					return true
				}
			}
		}
	}
	return false
}

// This separate transient action never enters the operation receipt/hash ledger.
func (h *OrganizationHandler) IFormOnboardingCredential(c *gin.Context, identity *AuthHandler) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var input struct {
		OrganizationID uuid.UUID `json:"organization_id"`
		RequestID      uuid.UUID `json:"request_id"`
		Subject        string    `json:"subject"`
		Password       string    `json:"password"`
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || input.OrganizationID == uuid.Nil || input.RequestID == uuid.Nil || len(input.Password) > 512 {
		h.respondError(c, apperrors.Validation("Некорректные параметры первого входа"))
		return
	}
	actor, err := strconv.ParseInt(strings.TrimPrefix(input.Subject, "workspace:"), 10, 64)
	if err != nil || actor <= 0 || serviceSubject(actor) != input.Subject {
		c.Status(400)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 35*time.Second)
	defer cancel()
	snapshot, err := h.service.IFormContext(ctx, actor, input.OrganizationID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	if !iformHRAllowed(snapshot, domain.IFormOperation{OrganizationID: input.OrganizationID, Subject: input.Subject}) {
		h.respondError(c, apperrors.Forbidden("Первый вход настраивает HR этой заявки"))
		return
	}
	employee, err := h.service.IFormOnboardingEmployee(ctx, input.OrganizationID, input.RequestID, actor)
	if err == nil {
		err = identity.authService.InitializeIFormEmployeeCredential(ctx, actor, employee, input.OrganizationID, input.RequestID, input.Password)
	}
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store, private")
	respondJSON(c, 200, gin.H{"initialized": true, "employee_id": employee})
}

func (h *OrganizationHandler) executeIFormOperation(ctx context.Context, identity *AuthHandler, actor int64, op domain.IFormOperation) (json.RawMessage, error) {
	inputs := op.Inputs
	if op.Operation == "workspace.capabilities" {
		return json.Marshal(gin.H{"state": "SUCCEEDED", "contract_version": 1, "operations": []string{"workspace.odo.create", "workspace.odo.wait", "workspace.sd.create", "workspace.sd.wait", "workspace.employee.create"}})
	}
	if op.Operation == "workspace.employee.create" {
		var request auth.CreateUserRequest
		if raw, exists := inputs["start_date"]; exists && json.Unmarshal(raw, &request.StartDate) != nil {
			return nil, apperrors.New("EMPLOYMENT_DATE_INVALID", "invalid employment start date", 400)
		}
		encoded, _ := json.Marshal(inputs)
		if json.Unmarshal(encoded, &request) != nil || request.FirstName == "" || request.LastName == "" || request.Email == "" || len(request.IIN) != 12 || request.TeamID == nil || request.Position == "" || request.ManagerID == nil {
			return nil, apperrors.Validation("Заполните ФИО, email, ИИН, должность, подразделение и руководителя сотрудника")
		}
		// Only these explicit fields are accepted; no platform role, password or company override.
		for key := range inputs {
			switch key {
			case "first_name", "last_name", "email", "iin", "position", "team_id", "manager_id", "start_date":
			default:
				return nil, apperrors.Validation("Недопустимое поле кадровой операции")
			}
		}
		request.Email = strings.ToLower(strings.TrimSpace(request.Email))
		profile, err := identity.authService.ProvisionIFormEmployee(ctx, actor, op.OrganizationID, op.RequestID, &request)
		if err != nil {
			return nil, err
		}
		return json.Marshal(gin.H{"state": "SUCCEEDED", "employee_id": profile.ID, "username": profile.Username, "organization_id": profile.OrganizationID, "team_id": profile.TeamID, "manager_id": profile.ManagerID, "credential_setup_required": true, "employment_admission": profile.EmploymentAdmission})
	}
	if strings.HasPrefix(op.Operation, "workspace.odo.") {
		token, err := identity.authService.IFormODOToken(ctx, actor, op.OrganizationID)
		if err != nil {
			return nil, err
		}
		base := os.Getenv("ODO_COMMANDS_BASE_URL")
		if op.Operation == "workspace.odo.create" {
			body, err := iformInputObject(inputs, []string{"kind", "direction", "document_type_id", "title", "summary", "body", "recipient_department_id", "registrar_id", "executor_ids", "approver_ids", "due_date", "is_controlled", "workflow_plan", "main_file"})
			if err != nil {
				return nil, err
			}
			result, err := iformUpstream(ctx, base, "/api/v1/documents", http.MethodPost, "Bearer "+token, "", op.Key(), body)
			if err != nil {
				return nil, err
			}
			if !iformODORouteConfirmed(inputs, result) {
				return nil, apperrors.Internal("ОДО не подтвердил сохранение маршрута из iForm. Повторите после обновления ОДО")
			}
			id, _ := result["id"].(string)
			if id == "" {
				return nil, apperrors.Internal("ODO returned no document identity")
			}
			return iformCreatedDocumentResult(inputs, result, id)
		}
		if _, err := iformInputObject(inputs, []string{"document_id", "completion_status", "require_route_completion"}); err != nil {
			return nil, err
		}
		milestone := ""
		requireRoute := false
		if raw, exists := inputs["require_route_completion"]; exists {
			var required *bool
			if json.Unmarshal(raw, &required) != nil || required == nil {
				return nil, apperrors.New("IFORM_DOCUMENT_COMPLETION_INVALID", "invalid route completion requirement", 400)
			}
			requireRoute = *required
		}
		if raw, exists := inputs["completion_status"]; exists {
			if json.Unmarshal(raw, &milestone) != nil || milestone == "" {
				return nil, apperrors.New("IFORM_DOCUMENT_COMPLETION_INVALID", "invalid document completion milestone", 400)
			}
			if _, valid := domain.IFormDocumentCompletion("", milestone); !valid {
				return nil, apperrors.New("IFORM_DOCUMENT_COMPLETION_INVALID", "invalid document completion milestone", 400)
			}
		}
		if requireRoute && milestone == "" {
			return nil, apperrors.New("IFORM_DOCUMENT_COMPLETION_INVALID", "route completion requires an explicit milestone", 400)
		}
		id := iformInputString(inputs, "document_id")
		if !iformExternalID(id) {
			return nil, apperrors.Validation("Не указан документ ОДО")
		}
		result, err := iformUpstream(ctx, base, "/api/v1/documents/"+id, http.MethodGet, "Bearer "+token, "", "", nil)
		if err != nil {
			return nil, err
		}
		if result["id"] != id {
			return nil, apperrors.New("IFORM_DOCUMENT_STATUS_UNAVAILABLE", "ODO returned another document identity", 503)
		}
		if milestone == "completed" && iformDocumentNeedsFinish(result) {
			result, err = iformUpstream(ctx, base, "/api/v1/documents/"+id+"/workflow-plan/finish", http.MethodPost, "Bearer "+token, "", "", nil)
			if err != nil {
				return nil, err
			}
		}
		return iformDocumentObservation(result, id, milestone, requireRoute)
	}
	authorization, err := h.service.IFormSDAuthorization(ctx, actor)
	if err != nil {
		return nil, err
	}
	base := os.Getenv("SD_ACCESS_API_URL")
	if op.Operation == "workspace.sd.create" {
		body, employee, err := iformSDTicketBody(inputs, func(employee int64) error {
			created, err := h.service.IFormOnboardingEmployee(ctx, op.OrganizationID, op.RequestID, actor)
			if err != nil {
				return err
			}
			if created != employee {
				return apperrors.New("IFORM_ONBOARDING_EMPLOYEE_INVALID", "employee does not match this request", 409)
			}
			return identity.authService.ValidateIFormOnboardingEmployee(ctx, actor, employee, op.OrganizationID, op.RequestID)
		})
		if err != nil {
			return nil, err
		}
		result, err := iformUpstream(ctx, base, "/tickets", http.MethodPost, authorization.Token, authorization.Host, op.Key(), body)
		if err != nil {
			return nil, err
		}
		id, _ := result["id"].(string)
		parsedID, parseErr := uuid.Parse(id)
		if parseErr != nil || parsedID == uuid.Nil {
			return nil, apperrors.Internal("SD returned no ticket identity")
		}
		response := gin.H{"state": "SUCCEEDED", "ticket_id": id, "path": "/sd/tickets/" + id, "number": result["number"]}
		if employee > 0 {
			response["employee_id"] = employee
			response["start_date"] = iformInputString(inputs, "start_date")
		}
		return json.Marshal(response)
	}
	id := iformInputString(inputs, "ticket_id")
	if _, err = uuid.Parse(id); err != nil {
		return nil, apperrors.Validation("Не указана заявка SD")
	}
	result, err := iformUpstream(ctx, base, "/tickets/"+id, http.MethodGet, authorization.Token, authorization.Host, "", nil)
	if err != nil {
		return nil, err
	}
	status, _ := result["status"].(map[string]any)
	kind, _ := status["type"].(string)
	state := "WAITING"
	switch kind {
	case "completed":
		state = "SUCCEEDED"
	case "cancelled":
		state = "FAILED"
	}
	return json.Marshal(gin.H{"state": state, "ticket_id": id, "source_status": kind})
}

func iformInputString(inputs map[string]json.RawMessage, key string) string {
	var value string
	_ = json.Unmarshal(inputs[key], &value)
	return strings.TrimSpace(value)
}
func iformExternalID(id string) bool {
	if len(id) < 1 || len(id) > 100 {
		return false
	}
	for _, ch := range id {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_') {
			return false
		}
	}
	return true
}
func iformInputObject(inputs map[string]json.RawMessage, allowed []string) (json.RawMessage, error) {
	for key := range inputs {
		found := false
		for _, name := range allowed {
			if name == key {
				found = true
				break
			}
		}
		if !found {
			return nil, apperrors.Validation("Недопустимое поле операции интеграции")
		}
	}
	return json.Marshal(inputs)
}

// Only fixed, configured service origins and hard-coded resource paths are used.
// Browser credentials and caller-provided URLs are never forwarded.
func iformUpstream(ctx context.Context, base, path, method, authorization, host, key string, body json.RawMessage) (map[string]any, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, apperrors.New("IFORM_SERVICE_UNAVAILABLE", "Подключение сервиса не настроено", 503)
	}
	request, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", authorization)
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	if host != "" {
		if strings.ContainsAny(host, "/\\\r\n") {
			return nil, apperrors.Internal("Invalid SD tenant host")
		}
		request.Host = host
	}
	client := &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, apperrors.New("IFORM_SERVICE_UNAVAILABLE", "Сервис временно недоступен; повтор операции безопасен", 503)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode >= 500 {
			return nil, apperrors.New("IFORM_SERVICE_UNAVAILABLE", "Сервис временно недоступен; повтор операции безопасен", 503)
		}
		return nil, apperrors.New("IFORM_UPSTREAM_REJECTED", fmt.Sprintf("Сервис отклонил операцию (%d). Проверьте права, назначения и настройки этапа", response.StatusCode), http.StatusUnprocessableEntity)
	}
	var result map[string]any
	if json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&result) != nil {
		return nil, apperrors.Internal("Invalid integration response")
	}
	return result, nil
}

// A document can be "completed" between execution and review/signing.
// A configured route is authoritative; older ODO documents without one retain
// the existing status contract. Never release the next iForm stage early.
func iformODOWaitState(document map[string]interface{}) string {
	status, _ := document["status"].(string)
	switch status {
	case "rejected", "cancelled", "withdrawn":
		return "FAILED"
	}
	if plan, ok := document["workflow_plan"].(map[string]interface{}); ok {
		if stages, ok := plan["stages"].([]interface{}); ok && len(stages) > 0 {
			if phase, _ := plan["phase"].(string); phase == "completed" {
				return "SUCCEEDED"
			}
			return "WAITING"
		}
	}
	switch status {
	case "completed", "closed", "archived":
		return "SUCCEEDED"
	}
	return "WAITING"
}

// A safe status label for the iForm card, without exposing the whole document.
func iformODOStageName(document map[string]interface{}) string {
	plan, ok := document["workflow_plan"].(map[string]interface{})
	if !ok {
		return ""
	}
	phase, _ := plan["phase"].(string)
	switch phase {
	case "planned", "registration":
		return "Регистрация документа в ОДО"
	case "completed":
		return "Маршрут документа завершён"
	}
	stages, ok := plan["stages"].([]interface{})
	if !ok {
		return ""
	}
	index := float64(0) // Index zero is omitted by ODO JSON serialization.
	if raw, exists := plan["current_stage_index"]; exists {
		var valid bool
		index, valid = raw.(float64)
		if !valid {
			return ""
		}
	}
	if index < 0 || index >= float64(len(stages)) {
		return ""
	}
	stage, ok := stages[int(index)].(map[string]interface{})
	if !ok {
		return ""
	}
	name, _ := stage["name"].(string)
	if len(name) > 500 {
		return ""
	}
	return name
}

func iformODORouteConfirmed(inputs map[string]json.RawMessage, result map[string]interface{}) bool {
	raw, configured := inputs["workflow_plan"]
	if !configured {
		return true
	}
	var expected struct {
		Stages []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"stages"`
	}
	if json.Unmarshal(raw, &expected) != nil || len(expected.Stages) == 0 {
		return false
	}
	plan, ok := result["workflow_plan"].(map[string]interface{})
	if !ok {
		return false
	}
	stages, ok := plan["stages"].([]interface{})
	if !ok || len(stages) != len(expected.Stages) {
		return false
	}
	for i, raw := range stages {
		stage, ok := raw.(map[string]interface{})
		if !ok {
			return false
		}
		if stage["id"] != expected.Stages[i].ID || stage["kind"] != expected.Stages[i].Kind {
			return false
		}
	}
	return true
}
