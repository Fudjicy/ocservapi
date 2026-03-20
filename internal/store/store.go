package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/example/ocservapi/internal/audit"
	"github.com/example/ocservapi/internal/auth"
	"github.com/example/ocservapi/internal/config"
	"github.com/example/ocservapi/internal/migrations"
	"github.com/example/ocservapi/internal/pgwire"
	"github.com/example/ocservapi/internal/system"
)

type PostgresStore struct {
	cfg     config.Config
	version string
	client  *pgwire.Client
}

type Store = PostgresStore

type schemaVersionRow struct {
	Version uint `json:"version"`
}

type countRow struct {
	Count int `json:"count"`
}

type loginRow struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	Role         string `json:"role"`
	PasswordSalt string `json:"password_salt"`
	PasswordHash string `json:"password_hash"`
}

type systemRow struct {
	InstallationID string    `json:"installation_id"`
	DisplayName    string    `json:"display_name"`
	InitializedAt  time.Time `json:"initialized_at"`
}

type whoamiRow struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

func Open(_ context.Context, cfg config.Config, version string) (*Store, error) {
	client, err := pgwire.Connect(cfg.Postgres.DSN)
	if err != nil {
		return nil, err
	}
	return &PostgresStore{cfg: cfg, version: version, client: client}, nil
}

func (s *PostgresStore) Close() error {
	return s.client.Close()
}

func (s *PostgresStore) Ping(context.Context) error {
	return s.client.Ping()
}

func (s *PostgresStore) RunMigrations() error {
	if err := s.client.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version BIGINT PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		version, err := parseMigrationVersion(name)
		if err != nil {
			return err
		}
		if applied, err := s.isMigrationApplied(version); err != nil {
			return err
		} else if applied {
			continue
		}
		body, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if err := s.client.Exec(string(body)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if err := s.client.Exec(fmt.Sprintf(
			"INSERT INTO schema_migrations (version, name) VALUES (%d, %s)",
			version,
			quote(name),
		)); err != nil {
			return fmt.Errorf("record migration %s: %w", name, err)
		}
	}
	return nil
}

func (s *PostgresStore) SchemaVersion() (uint, error) {
	data, err := s.client.QueryJSON(`
		SELECT COALESCE(row_to_json(t), '{"version":0}'::json)
		FROM (
			SELECT COALESCE(MAX(version), 0)::bigint AS version FROM schema_migrations
		) t
	`)
	if err != nil {
		return 0, err
	}
	var row schemaVersionRow
	if err := json.Unmarshal(data, &row); err != nil {
		return 0, fmt.Errorf("decode schema version: %w", err)
	}
	return row.Version, nil
}

func (s *PostgresStore) Bootstrap(_ context.Context) error {
	systemExists, err := s.rowCount(`SELECT COUNT(*) AS count FROM system_instance`)
	if err != nil {
		return err
	}
	if systemExists == 0 {
		if err := s.client.Exec(fmt.Sprintf(`
			INSERT INTO system_instance (installation_id, display_name, init_version)
			VALUES (%s, %s, %s)
		`, quote(randomID()), quote(s.cfg.Bootstrap.DisplayName), quote(s.version))); err != nil {
			return fmt.Errorf("insert system_instance: %w", err)
		}
	}

	ownerExists, err := s.rowCount(fmt.Sprintf("SELECT COUNT(*) AS count FROM users WHERE username = %s", quote(s.cfg.Bootstrap.OwnerUsername)))
	if err != nil {
		return err
	}
	if ownerExists == 0 {
		salt, hash := hashPassword(s.cfg.Bootstrap.OwnerPassword)
		if err := s.client.Exec(fmt.Sprintf(`
			INSERT INTO users (username, role, password_salt, password_hash)
			VALUES (%s, 'owner', %s, %s)
		`, quote(s.cfg.Bootstrap.OwnerUsername), quote(salt), quote(hash))); err != nil {
			return fmt.Errorf("insert owner: %w", err)
		}
	}

	endpointExists, err := s.rowCount(`SELECT COUNT(*) AS count FROM endpoints`)
	if err != nil {
		return err
	}
	if endpointExists == 0 {
		if err := s.client.Exec(`
			INSERT INTO endpoints (name, address, description)
			VALUES ('node01', '10.0.0.11', 'Bootstrap demo endpoint')
		`); err != nil {
			return fmt.Errorf("insert bootstrap endpoint: %w", err)
		}
	}

	if err := s.client.Exec(`
		INSERT INTO endpoint_admin_access (
			user_id, endpoint_id, can_view, can_inspect, can_deploy,
			can_manage_users, can_manage_certificates, can_manage_endpoint
		)
		SELECT u.id, e.id, TRUE, TRUE, TRUE, TRUE, TRUE, TRUE
		FROM users u
		JOIN endpoints e ON TRUE
		WHERE u.role = 'owner'
		  AND NOT EXISTS (
			SELECT 1 FROM endpoint_admin_access a WHERE a.user_id = u.id AND a.endpoint_id = e.id
		)
	`); err != nil {
		return fmt.Errorf("grant owner endpoint access: %w", err)
	}

	if err := s.client.Exec(`
		INSERT INTO certificates (endpoint_id, common_name, cert_type, status)
		SELECT e.id, 'node01.example.internal', 'server', 'issued'
		FROM endpoints e
		WHERE e.name = 'node01'
		  AND NOT EXISTS (SELECT 1 FROM certificates WHERE common_name = 'node01.example.internal')
	`); err != nil {
		return fmt.Errorf("seed bootstrap certificate: %w", err)
	}

	if err := s.client.Exec(`
		INSERT INTO deployments (endpoint_id, created_by_user_id, operation_type, status, summary, finished_at)
		SELECT e.id, u.id, 'prepare', 'success', 'Initial bootstrap preparation record', NOW()
		FROM endpoints e
		JOIN users u ON u.role = 'owner'
		WHERE e.name = 'node01'
		  AND NOT EXISTS (SELECT 1 FROM deployments WHERE summary = 'Initial bootstrap preparation record')
	`); err != nil {
		return fmt.Errorf("seed bootstrap deployment: %w", err)
	}

	if err := s.client.Exec(`
		INSERT INTO audit_events (actor_user_id, endpoint_id, action, result, message)
		SELECT u.id, e.id, 'bootstrap', 'success', 'Bootstrap data ensured'
		FROM users u
		JOIN endpoints e ON e.name = 'node01'
		WHERE u.role = 'owner'
		  AND NOT EXISTS (SELECT 1 FROM audit_events WHERE action = 'bootstrap' AND message = 'Bootstrap data ensured')
	`); err != nil {
		return fmt.Errorf("seed bootstrap audit: %w", err)
	}

	return nil
}

func (s *PostgresStore) Login(_ context.Context, username, password string, ttl time.Duration) (string, auth.User, error) {
	data, err := s.client.QueryJSON(fmt.Sprintf(`
		SELECT COALESCE(row_to_json(t), 'null'::json)
		FROM (
			SELECT id, username, role, password_salt, password_hash
			FROM users
			WHERE username = %s
			LIMIT 1
		) t
	`, quote(username)))
	if err != nil {
		return "", auth.User{}, err
	}
	if string(data) == "null" {
		return "", auth.User{}, fmt.Errorf("invalid username or password")
	}
	var row loginRow
	if err := json.Unmarshal(data, &row); err != nil {
		return "", auth.User{}, fmt.Errorf("decode login row: %w", err)
	}
	if !verifyPassword(password, row.PasswordSalt, row.PasswordHash) {
		return "", auth.User{}, fmt.Errorf("invalid username or password")
	}
	token := randomID()
	tokenHash := hashToken(token)
	if err := s.client.Exec(fmt.Sprintf(`
		INSERT INTO sessions (token_hash, user_id, expires_at)
		VALUES (%s, %d, NOW() + INTERVAL '%d seconds')
	`, quote(tokenHash), row.ID, int(ttl.Seconds()))); err != nil {
		return "", auth.User{}, fmt.Errorf("create session: %w", err)
	}
	_ = s.InsertAudit(context.Background(), row.ID, nil, "login", "success", "User logged in via API")
	return token, auth.User{ID: row.ID, Username: row.Username, Role: row.Role}, nil
}

func (s *PostgresStore) Authenticate(_ context.Context, token string) (auth.User, error) {
	data, err := s.client.QueryJSON(fmt.Sprintf(`
		SELECT COALESCE(row_to_json(t), 'null'::json)
		FROM (
			SELECT u.id, u.username, u.role
			FROM sessions s
			JOIN users u ON u.id = s.user_id
			WHERE s.token_hash = %s
			  AND s.expires_at > NOW()
			ORDER BY s.id DESC
			LIMIT 1
		) t
	`, quote(hashToken(token))))
	if err != nil {
		return auth.User{}, err
	}
	if string(data) == "null" {
		return auth.User{}, fmt.Errorf("invalid token")
	}
	var row whoamiRow
	if err := json.Unmarshal(data, &row); err != nil {
		return auth.User{}, fmt.Errorf("decode whoami row: %w", err)
	}
	return auth.User{ID: row.ID, Username: row.Username, Role: row.Role}, nil
}

func (s *PostgresStore) GetSystemInfo(_ context.Context) (system.Info, error) {
	schemaVersion, err := s.SchemaVersion()
	if err != nil {
		return system.Info{}, err
	}
	data, err := s.client.QueryJSON(`
		SELECT row_to_json(t)
		FROM (
			SELECT installation_id, display_name, initialized_at
			FROM system_instance
			ORDER BY id ASC
			LIMIT 1
		) t
	`)
	if err != nil {
		return system.Info{}, err
	}
	var sys systemRow
	if err := json.Unmarshal(data, &sys); err != nil {
		return system.Info{}, fmt.Errorf("decode system row: %w", err)
	}
	endpointCount, err := s.rowCount(`SELECT COUNT(*) AS count FROM endpoints`)
	if err != nil {
		return system.Info{}, err
	}
	adminCount, err := s.rowCount(`SELECT COUNT(*) AS count FROM users WHERE role = 'admin'`)
	if err != nil {
		return system.Info{}, err
	}
	auditCount, err := s.rowCount(`SELECT COUNT(*) AS count FROM audit_events`)
	if err != nil {
		return system.Info{}, err
	}
	deploymentCount, err := s.rowCount(`SELECT COUNT(*) AS count FROM deployments`)
	if err != nil {
		return system.Info{}, err
	}
	certificateCount, err := s.rowCount(`SELECT COUNT(*) AS count FROM certificates`)
	if err != nil {
		return system.Info{}, err
	}

	return system.Info{
		InstallationID:   sys.InstallationID,
		DisplayName:      sys.DisplayName,
		InitializedAt:    sys.InitializedAt,
		Version:          s.version,
		SchemaVersion:    schemaVersion,
		SafeDSN:          SafeDSN(s.cfg.Postgres.DSN),
		DataDir:          s.cfg.Storage.DataDir,
		MasterKeyLoaded:  true,
		APIAddress:       s.cfg.Server.Listen,
		DBStatus:         "ok",
		EndpointCount:    endpointCount,
		AdminCount:       adminCount,
		AuditCount:       auditCount,
		DeploymentCount:  deploymentCount,
		CertificateCount: certificateCount,
	}, nil
}

func (s *PostgresStore) ListEndpoints(_ context.Context, user auth.User) ([]Endpoint, error) {
	query := `
		SELECT COALESCE(json_agg(row_to_json(t) ORDER BY t.name), '[]'::json)
		FROM (
			SELECT e.id, e.name, e.address, e.description, e.created_at
			FROM endpoints e
	`
	if !user.IsOwner() {
		query += fmt.Sprintf(`
			JOIN endpoint_admin_access a ON a.endpoint_id = e.id
			WHERE a.user_id = %d AND a.can_view = TRUE
		`, user.ID)
	}
	query += `
		) t
	`
	data, err := s.client.QueryJSON(query)
	if err != nil {
		return nil, err
	}
	var items []Endpoint
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("decode endpoints: %w", err)
	}
	return items, nil
}

func (s *PostgresStore) ListDeployments(_ context.Context, user auth.User) ([]Deployment, error) {
	query := `
		SELECT COALESCE(json_agg(row_to_json(t) ORDER BY t.started_at DESC), '[]'::json)
		FROM (
			SELECT d.id, e.name AS endpoint, d.operation_type AS operation, d.status, d.summary, d.started_at, d.finished_at, COALESCE(u.username, 'system') AS triggered_by
			FROM deployments d
			LEFT JOIN endpoints e ON e.id = d.endpoint_id
			LEFT JOIN users u ON u.id = d.created_by_user_id
	`
	if !user.IsOwner() {
		query += fmt.Sprintf(`
			JOIN endpoint_admin_access a ON a.endpoint_id = d.endpoint_id
			WHERE a.user_id = %d AND a.can_view = TRUE
		`, user.ID)
	}
	query += `
		) t
	`
	data, err := s.client.QueryJSON(query)
	if err != nil {
		return nil, err
	}
	var items []Deployment
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("decode deployments: %w", err)
	}
	return items, nil
}

func (s *PostgresStore) ListAuditEvents(_ context.Context, user auth.User) ([]audit.Event, error) {
	query := `
		SELECT COALESCE(json_agg(row_to_json(t) ORDER BY t.created_at DESC), '[]'::json)
		FROM (
			SELECT a.id, COALESCE(u.username, 'system') AS actor, a.action, a.result, a.message, e.name AS endpoint, a.created_at
			FROM audit_events a
			LEFT JOIN users u ON u.id = a.actor_user_id
			LEFT JOIN endpoints e ON e.id = a.endpoint_id
	`
	if !user.IsOwner() {
		query += fmt.Sprintf(`
			LEFT JOIN endpoint_admin_access access ON access.endpoint_id = a.endpoint_id AND access.user_id = %d
			WHERE a.endpoint_id IS NULL OR access.can_view = TRUE
		`, user.ID)
	}
	query += `
		) t
	`
	data, err := s.client.QueryJSON(query)
	if err != nil {
		return nil, err
	}
	var items []audit.Event
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("decode audit: %w", err)
	}
	return items, nil
}

func (s *PostgresStore) ListAccess(_ context.Context, user auth.User) ([]AccessSummary, error) {
	query := `
		SELECT COALESCE(json_agg(row_to_json(t) ORDER BY t.endpoint_name), '[]'::json)
		FROM (
			SELECT e.name AS endpoint_name, a.can_view, a.can_inspect, a.can_deploy, a.can_manage_users, a.can_manage_certificates, a.can_manage_endpoint
			FROM endpoint_admin_access a
			JOIN endpoints e ON e.id = a.endpoint_id
	`
	if !user.IsOwner() {
		query += fmt.Sprintf(`
			WHERE a.user_id = %d
		`, user.ID)
	}
	query += `
		) t
	`
	data, err := s.client.QueryJSON(query)
	if err != nil {
		return nil, err
	}
	var items []AccessSummary
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("decode access: %w", err)
	}
	return items, nil
}

func (s *PostgresStore) InsertAudit(_ context.Context, actorID int64, endpointID *int64, action, result, message string) error {
	endpointValue := "NULL"
	if endpointID != nil {
		endpointValue = strconv.FormatInt(*endpointID, 10)
	}
	return s.client.Exec(fmt.Sprintf(`
		INSERT INTO audit_events (actor_user_id, endpoint_id, action, result, message)
		VALUES (%d, %s, %s, %s, %s)
	`, actorID, endpointValue, quote(action), quote(result), quote(message)))
}

func (s *PostgresStore) rowCount(query string) (int, error) {
	data, err := s.client.QueryJSON(fmt.Sprintf(`SELECT row_to_json(t) FROM (%s) t`, query))
	if err != nil {
		return 0, err
	}
	var row countRow
	if err := json.Unmarshal(data, &row); err != nil {
		return 0, fmt.Errorf("decode count row: %w", err)
	}
	return row.Count, nil
}

func (s *PostgresStore) isMigrationApplied(version int) (bool, error) {
	count, err := s.rowCount(fmt.Sprintf("SELECT COUNT(*) AS count FROM schema_migrations WHERE version = %d", version))
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func parseMigrationVersion(name string) (int, error) {
	parts := strings.SplitN(name, "_", 2)
	if len(parts) < 1 {
		return 0, fmt.Errorf("invalid migration name %q", name)
	}
	version, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("parse migration version %q: %w", name, err)
	}
	return version, nil
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

func quote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
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

func hashPassword(password string) (salt string, hash string) {
	saltBytes := make([]byte, 16)
	_, _ = rand.Read(saltBytes)
	salt = hex.EncodeToString(saltBytes)
	hash = derivePasswordHash(password, salt)
	return salt, hash
}

func verifyPassword(password, salt, expected string) bool {
	return derivePasswordHash(password, salt) == expected
}

func derivePasswordHash(password, salt string) string {
	digest := []byte(password + ":" + salt)
	for i := 0; i < 20000; i++ {
		sum := sha256.Sum256(digest)
		digest = sum[:]
	}
	return hex.EncodeToString(digest)
}
