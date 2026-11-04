package web

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
	"net/netip"
	"net/url"
	"strings"

	"github.com/vatebur/gamehelm/internal/auth"
	"github.com/vatebur/gamehelm/internal/config"
	"github.com/vatebur/gamehelm/internal/control"
)

const (
	sessionCookieName   = "gamehelm_session"
	loginCSRFCookieName = "gamehelm_login_csrf"
)

//go:embed *.html assets/*
var webFiles embed.FS

type Controller interface {
	Views() []control.ServiceView
	Action(id, action, source string) (bool, error)
	ServiceIDs() []string
}

type Server struct {
	cfg       config.Config
	ctrl      Controller
	logger    *log.Logger
	sessions  *auth.Sessions
	limiter   *auth.LoginLimiter
	templates *template.Template
	publicURL *url.URL
	proxies   []netip.Prefix
	hashing   chan struct{}
}

type loginPageData struct {
	CSRF   string
	Error  string
	Secure bool
}

type controlPageData struct {
	CSRF     string
	Services []controlServiceData
}

type controlServiceData struct {
	ID      string
	Name    string
	Index   string
	Runtime string
}

func New(cfg config.Config, ctrl Controller, logger *log.Logger) (*Server, error) {
	if err := auth.ValidateHash(cfg.Auth.PasswordHash); err != nil {
		return nil, fmt.Errorf("认证哈希配置无效")
	}
	tmpl, err := template.ParseFS(webFiles, "*.html")
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:       cfg,
		ctrl:      ctrl,
		logger:    logger,
		sessions:  auth.NewSessions(),
		limiter:   auth.NewLoginLimiter(),
		templates: tmpl,
		hashing:   make(chan struct{}, 2),
	}
	if cfg.Web.PublicOrigin != "" {
		s.publicURL, err = url.Parse(cfg.Web.PublicOrigin)
		if err != nil || (s.publicURL.Scheme != "http" && s.publicURL.Scheme != "https") || s.publicURL.Host == "" || s.publicURL.User != nil || s.publicURL.RawQuery != "" || s.publicURL.Fragment != "" || (s.publicURL.Path != "" && s.publicURL.Path != "/") {
			return nil, fmt.Errorf("public_origin 必须是 HTTP 或 HTTPS 来源地址")
		}
	}
	for _, value := range cfg.Web.TrustedProxies {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, fmt.Errorf("trusted_proxies 必须是有效的 CIDR")
		}
		s.proxies = append(s.proxies, prefix)
	}
	return s, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	assets, _ := fs.Sub(webFiles, "assets")
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
	return s.securityHeaders(s.recoverPanic(s.checkOrigin(mux)))
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; form-action 'self'; base-uri 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Print("请求处理异常")
				http.Error(w, "服务器内部错误", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentSession(r); ok {
		http.Redirect(w, r, "/control", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentSession(r); ok {
		http.Redirect(w, r, "/control", http.StatusSeeOther)
		return
	}
	s.renderLogin(w, r, "", http.StatusOK)
}

func (s *Server) renderLogin(w http.ResponseWriter, r *http.Request, message string, status int) {
	token, err := auth.RandomToken()
	if err != nil {
		http.Error(w, "无法创建登录请求", http.StatusInternalServerError)
		return
	}
	s.setCookie(w, r, loginCSRFCookieName, token, "/login", 0)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := s.templates.ExecuteTemplate(w, "login.html", loginPageData{CSRF: token, Error: message, Secure: s.secure(r)}); err != nil {
		s.logger.Printf("登录模板渲染失败 error=%q", err)
	}
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		s.renderLogin(w, r, "登录请求无效", http.StatusBadRequest)
		return
	}
	cookie, err := r.Cookie(loginCSRFCookieName)
	if err != nil || !constantTimeEqual(cookie.Value, r.PostForm.Get("csrf")) {
		s.renderLogin(w, r, "登录失败，请刷新页面后重试", http.StatusBadRequest)
		return
	}
	password := r.PostForm.Get("password")
	if len(password) == 0 || len(password) > 1024 {
		s.renderLogin(w, r, "登录失败，请检查密码", http.StatusUnauthorized)
		return
	}
	select {
	case s.hashing <- struct{}{}:
		defer func() { <-s.hashing }()
	default:
		s.renderLogin(w, r, "登录繁忙，请稍后再试", http.StatusTooManyRequests)
		return
	}
	if !s.limiter.Begin(ip) {
		s.logger.Printf("登录已限速 ip=%s", ip)
		s.renderLogin(w, r, "登录失败，请稍后再试", http.StatusTooManyRequests)
		return
	}
	if !auth.VerifyPassword(s.cfg.Auth.PasswordHash, password) {
		s.logger.Printf("登录失败 ip=%s", ip)
		s.renderLogin(w, r, "登录失败，请检查密码", http.StatusUnauthorized)
		return
	}
	token, _, err := s.sessions.Create()
	if err != nil {
		http.Error(w, "无法创建登录会话，请稍后再试", http.StatusServiceUnavailable)
		return
	}
	s.limiter.Success(ip)
	s.setCookie(w, r, sessionCookieName, token, "/", 0)
	s.setCookie(w, r, loginCSRFCookieName, "", "/login", -1)
	s.logger.Printf("登录成功 ip=%s", ip)
	http.Redirect(w, r, "/control", http.StatusSeeOther)
}

func (s *Server) handleControl(w http.ResponseWriter, r *http.Request) {
	sess, _ := s.currentSession(r)
	services := make([]controlServiceData, 0, len(s.cfg.Services))
	for index, id := range s.ctrl.ServiceIDs() {
		services = append(services, controlServiceData{
			ID:      id,
			Name:    s.cfg.Services[id].DisplayName,
			Index:   fmt.Sprintf("%02d", index+1),
			Runtime: strings.ToUpper(s.cfg.Services[id].Runtime),
		})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.templates.ExecuteTemplate(w, "control.html", controlPageData{
		CSRF: sess.CSRF,

		Services: services,
	}); err != nil {
		s.logger.Printf("控制台模板渲染失败 error=%q", err)
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	token, _, ok := s.sessionFromRequest(r)
	if !ok {
		writeJSONError(w, "登录状态已失效", http.StatusUnauthorized)
		return
	}
	s.sessions.Delete(token)
	s.setCookie(w, r, sessionCookieName, "", "/", -1)
	s.logger.Printf("退出登录 ip=%s", s.clientIP(r))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"services": s.ctrl.Views(),
	})
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	action := r.PathValue("action")
	if _, exists := s.cfg.Services[id]; !exists {
		writeJSONError(w, "未知服务", http.StatusNotFound)
		return
	}
	if action != "start" && action != "stop" && action != "extend" {
		writeJSONError(w, "未知操作", http.StatusNotFound)
		return
	}
	accepted, err := s.ctrl.Action(id, action, s.clientIP(r))
	if err != nil {
		writeJSONError(w, "操作暂时无法执行，请查看当前状态后重试", http.StatusConflict)
		return
	}
	status := http.StatusOK
	if accepted {
		status = http.StatusAccepted
	}
	writeJSON(w, status, map[string]any{"ok": true, "accepted": accepted, "services": s.ctrl.Views()})
}

func (s *Server) requireControl(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.currentSession(r); !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (s *Server) requireAPI(next http.HandlerFunc) http.HandlerFunc {
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

func (s *Server) currentSession(r *http.Request) (auth.Session, bool) {
	_, sess, ok := s.sessionFromRequest(r)
	return sess, ok
}

func (s *Server) sessionFromRequest(r *http.Request) (string, auth.Session, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return "", auth.Session{}, false
	}
	sess, ok := s.sessions.Get(cookie.Value)
	return cookie.Value, sess, ok
}

func constantTimeEqual(a, b string) bool {
	if a == "" || len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func (s *Server) secure(r *http.Request) bool {
	return r.TLS != nil || (s.publicURL != nil && s.publicURL.Scheme == "https")
}

func (s *Server) setCookie(w http.ResponseWriter, r *http.Request, name, value, path string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: path, MaxAge: maxAge,
		Secure: s.secure(r), HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) checkOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			writeJSONError(w, "请求来源无效", http.StatusForbidden)
			return
		}
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		host := r.Host
		if s.publicURL != nil {
			scheme, host = s.publicURL.Scheme, s.publicURL.Host
		}
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = r.Header.Get("Referer")
		}
		if origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.User != nil || u.Scheme != scheme || !strings.EqualFold(u.Host, host) {
				writeJSONError(w, "请求来源无效", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) trusted(address netip.Addr) bool {
	for _, prefix := range s.proxies {
		if prefix.Contains(address.Unmap()) {
			return true
		}
	}
	return false
}

func (s *Server) clientIP(r *http.Request) string {
	remote := remoteIP(r)
	address, err := netip.ParseAddr(remote)
	if err != nil || !s.trusted(address) {
		return remote
	}
	forwarded := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	if len(forwarded) > 20 {
		return remote
	}
	for i := len(forwarded) - 1; i >= 0; i-- {
		candidate, err := netip.ParseAddr(strings.TrimSpace(forwarded[i]))
		if err != nil {
			return remote
		}
		address = candidate.Unmap()
		if !s.trusted(address) {
			return address.String()
		}
	}
	return address.String()
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
