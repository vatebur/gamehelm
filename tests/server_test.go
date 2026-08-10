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
	"testing"

	"github.com/vatebur/gamehelm/internal/app"
)

var csrfFieldPattern = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func newHTTPTestServer(t *testing.T, cfg app.Config, runner app.CommandRunner) (*httptest.Server, *http.Client) {
	t.Helper()
	logger := log.New(io.Discard, "", 0)
	controller := newTestController(t, cfg, runner)
	serverApp, err := app.NewAppServer(cfg, controller, logger)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(serverApp.Handler())
	jar, err := cookiejar.New(nil)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	client := server.Client()
	client.Jar = jar
	return server, client
}

func getLoginCSRF(t *testing.T, client *http.Client, serverURL string) string {
	t.Helper()
	response, err := client.Get(serverURL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	match := csrfFieldPattern.FindSubmatch(body)
	if len(match) != 2 {
		t.Fatal("login page did not contain a CSRF field")
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
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return response, body
}

func sessionCookieValue(t *testing.T, client *http.Client, serverURL string) string {
	t.Helper()
	parsedURL, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range client.Jar.Cookies(parsedURL) {
		if cookie.Name == "gamehelm_session" {
			return cookie.Value
		}
	}
	t.Fatal("login did not create a session cookie")
	return ""
}

func TestLoginCSRFUnaffectedByFaviconRequest(t *testing.T) {
	cfg := testConfig(t)
	runner := &fakeRunner{statuses: map[string]app.UnitStatus{}}
	server, client := newHTTPTestServer(t, cfg, runner)
	defer server.Close()
	csrf := getLoginCSRF(t, client, server.URL)

	response, err := client.Get(server.URL + "/favicon.ico")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("favicon status = %d, want 204", response.StatusCode)
	}

	form := url.Values{"csrf": {csrf}, "password": {"wrong-password"}}
	response, err = client.Post(server.URL+"/login", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d, want 401", response.StatusCode)
	}
}

func TestLoginLocksAfterFiveFailures(t *testing.T) {
	cfg := testConfig(t)
	runner := &fakeRunner{statuses: map[string]app.UnitStatus{}}
	server, client := newHTTPTestServer(t, cfg, runner)
	defer server.Close()
	csrf := getLoginCSRF(t, client, server.URL)

	for attempt := 1; attempt <= 5; attempt++ {
		response, body := postLogin(t, client, server.URL, csrf, "wrong-password")
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", attempt, response.StatusCode)
		}
		match := csrfFieldPattern.FindSubmatch(body)
		if len(match) != 2 {
			t.Fatalf("attempt %d response did not contain a CSRF field", attempt)
		}
		csrf = string(match[1])
	}

	response, _ := postLogin(t, client, server.URL, csrf, cfg.Password)
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("locked login status = %d, want 429", response.StatusCode)
	}
}

func TestSuccessfulLoginsCreateDistinctSessions(t *testing.T) {
	cfg := testConfig(t)
	runner := &fakeRunner{statuses: map[string]app.UnitStatus{}}
	server, firstClient := newHTTPTestServer(t, cfg, runner)
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	secondClient := *server.Client()
	secondClient.Jar = jar

	for _, client := range []*http.Client{firstClient, &secondClient} {
		csrf := getLoginCSRF(t, client, server.URL)
		response, _ := postLogin(t, client, server.URL, csrf, cfg.Password)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("login status = %d, want 200 after redirect", response.StatusCode)
		}
	}

	firstToken := sessionCookieValue(t, firstClient, server.URL)
	secondToken := sessionCookieValue(t, &secondClient, server.URL)
	if firstToken == secondToken {
		t.Fatal("separate logins must create distinct session tokens")
	}
}

func TestControlPageRendersEveryConfiguredService(t *testing.T) {
	cfg := testConfig(t)
	cfg.Services["factorio"] = app.ServiceConfig{DisplayName: "异星工厂", Unit: "factorio.service"}
	runner := &fakeRunner{statuses: map[string]app.UnitStatus{}}
	server, client := newHTTPTestServer(t, cfg, runner)
	defer server.Close()
	csrf := getLoginCSRF(t, client, server.URL)

	form := url.Values{"csrf": {csrf}, "password": {cfg.Password}}
	response, err := client.Post(server.URL+"/login", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	for _, name := range []string{"异星工厂", "帕鲁世界", "泰拉瑞亚"} {
		if !strings.Contains(page, name) {
			t.Fatalf("control page missing service %q", name)
		}
	}
	if got := strings.Count(page, `class="service-card"`); got != 3 {
		t.Fatalf("service cards = %d, want 3", got)
	}
}
