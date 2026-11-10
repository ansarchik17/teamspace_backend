package aiaccess

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnaccountedTransportNeverDispatchesGeneration(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
	defer srv.Close()
	for _, readOnly := range []bool{false, true} {
		client := &http.Client{Transport: BlockedTransport{ReadOnly: readOnly}}
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			req, err := http.NewRequest(method, srv.URL, nil)
			require.NoError(t, err)
			_, err = client.Do(req)
			require.Error(t, err)
		}
	}
	require.Zero(t, calls.Load())
	client := &http.Client{Transport: BlockedTransport{ReadOnly: true}}
	resp, err := client.Get(srv.URL)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, int32(1), calls.Load())
}
