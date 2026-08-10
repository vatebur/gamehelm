package main

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
)

var csrfFieldPattern = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func TestLoginCSRFUnaffectedByFaviconRequest(t *testing.T) {
	cfg := testConfig(t)
	cfg.Password = defaultPassword
	runner := &fakeRunner{statuses: map[string]unitStatus{
		"palworld.service": {Load: "loaded", Active: "inactive", Sub: "dead"},
		"terraria.service": {Load: "loaded", Active: "inactive", Sub: "dead"},
	}}
	logger := log.New(io.Discard, "", 0)
	ctrl := newTestController(t, cfg, runner)
	app, err := newAppServer(cfg, ctrl, logger)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.handler())
	defer server.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Jar = jar

	response, err := client.Get(server.URL + "/login")
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
	csrf := string(match[1])

	response, err = client.Get(server.URL + "/favicon.ico")
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
