package monitors

import (
	"net/http"

	"github.com/fsnotify/fsnotify"
	"lotusforge.au/api-server/handlers"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/tools"
)

// FunctionsMonitor watches config/functions/ for changes to declarative
// function YAMLs. On a Write event the file is re-parsed, re-validated against
// the live model registry, re-registered into the function registry, and
// re-registered as an HTTP route via the version-gated HandlerRegistry. Bad
// updates log loudly and leave the previous version in place.
func FunctionsMonitor(
	handlerRegistry *tools.HandlerRegistry,
	modelRegistry *tools.ModelRegistry,
	functionRegistry *tools.FunctionRegistry,
	auth func(http.Handler) http.Handler,
	qm *tools.QueueManager,
	server_conf *models.SwappableServerConfig,
) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		qm.Logger.Error("Failed to load function watcher; functions will not hot-reload", "error", err)
		return
	}
	defer watcher.Close()

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok { return }
				if !event.Has(fsnotify.Write) { continue }

				fn, err := tools.LoadYAMLIntoModel[models.FunctionDef](event.Name)
				if err != nil {
					qm.Logger.Error("Updated function YAML failed to load; skipping",
						"file", event.Name, "error", err)
					continue
				}
				if errs := tools.ValidateFunction(fn, modelRegistry); len(errs) > 0 {
					qm.Logger.Error("Updated function YAML failed validation; skipping",
						"file", event.Name, "errors", errs)
					continue
				}
				qm.Logger.Debug("Function update detected", "name", *fn.Name, "bound-to", *fn.Bound_to)
				functionRegistry.Register(fn)
				handlers.RegisterFunctionRoutes(fn, modelRegistry, handlerRegistry, auth, qm, server_conf)

			case err, ok := <-watcher.Errors:
				if !ok { return }
				qm.Logger.Error("Function watcher error", "error", err)
			}
		}
	}()

	if err := watcher.Add("./config/functions"); err != nil {
		// Missing directory is not fatal — functions are opt-in.
		qm.Logger.Debug("Function directory absent; hot-reload disabled until it exists",
			"error", err)
	}

	<-make(chan struct{})
}
