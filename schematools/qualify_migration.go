package schematools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"lotusforge.au/api-server/models"
)

// Tables are named <service>_<table-name> (see models.QualifiedName). A database built
// before that has the bare names. Atlas only diffs the tables it is told about, so left
// alone it would see each qualified table as new and create an empty one beside the old.
// MigrateToQualifiedNames renames the old tables, and everything named after them, in
// place before the schema sync runs, so the sync finds nothing to change.

// catalogEntry is one object in the public schema
type catalogEntry struct {
	Kind  string // "r" table, "i" index, "S" sequence, "f" foreign key constraint
	Name  string
	Table string // the table an index, sequence or constraint belongs to; "" for tables
}

// catalogSQL lists the tables, indexes, owned sequences and foreign keys in public
const catalogSQL = `
SELECT 'r' AS kind, c.relname AS name, '' AS tbl
  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')
UNION ALL
SELECT 'i', ic.relname, tc.relname
  FROM pg_index x
  JOIN pg_class ic ON ic.oid = x.indexrelid
  JOIN pg_class tc ON tc.oid = x.indrelid
  JOIN pg_namespace n ON n.oid = tc.relnamespace
 WHERE n.nspname = 'public'
UNION ALL
SELECT 'S', s.relname, t.relname
  FROM pg_class s
  JOIN pg_depend d ON d.objid = s.oid AND d.classid = 'pg_class'::regclass AND d.deptype IN ('a', 'i')
  JOIN pg_class t ON t.oid = d.refobjid
  JOIN pg_namespace n ON n.oid = s.relnamespace
 WHERE s.relkind = 'S' AND n.nspname = 'public'
UNION ALL
SELECT 'f', con.conname, t.relname
  FROM pg_constraint con
  JOIN pg_class t ON t.oid = con.conrelid
  JOIN pg_namespace n ON n.oid = t.relnamespace
 WHERE con.contype = 'f' AND n.nspname = 'public'`

// renamePlan is what one migration run will do
type renamePlan struct {
	Statements []string     // DDL and data fix-ups, run in order in one transaction
	Renamed    [][2]string  // short and qualified table names of every table renamed
	Skipped    []string     // why a model could not be migrated
}

// planQualifiedRenames works out the renames for every loaded model whose bare table
// still exists. It is pure, so it can be tested without a database.
func planQualifiedRenames(catalog []catalogEntry, loaded []models.DataModel) renamePlan {
	exists := map[string]bool{}      // "<kind>:<name>"
	owned := map[string][]catalogEntry{} // table -> its indexes, sequences and constraints
	for _, e := range catalog {
		exists[e.Kind+":"+e.Name] = true
		if e.Table != "" { owned[e.Table] = append(owned[e.Table], e) }
	}
	// Index and sequence names share one namespace in PostgreSQL
	relationTaken := func(name string) bool { return exists["r:"+name] || exists["i:"+name] || exists["S:"+name] }

	var plan renamePlan
	var ddl []string
	rename := func(kind string, old string, new string) {
		ddl = append(ddl, fmt.Sprintf("ALTER %s %s RENAME TO %s", kind, ident(old), ident(new)))
		exists[map[string]string{"TABLE": "r", "INDEX": "i", "SEQUENCE": "S"}[kind]+":"+new] = true
	}

	for i := range loaded {
		m := &loaded[i]
		if m.Short_table_name == nil || m.Table_name == nil || m.Service == nil { continue }
		short, table, svc := *m.Short_table_name, *m.Table_name, *m.Service
		if short == table || !exists["r:"+short] { continue }
		if exists["r:"+table] {
			plan.Skipped = append(plan.Skipped, fmt.Sprintf("%s: both %q and %q exist, so neither was renamed. Move the data across by hand", table, short, table))
			continue
		}

		// The YAML names of the model's unique keys and indexes, before the service prefix
		yamlIndex := map[string]string{}
		for name := range m.Unique_keys { yamlIndex[strings.TrimPrefix(name, svc+"_")] = name }
		for name := range m.Indexes { yamlIndex[strings.TrimPrefix(name, svc+"_")] = name }

		tables := [][2]string{{short, table}}
		if m.Track_history != nil && *m.Track_history && exists["r:"+short+"_history"] {
			tables = append(tables, [2]string{short + "_history", table + "_history"})
		}

		for _, t := range tables {
			oldT, newT := t[0], t[1]
			rename("TABLE", oldT, newT)

			for _, e := range owned[oldT] {
				var target string
				switch e.Kind {
				case "i":
					// Named in the YAML: gets the service prefix. Named after the table
					// (the primary key, say): follows the table.
					if q, ok := yamlIndex[e.Name]; ok {
						target = q
					} else if rest, ok := strings.CutPrefix(e.Name, oldT+"_"); ok {
						target = newT + "_" + rest
					}
				case "S":
					if rest, ok := strings.CutPrefix(e.Name, oldT+"_"); ok { target = newT + "_" + rest }
				case "f":
					// Only the history table's foreign keys are named after the table
					if rest, ok := strings.CutPrefix(e.Name, "fk_"+short+"_history_"); ok && oldT == short+"_history" {
						ddl = append(ddl, fmt.Sprintf("ALTER TABLE %s RENAME CONSTRAINT %s TO %s",
							ident(newT), ident(e.Name), ident("fk_"+table+"_history_"+rest)))
					}
					continue
				}
				if target == "" || target == e.Name { continue }
				if relationTaken(target) {
					plan.Skipped = append(plan.Skipped, fmt.Sprintf("%s: %q could not become %q, which already exists", table, e.Name, target))
					continue
				}
				if e.Kind == "i" { rename("INDEX", e.Name, target) } else { rename("SEQUENCE", e.Name, target) }
			}
		}
		plan.Renamed = append(plan.Renamed, [2]string{short, table})
	}

	plan.Statements = ddl
	if len(plan.Renamed) == 0 { return plan }

	// Rows that name a table by its old name. Run after the DDL, so the system tables
	// themselves already have their new names.
	for _, r := range plan.Renamed {
		short, table := r[0], r[1]
		if exists["r:ohc_diffs"] {
			plan.Statements = append(plan.Statements,
				fmt.Sprintf("UPDATE ohc_diffs SET diff_type = %s WHERE diff_type = %s", literal(table), literal(short)))
		}
		if exists["r:ohc_batch_numbers"] {
			svc, _, _ := strings.Cut(table, "_")
			plan.Statements = append(plan.Statements,
				fmt.Sprintf("UPDATE ohc_batch_numbers SET table_name = %s, service = %s WHERE table_name = %s",
					literal(table), literal(svc), literal(short)))
		}
	}
	return plan
}

// MigrateToQualifiedNames renames tables, and the indexes, sequences and history
// foreign keys named after them, from their bare names to <service>_<table-name>, in
// one transaction. It then renames each table's schema sidecar files to match. Run it
// on the loaded models before BootstrapModels. A model already migrated is a no-op.
func MigrateToQualifiedNames(ctx context.Context, db models.DBTxBeginner, loaded []models.DataModel, logger *slog.Logger) error {
	rows, err := db.Query(ctx, catalogSQL)
	if err != nil { return fmt.Errorf("read catalog: %w", err) }
	catalog, err := pgx.CollectRows(rows, pgx.RowToStructByPos[catalogEntry])
	if err != nil { return fmt.Errorf("read catalog: %w", err) }

	plan := planQualifiedRenames(catalog, loaded)
	for _, s := range plan.Skipped { logger.Warn("Table name migration skipped", "reason", s) }
	if len(plan.Renamed) == 0 { return nil }

	tx, err := db.Begin(ctx)
	if err != nil { return fmt.Errorf("begin: %w", err) }
	defer tx.Rollback(ctx)
	for _, stmt := range plan.Statements {
		if _, err := tx.Exec(ctx, stmt); err != nil { return fmt.Errorf("%s: %w", stmt, err) }
	}
	if err := tx.Commit(ctx); err != nil { return fmt.Errorf("commit: %w", err) }

	for _, r := range plan.Renamed {
		logger.Info("Renamed table to include its service", "from", r[0], "to", r[1])
		renameSidecars(r[0], r[1], logger)
	}
	return nil
}

// renameSidecars moves a table's version and last-good config files to its new name.
// The old HCL file names the old table inside it, so it is removed instead: with no HCL
// file the next schema sync runs for the table, finds the renamed table already matches,
// and writes a fresh one. Nothing reads an HCL file's contents in between.
func renameSidecars(short string, table string, logger *slog.Logger) {
	pairs := [][2]string{
		{hclVersionPath(hclPathFor(short)), hclVersionPath(hclPathFor(table))},
		{configLastGoodPath(short), configLastGoodPath(table)},
	}
	for _, p := range pairs {
		if _, err := os.Stat(p[1]); err == nil { continue }
		if err := os.Rename(p[0], p[1]); err != nil && !errors.Is(err, os.ErrNotExist) {
			logger.Warn("Failed to rename schema file", "from", p[0], "to", p[1], "error", err)
		}
	}
	if err := os.Remove(hclPathFor(short)); err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Warn("Failed to remove old schema file", "file", hclPathFor(short), "error", err)
	}
}

func ident(name string) string   { return pgx.Identifier{name}.Sanitize() }
func literal(s string) string    { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
