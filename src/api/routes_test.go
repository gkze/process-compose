package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/f1bonacc1/process-compose/src/config"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestWebSocketCrossOriginHandshakeReturnsDocumentedError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	api := NewPcApi(&mockProject{})
	router.GET("/process/logs/ws", api.HandleLogsStream)
	router.GET("/process/states/ws", api.HandleStatesStream)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	document := loadOpenAPIDocument(t, filepath.Join(packageDirectory(t), "..", "docs", "swagger.json"))
	tests := []struct {
		operationID string
		path        string
	}{
		{operationID: "LogsStream", path: "/process/logs/ws?name=test&offset=0"},
		{operationID: "StatesStream", path: "/process/states/ws"},
	}
	for _, test := range tests {
		t.Run(test.operationID, func(t *testing.T) {
			header := http.Header{"Origin": []string{"https://cross-origin.example"}}
			connection, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+test.path, header)
			if connection != nil {
				_ = connection.Close()
			}
			if err == nil {
				t.Fatal("cross-origin WebSocket handshake unexpectedly succeeded")
			}
			if response == nil {
				t.Fatalf("cross-origin WebSocket handshake returned no HTTP response: %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusForbidden {
				t.Fatalf("cross-origin WebSocket handshake status = %d, want %d", response.StatusCode, http.StatusForbidden)
			}
			if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
				t.Errorf("cross-origin WebSocket handshake Content-Type = %q, want application/json", contentType)
			}
			var errorResponse ErrorResponse
			if err := json.NewDecoder(response.Body).Decode(&errorResponse); err != nil {
				t.Fatalf("cross-origin WebSocket handshake response is not JSON: %v", err)
			}
			if errorResponse.Error == "" {
				t.Error("cross-origin WebSocket handshake response has an empty error")
			}

			path := strings.Split(test.path, "?")[0]
			pathItem := document.Paths.Find(path)
			if pathItem == nil || pathItem.Get == nil || pathItem.Get.Responses.Value("403") == nil {
				t.Errorf("%s cross-origin HTTP 403 response is not documented", test.operationID)
			}
		})
	}
}

func TestWebSocketUpgradeErrorsUseDocumentedJSONSchema(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.GET("/process/states/ws", NewPcApi(&mockProject{}).HandleStatesStream)
	request := httptest.NewRequest(http.MethodGet, "/process/states/ws", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("WebSocket upgrade status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Errorf("WebSocket upgrade Content-Type = %q, want application/json", contentType)
	}
	var errorResponse ErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &errorResponse); err != nil {
		t.Fatalf("WebSocket upgrade response is not JSON: %v\n%s", err, response.Body.Bytes())
	}
	if errorResponse.Error == "" {
		t.Errorf("WebSocket upgrade response has an empty error: %s", response.Body.Bytes())
	}
}

func TestWebSocketLogsRejectsInvalidFollow(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.GET("/process/logs/ws", NewPcApi(&mockProject{}).HandleLogsStream)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	connection, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/process/logs/ws?name=test&offset=0&follow=truthy", nil)
	if connection != nil {
		_ = connection.Close()
	}
	if err == nil {
		t.Fatal("invalid follow value unexpectedly upgraded to WebSocket")
	}
	if response == nil {
		t.Fatalf("invalid follow value returned no HTTP response: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid follow status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
}

func TestWebSocketLogsRequiresProcessName(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.GET("/process/logs/ws", NewPcApi(&mockProject{}).HandleLogsStream)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	for _, test := range []struct {
		name  string
		query string
	}{
		{name: "omitted", query: "offset=0"},
		{name: "only separators", query: "name=,,&offset=0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			connection, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/process/logs/ws?"+test.query, nil)
			if connection != nil {
				_ = connection.Close()
			}
			if err == nil {
				t.Fatal("request without a process name unexpectedly upgraded to WebSocket")
			}
			if response == nil {
				t.Fatalf("request without a process name returned no HTTP response: %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("request without a process name status = %d, want %d", response.StatusCode, http.StatusBadRequest)
			}
			if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
				t.Errorf("request without a process name Content-Type = %q, want application/json", contentType)
			}
			var errorResponse ErrorResponse
			if err := json.NewDecoder(response.Body).Decode(&errorResponse); err != nil {
				t.Fatalf("request without a process name response is not JSON: %v", err)
			}
			if errorResponse.Error == "" {
				t.Error("request without a process name response has an empty error")
			}
		})
	}
}

func TestTokenAuthMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(TokenAuthMiddleware("valid-token-1234567890"))
	r.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	t.Run("no token", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected status code %d, got %d", http.StatusUnauthorized, w.Code)
		}
		if got, want := w.Body.String(), "{\"error\":\"unauthorized\"}"; got != want {
			t.Errorf("Expected body %s, got %s", want, got)
		}
	})

	t.Run("invalid token", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set(config.TokenHeader, "wrong-token")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected status code %d, got %d", http.StatusUnauthorized, w.Code)
		}
		if got, want := w.Body.String(), "{\"error\":\"unauthorized\"}"; got != want {
			t.Errorf("Expected body %s, got %s", want, got)
		}
	})

	t.Run("valid token", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set(config.TokenHeader, "valid-token-1234567890")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
		}
	})
}
