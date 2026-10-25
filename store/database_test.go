package store

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"lotusforge.au/api-server/models/modelstest"
)

// Testing scripts

func TestUpdate(t *testing.T) {
	// Setup
	fake_db := &modelstest.FakeDB{}
	model := basicModel()
	values_a := map[string]any{ "code": "foo", "name": "bar", "count": 5 }
	values_b := map[string]any{	"code": "foobar", "name": "barst", "count": 3 }
	values_c := map[string]any{ "code": "hill", "count": 223 }
	values_wrong := map[string]any{	"count": 0 }

	values_all := []map[string]any{values_a, values_b, values_c}
	values_all_with_wrong := []map[string]any{values_a, values_b, values_wrong}

	// Test a single insert
	t.Run("Update a single value", func(t *testing.T) {
		expected_sql := "UPDATE test_table SET name = $2, count = $3 WHERE code = $1;"
		expected_args := "[foo bar 5]"
		multiUpdate(context.Background(), fake_db, &model, []map[string]any{values_a})
		if (!strings.Contains(fake_db.LastCall().SQL, "name") || !strings.Contains(fake_db.LastCall().SQL, "count") || !strings.Contains(fake_db.LastCall().SQL, "WHERE code")) {
			t.Errorf("Expected Sql similar to: %s\nReceived Sql: %s", expected_sql, fake_db.LastCall().SQL)
		}
		if (len(fake_db.LastCall().Args) != 3) {
			t.Errorf("Expected args: %s\nReceived args: %s", expected_args, fmt.Sprint(fake_db.LastCall().Args))
		}
	})

	t.Run("Update multiple values", func(t *testing.T) {
		expected_sql := "UPDATE test_table SET name = $2, count = $3 WHERE code = $1;"
		expected_args := "[foo bar 5]"
		multiUpdate(context.Background(), fake_db, &model, values_all)
		// The values in the fake db should now be the last item, which has only two items
		if (!strings.Contains(fake_db.LastCall().SQL, "count") || !strings.Contains(fake_db.LastCall().SQL, "WHERE code")) {
			t.Errorf("Expected Sql: %s\nReceived Sql: %s", expected_sql, fake_db.LastCall().SQL)
		}
		if (len(fake_db.LastCall().Args) != 2) {
			t.Errorf("Expected args: %s\nReceived args: %s - len: %v", expected_args, fmt.Sprint(fake_db.LastCall().Args), len(fake_db.LastCall().Args))
		}
	})

	t.Run("Update multiple values - final value invalid", func(t *testing.T) {
		expected_sql := "UPDATE test_table SET name = $2, count = $3 WHERE code = $1;"
		expected_args := "[foo bar 5]"
		multiUpdate(context.Background(), fake_db, &model, values_all_with_wrong)
		// The values in the fake db should now be the second last item, which has three items
		if (!strings.Contains(fake_db.LastCall().SQL, "name") || !strings.Contains(fake_db.LastCall().SQL, "count") || !strings.Contains(fake_db.LastCall().SQL, "WHERE code")) {
			t.Errorf("Expected Sql: %s\nReceived Sql: %s", expected_sql, fake_db.LastCall().SQL)
		}
		if (len(fake_db.LastCall().Args) != 3) {
			t.Errorf("Expected args: %s\nReceived args: %s - len: %v", expected_args, fmt.Sprint(fake_db.LastCall().Args), len(fake_db.LastCall().Args))
		}
	})
}
