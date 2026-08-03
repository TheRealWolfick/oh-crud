package monitors

import (
	"context"
	"net/http"

	"github.com/fsnotify/fsnotify"
	"github.com/jackc/pgx/v5/pgxpool"
	"lotusforge.au/api-server/handlers"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/schematools"
	"lotusforge.au/api-server/tools"
)

func ModelsMonitor(handlerRegistry *tools.HandlerRegistry, modelRegistry *tools.ModelRegistry, auth func(http.Handler) http.Handler, qm *tools.QueueManager, gate *schematools.PendingApprovalGate, server_conf *models.SwappableServerConfig, evh *tools.EventManager) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		qm.Logger.Error("Failed to load watcher, config will not update live", "error", err)
		return
	}
	defer watcher.Close()

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Has(fsnotify.Write) {
					updated_config, err := tools.LoadYAMLIntoModel[models.DataModel](event.Name)
					if err != nil {
						qm.Logger.Error("Updated YAML failed to load. Skipping updating routes", "error", err)
					} else {
						// Record the source path so the gate/snapshot logic can find and revert it.
						filepath := event.Name
						updated_config.Filepath = &filepath
						// Check updated model is valid
						qm.Logger.Debug("Update detected to config file", "config", *updated_config.Name)
						if err := tools.ValidateDataModel(*updated_config); err != nil {
							qm.Logger.Error("Updated YAML failed validation, routes not updated", "error", err)
						} else {
							// Process model
							tools.ProcessModelAdditionalFields(updated_config)
							// Sync model first, stopping if there is an error or destructive change
							schematools.SyncModelIfNeeded(context.Background(), qm.Db.(*pgxpool.Pool), updated_config, modelRegistry.All(), qm.Logger, gate)
							// If the sync stopped because of destructive changes, the change will be waiting in the gate
							if _, pending := gate.Pending(*updated_config.Table_name); !pending {
								modelRegistry.Register(updated_config)
								handlers.RegisterRoutes(updated_config, handlerRegistry, auth, qm, server_conf, evh, gate, modelRegistry)
							} else {
								qm.Logger.Info("Pending change waiting", "table", *updated_config.Table_name)
							}
						}
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				qm.Logger.Error("Error in file watcher", "Error", err)
			}
		}
	}()

	paths := []string{"./config/base-models", "./config/special-models", "./config/default"}
	for _, p := range paths {
		err = watcher.Add(p)
		if err != nil {
			qm.Logger.Error("Failed to load monitoring for folder", "dir", p)
		}
	}

	// Block goroutine
	<-make(chan struct{})
}
