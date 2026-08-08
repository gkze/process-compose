package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/f1bonacc1/process-compose/src/config"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

const (
	liveConformanceEnv = "PC_OPENAPI_LIVE_CONFORMANCE"
	liveToken          = "openapi-conformance-token"
)

var fixtureRunningAfterOperation = map[string]bool{
	"RestartProcess":   true,
	"StopProcess":      false,
	"StopProcesses":    false,
	"StartProcess":     true,
	"RestartNamespace": true,
	"StopNamespace":    false,
	"StartNamespace":   true,
}

var httpOperationOverrides = map[string]requestOverride{
	"StopProcesses": {
		body: []byte(`["fixture"]`),
	},
	"UpdateProcess": {
		body: []byte(`{"name":"fixture","replicaName":"fixture","command":"sleep 300","namespace":"conformance","replicas":1,"isInteractive":true}`),
	},
	"SendProcessKeys": {
		body: []byte(`{"keys":"x"}`),
	},
	"UpdateProject": {
		body: []byte(`{"version":"0.5","name":"openapi-conformance","processes":{"fixture":{"name":"fixture","replicaName":"fixture","command":"sleep 300","namespace":"conformance","replicas":1,"isInteractive":true}}}`),
	},
}

type operationLocation struct {
	method    string
	operation *openapi3.Operation
	path      string
}

type requestOverride struct {
	body       []byte
	omitBody   bool
	omitToken  bool
	pathValues map[string]string
	query      url.Values
	token      string
}

type contractCase struct {
	contractValid bool
	name          string
	operationID   string
	override      requestOverride
	wantStatus    int
}

type semanticContractCase struct {
	contractCase
	waitForFixtureStopped bool
	wantObject            map[string]string
}

var documentedAdditionalSuccessCases = []semanticContractCase{
	{
		contractCase: contractCase{
			name:          "StopProcesses returns partial results",
			operationID:   "StopProcesses",
			contractValid: true,
			override: requestOverride{
				body: []byte(`["fixture","missing"]`),
			},
			wantStatus: http.StatusMultiStatus,
		},
		waitForFixtureStopped: true,
		wantObject: map[string]string{
			"fixture": "ok",
			"missing": "process missing does not exist",
		},
	},
	{
		contractCase: contractCase{
			name:          "UpdateProject returns partial results",
			operationID:   "UpdateProject",
			contractValid: true,
			override: requestOverride{
				body: []byte(`{"version":"0.5","name":"openapi-conformance","processes":{"fixture":{"name":"fixture","replicaName":"missing","command":"sleep 300","description":"changed","namespace":"conformance","replicas":1,"isInteractive":true},"added":{"name":"added","replicaName":"added","command":"sleep 300","namespace":"conformance","replicas":1,"disabled":true}}}`),
			},
			wantStatus: http.StatusMultiStatus,
		},
		wantObject: map[string]string{
			"added":   "added",
			"fixture": "error",
		},
	},
	{
		contractCase: contractCase{
			name:          "ReloadProject returns partial results",
			operationID:   "ReloadProject",
			contractValid: true,
			wantStatus:    http.StatusMultiStatus,
		},
		wantObject: map[string]string{
			"added":   "added",
			"fixture": "error",
		},
	},
}

type liveResponse struct {
	body []byte
}

type liveContractHarness struct {
	baseURL    string
	client     *http.Client
	operations map[string]operationLocation
	router     routers.Router
}

type runningProcessCompose struct {
	authToken  string
	baseURL    string
	command    *exec.Cmd
	configPath string
	done       chan error
	once       sync.Once
	output     *os.File
	outputPath string
}

func TestFreshBinaryHTTPConformanceClientReturnsFirstRedirectResponse(t *testing.T) {
	router := http.NewServeMux()
	router.HandleFunc("/redirect", func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "/final", http.StatusTemporaryRedirect)
	})
	router.HandleFunc("/final", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	client := newLiveContractHTTPClient(5 * time.Second)
	response, err := client.Get(server.URL + "/redirect")
	if err != nil {
		t.Fatalf("call redirecting test server: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("first redirect response status = %d, want %d", response.StatusCode, http.StatusTemporaryRedirect)
	}
}

//nolint:maintidx // This top-level end-to-end test intentionally orchestrates the independent contract subtests.
func TestFreshBinaryHTTPConformance(t *testing.T) {
	if os.Getenv(liveConformanceEnv) != "1" {
		t.Skipf("set %s=1 or run make check-openapi-contract", liveConformanceEnv)
	}

	repositoryRoot := filepath.Clean(filepath.Join(packageDirectory(t), "..", ".."))
	revision := sourceRevision(t, repositoryRoot)
	snapshotRoot, snapshotRevision := createSourceSnapshot(t, repositoryRoot)
	documentPath := filepath.Join(snapshotRoot, "src", "docs", "swagger.json")
	loader := openapi3.NewLoader()
	document, err := loader.LoadFromFile(documentPath)
	if err != nil {
		t.Fatalf("load generated OpenAPI document: %v", err)
	}
	if err := document.Validate(context.Background()); err != nil {
		t.Fatalf("validate generated OpenAPI document: %v", err)
	}

	if document.OpenAPI != "3.0.3" {
		t.Errorf("generated contract version = %q, want OpenAPI 3.0.3", document.OpenAPI)
	}
	if got, want := firstServerURL(document), "http://localhost:8080/"; got != want {
		t.Errorf("generated contract server = %q, want %q for the binary's plain HTTP listener", got, want)
	}

	binaryPath := buildFreshProcessCompose(t, snapshotRoot, revision)
	assertFreshBinaryRevision(t, binaryPath, snapshotRevision, revision)
	running := startFreshProcessCompose(t, binaryPath, "")
	t.Cleanup(func() { running.stop(t) })

	client := newLiveContractHTTPClient(5 * time.Second)
	assertEmbeddedSwaggerInventory(t, client, running.baseURL, document)

	// Contract routing is host-agnostic during the harness because the freshly
	// built binary binds an ephemeral port rather than the documented default.
	document.Servers = nil
	router, err := gorillamux.NewRouter(document)
	if err != nil {
		t.Fatalf("create OpenAPI request router: %v", err)
	}
	harness := &liveContractHarness{
		baseURL:    running.baseURL,
		client:     client,
		operations: indexOperations(t, document),
		router:     router,
	}
	httpOperationIDs := sortedHTTPOperationIDs(harness.operations)
	assertDocumentedHTTPSuccessStatusCoverage(t, harness.operations, httpOperationIDs)

	t.Run("schemathesis-read-only-contract", func(t *testing.T) {
		if os.Getenv(schemathesisConformanceEnv) != "1" {
			t.Skipf("set %s=1 or run make check-openapi-contract", schemathesisConformanceEnv)
		}
		generated := startFreshProcessCompose(t, binaryPath, "")
		defer generated.stop(t)
		runSchemathesisReadOnlyConformance(t, snapshotRoot, documentPath, generated.baseURL)
	})

	t.Run("no-token-server-allows-anonymous", func(t *testing.T) {
		harness.exercise(t, "IsAlive", requestOverride{omitToken: true}, true, http.StatusOK)
	})

	t.Run("conditional-token-auth", func(t *testing.T) {
		authenticated := startFreshProcessCompose(t, binaryPath, liveToken)
		t.Cleanup(func() { authenticated.stop(t) })
		authHarness := *harness
		authHarness.baseURL = authenticated.baseURL

		for _, testCase := range []struct {
			name     string
			override requestOverride
		}{
			{name: "missing", override: requestOverride{omitToken: true}},
			{name: "invalid", override: requestOverride{token: "not-the-conformance-token"}},
		} {
			t.Run(testCase.name, func(t *testing.T) {
				authHarness.exercise(t, "IsAlive", testCase.override, true, http.StatusUnauthorized)
			})
		}

		authHarness.exercise(t, "IsAlive", requestOverride{}, true, http.StatusOK)
	})

	t.Run("generated-invalid-and-boundary-requests", func(t *testing.T) {
		cases := []contractCase{
			{
				name:          "UpdateProject requires a body",
				operationID:   "UpdateProject",
				contractValid: false,
				override:      requestOverride{omitBody: true},
				wantStatus:    http.StatusBadRequest,
			},
			{
				name:          "UpdateProject rejects malformed JSON",
				operationID:   "UpdateProject",
				contractValid: false,
				override:      requestOverride{body: []byte("{")},
				wantStatus:    http.StatusBadRequest,
			},
			{
				name:          "StopProcesses requires a body",
				operationID:   "StopProcesses",
				contractValid: false,
				override:      requestOverride{omitBody: true},
				wantStatus:    http.StatusBadRequest,
			},
			{
				name:          "ScaleProcess rejects below minimum",
				operationID:   "ScaleProcess",
				contractValid: false,
				override: requestOverride{pathValues: map[string]string{
					"name":  "fixture",
					"scale": "0",
				}},
				wantStatus: http.StatusBadRequest,
			},
			{
				name:          "SendSignal rejects below minimum",
				operationID:   "SendSignal",
				contractValid: false,
				override: requestOverride{pathValues: map[string]string{
					"name":   "fixture",
					"signal": "-1",
				}},
				wantStatus: http.StatusBadRequest,
			},
			{
				name:          "SendSignal rejects above maximum",
				operationID:   "SendSignal",
				contractValid: false,
				override: requestOverride{pathValues: map[string]string{
					"name":   "fixture",
					"signal": "23",
				}},
				wantStatus: http.StatusBadRequest,
			},
			{
				name:          "SendSignal accepts maximum",
				operationID:   "SendSignal",
				contractValid: true,
				override: requestOverride{pathValues: map[string]string{
					"name":   "fixture",
					"signal": "22",
				}},
				wantStatus: http.StatusOK,
			},
		}

		for _, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				harness.exercise(t, testCase.operationID, testCase.override, testCase.contractValid, testCase.wantStatus)
			})
		}
	})

	t.Run("project-state-memory-query", func(t *testing.T) {
		withoutMemory := harness.exercise(t, "GetProjectState", requestOverride{}, true, http.StatusOK)
		withoutObject := decodeJSONObject(t, withoutMemory.body)
		if _, exists := withoutObject["memoryState"]; exists {
			t.Errorf("GetProjectState without withMemory unexpectedly returned memoryState")
		}

		withMemory := harness.exercise(t, "GetProjectState", requestOverride{query: url.Values{
			"withMemory": []string{"true"},
		}}, true, http.StatusOK)
		withObject := decodeJSONObject(t, withMemory.body)
		memoryRaw, exists := withObject["memoryState"]
		if !exists {
			t.Fatalf("GetProjectState with withMemory=true omitted memoryState")
		}
		assertJSONKeys(t, memoryRaw, "allocated", "totalAllocated", "systemMemory", "gcCycles")
	})

	t.Run("documented-additional-success-responses", func(t *testing.T) {
		for _, testCase := range documentedAdditionalSuccessCases {
			t.Run(testCase.name, func(t *testing.T) {
				isolated := startFreshProcessCompose(t, binaryPath, "")
				defer isolated.stop(t)
				operationHarness := *harness
				operationHarness.baseURL = isolated.baseURL
				operationHarness.waitForFixtureState(t, true)
				if testCase.operationID == "ReloadProject" {
					writePartialReloadConfiguration(t, isolated.configPath)
				}

				response := operationHarness.exercise(
					t,
					testCase.operationID,
					testCase.override,
					testCase.contractValid,
					testCase.wantStatus,
				)
				assertJSONStringObject(t, testCase.operationID, response.body, testCase.wantObject)
				if testCase.waitForFixtureStopped {
					operationHarness.waitForFixtureState(t, false)
				}
			})
		}
	})

	t.Run("all-documented-http-operations", func(t *testing.T) {
		for _, operationID := range httpOperationIDs {
			t.Run(operationID, func(t *testing.T) {
				fixtureStartsDisabled := operationID == "StartProcess"
				isolated := startFreshProcessComposeWithFixture(t, binaryPath, "", fixtureStartsDisabled)
				defer isolated.stop(t)
				operationHarness := *harness
				operationHarness.baseURL = isolated.baseURL
				operationHarness.waitForFixtureState(t, !fixtureStartsDisabled)
				if operationID == "SendProcessKeys" {
					operationHarness.waitForProcessKeys(t)
				}
				response := operationHarness.exercise(t, operationID, httpOperationOverrides[operationID], true, http.StatusOK)
				if operationID == "StopProcesses" {
					assertJSONStringObject(t, operationID, response.body, map[string]string{"fixture": "ok"})
				}
				if running, transitionsProcess := fixtureRunningAfterOperation[operationID]; transitionsProcess {
					operationHarness.waitForFixtureState(t, running)
				}
			})
		}
	})
}

func newLiveContractHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func writePartialReloadConfiguration(t *testing.T, configPath string) {
	t.Helper()
	// The loader accepts a negative replica count and preserves the explicit
	// replica name. The runtime rejects that missing process after adding the
	// other one, yielding a bounded partial reload without external dependencies.
	configuration := []byte(`version: "0.5"
name: openapi-conformance
processes:
  fixture:
    command: "sleep 300"
    description: changed
    namespace: conformance
    is_interactive: true
    replicas: -1
    replica_name: missing
  added:
    command: "sleep 300"
    namespace: conformance
    disabled: true
`)
	if err := os.WriteFile(configPath, configuration, 0o600); err != nil {
		t.Fatalf("write partial reload configuration: %v", err)
	}
}

func (harness *liveContractHarness) waitForFixtureState(t *testing.T, wantRunning bool) {
	t.Helper()
	location := harness.operations["GetProcess"]
	deadline := time.Now().Add(5 * time.Second)
	var lastResponse string
	for time.Now().Before(deadline) {
		request := harness.requestFor(t, location, requestOverride{})
		response, err := harness.client.Do(request)
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK {
				var state struct {
					IsRunning bool `json:"is_running"`
				}
				if err := json.Unmarshal(body, &state); err == nil && state.IsRunning == wantRunning {
					return
				}
			}
			lastResponse = fmt.Sprintf("status=%d body=%s", response.StatusCode, body)
		} else {
			lastResponse = err.Error()
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("fixture running state did not become %t: %s", wantRunning, lastResponse)
}

func (harness *liveContractHarness) waitForProcessKeys(t *testing.T) {
	t.Helper()
	location := harness.operations["SendProcessKeys"]
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		request := harness.requestFor(t, location, httpOperationOverrides["SendProcessKeys"])
		response, err := harness.client.Do(request)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("wait for interactive process: unexpected status %d", response.StatusCode)
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("interactive process did not become ready for SendProcessKeys")
}

func buildFreshProcessCompose(t *testing.T, repositoryRoot, revision string) string {
	t.Helper()
	tempDirectory := t.TempDir()
	binaryPath := filepath.Join(tempDirectory, "process-compose")
	linkerFlags := strings.Join([]string{
		"-X github.com/f1bonacc1/process-compose/src/config.Version=openapi-conformance",
		"-X github.com/f1bonacc1/process-compose/src/config.Commit=" + revision,
		"-X github.com/f1bonacc1/process-compose/src/config.CheckForUpdates=false",
	}, " ")
	build := exec.Command("go", "build", "-buildvcs=true", "-trimpath", "-ldflags", linkerFlags, "-o", binaryPath, ".")
	build.Dir = repositoryRoot
	build.Env = withEnvironmentValue(os.Environ(), "CGO_ENABLED", "0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fresh process-compose binary: %v\n%s", err, output)
	}
	return binaryPath
}

func startFreshProcessCompose(t *testing.T, binaryPath, authToken string) *runningProcessCompose {
	return startFreshProcessComposeWithFixture(t, binaryPath, authToken, false)
}

func startFreshProcessComposeWithFixture(t *testing.T, binaryPath, authToken string, fixtureDisabled bool) *runningProcessCompose {
	t.Helper()
	tempDirectory := t.TempDir()
	configPath := filepath.Join(tempDirectory, "process-compose.yaml")
	disabledSetting := ""
	if fixtureDisabled {
		disabledSetting = "    disabled: true\n"
	}
	yamlConfiguration := []byte(fmt.Sprintf(`version: "0.5"
name: openapi-conformance
processes:
  fixture:
    command: "sleep 300"
    namespace: conformance
    is_interactive: true
%s`, disabledSetting))
	if err := os.WriteFile(configPath, yamlConfiguration, 0o600); err != nil {
		t.Fatalf("write conformance configuration: %v", err)
	}

	port := reserveTCPPort(t)
	outputPath := filepath.Join(tempDirectory, "process-compose.output.log")
	outputFile, err := os.Create(outputPath)
	if err != nil {
		t.Fatalf("create server output log: %v", err)
	}
	command := exec.Command(
		binaryPath,
		"--config", configPath,
		"--tui=false",
		"--keep-project",
		"--disable-dotenv",
		"--address", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"--log-file", filepath.Join(tempDirectory, "process-compose.log"),
	)
	command.Dir = tempDirectory
	command.Env = processComposeEnvironment(authToken)
	if runtime.GOOS == "darwin" {
		// Darwin port discovery invokes lsof for each socket family. Stub that
		// host-wide system boundary so GetProcessPorts remains deterministic.
		lsofPath := filepath.Join(tempDirectory, "lsof")
		if err := os.WriteFile(lsofPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatalf("write hermetic lsof stub: %v", err)
		}
		command.Env = withEnvironmentValue(
			command.Env,
			"PATH",
			tempDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
		)
	}
	command.Stdout = outputFile
	command.Stderr = outputFile
	configureProcessGroup(command)
	if err := command.Start(); err != nil {
		_ = outputFile.Close()
		t.Fatalf("start fresh process-compose binary: %v", err)
	}
	running := &runningProcessCompose{
		authToken:  authToken,
		baseURL:    fmt.Sprintf("http://127.0.0.1:%d", port),
		command:    command,
		configPath: configPath,
		done:       make(chan error, 1),
		output:     outputFile,
		outputPath: outputPath,
	}
	go func() { running.done <- command.Wait() }()

	readinessClient := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-running.done:
			_ = outputFile.Close()
			t.Fatalf("fresh process-compose exited before readiness: %v\n%s", err, readServerOutput(outputPath))
		default:
		}
		request, err := http.NewRequest(http.MethodGet, running.baseURL+"/live", nil)
		if err != nil {
			t.Fatalf("create readiness request: %v", err)
		}
		if authToken != "" {
			request.Header.Set(config.TokenHeader, authToken)
		}
		response, err := readinessClient.Do(request)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return running
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	running.stop(t)
	t.Fatalf("fresh process-compose did not become ready\n%s", readServerOutput(outputPath))
	return nil
}

func processComposeEnvironment(authToken string) []string {
	environment := make([]string, 0, len(os.Environ())+1)
	for _, variable := range os.Environ() {
		if isProcessComposeSetting(variable) {
			continue
		}
		environment = append(environment, variable)
	}
	if authToken != "" {
		environment = append(environment, config.EnvVarApiToken+"="+authToken)
	}
	return environment
}

func withEnvironmentValue(environment []string, name, value string) []string {
	replaced := make([]string, 0, len(environment)+1)
	for _, variable := range environment {
		variableName, _, _ := strings.Cut(variable, "=")
		if !strings.EqualFold(variableName, name) {
			replaced = append(replaced, variable)
		}
	}
	return append(replaced, name+"="+value)
}

func isProcessComposeSetting(variable string) bool {
	name, _, _ := strings.Cut(variable, "=")
	name = strings.ToUpper(name)
	return strings.HasPrefix(name, "PC_") || name == "PROC_COMP_CONFIG" || name == "COMPOSE_SHELL"
}

func TestProcessComposeEnvironmentIsHermetic(t *testing.T) {
	inheritedSettings := []string{
		"PC_NO_SERVER",
		"PC_SOCKET_PATH",
		"PC_NAMESPACES",
		config.EnvVarApiTokenPath,
		"PROC_COMP_CONFIG",
		"pc_api_token",
		"Proc_Comp_Config",
		"COMPOSE_SHELL",
	}
	for _, variable := range inheritedSettings {
		t.Setenv(variable, "inherited-host-setting")
	}

	withoutToken := processComposeEnvironment("")
	assertEnvironmentOmits(t, withoutToken, inheritedSettings...)

	withToken := processComposeEnvironment(liveToken)
	for _, inheritedSetting := range inheritedSettings {
		if strings.EqualFold(inheritedSetting, config.EnvVarApiToken) {
			continue
		}
		assertEnvironmentOmits(t, withToken, inheritedSetting)
	}
	injectedTokenCount := 0
	for _, variable := range withToken {
		if strings.EqualFold(strings.SplitN(variable, "=", 2)[0], config.EnvVarApiToken) {
			if variable != config.EnvVarApiToken+"="+liveToken {
				t.Fatalf("token child inherited Process Compose setting %q", variable)
			}
			injectedTokenCount++
		}
	}
	if injectedTokenCount != 1 {
		t.Fatalf("token child has %d API token settings, want exactly one", injectedTokenCount)
	}
}

func TestWithEnvironmentValueReplacesInheritedValues(t *testing.T) {
	environment := []string{
		"PATH=/inherited/first",
		"OTHER=preserved",
		"path=/inherited/second",
	}
	got := withEnvironmentValue(environment, "PATH", "/hermetic/bin")

	if strings.Join(got, "\n") != "OTHER=preserved\nPATH=/hermetic/bin" {
		t.Fatalf("replaced environment = %q", got)
	}
}

func assertEnvironmentOmits(t *testing.T, environment []string, omittedNames ...string) {
	t.Helper()
	for _, variable := range environment {
		name, _, _ := strings.Cut(variable, "=")
		for _, omittedName := range omittedNames {
			if strings.EqualFold(name, omittedName) {
				t.Fatalf("child inherited Process Compose setting %q", variable)
			}
		}
	}
}

func (running *runningProcessCompose) stop(t *testing.T) {
	t.Helper()
	running.once.Do(func() {
		request, _ := http.NewRequest(http.MethodPost, running.baseURL+"/project/stop", nil)
		if running.authToken != "" {
			request.Header.Set(config.TokenHeader, running.authToken)
		}
		client := &http.Client{Timeout: time.Second}
		if response, err := client.Do(request); err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}

		select {
		case <-running.done:
		case <-time.After(3 * time.Second):
			_ = running.command.Process.Signal(os.Interrupt)
			select {
			case <-running.done:
			case <-time.After(2 * time.Second):
				_ = killProcessGroup(running.command)
				<-running.done
			}
		}
		_ = running.output.Close()
	})
}

func reserveTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve TCP port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func sourceRevision(t *testing.T, repositoryRoot string) string {
	t.Helper()
	headCommand := exec.Command("git", "rev-parse", "HEAD")
	headCommand.Dir = repositoryRoot
	headOutput, err := headCommand.Output()
	if err != nil {
		t.Fatalf("read source revision: %v", err)
	}
	return strings.TrimSpace(string(headOutput))
}

func assertFreshBinaryRevision(t *testing.T, binaryPath, snapshotRevision, sourceRevision string) {
	t.Helper()
	metadataCommand := exec.Command("go", "version", "-m", binaryPath)
	metadata, err := metadataCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("read fresh binary build metadata: %v\n%s", err, metadata)
	}
	if !strings.Contains(string(metadata), "vcs.revision="+snapshotRevision) && !strings.Contains(string(metadata), "vcs.revision\t"+snapshotRevision) {
		t.Fatalf("fresh binary VCS metadata does not identify source snapshot revision %s\n%s", snapshotRevision, metadata)
	}

	versionCommand := exec.Command(binaryPath, "version")
	output, err := versionCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("read fresh binary version: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Commit:         "+sourceRevision) {
		t.Fatalf("fresh binary does not report source base revision %s\n%s", sourceRevision, output)
	}
	t.Logf("fresh binary provenance: source base %s, immutable snapshot %s", sourceRevision, snapshotRevision)
}

func assertEmbeddedSwaggerInventory(t *testing.T, client *http.Client, baseURL string, document *openapi3.T) {
	t.Helper()
	response, err := client.Get(baseURL + "/swagger/doc.json")
	if err != nil {
		t.Fatalf("read embedded Swagger from fresh binary: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("read embedded Swagger: status %d", response.StatusCode)
	}
	var embedded struct {
		Swagger  string                `json:"swagger"`
		Security []map[string][]string `json:"security"`
		Paths    map[string]map[string]struct {
			OperationID string `json:"operationId"`
		} `json:"paths"`
	}
	if err := json.NewDecoder(response.Body).Decode(&embedded); err != nil {
		t.Fatalf("decode embedded Swagger: %v", err)
	}
	if embedded.Swagger != "2.0" {
		t.Errorf("fresh binary embedded contract version = %q, want Swagger 2.0", embedded.Swagger)
	}
	embeddedSecurity, err := json.Marshal(embedded.Security)
	if err != nil {
		t.Fatalf("encode embedded Swagger security: %v", err)
	}
	generatedSecurity, err := json.Marshal(document.Security)
	if err != nil {
		t.Fatalf("encode generated OpenAPI security: %v", err)
	}
	if !bytes.Equal(embeddedSecurity, generatedSecurity) {
		t.Errorf("fresh binary embedded security = %s, generated OpenAPI security = %s", embeddedSecurity, generatedSecurity)
	}

	embeddedInventory := make([]string, 0)
	for path, pathItem := range embedded.Paths {
		for method, operation := range pathItem {
			embeddedInventory = append(embeddedInventory, strings.ToUpper(method)+" "+path+" "+operation.OperationID)
		}
	}
	sort.Strings(embeddedInventory)
	generatedInventory := operationInventory(document)
	if strings.Join(embeddedInventory, "\n") != strings.Join(generatedInventory, "\n") {
		t.Errorf("fresh binary embedded Swagger inventory differs from generated OpenAPI\nembedded:\n%s\ngenerated:\n%s", strings.Join(embeddedInventory, "\n"), strings.Join(generatedInventory, "\n"))
	}
}

func operationInventory(document *openapi3.T) []string {
	inventory := make([]string, 0)
	for _, path := range document.Paths.InMatchingOrder() {
		for method, operation := range document.Paths.Value(path).Operations() {
			inventory = append(inventory, method+" "+path+" "+operation.OperationID)
		}
	}
	sort.Strings(inventory)
	return inventory
}

func indexOperations(t *testing.T, document *openapi3.T) map[string]operationLocation {
	t.Helper()
	operations := make(map[string]operationLocation)
	for _, path := range document.Paths.InMatchingOrder() {
		for method, operation := range document.Paths.Value(path).Operations() {
			if operation.OperationID == "" {
				t.Fatalf("%s %s is missing operationId", method, path)
			}
			if _, duplicate := operations[operation.OperationID]; duplicate {
				t.Fatalf("duplicate operationId %q", operation.OperationID)
			}
			operations[operation.OperationID] = operationLocation{method: method, operation: operation, path: path}
		}
	}
	return operations
}

func sortedHTTPOperationIDs(operations map[string]operationLocation) []string {
	operationIDs := make([]string, 0, len(operations))
	for operationID := range operations {
		if _, isWebSocket := webSocketOperation(operationID); !isWebSocket {
			operationIDs = append(operationIDs, operationID)
		}
	}
	sort.Strings(operationIDs)
	return operationIDs
}

func assertDocumentedHTTPSuccessStatusCoverage(t *testing.T, operations map[string]operationLocation, operationIDs []string) {
	t.Helper()
	covered := make(map[string]map[int]struct{}, len(operationIDs))
	for _, operationID := range operationIDs {
		covered[operationID] = map[int]struct{}{http.StatusOK: {}}
	}
	for _, testCase := range documentedAdditionalSuccessCases {
		statuses := covered[testCase.operationID]
		if statuses == nil {
			statuses = make(map[int]struct{})
			covered[testCase.operationID] = statuses
		}
		statuses[testCase.wantStatus] = struct{}{}
	}

	for _, operationID := range operationIDs {
		location := operations[operationID]
		documented := make(map[int]struct{})
		for _, statusKey := range location.operation.Responses.Keys() {
			if len(statusKey) != 3 || statusKey[0] != '2' {
				continue
			}
			status, err := strconv.Atoi(statusKey)
			if err != nil {
				t.Errorf("%s documents non-numeric success response %q, which the live case inventory cannot exercise exactly", operationID, statusKey)
				continue
			}
			documented[status] = struct{}{}
		}
		for status := range documented {
			if _, ok := covered[operationID][status]; !ok {
				t.Errorf("%s documents success response %d without a live black-box case", operationID, status)
			}
		}
		for status := range covered[operationID] {
			if _, ok := documented[status]; !ok {
				t.Errorf("%s live black-box case expects undocumented success response %d", operationID, status)
			}
		}
	}
}

func (harness *liveContractHarness) exercise(t *testing.T, operationID string, override requestOverride, contractValid bool, wantStatus int) liveResponse {
	t.Helper()
	location, ok := harness.operations[operationID]
	if !ok {
		t.Fatalf("operation %q is missing from generated OpenAPI", operationID)
	}
	if _, isWebSocket := webSocketOperation(operationID); isWebSocket {
		t.Fatalf("WebSocket operation %q requires the separate WebSocket harness", operationID)
	}

	validationRequest := harness.requestFor(t, location, override)
	route, pathParameters, err := harness.router.FindRoute(validationRequest)
	if err != nil {
		t.Fatalf("match generated request to OpenAPI route: %v", err)
	}
	requestInput := &openapi3filter.RequestValidationInput{
		Request:    validationRequest,
		PathParams: pathParameters,
		Route:      route,
		Options: &openapi3filter.Options{
			AuthenticationFunc: validateAPITokenSecurity,
			MultiError:         true,
		},
	}
	validationErr := openapi3filter.ValidateRequest(context.Background(), requestInput)
	if contractValid && validationErr != nil {
		t.Fatalf("generated request is invalid according to OpenAPI: %v", validationErr)
	}
	if !contractValid && validationErr == nil {
		t.Fatalf("generated invalid request was accepted by OpenAPI")
	}

	liveRequest := harness.requestFor(t, location, override)
	response, err := harness.client.Do(liveRequest)
	if err != nil {
		t.Fatalf("call fresh binary: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatalf("read fresh binary response: %v", readErr)
	}
	if response.StatusCode != wantStatus {
		t.Errorf("fresh binary status = %d, want %d: %s", response.StatusCode, wantStatus, body)
	}
	if location.operation.Responses.Status(response.StatusCode) == nil && location.operation.Responses.Default() == nil {
		t.Errorf("fresh binary returned undocumented status %d: %s", response.StatusCode, body)
	}
	responseInput := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: requestInput,
		Status:                 response.StatusCode,
		Header:                 response.Header,
		Options: &openapi3filter.Options{
			IncludeResponseStatus: true,
			MultiError:            true,
		},
	}
	responseInput.SetBodyBytes(body)
	if err := openapi3filter.ValidateResponse(context.Background(), responseInput); err != nil {
		t.Errorf("fresh binary response does not match OpenAPI: %v\nstatus=%d body=%s", err, response.StatusCode, body)
	}
	assertSemanticResponse(t, operationID, response.StatusCode, body)
	return liveResponse{body: body}
}

func validateAPITokenSecurity(_ context.Context, input *openapi3filter.AuthenticationInput) error {
	if input.SecuritySchemeName != "ApiTokenAuth" {
		return input.NewError(fmt.Errorf("unexpected security scheme %q", input.SecuritySchemeName))
	}
	if input.RequestValidationInput.Request.Header.Get(config.TokenHeader) != liveToken {
		return input.NewError(fmt.Errorf("missing or invalid %s header", config.TokenHeader))
	}
	return nil
}

func (harness *liveContractHarness) requestFor(t *testing.T, location operationLocation, override requestOverride) *http.Request {
	t.Helper()
	path := location.path
	query := make(url.Values)
	for _, parameterRef := range location.operation.Parameters {
		parameter := parameterRef.Value
		if parameter == nil {
			continue
		}
		switch parameter.In {
		case openapi3.ParameterInPath:
			value := syntheticParameterValue(location, parameter)
			if overrideValue, exists := override.pathValues[parameter.Name]; exists {
				value = overrideValue
			}
			path = strings.ReplaceAll(path, "{"+parameter.Name+"}", url.PathEscape(value))
		case openapi3.ParameterInQuery:
			if parameter.Required {
				query.Set(parameter.Name, syntheticParameterValue(location, parameter))
			}
		}
	}
	for name, values := range override.query {
		query[name] = append([]string(nil), values...)
	}

	var body []byte
	if override.body != nil {
		body = append([]byte(nil), override.body...)
	} else if !override.omitBody && location.operation.RequestBody != nil && location.operation.RequestBody.Value != nil {
		mediaType := location.operation.RequestBody.Value.Content.Get("application/json")
		if mediaType == nil || mediaType.Schema == nil {
			t.Fatalf("%s request body has no application/json schema", location.operation.OperationID)
		}
		value := syntheticSchemaValue(mediaType.Schema)
		var err error
		body, err = json.Marshal(value)
		if err != nil {
			t.Fatalf("encode generated request body: %v", err)
		}
	}

	requestURL := harness.baseURL + path
	if encoded := query.Encode(); encoded != "" {
		requestURL += "?" + encoded
	}
	request, err := http.NewRequest(location.method, requestURL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create generated request: %v", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if !override.omitToken {
		token := liveToken
		if override.token != "" {
			token = override.token
		}
		request.Header.Set(config.TokenHeader, token)
	}
	return request
}

func syntheticParameterValue(location operationLocation, parameter *openapi3.Parameter) string {
	if parameter.Name == "name" {
		if strings.HasPrefix(location.path, "/namespace/") {
			return "conformance"
		}
		return "fixture"
	}
	if parameter.Schema != nil && parameter.Schema.Value != nil {
		schema := parameter.Schema.Value
		if schema.Default != nil {
			return fmt.Sprint(schema.Default)
		}
		if schema.Min != nil {
			return strconv.FormatFloat(*schema.Min, 'f', -1, 64)
		}
		if schema.Type != nil && schema.Type.Is(openapi3.TypeInteger) {
			if parameter.Name == "limit" {
				return "10"
			}
			return "0"
		}
		if schema.Type != nil && schema.Type.Is(openapi3.TypeBoolean) {
			return "false"
		}
	}
	return "value"
}

func syntheticSchemaValue(reference *openapi3.SchemaRef) any {
	if reference == nil || reference.Value == nil {
		return nil
	}
	schema := reference.Value
	if schema.Example != nil {
		return schema.Example
	}
	if schema.Default != nil {
		return schema.Default
	}
	if len(schema.Enum) > 0 {
		return schema.Enum[0]
	}
	if schema.Type != nil {
		switch {
		case schema.Type.Is(openapi3.TypeObject):
			value := make(map[string]any, len(schema.Required))
			for _, name := range schema.Required {
				value[name] = syntheticSchemaValue(schema.Properties[name])
			}
			return value
		case schema.Type.Is(openapi3.TypeArray):
			return []any{}
		case schema.Type.Is(openapi3.TypeBoolean):
			return false
		case schema.Type.Is(openapi3.TypeInteger), schema.Type.Is(openapi3.TypeNumber):
			if schema.Min != nil {
				return *schema.Min
			}
			return 0
		case schema.Type.Is(openapi3.TypeString):
			return "value"
		}
	}
	return map[string]any{}
}

func assertSemanticResponse(t *testing.T, operationID string, status int, body []byte) {
	t.Helper()
	if status >= 400 {
		object := decodeJSONObject(t, body)
		errorValue, exists := object["error"]
		if !exists {
			t.Errorf("error response is missing required key %q: %s", "error", body)
			return
		}
		var message string
		if err := json.Unmarshal(errorValue, &message); err != nil || message == "" {
			t.Errorf("error response key %q is not a non-empty string: %s", "error", body)
		}
		assertNoPascalCaseJSONKeys(t, body, "$")
		return
	}

	expectedKeys := map[string][]string{
		"IsAlive":             {"status"},
		"GetProjectName":      {"projectName"},
		"GetProjectState":     {"fileNames", "upTime", "startTime", "processNum", "runningProcessNum", "userName", "version", "projectName"},
		"GetProcesses":        {"data"},
		"GetProcess":          {"name", "namespace", "status", "system_time", "age", "is_ready", "has_ready_probe", "restarts", "exit_code", "pid", "is_elevated", "password_provided", "mem", "cpu", "is_running"},
		"GetProcessInfo":      {"name", "command", "restartPolicy", "shutDownParams"},
		"GetProcessPorts":     {"name", "tcp_ports", "udp_ports"},
		"GetProcessLogs":      {"logs"},
		"GetDependencyGraph":  {"nodes"},
		"TruncateProcessLogs": {"name"},
		"ScaleProcess":        {"name"},
		"SendSignal":          {"name"},
		"StartProcess":        {"name"},
		"RestartProcess":      {"name"},
		"StopProcess":         {"name"},
		"SendProcessKeys":     {"name"},
		"StartNamespace":      {"name"},
		"StopNamespace":       {"name"},
		"RestartNamespace":    {"name"},
		"UpdateProcess":       {"name", "command", "replicaName", "isInteractive"},
		"ShutDownProject":     {"status"},
	}
	if operationID == "GetNamespaces" {
		var array []any
		if err := json.Unmarshal(body, &array); err != nil {
			t.Errorf("GetNamespaces response is not an array: %v\n%s", err, body)
		}
		return
	}
	if keys := expectedKeys[operationID]; len(keys) > 0 {
		assertJSONKeys(t, body, keys...)
	}
	if len(body) > 0 {
		assertNoPascalCaseJSONKeys(t, body, "$")
	}
}

func assertJSONKeys(t *testing.T, body []byte, keys ...string) {
	t.Helper()
	object := decodeJSONObject(t, body)
	for _, key := range keys {
		if _, exists := object[key]; !exists {
			t.Errorf("response is missing required semantic key %q: %s", key, body)
		}
	}
}

func assertJSONStringObject(t *testing.T, operationID string, body []byte, want map[string]string) {
	t.Helper()
	object := decodeJSONObject(t, body)
	if len(object) != len(want) {
		t.Errorf("%s response has %d entries, want exactly %d: %s", operationID, len(object), len(want), body)
	}
	for key, wantValue := range want {
		rawValue, exists := object[key]
		if !exists {
			t.Errorf("%s response is missing result for %q: %s", operationID, key, body)
			continue
		}
		var gotValue string
		if err := json.Unmarshal(rawValue, &gotValue); err != nil {
			t.Errorf("%s result for %q is not a string: %v", operationID, key, err)
			continue
		}
		if gotValue != wantValue {
			t.Errorf("%s result for %q = %q, want %q", operationID, key, gotValue, wantValue)
		}
	}
}

func decodeJSONObject(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		t.Fatalf("response is not a JSON object: %v\n%s", err, body)
	}
	return object
}

func assertNoPascalCaseJSONKeys(t *testing.T, raw []byte, path string) {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Errorf("decode JSON for key-shape assertion: %v", err)
		return
	}
	assertNoPascalCaseValue(t, value, path)
}

func assertNoPascalCaseValue(t *testing.T, value any, path string) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		dynamicKeys := isDynamicJSONMap(path)
		for key, child := range typed {
			if !dynamicKeys && key != "" && key[0] >= 'A' && key[0] <= 'Z' {
				t.Errorf("%s.%s starts with uppercase (PascalCase leak)", path, key)
			}
			assertNoPascalCaseValue(t, child, path+"."+key)
		}
	case []any:
		for index, child := range typed {
			assertNoPascalCaseValue(t, child, fmt.Sprintf("%s[%d]", path, index))
		}
	}
}

func isDynamicJSONMap(path string) bool {
	for _, suffix := range []string{".dependsOn", ".dotEnvVars", ".envCommands", ".extensions", ".nodes", ".vars"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

func firstServerURL(document *openapi3.T) string {
	if len(document.Servers) == 0 || document.Servers[0] == nil {
		return ""
	}
	return document.Servers[0].URL
}

func readServerOutput(path string) string {
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("read output: %v", err)
	}
	return string(contents)
}
