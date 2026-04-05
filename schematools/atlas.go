package schematools

// schematools/atlas.go
//
// Usage:
//   gen := schematools.NewSchemaGenerator(pool)
//   gen.Approval = gate.ApprovalFuncFor(tableName)  // optional
//
//   // On startup / version bump (use BootstrapModels instead):
//   err := gen.SyncModel(ctx, model, hclPath)
//
// SyncModel will:
//   1. Generate (or regenerate) the .hcl file from the DataModel.
//   2. Apply the schema via the Atlas Go SDK against the live database.
//   3. Before applying any destructive change, call the ApprovalFunc —
//      returning an error from it aborts the apply.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"

	"ariga.io/atlas/sql/postgres"
	"ariga.io/atlas/sql/schema"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"lotusforge.au/api-server/models"
)

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// ApprovalFunc is called when Atlas detects destructive changes.
// changes is a human-readable summary. Return nil to proceed, non-nil to abort.
type ApprovalFunc func(ctx context.Context, changes string) error

// SchemaGenerator converts DataModel configs to Atlas HCL and applies them.
type SchemaGenerator struct {
	db       *sql.DB      // live database connection (derived from pgxpool)
	Approval ApprovalFunc // nil = auto-approve everything (dev mode)
}

// NewSchemaGenerator creates a SchemaGenerator from the app's pgxpool.
// The pool is not closed by the generator.
func NewSchemaGenerator(pool *pgxpool.Pool) *SchemaGenerator {
	return &SchemaGenerator{
		db: stdlib.OpenDBFromPool(pool),
	}
}

// SyncModel writes (or overwrites) the HCL file at hclPath, then applies any
// schema changes to the live database.
func (g *SchemaGenerator) SyncModel(ctx context.Context, model *models.DataModel, hclPath string) error {
	if model.Table_name == nil {
		return fmt.Errorf("model is missing table-name")
	}

	hcl, err := ModelToHCL(model)
	if err != nil {
		return fmt.Errorf("hcl generation: %w", err)
	}
	if err := os.MkdirAll(dirOf(hclPath), 0o755); err != nil {
		return fmt.Errorf("create hcl dir: %w", err)
	}
	if err := os.WriteFile(hclPath, []byte(hcl), 0o644); err != nil {
		return fmt.Errorf("write hcl: %w", err)
	}

	return g.applySchema(ctx, *model.Table_name, hcl)
}

// ---------------------------------------------------------------------------
// HCL generation  (ModelToHCL is exported so you can unit-test it)
// ---------------------------------------------------------------------------

// ModelToHCL converts a *models.DataModel into an Atlas schema HCL string (includes FKs).
// Written to disk for documentation. For Atlas apply use AllModelsToHCL (combined) or
// modelToHCL_NoFKs (per-table live-reload without cross-table references).
func ModelToHCL(m *models.DataModel) (string, error) {
	if m.Table_name == nil {
		return "", fmt.Errorf("model is missing table-name")
	}
	var sb strings.Builder
	sb.WriteString("# Auto-generated — source of truth is the YAML config. DO NOT EDIT.\n\n")
	sb.WriteString("schema \"public\" {}\n\n")
	sb.WriteString(tableBlock(m))
	return sb.String(), nil
}

// modelToHCL_NoFKs generates a single-table HCL without FK constraints.
// Used when applying a live-reload schema update in isolation — cross-table FK
// references can't be resolved without all other tables present in the same realm.
func modelToHCL_NoFKs(m *models.DataModel) (string, error) {
	if m.Table_name == nil {
		return "", fmt.Errorf("model is missing table-name")
	}
	var sb strings.Builder
	sb.WriteString("# Auto-generated — source of truth is the YAML config. DO NOT EDIT.\n\n")
	sb.WriteString("schema \"public\" {}\n\n")
	sb.WriteString(tableBlockNoFKs(m))
	return sb.String(), nil
}

// AllModelsToHCL generates a single combined HCL string containing all models.
// All tables are defined in one realm, so cross-table FK references resolve correctly.
// Use this for full-schema bootstrap syncs.
func AllModelsToHCL(ms []*models.DataModel) (string, error) {
	var sb strings.Builder
	sb.WriteString("# Auto-generated — source of truth is the YAML config. DO NOT EDIT.\n\n")
	sb.WriteString("schema \"public\" {}\n\n")
	for _, m := range ms {
		if m.Table_name == nil {
			continue
		}
		sb.WriteString(tableBlock(m))
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

// tableBlock renders the full `table "x" { … }` block, including FK constraints.
func tableBlock(m *models.DataModel) string {
	return tableBlockOpts(m, true)
}

// tableBlockNoFKs renders the table block without FK constraints.
// Used for per-table live-reload syncs where cross-table references can't be resolved.
func tableBlockNoFKs(m *models.DataModel) string {
	return tableBlockOpts(m, false)
}

func tableBlockOpts(m *models.DataModel, includeFKs bool) string {
	var sb strings.Builder
	tbl := *m.Table_name

	sb.WriteString(fmt.Sprintf("table %q {\n", tbl))
	sb.WriteString("  schema = schema.public\n\n")

	// --- columns ---
	for _, name := range orderedFields(m) {
		f := m.Fields[name]
		sb.WriteString(columnBlock(name, &f))
	}

	// --- primary key ---
	if m.Primary_key != nil && *m.Primary_key != "" {
		sb.WriteString(fmt.Sprintf("\n  primary_key {\n    columns = [column.%s]\n  }\n", *m.Primary_key))
	}

	// --- foreign keys ---
	if includeFKs && len(m.Foreign_keys) > 0 {
		for _, fkName := range sortedKeys(m.Foreign_keys) {
			fk := m.Foreign_keys[fkName]
			sb.WriteString(foreignKeyBlock(fkName, fk))
		}
	}

	// --- unique keys ---
	if len(m.Unique_keys) > 0 {
		for _, ukName := range sortedUKKeys(m.Unique_keys) {
			uk := m.Unique_keys[ukName]
			sb.WriteString(uniqueKeyBlock(ukName, uk))
		}
	}

	sb.WriteString("}\n")
	return sb.String()
}

// columnBlock renders one `column "x" { … }` block.
func columnBlock(name string, f *models.DataModelField) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("  column %q {\n", name))

	dbType := ""
	if f.DB_type != nil {
		dbType = *f.DB_type
	}
	sb.WriteString(fmt.Sprintf("    type = %s\n", hclType(dbType)))

	// Serial types are implicitly NOT NULL in PostgreSQL — emitting null=true is invalid.
	if f.Nullable != nil && *f.Nullable && !isSerialType(dbType) {
		sb.WriteString("    null = true\n")
	}

	if f.Default != nil {
		sb.WriteString(fmt.Sprintf("    default = %s\n", hclDefault(*f.Default, dbType)))
	}

	sb.WriteString("  }\n")
	return sb.String()
}

// foreignKeyBlock renders one `foreign_key "x" { … }` block.
func foreignKeyBlock(name string, fk models.ForeignKey) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n  foreign_key %q {\n", name))

	cols := make([]string, len(fk.Fields))
	for i, c := range fk.Fields {
		cols[i] = "column." + c
	}
	sb.WriteString(fmt.Sprintf("    columns     = [%s]\n", strings.Join(cols, ", ")))

	if fk.Target_table != nil {
		refCols := make([]string, len(fk.Target_fields))
		for i, c := range fk.Target_fields {
			refCols[i] = fmt.Sprintf("table.%s.column.%s", *fk.Target_table, c)
		}
		sb.WriteString(fmt.Sprintf("    ref_columns = [%s]\n", strings.Join(refCols, ", ")))
	}

	if fk.ON_UPDATE != nil {
		sb.WriteString(fmt.Sprintf("    on_update   = %s\n", hclFKAction(*fk.ON_UPDATE)))
	}
	if fk.ON_DELETE != nil {
		sb.WriteString(fmt.Sprintf("    on_delete   = %s\n", hclFKAction(*fk.ON_DELETE)))
	}

	sb.WriteString("  }\n")
	return sb.String()
}

// uniqueKeyBlock renders one `index "x" { unique = true … }` block.
func uniqueKeyBlock(name string, uk models.UniqueKey) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n  index %q {\n", name))
	sb.WriteString("    unique = true\n")

	cols := make([]string, len(uk.Fields))
	for i, c := range uk.Fields {
		cols[i] = "column." + c
	}
	sb.WriteString(fmt.Sprintf("    columns = [%s]\n", strings.Join(cols, ", ")))
	sb.WriteString("  }\n")
	return sb.String()
}

// ---------------------------------------------------------------------------
// Atlas apply (uses the Atlas Go SDK directly — no CLI subprocess)
// ---------------------------------------------------------------------------

// applyAllSchemas diffs and applies a combined HCL that spans multiple tables.
// tableNames is the set of tables to inspect from the live DB as the "current" state.
func (g *SchemaGenerator) applyAllSchemas(ctx context.Context, tableNames []string, hcl string) error {
	drv, err := postgres.Open(g.db)
	if err != nil {
		return fmt.Errorf("atlas postgres driver: %w", err)
	}

	current, err := drv.InspectSchema(ctx, "public", &schema.InspectOptions{
		Tables: tableNames,
	})
	if err != nil {
		return fmt.Errorf("inspect current schema: %w", err)
	}

	desired, err := parseHCLSchema(hcl)
	if err != nil {
		return fmt.Errorf("parse desired schema: %w", err)
	}

	changes, err := drv.SchemaDiff(current, desired)
	if err != nil {
		return fmt.Errorf("schema diff: %w", err)
	}
	if len(changes) == 0 {
		return nil
	}

	if destructive := describeDestructive(changes); destructive != "" && g.Approval != nil {
		if err := g.Approval(ctx, destructive); err != nil {
			return fmt.Errorf("apply aborted: %w", err)
		}
	}

	return drv.ApplyChanges(ctx, changes)
}

func (g *SchemaGenerator) applySchema(ctx context.Context, tableName string, hcl string) error {
	drv, err := postgres.Open(g.db)
	if err != nil {
		return fmt.Errorf("atlas postgres driver: %w", err)
	}

	// Inspect the current state of just this table.
	current, err := drv.InspectSchema(ctx, "public", &schema.InspectOptions{
		Tables: []string{tableName},
	})
	if err != nil {
		return fmt.Errorf("inspect current schema: %w", err)
	}

	// Parse the desired state from the generated HCL.
	desired, err := parseHCLSchema(hcl)
	if err != nil {
		return fmt.Errorf("parse desired schema: %w", err)
	}

	// Diff current → desired.
	changes, err := drv.SchemaDiff(current, desired)
	if err != nil {
		return fmt.Errorf("schema diff: %w", err)
	}
	if len(changes) == 0 {
		return nil // nothing to do
	}

	// Check for destructive changes and call approval if configured.
	if destructive := describeDestructive(changes); destructive != "" && g.Approval != nil {
		if err := g.Approval(ctx, destructive); err != nil {
			return fmt.Errorf("apply aborted: %w", err)
		}
	}

	return drv.ApplyChanges(ctx, changes)
}

// describeDestructive returns a human-readable description of any DROP or
// destructive ALTER operations in the change list, or "" if none.
func describeDestructive(changes []schema.Change) string {
	var lines []string
	for _, c := range changes {
		switch v := c.(type) {
		case *schema.DropTable:
			lines = append(lines, fmt.Sprintf("DROP TABLE %q", v.T.Name))
		case *schema.ModifyTable:
			for _, tc := range v.Changes {
				switch col := tc.(type) {
				case *schema.DropColumn:
					lines = append(lines, fmt.Sprintf("DROP COLUMN %q.%q", v.T.Name, col.C.Name))
				case *schema.ModifyColumn:
					if col.Change.Is(schema.ChangeType) {
						lines = append(lines,
							fmt.Sprintf("ALTER COLUMN %q.%q TYPE (was %s)",
								v.T.Name, col.From.Name, col.From.Type.Raw))
					}
				case *schema.DropIndex:
					lines = append(lines, fmt.Sprintf("DROP INDEX %q on %q", col.I.Name, v.T.Name))
				}
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// HCL parsing helper
// ---------------------------------------------------------------------------

// parseHCLSchema parses an Atlas HCL string and returns the "public" schema.
// The HCL is evaluated into a Realm first (since it contains a `schema "public" {}`
// block), then the public schema is extracted for diffing.
func parseHCLSchema(hcl string) (*schema.Schema, error) {
	parser := hclparse.NewParser()
	_, diags := parser.ParseHCL([]byte(hcl), "schema.hcl")
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse HCL: %s", diags.Error())
	}

	var realm schema.Realm
	if err := postgres.EvalHCL.Eval(parser, &realm, nil); err != nil {
		return nil, fmt.Errorf("eval HCL: %w", err)
	}

	for _, s := range realm.Schemas {
		if s.Name == "public" {
			return s, nil
		}
	}
	if len(realm.Schemas) > 0 {
		return realm.Schemas[0], nil
	}
	return nil, fmt.Errorf("no schema found in HCL")
}

// ---------------------------------------------------------------------------
// Type / default mapping helpers
// ---------------------------------------------------------------------------

func hclType(dbType string) string {
	lower := strings.ToLower(strings.TrimSpace(dbType))
	switch {
	case lower == "serial":
		return "sql(\"serial\")"
	case lower == "bigserial":
		return "sql(\"bigserial\")"
	case lower == "smallserial":
		return "sql(\"smallserial\")"
	case lower == "boolean" || lower == "bool":
		return "boolean"
	case lower == "int" || lower == "integer" || lower == "int4":
		return "int"
	case lower == "bigint" || lower == "int8":
		return "int64"
	case lower == "smallint" || lower == "int2":
		return `sql("smallint")`
	case lower == "text":
		return "text"
	case lower == "uuid":
		return "uuid"
	case lower == "jsonb":
		return "jsonb"
	case lower == "json":
		return "json"
	case lower == "timestamptz" || lower == "timestamp with time zone":
		return "timestamptz"
	case lower == "timestamp" || lower == "timestamp without time zone":
		return "timestamp"
	case lower == "date":
		return "date"
	case lower == "float4" || lower == "real":
		return "float32"
	case lower == "float8" || lower == "double precision":
		return "float64"
	default:
		// character varying(n), numeric(p,s), etc. — pass raw
		return fmt.Sprintf("sql(%q)", dbType)
	}
}

// hclDefault formats a YAML default string for HCL, inferring the literal type.
func hclDefault(raw string, dbType string) string {
	lower := strings.ToLower(strings.TrimSpace(dbType))

	if lower == "boolean" || lower == "bool" {
		if raw == "true" || raw == "false" {
			return raw
		}
	}

	if isNumericDBType(lower) {
		if isNumericLiteral(raw) {
			return raw
		}
	}

	if strings.ToLower(raw) == "null" {
		return "sql(\"NULL\")"
	}

	if looksLikeSQLExpr(raw) {
		return fmt.Sprintf("sql(%q)", raw)
	}

	return fmt.Sprintf("%q", raw)
}

func isSerialType(dbType string) bool {
	lower := strings.ToLower(strings.TrimSpace(dbType))
	return lower == "serial" || lower == "bigserial" || lower == "smallserial"
}

func isNumericDBType(lower string) bool {
	return lower == "int" || lower == "integer" || lower == "bigint" ||
		lower == "smallint" || lower == "float4" || lower == "float8" ||
		lower == "real" || lower == "double precision" ||
		strings.HasPrefix(lower, "numeric") || strings.HasPrefix(lower, "decimal")
}

func isNumericLiteral(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && c != '.' && c != '-' {
			return false
		}
	}
	return true
}

func looksLikeSQLExpr(s string) bool {
	up := strings.ToUpper(strings.TrimSpace(s))
	return strings.Contains(up, "(") ||
		up == "NOW()" ||
		up == "CURRENT_TIMESTAMP" ||
		up == "CURRENT_DATE" ||
		strings.HasPrefix(up, "GEN_RANDOM")
}

func hclFKAction(action string) string {
	switch strings.ToUpper(strings.TrimSpace(action)) {
	case "CASCADE":
		return "CASCADE"
	case "SET NULL":
		return "SET_NULL"
	case "SET DEFAULT":
		return "SET_DEFAULT"
	case "RESTRICT":
		return "RESTRICT"
	default:
		return "NO_ACTION"
	}
}

// ---------------------------------------------------------------------------
// Ordering helpers for deterministic output
// ---------------------------------------------------------------------------

// orderedFields returns field names with the primary-key column first,
// then all others alphabetically.
func orderedFields(m *models.DataModel) []string {
	pk := ""
	if m.Primary_key != nil {
		pk = *m.Primary_key
	}
	var first, rest []string
	for name := range m.Fields {
		if name == pk {
			first = append(first, name)
		} else {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	return append(first, rest...)
}

func sortedKeys(m map[string]models.ForeignKey) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedUKKeys(m map[string]models.UniqueKey) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// dirOf returns the directory portion of a file path.
func dirOf(p string) string {
	idx := strings.LastIndexAny(p, "/\\")
	if idx < 0 {
		return "."
	}
	return p[:idx]
}
