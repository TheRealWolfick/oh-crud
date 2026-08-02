package schematools

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/tools"
)

// hclDir is where generated HCL files (and their .version sidecars) are written.
const hclDir = "./config/database"

// ---------------------------------------------------------------------------
// Version-check helpers
// ---------------------------------------------------------------------------

// hclPathFor returns the HCL file path for a given table.
func hclPathFor(tableName string) string {
	return filepath.Join(hclDir, tableName+".pg.hcl")
}

// hclVersionPath returns the sidecar file that stores the last-applied version
// for a given HCL file, e.g. "config/database/departments.hcl.version"
func hclVersionPath(hclPath string) string {
	return hclPath + ".version"
}

// configCurrentPath returns the sidecar file that records the last known
// good config. If any destructive changes are rejected, this file will overwrite
// the updated config file, reverting it back to its previous state
func configCurrentVersionPath(tableName string) string {
	return filepath.Join(hclDir, tableName+".current.yaml")
}

// needsSync returns true when:
//   - the .hcl file does not exist yet, OR
//   - the stored version doesn't match the model's current version
func needsSync(model *models.DataModel, hclPath string) bool {
	if _, err := os.Stat(hclPath); os.IsNotExist(err) {
		return true
	}
	versionFile := hclVersionPath(hclPath)
	stored, err := os.ReadFile(versionFile)
	if err != nil {
		// Write current file to best version (new file detected)
		cur, err := os.ReadFile(*model.Filepath); if err != nil {
			slog.Error("Error reading config current file in needsSync", "error", err)
			return true
		}
		err = os.WriteFile(configCurrentVersionPath(*model.Table_name), cur, 0o644)
		if err != nil {
			slog.Error("Error writing config current file in needsSync", "error", err)
			return true
		}
		return true // no version file → treat as new
	}
	if model.Version == nil {
		return false
	}
	return strings.TrimSpace(string(stored)) != strings.TrimSpace(*model.Version)
}

// writeVersion persists the applied version alongside the .hcl file.
func writeVersion(model *models.DataModel, hclPath string) error {
	if model.Version == nil {
		return nil
	}
	return os.WriteFile(hclVersionPath(hclPath), []byte(*model.Version), 0o644)
}

// ---------------------------------------------------------------------------
// Approval strategies
// ---------------------------------------------------------------------------

// InteractiveApproval prints the destructive changes to stdout and waits for
// the operator to type "yes". Use this in CLI tools only — not in a server process.
func InteractiveApproval(_ context.Context, changes string) error {
	fmt.Println("WARNING: DESTRUCTIVE SCHEMA CHANGES DETECTED:")
	fmt.Println(changes)
	fmt.Print("\nType 'yes' to proceed, anything else to abort: ")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	if strings.TrimSpace(scanner.Text()) != "yes" {
		return fmt.Errorf("operator declined destructive changes")
	}
	return nil
}

// EnvFlagApproval approves destructive changes only when
// ALLOW_DESTRUCTIVE_MIGRATIONS=true is set. Suitable for CI pipelines.
func EnvFlagApproval(_ context.Context, changes string) error {
	if os.Getenv("ALLOW_DESTRUCTIVE_MIGRATIONS") == "true" {
		fmt.Printf("ALLOW_DESTRUCTIVE_MIGRATIONS=true — proceeding with:\n%s\n", changes)
		return nil
	}
	return fmt.Errorf("destructive changes require ALLOW_DESTRUCTIVE_MIGRATIONS=true:\n%s", changes)
}

// NeverApproval always rejects destructive changes. Use when schema changes must
// be managed through an explicit out-of-band deploy process.
func NeverApproval(_ context.Context, changes string) error {
	return fmt.Errorf("destructive schema changes are not auto-applied:\n%s", changes)
}

// ---------------------------------------------------------------------------
// PendingApprovalGate — records blocked destructive changes for future approval
// ---------------------------------------------------------------------------

// PendingChange holds a destructive schema change that was blocked and is
// waiting for manual approval before it can be re-applied.
type PendingChange struct {
	Table        string
	Changes   	 string
	ApprovalFunc any
}

// PendingApprovalGate blocks destructive schema changes and records them so a
// future HTTP endpoint (or admin tool) can list and approve them.
//
// Wire it in by passing gate.ApprovalFuncFor(tableName) as the ApprovalFunc on
// a SchemaGenerator. When the frontend is available, call Approve/Deny and
// re-trigger SyncModelIfNeeded for the relevant table.
type PendingApprovalGate struct {
	mu      sync.Mutex
	pending map[string]PendingChange
}

func NewPendingApprovalGate() *PendingApprovalGate {
	return &PendingApprovalGate{}
}

// ApprovalFuncFor returns an ApprovalFunc that records the blocked changes
// under tableName and aborts the apply. If the same table is blocked again
// before being cleared, the recorded changes are overwritten.
func (g *PendingApprovalGate) ApprovalFuncFor() ApprovalFunc {
	return func(tableName string, changes string) error {
		g.mu.Lock()
		defer g.mu.Unlock()

		// Write the pending changes. This will overwrite/replace any existing changes.
		// Use the table name passed by the apply path — in a combined apply this is the
		// table actually being modified, which is more accurate than the bound name.
		g.pending[tableName] = PendingChange{ Table: tableName, Changes: changes }

		return fmt.Errorf("destructive changes for table %q queued for manual approval", tableName)
	}
}

// Pending returns what is pending for this specific end point, it also returns if
// there is any pending changes or not
func (g *PendingApprovalGate) Pending(table string) (PendingChange, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	pending, ok := g.pending[table]

	return pending, ok
}
// Pending returns a snapshot of all changes currently blocked for approval.
func (g *PendingApprovalGate) PendingAll() []PendingChange {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]PendingChange, 0, len(g.pending))

	for _, v := range g.pending {
		out = append(out, v)
	}
	return out
}

// Remove clears the pending entry for tableName. Call this after the change is
// either approved and applied, or denied.
func (g *PendingApprovalGate) Remove(tableName string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	delete(g.pending, tableName)
}

// ---------------------------------------------------------------------------
// Bootstrap — called from main on startup
// ---------------------------------------------------------------------------

// BootstrapModels syncs all loaded models to the database on startup using a single
// combined HCL that covers every table. This lets Atlas resolve cross-table FK
// references and apply tables in correct dependency order.
//
// A sync is triggered only if at least one model's version has changed or its HCL
// file is missing. Non-destructive changes are applied automatically. Destructive
// changes are passed to the gate (recorded + apply aborted). Pass nil for gate to
// auto-approve destructive changes (dev mode only).
func BootstrapModels(
	ctx context.Context, 
	pool *pgxpool.Pool, 
	loadedModels []models.DataModel, 
	logger *slog.Logger, 
	gate *PendingApprovalGate,
) {
	// Collect valid models and their table names.
	ptrs := make([]*models.DataModel, 0, len(loadedModels))
	tableNames := make([]string, 0, len(loadedModels))
	for i := range loadedModels {
		m := &loadedModels[i]
		if m.Table_name == nil {
			continue
		}
		ptrs = append(ptrs, m)
		tableNames = append(tableNames, *m.Table_name)
		if m.Track_history != nil && *m.Track_history {
			tableNames = append(tableNames, fmt.Sprintf("%s_history", *m.Table_name))
		}
	}
	if len(ptrs) == 0 {
		return
	}

	// Check whether any model needs a sync.
	anyNeedsSync := false
	for _, m := range ptrs {
		// Read if a sync is needed based on the sidecar version file, and the version in the config
		if needsSync(m, hclPathFor(*m.Table_name)) {
			anyNeedsSync = true
		}
	}
	if !anyNeedsSync {
		logger.Debug("All schemas up to date")
		return
	}

	gen := NewSchemaGenerator(pool)
	if gate != nil { gen.Approval = gate.ApprovalFuncFor() }
	scheduled_for_write := []models.DataModel{}
	
	// Write individual per-table HCL files to disk (for documentation / FK reference).
	for i, m := range ptrs {
		// Convert the model to HCL
		hcl, err := ModelToHCL(m)
		if err != nil {
			logger.Warn("HCL generation failed", "table", *m.Table_name, "error", err)
			continue
		}
		
		// Ensure that the HCL folder exists
		hclPath := hclPathFor(*m.Table_name)
		if err := os.MkdirAll(dirOf(hclPath), 0o755); err != nil {
			logger.Warn("Failed to create HCL dir", "table", *m.Table_name, "error", err)
			continue
		}

		// If there is an approval gate, should always be yes, but only run if there is at least
		// one item that needs syncing
		if gate != nil && anyNeedsSync {
			// Create the approval func for this table
			// Check if there are destructive
			err, destructive := gen.validateChange(context.Background(), *m.Table_name, hcl)
			if (err != nil) { logger.Error("Failed to validate changes", "table", *m.Table_name, "error", err) } else {
				if (destructive) {
					logger.Warn("Destructive change detected and pending approval", "table", *m.Table_name)
					// Load current safe file and replace the current model pointer with the
					// last known good version, so the combined apply reverts this table.
					good, loadErr := loadDataModel(configCurrentVersionPath(*m.Table_name))
					if loadErr != nil {
						logger.Error("Failed to load last-good config for reverted table", "table", *m.Table_name, "error", loadErr)
						continue // keep ptrs[i] as-is; do not snapshot a rejected change
					}
					ptrs[i] = good
					// Note: do NOT schedule a snapshot here — the last known good file must
					// remain untouched so a later deny can revert to it.
				} else {
					// Write the updated, non destructive hcl file.
					if err := os.WriteFile(hclPath, []byte(hcl), 0o644); err != nil {
						logger.Warn("Failed to write HCL file", "table", *m.Table_name, "error", err)
					}
					// Schedule the applied model to refresh its last-good snapshot after sync.
					scheduled_for_write = append(scheduled_for_write, *m)
				}
			}
		} else {
			if err := os.WriteFile(hclPath, []byte(hcl), 0o644); err != nil {
				logger.Warn("Failed to write HCL file", "table", *m.Table_name, "error", err)
			}
		}
	}

	// Generate combined HCL (all tables in one realm so FK refs resolve).
	combinedHCL, err := AllModelsToHCL(ptrs)
	if err != nil {
		logger.Warn("Combined HCL generation failed", "error", err)
		return
	}

	// Sync all schemas with non destructive changes
	logger.Info("Syncing all schemas", "tables", tableNames)
	if err := gen.applyAllSchemas(ctx, tableNames, combinedHCL); err != nil {
		logger.Warn("Schema sync failed", "error", err)
		return
	}

	// Write version sidecars so subsequent startups skip unchanged models.
	for _, m := range ptrs {
		if err := writeVersion(m, hclPathFor(*m.Table_name)); err != nil {
			logger.Warn("Failed to write schema version file", "table", *m.Table_name, "error", err)
		}
	}
	logger.Info("Schema sync complete", "tables", len(tableNames))

	for _, m := range scheduled_for_write {
		cur, err := os.ReadFile(*m.Filepath); if err != nil {
			slog.Error("Error reading config current file in needsSync", "error", err)
			return
		}
		err = os.WriteFile(configCurrentVersionPath(*m.Table_name), cur, 0o644)
		if err != nil {
			slog.Error("Error writing config current file in needsSync", "error", err)
			return
		}
	}
	logger.Info("All tables last successful file loaded", "tables", len(tableNames))
}

// SyncModelIfNeeded checks whether a model's schema is out of date and, if so,
// applies the changes using a combined HCL that covers all currently loaded models.
// This allows FK constraints that reference other tables to be applied correctly
// during live reload, not just at startup.
// Errors are logged, not returned, so a schema failure never breaks route registration.
func SyncModelIfNeeded(ctx context.Context, pool *pgxpool.Pool, model *models.DataModel, allModels []models.DataModel, logger *slog.Logger, gate *PendingApprovalGate) {
	if model.Table_name == nil {
		return
	}
	syncModel(ctx, pool, model, allModels, logger, gate)
}

// Load model to hcl is a standalone function that loads a model and converts it into HCL
// This is primarily used for loading the last good version of a file while updates are
// pending review
func loadDataModel(filepath string) (*models.DataModel, error) {
	// Load the model
	data, err := tools.LoadYAMLIntoModel[models.DataModel](filepath)
	data.Filepath = &filepath
	if err != nil {
		return nil, err
	}
	if err := tools.ValidateDataModel(*data); err != nil {
		return nil, err
	}
	tools.ProcessModelAdditionalFields(data)

	// Return the updated pointer
	return data, nil
}

// syncModel is used by SyncModelIfNeeded (live-reload path).
// It uses the same combined-HCL strategy as BootstrapModels so that cross-table
// FK constraints are resolved and applied correctly during live reload.
func syncModel(ctx context.Context, pool *pgxpool.Pool, model *models.DataModel, allModels []models.DataModel, logger *slog.Logger, gate *PendingApprovalGate) {
	tableName := *model.Table_name
	hclPath := hclPathFor(tableName)

	if !needsSync(model, hclPath) {
		logger.Debug("Schema up to date", "table", tableName, "version", versionStr(model))
		return
	}

	// Build the full model list for the combined sync, with the updated model
	// taking precedence over any older entry for its table name.
	ptrs := make([]*models.DataModel, 0, len(allModels))
	tableNames := make([]string, 0, len(allModels))
	seen := false
	for i := range allModels {
		m := &allModels[i]
		if m.Table_name == nil {
			continue
		}
		if *m.Table_name == tableName {
			ptrs = append(ptrs, model) // use the freshly loaded version
			seen = true
		} else {
			ptrs = append(ptrs, m)
		}
		tableNames = append(tableNames, *m.Table_name)
		if m.Track_history != nil && *m.Track_history {
			tableNames = append(tableNames, fmt.Sprintf("%s_history", *m.Table_name))
		}
	}
	if !seen {
		// Brand-new table not yet in the registry — append it.
		ptrs = append(ptrs, model)
		tableNames = append(tableNames, tableName)
	}

	// Check if this model has a history table
	if model.Track_history != nil && *model.Track_history {
		tableNames = append(tableNames, fmt.Sprintf("%s_history", *model.Table_name))
	}

	// Write individual per-table HCL files to disk (includes FKs, for documentation).
	for _, m := range ptrs {
		hcl, err := ModelToHCL(m)
		if err != nil {
			logger.Warn("HCL generation failed", "table", *m.Table_name, "error", err)
			continue
		}
		p := hclPathFor(*m.Table_name)
		if err := os.MkdirAll(dirOf(p), 0o755); err != nil {
			logger.Warn("Failed to create HCL dir", "table", *m.Table_name, "error", err)
			continue
		}
		if err := os.WriteFile(p, []byte(hcl), 0o644); err != nil {
			logger.Warn("Failed to write HCL file", "table", *m.Table_name, "error", err)
		}
	}

	combinedHCL, err := AllModelsToHCL(ptrs)
	if err != nil {
		logger.Warn("Combined HCL generation failed", "table", tableName, "error", err)
		return
	}

	gen := NewSchemaGenerator(pool)
	if gate != nil {
		gen.Approval = gate.ApprovalFuncFor()
	}

	logger.Info("Syncing schema", "table", tableName, "version", versionStr(model))
	if err := gen.applyAllSchemas(ctx, tableNames, combinedHCL); err != nil {
		logger.Warn("Schema sync failed", "table", tableName, "error", err)
		if gate != nil {
			for _, v := range gate.PendingAll() {
				logger.Warn("Blocked destructive changes", "trigger_table", v.Table, "changes", v.Changes)
			}
		}
		return
	}

	if err := writeVersion(model, hclPath); err != nil {
		logger.Warn("Failed to write schema version file", "table", tableName, "error", err)
		return
	}
	logger.Info("Schema sync complete", "table", tableName, "version", versionStr(model))
}

func versionStr(m *models.DataModel) string {
	if m.Version != nil {
		return *m.Version
	}
	return "unknown"
}
