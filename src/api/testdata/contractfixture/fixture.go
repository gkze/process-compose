package contractfixture

import (
	"os"

	g "github.com/gin-gonic/gin"
)

type PcApi struct{}

type Payload struct {
	Name string `json:"name"`
}

func (*PcApi) TypedHandler(context *g.Context) {
	const memoryKey = "withMemory"
	_ = context.DefaultQuery(memoryKey, "false")

	const replicasKey = "replicas"
	_ = context.Param(replicasKey)

	var payload Payload
	_ = context.ShouldBindJSON(&payload)
}

type unrelatedContext struct{}

func (*unrelatedContext) Query(string) string { return "" }

func (*PcApi) UnrelatedQueryMethod(context *unrelatedContext) {
	_ = context.Query("not-an-http-input")
}

func (*PcApi) RegisteredHandler(context *g.Context) {
	_ = context.Query("known")
}

func (*PcApi) DynamicInput(context *g.Context) {
	_ = context.Query(os.Getenv("QUERY_NAME"))
}
