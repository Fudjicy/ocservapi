package system

import "time"

type Info struct {
	InstallationID   string    `json:"installation_id"`
	DisplayName      string    `json:"display_name"`
	InitializedAt    time.Time `json:"initialized_at"`
	Version          string    `json:"version"`
	SchemaVersion    uint      `json:"schema_version"`
	SafeDSN          string    `json:"safe_dsn"`
	DataDir          string    `json:"data_dir"`
	MasterKeyLoaded  bool      `json:"master_key_loaded"`
	APIAddress       string    `json:"api_address"`
	DBStatus         string    `json:"db_status"`
	EndpointCount    int       `json:"endpoint_count"`
	AdminCount       int       `json:"admin_count"`
	AuditCount       int       `json:"audit_count"`
	DeploymentCount  int       `json:"deployment_count"`
	CertificateCount int       `json:"certificate_count"`
}
