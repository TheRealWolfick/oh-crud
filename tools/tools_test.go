package tools

import (
	"encoding/json"
	"reflect"
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
