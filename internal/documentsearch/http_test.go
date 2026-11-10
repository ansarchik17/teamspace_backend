package documentsearch

import (
	"net/http"
	"net/http/httptest"
)

// Exercise real HTTP request/response encoding without TCP listeners, allowing
// the same tests to run in a restricted sandbox and normal CI.
type testRoundTripper func(*http.Request) (*http.Response, error)

func (f testRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type testHTTPService struct {
	URL     string
	handler http.Handler
}

func newTestHTTPService(handler http.Handler) *testHTTPService {
	return &testHTTPService{URL: "http://local-model.test", handler: handler}
}
func (s *testHTTPService) Close() {}
func (s *testHTTPService) Client() *http.Client {
	return &http.Client{Transport: testRoundTripper(func(r *http.Request) (*http.Response, error) {
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		recorder := httptest.NewRecorder()
		s.handler.ServeHTTP(recorder, r)
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		return recorder.Result(), nil
	})}
}
