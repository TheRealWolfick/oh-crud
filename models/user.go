package models

import (
	"errors"
	"strings"
)

type UserInfoResponse struct {
	Username	 string `json:"username" db:"username"`
	Email      string `json:"email" db:"email"`
	Mobile		 string `json:"mobile" db:"mobile"`
}

type CreateUserRequest struct {
	UserInfoResponse
}

type User struct {
	UserInfoResponse
	Api_Access bool   `json:"api_access" db:"api_access"`
	Api_Key    string `json:"api_key" db:"api_key"`
}

type UserRequest struct {
	Username	 string `json:"username" db:"username"`
	Api_Key    string `json:"api_key" db:"api_key"`
}

type UserUpdate struct {
	Email     *string `json:"email,omitempty"`
	Mobile    *string `json:"mobile,omitempty"`
}

func (r *UserRequest) Validate() error {
	r.Username = strings.TrimSpace(r.Username)
	r.Api_Key = strings.TrimSpace(r.Api_Key)

	if r.Username == "" {
		return errors.New("A username must be supplied.")
	}
	if len(r.Username) > 20 {
		return errors.New("Username too long")
	}

	return nil
}
