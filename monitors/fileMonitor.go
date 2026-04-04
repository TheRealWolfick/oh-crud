package monitors

import (
	"fmt"
	"net/http"

	"github.com/fsnotify/fsnotify"
	"lotusforge.au/api-server/handlers"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/tools"
)


func ModelsMonitor(handlerRegistry *models.HandlerRegistry, auth func(http.Handler) http.Handler, qm *tools.QueueManager) {
	// create the watcher
	watcher, err := fsnotify.NewWatcher()

	if err != nil {
		qm.Logger.Error("Failed to load watcher, config will not update live", "error", err)
		return
	}

	defer watcher.Close()

	// Monitor for events
	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Has(fsnotify.Write) {
					updated_config, err := tools.LoadModel_YAML(event.Name)
					if err != nil {
						qm.Logger.Error("Updated YAML failed to load. Skipping updating routes", "error", err)
					} else {
						qm.Logger.Debug("Update detected to config file", "config", *updated_config.Name)
						is_valid, errors := tools.CheckConfigIsValid(*updated_config)
						if is_valid {
							handlers.RegisterRoutes(updated_config, handlerRegistry, auth, qm)
						} else {
							qm.Logger.Error("Updated YAML was not valid. Errors:")
							for pos, err := range errors {
								qm.Logger.Error(fmt.Sprintf("Error #%v", pos), "Field", err.Field, "Error", err.Message)
							}
						}
					}
				}
			case err, ok := <- watcher.Errors:
				if !ok {
					return
				}
				qm.Logger.Error("Error in file watcher", "Error", err)
			}
		}
	}()

	paths := []string{"./config/base-models", "./config/special-models"}

	for _, p := range paths {
		err = watcher.Add(p)
		if err != nil { qm.Logger.Error("Failed to load monitoring for folder", "dir", p) }
	}

	// Block go routine
	<-make(chan struct{})
}
