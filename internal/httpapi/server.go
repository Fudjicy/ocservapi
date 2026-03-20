package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/example/ocservapi/internal/auth"
	"github.com/example/ocservapi/internal/store"
)

type Options struct {
	Store         *store.Store
	Version       string
	SessionTTL    time.Duration
	MasterKeyPath string
}

type Server struct {
	store         *store.Store
	version       string
	sessionTTL    time.Duration
	masterKeyPath string
}

type ctxKey string

const userCtxKey ctxKey = "user"

func NewServer(opts Options) http.Handler {
	s := &Server{store: opts.Store, version: opts.Version, sessionTTL: opts.SessionTTL, masterKeyPath: opts.MasterKeyPath}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/auth/login", s.handleLogin)
	mux.Handle("/auth/whoami", s.requireAuth(http.HandlerFunc(s.handleWhoAmI)))
	mux.Handle("/system/info", s.requireAuth(http.HandlerFunc(s.handleSystemInfo)))
	mux.Handle("/endpoints", s.requireAuth(http.HandlerFunc(s.handleEndpoints)))
	mux.Handle("/deployments", s.requireAuth(http.HandlerFunc(s.handleDeployments)))
	mux.Handle("/audit", s.requireAuth(http.HandlerFunc(s.handleAudit)))
	mux.Handle("/access", s.requireAuth(http.HandlerFunc(s.handleAccess)))
	mux.HandleFunc("/api/v1/auth/login", s.handleLogin)
	mux.Handle("/api/v1/auth/whoami", s.requireAuth(http.HandlerFunc(s.handleWhoAmI)))
	mux.Handle("/api/v1/system/info", s.requireAuth(http.HandlerFunc(s.handleSystemInfo)))
	mux.Handle("/api/v1/endpoints", s.requireAuth(http.HandlerFunc(s.handleEndpoints)))
	mux.Handle("/api/v1/deployments", s.requireAuth(http.HandlerFunc(s.handleDeployments)))
	mux.Handle("/api/v1/audit", s.requireAuth(http.HandlerFunc(s.handleAudit)))
	mux.Handle("/api/v1/access", s.requireAuth(http.HandlerFunc(s.handleAccess)))
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	status := map[string]any{"status": "ok", "db": "ok", "version": s.version}
	if err := s.store.Ping(r.Context()); err != nil {
		status["status"] = "degraded"
		status["db"] = err.Error()
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	token, user, err := s.store.Login(r.Context(), strings.TrimSpace(req.Username), req.Password, s.sessionTTL)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": user})
}

func (s *Server) handleWhoAmI(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, currentUser(r.Context()))
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	info, err := s.store.GetSystemInfo(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, err = os.ReadFile(s.masterKeyPath)
	info.MasterKeyLoaded = err == nil
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleEndpoints(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListEndpoints(r.Context(), currentUser(r.Context()))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleDeployments(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListDeployments(r.Context(), currentUser(r.Context()))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListAuditEvents(r.Context(), currentUser(r.Context()))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleAccess(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListAccess(r.Context(), currentUser(r.Context()))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if token == "" {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		user, err := s.store.Authenticate(r.Context(), token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userCtxKey, user)))
	})
}

func currentUser(ctx context.Context) auth.User {
	user, _ := ctx.Value(userCtxKey).(auth.User)
	return user
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
