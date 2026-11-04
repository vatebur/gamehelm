package tests

import (
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/vatebur/gamehelm/internal/auth"
	"github.com/vatebur/gamehelm/internal/config"
	"github.com/vatebur/gamehelm/internal/control"
	"github.com/vatebur/gamehelm/internal/web"
)

const httpTestPassword = "test-password-2.0"

var (
	csrfFieldPattern = regexp.MustCompile(`name="csrf" value="([^"]+)"`)
	csrfMetaPattern  = regexp.MustCompile(`name="csrf-token" content="([^"]+)"`)
	httpHashOnce     sync.Once
	httpPasswordHash string
	httpHashError    error
)

type httpController struct {
	mu      sync.Mutex
	cfg     config.Config
	actions []string
}

func (c *httpController) Views() []control.ServiceView {
	views := make([]control.ServiceView, 0, len(c.cfg.Services))
	for _, id := range c.cfg.ServiceIDs() {
		svc := c.cfg.Services[id]
		views = append(views, control.ServiceView{ID: id, Name: svc.DisplayName, Runtime: svc.Runtime, Available: true, Status: "已停止"})
	}
	return views
}
func (c *httpController) ServiceIDs() []string { return c.cfg.ServiceIDs() }
func (c *httpController) Action(id, action, _ string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.actions = append(c.actions, id+":"+action)
	return action != "extend", nil
}

func httpConfig(t *testing.T) config.Config {
	t.Helper()
	httpHashOnce.Do(func() { httpPasswordHash, httpHashError = auth.HashPassword(httpTestPassword) })
	if httpHashError != nil {
		t.Fatal(httpHashError)
	}
	return config.Config{
		Auth: config.AuthConfig{PasswordHash: httpPasswordHash},
		Services: map[string]config.Service{
			"palworld": {DisplayName: "帕鲁世界", Runtime: "docker", Container: "palworld"},
			"terraria": {DisplayName: "泰拉瑞亚", Runtime: "systemd", Unit: "terraria.service"},
		},
	}
}

func httpHandler(t *testing.T, cfg config.Config) (http.Handler, *httpController) {
	t.Helper()
	ctrl := &httpController{cfg: cfg}
	server, err := web.New(cfg, ctrl, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler(), ctrl
}

func newHTTPTestServer(t *testing.T, cfg config.Config) (*httptest.Server, *http.Client, *httpController) {
	t.Helper()
	handler, ctrl := httpHandler(t, cfg)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Jar = jar
	return server, client, ctrl
}

func readHTTPBody(t *testing.T, response *http.Response) []byte {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	if closeErr := response.Body.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	return body
}
func getLoginCSRF(t *testing.T, client *http.Client, serverURL string) string {
	t.Helper()
	response, err := client.Get(serverURL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	match := csrfFieldPattern.FindSubmatch(readHTTPBody(t, response))
	if len(match) != 2 {
		t.Fatal("missing login CSRF")
	}
	return string(match[1])
}
func postLogin(t *testing.T, client *http.Client, serverURL, csrf, password string) (*http.Response, []byte) {
	t.Helper()
	form := url.Values{"csrf": {csrf}, "password": {password}}
	response, err := client.Post(serverURL+"/login", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	return response, readHTTPBody(t, response)
}
func loginHTTP(t *testing.T, client *http.Client, serverURL string) string {
	t.Helper()
	response, body := postLogin(t, client, serverURL, getLoginCSRF(t, client, serverURL), httpTestPassword)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login status=%d", response.StatusCode)
	}
	match := csrfMetaPattern.FindSubmatch(body)
	if len(match) != 2 {
		t.Fatal("missing control CSRF")
	}
	return string(match[1])
}
func sessionCookieValue(t *testing.T, client *http.Client, serverURL string) string {
	t.Helper()
	parsed, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range client.Jar.Cookies(parsed) {
		if cookie.Name == "gamehelm_session" {
			return cookie.Value
		}
	}
	t.Fatal("missing session cookie")
	return ""
}

func TestLoginCSRFUnaffectedByFaviconRequest(t *testing.T) {
	server, client, _ := newHTTPTestServer(t, httpConfig(t))
	csrf := getLoginCSRF(t, client, server.URL)
	response, err := client.Get(server.URL + "/favicon.ico")
	if err != nil {
		t.Fatal(err)
	}
	readHTTPBody(t, response)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("favicon status=%d", response.StatusCode)
	}
	response, _ = postLogin(t, client, server.URL, csrf, "wrong-password")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong-password status=%d", response.StatusCode)
	}
}

func TestLoginLocksAfterFiveFailures(t *testing.T) {
	server, client, _ := newHTTPTestServer(t, httpConfig(t))
	for attempt := 0; attempt < 5; attempt++ {
		response, _ := postLogin(t, client, server.URL, getLoginCSRF(t, client, server.URL), "wrong-password")
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d status=%d", attempt, response.StatusCode)
		}
	}
	response, _ := postLogin(t, client, server.URL, getLoginCSRF(t, client, server.URL), httpTestPassword)
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("locked status=%d", response.StatusCode)
	}
}

func TestSuccessfulLoginsCreateDistinctSessions(t *testing.T) {
	server, first, _ := newHTTPTestServer(t, httpConfig(t))
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	second := *server.Client()
	second.Jar = jar
	loginHTTP(t, first, server.URL)
	loginHTTP(t, &second, server.URL)
	if sessionCookieValue(t, first, server.URL) == sessionCookieValue(t, &second, server.URL) {
		t.Fatal("sessions reused")
	}
}

func TestControlPageRendersMixedServices(t *testing.T) {
	cfg := httpConfig(t)
	cfg.Services["factorio"] = config.Service{DisplayName: "异星工厂", Runtime: "systemd", Unit: "factorio.service"}
	server, client, _ := newHTTPTestServer(t, cfg)
	loginHTTP(t, client, server.URL)
	response, err := client.Get(server.URL + "/control")
	if err != nil {
		t.Fatal(err)
	}
	page := string(readHTTPBody(t, response))
	for _, text := range []string{"异星工厂", "帕鲁世界", "泰拉瑞亚", "DOCKER / 受管服务", "SYSTEMD / 受管服务"} {
		if !strings.Contains(page, text) {
			t.Fatalf("missing %q", text)
		}
	}
	if strings.Count(page, `class="service-card"`) != 3 {
		t.Fatal("wrong card count")
	}
}

func TestHTTPIPWorksWithoutProxyConfiguration(t *testing.T) {
	handler, _ := httpHandler(t, httpConfig(t))
	request := httptest.NewRequest(http.MethodGet, "http://192.0.2.2:8231/login", nil)
	request.Header.Set("X-Forwarded-Proto", "https")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("IP login status=%d", recorder.Code)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatal("unexpected direct HTTP cookie properties")
	}
}

func TestHTTPSOriginSetsSecureCookiesAndChecksOrigin(t *testing.T) {
	cfg := httpConfig(t)
	cfg.Web.PublicOrigin = "https://games.example.com"
	handler, _ := httpHandler(t, cfg)
	get := httptest.NewRecorder()
	handler.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8231/login", nil))
	cookie := get.Result().Cookies()[0]
	if !cookie.Secure {
		t.Fatal("HTTPS origin did not set Secure")
	}
	form := url.Values{"csrf": {cookie.Value}, "password": {httpTestPassword}}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8231/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://games.example.com")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("proxied login status=%d", response.Code)
	}
	for _, result := range response.Result().Cookies() {
		if !result.Secure {
			t.Fatal("cookie creation/deletion missing Secure")
		}
	}
	request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8231/login", strings.NewReader(form.Encode()))
	request.Header.Set("Origin", "https://attacker.example")
	request.AddCookie(cookie)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status=%d", response.Code)
	}
}

func TestActionsRequireSessionCSRFAndKnownTarget(t *testing.T) {
	server, client, ctrl := newHTTPTestServer(t, httpConfig(t))
	csrf := loginHTTP(t, client, server.URL)
	for _, test := range []struct {
		path, token, origin string
		status              int
	}{
		{"/api/services/palworld/start", "", "", http.StatusForbidden},
		{"/api/services/unknown/start", csrf, "", http.StatusNotFound},
		{"/api/services/palworld/delete", csrf, "", http.StatusNotFound},
		{"/api/services/palworld/start", csrf, "https://attacker.example", http.StatusForbidden},
		{"/api/services/palworld/start", csrf, server.URL, http.StatusAccepted},
	} {
		request, err := http.NewRequest(http.MethodPost, server.URL+test.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("X-CSRF-Token", test.token)
		request.Header.Set("Origin", test.origin)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		readHTTPBody(t, response)
		if response.StatusCode != test.status {
			t.Fatalf("%s status=%d, want %d", test.path, response.StatusCode, test.status)
		}
	}
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	if len(ctrl.actions) != 1 {
		t.Fatalf("unexpected actions: %v", ctrl.actions)
	}
}

func TestLogoutInvalidatesSession(t *testing.T) {
	server, client, _ := newHTTPTestServer(t, httpConfig(t))
	csrf := loginHTTP(t, client, server.URL)
	request, err := http.NewRequest(http.MethodPost, server.URL+"/logout", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-CSRF-Token", csrf)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	readHTTPBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("logout=%d", response.StatusCode)
	}
	response, err = client.Get(server.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	readHTTPBody(t, response)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after logout=%d", response.StatusCode)
	}
}
