package api

import (
	"fmt"
	"go/constant"
	"go/types"
	"net/http"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

const ginPackagePath = "github.com/gin-gonic/gin"

var ginInputMethods = map[string]string{
	"BindJSON":       "body",
	"DefaultQuery":   "query",
	"Param":          "path",
	"Query":          "query",
	"ShouldBindJSON": "body",
}

var safeGinContextMethods = map[string]struct{}{"JSON": {}}

func TestRuntimeGinRoutesIncludeRegistrationsOutsideRoutesFile(t *testing.T) {
	t.Parallel()

	router := gin.New()
	group := router.Group("/v1")
	group.GET("/process/:name", NewPcApi(&mockProject{}).GetProcess)

	routes, err := runtimeGinRoutes(router)
	if err != nil {
		t.Fatalf("inventory runtime Gin routes: %v", err)
	}
	want := []sourceRoute{{handler: "GetProcess", method: http.MethodGet, path: "/v1/process/:name"}}
	if !reflect.DeepEqual(routes, want) {
		t.Fatalf("runtime Gin routes = %#v, want %#v", routes, want)
	}
}

func TestSSAGinInputAnalysisUsesPackageIdentityAndConstants(t *testing.T) {
	t.Parallel()

	fixtureDir := filepath.Join(packageDirectory(t), "testdata", "contractfixture")
	inputs, err := analyzeGinHandlerInputs(fixtureDir, []string{"TypedHandler"})
	if err != nil {
		t.Fatalf("analyze typed Gin handler inputs: %v", err)
	}

	want := []sourceInput{
		{kind: "body", name: "body"},
		{kind: "path", name: "replicas"},
		{kind: "query", name: "withMemory"},
	}
	if got := inputs["TypedHandler"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("TypedHandler inputs = %#v, want %#v", got, want)
	}
	if _, exists := inputs["UnrelatedQueryMethod"]; exists {
		t.Fatal("a Query method on a non-Gin context was treated as a request input")
	}

	originalOmissionGaps := []string{
		"GET /fixture (TypedHandler) consumes undocumented body:body",
		"GET /fixture (TypedHandler) consumes undocumented query:withMemory",
	}
	if got := inputGaps(
		sourceRoute{handler: "TypedHandler", method: http.MethodGet, path: "/fixture"},
		inputs["TypedHandler"],
		[]sourceInput{want[1]},
	); !reflect.DeepEqual(got, originalOmissionGaps) {
		t.Fatalf("original body and withMemory omission gaps = %#v, want %#v", got, originalOmissionGaps)
	}

	documented := []sourceInput{want[0], want[1], {kind: "query", name: "documentedOnly", typeName: "string"}}
	wantGaps := []string{
		"GET /fixture (TypedHandler) documents query:documentedOnly but the handler does not consume it",
		"GET /fixture (TypedHandler) consumes undocumented query:withMemory",
	}
	if got := inputGaps(sourceRoute{handler: "TypedHandler", method: http.MethodGet, path: "/fixture"}, inputs["TypedHandler"], documented); !reflect.DeepEqual(got, wantGaps) {
		t.Fatalf("input gaps = %#v, want %#v", got, wantGaps)
	}
}

func TestSSAGinInputAnalysisFailsClosedOnDynamicInputNames(t *testing.T) {
	t.Parallel()

	fixtureDir := filepath.Join(packageDirectory(t), "testdata", "contractfixture")
	inputs, err := analyzeGinHandlerInputs(fixtureDir, []string{"RegisteredHandler"})
	if err != nil {
		t.Fatalf("analyze registered handler only: %v", err)
	}
	if got := inputs["RegisteredHandler"]; !reflect.DeepEqual(got, []sourceInput{{kind: "query", name: "known"}}) {
		t.Fatalf("RegisteredHandler inputs = %#v", got)
	}

	_, err = analyzeGinHandlerInputs(fixtureDir, []string{"DynamicInput"})
	if err == nil || !strings.Contains(err.Error(), "not a compile-time string constant") {
		t.Fatalf("dynamic Gin input name error = %v", err)
	}
}

func runtimeGinRoutes(router *gin.Engine) ([]sourceRoute, error) {
	registered := router.Routes()
	routes := make([]sourceRoute, 0, len(registered))
	for _, route := range registered {
		if route.Path == "/" || strings.HasPrefix(route.Path, "/swagger/") {
			continue
		}
		function := runtime.FuncForPC(reflect.ValueOf(route.HandlerFunc).Pointer())
		if function == nil {
			return nil, fmt.Errorf("%s %s: cannot resolve Gin handler symbol", route.Method, route.Path)
		}
		name := strings.TrimSuffix(function.Name(), "-fm")
		const receiver = ".(*PcApi)."
		index := strings.LastIndex(name, receiver)
		if index < 0 {
			return nil, fmt.Errorf("%s %s: handler %q is not a direct *PcApi method", route.Method, route.Path, function.Name())
		}
		handler := name[index+len(receiver):]
		if handler == "" || strings.Contains(handler, ".") {
			return nil, fmt.Errorf("%s %s: handler %q is not a direct *PcApi method", route.Method, route.Path, function.Name())
		}
		routes = append(routes, sourceRoute{handler: handler, method: route.Method, path: route.Path})
	}
	sortSourceRoutes(routes)
	return routes, nil
}

// analyzeGinHandlerInputs proves only which inputs registered handlers consume.
// Requiredness, types, defaults, and error behavior belong to the live harness.
func analyzeGinHandlerInputs(directory string, handlerNames []string) (map[string][]sourceInput, error) {
	loaded, err := packages.Load(&packages.Config{
		Dir:  directory,
		Mode: packages.LoadSyntax,
	}, ".")
	if err != nil {
		return nil, fmt.Errorf("load Go package %s: %w", directory, err)
	}
	if len(loaded) != 1 {
		return nil, fmt.Errorf("load Go package %s: got %d packages, want 1", directory, len(loaded))
	}
	pkg := loaded[0]
	if len(pkg.Errors) > 0 {
		return nil, fmt.Errorf("load Go package %s: %s", directory, packagesError(pkg.Errors))
	}

	program, ssaPackages := ssautil.Packages(loaded, ssa.InstantiateGenerics)
	program.Build()
	if len(ssaPackages) != 1 || ssaPackages[0] == nil {
		return nil, fmt.Errorf("build SSA for Go package %s", directory)
	}

	pcAPIObject := pkg.Types.Scope().Lookup("PcApi")
	if pcAPIObject == nil {
		return nil, fmt.Errorf("Go package %s has no PcApi type", directory)
	}
	pcAPIType, ok := pcAPIObject.Type().(*types.Named)
	if !ok {
		return nil, fmt.Errorf("Go package %s PcApi is not a named type", directory)
	}

	result := make(map[string][]sourceInput)
	requested := make(map[string]struct{}, len(handlerNames))
	for _, name := range handlerNames {
		requested[name] = struct{}{}
	}
	receiverType := types.NewPointer(pcAPIType)
	methods := types.NewMethodSet(receiverType)
	for index := 0; index < methods.Len(); index++ {
		method, ok := methods.At(index).Obj().(*types.Func)
		if !ok || method.Pkg() != pkg.Types {
			continue
		}
		signature, ok := method.Type().(*types.Signature)
		if !ok || !types.Identical(signature.Recv().Type(), receiverType) || signature.Params().Len() != 1 || !isGinContext(signature.Params().At(0).Type()) {
			continue
		}
		if _, wanted := requested[method.Name()]; !wanted {
			continue
		}
		function := program.FuncValue(method)
		if function == nil || function.Blocks == nil {
			return nil, fmt.Errorf("analyze %s: no SSA body", method.FullName())
		}
		inputs, err := analyzeGinHandler(function)
		if err != nil {
			return nil, fmt.Errorf("analyze %s: %w", method.FullName(), err)
		}
		result[method.Name()] = inputs
		delete(requested, method.Name())
	}
	if len(requested) != 0 {
		missing := make([]string, 0, len(requested))
		for name := range requested {
			missing = append(missing, name)
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("registered Gin handlers have no analyzable *PcApi method: %s", strings.Join(missing, ", "))
	}
	return result, nil
}

func analyzeGinHandler(function *ssa.Function) ([]sourceInput, error) {
	if err := auditGinContextUses(function); err != nil {
		return nil, err
	}

	inputsByKey := make(map[string]sourceInput)
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok {
				continue
			}
			method, ginCall := ginContextMethod(call)
			if !ginCall {
				continue
			}
			kind, inputBearing := ginInputMethods[method]
			if !inputBearing {
				if _, safe := safeGinContextMethods[method]; !safe {
					return nil, fmt.Errorf("unclassified Gin context method %s", method)
				}
				continue
			}

			input := sourceInput{kind: kind}
			arguments := call.Common().Args
			if kind == "body" {
				if len(arguments) != 2 {
					return nil, fmt.Errorf("%s has %d SSA arguments, want receiver and body", method, len(arguments))
				}
				input.name = "body"
			} else {
				name, ok := constantStringArgument(arguments, 1)
				if !ok {
					return nil, fmt.Errorf("%s input name is not a compile-time string constant", method)
				}
				input.name = name
			}
			inputsByKey[input.kind+":"+input.name] = input
		}
	}

	keys := make([]string, 0, len(inputsByKey))
	for key := range inputsByKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	inputs := make([]sourceInput, 0, len(keys))
	for _, key := range keys {
		inputs = append(inputs, inputsByKey[key])
	}
	return inputs, nil
}

func packagesError(errors []packages.Error) string {
	messages := make([]string, 0, len(errors))
	for _, err := range errors {
		messages = append(messages, err.Error())
	}
	sort.Strings(messages)
	return strings.Join(messages, "; ")
}

func isGinContext(typeValue types.Type) bool {
	if typeValue == nil {
		return false
	}
	if pointer, ok := typeValue.(*types.Pointer); ok {
		typeValue = pointer.Elem()
	}
	named, ok := typeValue.(*types.Named)
	return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == ginPackagePath && named.Obj().Name() == "Context"
}

func ginContextMethod(call *ssa.Call) (string, bool) {
	callee := call.Common().StaticCallee()
	if callee == nil || callee.Object() == nil {
		return "", false
	}
	method, ok := callee.Object().(*types.Func)
	if !ok || method.Pkg() == nil || method.Pkg().Path() != ginPackagePath {
		return "", false
	}
	signature, ok := method.Type().(*types.Signature)
	return method.Name(), ok && signature.Recv() != nil && isGinContext(signature.Recv().Type())
}

func constantStringArgument(arguments []ssa.Value, index int) (string, bool) {
	if index < 0 || index >= len(arguments) {
		return "", false
	}
	value, ok := arguments[index].(*ssa.Const)
	if !ok || value.Value == nil || value.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(value.Value), true
}

func auditGinContextUses(function *ssa.Function) error {
	if len(function.Params) != 2 || !isGinContext(function.Params[1].Type()) {
		return fmt.Errorf("unexpected SSA handler signature")
	}
	context := function.Params[1]
	contextType := context.Type().(*types.Pointer).Elem().Underlying().(*types.Struct)
	if context.Referrers() == nil {
		return nil
	}
	for _, reference := range *context.Referrers() {
		switch reference := reference.(type) {
		case *ssa.Call:
			if _, ginCall := ginContextMethod(reference); ginCall {
				continue
			}
		case *ssa.FieldAddr:
			field := contextType.Field(reference.Field).Name()
			if _, webSocket := webSocketHandlers[function.Name()]; webSocket && (field == "Request" || field == "Writer") {
				continue
			}
			return fmt.Errorf("direct Gin context field access %s", field)
		}
		return fmt.Errorf("Gin context escapes through %T", reference)
	}
	return nil
}
