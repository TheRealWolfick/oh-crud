package tools

import (
	"encoding/json"
	"reflect"
	"testing"
)

type testStruct struct {
	Word string `json:"jsonword" db:"dbword" none:"NULL"`
	Value int `json:"jsonvalue" db:"dbvalue" none:0`
	IsTrue bool `json:"jsonistrue" db:"dbistrue" none:"DEFAULT"`
	Something string `json:"jsonsomething" db:"dbsomething"`
}

type testQueryableStruct struct {
	Word *string `json:"jsonword,omitempty" db:"dbword" none:"NULL"`
	Value *int `json:"jsonvalue,omitempty" db:"dbvalue" none:"0"`
	IsTrue *bool `json:"jsonistrue,omitempty" db:"dbistrue" none:"DEFAULT"`
	Something *string `json:"jsonsomething,omitempty" db:"dbsomething" none:""`
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
	insertData := `{"jsonvalue": 50, "jsonsomething": "some text"}`
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
	new1json := `{"jsonword": "new1", "jsonvalue": 5}`
	newgroupjson := `[{"jsonword": "new1", "jsonvalue": 5}, {"jsonword": "new2", "jsonistrue": true}, {"jsonistrue": false, "jsonsomething": "more text"}]`
	var new1 testQueryableStruct
	var newGroup []testQueryableStruct
	qb2 := NewQueryBuilder("primkey", "oops")
	
	json.Unmarshal([]byte(new1json), &new1)
	json.Unmarshal([]byte(newgroupjson), &newGroup)

	t.Run("Test building insert for one struct", func(t *testing.T) {
		qry := qb2.BuildInsert("sometable", new1)
		exp := "INSERT INTO sometable (dbword, dbvalue, dbistrue, dbsomething) VALUES ($1, $2, DEFAULT, '');"
		if qry != exp {
			t.Errorf("Expected: %s\nReceived: %s", exp, qry)
		}
	})
	t.Run("Test multi insert", func(t *testing.T) {
		qry := qb2.BuildMultiInsert("sometable", ToAnySlice(newGroup))
		vals := qb2.GetArgs()
		exp := `INSERT INTO sometable (dbword, dbvalue, dbistrue, dbsomething) VALUES ($1, $2, DEFAULT, ''), ($3, 0, $4, ''), (NULL, 0, $5, $6);`
		if qry != exp {
			t.Errorf("Expected: %s\nReceived: %s\nVals: %v", exp, qry, vals)
		}
	})
}
