//go:build ignore

// generate-openapi writes the OpenAPI document of the HTTP API, which huma
// derives from the registered operations, to src/api/openapi.json.
package main

import (
	"fmt"
	"os"

	"github.com/f1bonacc1/process-compose/src/api"
	"github.com/gin-gonic/gin"
)

func main() {
	gin.SetMode(gin.ReleaseMode)
	document, err := api.OpenAPIDocument()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile("src/api/openapi.json", document, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
