package api

import "net/http"

type LogMessage struct {
	Message     string `json:"message"`
	ProcessName string `json:"process_name"`
}

// NameResponse represents a simple response containing a process name.
type NameResponse struct {
	Name string `json:"name"`
}

// ProjectNameResponse represents a response containing the project name.
type ProjectNameResponse struct {
	ProjectName string `json:"projectName"`
}

// StatusResponse represents a simple response containing a status string.
type StatusResponse struct {
	Status string `json:"status"`
}

// LogsResponse represents a response containing logs lines.
type LogsResponse struct {
	Logs []string `json:"logs"`
}

// ErrorResponse is the body of every API error response. It is also the
// huma error model (see newErrorResponse), so the documented error schema and
// the errors huma writes for invalid requests are the same type.
type ErrorResponse struct { //nolint:errname // the type name is the documented schema name api.ErrorResponse
	Message string `json:"error" required:"true"`
	status  int
}

func (e *ErrorResponse) Error() string {
	return e.Message
}

func (e *ErrorResponse) GetStatus() int {
	return e.status
}

func newStatusError(status int, err error) *ErrorResponse {
	return &ErrorResponse{status: status, Message: err.Error()}
}

func badRequest(err error) *ErrorResponse {
	return newStatusError(http.StatusBadRequest, err)
}

// SendKeysRequest represents the body for sending keystrokes to a process's stdin.
type SendKeysRequest struct {
	Keys string `json:"keys"`
}
