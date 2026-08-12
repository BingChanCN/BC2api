package pluginruntime

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

const (
	pluginProtocolVersion = "1"
	// connect-src 'self'（而非 'none'）: 允许 iframe 内 fetch/XHR 加载同源静态资源（Phaser 等游戏引擎的 Loader 依赖它）。
	// 安全性不受影响: sandbox 无 allow-same-origin，iframe 内请求是匿名请求（不携带主站 Cookie/JWT），
	// 只能访问插件自身的 public/** 与公开接口；登录态 API 仍需经父页面受控桥。
	pluginUIContentPolicy = "sandbox allow-scripts; default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; media-src 'self' blob:; worker-src 'self' blob:; connect-src 'self'; frame-src 'none'; child-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"
)

type Runtime struct {
	registry *Registry
	client   *http.Client
}

type InvokeRequest struct {
	Method         string `json:"method"`
	Path           string `json:"path"`
	Query          string `json:"query,omitempty"`
	ContentType    string `json:"content_type,omitempty"`
	Accept         string `json:"accept,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	Body           string `json:"body,omitempty"`
}

type InvokeResponse struct {
	Status      int    `json:"status"`
	ContentType string `json:"content_type,omitempty"`
	Encoding    string `json:"encoding"`
	Body        string `json:"body"`
}

type RegistryStatus struct {
	Enabled     bool                   `json:"enabled"`
	LastRefresh time.Time              `json:"last_refresh"`
	Plugins     []RegistryPluginStatus `json:"plugins"`
	Errors      map[string]string      `json:"errors"`
}

type RegistryPluginStatus struct {
	PublicPlugin
	Runtime string `json:"runtime"`
}

func NewRuntime(registry *Registry) *Runtime {
	cfg := registry.Config()
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = dialer.DialContext
	transport.MaxIdleConns = 32
	transport.MaxIdleConnsPerHost = 8
	transport.IdleConnTimeout = 60 * time.Second

	return newRuntimeWithClient(registry, &http.Client{
		Transport: transport,
		Timeout:   time.Duration(cfg.ProxyTimeoutSeconds) * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	})
}

func newRuntimeWithClient(registry *Registry, client *http.Client) *Runtime {
	return &Runtime{registry: registry, client: client}
}

func (r *Runtime) List(c *gin.Context) {
	role, ok := servermiddleware.GetUserRoleFromContext(c)
	if !ok {
		response.Unauthorized(c, "Authentication required")
		return
	}
	response.Success(c, r.registry.List(role))
}

func (r *Runtime) Diagnostics(c *gin.Context) {
	response.Success(c, r.registry.Status())
}

func (r *Runtime) Refresh(c *gin.Context) {
	if err := r.registry.ForceRefresh(); err != nil {
		response.InternalError(c, "Plugin registry refresh failed")
		return
	}
	response.Success(c, r.registry.Status())
}

func (r *Runtime) Invoke(c *gin.Context) {
	subject, ok := servermiddleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Authentication required")
		return
	}
	role, ok := servermiddleware.GetUserRoleFromContext(c)
	if !ok {
		response.Unauthorized(c, "Authentication required")
		return
	}

	pluginID := c.Param("id")
	entry, ok := r.registry.get(pluginID, role)
	if !ok || entry.manifest.API == nil || entry.apiURL == nil {
		response.NotFound(c, "Plugin API not found")
		return
	}

	maxEnvelopeBytes := r.registry.cfg.MaxRequestBodyBytes + 64*1024
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxEnvelopeBytes)
	var request InvokeRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid plugin request")
		return
	}
	request.Method = strings.ToUpper(strings.TrimSpace(request.Method))
	if !slices.Contains(entry.manifest.API.AllowedMethods, request.Method) {
		response.Error(c, http.StatusMethodNotAllowed, "Plugin method is not allowed")
		return
	}

	relativePath, err := cleanRequestPath(request.Path)
	if err != nil {
		response.BadRequest(c, "Invalid plugin path")
		return
	}
	query, err := parsePluginQuery(request.Query)
	if err != nil {
		response.BadRequest(c, "Invalid plugin query")
		return
	}
	if int64(len(request.Body)) > r.registry.cfg.MaxRequestBodyBytes {
		response.Error(c, http.StatusRequestEntityTooLarge, "Plugin request body is too large")
		return
	}
	if !validForwardHeader(request.ContentType, 256) ||
		!validForwardHeader(request.Accept, 256) ||
		!validForwardHeader(request.IdempotencyKey, 128) {
		response.BadRequest(c, "Invalid plugin request header")
		return
	}

	target := joinTargetURL(entry.apiURL, relativePath, query.Encode())
	upstreamRequest, err := http.NewRequestWithContext(
		c.Request.Context(),
		request.Method,
		target.String(),
		bytes.NewBufferString(request.Body),
	)
	if err != nil {
		response.InternalError(c, "Failed to create plugin request")
		return
	}
	if request.ContentType != "" {
		upstreamRequest.Header.Set("Content-Type", request.ContentType)
	}
	if request.Accept != "" {
		upstreamRequest.Header.Set("Accept", request.Accept)
	}
	if request.IdempotencyKey != "" {
		upstreamRequest.Header.Set("Idempotency-Key", request.IdempotencyKey)
	}
	setPluginIdentityHeaders(
		upstreamRequest,
		entry.apiSecret,
		pluginID,
		subject.UserID,
		role,
	)

	upstreamResponse, err := r.client.Do(upstreamRequest)
	if err != nil {
		response.Error(c, http.StatusBadGateway, "Plugin service is unavailable")
		return
	}
	defer upstreamResponse.Body.Close()

	if upstreamResponse.StatusCode >= 300 && upstreamResponse.StatusCode < 400 {
		response.Error(c, http.StatusBadGateway, "Plugin redirects are not supported")
		return
	}
	body, err := readLimited(upstreamResponse.Body, r.registry.cfg.MaxResponseBodyBytes)
	if err != nil {
		response.Error(c, http.StatusBadGateway, "Plugin response is too large or unreadable")
		return
	}

	encoding := "utf8"
	bodyText := string(body)
	if !utf8.Valid(body) {
		encoding = "base64"
		bodyText = base64.StdEncoding.EncodeToString(body)
	}
	response.Success(c, InvokeResponse{
		Status:      upstreamResponse.StatusCode,
		ContentType: sanitizeContentType(upstreamResponse.Header.Get("Content-Type")),
		Encoding:    encoding,
		Body:        bodyText,
	})
}

func (r *Runtime) ServeUI(c *gin.Context) {
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		c.Status(http.StatusMethodNotAllowed)
		return
	}
	entry, ok := r.registry.getAny(c.Param("id"))
	if !ok {
		c.Status(http.StatusNotFound)
		return
	}

	setPluginUIHeaders(c.Writer.Header())
	relativePath, err := cleanRequestPath(c.Param("path"))
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	if entry.manifest.Runtime.Type == "static" {
		r.serveStaticUI(c, entry, relativePath)
		return
	}
	r.serveProxyUI(c, entry, relativePath)
}

func (r *Runtime) serveStaticUI(c *gin.Context, entry *pluginEntry, relativePath string) {
	filePath := entry.staticEntry
	if relativePath != "" {
		if containsHiddenPathSegment(relativePath) {
			c.Status(http.StatusNotFound)
			return
		}
		candidate, err := resolveInside(entry.staticRoot, relativePath)
		if err == nil {
			if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() {
				filePath = candidate
			} else {
				err = os.ErrNotExist
			}
		}
		if err != nil && (!entry.manifest.Runtime.SPA || filepath.Ext(relativePath) != "") {
			c.Status(http.StatusNotFound)
			return
		}
	}

	file, err := os.Open(filePath)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		c.Status(http.StatusNotFound)
		return
	}
	if contentType := mime.TypeByExtension(filepath.Ext(filePath)); contentType != "" {
		c.Header("Content-Type", contentType)
	}
	if filepath.Clean(filePath) == filepath.Clean(entry.staticEntry) {
		c.Header("Cache-Control", "no-store")
	} else {
		c.Header("Cache-Control", "no-cache")
	}
	http.ServeContent(c.Writer, c.Request, filepath.Base(filePath), info.ModTime(), file)
}

func (r *Runtime) serveProxyUI(c *gin.Context, entry *pluginEntry, relativePath string) {
	target := joinTargetURL(entry.runtimeURL, relativePath, c.Request.URL.RawQuery)
	request, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, target.String(), nil)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	if accept := c.GetHeader("Accept"); validForwardHeader(accept, 256) {
		request.Header.Set("Accept", accept)
	}

	upstreamResponse, err := r.client.Do(request)
	if err != nil {
		c.String(http.StatusBadGateway, "Plugin UI is unavailable")
		return
	}
	defer upstreamResponse.Body.Close()
	if upstreamResponse.StatusCode >= 300 && upstreamResponse.StatusCode < 400 {
		c.String(http.StatusBadGateway, "Plugin UI redirects are not supported")
		return
	}
	body, err := readLimited(upstreamResponse.Body, r.registry.cfg.MaxResponseBodyBytes)
	if err != nil {
		c.String(http.StatusBadGateway, "Plugin UI response is too large or unreadable")
		return
	}
	if contentType := sanitizeContentType(upstreamResponse.Header.Get("Content-Type")); contentType != "" {
		c.Header("Content-Type", contentType)
	}
	if cacheControl := sanitizeCacheControl(upstreamResponse.Header.Get("Cache-Control")); cacheControl != "" {
		c.Header("Cache-Control", cacheControl)
	}
	c.Data(upstreamResponse.StatusCode, c.Writer.Header().Get("Content-Type"), body)
}

func cleanRequestPath(raw string) (string, error) {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "/"))
	if raw == "" {
		return "", nil
	}
	if len(raw) > 2048 || strings.ContainsAny(raw, "\\\x00?#") {
		return "", fmt.Errorf("invalid path")
	}
	decoded, err := url.PathUnescape(raw)
	if err != nil || strings.ContainsAny(decoded, "\\\x00?#") {
		return "", fmt.Errorf("invalid path")
	}
	for _, segment := range strings.Split(decoded, "/") {
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("invalid path segment")
		}
	}
	cleaned := strings.TrimPrefix(path.Clean("/"+decoded), "/")
	if cleaned == "." {
		return "", nil
	}
	return cleaned, nil
}

func parsePluginQuery(raw string) (url.Values, error) {
	if len(raw) > 4096 {
		return nil, fmt.Errorf("query is too long")
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, err
	}
	for key, items := range values {
		if len(key) > 256 || len(items) > 50 {
			return nil, fmt.Errorf("query exceeds limits")
		}
		for _, item := range items {
			if len(item) > 2048 {
				return nil, fmt.Errorf("query value exceeds limit")
			}
		}
	}
	return values, nil
}

func joinTargetURL(base *url.URL, relativePath, rawQuery string) *url.URL {
	target := *base
	basePath := strings.TrimSuffix(base.Path, "/")
	if relativePath == "" {
		target.Path = basePath + "/"
	} else {
		target.Path = basePath + "/" + relativePath
	}
	target.RawPath = ""
	target.RawQuery = rawQuery
	return &target
}

func setPluginIdentityHeaders(request *http.Request, secret []byte, pluginID string, userID int64, role string) {
	request.Header.Set("Authorization", "Bearer "+string(secret))
	request.Header.Set("X-Sub2API-Plugin-Protocol", pluginProtocolVersion)
	request.Header.Set("X-Sub2API-Plugin-ID", pluginID)
	request.Header.Set("X-Sub2API-User-ID", strconv.FormatInt(userID, 10))
	request.Header.Set("X-Sub2API-User-Role", role)
	request.Header.Set("X-Sub2API-Request-ID", randomID())
}

func randomID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return base64.RawURLEncoding.EncodeToString(value)
}

func validForwardHeader(value string, max int) bool {
	return len(value) <= max && !strings.ContainsAny(value, "\r\n")
}

func sanitizeContentType(value string) string {
	if !validForwardHeader(value, 256) {
		return "application/octet-stream"
	}
	mediaType, params, err := mime.ParseMediaType(value)
	if err != nil {
		return "application/octet-stream"
	}
	return mime.FormatMediaType(mediaType, params)
}

func sanitizeCacheControl(value string) string {
	if !validForwardHeader(value, 256) {
		return ""
	}
	return value
}

func containsHiddenPathSegment(value string) bool {
	for _, segment := range strings.Split(filepath.ToSlash(value), "/") {
		if strings.HasPrefix(segment, ".") {
			return true
		}
	}
	return false
}

func setPluginUIHeaders(header http.Header) {
	header.Set("Content-Security-Policy", pluginUIContentPolicy)
	header.Set("X-Frame-Options", "SAMEORIGIN")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=(), serial=(), bluetooth=()")
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("body exceeds %d bytes", limit)
	}
	return body, nil
}
