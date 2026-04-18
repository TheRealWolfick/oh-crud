package tools

import (
	"testing"
)

type testBinaryStruct struct {
	Word *string `json:"word,omitempty" db:"dbword" req:"true" pk:"true" diff:"true"`
	Value *int `json:"value,omitempty" db:"dbvalue" req:"true" none:"0"`
	IsTrue *bool `json:"istrue,omitempty" db:"dbistrue" none:"DEFAULT"`
	Something *string `json:"something,omitempty" db:"dbsomething" none:""`
	NotDBVal *string `json:"notdbval,omitempty"`
}

func TestBinarySearch(t *testing.T) {
	vals1 := []string{"Word1", "Blame", "green", "brown", "green", "brown", "blue", "red", "grand", "opening", "show", "tonight"}
	vals2 := []int{20, 50, 30, 60, 30, 10, 5, 15, 1, 2, 3, 4}
	vals3 := []bool{true, false, true, false, true, true, false, false, true, true, true, true}
	vals4 := []string{"Fox", "Trot", "Loot", "Protest", "Defend", "Sydney", "Highway", "Slam", "dont", "know", "im", "right"}

	left := make([]*testBinaryStruct, 8)
	right := make([]*testBinaryStruct, 4)

	for i := range 12 {
		if i < 4 {
			left[i] = &testBinaryStruct{
				Word: &vals1[i],
				Value: &vals2[i],
				IsTrue: &vals3[i],
				Something: &vals4[i],
			}
		} else if i < 8 {
			right[i-4] = &testBinaryStruct{
				Word: &vals1[i],
				Value: &vals2[i],
				IsTrue: &vals3[i],
				Something: &vals4[i],
			}
		} else {
			left[i-4] = &testBinaryStruct{
				Word: &vals1[i],
				Value: &vals2[i],
				IsTrue: &vals3[i],
				Something: &vals4[i],

			}
		}
	}

	t.Run("Test Binary Search", func(t *testing.T) {
		pos, found := BinarySearch("opening", left, "Word")
		if found == false || pos != 5 {
			t.Errorf("Values:%s\nPosition of 'opening': %v\nWas found: %v", DereferencedString(left), pos, found)
		}
	})

	// Function not currently used.
	t.Run("Test merge function", func(t *testing.T) {
		//diffs := DiffStructSlices(left, right)
		//t.Errorf("Diffs:\n%s", DereferencedString(diffs))
	})
}
