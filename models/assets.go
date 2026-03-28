package models

import "time"

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
	Asset_id                 *int        `json:"asset_id,omitempty"                db:"asset_id"                req:""      pk:"true"  none:"DEFAULT"  exclude_diff:"true"`
	Added_date               *time.Time  `json:"added_date,omitempty"              db:"added_date"              req:""      pk:""      none:"DEFAULT"  exclude_diff:"true"`
	Additional_details       *string     `json:"additional_details,omitempty"      db:"additional_details"      req:""      pk:""      none:""         exclude_diff:"true"`
	Asset_category           *string     `json:"asset_category"                    db:"asset_category"          req:"true"  pk:""                     `
	Asset_no                 *string     `json:"asset_no"                          db:"asset_no"                req:"true"  pk:"true"  none:"DEFAULT"  diff:"true"`
  Asset_status             *string     `json:"asset_status,omitempty"            db:"asset_status"            req:""      pk:""      none:"DEFAULT" `
  Building                 *string     `json:"building,omitempty"                db:"building"                req:"true"  pk:""                     `
  Condition_rating         *int        `json:"condition_rating,omitempty"        db:"condition_rating"        req:""      pk:""      none:"NULL"    `
	Condition_rating_date    *time.Time  `json:"condition_rating_date,omitempty"   db:"condition_rating_date"   req:""      pk:""      none:"NULL"    `
	Contact_ID               *string     `json:"contact_ID,omitempty"              db:"contact_id"              req:""      pk:""      none:"DEFAULT"  exclude_diff:"true"`
  Department               *string     `json:"department,omitempty"              db:"department"              req:"true"  pk:""                     `
  Description              *string     `json:"description,omitempty"             db:"description"             req:"true"  pk:""                     `
  Disposal_cost            *float64    `json:"disposal_cost,omitempty"           db:"disposal_cost"           req:""      pk:""      none:"DEFAULT" `
  Disposal_date            *time.Time  `json:"disposal_date,omitempty"           db:"disposal_date"           req:""      pk:""      none:"NULL"    `
	Disposal_reason          *string     `json:"disposal_reason,omitempty"         db:"disposal_reason"         req:""      pk:""      none:"NULL"`
  Domain                   *string     `json:"domain"                            db:"domain"                  req:"true"  pk:""                     `
	Finance_group_code       *string     `json:"finance_group_code,omitempty"      db:"finance_group_code"      req:""      pk:""      none:"DEFAULT"  exclude_diff:"true"`
	Floor                    *string     `json:"floor"                             db:"floor"                   req:"true"  pk:""                      absolute:"true" `
	GL_asset_reference       *string     `json:"GL_asset_reference,omitempty"      db:"gl_asset_reference"      req:""      pk:""      none:"DEFAULT"  exclude_diff:"true"`
  Install_date             *time.Time  `json:"install_date,omitempty"            db:"install_date"            req:""      pk:""      none:"NULL"    `
  Installation_cost        *float64    `json:"installation_cost,omitempty"       db:"installation_cost"       req:""      pk:""      none:"DEFAULT" `
  Invoice_no               *string     `json:"invoice_no,omitempty"              db:"invoice_no"              req:""      pk:""      none:"DEFAULT" `
	Is_virtual_asset         *bool       `json:"is_virtual_asset,omitempty"        db:"is_virtual_asset"        req:""      pk:""      none:"DEFAULT"  exclude_diff:"true"`
	Label_location           *string     `json:"label_location,omitempty"          db:"label_location"          req:""      pk:""      none:"DEFAULT"  exclude_diff:"true"`
  Latitude                 *float64    `json:"latitude,omitempty"                db:"latitude"                req:""      pk:""      none:"DEFAULT" `
	Location                 *string     `json:"location,omitempty"                db:"location"                req:""      pk:""      none:"DEFAULT"  exclude_diff:"true"`
  Longitude                *float64    `json:"longitude,omitempty"               db:"longitude"               req:""      pk:""      none:"DEFAULT" `
  Make                     *string     `json:"make,omitempty"                    db:"make"                    req:""      pk:""      none:"DEFAULT" `
	Manufacturer             *string     `json:"manufacturer,omitempty"            db:"manufacturer"            req:""      pk:""      none:"DEFAULT"  exclude_diff:"true"`
  Model                    *string     `json:"model,omitempty"                   db:"model"                   req:""      pk:""      none:"DEFAULT" `
  Owning_cost_center       *string     `json:"owning_cost_center,omitempty"      db:"owning_cost_center"      req:""      pk:""      none:"DEFAULT" `
  Purchase_cost            *float64    `json:"purchase_cost,omitempty"           db:"purchase_cost"           req:""      pk:""      none:"DEFAULT" `
  Purchase_date            *time.Time  `json:"purchase_date,omitempty"           db:"purchase_date"           req:""      pk:""      none:"NULL"    `
  Purchase_order_no        *string     `json:"purchase_order_no,omitempty"       db:"purchase_order_no"       req:""      pk:""      none:"DEFAULT" `
  Purchasing_cost_center   *string     `json:"purchasing_cost_center,omitempty"  db:"purchasing_cost_center"  req:""      pk:""      none:"DEFAULT" `
	RFID_tag_ID              *string     `json:"RFID_tag_ID,omitempty"             db:"rfid_tag_id"             req:""      pk:""      none:"DEFAULT"  exclude_diff:"true"`
  Room                     *string     `json:"room,omitempty"                    db:"room"                    req:""      pk:""      none:"DEFAULT" `
  Serial                   *string     `json:"serial,omitempty"                  db:"serial"                  req:""      pk:""      none:"DEFAULT" `
	Service_agent            *string     `json:"service_agent,omitempty"           db:"service_agent"           req:""      pk:""      none:"DEFAULT"  exclude_diff:"true"`
	Spare_parts              *string     `json:"spare_parts,omitempty"             db:"spare_parts"             req:""      pk:""      none:"DEFAULT"  exclude_diff:"true"`
	Spare_parts_bin_no       *string     `json:"spare_parts_bin_no,omitempty"      db:"spare_parts_bin_no"      req:""      pk:""      none:"DEFAULT"  exclude_diff:"true"`
	Spare_parts_held         *string     `json:"spare_parts_held,omitempty"        db:"spare_parts_held"        req:""      pk:""      none:"DEFAULT"  exclude_diff:"true"`
  Supplier_code            *string     `json:"supplier_code,omitempty"           db:"supplier_code"           req:""      pk:""      none:"DEFAULT" `
  Tech_manual_reference    *string     `json:"tech_manual_reference,omitempty"   db:"tech_manual_reference"   req:""      pk:""      none:"DEFAULT" `
	User_defined_fields      *string     `json:"user_defined_fields,omitempty"     db:"user_defined_fields"     req:""      pk:""      none:"NULL"     exclude_diff:"true"`
  Warranty_period          *int        `json:"warranty_period,omitempty"         db:"warranty_period"         req:""      pk:""      none:"12"      `
  Working_life             *int        `json:"working_life,omitempty"            db:"working_life"            req:""      pk:""      none:"NULL"    `
  Ownership_status         *string     `json:"ownership_status,omitempty"        db:"ownership_status"        req:""      pk:""      none:"DEFAULT" `
	Altered_date             *time.Time  `json:"altered_date,omitempty"            db:"altered_date"            req:""      pk:""      none:"DEFAULT"  exclude_diff:"true"`
}

type Unresolved_Assets struct {
	Asset_id                 *int       `json:"asset_id,omitempty"                db:"asset_id"                req:""      pk:"true"  none:"DEFAULT"  customwhere:"value->'item'->>'asset_id'" `
	Added_date               *time.Time `json:"added_date,omitempty"              db:"added_date"              req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'added_date'" `
	Additional_details       *string    `json:"additional_details,omitempty"      db:"additional_details"      req:""      pk:""      none:""         customwhere:"value->'item'->>'additional_details'" `
	Asset_category           *string    `json:"asset_category"                    db:"asset_category"          req:"true"  pk:""                      customwhere:"value->'item'->>'asset_category'" `
	Asset_no                 *string    `json:"asset_no"                          db:"asset_no"                req:""      pk:"true"  none:"DEFAULT"  customwhere:"value->'item'->>'asset_no'" `
	Asset_status             *string    `json:"asset_status,omitempty"            db:"asset_status"            req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'asset_status'" `
	Building                 *string    `json:"building,omitempty"                db:"building"                req:"true"  pk:""                      customwhere:"value->'item'->>'building'" `
	Condition_rating         *string       `json:"condition_rating,omitempty"        db:"condition_rating"        req:""      pk:""      none:"NULL"     customwhere:"value->'item'->>'condition_rating'" `
	Condition_rating_date    *time.Time `json:"condition_rating_date,omitempty"   db:"condition_rating_date"   req:""      pk:""      none:"NULL"     customwhere:"value->'item'->>'condition_rating_date'"`
	Contact_ID               *string    `json:"contact_ID,omitempty"              db:"contact_id"              req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'contact_id'" `
	Department               *string    `json:"department,omitempty"              db:"department"              req:"true"  pk:""                      customwhere:"value->'item'->>'department'" `
	Description              *string    `json:"description,omitempty"             db:"description"             req:"true"  pk:""                      customwhere:"value->'item'->>'description'" `
	Disposal_cost            *float64   `json:"disposal_cost,omitempty"           db:"disposal_cost"           req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'disposal_cost'" `
	Disposal_date            *time.Time `json:"disposal_date,omitempty"           db:"disposal_date"           req:""      pk:""      none:"NULL"     customwhere:"value->'item'->>'disposal_date'" `
	Disposal_reason          *string    `json:"disposal_reason,omitempty"         db:"disposal_reason"         req:""      pk:""      none:""         customwhere:"value->'item'->>'disposal_reason'" `
	Domain                   *string    `json:"domain"                            db:"domain"                  req:"true"  pk:""                      customwhere:"value->'item'->>'domain'" `
	Finance_group_code       *string    `json:"finance_group_code,omitempty"      db:"finance_group_code"      req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'finance_group_code'" `
	Floor                    *string    `json:"floor"                             db:"floor"                   req:"true"  pk:""      absolute:"true" customwhere:"value->'item'->>'floor'" `
	GL_asset_reference       *string    `json:"GL_asset_reference,omitempty"      db:"gl_asset_reference"      req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'gl_asset_reference'" `
	Install_date             *time.Time `json:"install_date,omitempty"            db:"install_date"            req:""      pk:""      none:"NULL"     customwhere:"value->'item'->>'install_date'" `
	Installation_cost        *float64   `json:"installation_cost,omitempty"       db:"installation_cost"       req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'installation_cost'" `
	Invoice_no               *string    `json:"invoice_no,omitempty"              db:"invoice_no"              req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'invoice_no'" `
	Is_virtual_asset         *bool      `json:"is_virtual_asset,omitempty"        db:"is_virtual_asset"        req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'is_virtual_asset'" `
	Label_location           *string    `json:"label_location,omitempty"          db:"label_location"          req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'label_location'" `
	Latitude                 *float64   `json:"latitude,omitempty"                db:"latitude"                req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'latitude'" `
	Location                 *string    `json:"location,omitempty"                db:"location"                req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'location'" `
	Longitude                *float64   `json:"longitude,omitempty"               db:"longitude"               req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'longitude'" `
	Make                     *string    `json:"make,omitempty"                    db:"make"                    req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'make'" `
	Manufacturer             *string    `json:"manufacturer,omitempty"            db:"manufacturer"            req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'manufacturer'" `
	Model                    *string    `json:"model,omitempty"                   db:"model"                   req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'model'" `
	Owning_cost_center       *string    `json:"owning_cost_center,omitempty"      db:"owning_cost_center"      req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'owning_cost_center'" `
	Purchase_cost            *float64   `json:"purchase_cost,omitempty"           db:"purchase_cost"           req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'purchase_cost'" `
	Purchase_date            *time.Time `json:"purchase_date,omitempty"           db:"purchase_date"           req:""      pk:""      none:"NULL"     customwhere:"value->'item'->>'purchase_date'" `
	Purchase_order_no        *string    `json:"purchase_order_no,omitempty"       db:"purchase_order_no"       req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'purchase_order_no'" `
	Purchasing_cost_center   *string    `json:"purchasing_cost_center,omitempty"  db:"purchasing_cost_center"  req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'purchasing_cost_center'" `
	RFID_tag_ID              *string    `json:"RFID_tag_ID,omitempty"             db:"rfid_tag_id"             req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'rfid_tag_id'" `
	Room                     *string    `json:"room,omitempty"                    db:"room"                    req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'room'" `
	Serial                   *string    `json:"serial,omitempty"                  db:"serial"                  req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'serial'" `
	Service_agent            *string    `json:"service_agent,omitempty"           db:"service_agent"           req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'service_agent'" `
	Spare_parts              *string    `json:"spare_parts,omitempty"             db:"spare_parts"             req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'spare_parts'" `
	Spare_parts_bin_no       *string    `json:"spare_parts_bin_no,omitempty"      db:"spare_parts_bin_no"      req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'spare_parts_bin_no'" `
	Spare_parts_held         *string    `json:"spare_parts_held,omitempty"        db:"spare_parts_held"        req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'spare_parts_held'" `
	Supplier_code            *string    `json:"supplier_code,omitempty"           db:"supplier_code"           req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'supplier_code'" `
	Tech_manual_reference    *string    `json:"tech_manual_reference,omitempty"   db:"tech_manual_reference"   req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'tech_manual_reference'" `
	User_defined_fields      *string    `json:"user_defined_fields,omitempty"     db:"user_defined_fields"     req:""      pk:""      none:"NULL"     customwhere:"value->'item'->>'user_defined_fields'" `
	Warranty_period          *int       `json:"warranty_period,omitempty"         db:"warranty_period"         req:""      pk:""      none:"12"       customwhere:"value->'item'->>'warranty_period'" `
	Working_life             *int       `json:"working_life,omitempty"            db:"working_life"            req:""      pk:""      none:"NULL"     customwhere:"value->'item'->>'working_life'" `
	Ownership_status         *string    `json:"ownership_status,omitempty"        db:"ownership_status"        req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'ownership_status'" `
	Altered_date             *time.Time `json:"altered_date,omitempty"            db:"altered_date"            req:""      pk:""      none:"DEFAULT"  customwhere:"value->'item'->>'altered_date'" `
	Error                    *string    `json:"error,omitempty"          db:"error"          req:""   pk:""   none:"" customwhere:"value->'error'->>'Detail'"`
	Error_message            *string    `json:"error_message,omitempty"  db:"error_message"  req:""   pk:""   none:"" customwhere:"value->'error'->>'Message'"`
}


func UnresolvedAssets_CustomSelect() []string {
	return []string{
		"DISTINCT value->'item'->>'asset_id' AS asset_id",
		"(value->'item'->>'added_date')::timestamp AS added_date",
		"value->'item'->>'additional_details' AS additional_details",
		"value->'item'->>'asset_category' AS asset_category",
		"value->'item'->>'asset_no' AS asset_no",
		"value->'item'->>'asset_status' AS asset_status",
		"value->'item'->>'building' AS building",
		"value->'item'->>'condition_rating' AS condition_rating",
		"value->'item'->>'condition_rating_date' AS condition_rating_date",
		"value->'item'->>'contact_ID' AS contact_ID",
		"value->'item'->>'department' AS department",
		"value->'item'->>'description' AS description",
		"(value->'item'->>'disposal_cost')::numeric AS disposal_cost",
		"(value->'item'->>'disposal_date')::timestamp AS disposal_date",
		"value->'item'->>'disposal_reason' AS disposal_reason",
		"value->'item'->>'domain' AS domain",
		"value->'item'->>'finance_group_code' AS finance_group_code",
		"value->'item'->>'floor' AS floor",
		"value->'item'->>'GL_asset_reference' AS GL_asset_reference",
		"(value->'item'->>'install_date')::timestamp AS install_date",
		"(value->'item'->>'installation_cost')::numeric AS installation_cost",
		"value->'item'->>'invoice_no' AS invoice_no",
		"(value->'item'->>'is_virtual_asset')::boolean AS is_virtual_asset",
		"value->'item'->>'label_location' AS label_location",
		"value->'item'->>'latitude' AS latitude",
		"value->'item'->>'location' AS location",
		"value->'item'->>'longitude' AS longitude",
		"value->'item'->>'make' AS make",
		"value->'item'->>'manufacturer' AS manufacturer",
		"value->'item'->>'model' AS model",
		"value->'item'->>'owning_cost_center' AS owning_cost_center",
		"(value->'item'->>'purchase_cost')::numeric AS purchase_cost",
		"(value->'item'->>'purchase_date')::timestamp AS purchase_date",
		"value->'item'->>'purchase_order_no' AS purchase_order_no",
		"value->'item'->>'purchasing_cost_center' AS purchasing_cost_center",
		"value->'item'->>'RFID_tag_ID' AS RFID_tag_ID",
		"value->'item'->>'room' AS room",
		"value->'item'->>'serial' AS serial",
		"value->'item'->>'service_agent' AS service_agent",
		"value->'item'->>'spare_parts' AS spare_parts",
		"value->'item'->>'spare_parts_bin_no' AS spare_parts_bin_no",
		"value->'item'->>'spare_parts_held' AS spare_parts_held",
		"value->'item'->>'supplier_code' AS supplier_code",
		"value->'item'->>'tech_manual_reference' AS tech_manual_reference",
		"value->'item'->>'user_defined_fields' AS user_defined_fields",
		"(value->'item'->>'warranty_period')::numeric AS warranty_period",
		"(value->'item'->>'working_life')::numeric AS working_life",
		"value->'item'->>'ownership_status' AS ownership_status",
		"value->'item'->>'altered_date' AS altered_date",
		"value->'error'->>'Detail' AS error",
		"value->'error'->>'Message' AS error_message",
	}
}
