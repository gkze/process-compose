package api

import (
	"net/http"
	"net/url"
	"reflect"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/f1bonacc1/process-compose/src/config"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// TokenAuthMiddleware enforces API access using an auth token if configured.
func TokenAuthMiddleware(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		reqToken := c.GetHeader(config.TokenHeader)
		if reqToken != token {
			log.Error().
				Str("client_ip", c.ClientIP()).
				Str("method", c.Request.Method).
				Str("path", c.Request.URL.Path).
				Msg("failed login attempt: invalid or missing token")
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{Message: "unauthorized"})
			return
		}
		c.Next()
	}
}

// InitRoutes initialize routing information. serverURL is the server the
// served OpenAPI document names.
func InitRoutes(useLogger bool, handler *PcApi, serverURL string) *gin.Engine {
	r := gin.New()
	// Match routes against the raw (still percent-encoded) path so that
	// process names containing "/" (sent as %2F) reach the :name param
	// instead of splitting the route. Only kicks in when the request path
	// actually contains percent-encoding, so plain names route as before.
	r.UseRawPath = true
	r.UnescapePathValues = true
	if useLogger {
		r.Use(gin.Logger())
	}
	r.Use(gin.Recovery())

	authToken := config.GetApiToken()
	if authToken != "" {
		if len(authToken) < 20 {
			log.Fatal().Msgf("%s must be at least 20 characters long", config.EnvVarApiToken)
		}
		r.Use(TokenAuthMiddleware(authToken))
	}

	r.GET("/", func(c *gin.Context) {
		location := url.URL{Path: docsPath}
		c.Redirect(http.StatusFound, location.RequestURI())
	})

	humaAPI := newHumaAPI(r, serverURL)
	registerOperations(humaAPI, handler)

	r.GET("/process/logs/ws", handler.HandleLogsStream)
	r.GET("/process/states/ws", handler.HandleStatesStream)

	return r
}

// document completes op with what every operation shares: its 200 response
// (unless op documents it already), the token middleware's 401 and the given
// error statuses, all with ErrorResponse bodies. Request bodies have no size
// limit or read timeout, as before huma.
func document(humaAPI huma.API, op huma.Operation, success string, errorStatuses ...int) huma.Operation {
	if op.Responses == nil {
		op.Responses = map[string]*huma.Response{}
	}
	if op.Responses[strconv.Itoa(http.StatusOK)] == nil {
		op.Responses[strconv.Itoa(http.StatusOK)] = &huma.Response{Description: success}
	}
	op.Responses[strconv.Itoa(http.StatusUnauthorized)] = errorResponse(humaAPI, "API token missing or invalid")
	for _, status := range errorStatuses {
		op.Responses[strconv.Itoa(status)] = errorResponse(humaAPI, http.StatusText(status))
	}
	op.MaxBodyBytes = -1
	op.BodyReadTimeout = -1
	return op
}

func jsonResponse(humaAPI huma.API, description string, body reflect.Type) *huma.Response {
	return &huma.Response{
		Description: description,
		Content: map[string]*huma.MediaType{"application/json": {
			Schema: humaAPI.OpenAPI().Components.Schemas.Schema(body, true, ""),
		}},
	}
}

func errorResponse(humaAPI huma.API, description string) *huma.Response {
	return jsonResponse(humaAPI, description, reflect.TypeFor[ErrorResponse]())
}

// multiStatusResponse documents a 207 Multi-Status: the operation failed for
// some of the items it was given, and the body maps each item to its status.
func multiStatusResponse(humaAPI huma.API, description string) *huma.Response {
	return jsonResponse(humaAPI, description, reflect.TypeFor[map[string]string]())
}

// requestBody describes a request body; huma documents its schema from the
// handler's input type.
func requestBody(description string) *huma.RequestBody {
	return &huma.RequestBody{Description: description, Content: map[string]*huma.MediaType{}}
}

func registerOperations(humaAPI huma.API, handler *PcApi) {
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "IsAlive", Method: http.MethodGet, Path: "/live", Tags: []string{"Liveness"},
		Summary: "Liveness Check", Description: "Check if server is responding",
	}, "Alive Status"), handler.IsAlive)
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "GetProcesses", Method: http.MethodGet, Path: "/processes", Tags: []string{"Process"},
		Summary: "Get all processes", Description: "Retrieves all the configured processes and their status",
	}, "Processes Status", http.StatusBadRequest), handler.GetProcesses)
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "GetProcess", Method: http.MethodGet, Path: "/process/{name}", Tags: []string{"Process"},
		Summary: "Get process state", Description: "Retrieves the given process and its status",
	}, "OK", http.StatusBadRequest), handler.GetProcess)
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "GetProcessInfo", Method: http.MethodGet, Path: "/process/info/{name}", Tags: []string{"Process"},
		Summary: "Get process config", Description: "Retrieves the given process and its config",
	}, "OK", http.StatusBadRequest), handler.GetProcessInfo)
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "UpdateProcess", Method: http.MethodPost, Path: "/process", Tags: []string{"Process"},
		Summary: "Updates process configuration", Description: "Update process",
		RequestBody: requestBody("Process configuration to update"),
	}, "Updated Process Config", http.StatusBadRequest), handler.UpdateProcess)
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "GetProcessPorts", Method: http.MethodGet, Path: "/process/ports/{name}", Tags: []string{"Process"},
		Summary: "Get process ports", Description: "Retrieves process open ports",
	}, "Process Ports", http.StatusBadRequest), handler.GetProcessPorts)
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "GetProcessLogs", Method: http.MethodGet, Path: "/process/logs/{name}/{endOffset}/{limit}", Tags: []string{"Process"},
		Summary: "Get process logs", Description: "Retrieves the process logs",
	}, "Process Logs", http.StatusBadRequest), handler.GetProcessLogs)
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "TruncateProcessLogs", Method: http.MethodDelete, Path: "/process/logs/{name}", Tags: []string{"Process"},
		Summary: "Truncate process logs", Description: "Truncates the process logs",
	}, "Truncated Process Name", http.StatusBadRequest), handler.TruncateProcessLogs)
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "StopProcess", Method: http.MethodPatch, Path: "/process/stop/{name}", Tags: []string{"Process"},
		Summary: "Stop a process", Description: "Sends kill signal to the process",
	}, "Stopped Process Name", http.StatusBadRequest), handler.StopProcess)
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "SendSignal", Method: http.MethodPatch, Path: "/process/signal/{name}/{signal}", Tags: []string{"Process"},
		Summary: "Signal a process", Description: "Sends a POSIX signal to the process",
	}, "Signaled Process Name", http.StatusBadRequest), handler.SendSignal)
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "StopProcesses", Method: http.MethodPatch, Path: "/processes/stop", Tags: []string{"Process"},
		Summary: "Stop processes", Description: "Sends kill signal to the processes list",
		Responses:   map[string]*huma.Response{"207": multiStatusResponse(humaAPI, "Stopped Processes Names")},
		RequestBody: requestBody("Processes Names"),
	}, "Stopped Processes Names", http.StatusBadRequest), handler.StopProcesses)
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "StartProcess", Method: http.MethodPost, Path: "/process/start/{name}", Tags: []string{"Process"},
		Summary: "Start a process", Description: "Starts the process if the state is not 'running' or 'pending'",
	}, "Started Process Name", http.StatusBadRequest), handler.StartProcess)
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "RestartProcess", Method: http.MethodPost, Path: "/process/restart/{name}", Tags: []string{"Process"},
		Summary: "Restart a process", Description: "Restarts the process",
	}, "Restarted Process Name", http.StatusBadRequest), handler.RestartProcess)
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "SendProcessKeys", Method: http.MethodPost, Path: "/process/send-keys/{name}", Tags: []string{"Process"},
		Summary: "Send keys to an interactive process", Description: "Sends keystroke(s) to an interactive process's stdin",
		RequestBody: requestBody("Keys to send"),
	}, "Process Name", http.StatusBadRequest), handler.SendProcessKeys)
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "ScaleProcess", Method: http.MethodPatch, Path: "/process/scale/{name}/{scale}", Tags: []string{"Process"},
		Summary: "Scale a process to a given replicas count", Description: "Scale a process",
	}, "Scaled Process Name", http.StatusBadRequest), handler.ScaleProcess)
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "ShutDownProject", Method: http.MethodPost, Path: "/project/stop", Tags: []string{"Project"},
		Summary: "Stops all the processes and the server", Description: "Shuts down the server",
		Responses: map[string]*huma.Response{"200": jsonResponse(humaAPI, "Stopped Server", reflect.TypeFor[StatusResponse]())},
	}, "Stopped Server"), handler.ShutDownProject)
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "UpdateProject", Method: http.MethodPost, Path: "/project", Tags: []string{"Project"},
		Summary: "Updates running processes", Description: "Update running project",
		Responses:   map[string]*huma.Response{"207": multiStatusResponse(humaAPI, "Update Project Status")},
		RequestBody: requestBody("Project configuration to update"),
	}, "Update Project Status", http.StatusBadRequest), handler.UpdateProject)
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "ReloadProject", Method: http.MethodPost, Path: "/project/configuration", Tags: []string{"Project"},
		Summary: "Reload project", Description: "Reload project state from config",
		Responses: map[string]*huma.Response{"207": multiStatusResponse(humaAPI, "Update Project Status")},
	}, "Update Project Status", http.StatusBadRequest), handler.ReloadProject)
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "GetProjectName", Method: http.MethodGet, Path: "/project/name", Tags: []string{"ProjectName"},
		Summary: "Get Project Name", Description: "Get process compose project name",
	}, "Project Name", http.StatusBadRequest), handler.GetProjectName)
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "GetProjectState", Method: http.MethodGet, Path: "/project/state", Tags: []string{"Project"},
		Summary: "Get project state", Description: "Retrieves project state information",
	}, "Project State", http.StatusBadRequest, http.StatusInternalServerError), handler.GetProjectState)
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "StartNamespace", Method: http.MethodPost, Path: "/namespace/start/{name}", Tags: []string{"Namespace"},
		Summary: "Start a namespace", Description: "Starts the namespace",
	}, "Started Namespace Name", http.StatusBadRequest), handler.StartNamespace)
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "StopNamespace", Method: http.MethodPost, Path: "/namespace/stop/{name}", Tags: []string{"Namespace"},
		Summary: "Stop a namespace", Description: "Stops the namespace",
	}, "Stopped Namespace Name", http.StatusBadRequest), handler.StopNamespace)
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "RestartNamespace", Method: http.MethodPost, Path: "/namespace/restart/{name}", Tags: []string{"Namespace"},
		Summary: "Restart a namespace", Description: "Restarts the namespace",
	}, "Restarted Namespace Name", http.StatusBadRequest), handler.RestartNamespace)
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "GetNamespaces", Method: http.MethodGet, Path: "/namespaces", Tags: []string{"Namespace"},
		Summary: "Get all namespaces", Description: "Retrieves all unique namespaces",
	}, "Namespaces List", http.StatusBadRequest), handler.GetNamespaces)
	huma.Register(humaAPI, document(humaAPI, huma.Operation{
		OperationID: "GetDependencyGraph", Method: http.MethodGet, Path: "/graph", Tags: []string{"Graph"},
		Summary: "Get dependency graph", Description: "Returns the process dependency graph with current status",
	}, "OK", http.StatusBadRequest), handler.GetDependencyGraph)

	documentWebSocketOperations(humaAPI)
}
