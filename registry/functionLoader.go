package registry

import (
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"

	"lotusforge.au/api-server/datamodel"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/querybuilder"
)

// reservedFunctionNames lists function `name:` values that conflict with built-in
// subroutes on a model's endpoint and so cannot be registered by user functions.
// `aggregate` is omitted from the reserved set because it's served by the same
// dynamic dispatch — a user authoring a function called `aggregate` would shadow
// the built-in aggregate URL behaviour, which is a foot-gun.
var reservedFunctionNames = map[string]struct{}{
	"aggregate": {},
	"diff":      {},
	"schema":    {},
	"history":   {},
	"group":     {},
}

// FunctionRegistry is a thread-safe store of all loaded FunctionDefs, keyed by
// (bound path, name). The bound path is the bound model's Path ("service/end-point").
type FunctionRegistry struct {
	mu    sync.RWMutex
	funcs map[string]*models.FunctionDef
}

func NewFunctionRegistry() *FunctionRegistry {
	return &FunctionRegistry{funcs: map[string]*models.FunctionDef{}}
}

// functionKey is the canonical map key — guarantees uniqueness across endpoints.
func functionKey(path, name string) string {
	return path + "::" + name
}

// Register adds or replaces a function. Safe to call concurrently.
func (r *FunctionRegistry) Register(fn *models.FunctionDef) {
	if fn.BoundPath() == "" || fn.Name == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.funcs[functionKey(fn.BoundPath(), *fn.Name)] = fn
}

// Get looks up a function by its bound path ("service/end-point") and name.
func (r *FunctionRegistry) Get(path, name string) (*models.FunctionDef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	fn, ok := r.funcs[functionKey(path, name)]
	return fn, ok
}

// All returns a snapshot of all currently registered functions.
func (r *FunctionRegistry) All() []*models.FunctionDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*models.FunctionDef, 0, len(r.funcs))
	for _, fn := range r.funcs {
		out = append(out, fn)
	}
	return out
}

// LoadFunctionsFromDir reads every .yaml file in dir, parses it as a
// FunctionDef, validates it against the live model registry, and returns the
// successfully validated functions. Validation failures are logged and skipped
// (matching loadModelsFromDir behaviour) — a single bad file never aborts startup.
func LoadFunctionsFromDir(
	dir string,
	model_reg *ModelRegistry,
	logger *slog.Logger,
) []*models.FunctionDef {
	out := []*models.FunctionDef{}

	// Functions may sit in any folder below dir, so a service can keep its own together
	files, err := datamodel.YAMLFiles(dir, true)
	if err != nil {
		// Missing directory is not fatal — functions are opt-in.
		if os.IsNotExist(err) {
			logger.Debug("Function directory absent; skipping", "dir", dir)
			return out
		}
		logger.Error("Error reading function config dir", "dir", dir, "error", err)
		return out
	}

	// Track names registered so far so duplicates within a single load fail loud.
	seen := map[string]string{} // key → source file

	for _, path := range files {
		fn, err := datamodel.LoadYAMLIntoModel[models.FunctionDef](path)
		if err != nil {
			logger.Warn("Failed to load function file", "file", path, "error", err)
			continue
		}
		if errs := ValidateFunction(fn, model_reg); len(errs) > 0 {
			logger.Warn("Function file failed validation", "file", path, "errors", errs)
			continue
		}
		key := functionKey(fn.BoundPath(), *fn.Name)
		if prev, dup := seen[key]; dup {
			logger.Warn("Duplicate function name on same endpoint; second occurrence skipped",
				"endpoint", fn.BoundPath(), "name", *fn.Name,
				"first", prev, "second", path)
			continue
		}
		seen[key] = path
		out = append(out, fn)
	}

	return out
}

// ValidateFunction performs all checks that are independent of HTTP request state.
// Returns a slice of errors so the caller can report every problem at once.
func ValidateFunction(fn *models.FunctionDef, model_reg *ModelRegistry) []error {
	var errs []error

	// Required fields.
	if fn.Name == nil || *fn.Name == "" {
		errs = append(errs, fmt.Errorf("function: missing required field 'name'"))
	}
	if fn.Version == nil || *fn.Version == "" {
		errs = append(errs, fmt.Errorf("function %q: missing required field 'version'", deref(fn.Name)))
	}
	if fn.Service == nil || *fn.Service == "" {
		errs = append(errs, fmt.Errorf("function %q: missing required field 'service'", deref(fn.Name)))
	}
	if fn.Bound_to == nil || *fn.Bound_to == "" {
		errs = append(errs, fmt.Errorf("function %q: missing required field 'bound-to'", deref(fn.Name)))
	}

	// Reserved names.
	if fn.Name != nil {
		if _, reserved := reservedFunctionNames[*fn.Name]; reserved {
			errs = append(errs, fmt.Errorf("function name %q is reserved", *fn.Name))
		}
	}

	// Bound model must exist in the function's own service, so a service can only add
	// functions to its own tables.
	var cfg *models.DataModel
	if path := fn.BoundPath(); path != "" {
		m, ok := model_reg.ByPath(path)
		if !ok {
			errs = append(errs, fmt.Errorf("function %q: bound-to %q does not match any loaded end-point in service %q",
				deref(fn.Name), *fn.Bound_to, *fn.Service))
		} else {
			cfg = m
		}
	}

	// If the model couldn't be resolved we can't check field references; bail early.
	if cfg == nil {
		return errs
	}

	// where: every key must resolve to a non-private model field.
	for k := range fn.Where {
		if _, ok := datamodel.CheckFieldGetValid(k, cfg); !ok {
			errs = append(errs, fmt.Errorf("function %q: where field %q is unknown or private",
				deref(fn.Name), k))
		}
	}

	// parameters: name + field required, field must resolve.
	paramNames := map[string]bool{}
	for i, p := range fn.Parameters {
		if p.Name == nil || *p.Name == "" {
			errs = append(errs, fmt.Errorf("function %q: parameter[%d] missing 'name'", deref(fn.Name), i))
			continue
		}
		if paramNames[*p.Name] {
			errs = append(errs, fmt.Errorf("function %q: duplicate parameter name %q",
				deref(fn.Name), *p.Name))
		}
		paramNames[*p.Name] = true
		if p.Field == nil || *p.Field == "" {
			errs = append(errs, fmt.Errorf("function %q: parameter %q missing 'field'",
				deref(fn.Name), *p.Name))
			continue
		}
		if _, ok := datamodel.CheckFieldGetValid(*p.Field, cfg); !ok {
			errs = append(errs, fmt.Errorf("function %q: parameter %q references unknown or private field %q",
				deref(fn.Name), *p.Name, *p.Field))
		}
	}

	// fields: bare-string entries must resolve. Calculated entries rejected in v1.
	for i, f := range fn.Fields {
		if f.IsCalculated() {
			errs = append(errs, fmt.Errorf("function %q: fields[%d] uses 'expression' (calculated fields not supported in this version)",
				deref(fn.Name), i))
			continue
		}
		if f.Field == "" {
			errs = append(errs, fmt.Errorf("function %q: fields[%d] is empty", deref(fn.Name), i))
			continue
		}
		if _, ok := datamodel.CheckFieldGetValid(f.Field, cfg); !ok {
			errs = append(errs, fmt.Errorf("function %q: fields[%d] references unknown or private field %q",
				deref(fn.Name), i, f.Field))
		}
	}

	// group-by tokens.
	for _, g := range fn.Group_by {
		if _, ok := datamodel.CheckFieldGetValid(g, cfg); !ok {
			errs = append(errs, fmt.Errorf("function %q: group-by %q is unknown or private",
				deref(fn.Name), g))
		}
	}

	// aggregate tokens — must parse via ParseAggregateFuncString.
	// We use a throwaway QueryBuilder for the parse; it doesn't mutate cfg.
	scratch := querybuilder.NewQueryBuilder(slog.Default())
	for _, a := range fn.Aggregate {
		if _, ok, _, _ := querybuilder.ParseAggregateFuncString(a, scratch, cfg); !ok {
			errs = append(errs, fmt.Errorf("function %q: aggregate token %q is invalid (expected count, sum:f, avg:f, min:f, max:f, distinct:f~f..)",
				deref(fn.Name), a))
		}
	}

	// sort-by tokens — must resolve to either an aggregate, a group-by column, or
	// a declared `fields` entry. The "must appear in SELECT list" check at runtime
	// catches the rest, but loud failures at load time are friendlier.
	declaredFields := map[string]bool{}
	for _, f := range fn.Fields {
		declaredFields[f.Field] = true
	}
	for _, g := range fn.Group_by {
		if f, ok := datamodel.CheckFieldGetValid(g, cfg); ok {
			declaredFields[f] = true
		}
	}
	for _, s := range fn.Sort_by {
		token := strings.TrimSpace(strings.Split(s, "~")[0])
		if _, ok, _, _ := querybuilder.ParseAggregateFuncString(token, scratch, cfg); ok {
			continue
		}
		if declaredFields[token] {
			continue
		}
		if _, ok := datamodel.CheckFieldGetValid(token, cfg); ok && !slices.Contains(fn.Sort_by, token) {
			// Allow plain field references for non-aggregating functions even when
			// they're not in `fields:` — the runtime executor will skip unknown
			// sort tokens defensively, matching the URL aggregate path's leniency.
			continue
		}
		errs = append(errs, fmt.Errorf("function %q: sort-by token %q does not resolve to an aggregate, group-by, or declared field",
			deref(fn.Name), s))
	}

	return errs
}

func deref(s *string) string {
	if s == nil { return "<unnamed>" }
	return *s
}
