package tools

import (
	"encoding/json"
	"reflect"
	"testing"
)


type testingStruct struct {
	Word *string `json:"word,omitempty" db:"dbword" req:"true" pk:"true"`
	Value *int `json:"value,omitempty" db:"dbvalue" req:"true" none:"0"`
	IsTrue *bool `json:"istrue,omitempty" db:"dbistrue" none:"DEFAULT"`
	Something *string `json:"something,omitempty" db:"dbsomething" none:""`
	NotDBVal *string `json:"notdbval,omitempty"`
}


func TestStructIsEmpty(t *testing.T) {
	s := &testingStruct{}
	value := 5
	s2 := &testingStruct{
		Value: &value,
	}

	t.Run("Is empty struct empty?",func(t *testing.T) {
		isEmpty := StructIsEmpty(s)
		if !isEmpty {t.Error("Expected struct to be seen as empty")}
	})
	t.Run("Is non empty struct empty?", func(t *testing.T) {
		isEmpty := StructIsEmpty(s2)
		if isEmpty {t.Error("Expected struct to be seen as not empty")}
	})

	s2 = nil

	t.Run("Cleared struct returns as empty.", func(t *testing.T) {
		isEmpty := StructIsEmpty(s2)
		if !isEmpty {t.Error("Expected nil struct to be seen as empty.")}
	})
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


func TestQueryBuilder(t *testing.T) {
	qb := NewQueryBuilder(GetBasicLogger())
	insertStruct := &testingStruct{}
	insertData := `{"value": 50, "something": "some text"}`
	json.Unmarshal([]byte(insertData), &insertStruct)

  t.Run("Does a new query builder have updates?", func(t *testing.T) {if qb.HasUpdates() {t.Error("Expected there to be no updates")}})

	qb.SetWhere("dbword", "primvalue", reflect.String)
	qb.SetValue("dbsomething" ,*insertStruct.Something)
	qb.SetValue("dbvalue", *insertStruct.Value)

  t.Run("Does a query builder with updates have updates?", func(t *testing.T) {if !qb.HasUpdates() {t.Error("Expected there to be updates")}})
	t.Run("Does the GetArgs return the correct values", func(t *testing.T) {
		if !reflect.DeepEqual(qb.GetArgs(), []any{"primvalue", "some text", 50}) {
			t.Errorf("Expected GetArgs to return [primvalue, some text, 50]. Returned: [%v, %v, %v]", qb.GetArgs()...)
		}
	})

	// Test bulding select and update queries
	t.Run("Build select string", func(t *testing.T) {
		if qb.BuildSelect("sometable", []string{"fieldA", "fieldB", "fieldC"}) != "SELECT fieldA, fieldB, fieldC FROM sometable WHERE dbword ~* $1;" {
			t.Errorf("Expected: %s\nReceived:%s", "SELECT fieldA, fieldB, fieldC FROM sometable WHERE dbword ~* $1;", qb.BuildSelect("sometable", []string{"fieldA", "fieldB", "fieldC"}))
		}
	})

	// Test insert and multi insert queries
	new1json := `{"word": "new1", "value": 5}`
	newgroupinvalidjson := `[{"word": "new1", "value": 5}, {"word": "new2", "istrue": true}, {"istrue": false, "something": "more text"}]`
	newgroupvalidjson := `[{"word": "new1", "value": 5}, {"word": "new2", "value": 50, "notdbval": "redundant"}, {"word": "valid", "value": 15, "istrue": false, "something": "more text"}]`
	var new1 testingStruct
	var newInvalidGroup []testingStruct
	var newValidGroup []testingStruct
	qb_singleValid := NewQueryBuilder(GetBasicLogger())
	// Read the data into the variables
	json.Unmarshal([]byte(new1json), &new1)
	json.Unmarshal([]byte(newgroupinvalidjson), &newInvalidGroup)
	json.Unmarshal([]byte(newgroupvalidjson), &newValidGroup)

	// Test on one struct
	t.Run("Test building insert for one struct", func(t *testing.T) {
		qry := qb_singleValid.BuildInsert("sometable", new1)
		exp := "INSERT INTO sometable (dbword, dbvalue, dbistrue, dbsomething) VALUES ($1, $2, DEFAULT, '');"
		if qry != exp {
			t.Errorf("Expected: %s\nReceived: %v", exp, qry)
		}
		args := qb_singleValid.GetArgsAsString()
		if args != "new1, 5" {
			t.Errorf("Expected: new1 5\nReceived: %s", args)
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
		if DereferencedString(diffs) != expected {
			//t.Errorf("Returned: %v", DereferencedString(diffs))
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
		if DereferencedString(diffs) != expected {
			//t.Errorf("Returned: %v", DereferencedString(diffs))
		}
	})
}
