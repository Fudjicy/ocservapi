package audit

import "time"

type Event struct {
	ID        int64     `json:"id"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	Result    string    `json:"result"`
	Message   string    `json:"message"`
	Endpoint  *string   `json:"endpoint,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}
