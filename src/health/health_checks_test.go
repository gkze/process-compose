package health

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/f1bonacc1/go-health/v2"
	"github.com/f1bonacc1/go-health/v2/checkers"
	"github.com/f1bonacc1/process-compose/src/command"
	"github.com/google/go-cmp/cmp"
)

func TestProber_getHttpChecker(t *testing.T) {
	type fields struct {
		Name       string
		Probe      Probe
		Env        []string
		OnCheckEnd func(bool, bool, string, any)
	}
	tests := []struct {
		name    string
		fields  fields
		want    health.ICheckable
		wantErr bool
	}{
		{
			name:   "Happy path: default http probe",
			fields: fields{Probe: Probe{HttpGet: &HttpProbe{}}},
			want: &checkers.HTTP{Config: &checkers.HTTPConfig{
				URL: &url.URL{
					Scheme: "http",
					Host:   "127.0.0.1",
					Path:   "/",
				},
				Method:     http.MethodGet,
				StatusCode: 200,
				Client:     &http.Client{Timeout: time.Second},
				Timeout:    time.Second,
			}},
		},
		{
			name: "Happy path: include headers in http probe",
			fields: fields{
				Name:  "http",
				Probe: Probe{HttpGet: &HttpProbe{Headers: map[string]string{"foo": "bar"}}},
			},
			want: &checkers.HTTP{
				Config: &checkers.HTTPConfig{
					URL: &url.URL{
						Scheme: "http",
						Host:   "127.0.0.1",
						Path:   "/",
					},
					Method:     http.MethodGet,
					Headers:    http.Header{"Foo": {"bar"}},
					StatusCode: 200,
					Client:     &http.Client{Timeout: time.Second},
					Timeout:    time.Second,
				},
			},
		},
		{
			name: "Happy path: include status in http probe",
			fields: fields{
				Name:  "http",
				Probe: Probe{HttpGet: &HttpProbe{StatusCode: 204}},
			},
			want: &checkers.HTTP{
				Config: &checkers.HTTPConfig{
					URL: &url.URL{
						Scheme: "http",
						Host:   "127.0.0.1",
						Path:   "/",
					},
					Method:     http.MethodGet,
					StatusCode: 204,
					Client:     &http.Client{Timeout: time.Second},
					Timeout:    time.Second,
				},
			},
		},
		{
			name: "Happy path: include method in http probe",
			fields: fields{
				Name:  "http",
				Probe: Probe{HttpGet: &HttpProbe{Method: http.MethodHead}},
			},
			want: &checkers.HTTP{
				Config: &checkers.HTTPConfig{
					URL: &url.URL{
						Scheme: "http",
						Host:   "127.0.0.1",
						Path:   "/",
					},
					Method:     http.MethodHead,
					StatusCode: 200,
					Client:     &http.Client{Timeout: time.Second},
					Timeout:    time.Second,
				},
			},
		},
		{
			name: "Happy path: error codes are not allowed",
			fields: fields{
				Name:  "http",
				Probe: Probe{HttpGet: &HttpProbe{StatusCode: 404}},
			},
			want: &checkers.HTTP{
				Config: &checkers.HTTPConfig{
					URL: &url.URL{
						Scheme: "http",
						Host:   "127.0.0.1",
						Path:   "/",
					},
					Method:     http.MethodGet,
					StatusCode: 200,
					Client:     &http.Client{Timeout: time.Second},
					Timeout:    time.Second,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := New(tt.fields.Name, tt.fields.Probe, tt.fields.Env, *command.DefaultShellConfig(), tt.fields.OnCheckEnd)
			if (err != nil) != tt.wantErr {
				t.Errorf("health.New error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			got, err := p.getHttpChecker()
			if (err != nil) != tt.wantErr {
				t.Errorf("Prober.getHttpChecker error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !cmp.Equal(got, tt.want) {
				t.Errorf("Probe.getHttpChecker diff = %s", cmp.Diff(got, tt.want))
			}
		})
	}
}

// TestProber_httpMethodSentToServer checks the configured method is the one
// the probed server receives, so a server that only answers that method is
// reported healthy.
func TestProber_httpMethodSentToServer(t *testing.T) {
	for _, method := range []string{"", http.MethodHead, http.MethodPost} {
		want := method
		if want == "" {
			want = http.MethodGet
		}
		t.Run(want, func(t *testing.T) {
			received := make(chan string, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case received <- r.Method:
				default:
				}
				if r.Method != want {
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			}))
			defer srv.Close()
			srvUrl, err := url.Parse(srv.URL)
			if err != nil {
				t.Fatal(err)
			}

			p, err := New("http", Probe{HttpGet: &HttpProbe{
				Method: method,
				Host:   srvUrl.Hostname(),
				Port:   srvUrl.Port(),
			}}, nil, *command.DefaultShellConfig(), func(bool, bool, string, any) {})
			if err != nil {
				t.Fatal(err)
			}
			checker, err := p.getHttpChecker()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := checker.Status(); err != nil {
				t.Errorf("probe status error = %v", err)
			}
			if got := <-received; got != want {
				t.Errorf("server received method %s, want %s", got, want)
			}
		})
	}
}
