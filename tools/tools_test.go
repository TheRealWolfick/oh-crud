package tools

import (
	"encoding/json"
	"net/http"
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
	qb := NewQueryBuilder()
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
	qb_singleValid := NewQueryBuilder()
	qb_groupValid := NewQueryBuilder()
	
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

	t.Run("Test multi insert - Invalid", func(t *testing.T) {
		valid, invalid := ValidateMultiStruct(newInvalidGroup)
		if len(valid) != 1 || len(invalid) != 2 {
			t.Error("Expected object to be invalid, was valid")
		}
	})

	t.Run("Test multi insert - valid", func(t *testing.T) { 
		valid, invalid := ValidateMultiStruct(newValidGroup)
		if len(valid) != 3 {
			t.Errorf("Expected object to be valid, was invalid. Invalid entries: %v", invalid)
		}

		qry := qb_groupValid.BuildMultiInsert("sometable", ToAnySlice(newValidGroup))
		vals := qb_groupValid.GetArgs()
		exp := `INSERT INTO sometable (dbword, dbvalue, dbistrue, dbsomething) VALUES ($1, $2, DEFAULT, ''), ($3, $4, DEFAULT, ''), ($5, $6, $7, $8);`
		if qry != exp {
			t.Errorf("Expected: %s\nReceived: %s\nVals: %v", exp, qry, vals)
		}
		args := qb_groupValid.GetArgsAsString()
		if args != "new1, 5, new2, 50, valid, 15, false, more text" {
			t.Errorf("Expected: new1, 5, new2, 50, valid, 15, false, more text\nReceived: %s", args)
		}


	})
}


func TestSetValueFromStruct(t *testing.T) {
	t.Run("Set values from struct with non-nil pointer fields", func(t *testing.T) {
		qb := NewQueryBuilder()
		word := "test"
		value := 42
		isTrue := true
		something := "description"
		
		testStruct := &testingStruct{
			Word:      &word,
			Value:     &value,
			IsTrue:    &isTrue,
			Something: &something,
		}
		
		SetValueFromStruct(qb, testStruct)
		
		args := qb.GetArgs()
		expected := []any{"test", 42, true, "description"}
		
		if !reflect.DeepEqual(args, expected) {
			t.Errorf("Expected args: %v\nReceived: %v", expected, args)
		}
	})
	
	t.Run("Skip nil pointer fields", func(t *testing.T) {
		qb := NewQueryBuilder()
		value := 42
		
		testStruct := &testingStruct{
			Word:      nil,
			Value:     &value,
			IsTrue:    nil,
			Something: nil,
		}
		
		SetValueFromStruct(qb, testStruct)
		
		args := qb.GetArgs()
		expected := []any{42}
		
		if !reflect.DeepEqual(args, expected) {
			t.Errorf("Expected only Value to be set: %v\nReceived: %v", expected, args)
		}
	})
	
	t.Run("Skip fields without db tag", func(t *testing.T) {
		qb := NewQueryBuilder()
		word := "test"
		value := 42
		notdbval := "should be skipped"
		
		testStruct := &testingStruct{
			Word:     &word,
			Value:    &value,
			NotDBVal: &notdbval,
		}
		
		SetValueFromStruct(qb, testStruct)
		
		args := qb.GetArgs()
		expected := []any{"test", 42}
		
		if !reflect.DeepEqual(args, expected) {
			t.Errorf("Expected NotDBVal to be skipped: %v\nReceived: %v", expected, args)
		}
	})
	
	
	t.Run("Handle empty struct", func(t *testing.T) {
		qb := NewQueryBuilder()
		
		testStruct := &testingStruct{}
		
		SetValueFromStruct(qb, testStruct)
		
		args := qb.GetArgs()
		
		if len(args) != 0 {
			t.Errorf("Expected no values from empty struct\nReceived: %v", args)
		}
	})
}



func TestSetFromURL(t *testing.T) {
	t.Run("Set where clauses from valid URL parameters", func(t *testing.T) {
		qb := NewQueryBuilder()
		
		req, _ := http.NewRequest("GET", "/?dbword=search&dbvalue=25&dbistrue=true", nil)
		
		model := &testingStruct{}
		err := SetWhereFromURL(qb, req, model)
		
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		
		args := qb.GetArgs()
		expected := []any{"search", int64(25), true}
		
		if !reflect.DeepEqual(args, expected) {
			t.Errorf("Expected args: %v\nReceived: %v", expected, args)
		}
	})
	
	t.Run("Skip invalid integer values", func(t *testing.T) {
		qb := NewQueryBuilder()
		
		req, _ := http.NewRequest("GET", "/?dbword=search&dbvalue=notanumber", nil)
		
		model := &testingStruct{}
		err := SetWhereFromURL(qb, req, model)
		
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		
		args := qb.GetArgs()
		expected := []any{"search"}
		
		if !reflect.DeepEqual(args, expected) {
			t.Errorf("Expected invalid integer to be skipped: %v\nReceived: %v", expected, args)
		}
	})
	
	t.Run("Skip invalid boolean values", func(t *testing.T) {
		qb := NewQueryBuilder()
		
		req, _ := http.NewRequest("GET", "/?dbword=test&dbistrue=notabool", nil)
		
		model := &testingStruct{}
		err := SetWhereFromURL(qb, req, model)
		
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		
		args := qb.GetArgs()
		expected := []any{"test"}
		
		if !reflect.DeepEqual(args, expected) {
			t.Errorf("Expected invalid boolean to be skipped: %v\nReceived: %v", expected, args)
		}
	})
	
	t.Run("Handle empty query parameters", func(t *testing.T) {
		qb := NewQueryBuilder()
		
		req, _ := http.NewRequest("GET", "/", nil)
		
		model := &testingStruct{}
		err := SetWhereFromURL(qb, req, model)
		
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		
		args := qb.GetArgs()
		
		if len(args) != 0 {
			t.Errorf("Expected no args from empty query\nReceived: %v", args)
		}
	})
	
	t.Run("Skip fields without db tag", func(t *testing.T) {
		qb := NewQueryBuilder()
		
		req, _ := http.NewRequest("GET", "/?dbword=test&notdbval=shouldskip", nil)
		
		model := &testingStruct{}
		err := SetWhereFromURL(qb, req, model)
		
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		
		args := qb.GetArgs()
		expected := []any{"test"}
		
		if !reflect.DeepEqual(args, expected) {
			t.Errorf("Expected field without db tag to be skipped: %v\nReceived: %v", expected, args)
		}
	})
	
	t.Run("Handle multiple valid parameters", func(t *testing.T) {
		qb := NewQueryBuilder()
		
		req, _ := http.NewRequest("GET", "/?dbword=search&dbvalue=100&dbistrue=false&dbsomething=text", nil)
		
		model := &testingStruct{}
		err := SetWhereFromURL(qb, req, model)
		
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		
		args := qb.GetArgs()
		expected := []any{"search", int64(100), false, "text"}
		
		if !reflect.DeepEqual(args, expected) {
			t.Errorf("Expected all valid parameters: %v\nReceived: %v", expected, args)
		}
	})
	
	t.Run("Skip empty parameter values", func(t *testing.T) {
		qb := NewQueryBuilder()
		
		req, _ := http.NewRequest("GET", "/?dbword=search&dbvalue=&dbsomething=text", nil)
		
		model := &testingStruct{}
		err := SetWhereFromURL(qb, req, model)
		
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		
		args := qb.GetArgs()
		expected := []any{"search", "text"}
		
		if !reflect.DeepEqual(args, expected) {
			t.Errorf("Expected empty values to be skipped: %v\nReceived: %v", expected, args)
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
