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
func configLastGoodPath(tableName string) string {
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
		err = os.WriteFile(configLastGoodPath(*model.Table_name), cur, 0o644)
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
// waiting for manual approval before it can be re-applied. Model is the freshly
// loaded config that produced the change; on approval it becomes the desired state,
// and its Filepath is used to refresh the last-good snapshot.
type PendingChange struct {
	Table    string
	Changes  string
	Approved bool
	Model    *models.DataModel
}

// PendingApprovalGate blocks destructive schema changes and records them so an
// HTTP endpoint (or admin tool) can list, approve, or deny them.
//
// The apply path calls Record(...) when it detects a destructive change. An admin
// then calls SetApproved(table) and ApplyApproved(...) to commit, or RevertChange(...)
// to discard and restore the last-good config.
type PendingApprovalGate struct {
	mu      sync.Mutex
	pending map[string]PendingChange
}

func NewPendingApprovalGate() *PendingApprovalGate {
	return &PendingApprovalGate{pending: make(map[string]PendingChange)}
}

// Record stores (or replaces) a blocked destructive change for tableName, along with
// the model that produced it. New records start unapproved.
func (g *PendingApprovalGate) Record(table string, changes string, model *models.DataModel) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pending[table] = PendingChange{Table: table, Changes: changes, Approved: false, Model: model}
}

// SetApproved marks a pending change approved, writing the flag back into the map.
func (g *PendingApprovalGate) SetApproved(table string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if pc, ok := g.pending[table]; ok {
		pc.Approved = true
		g.pending[table] = pc
	}
}

// Pending returns the pending change for a table and whether one exists.
func (g *PendingApprovalGate) Pending(table string) (PendingChange, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	pending, ok := g.pending[table]
	return pending, ok
}

// PendingAll returns a snapshot of all changes currently blocked for approval.
func (g *PendingApprovalGate) PendingAll() []PendingChange {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]PendingChange, 0, len(g.pending))
	for _, v := range g.pending {
		out = append(out, v)
	}
	return out
}

// PendingTables returns the table names with pending updates.
func (g *PendingApprovalGate) PendingTables() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]string, 0, len(g.pending))
	for t := range g.pending {
		out = append(out, t)
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
	scheduled_for_write := []models.DataModel{}

	// Per-table destructive detection. A destructive table is recorded in the gate and
	// reverted (in ptrs) to its last-good config so the combined apply below only carries
	// non-destructive changes. Detection uses the no-FK single-table HCL so cross-table FK
	// references don't need every table present in one realm just to diff one table.
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

		if gate != nil {
			// Detect destructive changes for this table in isolation.
			noFK, err := modelToHCL_NoFKs(m)
			if err != nil {
				logger.Warn("HCL (no-FK) generation failed", "table", *m.Table_name, "error", err)
				continue
			}
			_, destructive, err := gen.planChanges(context.Background(), []string{*m.Table_name}, noFK)
			if err != nil {
				logger.Error("Failed to validate changes", "table", *m.Table_name, "error", err)
				continue
			}
			if destructive != "" {
				logger.Warn("Destructive change detected and pending approval", "table", *m.Table_name, "changes", destructive)
				// Record the change and revert this table (in ptrs) to its last-good config
				// so the combined apply does not carry the destructive change.
				gate.Record(*m.Table_name, destructive, m)
				good, loadErr := loadDataModel(configLastGoodPath(*m.Table_name))
				if loadErr != nil {
					logger.Error("Failed to load last-good config for reverted table", "table", *m.Table_name, "error", loadErr)
					continue
				}
				ptrs[i] = good
				continue // Do not snapshot
			}
		}

		// Non-destructive (or no gate): write the HCL and schedule a snapshot refresh.
		if err := os.WriteFile(hclPath, []byte(hcl), 0o644); err != nil {
			logger.Warn("Failed to write HCL file", "table", *m.Table_name, "error", err)
		}
		scheduled_for_write = append(scheduled_for_write, *m)
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
		err = os.WriteFile(configLastGoodPath(*m.Table_name), cur, 0o644)
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
	if err != nil {
		return nil, err
	}
	fp := filepath
	data.Filepath = &fp
	if err := tools.ValidateDataModel(*data); err != nil {
		return nil, err
	}
	tools.ProcessModelAdditionalFields(data)

	// Return the updated pointer
	return data, nil
}

// buildCombined returns the model pointers and table names for a combined sync:
// the freshly loaded `model` takes precedence for its own table, every other table
// uses its current registry entry. Pending (unapplied) changes on other tables are
// intentionally ignored here — approvals are committed by ApplyApproved, not by a
// live edit of an unrelated file.
func buildCombined(model *models.DataModel, allModels []models.DataModel) ([]*models.DataModel, []string) {
	tableName := *model.Table_name
	ptrs := make([]*models.DataModel, 0, len(allModels)+1)
	tableNames := make([]string, 0, len(allModels)+1)

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
		ptrs = append(ptrs, model) // brand-new table not yet in the registry
		tableNames = append(tableNames, tableName)
		if model.Track_history != nil && *model.Track_history {
			tableNames = append(tableNames, fmt.Sprintf("%s_history", tableName))
		}
	}
	return ptrs, tableNames
}

// writeHCLFiles writes per-table HCL (with FKs) to disk for documentation.
func writeHCLFiles(ptrs []*models.DataModel, logger *slog.Logger) {
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
}

// writeSnapshot refreshes the last-good version + config snapshot for an applied model.
func writeSnapshot(model *models.DataModel, logger *slog.Logger) {
	hclPath := hclPathFor(*model.Table_name)
	if err := writeVersion(model, hclPath); err != nil {
		logger.Warn("Failed to write schema version file", "table", *model.Table_name, "error", err)
	}
	if model.Filepath == nil {
		return
	}
	d, err := os.ReadFile(*model.Filepath)
	if err != nil {
		logger.Warn("Failed to read config file for snapshot", "table", *model.Table_name, "error", err)
		return
	}
	if err := os.WriteFile(configLastGoodPath(*model.Table_name), d, 0o644); err != nil {
		logger.Warn("Failed to write last-good snapshot", "table", *model.Table_name, "error", err)
	}
}

// syncModel is used by SyncModelIfNeeded (live-reload path). It builds a combined HCL
// covering all currently loaded models (so cross-table FKs resolve), then either applies
// a non-destructive change directly, or — if the change is destructive — records it in the
// gate and returns without applying. The caller (file monitor) skips route registration
// while a change is pending.
func syncModel(ctx context.Context, pool *pgxpool.Pool, model *models.DataModel, allModels []models.DataModel, logger *slog.Logger, gate *PendingApprovalGate) {
	tableName := *model.Table_name
	hclPath := hclPathFor(tableName)

	if !needsSync(model, hclPath) {
		logger.Debug("Schema up to date", "table", tableName, "version", versionStr(model))
		return
	}

	ptrs, tableNames := buildCombined(model, allModels)

	combinedHCL, err := AllModelsToHCL(ptrs)
	if err != nil {
		logger.Warn("Combined HCL generation failed", "table", tableName, "error", err)
		return
	}

	gen := NewSchemaGenerator(pool)
	changes, destructive, err := gen.planChanges(ctx, tableNames, combinedHCL)
	if err != nil {
		logger.Warn("Schema diff failed", "table", tableName, "error", err)
		return
	}

	// Destructive change → record for approval and stop. The only table whose desired
	// state differs from the DB here is the edited one, so the change belongs to it.
	if destructive != "" && gate != nil {
		logger.Warn("Destructive change detected and pending approval", "table", tableName, "changes", destructive)
		gate.Record(tableName, destructive, model)
		return
	}

	if len(changes) == 0 {
		// Version bumped but no schema delta — still refresh sidecars so we don't re-diff.
		writeSnapshot(model, logger)
		logger.Debug("No schema changes", "table", tableName, "version", versionStr(model))
		return
	}

	writeHCLFiles(ptrs, logger)

	logger.Info("Syncing schema", "table", tableName, "version", versionStr(model))
	if err := gen.applyAllSchemas(ctx, tableNames, combinedHCL); err != nil {
		logger.Warn("Schema sync failed", "table", tableName, "error", err)
		return
	}

	writeSnapshot(model, logger)
	logger.Info("Schema sync complete", "table", tableName, "version", versionStr(model))
}

// ApplyApproved commits every approved pending change in one combined apply. Approved
// tables contribute their new model; all other tables contribute their current registry
// entry (which equals the live DB), so unapproved pending changes are not applied and
// cannot be silently altered. If an approved change depends on a still-pending one (e.g. a
// new FK target), the combined apply fails and this returns an error asking for the upstream
// change to be approved first — nothing is committed for that batch.
//
// On success, each applied table's version + last-good snapshot is refreshed, its gate
// entry cleared, and onApplied(model) is invoked so the caller can re-register routes.
func ApplyApproved(ctx context.Context, pool *pgxpool.Pool, allModels []models.DataModel, logger *slog.Logger, gate *PendingApprovalGate, onApplied func(*models.DataModel)) error {
	// Collect approved tables.
	approved := make(map[string]*models.DataModel)
	for _, pc := range gate.PendingAll() {
		if pc.Approved && pc.Model != nil {
			approved[pc.Table] = pc.Model
		}
	}
	if len(approved) == 0 {
		return fmt.Errorf("no approved changes to apply")
	}

	// Build the desired model set: approved model where present, else current registry entry.
	ptrs := make([]*models.DataModel, 0, len(allModels))
	tableNames := make([]string, 0, len(allModels))
	covered := make(map[string]bool)
	for i := range allModels {
		m := &allModels[i]
		if m.Table_name == nil {
			continue
		}
		if am, ok := approved[*m.Table_name]; ok {
			ptrs = append(ptrs, am)
		} else {
			ptrs = append(ptrs, m)
		}
		covered[*m.Table_name] = true
		tableNames = append(tableNames, *m.Table_name)
		if m.Track_history != nil && *m.Track_history {
			tableNames = append(tableNames, fmt.Sprintf("%s_history", *m.Table_name))
		}
	}
	// Approved brand-new tables not yet in the registry.
	for t, am := range approved {
		if covered[t] {
			continue
		}
		ptrs = append(ptrs, am)
		tableNames = append(tableNames, t)
		if am.Track_history != nil && *am.Track_history {
			tableNames = append(tableNames, fmt.Sprintf("%s_history", t))
		}
	}

	combinedHCL, err := AllModelsToHCL(ptrs)
	if err != nil {
		return fmt.Errorf("combined HCL generation: %w", err)
	}

	gen := NewSchemaGenerator(pool)
	changes, _, err := gen.planChanges(ctx, tableNames, combinedHCL)
	if err != nil {
		return fmt.Errorf("plan approved changes: %w", err)
	}
	if len(changes) > 0 {
		// Apply the whole approved batch atomically — a failure (e.g. an approved change
		// depending on a still-pending one) rolls back with nothing committed.
		transactional, err := gen.applyChangesTx(ctx, changes)
		if err != nil {
			return fmt.Errorf("apply approved changes: %w (ensure any interdependent/upstream tables are also approved)", err)
		}
		if !transactional {
			logger.Warn("Approved changes applied non-atomically (plan is not transactional)", "tables", tableNames)
		}
	}

	// Success — persist and register each applied model, then clear it from the gate.
	writeHCLFiles(ptrs, logger)
	for t, am := range approved {
		writeSnapshot(am, logger)
		gate.Remove(t)
		if onApplied != nil {
			onApplied(am)
		}
		logger.Info("Approved schema change applied", "table", t, "version", versionStr(am))
	}
	return nil
}

// RevertChange discards a pending change and restores the live config file from its
// last-good snapshot. The file write re-triggers the monitor, which re-syncs (a no-op
// against the DB) and re-registers the previous handler.
func RevertChange(gate *PendingApprovalGate, tableName string, logger *slog.Logger) error {
	pc, ok := gate.Pending(tableName)
	if !ok {
		return fmt.Errorf("no pending change for table %q", tableName)
	}
	if pc.Model == nil || pc.Model.Filepath == nil {
		return fmt.Errorf("pending change for %q has no source file to revert", tableName)
	}
	snapshot, err := os.ReadFile(configLastGoodPath(tableName))
	if err != nil {
		return fmt.Errorf("read last-good snapshot: %w", err)
	}
	if err := os.WriteFile(*pc.Model.Filepath, snapshot, 0o644); err != nil {
		return fmt.Errorf("restore config file: %w", err)
	}
	gate.Remove(tableName)
	logger.Info("Reverted pending change to last-good config", "table", tableName)
	return nil
}

func versionStr(m *models.DataModel) string {
	if m.Version != nil {
		return *m.Version
	}
	return "unknown"
}
