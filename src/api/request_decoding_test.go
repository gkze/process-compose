package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/f1bonacc1/process-compose/src/types"
)

// These tests pin the request handling the API had before huma, where JSON
// binding decoded every request body.

func TestRequestBodyIsJSONWhateverItsContentType(t *testing.T) {
	for _, contentType := range []string{"", "application/json", "application/json; charset=utf-8", "text/plain", "application/x-www-form-urlencoded"} {
		t.Run(contentType, func(t *testing.T) {
			var gotKeys string
			mock := &mockProject{sendProcessKeysFn: func(_ string, keys string) error {
				gotKeys = keys
				return nil
			}}
			req := httptest.NewRequest(http.MethodPost, "/process/send-keys/web", strings.NewReader(`{"keys":"q"}`))
			if contentType != "" {
				req.Header.Set("Content-Type", contentType)
			}
			w := httptest.NewRecorder()
			setupRouter(mock).ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
			}
			if gotKeys != "q" {
				t.Errorf("keys = %q, want %q", gotKeys, "q")
			}
		})
	}
}

// TestRequestBodyAcceptsNullsAndUnknownFields covers clients that encode a Go
// ProcessConfig (nil slices and maps become null) or send fields this
// version does not know.
func TestRequestBodyAcceptsNullsAndUnknownFields(t *testing.T) {
	var got *types.ProcessConfig
	mock := &mockProject{updateProcessFn: func(proc *types.ProcessConfig) error {
		got = proc
		return nil
	}}
	body := `{"name":"web","command":"sleep 1","environment":null,"namespace":null,"livenessProbe":null,"dependsOn":null,"vars":null,"futureField":5}`
	w := performRequest(setupRouter(mock), http.MethodPost, "/process", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if got == nil || got.Name != "web" || got.Command != "sleep 1" {
		t.Errorf("decoded process = %+v, want name web and command sleep 1", got)
	}
}

// TestRequestBodyIsValidated checks that a body that breaks the documented
// schema is rejected before the handler runs.
func TestRequestBodyIsValidated(t *testing.T) {
	called := false
	mock := &mockProject{updateProcessFn: func(*types.ProcessConfig) error {
		called = true
		return nil
	}}
	w := performRequest(setupRouter(mock), http.MethodPost, "/process", `{"name":"web","restartPolicy":{"restart":9}}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if called {
		t.Error("the handler ran for a body outside the documented schema")
	}
}

func TestRequestBodyHasNoSizeLimit(t *testing.T) {
	var gotEnvironment int
	mock := &mockProject{updateProjectFn: func(project *types.Project) (map[string]string, error) {
		gotEnvironment = len(project.Environment)
		return map[string]string{}, nil
	}}
	// about 2 MiB, twice huma's default limit
	environment := make([]string, 64*1024)
	for i := range environment {
		environment[i] = "VARIABLE=" + strings.Repeat("x", 22)
	}
	body, err := json.Marshal(map[string]any{"environment": environment})
	if err != nil {
		t.Fatal(err)
	}
	w := performRequest(setupRouter(mock), http.MethodPost, "/project", string(body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a %d byte body: %.200s", w.Code, len(body), w.Body.String())
	}
	if gotEnvironment != len(environment) {
		t.Errorf("decoded %d environment entries, want %d", gotEnvironment, len(environment))
	}
}

// TestInvalidRequestsAreBadRequests covers requests huma rejects before the
// handler runs: they are 400s with an ErrorResponse body, not 422s.
func TestInvalidRequestsAreBadRequests(t *testing.T) {
	for _, test := range []struct {
		method, path, body string
	}{
		{http.MethodPatch, "/process/scale/web/0", ""},
		{http.MethodPatch, "/process/signal/web/23", ""},
		{http.MethodGet, "/process/logs/web/x/1", ""},
		{http.MethodGet, "/project/state?withMemory=maybe", ""},
		{http.MethodPost, "/project", `{"processes":`},
		{http.MethodPatch, "/processes/stop", ""},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			w := performRequest(setupRouter(&mockProject{}), test.method, test.path, test.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
			}
			var response map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response["error"] == "" || len(response) != 1 {
				t.Errorf("body = %s, want {\"error\": ...}", w.Body.String())
			}
		})
	}
}

// TestInvalidBodyErrorDoesNotEchoTheBody keeps request bodies, which can
// carry environment secrets, out of error messages.
func TestInvalidBodyErrorDoesNotEchoTheBody(t *testing.T) {
	body := `{"environment":["DB_PASSWORD=hunter2"],"processes":{"web":{"replicas":"two"}}}`
	w := performRequest(setupRouter(&mockProject{}), http.MethodPost, "/project", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "hunter2") {
		t.Errorf("error response echoes the request body: %s", w.Body.String())
	}
}
