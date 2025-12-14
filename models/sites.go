package models

type Domain struct {
	Domain_code *string `json:"domain_code" db:"domain_code"`
	Description *string `json:"domain_description,omitempty" db:"domain_description" none:""`
}
