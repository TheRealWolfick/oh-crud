package models

import "github.com/golang-jwt/jwt/v5"

type Jwk struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
}

type Claims struct {
	TokenUse  string    `json:"token_use"`   // Service or user
	Roles     []string  `json:"roles,omitempty"`
	SID       string    `json:"sid,omitempty"`  // login session - user ID
	AMR       []string  `json:"amr,omitempty"`  // How the user authenticated
	Username  string    `json:"preferred_username,omitempty"` // Recorded against writes made with a user token
	ClientID  string    `json:"client_id,omitempty"`          // The client a user token was issued to
	jwt.RegisteredClaims
}
