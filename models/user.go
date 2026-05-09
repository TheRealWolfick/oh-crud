package models

type UserInfoResponse struct {
	Username string `json:"username" db:"username"`
	Email    string `json:"email" db:"email"`
	Mobile   string `json:"mobile" db:"mobile"`
}

type User struct {
	UserInfoResponse
	Api_Access bool   `json:"api_access" db:"api_access"`
	Api_Key    string `json:"api_key" db:"api_key"`
	Roles      string `json:"roles" db:"roles"`
}

type UserRequest struct {
	Username  string `json:"username" db:"username"`
	Api_Key   string `json:"api_key" db:"api_key"`
}
