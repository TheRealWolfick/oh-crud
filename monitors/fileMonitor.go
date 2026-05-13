package monitors

import (
	"context"
	"net/http"

	"github.com/fsnotify/fsnotify"
	"lotusforge.au/api-server/handlers"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/schematools"
	"lotusforge.au/api-server/tools"
)

func ModelsMonitor(handlerRegistry *tools.HandlerRegistry, modelRegistry *tools.ModelRegistry, auth func(http.Handler) http.Handler, qm *tools.QueueManager, gate *schematools.PendingApprovalGate, server_conf *models.SwappableServerConfig) {
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
						qm.Logger.Debug("Update detected to config file", "config", *updated_config.Name)
						if err := tools.ValidateDataModel(*updated_config); err != nil {
							qm.Logger.Error("Updated YAML failed validation, routes not updated", "error", err)
						} else {
							tools.ProcessModelAdditionalFields(updated_config)
							modelRegistry.Register(updated_config)
							handlers.RegisterRoutes(updated_config, handlerRegistry, auth, qm, server_conf)
							schematools.SyncModelIfNeeded(context.Background(), qm.Db, updated_config, modelRegistry.All(), qm.Logger, gate)
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
