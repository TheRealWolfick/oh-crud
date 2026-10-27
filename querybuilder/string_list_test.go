package querybuilder

import (
	"reflect"
	"strings"
	"testing"

	"lotusforge.au/api-server/datamodel"
	"lotusforge.au/api-server/logging"
	"lotusforge.au/api-server/models"
)

func stringListTestModel() *models.DataModel {
	return &models.DataModel{
		Name:        ptr("posts"),
		Version:     ptr("1.0.0"),
		Table_name:  ptr("posts"),
		End_point:   ptr("posts"),
		Primary_key: ptr("id"),
		Fields: map[string]models.DataModelField{
			"id":   {Type: ptr("int"), JSON: ptr("id"), DB_type: ptr("serial")},
			"tags": {Type: ptr("[]string"), JSON: ptr("tags"), DB_type: ptr("text[]")},
		},
	}
}

func TestStringListWhere(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		wantSQL string
		wantArg []string
	}{
		{"default matches any", "[a,b]", "SELECT id FROM posts WHERE tags && $1;", []string{"a", "b"}},
		{"~ matches any", "~a,b", "SELECT id FROM posts WHERE tags && $1;", []string{"a", "b"}},
		{"! matches all", "![a,b]", "SELECT id FROM posts WHERE tags @> $1;", []string{"a", "b"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			qb := NewQueryBuilder(logging.Basic())
			qb.SetWhere("tags", c.value, datamodel.FieldStringList)

			if got := qb.BuildSelect("posts", []string{"id"}); got != c.wantSQL {
				t.Errorf("Expected: %s\nReceived: %s", c.wantSQL, got)
			}
			if !reflect.DeepEqual(qb.GetArgs(), []any{c.wantArg}) {
				t.Errorf("Expected args to be the bound []string %v. Received: %#v", c.wantArg, qb.GetArgs())
			}
		})
	}

	t.Run("malformed list adds no where clause", func(t *testing.T) {
		qb := NewQueryBuilder(logging.Basic())
		qb.SetWhere("tags", "[a,b", datamodel.FieldStringList)
		if got := qb.BuildSelect("posts", []string{"id"}); strings.Contains(got, "WHERE") {
			t.Errorf("Expected no WHERE clause. Received: %s", got)
		}
	})
}

func TestStringListUpdate(t *testing.T) {
	cfg := stringListTestModel()

	t.Run("plain list overwrites", func(t *testing.T) {
		qb := NewQueryBuilder(logging.Basic())
		qb.SetWhereAbsolute("id", 12)
		qb.SetValue("tags", []string{"a", "b"})

		exp := "UPDATE posts SET tags = $2 WHERE id = $1;"
		if got := qb.BuildUpdate(cfg); got != exp {
			t.Errorf("Expected: %s\nReceived: %s", exp, got)
		}
		if len(qb.ListPatchedFields()) != 0 {
			t.Errorf("Expected no patched fields. Received: %v", qb.ListPatchedFields())
		}
	})

	t.Run("add/remove patch is applied in SQL", func(t *testing.T) {
		qb := NewQueryBuilder(logging.Basic())
		qb.SetWhereAbsolute("id", 12)
		qb.SetValue("tags", datamodel.StringListPatch{Add: []string{"urgent"}, Remove: []string{"draft"}})

		exp := "UPDATE posts SET tags = " +
			"ARRAY(SELECT v FROM unnest(COALESCE(tags, '{}') || " +
			"ARRAY(SELECT a FROM unnest($2::text[]) WITH ORDINALITY AS l_add(a, i) WHERE a <> ALL(COALESCE(tags, '{}')) ORDER BY i)" +
			") WITH ORDINALITY AS l_cur(v, i) WHERE v <> ALL($3::text[]) ORDER BY i)" +
			" WHERE id = $1 RETURNING tags;"
		if got := qb.BuildUpdate(cfg); got != exp {
			t.Errorf("Expected: %s\nReceived: %s", exp, got)
		}
		wantArgs := []any{12, []string{"urgent"}, []string{"draft"}}
		if !reflect.DeepEqual(qb.GetArgs(), wantArgs) {
			t.Errorf("Expected args: %#v\nReceived: %#v", wantArgs, qb.GetArgs())
		}
		if !reflect.DeepEqual(qb.ListPatchedFields(), []string{"tags"}) {
			t.Errorf("Expected patched fields [tags]. Received: %v", qb.ListPatchedFields())
		}
	})
}

func TestStringListInsertPatch(t *testing.T) {
	qb := NewQueryBuilder(logging.Basic())
	qb.BuildMultiInsert(stringListTestModel(), []map[string]any{
		{"id": 1, "tags": datamodel.StringListPatch{Add: []string{"a"}, Remove: []string{"b"}}},
	})

	found := false
	for _, arg := range qb.GetArgs() {
		if _, isPatch := arg.(datamodel.StringListPatch); isPatch {
			t.Fatalf("Expected the patch to be bound as its add list. Args: %#v", qb.GetArgs())
		}
		if list, ok := arg.([]string); ok {
			found = true
			if !reflect.DeepEqual(list, []string{"a"}) { t.Errorf("Expected tags to be inserted as [a]. Received: %v", list) }
		}
	}
	if !found { t.Errorf("Expected tags to be bound as a []string. Args: %#v", qb.GetArgs()) }
}
