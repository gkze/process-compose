package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/f1bonacc1/process-compose/src/config"
	"github.com/getkin/kin-openapi/openapi3"
)

type sourceRoute struct {
	handler string
	method  string
	path    string
}

type sourceInput struct {
	defaultValue *string
	kind         string
	mediaType    string
	name         string
	required     bool
	typeName     string
}

type criticalHTTPInputContract struct {
	input  sourceInput
	path   string
	method string
}

var criticalHTTPInputContracts = []criticalHTTPInputContract{
	{
		method: http.MethodPost,
		path:   "/project",
		input:  sourceInput{kind: "body", mediaType: "application/json", name: "body", required: true, typeName: "types.Project"},
	},
	{
		method: http.MethodGet,
		path:   "/project/state",
		input:  sourceInput{defaultValue: stringPointer("false"), kind: "query", name: "withMemory", required: false, typeName: "boolean"},
	},
}

var webSocketHandlers = map[string]struct{}{
	"HandleLogsStream":   {},
	"HandleStatesStream": {},
}

func TestHTTPRoutesMatchOpenAPIInputs(t *testing.T) {
	apiDir := packageDirectory(t)
	routes := contractRuntimeRoutes(t)
	handlerInputs, err := analyzeGinHandlerInputs(apiDir, routeHandlerNames(routes, false))
	if err != nil {
		t.Fatalf("analyze typed Gin handler inputs: %v", err)
	}
	document := loadOpenAPIDocument(t, filepath.Join(apiDir, "..", "docs", "swagger.json"))

	seenOperationKeys := make(map[string]struct{})
	for _, route := range routes {
		if route.path == "/" || strings.HasPrefix(route.path, "/swagger/") {
			continue
		}
		if _, isWebSocket := webSocketHandlers[route.handler]; isWebSocket {
			continue
		}

		openAPIPath := ginPathToOpenAPIPath(route.path)
		pathItem := document.Paths.Find(openAPIPath)
		if pathItem == nil {
			t.Errorf("%s %s (%s) is registered in Gin but missing from OpenAPI", route.method, openAPIPath, route.handler)
			continue
		}
		operation := pathItem.GetOperation(route.method)
		if operation == nil {
			t.Errorf("%s %s (%s) is registered in Gin but missing from OpenAPI", route.method, openAPIPath, route.handler)
			continue
		}
		seenOperationKeys[openAPIOperationKey(route.method, openAPIPath)] = struct{}{}

		source, err := handlerInputsForRoute(handlerInputs, route)
		if err != nil {
			t.Error(err)
			continue
		}
		documented := documentedInputs(pathItem, operation)
		compareInputs(t, route, source, documented)
		for _, gap := range criticalHTTPInputContractGaps(route, documented) {
			t.Error(gap)
		}
	}

	for path, pathItem := range document.Paths.Map() {
		for method, operation := range pathItem.Operations() {
			if _, isWebSocket := webSocketOperation(operation.OperationID); isWebSocket {
				continue
			}
			if _, seen := seenOperationKeys[openAPIOperationKey(method, path)]; !seen {
				t.Errorf("%s %s (%s) is documented in OpenAPI but has no registered Gin route", method, path, operation.OperationID)
			}
		}
	}
}

func TestHTTPRoutesMatchOpenAPIInputsRejectsCriticalMetadataDrift(t *testing.T) {
	falseValue := "false"
	trueValue := "true"
	tests := []struct {
		documented []sourceInput
		name       string
		route      sourceRoute
	}{
		{
			name:  "UpdateProject body schema",
			route: sourceRoute{handler: "UpdateProject", method: http.MethodPost, path: "/project"},
			documented: []sourceInput{
				{kind: "body", mediaType: "application/json", name: "body", required: true, typeName: "types.ProcessConfig"},
			},
		},
		{
			name:  "UpdateProject body media type",
			route: sourceRoute{handler: "UpdateProject", method: http.MethodPost, path: "/project"},
			documented: []sourceInput{
				{kind: "body", mediaType: "text/plain", name: "body", required: true, typeName: "types.Project"},
			},
		},
		{
			name:  "UpdateProject body requiredness",
			route: sourceRoute{handler: "UpdateProject", method: http.MethodPost, path: "/project"},
			documented: []sourceInput{
				{kind: "body", mediaType: "application/json", name: "body", required: false, typeName: "types.Project"},
			},
		},
		{
			name:  "GetProjectState query type",
			route: sourceRoute{handler: "GetProjectState", method: http.MethodGet, path: "/project/state"},
			documented: []sourceInput{
				{defaultValue: &falseValue, kind: "query", name: "withMemory", required: false, typeName: "string"},
			},
		},
		{
			name:  "GetProjectState query requiredness",
			route: sourceRoute{handler: "GetProjectState", method: http.MethodGet, path: "/project/state"},
			documented: []sourceInput{
				{defaultValue: &falseValue, kind: "query", name: "withMemory", required: true, typeName: "boolean"},
			},
		},
		{
			name:  "GetProjectState query missing default",
			route: sourceRoute{handler: "GetProjectState", method: http.MethodGet, path: "/project/state"},
			documented: []sourceInput{
				{kind: "query", name: "withMemory", required: false, typeName: "boolean"},
			},
		},
		{
			name:  "GetProjectState query wrong default",
			route: sourceRoute{handler: "GetProjectState", method: http.MethodGet, path: "/project/state"},
			documented: []sourceInput{
				{defaultValue: &trueValue, kind: "query", name: "withMemory", required: false, typeName: "boolean"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gaps := criticalHTTPInputContractGaps(test.route, test.documented)
			if len(gaps) != 1 {
				t.Fatalf("critical input contract gaps = %#v, want exactly one metadata mismatch", gaps)
			}
		})
	}
}

func TestOpenAPIWebSocketOperationsAreExplicitlyOutOfScope(t *testing.T) {
	apiDir := packageDirectory(t)
	routes := contractRuntimeRoutes(t)
	handlerInputs, err := analyzeGinHandlerInputs(apiDir, routeHandlerNames(routes, true))
	if err != nil {
		t.Fatalf("analyze typed Gin handler inputs: %v", err)
	}
	document := loadOpenAPIDocument(t, filepath.Join(apiDir, "..", "docs", "swagger.json"))
	falseValue := "false"
	expectations := []struct {
		documentedInputs map[string]sourceInput
		operationID      string
		queryNames       []string
		route            sourceRoute
	}{
		{
			documentedInputs: map[string]sourceInput{
				"query:follow": {defaultValue: &falseValue, kind: "query", name: "follow", required: false, typeName: "boolean"},
				"query:name":   {kind: "query", name: "name", required: true, typeName: "string"},
				"query:offset": {kind: "query", name: "offset", required: true, typeName: "integer"},
			},
			operationID: "LogsStream",
			queryNames:  []string{"follow", "name", "offset"},
			route:       sourceRoute{handler: "HandleLogsStream", method: "GET", path: "/process/logs/ws"},
		},
		{
			documentedInputs: map[string]sourceInput{
				"query:name": {kind: "query", name: "name", required: false, typeName: "string"},
			},
			operationID: "StatesStream",
			queryNames:  []string{"name"},
			route:       sourceRoute{handler: "HandleStatesStream", method: "GET", path: "/process/states/ws"},
		},
	}

	// This static boundary covers route registration and handler-consumed query
	// inputs. Upgrade handshakes and WebSocket message frames require a separate
	// runtime harness and remain explicitly outside this test.
	wantRoutes := make([]sourceRoute, 0, len(expectations))
	for _, expectation := range expectations {
		wantRoutes = append(wantRoutes, expectation.route)
	}
	gotRoutes := make([]sourceRoute, 0, len(expectations))
	for _, route := range routes {
		if _, isWebSocket := webSocketHandlers[route.handler]; isWebSocket {
			gotRoutes = append(gotRoutes, route)
		}
	}
	sortSourceRoutes(gotRoutes)
	sortSourceRoutes(wantRoutes)
	if !reflect.DeepEqual(gotRoutes, wantRoutes) {
		t.Fatalf("WebSocket source route inventory changed: got %#v, want %#v; upgrade/frame conformance requires its own harness", gotRoutes, wantRoutes)
	}

	var operations []string
	for _, pathItem := range document.Paths.Map() {
		for _, operation := range pathItem.Operations() {
			if _, isWebSocket := webSocketOperation(operation.OperationID); isWebSocket {
				operations = append(operations, operation.OperationID)
			}
		}
	}
	sort.Strings(operations)

	wantOperations := []string{"LogsStream", "StatesStream"}
	if !reflect.DeepEqual(operations, wantOperations) {
		t.Fatalf("WebSocket operation inventory changed: got %v, want %v; upgrade/frame conformance requires its own harness", operations, wantOperations)
	}

	for _, expectation := range expectations {
		pathItem := document.Paths.Find(ginPathToOpenAPIPath(expectation.route.path))
		if pathItem == nil {
			t.Errorf("%s %s (%s) is registered in Gin but missing from OpenAPI", expectation.route.method, expectation.route.path, expectation.route.handler)
			continue
		}
		operation := pathItem.GetOperation(expectation.route.method)
		if operation == nil {
			t.Errorf("%s %s (%s) is registered in Gin but missing from OpenAPI", expectation.route.method, expectation.route.path, expectation.route.handler)
			continue
		}
		if operation.OperationID != expectation.operationID {
			t.Errorf("%s %s (%s) operationId = %q, want %q", expectation.route.method, expectation.route.path, expectation.route.handler, operation.OperationID, expectation.operationID)
		}

		gotQueryNames := queryInputNames(handlerInputs[expectation.route.handler])
		if !reflect.DeepEqual(gotQueryNames, expectation.queryNames) {
			t.Errorf("%s source query inputs = %#v, want %#v", expectation.route.handler, gotQueryNames, expectation.queryNames)
		}
		documentedQueryNames := queryInputNames(documentedInputs(pathItem, operation))
		if !reflect.DeepEqual(documentedQueryNames, gotQueryNames) {
			t.Errorf("%s OpenAPI query inputs = %#v, source consumes %#v", expectation.operationID, documentedQueryNames, gotQueryNames)
		}
		assertWebSocketInputs(t, document, expectation.operationID, expectation.documentedInputs)
		assertWebSocketHTTPResponses(t, expectation.route, operation)
		if expectation.operationID == "LogsStream" {
			assertLogsStreamNameSchema(t, operation)
		}
	}
}

func TestOpenAPIErrorResponsesUseRequiredErrorSchema(t *testing.T) {
	t.Parallel()

	apiDir := packageDirectory(t)
	document := loadOpenAPIDocument(t, filepath.Join(apiDir, "..", "docs", "swagger.json"))
	if document.Components == nil {
		t.Fatal("OpenAPI document is missing components")
	}
	errorSchemaComponent, ok := document.Components.Schemas["api.ErrorResponse"]
	if !ok || errorSchemaComponent.Value == nil {
		t.Fatal("OpenAPI components are missing api.ErrorResponse")
	}
	errorSchema := errorSchemaComponent.Value
	if !containsString(errorSchema.Required, "error") {
		t.Errorf("api.ErrorResponse does not require the error property")
	}
	if property, ok := errorSchema.Properties["error"]; !ok || property.Value == nil || property.Value.Type == nil || !property.Value.Type.Is("string") {
		t.Errorf("api.ErrorResponse.error = %#v, want a string property", property)
	}

	const errorSchemaRef = "#/components/schemas/api.ErrorResponse"
	for path, pathItem := range document.Paths.Map() {
		for method, operation := range pathItem.Operations() {
			for status, response := range operation.Responses.Map() {
				if len(status) != 3 || (status[0] != '4' && status[0] != '5') {
					continue
				}
				if response.Value == nil {
					t.Errorf("%s %s (%s) response %s is unresolved", method, path, operation.OperationID, status)
					continue
				}
				mediaType := response.Value.Content.Get("application/json")
				if mediaType == nil || mediaType.Schema == nil {
					t.Errorf("%s %s (%s) response %s has no application/json schema", method, path, operation.OperationID, status)
					continue
				}
				if mediaType.Schema.Ref != errorSchemaRef {
					t.Errorf("%s %s (%s) response %s schema = %q, want %q", method, path, operation.OperationID, status, mediaType.Schema.Ref, errorSchemaRef)
				}
			}
		}
	}
}

func TestOpenAPIDocumentsConditionalTokenHeader(t *testing.T) {
	t.Parallel()

	apiDir := packageDirectory(t)
	document := loadOpenAPIDocument(t, filepath.Join(apiDir, "..", "docs", "swagger.json"))
	if document.Components == nil {
		t.Fatal("OpenAPI document is missing components")
	}
	schemeRef, ok := document.Components.SecuritySchemes["ApiTokenAuth"]
	if !ok || schemeRef.Value == nil {
		t.Fatal("OpenAPI components are missing the conditional ApiTokenAuth scheme")
	}
	scheme := schemeRef.Value
	if scheme.Type != "apiKey" || scheme.In != "header" || scheme.Name != config.TokenHeader {
		t.Errorf("ApiTokenAuth = %#v, want an apiKey in the %s header", scheme, config.TokenHeader)
	}
	if !strings.Contains(strings.ToLower(scheme.Description), "configured") {
		t.Errorf("ApiTokenAuth description does not explain its conditional configuration: %q", scheme.Description)
	}
	wantSecurity := openapi3.SecurityRequirements{{"ApiTokenAuth": {}}, {}}
	if !reflect.DeepEqual(document.Security, wantSecurity) {
		t.Errorf("OpenAPI global security = %#v, want %#v so generated clients expose optional token authentication", document.Security, wantSecurity)
	}
	for path, pathItem := range document.Paths.Map() {
		for method, operation := range pathItem.Operations() {
			response := operation.Responses.Value("401")
			if response == nil || response.Value == nil {
				t.Errorf("%s %s (%s) omits the token middleware's 401 response", method, path, operation.OperationID)
				continue
			}
			mediaType := response.Value.Content.Get("application/json")
			if mediaType == nil || mediaType.Schema == nil || mediaType.Schema.Ref != "#/components/schemas/api.ErrorResponse" {
				t.Errorf("%s %s (%s) 401 response = %#v, want api.ErrorResponse JSON", method, path, operation.OperationID, response)
			}
		}
	}
}

func TestHandlerInputsForRouteRejectsUnparsedHandler(t *testing.T) {
	t.Parallel()

	route := sourceRoute{handler: "MissingHandler", method: "GET", path: "/missing"}
	if _, err := handlerInputsForRoute(map[string][]sourceInput{}, route); err == nil {
		t.Fatal("registered route with an unparsed handler was accepted")
	}
}

func assertWebSocketInputs(t *testing.T, document *openapi3.T, operationID string, want map[string]sourceInput) {
	t.Helper()
	for _, pathItem := range document.Paths.Map() {
		for _, operation := range pathItem.Operations() {
			if operation.OperationID != operationID {
				continue
			}
			got := inputsByKey(documentedInputs(pathItem, operation))
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s WebSocket input inventory changed: got %#v, want %#v; runtime conformance remains separate", operationID, got, want)
			}
			return
		}
	}
	t.Errorf("WebSocket operation %s is missing from OpenAPI", operationID)
}

func assertWebSocketHTTPResponses(t *testing.T, route sourceRoute, operation *openapi3.Operation) {
	t.Helper()
	gotStatuses := operation.Responses.Keys()
	sort.Strings(gotStatuses)
	wantStatuses := []string{"101", "400", "401", "403"}
	if !reflect.DeepEqual(gotStatuses, wantStatuses) {
		t.Errorf("%s %s (%s) HTTP handshake responses = %v, want %v; WebSocket message-frame conformance remains separate", route.method, route.path, operation.OperationID, gotStatuses, wantStatuses)
	}
}

func assertLogsStreamNameSchema(t *testing.T, operation *openapi3.Operation) {
	t.Helper()
	for _, parameterRef := range operation.Parameters {
		if parameterRef == nil || parameterRef.Value == nil || parameterRef.Value.Name != "name" {
			continue
		}
		schemaRef := parameterRef.Value.Schema
		if schemaRef == nil || schemaRef.Value == nil {
			t.Fatal("LogsStream name parameter has no schema")
		}
		schema := schemaRef.Value
		if schema.MinLength != 1 {
			t.Errorf("LogsStream name minLength = %d, want 1", schema.MinLength)
		}
		if schema.Pattern != "[^,]" {
			t.Errorf("LogsStream name pattern = %q, want %q", schema.Pattern, "[^,]")
		}
		for _, invalid := range []string{"", ",", ",,,"} {
			if err := schema.VisitJSONString(invalid); err == nil {
				t.Errorf("LogsStream name schema accepts %q without a process name", invalid)
			}
		}
		for _, valid := range []string{"fixture", ",fixture", "fixture,"} {
			if err := schema.VisitJSONString(valid); err != nil {
				t.Errorf("LogsStream name schema rejects %q: %v", valid, err)
			}
		}
		return
	}
	t.Fatal("LogsStream name parameter is missing")
}

func queryInputNames(inputs []sourceInput) []string {
	var names []string
	for _, input := range inputs {
		if input.kind == "query" {
			names = append(names, input.name)
		}
	}
	sort.Strings(names)
	return names
}

func sortSourceRoutes(routes []sourceRoute) {
	sort.Slice(routes, func(left, right int) bool {
		leftKey := routes[left].method + "\x00" + routes[left].path + "\x00" + routes[left].handler
		rightKey := routes[right].method + "\x00" + routes[right].path + "\x00" + routes[right].handler
		return leftKey < rightKey
	})
}

func packageDirectory(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get package directory: %v", err)
	}
	return workingDirectory
}

func contractRuntimeRoutes(t *testing.T) []sourceRoute {
	t.Helper()
	t.Setenv(config.EnvVarApiToken, "")
	t.Setenv(config.EnvVarApiTokenPath, "")
	previousTokenPath := config.CliApiTokenPath
	config.CliApiTokenPath = ""
	t.Cleanup(func() {
		config.CliApiTokenPath = previousTokenPath
	})

	routes, err := runtimeGinRoutes(InitRoutes(false, NewPcApi(&mockProject{})))
	if err != nil {
		t.Fatalf("inventory runtime Gin routes: %v", err)
	}
	return routes
}

func routeHandlerNames(routes []sourceRoute, webSockets bool) []string {
	names := make([]string, 0, len(routes))
	for _, route := range routes {
		_, isWebSocket := webSocketHandlers[route.handler]
		if isWebSocket != webSockets {
			continue
		}
		names = append(names, route.handler)
	}
	return names
}

func handlerInputsForRoute(inputs map[string][]sourceInput, route sourceRoute) ([]sourceInput, error) {
	handlerInputs, ok := inputs[route.handler]
	if !ok {
		return nil, fmt.Errorf("%s %s references handler %q, but its declaration was not parsed", route.method, route.path, route.handler)
	}
	return handlerInputs, nil
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

func documentedInputs(pathItem *openapi3.PathItem, operation *openapi3.Operation) []sourceInput {
	parameters := make(openapi3.Parameters, 0, len(pathItem.Parameters)+len(operation.Parameters))
	parameters = append(parameters, pathItem.Parameters...)
	parameters = append(parameters, operation.Parameters...)
	inputs := make([]sourceInput, 0, len(parameters)+1)
	for _, parameterRef := range parameters {
		if parameterRef == nil || parameterRef.Value == nil {
			continue
		}
		parameter := parameterRef.Value
		if parameter.In != openapi3.ParameterInPath && parameter.In != openapi3.ParameterInQuery {
			continue
		}
		input := sourceInput{kind: parameter.In, name: parameter.Name, required: parameter.Required, typeName: schemaType(parameter.Schema)}
		if parameter.Schema != nil && parameter.Schema.Value != nil && parameter.Schema.Value.Default != nil {
			value := fmt.Sprint(parameter.Schema.Value.Default)
			input.defaultValue = &value
		}
		inputs = append(inputs, input)
	}
	if operation.RequestBody != nil && operation.RequestBody.Value != nil {
		body := operation.RequestBody.Value
		mediaTypeName := "application/json"
		mediaType := body.Content.Get("application/json")
		if mediaType == nil && len(body.Content) == 1 {
			for candidateName, candidate := range body.Content {
				mediaTypeName = candidateName
				mediaType = candidate
			}
		}
		if mediaType == nil {
			mediaTypeName = ""
		}
		var typeName string
		if mediaType != nil {
			typeName = schemaType(mediaType.Schema)
		}
		inputs = append(inputs, sourceInput{kind: "body", mediaType: mediaTypeName, name: "body", required: body.Required, typeName: typeName})
	}
	return inputs
}

func compareInputs(t *testing.T, route sourceRoute, source, documented []sourceInput) {
	t.Helper()
	for _, gap := range inputGaps(route, source, documented) {
		t.Error(gap)
	}
}

func inputGaps(route sourceRoute, source, documented []sourceInput) []string {
	sourceByKey := inputsByKey(source)
	documentedByKey := inputsByKey(documented)

	keys := make(map[string]struct{}, len(sourceByKey)+len(documentedByKey))
	for key := range sourceByKey {
		keys[key] = struct{}{}
	}
	for key := range documentedByKey {
		keys[key] = struct{}{}
	}
	orderedKeys := make([]string, 0, len(keys))
	for key := range keys {
		orderedKeys = append(orderedKeys, key)
	}
	sort.Strings(orderedKeys)

	var gaps []string
	for _, key := range orderedKeys {
		_, sourceOK := sourceByKey[key]
		_, documentedOK := documentedByKey[key]
		switch {
		case sourceOK && !documentedOK:
			gaps = append(gaps, fmt.Sprintf("%s %s (%s) consumes undocumented %s", route.method, route.path, route.handler, key))
		case !sourceOK && documentedOK:
			gaps = append(gaps, fmt.Sprintf("%s %s (%s) documents %s but the handler does not consume it", route.method, route.path, route.handler, key))
		}
	}
	return gaps
}

func criticalHTTPInputContractGaps(route sourceRoute, documented []sourceInput) []string {
	documentedByKey := inputsByKey(documented)
	var gaps []string
	for _, contract := range criticalHTTPInputContracts {
		if route.method != contract.method || ginPathToOpenAPIPath(route.path) != contract.path {
			continue
		}

		key := contract.input.kind + ":" + contract.input.name
		got, exists := documentedByKey[key]
		if !exists {
			gaps = append(gaps, fmt.Sprintf("%s %s (%s) is missing critical %s input", route.method, contract.path, route.handler, key))
			continue
		}
		if !reflect.DeepEqual(got, contract.input) {
			gaps = append(gaps, fmt.Sprintf(
				"%s %s (%s) documents critical %s as %s, want %s",
				route.method,
				contract.path,
				route.handler,
				key,
				describeSourceInput(got),
				describeSourceInput(contract.input),
			))
		}
	}
	return gaps
}

func describeSourceInput(input sourceInput) string {
	defaultValue := "<none>"
	if input.defaultValue != nil {
		defaultValue = fmt.Sprintf("%q", *input.defaultValue)
	}
	return fmt.Sprintf("{required:%t type:%q mediaType:%q default:%s}", input.required, input.typeName, input.mediaType, defaultValue)
}

func stringPointer(value string) *string {
	return &value
}

func inputsByKey(inputs []sourceInput) map[string]sourceInput {
	result := make(map[string]sourceInput, len(inputs))
	for _, input := range inputs {
		result[input.kind+":"+input.name] = input
	}
	return result
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

func openAPIOperationKey(method, path string) string {
	return strings.ToUpper(method) + " " + path
}

func webSocketOperation(operationID string) (string, bool) {
	switch operationID {
	case "LogsStream", "StatesStream":
		return operationID, true
	default:
		return "", false
	}
}

func schemaType(schemaRef *openapi3.SchemaRef) string {
	if schemaRef == nil {
		return ""
	}
	if schemaRef.Ref != "" {
		return strings.TrimPrefix(schemaRef.Ref, "#/components/schemas/")
	}
	schema := schemaRef.Value
	if schema == nil || schema.Type == nil {
		return ""
	}
	if schema.Type.Is("array") && schema.Items != nil {
		return "[]" + schemaType(schema.Items)
	}
	types := schema.Type.Slice()
	if len(types) == 1 {
		return types[0]
	}
	return strings.Join(types, "|")
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
