package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"reflect"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/f1bonacc1/process-compose/src/config"
	"github.com/gin-gonic/gin"
)

const (
	// DefaultServerURL is the server documented in the committed OpenAPI
	// document; a running server documents its own listen address instead.
	DefaultServerURL = "http://localhost:8080/"
	apiTokenScheme   = "ApiTokenAuth"
	openAPIPath      = "/openapi"
	docsPath         = "/docs"
)

func init() {
	huma.NewError = newErrorResponse
}

// newErrorResponse makes every error huma writes an ErrorResponse. Invalid
// requests are reported as 400 Bad Request, which is what the API has always
// returned for them, instead of huma's 422. The message names each problem
// and where it is, but not the offending value, which can be a whole request
// body (with environment secrets in it).
func newErrorResponse(status int, msg string, errs ...error) huma.StatusError {
	if status == http.StatusUnprocessableEntity {
		status = http.StatusBadRequest
	}
	details := make([]string, 0, len(errs))
	for _, err := range errs {
		var detail *huma.ErrorDetail
		switch {
		case err == nil:
			continue
		case errors.As(err, &detail):
			if detail.Location != "" {
				details = append(details, detail.Message+" ("+detail.Location+")")
			} else {
				details = append(details, detail.Message)
			}
		default:
			details = append(details, err.Error())
		}
	}
	if len(details) > 0 {
		msg += ": " + strings.Join(details, "; ")
	}
	return &ErrorResponse{status: status, Message: msg}
}

// jsonBodyAPI decodes every request body as JSON, whatever its Content-Type,
// as JSON binding did before huma; huma alone rejects bodies whose
// Content-Type is not a registered format.
type jsonBodyAPI struct {
	huma.API
}

func (a jsonBodyAPI) Unmarshal(_ string, data []byte, v any) error {
	return a.API.Unmarshal("application/json", data, v)
}

// Config forwards the huma configuration, which huma looks up on the API.
func (a jsonBodyAPI) Config() huma.Config {
	return a.API.(interface{ Config() huma.Config }).Config()
}

// schemaNamer names component schemas after their Go package and type (for
// example types.ProcessState), as the API documentation always has.
func schemaNamer(t reflect.Type, hint string) string {
	name := huma.DefaultSchemaNamer(t, hint)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Name() == "" || t.PkgPath() == "" {
		return name
	}
	return path.Base(t.PkgPath()) + "." + name
}

// newHumaAPI creates the API on router. Request decoding matches the JSON
// binding the API used before huma: fields are optional and unknown fields
// are ignored.
func newHumaAPI(router *gin.Engine, serverURL string) huma.API {
	registry := huma.NewMapRegistry("#/components/schemas/", schemaNamer)
	cfg := huma.Config{
		OpenAPI: &huma.OpenAPI{
			OpenAPI: "3.1.0",
			Info: &huma.Info{
				Title:       "Process Compose API",
				Version:     "1.0",
				Description: "This is a sample Process Compose server.",
				Contact: &huma.Contact{
					Name: "Process Compose Discord Channel",
					URL:  "https://discord.gg/S4xgmRSHdC",
				},
				License: &huma.License{
					Name: "Apache 2.0",
					URL:  "http://www.apache.org/licenses/LICENSE-2.0.html",
				},
			},
			ExternalDocs: &huma.ExternalDocs{URL: "https://f1bonacc1.github.io/process-compose/"},
			Servers:      []*huma.Server{{URL: serverURL}},
			Components: &huma.Components{
				Schemas: registry,
				SecuritySchemes: map[string]*huma.SecurityScheme{
					apiTokenScheme: {
						Type:        "apiKey",
						In:          "header",
						Name:        config.TokenHeader,
						Description: "Required when API token authentication is configured; otherwise no API key is required.",
					},
				},
			},
			// The token is optional: it is only checked when one is configured.
			Security: []map[string][]string{{apiTokenScheme: {}}, {}},
		},
		OpenAPIPath:   openAPIPath,
		DocsPath:      docsPath,
		DocsRenderer:  huma.DocsRendererSwaggerUI,
		Formats:       map[string]huma.Format{"application/json": huma.DefaultJSONFormat, "json": huma.DefaultJSONFormat},
		DefaultFormat: "application/json",
	}
	cfg.FieldsOptionalByDefault = true
	cfg.AllowAdditionalPropertiesByDefault = true
	return jsonBodyAPI{humagin.New(router, cfg)}
}

// OpenAPIDocument returns the OpenAPI 3.0 document of the API, as committed in
// openapi.json and served at /openapi-3.0.json.
func OpenAPIDocument() ([]byte, error) {
	router := gin.New()
	humaAPI := newHumaAPI(router, DefaultServerURL)
	registerOperations(humaAPI, NewPcApi(nil))
	compact, err := humaAPI.OpenAPI().Downgrade()
	if err != nil {
		return nil, err
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, compact, "", "  "); err != nil {
		return nil, err
	}
	indented.WriteByte('\n')
	return indented.Bytes(), nil
}
