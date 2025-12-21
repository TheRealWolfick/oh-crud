package models

type Domain struct {
	Domain_code *string `json:"domain_code" db:"domain_code" req:"true" pk:"true"`
	Description *string `json:"domain_description,omitempty" db:"domain_description" none:""`
}

type GetDomain struct {
	Domain_code *string `json:"domain_code,omitempty" db:"domain_code" req:"true" pk:"true"`
	Description *string `json:"domain_description,omitempty" db:"domain_description"`
}

type Building struct {
	Building_id	 					*int `json:"building_id,omitempty" db:"building_id" pk:"true"`
	Building 							*string `json:"building,omitempty" db:"building" req:"true" pk:"true"`
	Db_domain 						*string `json:"db_domain,omitempty" db:"db_domain" req:"true"`
	Building_description 	*string `json:"building_description,omitempty" db:"building_description"`
	Building_address 			*string `json:"building_address,omitempty" db:"building_address"`
	Building_suburb 			*string `json:"building_suburb,omitempty" db:"building_suburb"`
	Building_state 				*string `json:"building_state,omitempty" db:"building_state"`
	Is_active 						*bool `json:"is_active,omitempty" db:"is_active"`
}
