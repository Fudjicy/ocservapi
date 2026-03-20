package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/example/ocservapi/internal/audit"
	"github.com/example/ocservapi/internal/auth"
	"github.com/example/ocservapi/internal/config"
	"github.com/example/ocservapi/internal/system"
)

type Store struct {
	mu      sync.Mutex
	cfg     config.Config
	version string
	path    string
	state   state
}

type state struct {
	SchemaVersion uint            `json:"schema_version"`
	System        systemRecord    `json:"system"`
	Users         []userRecord    `json:"users"`
	Endpoints     []Endpoint      `json:"endpoints"`
	Access        []AccessRecord  `json:"access"`
	Certificates  []Certificate   `json:"certificates"`
	Deployments   []Deployment    `json:"deployments"`
	Audit         []audit.Event   `json:"audit"`
	Sessions      []sessionRecord `json:"sessions"`
	NextID        int64           `json:"next_id"`
}

type systemRecord struct {
	InstallationID string    `json:"installation_id"`
	DisplayName    string    `json:"display_name"`
	InitializedAt  time.Time `json:"initialized_at"`
}

type userRecord struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type AccessRecord struct {
	UserID                int64 `json:"user_id"`
	EndpointID            int64 `json:"endpoint_id"`
	CanView               bool  `json:"can_view"`
	CanInspect            bool  `json:"can_inspect"`
	CanDeploy             bool  `json:"can_deploy"`
	CanManageUsers        bool  `json:"can_manage_users"`
	CanManageCertificates bool  `json:"can_manage_certificates"`
	CanManageEndpoint     bool  `json:"can_manage_endpoint"`
}

type Certificate struct {
	ID         int64     `json:"id"`
	EndpointID int64     `json:"endpoint_id"`
	CommonName string    `json:"common_name"`
	CertType   string    `json:"cert_type"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

type sessionRecord struct {
	TokenHash string    `json:"token_hash"`
	UserID    int64     `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

func Open(_ context.Context, cfg config.Config, version string) (*Store, error) {
	path := filepath.Join(cfg.Storage.DataDir, "state.json")
	if err := os.MkdirAll(cfg.Storage.DataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	st := &Store{cfg: cfg, version: version, path: path, state: state{NextID: 1}}
	if err := st.load(); err != nil {
		return nil, err
	}
	return st, nil
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s.saveLocked()
		}
		return fmt.Errorf("read state: %w", err)
	}
	if err := json.Unmarshal(data, &s.state); err != nil {
		return fmt.Errorf("parse state: %w", err)
	}
	if s.state.NextID == 0 {
		s.state.NextID = 1
	}
	return nil
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	return nil
}

func (s *Store) Close() error               { return nil }
func (s *Store) Ping(context.Context) error { return nil }

func (s *Store) RunMigrations() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.SchemaVersion < 1 {
		s.state.SchemaVersion = 1
	}
	return s.saveLocked()
}

func (s *Store) SchemaVersion() (uint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.SchemaVersion, nil
}

func (s *Store) Bootstrap(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if s.state.System.InstallationID == "" {
		s.state.System = systemRecord{
			InstallationID: randomID(),
			DisplayName:    s.cfg.Bootstrap.DisplayName,
			InitializedAt:  now,
		}
	}
	owner := s.findUserByUsernameLocked(s.cfg.Bootstrap.OwnerUsername)
	if owner == nil {
		id := s.nextIDLocked()
		s.state.Users = append(s.state.Users, userRecord{ID: id, Username: s.cfg.Bootstrap.OwnerUsername, Role: "owner", CreatedAt: now})
		owner = &s.state.Users[len(s.state.Users)-1]
		s.state.Audit = append(s.state.Audit, audit.Event{ID: s.nextIDLocked(), Actor: s.cfg.Bootstrap.OwnerUsername, Action: "bootstrap_owner", Result: "success", Message: "Bootstrap owner account created", CreatedAt: now})
	}
	if len(s.state.Endpoints) == 0 {
		ep := Endpoint{ID: s.nextIDLocked(), Name: "node01", Address: "10.0.0.11", Description: "Bootstrap demo endpoint", CreatedAt: now}
		s.state.Endpoints = append(s.state.Endpoints, ep)
		s.state.Certificates = append(s.state.Certificates, Certificate{ID: s.nextIDLocked(), EndpointID: ep.ID, CommonName: "node01.example.internal", CertType: "server", Status: "issued", CreatedAt: now})
		s.state.Deployments = append(s.state.Deployments, Deployment{ID: s.nextIDLocked(), Endpoint: &ep.Name, Operation: "prepare", Status: "success", Summary: "Initial bootstrap preparation record", StartedAt: now, FinishedAt: ptrTime(now), TriggeredBy: owner.Username})
		s.state.Audit = append(s.state.Audit, audit.Event{ID: s.nextIDLocked(), Actor: owner.Username, Action: "create_endpoint", Result: "success", Message: "Bootstrap endpoint node01 created", Endpoint: &ep.Name, CreatedAt: now})
	}
	for _, ep := range s.state.Endpoints {
		if !s.hasAccessLocked(owner.ID, ep.ID) {
			s.state.Access = append(s.state.Access, AccessRecord{UserID: owner.ID, EndpointID: ep.ID, CanView: true, CanInspect: true, CanDeploy: true, CanManageUsers: true, CanManageCertificates: true, CanManageEndpoint: true})
		}
	}
	return s.saveLocked()
}

func (s *Store) Login(_ context.Context, username string, ttl time.Duration) (string, auth.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user := s.findUserByUsernameLocked(username)
	if user == nil {
		return "", auth.User{}, fmt.Errorf("user %q not found", username)
	}
	token := randomID()
	s.state.Sessions = append(s.state.Sessions, sessionRecord{TokenHash: hashToken(token), UserID: user.ID, ExpiresAt: time.Now().UTC().Add(ttl)})
	s.state.Audit = append(s.state.Audit, audit.Event{ID: s.nextIDLocked(), Actor: user.Username, Action: "login", Result: "success", Message: "User logged in via API", CreatedAt: time.Now().UTC()})
	if err := s.saveLocked(); err != nil {
		return "", auth.User{}, err
	}
	return token, auth.User{ID: user.ID, Username: user.Username, Role: user.Role}, nil
}

func (s *Store) Authenticate(_ context.Context, token string) (auth.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hash := hashToken(token)
	for _, session := range s.state.Sessions {
		if session.TokenHash == hash && session.ExpiresAt.After(time.Now().UTC()) {
			for _, user := range s.state.Users {
				if user.ID == session.UserID {
					return auth.User{ID: user.ID, Username: user.Username, Role: user.Role}, nil
				}
			}
		}
	}
	return auth.User{}, fmt.Errorf("invalid token")
}

func (s *Store) GetSystemInfo(_ context.Context) (system.Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	admins := 0
	for _, user := range s.state.Users {
		if user.Role == "admin" {
			admins++
		}
	}
	return system.Info{
		InstallationID:   s.state.System.InstallationID,
		DisplayName:      s.state.System.DisplayName,
		InitializedAt:    s.state.System.InitializedAt,
		Version:          s.version,
		SchemaVersion:    s.state.SchemaVersion,
		SafeDSN:          SafeDSN(s.cfg.Postgres.DSN),
		DataDir:          s.cfg.Storage.DataDir,
		MasterKeyLoaded:  true,
		APIAddress:       s.cfg.Server.Listen,
		DBStatus:         "ok",
		EndpointCount:    len(s.state.Endpoints),
		AdminCount:       admins,
		AuditCount:       len(s.state.Audit),
		DeploymentCount:  len(s.state.Deployments),
		CertificateCount: len(s.state.Certificates),
	}, nil
}

func (s *Store) ListEndpoints(_ context.Context, user auth.User) ([]Endpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var items []Endpoint
	for _, ep := range s.state.Endpoints {
		if user.IsOwner() || s.canViewLocked(user.ID, ep.ID) {
			items = append(items, ep)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

func (s *Store) ListDeployments(_ context.Context, user auth.User) ([]Deployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var items []Deployment
	for _, d := range s.state.Deployments {
		if d.Endpoint == nil || user.IsOwner() || s.canViewEndpointNameLocked(user.ID, *d.Endpoint) {
			items = append(items, d)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].StartedAt.After(items[j].StartedAt) })
	return items, nil
}

func (s *Store) ListAuditEvents(_ context.Context, user auth.User) ([]audit.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var items []audit.Event
	for _, event := range s.state.Audit {
		if event.Endpoint == nil || user.IsOwner() || s.canViewEndpointNameLocked(user.ID, *event.Endpoint) {
			items = append(items, event)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, nil
}

func (s *Store) ListAccess(_ context.Context, user auth.User) ([]AccessSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var items []AccessSummary
	for _, access := range s.state.Access {
		if !user.IsOwner() && access.UserID != user.ID {
			continue
		}
		ep := s.findEndpointByIDLocked(access.EndpointID)
		if ep == nil {
			continue
		}
		items = append(items, AccessSummary{EndpointName: ep.Name, CanView: access.CanView, CanInspect: access.CanInspect, CanDeploy: access.CanDeploy, CanManageUsers: access.CanManageUsers, CanManageCertificates: access.CanManageCertificates, CanManageEndpoint: access.CanManageEndpoint})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].EndpointName < items[j].EndpointName })
	return items, nil
}

func (s *Store) InsertAudit(_ context.Context, actorID int64, endpointID *int64, action, result, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	actor := "system"
	if user := s.findUserByIDLocked(actorID); user != nil {
		actor = user.Username
	}
	var endpointName *string
	if endpointID != nil {
		if ep := s.findEndpointByIDLocked(*endpointID); ep != nil {
			endpointName = &ep.Name
		}
	}
	s.state.Audit = append(s.state.Audit, audit.Event{ID: s.nextIDLocked(), Actor: actor, Action: action, Result: result, Message: message, Endpoint: endpointName, CreatedAt: time.Now().UTC()})
	return s.saveLocked()
}

func SafeDSN(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "redacted"
	}
	if parsed.User != nil {
		parsed.User = url.User(parsed.User.Username())
	}
	if parsed.RawQuery != "" {
		query, _ := url.ParseQuery(parsed.RawQuery)
		if query.Has("password") {
			query.Set("password", "redacted")
			parsed.RawQuery = query.Encode()
		}
	}
	return parsed.String()
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

func randomID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func (s *Store) nextIDLocked() int64 {
	id := s.state.NextID
	s.state.NextID++
	return id
}

func (s *Store) findUserByUsernameLocked(username string) *userRecord {
	for i := range s.state.Users {
		if s.state.Users[i].Username == username {
			return &s.state.Users[i]
		}
	}
	return nil
}

func (s *Store) findUserByIDLocked(id int64) *userRecord {
	for i := range s.state.Users {
		if s.state.Users[i].ID == id {
			return &s.state.Users[i]
		}
	}
	return nil
}

func (s *Store) hasAccessLocked(userID, endpointID int64) bool {
	for _, access := range s.state.Access {
		if access.UserID == userID && access.EndpointID == endpointID {
			return true
		}
	}
	return false
}

func (s *Store) canViewLocked(userID, endpointID int64) bool {
	for _, access := range s.state.Access {
		if access.UserID == userID && access.EndpointID == endpointID && access.CanView {
			return true
		}
	}
	return false
}

func (s *Store) canViewEndpointNameLocked(userID int64, endpointName string) bool {
	for _, ep := range s.state.Endpoints {
		if ep.Name == endpointName {
			return s.canViewLocked(userID, ep.ID)
		}
	}
	return false
}

func (s *Store) findEndpointByIDLocked(id int64) *Endpoint {
	for i := range s.state.Endpoints {
		if s.state.Endpoints[i].ID == id {
			return &s.state.Endpoints[i]
		}
	}
	return nil
}

func ptrTime(v time.Time) *time.Time { return &v }
