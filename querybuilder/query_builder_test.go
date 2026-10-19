package querybuilder

import (
	"encoding/json"
	"reflect"
	"testing"

	"lotusforge.au/api-server/datamodel"
	"lotusforge.au/api-server/logging"
)

type testingStruct struct {
	Word *string `json:"word,omitempty" db:"dbword" req:"true" pk:"true"`
	Value *int `json:"value,omitempty" db:"dbvalue" req:"true" none:"0"`
	IsTrue *bool `json:"istrue,omitempty" db:"dbistrue" none:"DEFAULT"`
	Something *string `json:"something,omitempty" db:"dbsomething" none:""`
	NotDBVal *string `json:"notdbval,omitempty"`
}

func TestQueryBuilder(t *testing.T) {
	qb := NewQueryBuilder(logging.Basic())
	insertStruct := &testingStruct{}
	insertData := `{"value": 50, "something": "some text"}`
	json.Unmarshal([]byte(insertData), &insertStruct)

  t.Run("Does a new query builder have updates?", func(t *testing.T) {if qb.HasUpdates() {t.Error("Expected there to be no updates")}})

	qb.SetWhere("dbword", "primvalue", datamodel.FieldString)
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
	qb_singleValid := NewQueryBuilder(logging.Basic())
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
