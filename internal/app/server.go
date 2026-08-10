package app

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
)

const (
	sessionCookieName   = "gamehelm_session"
	loginCSRFCookieName = "gamehelm_login_csrf"
)

//go:embed web/*.html web/assets/*
var webFiles embed.FS

type appServer struct {
	cfg       Config
	ctrl      *controller
	logger    *log.Logger
	sessions  *sessionStore
	limiter   *loginLimiter
	templates *template.Template
}

type loginPageData struct {
	CSRF  string
	Error string
}

type controlPageData struct {
	CSRF            string
	DefaultPassword bool
	Services        []controlServiceData
}

type controlServiceData struct {
	ID    string
	Name  string
	Index string
}

func newAppServer(cfg Config, ctrl *controller, logger *log.Logger) (*appServer, error) {
	tmpl, err := template.ParseFS(webFiles, "web/*.html")
	if err != nil {
		return nil, err
	}
	return &appServer{
		cfg:       cfg,
		ctrl:      ctrl,
		logger:    logger,
		sessions:  newSessionStore(),
		limiter:   newLoginLimiter(),
		templates: tmpl,
	}, nil
}

func (s *appServer) handler() http.Handler {
	mux := http.NewServeMux()
	assets, _ := fs.Sub(webFiles, "web/assets")
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assets))))
	mux.HandleFunc("GET /{$}", s.handleRoot)
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /login", s.handleLoginGet)
	mux.HandleFunc("POST /login", s.handleLoginPost)
	mux.HandleFunc("GET /control", s.requireControl(s.handleControl))
	mux.HandleFunc("POST /logout", s.requireAPI(s.handleLogout))
	mux.HandleFunc("GET /api/status", s.requireAPI(s.handleStatus))
	mux.HandleFunc("POST /api/services/{id}/{action}", s.requireAPI(s.handleAction))
	return s.securityHeaders(s.recoverPanic(mux))
}

func (s *appServer) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; form-action 'self'; base-uri 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *appServer) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Printf("请求处理异常 path=%s error=%q", r.URL.Path, recovered)
				http.Error(w, "服务器内部错误", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *appServer) handleRoot(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentSession(r); ok {
		http.Redirect(w, r, "/control", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *appServer) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentSession(r); ok {
		http.Redirect(w, r, "/control", http.StatusSeeOther)
		return
	}
	s.renderLogin(w, "", http.StatusOK)
}

func (s *appServer) renderLogin(w http.ResponseWriter, message string, status int) {
	token, err := randomToken()
	if err != nil {
		http.Error(w, "无法创建登录请求", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     loginCSRFCookieName,
		Value:    token,
		Path:     "/login",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := s.templates.ExecuteTemplate(w, "login.html", loginPageData{CSRF: token, Error: message}); err != nil {
		s.logger.Printf("登录模板渲染失败 error=%q", err)
	}
}

func (s *appServer) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.limiter.allowed(ip) {
		s.logger.Printf("登录已限速 ip=%s", ip)
		s.renderLogin(w, "登录失败，请稍后再试", http.StatusTooManyRequests)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderLogin(w, "登录失败，请重试", http.StatusBadRequest)
		return
	}
	cookie, err := r.Cookie(loginCSRFCookieName)
	formCSRF := r.FormValue("csrf")
	if err != nil || !constantTimeEqual(cookie.Value, formCSRF) {
		s.logger.Printf("登录请求校验失败 ip=%s", ip)
		s.renderLogin(w, "登录失败，请刷新页面后重试", http.StatusBadRequest)
		return
	}
	if !constantTimeEqual(s.cfg.Password, r.FormValue("password")) {
		s.limiter.fail(ip)
		s.logger.Printf("登录失败 ip=%s", ip)
		s.renderLogin(w, "登录失败，请检查密码", http.StatusUnauthorized)
		return
	}

	token, _, err := s.sessions.create()
	if err != nil {
		http.Error(w, "无法创建登录会话", http.StatusInternalServerError)
		return
	}
	s.limiter.success(ip)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(w, &http.Cookie{Name: loginCSRFCookieName, Path: "/login", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	s.logger.Printf("登录成功 ip=%s", ip)
	http.Redirect(w, r, "/control", http.StatusSeeOther)
}

func (s *appServer) handleControl(w http.ResponseWriter, r *http.Request) {
	sess, _ := s.currentSession(r)
	services := make([]controlServiceData, 0, len(s.cfg.Services))
	for index, id := range s.ctrl.ids {
		services = append(services, controlServiceData{
			ID:    id,
			Name:  s.cfg.Services[id].DisplayName,
			Index: fmt.Sprintf("%02d", index+1),
		})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.templates.ExecuteTemplate(w, "control.html", controlPageData{
		CSRF:            sess.CSRF,
		DefaultPassword: s.cfg.Password == defaultPassword,
		Services:        services,
	}); err != nil {
		s.logger.Printf("控制台模板渲染失败 error=%q", err)
	}
}

func (s *appServer) handleLogout(w http.ResponseWriter, r *http.Request) {
	token, _, ok := s.sessionFromRequest(r)
	if !ok {
		writeJSONError(w, "登录状态已失效", http.StatusUnauthorized)
		return
	}
	s.sessions.delete(token)
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	s.logger.Printf("退出登录 ip=%s", clientIP(r))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *appServer) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"services":         s.ctrl.views(),
		"default_password": s.cfg.Password == defaultPassword,
	})
}

func (s *appServer) handleAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	action := r.PathValue("action")
	if !s.ctrl.validID(id) {
		writeJSONError(w, "未知服务", http.StatusNotFound)
		return
	}
	ip := clientIP(r)
	var err error
	switch action {
	case "start":
		err = s.ctrl.start(r.Context(), id, ip)
	case "stop":
		err = s.ctrl.stop(r.Context(), id, ip)
	case "extend":
		err = s.ctrl.extend(r.Context(), id, ip)
	default:
		writeJSONError(w, "未知操作", http.StatusNotFound)
		return
	}
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "services": s.ctrl.views()})
}

func (s *appServer) requireControl(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.currentSession(r); !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (s *appServer) requireAPI(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.currentSession(r)
		if !ok {
			writeJSONError(w, "登录状态已失效", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost && !constantTimeEqual(sess.CSRF, r.Header.Get("X-CSRF-Token")) {
			writeJSONError(w, "请求校验失败，请刷新页面", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (s *appServer) currentSession(r *http.Request) (session, bool) {
	_, sess, ok := s.sessionFromRequest(r)
	return sess, ok
}

func (s *appServer) sessionFromRequest(r *http.Request) (string, session, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return "", session{}, false
	}
	sess, ok := s.sessions.get(cookie.Value)
	return cookie.Value, sess, ok
}

func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeJSONError(w http.ResponseWriter, message string, status int) {
	writeJSON(w, status, map[string]string{"error": message})
}
