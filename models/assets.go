package models

type Asset_Categories struct {
  Cat_code          *string  `json:"cat_code"                   db:"cat_code"         req:"true"  pk:"true"                 `
	Asset_system      *string  `json:"asset_system,omitempty"     db:"asset_system"     req:""      pk:""      none:"DEFAULT" `
  Asset_subsystem   *string  `json:"asset_subsystem,omitempty"  db:"asset_subsystem"  req:""      pk:""      none:"DEFAULT" `
  Is_active         *bool    `json:"is_active,omitempty"        db:"is_active"        req:""      pk:""      none:"DEFAULT" `
	Description       *string  `json:"description,omitempty"      db:"description"      req:""      pk:""      none:""        `
}

type Condition_Ratings struct {
  Condition_rating  *int     `json:"condition_rating" db:"condition_rating"  pk:"true"  req:"true"          `
	Description       *string  `json:"description"      db:"description"       pk:""      req:""      none:"" `
	Long_description  *string  `json:"long_description" db:"long_description"  pk:""      req:""      none:"" `
}

type Asset_Data struct {
	Asset_id                 *int      `json:"asset_id"                db:"asset_id"                req:""      pk:"true"  none:"DEFAULT" `
  Added_date               *string   `json:"added_date,omitempty"              db:"added_date"              req:""      pk:""      none:"DEFAULT" `
  Additional_details       *string   `json:"additional_details,omitempty"      db:"additional_details"      req:""      pk:""      none:""        `
  Asset_category           *string   `json:"asset_category"                    db:"asset_category"          req:"true"  pk:""                     `
  Asset_no                 *string   `json:"asset_no"                          db:"asset_no"                req:""      pk:"true"  none:"DEFAULT" `
  Asset_status             *string   `json:"asset_status,omitempty"            db:"asset_status"            req:""      pk:""      none:"DEFAULT" `
  Building                 *string   `json:"building,omitempty"                db:"building"                req:"true"  pk:""                     `
  Condition_rating         *int      `json:"condition_rating,omitempty"        db:"condition_rating"        req:""      pk:""      none:"NULL"    `
  Contact_ID               *string   `json:"contact_ID,omitempty"              db:"contact_id"            req:""      pk:""      none:"DEFAULT" `
  Department               *string   `json:"department,omitempty"              db:"department"              req:"true"  pk:""                     `
  Description              *string   `json:"description,omitempty"             db:"description"             req:"true"  pk:""                     `
  Disposal_cost            *float64  `json:"disposal_cost,omitempty"           db:"disposal_cost"           req:""      pk:""      none:"DEFAULT" `
  Disposal_date            *string   `json:"disposal_date,omitempty"           db:"disposal_date"           req:""      pk:""      none:"NULL"    `
	Disposal_reason          *string   `json:"disposal_reason,omitempty"         db:"disposal_reason"         req:""      pk:"" `
  Domain                   *string   `json:"domain"                            db:"domain"                  req:"true"  pk:""                     `
	Finance_group_code       *string   `json:"finance_group_code,omitempty"      db:"finance_group_code"      req:""      pk:""      none:"DEFAULT" `
	Floor                    *string   `json:"floor"                             db:"floor"                   req:"true"  pk:""                      absolute:"true" `
  GL_asset_reference       *string   `json:"GL_asset_reference,omitempty"      db:"gl_asset_reference"    req:""      pk:""      none:"DEFAULT" `
  Install_date             *string   `json:"install_date,omitempty"            db:"install_date"            req:""      pk:""      none:"NULL"    `
  Installation_cost        *float64  `json:"installation_cost,omitempty"       db:"installation_cost"       req:""      pk:""      none:"DEFAULT" `
  Invoice_no               *string   `json:"invoice_no,omitempty"              db:"invoice_no"              req:""      pk:""      none:"DEFAULT" `
	Is_virtual_asset         *bool     `json:"is_virtual_asset,omitempty"        db:"is_virtual_asset"        req:""      pk:""      none:"DEFAULT" `
  Label_location           *string   `json:"label_location,omitempty"          db:"label_location"          req:""      pk:""      none:"DEFAULT" `
  Latitude                 *float64  `json:"latitude,omitempty"                db:"latitude"                req:""      pk:""      none:"DEFAULT" `
  Location                 *string   `json:"location,omitempty"                db:"location"                req:""      pk:""      none:"DEFAULT" `
  Longitude                *float64  `json:"longitude,omitempty"               db:"longitude"               req:""      pk:""      none:"DEFAULT" `
  Make                     *string   `json:"make,omitempty"                    db:"make"                    req:""      pk:""      none:"DEFAULT" `
  Manufacturer             *string   `json:"manufacturer,omitempty"            db:"manufacturer"            req:""      pk:""      none:"DEFAULT" `
  Model                    *string   `json:"model,omitempty"                   db:"model"                   req:""      pk:""      none:"DEFAULT" `
  Owning_cost_center       *string   `json:"owning_cost_center,omitempty"      db:"owning_cost_center"      req:""      pk:""      none:"DEFAULT" `
  Purchase_cost            *float64  `json:"purchase_cost,omitempty"           db:"purchase_cost"           req:""      pk:""      none:"DEFAULT" `
  Purchase_date            *string   `json:"purchase_date,omitempty"           db:"purchase_date"           req:""      pk:""      none:"NULL"    `
  Purchase_order_no        *string   `json:"purchase_order_no,omitempty"       db:"purchase_order_no"       req:""      pk:""      none:"DEFAULT" `
  Purchasing_cost_center   *string   `json:"purchasing_cost_center,omitempty"  db:"purchasing_cost_center"  req:""      pk:""      none:"DEFAULT" `
  RFID_tag_ID              *string   `json:"RFID_tag_ID,omitempty"             db:"rfid_tag_id"           req:""      pk:""      none:"DEFAULT" `
  Room                     *string   `json:"room,omitempty"                    db:"room"                    req:""      pk:""      none:"DEFAULT" `
  Serial                   *string   `json:"serial,omitempty"                  db:"serial"                  req:""      pk:""      none:"DEFAULT" `
  Service_agent            *string   `json:"service_agent,omitempty"           db:"service_agent"           req:""      pk:""      none:"DEFAULT" `
  Spare_parts              *string   `json:"spare_parts,omitempty"             db:"spare_parts"             req:""      pk:""      none:"DEFAULT" `
  Spare_parts_bin_no       *string   `json:"spare_parts_bin_no,omitempty"      db:"spare_parts_bin_no"      req:""      pk:""      none:"DEFAULT" `
  Spare_parts_held         *string   `json:"spare_parts_held,omitempty"        db:"spare_parts_held"        req:""      pk:""      none:"DEFAULT" `
  Supplier_code            *string   `json:"supplier_code,omitempty"           db:"supplier_code"           req:""      pk:""      none:"DEFAULT" `
  Tech_manual_reference    *string   `json:"tech_manual_reference,omitempty"   db:"tech_manual_reference"   req:""      pk:""      none:"DEFAULT" `
  User_defined_fields      *string   `json:"user_defined_fields,omitempty"     db:"user_defined_fields"     req:""      pk:""      none:"NULL"    `
  Warranty_period          *int      `json:"warranty_period,omitempty"         db:"warranty_period"         req:""      pk:""      none:"12"      `
  Working_life             *int      `json:"working_life,omitempty"            db:"working_life"            req:""      pk:""      none:"NULL"    `
  Ownership_status         *string   `json:"ownership_status,omitempty"        db:"ownership_status"        req:""      pk:""      none:"DEFAULT" `
  Altered_date             *string   `json:"altered_date,omitempty"            db:"altered_date"            req:""      pk:""      none:"DEFAULT" `
}
