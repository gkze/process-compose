package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const schemathesisConformanceEnv = "PC_SCHEMATHESIS_CONFORMANCE"

func schemathesisCommand(repositoryRoot, workingDirectory string, arguments ...string) *exec.Cmd {
	configuration := filepath.Join(repositoryRoot, "schemathesis.toml")
	pinnedArguments := []string{
		"run",
		"--locked",
		"--project", repositoryRoot,
		"--group", "conformance",
		"schemathesis",
		"--config-file", configuration,
	}

	command := exec.Command("uv", append(pinnedArguments, arguments...)...)
	command.Dir = workingDirectory
	return command
}

func runSchemathesisReadOnlyConformance(t *testing.T, snapshotRoot, schemaPath, baseURL string) {
	t.Helper()

	command := schemathesisCommand(
		snapshotRoot,
		snapshotRoot,
		"run",
		schemaPath,
		"--url", baseURL,
		"--include-method", http.MethodGet,
		"--exclude-operation-id", "LogsStream",
		"--exclude-operation-id", "StatesStream",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
			t.Fatalf("Schemathesis found a read-only HTTP contract violation:\n%s", output)
		}
		t.Fatalf("Schemathesis could not run the read-only HTTP contract check: %v\n%s", err, output)
	}
	t.Logf("Schemathesis read-only coverage:\n%s", output)
}

func TestSchemathesisDetectsUndocumentedStatus(t *testing.T) {
	if os.Getenv(schemathesisConformanceEnv) != "1" {
		t.Skipf("set %s=1 or run make check-openapi-contract", schemathesisConformanceEnv)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusTeapot)
		_, _ = writer.Write([]byte(`{"error":"intentionally undocumented"}`))
	}))
	t.Cleanup(server.Close)

	schemaPath := filepath.Join(t.TempDir(), "openapi.yaml")
	const schema = `openapi: 3.0.3
info:
  title: Schemathesis integration probe
  version: "1"
paths:
  /probe:
    get:
      operationId: Probe
      responses:
        "200":
          description: Expected response
          content:
            application/json:
              schema:
                type: object
                required: [status]
                properties:
                  status:
                    type: string
`
	if err := os.WriteFile(schemaPath, []byte(schema), 0o600); err != nil {
		t.Fatalf("write Schemathesis probe schema: %v", err)
	}

	repositoryRoot := filepath.Clean(filepath.Join(packageDirectory(t), "..", ".."))
	command := schemathesisCommand(
		repositoryRoot,
		filepath.Dir(schemaPath),
		"run",
		schemaPath,
		"--url", server.URL,
		"--phases", "coverage",
		"--checks", "status_code_conformance",
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("Schemathesis accepted an undocumented status:\n%s", output)
	}
	exitError, ok := err.(*exec.ExitError)
	if !ok || exitError.ExitCode() != 1 {
		t.Fatalf("Schemathesis probe exit = %v, want contract-failure exit 1:\n%s", err, output)
	}
	if !strings.Contains(string(output), "Undocumented HTTP status code") || !strings.Contains(string(output), "418") {
		t.Fatalf("Schemathesis failure did not identify the undocumented 418 response:\n%s", output)
	}
}
