package auth

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

func (u User) IsOwner() bool {
	return u.Role == "owner"
}
