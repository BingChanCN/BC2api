package pluginruntime

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

const testPluginSecret = "0123456789abcdef0123456789abcdef"

func TestRegistryRefreshAndRoleFiltering(t *testing.T) {
	root := t.TempDir()
	writeTestPlugin(t, root, Manifest{
		SchemaVersion: ManifestSchemaVersion,
		ID:            "hello",
		Name:          "Hello",
		Version:       "1.0.0",
		Enabled:       true,
		Visibility:    "user",
		SortOrder:     20,
		Runtime:       RuntimeManifest{Type: "static", Entry: "public/index.html"},
	}, "<h1>Hello</h1>", "")
	writeTestPlugin(t, root, Manifest{
		SchemaVersion: ManifestSchemaVersion,
		ID:            "admin-tool",
		Name:          "Admin tool",
		Version:       "1.0.0",
		Enabled:       true,
		Visibility:    "admin",
		SortOrder:     10,
		Runtime:       RuntimeManifest{Type: "static", Entry: "public/index.html"},
	}, "<h1>Admin</h1>", "")

	registry := newTestRegistry(root)
	if err := registry.ForceRefresh(); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}

	userPlugins := registry.List("user")
	if len(userPlugins) != 1 || userPlugins[0].ID != "hello" {
		t.Fatalf("user plugins = %#v, want only hello", userPlugins)
	}
	adminPlugins := registry.List("admin")
	if len(adminPlugins) != 2 || adminPlugins[0].ID != "admin-tool" || adminPlugins[1].ID != "hello" {
		t.Fatalf("admin plugins = %#v, want admin-tool then hello", adminPlugins)
	}

	manifestPath := filepath.Join(root, "hello", "manifest.json")
	manifest := readTestManifest(t, manifestPath)
	manifest.Name = "Hello v2"
	manifest.Enabled = false
	writeJSONFile(t, manifestPath, manifest)
	if err := registry.ForceRefresh(); err != nil {
		t.Fatalf("refresh after disable: %v", err)
	}
	if plugins := registry.List("user"); len(plugins) != 0 {
		t.Fatalf("disabled plugin still listed: %#v", plugins)
	}

	manifest.Enabled = true
	manifest.Runtime.Entry = "../manifest.json"
	writeJSONFile(t, manifestPath, manifest)
	if err := registry.ForceRefresh(); err != nil {
		t.Fatalf("refresh with invalid plugin should isolate it, got: %v", err)
	}
	status := registry.Status()
	if _, ok := status.Errors["hello"]; !ok {
		t.Fatalf("invalid plugin missing from diagnostics: %#v", status.Errors)
	}
	if _, ok := registry.getAny("hello"); ok {
		t.Fatal("invalid plugin remained active")
	}
}

func TestServeStaticUIOnlyExposesPublishedFiles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	writeTestPlugin(t, root, Manifest{
		SchemaVersion: ManifestSchemaVersion,
		ID:            "hello",
		Name:          "Hello",
		Version:       "1.0.0",
		Enabled:       true,
		Visibility:    "user",
		Runtime:       RuntimeManifest{Type: "static", Entry: "public/index.html", SPA: true},
	}, "<h1>Hello</h1>", "")
	writeTextFile(t, filepath.Join(root, "hello", "public", "app.js"), "window.hello = true")
	writeTextFile(t, filepath.Join(root, "hello", "public", ".private"), "hidden")

	registry := newTestRegistry(root)
	if err := registry.ForceRefresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	runtime := NewRuntime(registry)
	router := gin.New()
	router.GET("/plugin-runtime/:id/ui/*path", runtime.ServeUI)

	index := performRequest(router, http.MethodGet, "/plugin-runtime/hello/ui/", nil)
	if index.Code != http.StatusOK || !strings.Contains(index.Body.String(), "Hello") {
		t.Fatalf("index response = %d %q", index.Code, index.Body.String())
	}
	if got := index.Header().Get("Content-Security-Policy"); !strings.Contains(got, "connect-src 'self'") || !strings.Contains(got, "sandbox allow-scripts") {
		t.Fatalf("plugin CSP = %q", got)
	}
	if got := index.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("plugin ACAO = %q", got)
	}
	if got := index.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Fatalf("X-Frame-Options = %q", got)
	}

	asset := performRequest(router, http.MethodGet, "/plugin-runtime/hello/ui/app.js", nil)
	if asset.Code != http.StatusOK || !strings.Contains(asset.Body.String(), "window.hello") {
		t.Fatalf("asset response = %d %q", asset.Code, asset.Body.String())
	}

	for _, requestPath := range []string{
		"/plugin-runtime/hello/ui/.private",
		"/plugin-runtime/hello/ui/manifest.json",
		"/plugin-runtime/hello/ui/missing.js",
	} {
		response := performRequest(router, http.MethodGet, requestPath, nil)
		if response.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404", requestPath, response.Code)
		}
	}
}

func TestInvokeReplacesBrowserCredentialsWithPluginIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	writeTestPlugin(t, root, Manifest{
		SchemaVersion: ManifestSchemaVersion,
		ID:            "echo",
		Name:          "Echo",
		Version:       "1.0.0",
		Enabled:       true,
		Visibility:    "user",
		Runtime:       RuntimeManifest{Type: "static", Entry: "public/index.html"},
		API: &APIManifest{
			BaseURL:        "http://sub2api-plugin-echo/api",
			SecretFile:     ".api-secret",
			AllowedMethods: []string{"POST"},
		},
	}, "<h1>Echo</h1>", testPluginSecret)

	registry := newTestRegistry(root)
	if err := registry.ForceRefresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	var captured *http.Request
	var capturedBody string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		captured = request.Clone(request.Context())
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read upstream request: %v", err)
		}
		capturedBody = string(body)
		return &http.Response{
			StatusCode: http.StatusCreated,
			Header: http.Header{
				"Content-Type": []string{"application/json; charset=utf-8"},
				"Set-Cookie":   []string{"plugin_session=must-not-leak"},
			},
			Body: io.NopCloser(strings.NewReader(`{"ok":true}`)),
		}, nil
	})}
	runtime := newRuntimeWithClient(registry, client)
	router := gin.New()
	router.POST("/api/v1/plugins/:id/invoke", func(c *gin.Context) {
		c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 42})
		c.Set(string(servermiddleware.ContextKeyUserRole), "user")
		c.Next()
	}, runtime.Invoke)

	invokeBody := `{"method":"POST","path":"scores","query":"page=2","content_type":"application/json","body":"{\"score\":7}"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/plugins/echo/invoke", strings.NewReader(invokeBody))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer browser-jwt")
	request.Header.Set("Cookie", "session=browser-cookie")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("invoke response = %d %q", response.Code, response.Body.String())
	}
	if captured == nil {
		t.Fatal("plugin upstream was not called")
	}
	if got := captured.URL.String(); got != "http://sub2api-plugin-echo/api/scores?page=2" {
		t.Fatalf("upstream URL = %q", got)
	}
	if got := captured.Header.Get("Authorization"); got != "Bearer "+testPluginSecret {
		t.Fatalf("upstream Authorization = %q", got)
	}
	if got := captured.Header.Get("Cookie"); got != "" {
		t.Fatalf("browser Cookie leaked upstream: %q", got)
	}
	if got := captured.Header.Get("X-Sub2API-User-ID"); got != "42" {
		t.Fatalf("upstream user id = %q", got)
	}
	if got := captured.Header.Get("X-Sub2API-User-Role"); got != "user" {
		t.Fatalf("upstream user role = %q", got)
	}
	if got := captured.Header.Get("X-Sub2API-Plugin-ID"); got != "echo" {
		t.Fatalf("upstream plugin id = %q", got)
	}
	if capturedBody != `{"score":7}` {
		t.Fatalf("upstream body = %q", capturedBody)
	}
	if got := response.Header().Values("Set-Cookie"); len(got) != 0 {
		t.Fatalf("upstream Set-Cookie leaked to browser: %#v", got)
	}

	var envelope struct {
		Code int            `json:"code"`
		Data InvokeResponse `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Code != 0 || envelope.Data.Status != http.StatusCreated || envelope.Data.Body != `{"ok":true}` {
		t.Fatalf("invoke envelope = %#v", envelope)
	}
}

func newTestRegistry(root string) *Registry {
	return NewRegistry(config.PluginConfig{
		Enabled:                true,
		Directory:              root,
		RefreshIntervalSeconds: 3600,
		ProxyTimeoutSeconds:    5,
		MaxRequestBodyBytes:    1024 * 1024,
		MaxResponseBodyBytes:   1024 * 1024,
	})
}

func writeTestPlugin(t *testing.T, root string, manifest Manifest, indexHTML, secret string) {
	t.Helper()
	pluginRoot := filepath.Join(root, manifest.ID)
	if err := os.MkdirAll(filepath.Join(pluginRoot, "public"), 0o755); err != nil {
		t.Fatalf("create plugin directory: %v", err)
	}
	writeJSONFile(t, filepath.Join(pluginRoot, "manifest.json"), manifest)
	writeTextFile(t, filepath.Join(pluginRoot, "public", "index.html"), indexHTML)
	if secret != "" {
		writeTextFile(t, filepath.Join(pluginRoot, ".api-secret"), secret)
	}
}

func readTestManifest(t *testing.T, path string) Manifest {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return manifest
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("encode JSON: %v", err)
	}
	writeTextFile(t, path, string(data))
}

func writeTextFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func performRequest(handler http.Handler, method, target string, body []byte) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
