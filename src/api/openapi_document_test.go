package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/f1bonacc1/process-compose/src/config"
	"github.com/f1bonacc1/process-compose/src/types"
	"github.com/getkin/kin-openapi/openapi3"
)

// openAPIDocumentFile is the committed OpenAPI document, relative to this
// package. `make openapi` regenerates it.
const openAPIDocumentFile = "openapi.json"

const errorSchemaRef = "#/components/schemas/api.ErrorResponse"

func TestOpenAPIDocumentIsCurrent(t *testing.T) {
	generated, err := OpenAPIDocument()
	if err != nil {
		t.Fatalf("generate OpenAPI document: %v", err)
	}
	committed, err := os.ReadFile(filepath.Join(packageDirectory(t), openAPIDocumentFile))
	if err != nil {
		t.Fatalf("read committed OpenAPI document: %v", err)
	}
	if string(generated) != string(committed) {
		t.Fatalf("%s is out of date with the registered operations; run make openapi", openAPIDocumentFile)
	}
}

// TestOpenAPIDocumentsEveryRoute checks that every route the router serves is
// documented and every documented operation is served. huma documents the
// operations it registers; the WebSocket routes are documented by hand.
func TestOpenAPIDocumentsEveryRoute(t *testing.T) {
	t.Setenv(config.EnvVarApiToken, "")
	t.Setenv(config.EnvVarApiTokenPath, "")
	previousTokenPath := config.CliApiTokenPath
	config.CliApiTokenPath = ""
	t.Cleanup(func() { config.CliApiTokenPath = previousTokenPath })

	var served []string
	for _, route := range InitRoutes(false, NewPcApi(&mockProject{}), DefaultServerURL).Routes() {
		if route.Path == "/" || strings.HasPrefix(route.Path, openAPIPath) || strings.HasPrefix(route.Path, docsPath) {
			continue
		}
		served = append(served, route.Method+" "+ginPathToOpenAPIPath(route.Path))
	}
	sort.Strings(served)

	document := loadOpenAPIDocument(t, filepath.Join(packageDirectory(t), openAPIDocumentFile))
	var documented []string
	for path, pathItem := range document.Paths.Map() {
		for method := range pathItem.Operations() {
			documented = append(documented, method+" "+path)
		}
	}
	sort.Strings(documented)

	if !reflect.DeepEqual(served, documented) {
		t.Errorf("served routes differ from documented operations\nserved:\n%s\ndocumented:\n%s",
			strings.Join(served, "\n"), strings.Join(documented, "\n"))
	}
}

func TestOpenAPIErrorResponsesUseRequiredErrorSchema(t *testing.T) {
	document := loadOpenAPIDocument(t, filepath.Join(packageDirectory(t), openAPIDocumentFile))
	errorSchema := document.Components.Schemas["api.ErrorResponse"]
	if errorSchema == nil || errorSchema.Value == nil {
		t.Fatal("OpenAPI components are missing api.ErrorResponse")
	}
	if !containsString(errorSchema.Value.Required, "error") {
		t.Errorf("api.ErrorResponse does not require the error property")
	}
	for path, pathItem := range document.Paths.Map() {
		for method, operation := range pathItem.Operations() {
			for status, response := range operation.Responses.Map() {
				if status[0] != '4' && status[0] != '5' {
					continue
				}
				mediaType := response.Value.Content.Get("application/json")
				if mediaType == nil || mediaType.Schema == nil || mediaType.Schema.Ref != errorSchemaRef {
					t.Errorf("%s %s (%s) response %s does not use %s", method, path, operation.OperationID, status, errorSchemaRef)
				}
			}
		}
	}
}

func TestOpenAPIDocumentsConditionalTokenHeader(t *testing.T) {
	document := loadOpenAPIDocument(t, filepath.Join(packageDirectory(t), openAPIDocumentFile))
	scheme := document.Components.SecuritySchemes[apiTokenScheme]
	if scheme == nil || scheme.Value == nil {
		t.Fatalf("OpenAPI components are missing the %s scheme", apiTokenScheme)
	}
	if scheme.Value.Type != "apiKey" || scheme.Value.In != "header" || scheme.Value.Name != config.TokenHeader {
		t.Errorf("%s = %#v, want an apiKey in the %s header", apiTokenScheme, scheme.Value, config.TokenHeader)
	}
	wantSecurity := openapi3.SecurityRequirements{{apiTokenScheme: {}}, {}}
	if !reflect.DeepEqual(document.Security, wantSecurity) {
		t.Errorf("OpenAPI global security = %#v, want %#v so the token is optional", document.Security, wantSecurity)
	}
	for path, pathItem := range document.Paths.Map() {
		for method, operation := range pathItem.Operations() {
			response := operation.Responses.Value("401")
			if response == nil || response.Value.Content.Get("application/json") == nil {
				t.Errorf("%s %s (%s) omits the token middleware's 401 response", method, path, operation.OperationID)
			}
		}
	}
}

// TestOpenAPIWebSocketOperations covers the hand-written documentation of
// the WebSocket handshakes; the message frames are not documented.
func TestOpenAPIWebSocketOperations(t *testing.T) {
	document := loadOpenAPIDocument(t, filepath.Join(packageDirectory(t), openAPIDocumentFile))
	for path, want := range map[string][]string{
		"/process/logs/ws":   {"follow", "name", "offset"},
		"/process/states/ws": {"name"},
	} {
		pathItem := document.Paths.Find(path)
		if pathItem == nil || pathItem.Get == nil {
			t.Fatalf("GET %s is not documented", path)
		}
		var names []string
		for _, parameter := range pathItem.Get.Parameters {
			names = append(names, parameter.Value.Name)
		}
		sort.Strings(names)
		if !reflect.DeepEqual(names, want) {
			t.Errorf("GET %s query parameters = %v, want %v", path, names, want)
		}
		statuses := pathItem.Get.Responses.Keys()
		sort.Strings(statuses)
		if want := []string{"101", "400", "401", "403"}; !reflect.DeepEqual(statuses, want) {
			t.Errorf("GET %s handshake responses = %v, want %v", path, statuses, want)
		}
	}

	name := document.Paths.Find("/process/logs/ws").Get.Parameters.GetByInAndName("query", "name").Schema.Value
	for _, invalid := range []string{"", ",", ",,,"} {
		if err := name.VisitJSONString(invalid); err == nil {
			t.Errorf("LogsStream name schema accepts %q, which names no process", invalid)
		}
	}
	for _, valid := range []string{"fixture", ",fixture", "fixture,"} {
		if err := name.VisitJSONString(valid); err != nil {
			t.Errorf("LogsStream name schema rejects %q: %v", valid, err)
		}
	}
}

// TestOpenAPINamespaceMatchesWireFormat checks the namespace schema against
// what Namespaces.MarshalJSON writes: a string for one namespace and a list
// for several.
func TestOpenAPINamespaceMatchesWireFormat(t *testing.T) {
	document := loadOpenAPIDocument(t, filepath.Join(packageDirectory(t), openAPIDocumentFile))
	for _, component := range []string{"types.ProcessState", "types.ProcessConfig"} {
		schema := document.Components.Schemas[component].Value.Properties["namespace"].Value
		for _, namespaces := range []types.Namespaces{{"one"}, {"one", "two"}} {
			encoded, err := json.Marshal(namespaces)
			if err != nil {
				t.Fatal(err)
			}
			var value any
			if err := json.Unmarshal(encoded, &value); err != nil {
				t.Fatal(err)
			}
			if err := schema.VisitJSON(value); err != nil {
				t.Errorf("%s.namespace rejects %s: %v", component, encoded, err)
			}
		}
	}
}

func packageDirectory(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get package directory: %v", err)
	}
	return workingDirectory
}

func loadOpenAPIDocument(t *testing.T, filename string) *openapi3.T {
	t.Helper()
	document, err := openapi3.NewLoader().LoadFromFile(filename)
	if err != nil {
		t.Fatalf("load OpenAPI document %s: %v", filename, err)
	}
	if err := document.Validate(context.Background()); err != nil {
		t.Fatalf("validate OpenAPI document %s: %v", filename, err)
	}
	return document
}

func ginPathToOpenAPIPath(path string) string {
	parts := strings.Split(path, "/")
	for index, part := range parts {
		if strings.HasPrefix(part, ":") {
			parts[index] = "{" + strings.TrimPrefix(part, ":") + "}"
		}
	}
	return strings.Join(parts, "/")
}

func webSocketOperation(operationID string) (string, bool) {
	switch operationID {
	case "LogsStream", "StatesStream":
		return operationID, true
	default:
		return "", false
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
