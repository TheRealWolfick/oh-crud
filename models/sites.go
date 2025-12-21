package models

type Domain struct {
	Domain_code *string `json:"domain_code" db:"domain_code" req:"true" pk:"true"`
	Description *string `json:"domain_description,omitempty" db:"domain_description" none:""`
}

type GetDomain struct {
	Domain_code *string `json:"domain_code,omitempty" db:"domain_code" req:"true" pk:"true"`
	Description *string `json:"domain_description,omitempty" db:"domain_description"`
}
