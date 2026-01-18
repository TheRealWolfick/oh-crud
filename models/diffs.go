package models

type Item_Diff[T any] struct {
	Comparator *string `json:"comparator"`
	Supplied *T `json:"supplied"`
	Stored *T `json:"stored"`
}

type Diff[T any] struct {
	DiffType             *string           `json:"diff_type" db:"diff_type" req:"true"`
	TaskID               *string           `json:"task_id,omitempty" db:"task_id" none:"NULL"`
	MissingFromSupplied  []T              `json:"missing_from_supplied"  db:"missing_from_supplied" none:"NULL"`
	MissingFromStored    []T              `json:"missing_from_stored"  db:"missing_from_stored" none:"NULL"`
	Diffs                []Item_Diff[T]   `json:"diffs"  db:"diffs" none:"NULL"`
	UserGenerated        *string           `json:"-" db:"username" none:"NULL"`
	Checksum             *string           `json:"checksum" db:"checksum" none:"NULL"`
}


