package domain

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserHRProfileSummaryResponseJSONOmitsPhoto(t *testing.T) {
	t.Parallel()

	resp := UserHRProfileSummaryResponse{
		HRProfile: &UserHRProfileSummary{
			ID:         1,
			UserID:     42,
			PersonCode: "pc-1",
			FirstName:  "Jane",
			LastName:   "Doe",
		},
	}

	payload, err := json.Marshal(resp)
	require.NoError(t, err)
	require.NotContains(t, string(payload), "\"photo\"")
	require.NotContains(t, string(payload), "\"raw_payload\"")
}

func TestUserHRProfileResponseJSONIncludesPhoto(t *testing.T) {
	t.Parallel()

	resp := UserHRProfileResponse{
		HRProfile: &UserHRProfile{
			ID:         1,
			UserID:     42,
			PersonCode: "pc-1",
			FirstName:  "Jane",
			LastName:   "Doe",
			Photo:      "base64-photo",
		},
	}

	payload, err := json.Marshal(resp)
	require.NoError(t, err)
	require.Contains(t, string(payload), "\"photo\":\"base64-photo\"")
}
