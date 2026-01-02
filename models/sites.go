package models

type Domain struct {
	Domain_code *string `json:"domain_code" db:"domain_code" req:"true" pk:"true"`
	Description *string `json:"domain_description,omitempty" db:"domain_description" none:""`
}

type GetDomain struct {
	Domain_code *string `json:"domain_code,omitempty" db:"domain_code" req:"true" pk:"true"`
	Description *string `json:"domain_description,omitempty" db:"domain_description" none:""`
}

type Building struct {
	Building_id	 					*int     `json:"building_id,omitempty" db:"building_id" pk:"true" none:"DEFAULT"`
	Building 							*string  `json:"building,omitempty" db:"building" req:"true" pk:"true"`
	Db_domain 						*string  `json:"db_domain,omitempty" db:"db_domain" req:"true"`
	Building_description 	*string  `json:"building_description,omitempty" db:"building_description" none:"NULL"`
	Building_address 			*string  `json:"building_address,omitempty" db:"building_address" none:"NULL"`
	Building_suburb 			*string  `json:"building_suburb,omitempty" db:"building_suburb" none:"NULL"`
	Building_state 				*string  `json:"building_state,omitempty" db:"building_state" none:"NULL"`
	Is_active 						*bool 	 `json:"is_active,omitempty" db:"is_active" none:"DEFAULT"`
}

type Floors struct {
	Floor_id   *int     `json:"floor_id,omitempty" db:"floor_id" pk:"true" none:"DEFAULT"`
	Floor      *string  `json:"floor" db:"floor" pk:"true" req:"true"`
}

type Building_Floor struct {
	Bfloor_id   *int     `json:"bfloor_id,omitempty" db:"bfloor_id" pk:"true" none:"DEFAULT"`
	Building 		*string  `json:"building" db:"building" req:"true"`
	Floor       *string  `json:"floor" db:"floor" req:"true"`
	Is_active   *bool    `json:"is_active,omitempty" db:"is_active" none:"DEFAULT"`
}

type Building_Floor_Room struct {
	Bfroom_id					*int     `json:"bfroom_id,omitempty" db:"bfroom_id" pk:"true" none:"DEFAULT"`
	Building					*string  `json:"building" db:"building" req:"true"`
	Floor							*string  `json:"floor" db:"floor" req:"true"`
	Room							*string  `json:"room" db:"room" req:"true"`
	Room_description  *string  `json:"Room_description,omitempty" db:"Room_description" none:"DEFAULT"`
}

type Departments struct {
	Dept_id 	 *int 		`json:"dept_id,omitempty" db:"dept_id" pk:"true" none:"DEFAULT"`
	Dept_code  *string  `json:"dept_code" db:"dept_code" pk:"true" req:"true"`
	Building 	 *string  `json:"dept" db:"building" none:"NULL"`
	Floor 		 *string  `json:"floor" db:"floor" none:"NULL"`
	Is_active  *bool    `json:"is_active,omitempty" db:"is_active" none:"DEFAULT"`
}
