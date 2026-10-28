package models

import "time"

type User struct {
	ID            string     `json:"id" db:"id"`
	Username      string     `json:"username" db:"username"`
	Displayname   string     `json:"display_name" db:"display_name"`
	PasswordHash  string     `json:"password_hash" db:"password_hash"`
	TotpSecret    []byte     `json:"totp_secret" db:"totp_secret"`
	CreatedAt     time.Time  `json:"create_at" db:"created_at"`
	DisabledAt    *time.Time `json:"disabled_at" db:"disabled_at"`
	Roles         []string   `json:"roles" db:"roles"`
}

type RefreshToken struct {
	ID         int        `json:"id" db:"id"`
	TokenHash  string     `json:"token_hash" db:"token_hash"`
	UserID     string     `json:"user_id" db:"user_id"`
	ClientID   string     `json:"client_id" db:"client_id"`
	FamilyID   string     `json:"family_id" db:"family_id"`
	ExpiresAt  time.Time  `json:"expires_at" db:"expires_at"`
	UsedAt     *time.Time `json:"used_at" db:"used_at"`
	RevokedAt  *time.Time `json:"revoked_at" db:"revoked_at"`
}
