package tools

import (
	"encoding/json"
	"reflect"
	"testing"
)

type testStruct struct {
	Word string `json:"word" db:"dbword" none:"NULL"`
	Value int `json:"value" db:"dbvalue" none:0`
	IsTrue bool `json:"istrue" db:"dbistrue" none:"DEFAULT"`
	Something string `json:"something" db:"dbsomething"`
}

type testQueryableStruct struct {
	Word *string `json:"word,omitempty" db:"dbword" req:"true"`
	Value *int `json:"value,omitempty" db:"dbvalue" none:"0"`
	IsTrue *bool `json:"istrue,omitempty" db:"dbistrue" none:"DEFAULT"`
	Something *string `json:"something,omitempty" db:"dbsomething" none:""`
}

func TestStructIsEmpty(t *testing.T) {
	s := &testStruct{}
	s2 := &testStruct{Value: 5}

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

func TestQueryBuilder(t *testing.T) {
	qb := NewQueryBuilder("dbword", "primvalue")
	insertStruct := &testQueryableStruct{}
	insertData := `{"value": 50, "something": "some text"}`
	json.Unmarshal([]byte(insertData), &insertStruct)

  t.Run("Does a new query builder have updates?", func(t *testing.T) {if qb.HasUpdates() {t.Error("Expected there to be no updates")}})

	qb.Set("dbsomething" ,*insertStruct.Something)
	qb.Set("dbvalue", *insertStruct.Value)

  t.Run("Does a query builder with updates have updates?", func(t *testing.T) {if !qb.HasUpdates() {t.Error("Expected there to be updates")}})
	t.Run("Does the GetArgs return the correct values", func(t *testing.T) {
		if !reflect.DeepEqual(qb.GetArgs(), []any{"primvalue", "some text", 50}) {
			t.Errorf("Expected GetArgs to return [primvalue, some text, 50]. Returned: [%v, %v, %v]", qb.GetArgs()...)
		}
	})

	// Test bulding select and update queries
	t.Run("Build select string", func(t *testing.T) {
		if qb.BuildSelect("sometable", []string{"fieldA", "fieldB", "fieldC"}) != "SELECT fieldA, fieldB, fieldC FROM sometable WHERE dbword = $1;" {
			t.Errorf("Expected: %s\nReceived:%s", "SELECT fieldA, fieldB, fieldC FROM sometable WHERE dbword = $1;", qb.BuildSelect("sometable", []string{"fieldA", "fieldB", "fieldC"}))
		}
	})
	t.Run("Build update string", func(t *testing.T) {
		updateString := qb.BuildUpdate("sometable")
		if updateString != "UPDATE sometable SET dbvalue = $3, dbsomething = $2 WHERE dbword = $1;" && updateString != "UPDATE sometable SET dbsomething = $2, dbvalue = $3 WHERE dbword = $1;" {
			t.Errorf("Error in bulding update statement!\nReceived: '%s'", updateString)
		}
	})

	// Test insert and multi insert queries
	new1json := `{"word": "new1", "value": 5}`
	newgroupinvalidjson := `[{"word": "new1", "value": 5}, {"word": "new2", "istrue": true}, {"istrue": false, "something": "more text"}]`
	newgroupvalidjson := `[{"word": "new1", "value": 5}, {"word": "new2", "istrue": true}, {"word": "valid", "istrue": false, "something": "more text"}]`
	var new1 testQueryableStruct
	var newInvalidGroup []testQueryableStruct
	var newValidGroup []testQueryableStruct
	qb_singleValid := NewBlankQueryBuilder()
	qb_groupValid := NewBlankQueryBuilder()
	
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
		valid := ValidateMultiStruct(newInvalidGroup)
		if valid {
			t.Error("Expected object to be invalid, was valid")
		}
	})

	t.Run("Test multi insert", func(t *testing.T) { 
		valid := ValidateMultiStruct(newValidGroup)
		if !valid {
			t.Error("Expected object to be valid, was invalid")
		}

		qry := qb_groupValid.BuildMultiInsert("sometable", ToAnySlice(newValidGroup))
		vals := qb_groupValid.GetArgs()
		exp := `INSERT INTO sometable (dbword, dbvalue, dbistrue, dbsomething) VALUES ($1, $2, DEFAULT, ''), ($3, 0, $4, ''), ($5, 0, $6, $7);`
		if qry != exp {
			t.Errorf("Expected: %s\nReceived: %s\nVals: %v", exp, qry, vals)
		}
		args := qb_groupValid.GetArgsAsString()
		if args != "new1, 5, new2, true, valid, false, more text" {
			t.Errorf("Expected: new1, 5, new2, true, valid, false, more text\nReceived: %s", args)
		}


	})
}
