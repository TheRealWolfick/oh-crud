package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/errgroup"
	"lotusforge.au/api-server/middleware"
	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/tools"
)

// RegisterFunctionRoutes registers GET /{end_point}/fn/{function_name} for a
// single declarative function. The bound model is resolved at request time
// from model_reg so hot-reloading the model continues to work without
// re-registering the function.
func RegisterFunctionRoutes(
	fn *models.FunctionDef,
	model_reg *tools.ModelRegistry,
	handler_reg *tools.HandlerRegistry,
	auth func(http.Handler) http.Handler,
	qm *tools.QueueManager,
	server_conf *models.SwappableServerConfig,
) {
	if fn.Bound_to == nil || fn.Name == nil || fn.Version == nil {
		qm.Logger.Warn("Skipping function with missing required fields", "function", fn.Name)
		return
	}

	route := fmt.Sprintf("GET /%s/fn/%s", *fn.Bound_to, *fn.Name)
	qm.Logger.Debug("Registering declarative function", "route", route, "version", *fn.Version)

	// Resolve once for the model lookup at construction time. The handler
	// will look up live each request to honour hot-reloads.
	if _, ok := model_reg.ByEndpoint(*fn.Bound_to); !ok {
		qm.Logger.Warn("Bound model not found at registration; route registered but will 503 until model loads",
			"function", *fn.Name, "bound-to", *fn.Bound_to)
	}

	handler := getFunctionResource(fn, model_reg, qm, server_conf)
	handler_reg.Register(route, auth(handler), *fn.Version)
}

// getFunctionResource builds the per-request handler for a function. It applies
// the function's static `where`, declared parameters, and aggregate spec, then
// runs the same rows+count errgroup pattern as getResource.
func getFunctionResource(
	fn *models.FunctionDef,
	model_reg *tools.ModelRegistry,
	qm *tools.QueueManager,
	server_conf *models.SwappableServerConfig,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task_type := "Get Function"
		user_key := middleware.Contextkey("user")
		req_ip := tools.GetIP(r)
		req_id, err := tools.Generate32CharString()
		req_username := r.Context().Value(user_key).(*models.User).Username
		log := qm.Logger.With("user", req_username, "IP", req_ip, "function", *fn.Name, "end_point", *fn.Bound_to, "request_id", req_id)
		ctx := middleware.SetLogger(r.Context(), log)

		log.Info("REQUEST_RECEIVED")
		w.Header().Set("Content-Type", "application/json")
		response := map[string]any{"task_type": task_type, "function": *fn.Name}

		// Resolve the bound model at request time so model hot-reload is honoured.
		cfg, ok := model_reg.ByEndpoint(*fn.Bound_to)
		if !ok {
			log.Error("BOUND_MODEL_MISSING", "bound-to", *fn.Bound_to)
			http.Error(w, "Function's bound model is not loaded", http.StatusServiceUnavailable)
			return
		}

		// Permission check. Function-level roles-allowed wins; otherwise inherit
		// the model's GET allow-list.
		allowed := fn.Roles_allowed
		if len(allowed) == 0 {
			if cfg.End_points_allowed == nil || cfg.End_points_allowed.GET == nil {
				log.Warn("REQUEST_UNAUTHORISED", "error", "model GET disabled and no function role override")
				http.Error(w, "Function's bound model has GET disabled", http.StatusForbidden)
				return
			}
			allowed = cfg.End_points_allowed.GET
		}
		if !middleware.CheckUserHasAllowedRole(ctx, allowed, server_conf.Get()) {
			log.Warn("REQUEST_UNAUTHORISED", "error", "user role does not have permission")
			http.Error(w, "User role does not have access to this function", http.StatusUnauthorized)
			return
		}

		if err := r.ParseForm(); err != nil {
			log.Error("REQUEST_ERROR", "error", err)
			http.Error(w, "Could not parse request URL", http.StatusBadRequest)
			return
		}

		qb := tools.NewQueryBuilder(log)

		// 1. Static `where` — always-applied equality filters.
		for k, v := range fn.Where {
			qb.SetWhereAbsolute(k, v)
		}

		// 2. Declared parameters — translate URL values to WHERE clauses.
		if err := applyParameters(qb, fn.Parameters, cfg, r); err != nil {
			log.Error("REQUEST_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Parameter error: %v", err), http.StatusBadRequest)
			return
		}

		// 3. Build and apply the aggregate-shaped spec from YAML, with user
		//    sort_by appended as secondary sort tokens.
		spec := buildSpecFromFunction(fn, r)
		if err := qb.ApplyAggregateSpec(spec, cfg); err != nil {
			log.Error("REQUEST_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Spec error: %v", err), http.StatusBadRequest)
			return
		}

		// 4. Pagination (page / page_size) — `fields=` URL param is intentionally ignored.
		qb.ApplyPagination(r)

		// 5. SELECT column list. If the spec produced any qb.fields entries
		//    (group-by, aggregates, or YAML-declared fields) use those. Otherwise
		//    default to all columns of the bound model — same behaviour as the
		//    standard GET endpoint.
		var query string
		if qb.HasFields() {
			query = qb.BuildSelect(*cfg.Table_name, qb.GetFields())
		} else {
			query = qb.BuildSelect(*cfg.Table_name, tools.DynamicGetDatabaseColumns(cfg, false, false))
		}
		count_query := qb.BuildCountWithWhere(*cfg.Table_name)

		response["page"] = qb.GetPage()
		response["page_size"] = qb.GetPageSize()

		var (
			rows  pgx.Rows
			count pgx.Rows
		)
		g, ctx := errgroup.WithContext(ctx)

		g.Go(func() error {
			var err error
			rows, err = qm.Db.Query(ctx, query, qb.GetArgs()...)
			return err
		})

		g.Go(func() error {
			var err error
			count, err = qm.Db.Query(ctx, count_query, qb.GetArgs()...)
			return err
		})

		if err := g.Wait(); err != nil {
			log.Error("GET_ERROR", "error", err)
			http.Error(w, fmt.Sprintf("Error with the query:\n%v", err), http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		defer count.Close()

		response["data"], err = pgx.CollectRows(rows, pgx.RowToMap)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		response["total_count"], err = pgx.CollectOneRow(count, pgx.RowTo[int])
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(response)
	}
}

// applyParameters reads each declared parameter from r.FormValue and applies
// the corresponding WHERE clause via the appropriate QueryBuilder setter.
func applyParameters(
	qb *tools.QueryBuilder,
	params []models.Parameter,
	cfg *models.DataModel,
	r *http.Request,
) error {
	for _, p := range params {
		raw := r.FormValue(*p.Name)
		if raw == "" {
			if p.Required != nil && *p.Required {
				return fmt.Errorf("required parameter %q missing", *p.Name)
			}
			continue
		}

		field_name, ok := tools.CheckFieldGetValid(*p.Field, cfg)
		if !ok {
			return fmt.Errorf("parameter %q references invalid field", *p.Name)
		}
		field_cfg := cfg.Fields[field_name]
		if field_cfg.Type == nil {
			return fmt.Errorf("field %q has no type", field_name)
		}
		field_type, err := tools.DecodeFieldType(*field_cfg.Type)
		if err != nil {
			return err
		}

		// Explicit op wins over type-driven inference.
		if p.Op != nil && *p.Op != "" && *p.Op != "=" {
			qb.AppendWhere(field_name, *p.Op, raw)
			continue
		}

		// Equality — use absolute-match path when the field is configured for it.
		if (field_cfg.Absolute_match != nil && *field_cfg.Absolute_match) ||
			(p.Op != nil && *p.Op == "=") {
			if !tools.ValidateValue(field_type, raw) {
				continue
			}
			qb.SetWhereAbsolute(field_name, raw)
			continue
		}

		qb.SetWhere(field_name, raw, field_type)
	}
	return nil
}

// buildSpecFromFunction merges the function YAML's aggregate-shape declarations
// with the user's URL sort_by (appended as secondary sort).
func buildSpecFromFunction(fn *models.FunctionDef, r *http.Request) tools.AggregateSpec {
	plain_fields := make([]string, 0, len(fn.Fields))
	for _, f := range fn.Fields {
		// Calculated fields are rejected at validation time; defensive skip here.
		if f.IsCalculated() { continue }
		if f.Field != "" { plain_fields = append(plain_fields, f.Field) }
	}

	sort_by := append([]string{}, fn.Sort_by...)
	if user_sort := r.FormValue("sort_by"); user_sort != "" {
		for _, t := range strings.Split(user_sort, ",") {
			t = strings.TrimSpace(t)
			if t == "" { continue }
			// Skip duplicates (compare by base token, ignoring direction).
			base := strings.TrimSpace(strings.Split(t, "~")[0])
			if hasSortToken(sort_by, base) { continue }
			sort_by = append(sort_by, t)
		}
	}

	return tools.AggregateSpec{
		Fields:    plain_fields,
		GroupBy:   fn.Group_by,
		Aggregate: fn.Aggregate,
		SortBy:    sort_by,
	}
}

// hasSortToken returns true when any token in the slice has the same base
// (pre-`~`) as the candidate. Used to skip URL sort tokens that the YAML
// already covers.
func hasSortToken(tokens []string, base string) bool {
	for _, t := range tokens {
		if strings.TrimSpace(strings.Split(t, "~")[0]) == base {
			return true
		}
	}
	return slices.Contains(tokens, base)
}
