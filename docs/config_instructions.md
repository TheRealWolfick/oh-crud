# Config Instructions

## Base Model
This is the default model used. It is formatted with the following variables

### Top Level
**metadata**
name:                   A unique name to identify the model and is used in debug msgs and hcl filenames
version:                The version of a file in the form of `x.x.x`. This must be increased during any update
table-name:             The name of the database table to use/create
end-point:              The end point to listen against

**flags/controls**
track-history:          Boolean flag to track all changes. By default, it will use the primary key as the field to compare changes for all other fields
soft-delete:            Boolean flag to force all deletes to utilize the protected field "deleted_flag" to indicate deletion without deleting the row.
allow-diff:             Boolean flag to indicate whether this table allows diffs to be compared against itself.
end-points-allowed:     An object for controlling the methods allowed on this end point and who can utilize them via RBAC.

**connections**
web-hooks:              This is an object specifying urls to send notifications to on events for this end point

**identifiers**
primary-key:            The field which is the primary key for the table
foreign-keys:           An object specifying any foreign keys, including their cascade behaviour
unique-keys:            An object specifying any unique keys
track-history-field:    If track history is true, this field can be used to overwrite which field is treated as the primary key for history tracking.
diff-comparator:        Must be specified when allow-diff is true. It specifies which field is used to match records for diffs.

**specifications**
fields:                 Object fields used to define each column in the table

### Obects
#### End Points allowed
This is a list of all allowed end points, along with a sub list of the allowed roles. Admin has access to all roles regardless

Allowed end points: GET, POST, POST-GROUP, PUT, PUT-GROUP, DELETE, DELETE-GROUP

#### Webhooks
Webhooks are structured by trigger objects. Valid triggers are: on-get, on-update, on-delete, on-insert, on-any

Within each trigger is a list of event statuses with the urls to pass event notifications to. Refer to events.md for full information on events. Valid events for this list are: queued, start, success, warn, failed, error, all

The on-all trigger and all status are used to apply a webhook to every other event.

#### Foreign Keys
Foreign keys is a collection of foreign key objects.

**Internal fields**
foreign-key-fields:         A list of fields in this table that form the foreign key
foreign-key-target-table:   The table the foreign key is referencing
foreign-key-target-fields:  A list of the fields the foreign key is referencing on the target table
foreign-key-on-update:      What should the field do if the target value is updated*
foreign-key-on-delete:      What should the field do if the target value is deleted*
\* Valid actions are SET NULL, CASCADE, SET DEFAULT, RESTRICT, NO ACTION

#### Unique Keys
Unique keys are a collection of objects definining the unique constraints for fields on a table

Eack unique key is a list of fields which must be unique. This can be one or more fields.

#### Fields
By large the largest object of the config, this is a definition of each column, or field, in the database

**Required Fields**
type:               The type of field it is for internal usage (valid: int, float, bool, json, string, time, uuid)
db-type:            The type of field it is in the database (i.e. character varying(255), timestamp without time zone, smallint, numeric(10,2))

**Metadata**
json:               The json key value that is used to parse this value from incoming data, and map it to for outgoing data. Remove this field, or set to blank to prevent a user from being able to set or query it, such as confidential data.
json-alias:         A list of optional json keys that can also be used instead of the json value for incoming data.
default:            The default value if one was not supplied.

**Flags/controls**
include-in-diff:    True by default, but can be set to false to exclude this column from any diff comparisons
nullable:           False by default. Set to allow this column to be null
skip-insert:        False by default. Set true to never insert this field into the database (i.e an index column)
private:            False by default. Set true to prevent this field from ever being returned to the end user or being processed in an event
required-on-insert: False by default: Set true to ensure that this field is supplied. Any insert without it will be deemed invalid
absolute-match:     False by default: Set true to ensure all comparisons are done via `=` and not the regex `~*` comparator
rules:              A collection of rules for field validation. Valid rules, depending on the data type, are: min, max, pattern, enum, max-length
migration:          How Atlas should handle migration: valid, skip or recreate
