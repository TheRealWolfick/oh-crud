package monitors

import (
	"log/slog"

	"github.com/fsnotify/fsnotify"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/tools"
)


func ServerConfigMonitor(server_conf *models.SwappableServerConfig, log *slog.Logger) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Error("Failed to load watcher, config will not update live", "error", err)
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
					updated_config, err := tools.LoadYAMLIntoModel[models.ServerConfig](event.Name)
					if err != nil {
					  log.Error("Updated YAML failed to load. Skipping updating routes", "error", err)
					} else {
						server_conf.Swap(updated_config)
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Error("Error in file watcher", "Error", err)
			}
		}
	}()

	files := []string{"./config/server"}
	for _, p := range files {
		err = watcher.Add(p)
		if err != nil {
			log.Error("Failed to load monitoring for file", "file", p)
		}
	}

	// Block goroutine
	<-make(chan struct{})
}
