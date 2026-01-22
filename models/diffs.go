package models

import "time"

type Item_Diff[T any] struct {
	Comparator *string `json:"comparator"`
	Supplied *T `json:"supplied"`
	Stored *T `json:"stored"`
}

type Diff[T any] struct {
	DiffType             *string           `json:"diff_type" db:"diff_type" req:"true"`
	TaskID               *string           `json:"task_id,omitempty" db:"task_id" none:"NULL"`
	MissingFromSupplied  []T              `json:"missing_from_supplied,omitempty"  db:"missing_from_supplied" none:"NULL"`
	MissingFromStored    []T              `json:"missing_from_stored,omitempty"  db:"missing_from_stored" none:"NULL"`
	Diffs                []Item_Diff[T]   `json:"diffs,omitempty"  db:"diffs" none:"NULL"`
	UserGenerated        *string           `json:"user_generated,omitempty" db:"generated_by_user" none:"NULL"` // Changed db tag
	Checksum             *string           `json:"checksum" db:"checksum" none:"NULL"`
	Created              *time.Time        `json:"created,omitempty" db:"created" none:"DEFAULT"`
	Note                 *string           `json:"note,omitempty" db:"note" none:"NULL"`
	BatchId              *string           `json:"batch_id,omitempty" db:"batch_id" none:"DEFAULT"`
}
