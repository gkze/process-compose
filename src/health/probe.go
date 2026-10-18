package health

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// httpProbeMethods are the request methods an HTTP probe may use: the standard
// methods defined by RFC 9110 and RFC 5789 (PATCH). Methods are case-sensitive.
var httpProbeMethods = []string{
	http.MethodGet,
	http.MethodHead,
	http.MethodPost,
	http.MethodPut,
	http.MethodPatch,
	http.MethodDelete,
	http.MethodConnect,
	http.MethodOptions,
	http.MethodTrace,
}

type Probe struct {
	Exec             *ExecProbe `yaml:"exec,omitempty" json:"exec,omitempty"`
	HttpGet          *HttpProbe `yaml:"http_get,omitempty" json:"httpGet,omitempty"`
	InitialDelay     int        `yaml:"initial_delay_seconds,omitempty" json:"initialDelay,omitempty"`
	PeriodSeconds    int        `yaml:"period_seconds,omitempty" json:"periodSeconds,omitempty"`
	TimeoutSeconds   int        `yaml:"timeout_seconds,omitempty" json:"timeoutSeconds,omitempty"`
	SuccessThreshold int        `yaml:"success_threshold,omitempty" json:"successThreshold,omitempty"`
	FailureThreshold int        `yaml:"failure_threshold,omitempty" json:"failureThreshold,omitempty"`
}

type ExecProbe struct {
	Command    string `yaml:"command,omitempty" json:"command,omitempty"`
	WorkingDir string `yaml:"working_dir,omitempty" json:"workingDir,omitempty"`
}

type HttpProbe struct {
	Method     string            `yaml:"method,omitempty" json:"method,omitempty" jsonschema:"enum=GET,enum=HEAD,enum=POST,enum=PUT,enum=PATCH,enum=DELETE,enum=CONNECT,enum=OPTIONS,enum=TRACE"`
	Host       string            `yaml:"host,omitempty" json:"host,omitempty"`
	Path       string            `yaml:"path,omitempty" json:"path,omitempty"`
	Scheme     string            `yaml:"scheme,omitempty" json:"scheme,omitempty"`
	Port       string            `yaml:"port,omitempty" json:"port,omitempty"`
	NumPort    int               `yaml:"num_port,omitempty" json:"numPort,omitempty"`
	Headers    map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	StatusCode int               `yaml:"status_code,omitempty" json:"statusCode,omitempty"`
}

// TransformSchema documents the method values the HTTP API accepts.
func (HttpProbe) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	methods := make([]any, len(httpProbeMethods))
	for i, method := range httpProbeMethods {
		methods[i] = method
	}
	s.Properties["method"].Enum = methods
	return s
}

func (h *HttpProbe) getUrl() (*url.URL, error) {
	urlStr := ""
	if h.NumPort != 0 {
		urlStr = fmt.Sprintf("%s://%s:%d%s", h.Scheme, h.Host, h.NumPort, h.Path)
	}
	if h.NumPort == 0 {
		urlStr = fmt.Sprintf("%s://%s%s", h.Scheme, h.Host, h.Path)
	}
	return url.Parse(urlStr)
}

func (p *Probe) ValidateAndSetDefaults() {
	if p.InitialDelay < 0 {
		p.InitialDelay = 0
	}
	if p.PeriodSeconds < 1 {
		p.PeriodSeconds = 10
	}
	if p.TimeoutSeconds < 1 {
		p.TimeoutSeconds = 1
	}
	if p.SuccessThreshold < 1 {
		p.SuccessThreshold = 1
	}
	if p.FailureThreshold < 1 {
		p.FailureThreshold = 3
	}

	if p.HttpGet != nil {
		p.HttpGet.validateAndSetHttpDefaults()
	}
}

// Validate reports configuration errors that defaulting cannot repair.
func (p *Probe) Validate() error {
	if p.HttpGet != nil {
		return p.HttpGet.validate()
	}
	return nil
}

func (p *HttpProbe) validate() error {
	if p.Method != "" && !slices.Contains(httpProbeMethods, p.Method) {
		return fmt.Errorf("invalid http_get method '%s': must be one of %s", p.Method, strings.Join(httpProbeMethods, ", "))
	}
	return nil
}

func (p *HttpProbe) validateAndSetHttpDefaults() {
	if p.Method == "" {
		p.Method = http.MethodGet
	}
	if len(strings.TrimSpace(p.Host)) == 0 {
		p.Host = "127.0.0.1"
	}
	if len(strings.TrimSpace(p.Scheme)) == 0 {
		p.Scheme = "http"
	}
	if len(strings.TrimSpace(p.Path)) == 0 {
		p.Path = "/"
	}
	if p.Port == "" {
		p.NumPort = 0
	} else {
		p.NumPort, _ = strconv.Atoi(p.Port)
	}
	if p.NumPort < 1 || p.NumPort > 65535 {
		// if undefined or wrong value - will be treated as undefined
		p.NumPort = 0
	}
	if p.StatusCode < 1 || p.StatusCode >= 400 {
		p.StatusCode = http.StatusOK
	}
}
