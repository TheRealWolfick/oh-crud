package monitors

import (
	"context"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"github.com/jackc/pgx/v5/pgxpool"
	"lotusforge.au/api-server/datamodel"
	"lotusforge.au/api-server/events"
	"lotusforge.au/api-server/handlers"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/queue"
	"lotusforge.au/api-server/registry"
	"lotusforge.au/api-server/safe"
	"lotusforge.au/api-server/schematools"
)

func ModelsMonitor(handlerRegistry *registry.HandlerRegistry, modelRegistry *registry.ModelRegistry, auth handlers.ModelAuth, qm *queue.QueueManager, gate *schematools.PendingApprovalGate, server_conf *models.SwappableServerConfig, evh *events.EventManager) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		qm.Logger.Error("Failed to load watcher, config will not update live", "error", err)
		return
	}
	defer watcher.Close()

	// User models may sit in any folder below base-models, so a service can keep its own
	// together. The system models in default stay flat.
	recursive := []string{"./config/base-models"}
	flat := []string{"./config/special-models", "./config/default"}

	// Changes are staged per file until the file has been quiet for settleFor
	settle := newSettler(settleFor, qm.Logger, "models monitor")

	// reload validates, syncs and re-registers the model in one YAML file
	reload := func(path string) {
		if filepath.Ext(path) != ".yaml" { return }
		updated_config, err := datamodel.LoadYAMLIntoModel[models.DataModel](path)
		if err != nil {
			qm.Logger.Error("Updated YAML failed to load. Skipping updating routes", "error", err)
		} else {
			// Record the source path so the gate/snapshot logic can find and revert it.
			source := path
			updated_config.Filepath = &source
			// Check updated model is valid
			qm.Logger.Debug("Update detected to config file", "config", *updated_config.Name)
			if err := datamodel.ValidateDataModel(*updated_config); err != nil {
				qm.Logger.Error("Updated YAML failed validation, routes not updated", "error", err)
			} else if err := datamodel.ValidateModelOwner(*updated_config); err != nil {
				qm.Logger.Error("Updated YAML failed validation, routes not updated", "error", err)
			} else {
				// Process model
				datamodel.ProcessModelAdditionalFields(updated_config)
				// Sync model first, stopping if there is an error or destructive change
				schematools.SyncModelIfNeeded(context.Background(), qm.Db.(*pgxpool.Pool), updated_config, modelRegistry.All(), qm.Logger, gate)
				// If the sync stopped because of destructive changes, the change will be waiting in the gate
				if _, pending := gate.Pending(*updated_config.Table_name); !pending {
					if err := modelRegistry.Register(updated_config); err != nil {
						qm.Logger.Error("Updated model not loaded, routes not updated", "config", *updated_config.Name, "error", err)
						return
					}
					diff_model, _ := modelRegistry.ByTableName("ohc_diffs")
					handlers.RegisterRoutes(updated_config, diff_model, handlerRegistry, auth, qm, server_conf, evh, gate, modelRegistry)
				} else {
					qm.Logger.Info("Pending change waiting", "table", *updated_config.Table_name)
				}
			}
		}
	}

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// Handle each event on its own, so a panic from one bad file is logged and
				// the watcher keeps running
				func() {
					defer safe.Recover(qm.Logger, "models monitor", nil)
					// A new folder needs watching, and may arrive with models already in it
					if newFolder(event, recursive) {
						if err := watchTree(watcher, event.Name); err != nil {
							qm.Logger.Error("Failed to watch new folder", "dir", event.Name, "error", err)
						}
						files, _ := datamodel.YAMLFiles(event.Name, true)
						for _, f := range files { settle.stage(f, func() { reload(f) }) }
						return
					}
					if event.Has(fsnotify.Write) && filepath.Ext(event.Name) == ".yaml" {
						qm.Logger.Debug("Model file change staged", "file", event.Name, "settle", settleFor)
						settle.stage(event.Name, func() { reload(event.Name) })
					}
				}()

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				qm.Logger.Error("Error in file watcher", "Error", err)
			}
		}
	}()

	for _, p := range recursive {
		if err := watchTree(watcher, p); err != nil {
			qm.Logger.Error("Failed to load monitoring for folder", "dir", p, "error", err)
		}
	}
	for _, p := range flat {
		if err := watcher.Add(p); err != nil {
			qm.Logger.Error("Failed to load monitoring for folder", "dir", p)
		}
	}

	// Block goroutine
	<-make(chan struct{})
}
