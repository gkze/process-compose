package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/f1bonacc1/process-compose/src/app"
	"github.com/f1bonacc1/process-compose/src/types"
)

type PcApi struct {
	project app.IProject
}

func NewPcApi(project app.IProject) *PcApi {
	return &PcApi{project: project}
}

type processNameInput struct {
	Name string `path:"name" doc:"Process Name"`
}

type namespaceNameInput struct {
	Name string `path:"name" doc:"Namespace Name"`
}

type processLogsInput struct {
	Name      string `path:"name" doc:"Process Name"`
	EndOffset int    `path:"endOffset" doc:"Offset from the end of the log"`
	Limit     int    `path:"limit" doc:"Limit of lines to get (0 will get all the lines till the end)"`
}

type processSignalInput struct {
	Name   string `path:"name" doc:"Process Name"`
	Signal int    `path:"signal" minimum:"0" maximum:"22" doc:"Signal Number"`
}

type processScaleInput struct {
	Name  string `path:"name" doc:"Process Name"`
	Scale int    `path:"scale" minimum:"1" doc:"New amount of process replicas"`
}

type stopProcessesInput struct {
	Body []string
}

type sendKeysInput struct {
	Name string `path:"name" doc:"Process Name"`
	Body SendKeysRequest
}

type updateProcessInput struct {
	Body types.ProcessConfig
}

type updateProjectInput struct {
	Body types.Project
}

type projectStateInput struct {
	WithMemory bool `query:"withMemory" default:"false" doc:"Include runtime memory statistics"`
}

type bodyOutput[T any] struct {
	Body T
}

// statusMapOutput is a per-item status map, returned with 207 Multi-Status
// when only some of the items succeeded.
type statusMapOutput struct {
	Status int
	Body   map[string]string
}

func body[T any](value T) *bodyOutput[T] {
	return &bodyOutput[T]{Body: value}
}

func nameOutput(name string) *bodyOutput[NameResponse] {
	return body(NameResponse{Name: name})
}

// partialStatus returns the status map of an operation on several items: 200
// when it succeeded, 207 when it failed for some items and 400 when it failed
// for all of them.
func partialStatus(status map[string]string, err error) (*statusMapOutput, error) {
	if err == nil {
		return &statusMapOutput{Status: http.StatusOK, Body: status}, nil
	}
	if len(status) == 0 {
		return nil, badRequest(err)
	}
	return &statusMapOutput{Status: http.StatusMultiStatus, Body: status}, nil
}

func (api *PcApi) GetProcess(_ context.Context, in *processNameInput) (*bodyOutput[*types.ProcessState], error) {
	state, err := api.project.GetProcessState(in.Name)
	if err != nil {
		return nil, badRequest(err)
	}
	return body(state), nil
}

func (api *PcApi) GetProcessInfo(_ context.Context, in *processNameInput) (*bodyOutput[*types.ProcessConfig], error) {
	cfg, err := api.project.GetProcessInfo(in.Name)
	if err != nil {
		return nil, badRequest(err)
	}
	return body(cfg), nil
}

func (api *PcApi) GetProcesses(_ context.Context, _ *struct{}) (*bodyOutput[*types.ProcessesState], error) {
	states, err := api.project.GetProcessesState()
	if err != nil {
		return nil, badRequest(err)
	}
	return body(states), nil
}

func (api *PcApi) GetProcessLogs(_ context.Context, in *processLogsInput) (*bodyOutput[LogsResponse], error) {
	logs, err := api.project.GetProcessLog(in.Name, in.EndOffset, in.Limit)
	if err != nil {
		return nil, badRequest(err)
	}
	return body(LogsResponse{Logs: logs}), nil
}

func (api *PcApi) TruncateProcessLogs(_ context.Context, in *processNameInput) (*bodyOutput[NameResponse], error) {
	if err := api.project.TruncateProcessLogs(in.Name); err != nil {
		return nil, badRequest(err)
	}
	return nameOutput(in.Name), nil
}

func (api *PcApi) StopProcess(_ context.Context, in *processNameInput) (*bodyOutput[NameResponse], error) {
	if err := api.project.StopProcess(in.Name); err != nil {
		return nil, badRequest(err)
	}
	return nameOutput(in.Name), nil
}

func (api *PcApi) SendSignal(_ context.Context, in *processSignalInput) (*bodyOutput[NameResponse], error) {
	if err := api.project.SendSignal(in.Name, in.Signal); err != nil {
		return nil, badRequest(err)
	}
	return nameOutput(in.Name), nil
}

func (api *PcApi) StopProcesses(_ context.Context, in *stopProcessesInput) (*statusMapOutput, error) {
	return partialStatus(api.project.StopProcesses(in.Body))
}

func (api *PcApi) StartProcess(_ context.Context, in *processNameInput) (*bodyOutput[NameResponse], error) {
	if err := api.project.StartProcess(in.Name); err != nil {
		return nil, badRequest(err)
	}
	return nameOutput(in.Name), nil
}

func (api *PcApi) StartNamespace(_ context.Context, in *namespaceNameInput) (*bodyOutput[NameResponse], error) {
	if err := api.project.StartNamespace(in.Name); err != nil {
		return nil, badRequest(err)
	}
	return nameOutput(in.Name), nil
}

func (api *PcApi) StopNamespace(_ context.Context, in *namespaceNameInput) (*bodyOutput[NameResponse], error) {
	if err := api.project.StopNamespace(in.Name); err != nil {
		return nil, badRequest(err)
	}
	return nameOutput(in.Name), nil
}

func (api *PcApi) RestartNamespace(_ context.Context, in *namespaceNameInput) (*bodyOutput[NameResponse], error) {
	if err := api.project.RestartNamespace(in.Name); err != nil {
		return nil, badRequest(err)
	}
	return nameOutput(in.Name), nil
}

func (api *PcApi) GetNamespaces(_ context.Context, _ *struct{}) (*bodyOutput[[]string], error) {
	namespaces, err := api.project.GetNamespaces()
	if err != nil {
		return nil, badRequest(err)
	}
	return body(namespaces), nil
}

func (api *PcApi) RestartProcess(_ context.Context, in *processNameInput) (*bodyOutput[NameResponse], error) {
	if err := api.project.RestartProcess(in.Name); err != nil {
		return nil, badRequest(err)
	}
	return nameOutput(in.Name), nil
}

func (api *PcApi) SendProcessKeys(_ context.Context, in *sendKeysInput) (*bodyOutput[NameResponse], error) {
	if err := api.project.SendProcessKeys(in.Name, in.Body.Keys); err != nil {
		return nil, badRequest(err)
	}
	return nameOutput(in.Name), nil
}

func (api *PcApi) ScaleProcess(_ context.Context, in *processScaleInput) (*bodyOutput[NameResponse], error) {
	if err := api.project.ScaleProcess(in.Name, in.Scale); err != nil {
		return nil, badRequest(err)
	}
	return nameOutput(in.Name), nil
}

func (api *PcApi) IsAlive(_ context.Context, _ *struct{}) (*bodyOutput[StatusResponse], error) {
	return body(StatusResponse{Status: "alive"}), nil
}

func (api *PcApi) GetProjectName(_ context.Context, _ *struct{}) (*bodyOutput[ProjectNameResponse], error) {
	name, err := api.project.GetProjectName()
	if err != nil {
		return nil, badRequest(err)
	}
	return body(ProjectNameResponse{ProjectName: name}), nil
}

func (api *PcApi) GetProcessPorts(_ context.Context, in *processNameInput) (*bodyOutput[*types.ProcessPorts], error) {
	ports, err := api.project.GetProcessPorts(in.Name)
	if err != nil {
		return nil, badRequest(err)
	}
	return body(ports), nil
}

type shutDownOutput struct {
	Body func(huma.Context)
}

// ShutDownProject writes its response before it shuts the project down, so
// the response is not lost with the server.
func (api *PcApi) ShutDownProject(_ context.Context, _ *struct{}) (*shutDownOutput, error) {
	return &shutDownOutput{Body: func(ctx huma.Context) {
		ctx.SetHeader("Content-Type", "application/json")
		ctx.SetStatus(http.StatusOK)
		_ = json.NewEncoder(ctx.BodyWriter()).Encode(StatusResponse{Status: "stopped"})
		_ = api.project.ShutDownProject()
	}}, nil
}

func (api *PcApi) UpdateProject(_ context.Context, in *updateProjectInput) (*statusMapOutput, error) {
	return partialStatus(api.project.UpdateProject(&in.Body))
}

func (api *PcApi) UpdateProcess(_ context.Context, in *updateProcessInput) (*bodyOutput[*types.ProcessConfig], error) {
	if err := api.project.UpdateProcess(&in.Body); err != nil {
		return nil, badRequest(err)
	}
	return body(&in.Body), nil
}

func (api *PcApi) GetProjectState(_ context.Context, in *projectStateInput) (*bodyOutput[*types.ProjectState], error) {
	state, err := api.project.GetProjectState(in.WithMemory)
	if err != nil {
		return nil, newStatusError(http.StatusInternalServerError, err)
	}
	return body(state), nil
}

func (api *PcApi) ReloadProject(_ context.Context, _ *struct{}) (*statusMapOutput, error) {
	return partialStatus(api.project.ReloadProject())
}

func (api *PcApi) GetDependencyGraph(_ context.Context, _ *struct{}) (*bodyOutput[*types.DependencyGraph], error) {
	graph, err := api.project.GetDependencyGraph()
	if err != nil {
		return nil, badRequest(err)
	}
	return body(graph), nil
}
