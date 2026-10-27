package schematools

import (
	"strings"
	"testing"

	"ariga.io/atlas/sql/postgres"
	"ariga.io/atlas/sql/schema"
	"lotusforge.au/api-server/models"
)

func indexTestModel() *models.DataModel {
	return &models.DataModel{
		Table_name:  strp("refresh_tokens"),
		Primary_key: strp("id"),
		Fields: map[string]models.DataModelField{
			"id":         {DB_type: strp("bigserial")},
			"user_id":    {DB_type: strp("text")},
			"family_id":  {DB_type: strp("text")},
			"revoked_at": {DB_type: strp("timestamptz"), Nullable: boolp(true)},
		},
		Indexes: map[string]models.Index{
			"idx_refresh_tokens_user":   {Fields: []string{"user_id"}, Where: strp("revoked_at IS NULL")},
			"idx_refresh_tokens_family": {Fields: []string{"family_id"}, Type: strp("hash")},
		},
	}
}

func boolp(b bool) *bool { return &b }

// TestModelToHCL_Indexes confirms indexes render as non-unique Atlas index blocks
// and that Atlas parses the type and partial-index predicate back out.
func TestModelToHCL_Indexes(t *testing.T) {
	hcl, err := ModelToHCL(indexTestModel())
	if err != nil {
		t.Fatalf("ModelToHCL: %v", err)
	}
	for _, want := range []string{
		`index "idx_refresh_tokens_family"`,
		`type    = HASH`,
		`where   = "revoked_at IS NULL"`,
	} {
		if !strings.Contains(hcl, want) {
			t.Errorf("HCL missing %q:\n%s", want, hcl)
		}
	}

	s, err := parseHCLSchema(hcl)
	if err != nil {
		t.Fatalf("parseHCLSchema: %v\n%s", err, hcl)
	}
	tbl, ok := s.Table("refresh_tokens")
	if !ok {
		t.Fatal("table refresh_tokens not found in parsed schema")
	}

	user, ok := tbl.Index("idx_refresh_tokens_user")
	if !ok {
		t.Fatal("idx_refresh_tokens_user not found")
	}
	if user.Unique {
		t.Error("idx_refresh_tokens_user parsed as unique, want non-unique")
	}
	var pred postgres.IndexPredicate
	if !hasAttr(user.Attrs, &pred) || pred.P != "revoked_at IS NULL" {
		t.Errorf("idx_refresh_tokens_user predicate = %q, want %q", pred.P, "revoked_at IS NULL")
	}

	family, ok := tbl.Index("idx_refresh_tokens_family")
	if !ok {
		t.Fatal("idx_refresh_tokens_family not found")
	}
	var typ postgres.IndexType
	if !hasAttr(family.Attrs, &typ) || typ.T != "HASH" {
		t.Errorf("idx_refresh_tokens_family type = %q, want HASH", typ.T)
	}
}

// hasAttr copies the first attr matching target's type into target.
func hasAttr(attrs []schema.Attr, target schema.Attr) bool {
	for _, a := range attrs {
		switch t := target.(type) {
		case *postgres.IndexPredicate:
			if v, ok := a.(*postgres.IndexPredicate); ok {
				*t = *v
				return true
			}
		case *postgres.IndexType:
			if v, ok := a.(*postgres.IndexType); ok {
				*t = *v
				return true
			}
		}
	}
	return false
}
