package store

import "time"

type Endpoint struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Address     string    `json:"address"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

type Deployment struct {
	ID          int64      `json:"id"`
	Endpoint    *string    `json:"endpoint,omitempty"`
	Operation   string     `json:"operation"`
	Status      string     `json:"status"`
	Summary     string     `json:"summary"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	TriggeredBy string     `json:"triggered_by"`
}

type CertificateSummary struct {
	Total int `json:"total"`
}

type AccessSummary struct {
	EndpointName          string `json:"endpoint_name"`
	CanView               bool   `json:"can_view"`
	CanInspect            bool   `json:"can_inspect"`
	CanDeploy             bool   `json:"can_deploy"`
	CanManageUsers        bool   `json:"can_manage_users"`
	CanManageCertificates bool   `json:"can_manage_certificates"`
	CanManageEndpoint     bool   `json:"can_manage_endpoint"`
}
