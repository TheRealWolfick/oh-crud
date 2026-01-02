package models

type Domain struct {
	Domain_code *string `json:"domain_code"                   db:"domain_code"         req:"true"  pk:"true"  explicit:""     `
	Description *string `json:"domain_description,omitempty"  db:"domain_description"  req:""      pk:""      none:""  explicit:""     `
}

type GetDomain struct {
	Domain_code *string `json:"domain_code,omitempty"         db:"domain_code"         req:"true"  pk:"true"  explicit:""     `
	Description *string `json:"domain_description,omitempty"  db:"domain_description"  req:""      pk:""      none:""  explicit:""     `
}

type Building struct {
	Building_id	 					*int     `json:"building_id,omitempty"           db:"building_id"           req:""      pk:"true"  none:"DEFAULT"  explicit:""     `
	Building 							*string  `json:"building,omitempty"              db:"building"              req:"true"  pk:"true"  explicit:""     `
	Db_domain 						*string  `json:"db_domain,omitempty"             db:"db_domain"             req:"true"  pk:""      explicit:""     `
	Building_description 	*string  `json:"building_description,omitempty"  db:"building_description"  req:""      pk:""      none:"NULL"     explicit:""     `
	Building_address 			*string  `json:"building_address,omitempty"      db:"building_address"      req:""      pk:""      none:"NULL"     explicit:""     `
	Building_suburb 			*string  `json:"building_suburb,omitempty"       db:"building_suburb"       req:""      pk:""      none:"NULL"     explicit:""     `
	Building_state 				*string  `json:"building_state,omitempty"        db:"building_state"        req:""      pk:""      none:"NULL"     explicit:""     `
	Is_active 						*bool 	 `json:"is_active,omitempty"             db:"is_active"             req:""      pk:""      none:"DEFAULT"  explicit:""     `
}

type Floors struct {
	Floor_id   *int     `json:"floor_id,omitempty"  db:"floor_id"  pk:"true"  req:""      none:"DEFAULT"  explicit:""     `
	Floor      *string  `json:"floor"               db:"floor"     pk:"true"  req:"true"  explicit:"true" `
}

type Building_Floor struct {
	Bfloor_id   *int     `json:"bfloor_id,omitempty"  db:"bfloor_id"  req:""      pk:"true"  none:"DEFAULT"  explicit:""     `
	Building 		*string  `json:"building"             db:"building"   req:"true"  pk:"true"  explicit:""     `
	Floor       *string  `json:"floor"                db:"floor"      req:"true"  pk:""      explicit:"true" `
	Is_active   *bool    `json:"is_active,omitempty"  db:"is_active"  req:""      pk:""      none:"DEFAULT"  explicit:""     `
}

type Building_Floor_Room struct {
	Bfroom_id					*int     `json:"bfroom_id,omitempty"         db:"bfroom_id"        req:""      pk:"true"  none:"DEFAULT"  explicit:""     `
	Building					*string  `json:"building"                    db:"building"         req:"true"  pk:"true"  explicit:""     `
	Floor							*string  `json:"floor"                       db:"floor"            req:"true"  pk:"true"  explicit:"true" `
	Room							*string  `json:"room"                        db:"room"             req:"true"  pk:"true"  explicit:""     `
	Room_description  *string  `json:"room_description,omitempty"  db:"room_description" req:""      pk:"true"  none:"DEFAULT"  explicit:""     `
}

type Departments struct {
	Dept_id 	 *int 		`json:"dept_id,omitempty"    db:"dept_id"    req:""      pk:"true"  none:"DEFAULT"  explicit:""     `
	Dept_code  *string  `json:"dept_code"            db:"dept_code"  req:"true"  pk:"true"  explicit:""     `
	Building 	 *string  `json:"dept"                 db:"building"   req:""      pk:""      none:"NULL"     explicit:""     `
	Floor 		 *string  `json:"floor"                db:"floor"      req:""      pk:""      none:"NULL"     explicit:"true" `
	Is_active  *bool    `json:"is_active,omitempty"  db:"is_active"  req:""      pk:""      none:"DEFAULT"  explicit:""     `
}
