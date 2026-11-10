// Package aiaccess denies integrations whose usage/price contract is unconfirmed.
// Observe mode does not authorize an unpriced external generation.
package aiaccess

import (
	"net/http"

	"github.com/west6ide/workspace-backend/internal/apperrors"
)

func ContractUnavailable() *apperrors.AppError {
	return apperrors.New(apperrors.ErrCodeAIAccountingUnavailable, "AI usage contract is not confirmed; external generation is unavailable", http.StatusServiceUnavailable)
}

// BlockedTransport is installed at unaccounted HTTP boundaries. No server env
// flag can bypass it. ReadOnly is reserved for existing job-status GETs.
type BlockedTransport struct{ ReadOnly bool }

func (t BlockedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.ReadOnly && (req.Method == http.MethodGet || req.Method == http.MethodHead) {
		return http.DefaultTransport.RoundTrip(req)
	}
	return nil, ContractUnavailable()
}
