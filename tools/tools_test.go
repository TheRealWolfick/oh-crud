package tools

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"lotusforge.au/api-server/logging"
)

type testingStruct struct {
	Word *string `json:"word,omitempty" db:"dbword" req:"true" pk:"true"`
	Value *int `json:"value,omitempty" db:"dbvalue" req:"true" none:"0"`
	IsTrue *bool `json:"istrue,omitempty" db:"dbistrue" none:"DEFAULT"`
	Something *string `json:"something,omitempty" db:"dbsomething" none:""`
	NotDBVal *string `json:"notdbval,omitempty"`
}

func TestGettingFieldsFromStructs(t *testing.T) {
	var testStruct testingStruct
	testjson := `{"word": "a word", "value": 15, "something", "description"}`
	json.Unmarshal([]byte(testjson), &testStruct)

	t.Run("Get all the required values", func(t *testing.T) {
		req_fields := GetRequiredFields(testStruct)
		if !reflect.DeepEqual(req_fields, []string{"Word", "Value"}) {
			t.Errorf("Expected to receieve only Word and Value, recieved: %v", req_fields)
		}
	})

	t.Run("Get the primary keys", func(t *testing.T) {
		pk_fields := GetPrimaryKeys(testStruct)
		if !reflect.DeepEqual(pk_fields, []string{"Word"}) {
			t.Errorf("Expected to receieve only Word, recieved: %v", pk_fields)
		}
	})

	t.Run("Get all the db fields", func(t *testing.T) {
		db_fields := GetDatabaseFields(testStruct)
		if !reflect.DeepEqual(db_fields, []string{"Word", "Value", "IsTrue", "Something"}) {
			t.Errorf("Expected to receieve [Word, Value, IsTrue, Something] recieved: %v", db_fields)
		}
	})
}

func TestValidation(t *testing.T) {
	t.Run("Test valid string", func(t *testing.T) {
		val := "A string"
		expected := true

		is_valid := ValidateValue(reflect.String, val)
		if is_valid != expected {
			t.Errorf("Expected: %v\nRecieved: %v", expected, is_valid)
		}
	})
	t.Run("Test valid int", func(t *testing.T) {
		val := "250"
		expected := true

		is_valid := ValidateValue(reflect.Int64, val)
		if is_valid != expected {
			t.Errorf("Expected: %v\nRecieved: %v", expected, is_valid)
		}

	})
	t.Run("Test valid float", func(t *testing.T) {
		val := "252.50"
		expected := true

		is_valid := ValidateValue(reflect.Float64, val)
		if is_valid != expected {
			t.Errorf("Expected: %v\nRecieved: %v", expected, is_valid)
		}

	})
	t.Run("Test valid bool", func(t *testing.T) {
		val := "true"
		expected := true

		is_valid := ValidateValue(reflect.Bool, val)
		if is_valid != expected {
			t.Errorf("Expected: %v\nRecieved: %v", expected, is_valid)
		}

	})

	t.Run("Test invalid int", func(t *testing.T) {
		val := "Not an int"
		expected := false

		is_valid := ValidateValue(reflect.Int64, val)
		if is_valid != expected {
			t.Errorf("Expected: %v\nRecieved: %v", expected, is_valid)
		}

	})
	t.Run("Test invalid float", func(t *testing.T) {
		val := false
		expected := false

		is_valid := ValidateValue(reflect.Float64, val)
		if is_valid != expected {
			t.Errorf("Expected: %v\nRecieved: %v", expected, is_valid)
		}

	})
	t.Run("Test invalid bool", func(t *testing.T) {
		val := "maybe"
		expected := false

		is_valid := ValidateValue(reflect.Bool, val)
		if is_valid != expected {
			t.Errorf("Expected: %v\nRecieved: %v", expected, is_valid)
		}

	})
}

func TestDiffs(t *testing.T) {
	str1 := "struct"
	str2 := "struct"
	str3 := "struct"
	val1 := 50
	val2 := 40
	val3 := 50
	tru1 := true
	tru2 := false
	tru3 := true
	som1 := "Full struct"
	som3 := "Different full struct"

	struct1 := &testingStruct{
		Word: &str1,
		Value: &val1,
		IsTrue: &tru1,
		Something: &som1,
	}
	struct2 := &testingStruct{
		Word: &str2,
		Value: &val2,
		IsTrue: &tru2,
	}
	struct3 := &testingStruct{
		Word: &str3,
		Value: &val3,
		IsTrue: &tru3,
		Something: &som3,
	}

	t.Run("Test diff structs 1", func(t *testing.T) {
		expected := `{
              "comparator": "struct",
              "supplied": {
                "value": 50,
                "istrue": true,
                "something": "Full struct"
              },
              "stored": {
                "value": 40,
                "istrue": false
              }
            }`
		diffs := DiffStruct(struct1, struct2, "Word")
		if logging.PrettyJSON(diffs) != expected {
			//t.Errorf("Returned: %v", logging.PrettyJSON(diffs))
		}
	})

	t.Run("test diff structs 2", func(t *testing.T) {
		expected := `{
              "comparator": "struct",
              "supplied": {
                "something": "Full struct"
              },
              "stored": {
                "something": "Different full struct"
              }
            }`
		diffs := DiffStruct(struct1, struct3, "Word")
		if logging.PrettyJSON(diffs) != expected {
			//t.Errorf("Returned: %v", logging.PrettyJSON(diffs))
		}
	})
}

func TestSliceParsing(t *testing.T) {
	
	slice_string1 := "(brave,clean,fast)"
	slice_string2 := "brave,clean,fast"
	slice_string3 := "brave,clean,fast,"
	slice_string5 := "[turn,into,pointer]"
	slice_int1 := "[52,34,22]"
	slice_string_byte1 := []byte("[brave,clean,fast]")
	slice_int_byte1 := []byte("[52,34,22]")
	slice_any1 := []any{52, "34", 22.0}

	expected1 := []string{"brave", "clean", "fast"}
	expected3 := []string{"turn", "into", "pointer"}
	expected4 := []int{52, 34, 22}

	t.Run("Round brackets to string slice", func(t *testing.T) {
		converted, err := ParseToSlice[string](slice_string1)
		if err != nil { t.Fatalf("Unexpected error: %v", err) }
		eq := slices.Compare(converted, expected1)
		if eq != 0 { t.Errorf("Expected slices to be equal. Converted: %v  -- Expected: %v", converted, expected1) }
	})

	t.Run("no brackets to string slice", func(t *testing.T) {
		converted, err := ParseToSlice[string](slice_string2)
		if err != nil { t.Fatalf("Unexpected error: %v", err) }
		eq := slices.Compare(converted, expected1)
		if eq != 0 { t.Errorf("Expected slices to be equal. Converted: %v  -- Expected: %v", converted, expected1) }
	})

	t.Run("Trailing comma to string slice", func(t *testing.T) {
		converted, err := ParseToSlice[string](slice_string3)
		if err != nil { t.Fatalf("Unexpected error: %v", err) }
		eq := slices.Compare(converted, expected1)
		if eq != 0 { t.Errorf("Expected slices to be equal. Converted: %v  -- Expected: %v", converted, expected1) }
	})

	t.Run("Square brackets to string slice", func(t *testing.T) {
		converted, err := ParseToSlice[string](&slice_string5)
		if err != nil { t.Fatalf("Unexpected error: %v", err) }
		eq := slices.Compare(converted, expected3)
		if eq != 0 { t.Errorf("Expected slices to be equal. Converted: %v  -- Expected: %v", converted, expected3) }
	})

	t.Run("Int string to into slice", func(t *testing.T) {
		converted, err := ParseToSlice[int](slice_int1)
		if err != nil { t.Fatalf("Unexpected error: %v", err) }
		eq := slices.Compare(converted, expected4)
		if eq != 0 { t.Errorf("Expected slices to be equal. Converted: %v  -- Expected: %v", converted, expected4) }
	})

	t.Run("byte into string slice", func(t *testing.T) {
		converted, err := ParseToSlice[string](slice_string_byte1)
		if err != nil { t.Fatalf("Unexpected error: %v", err) }
		eq := slices.Compare(converted, expected1)
		if eq != 0 { t.Errorf("Expected slices to be equal. Converted: %v  -- Expected: %v", converted, expected1) }
	})

	t.Run("int byte to int slice", func(t *testing.T) {
		converted, err := ParseToSlice[int](slice_int_byte1)
		if err != nil { t.Fatalf("Unexpected error: %v", err) }
		eq := slices.Compare(converted, expected4)
		if eq != 0 { t.Errorf("Expected slices to be equal. Converted: %v  -- Expected: %v", converted, expected4) }
	})

	t.Run("Mixed any slice to int slice", func(t *testing.T) {
		converted, err := ParseToSlice[int](slice_any1)
		if err != nil { t.Fatalf("Unexpected error: %v", err) }
		eq := slices.Compare(converted, expected4)
		if eq != 0 { t.Errorf("Expected slices to be equal. Converted: %v  -- Expected: %v", converted, expected4) }
	})

	t.Run("Empty inputs return empty slice", func(t *testing.T) {
		var nilPtr *string
		for _, in := range []any{nil, "", "   ", "[]", "{}", []byte{}, nilPtr} {
			converted, err := ParseToSlice[string](in)
			if err != nil { t.Errorf("Unexpected error for %#v: %v", in, err) }
			if len(converted) != 0 { t.Errorf("Expected empty slice for %#v, got: %v", in, converted) }
		}
	})

	errorCases := []struct {
		name string
		parse func() (any, error)
	}{
		{"End bracket missing", func() (any, error) { return ParseToSlice[string]("[something,5,wrong ") }},
		{"Start bracket missing", func() (any, error) { return ParseToSlice[string]("something,5,wrong]") }},
		{"Mismatched brackets", func() (any, error) { return ParseToSlice[string]("[a,b}") }},
		{"Int string with bad element", func() (any, error) { return ParseToSlice[int]("[52,oops,22]") }},
		{"Int byte with bad element", func() (any, error) { return ParseToSlice[int]([]byte("{52,oops}")) }},
		{"Native slice with bad element", func() (any, error) { return ParseToSlice[int]([]string{"52", "oops"}) }},
		{"Map input", func() (any, error) { return ParseToSlice[string](map[string]int{"a": 1}) }},
		{"Struct input", func() (any, error) { return ParseToSlice[string](struct{ A int }{1}) }},
		{"Unsupported target type", func() (any, error) { return ParseToSlice[struct{}]("a,b") }},
	}

	for _, tc := range errorCases {
		t.Run(tc.name, func(t *testing.T) {
			converted, err := tc.parse()
			if err == nil { t.Errorf("Expected an error, got: %v", converted) }
		})
	}
}

func TestUnique(t *testing.T) {
	got := Unique([]string{"b", "a", "b", "c", "a"})
	if !slices.Equal(got, []string{"b", "a", "c"}) { t.Errorf("Expected [b a c] in first-seen order. Received: %v", got) }
}
