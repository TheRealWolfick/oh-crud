package monitors

import (
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"lotusforge.au/api-server/datamodel"
	"lotusforge.au/api-server/events"
	"lotusforge.au/api-server/globals"
	"lotusforge.au/api-server/handlers"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/queue"
	"lotusforge.au/api-server/registry"
	"lotusforge.au/api-server/safe"
)

// FunctionsMonitor watches config/functions/ for changes to declarative
// function YAMLs. On a Write event the file is re-parsed, re-validated against
// the live model registry, re-registered into the function registry, and
// re-registered as an HTTP route via the version-gated HandlerRegistry. Bad
// updates log loudly and leave the previous version in place.
func FunctionsMonitor(
	handlerRegistry *registry.HandlerRegistry,
	modelRegistry *registry.ModelRegistry,
	functionRegistry *registry.FunctionRegistry,
	auth handlers.ModelAuth,
	qm *queue.QueueManager,
	server_conf *models.SwappableServerConfig,
	evh *events.EventManager,
) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		qm.Logger.Error("Failed to load function watcher; functions will not hot-reload", "error", err)
		return
	}
	defer watcher.Close()

	// Functions may sit in any folder below config/functions
	root := "./config/functions"

	// Changes are staged per file until the file has been quiet for settleFor
	settle := newSettler(globals.SettleFor, qm.Logger, "functions monitor")

	// reload validates and re-registers the function in one YAML file
	reload := func(path string) {
		if filepath.Ext(path) != ".yaml" { return }
		fn, err := datamodel.LoadYAMLIntoModel[models.FunctionDef](path)
		if err != nil {
			qm.Logger.Error("Updated function YAML failed to load; skipping",
				"file", path, "error", err)
			return
		}
		if errs := registry.ValidateFunction(fn, modelRegistry); len(errs) > 0 {
			qm.Logger.Error("Updated function YAML failed validation; skipping",
				"file", path, "errors", errs)
			return
		}
		qm.Logger.Debug("Function update detected", "name", *fn.Name, "bound-to", fn.BoundPath())
		functionRegistry.Register(fn)
		handlers.RegisterFunctionRoutes(fn, modelRegistry, handlerRegistry, auth, qm, server_conf, evh)
	}

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok { return }
				// Handle each event on its own, so a panic from one bad file is logged and
				// the watcher keeps running
				func() {
					defer safe.Recover(qm.Logger, "functions monitor", nil)
					// A new folder needs watching, and may arrive with functions already in it
					if newFolder(event, []string{root}) {
						if err := watchTree(watcher, event.Name); err != nil {
							qm.Logger.Error("Failed to watch new function folder", "dir", event.Name, "error", err)
						}
						files, _ := datamodel.YAMLFiles(event.Name, true)
						for _, f := range files { settle.stage(f, func() { reload(f) }) }
						return
					}
					if event.Has(fsnotify.Write) && filepath.Ext(event.Name) == ".yaml" {
						qm.Logger.Debug("Function file change staged", "file", event.Name, "settle", globals.SettleFor)
						settle.stage(event.Name, func() { reload(event.Name) })
					}
				}()

			case err, ok := <-watcher.Errors:
				if !ok { return }
				qm.Logger.Error("Function watcher error", "error", err)
			}
		}
	}()

	if err := watchTree(watcher, root); err != nil {
		// Missing directory is not fatal — functions are opt-in.
		qm.Logger.Debug("Function directory absent; hot-reload disabled until it exists",
			"error", err)
	}

	// Block goroutine
	<-make(chan struct{})
}
