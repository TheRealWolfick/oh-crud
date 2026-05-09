package handlers

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/tools"
)

// OpenAPIHandler serves a dynamically generated OpenAPI 3.0.3 spec at GET /openapi.json.
type OpenAPIHandler struct {
	registry *tools.ModelRegistry
}

func NewOpenAPIHandler(registry *tools.ModelRegistry) *OpenAPIHandler {
	return &OpenAPIHandler{registry: registry}
}

func (h *OpenAPIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	spec := buildOpenAPISpec(h.registry.All())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(spec)
}

// ── OpenAPI types ─────────────────────────────────────────────────────────────

type oaSpec struct {
	OpenAPI    string                `json:"openapi"`
	Info       oaInfo                `json:"info"`
	Paths      map[string]oaPathItem `json:"paths"`
	Components oaComponents          `json:"components"`
}

type oaInfo struct {
	Title   string `json:"title"`
	Version string `json:"version"`
}

type oaComponents struct {
	Schemas map[string]oaSchema `json:"schemas"`
}

type oaSchema struct {
	Type        string               `json:"type,omitempty"`
	Format      string               `json:"format,omitempty"`
	Properties  map[string]*oaSchema `json:"properties,omitempty"`
	Items       *oaSchema            `json:"items,omitempty"`
	Required    []string             `json:"required,omitempty"`
	Nullable    bool                 `json:"nullable,omitempty"`
	Description string               `json:"description,omitempty"`
}

type oaSchemaRef struct {
	Ref   string    `json:"$ref,omitempty"`
	Type  string    `json:"type,omitempty"`
	Items *oaSchema `json:"items,omitempty"`
}

type oaPathItem struct {
	Get    *oaOperation `json:"get,omitempty"`
	Post   *oaOperation `json:"post,omitempty"`
	Put    *oaOperation `json:"put,omitempty"`
	Delete *oaOperation `json:"delete,omitempty"`
}

type oaOperation struct {
	Summary     string                `json:"summary"`
	OperationID string                `json:"operationId"`
	Tags        []string              `json:"tags,omitempty"`
	Parameters  []oaParameter         `json:"parameters,omitempty"`
	RequestBody *oaRequestBody        `json:"requestBody,omitempty"`
	Responses   map[string]oaResponse `json:"responses"`
}

type oaParameter struct {
	Name        string   `json:"name"`
	In          string   `json:"in"`
	Required    bool     `json:"required"`
	Description string   `json:"description,omitempty"`
	Schema      oaSchema `json:"schema"`
}

type oaRequestBody struct {
	Required bool                     `json:"required"`
	Content  map[string]oaMediaType   `json:"content"`
}

type oaMediaType struct {
	Schema oaSchemaRef `json:"schema"`
}

type oaResponse struct {
	Description string                   `json:"description"`
	Content     map[string]oaMediaType   `json:"content,omitempty"`
}

// ── Spec builder ──────────────────────────────────────────────────────────────

func buildOpenAPISpec(dataModels []models.DataModel) oaSpec {
	spec := oaSpec{
		OpenAPI: "3.0.3",
		Info:    oaInfo{Title: "Asset Data API", Version: "1.0.0"},
		Paths:   map[string]oaPathItem{},
		Components: oaComponents{
			Schemas: map[string]oaSchema{},
		},
	}

	for _, m := range dataModels {
		if m.Name == nil || m.End_point == nil {
			continue
		}

		schemaName := titleFirst(*m.Name)
		spec.Components.Schemas[schemaName] = buildModelSchema(m)
		schemaRef := "#/components/schemas/" + schemaName

		allowed := m.End_points_allowed
		tags := []string{*m.Name}

		// ── Standard CRUD path ──────────────────────────────────────────
		path := "/" + *m.End_point
		pi := oaPathItem{}

		if isEnabled(allowed, "GET") {
			pi.Get = &oaOperation{
				Summary:     "List " + *m.Name,
				OperationID: "list" + sanitizeName(*m.Name),
				Tags:        tags,
				Parameters:  buildGetParams(m, schemaRef),
				Responses: map[string]oaResponse{
					"200": {
						Description: "Success",
						Content: map[string]oaMediaType{
							"application/json": {Schema: oaSchemaRef{
								Type:  "array",
								Items: &oaSchema{Type: "object"},
							}},
						},
					},
				},
			}
		}
		if isEnabled(allowed, "POST") {
			pi.Post = &oaOperation{
				Summary:     "Create " + *m.Name,
				OperationID: "create" + sanitizeName(*m.Name),
				Tags:        tags,
				RequestBody: bodyRef(schemaRef, false),
				Responses:   taskResponse(),
			}
		}
		if isEnabled(allowed, "PUT") {
			pi.Put = &oaOperation{
				Summary:     "Update " + *m.Name,
				OperationID: "update" + sanitizeName(*m.Name),
				Tags:        tags,
				RequestBody: bodyRef(schemaRef, false),
				Responses:   taskResponse(),
			}
		}
		if isEnabled(allowed, "DELETE") {
			pi.Delete = &oaOperation{
				Summary:     "Delete " + *m.Name,
				OperationID: "delete" + sanitizeName(*m.Name),
				Tags:        tags,
				RequestBody: bodyRef(schemaRef, false),
				Responses:   taskResponse(),
			}
		}
		spec.Paths[path] = pi

		// ── Group path ──────────────────────────────────────────────────
		gpItem := oaPathItem{}
		hasGroup := false
		if isEnabled(allowed, "POST_GROUP") {
			hasGroup = true
			gpItem.Post = &oaOperation{
				Summary:     "Bulk create " + *m.Name,
				OperationID: "bulkCreate" + sanitizeName(*m.Name),
				Tags:        tags,
				RequestBody: bodyArrayRef(schemaRef),
				Responses:   taskResponse(),
			}
		}
		if isEnabled(allowed, "PUT_GROUP") {
			hasGroup = true
			gpItem.Put = &oaOperation{
				Summary:     "Bulk update " + *m.Name,
				OperationID: "bulkUpdate" + sanitizeName(*m.Name),
				Tags:        tags,
				RequestBody: bodyArrayRef(schemaRef),
				Responses:   taskResponse(),
			}
		}
		if isEnabled(allowed, "DELETE_GROUP") {
			hasGroup = true
			gpItem.Delete = &oaOperation{
				Summary:     "Bulk delete " + *m.Name,
				OperationID: "bulkDelete" + sanitizeName(*m.Name),
				Tags:        tags,
				RequestBody: bodyArrayRef(schemaRef),
				Responses:   taskResponse(),
			}
		}
		if hasGroup {
			spec.Paths["/"+*m.End_point+"/group"] = gpItem
		}

		// ── Diff paths ──────────────────────────────────────────────────
		if m.Allow_diff != nil && *m.Allow_diff {
			diffItem := oaPathItem{}
			diffItem.Get = &oaOperation{
				Summary:     "Get diffs for " + *m.Name,
				OperationID: "getDiff" + sanitizeName(*m.Name),
				Tags:        tags,
				Parameters: []oaParameter{
					{Name: "task_id", In: "query", Schema: oaSchema{Type: "string"}},
					{Name: "checksum", In: "query", Schema: oaSchema{Type: "string"}},
				},
				Responses: map[string]oaResponse{
					"200": {Description: "Diff records"},
				},
			}
			diffItem.Post = &oaOperation{
				Summary:     "Create diff for " + *m.Name,
				OperationID: "createDiff" + sanitizeName(*m.Name),
				Tags:        tags,
				RequestBody: bodyArrayRef(schemaRef),
				Responses:   taskResponse(),
			}
			diffItem.Put = &oaOperation{
				Summary:     "Action diff for " + *m.Name,
				OperationID: "actionDiff" + sanitizeName(*m.Name),
				Tags:        tags,
				Parameters: []oaParameter{
					{Name: "checksum", In: "query", Required: true, Schema: oaSchema{Type: "string"}},
				},
				Responses: map[string]oaResponse{
					"200": {Description: "Sync instructions"},
				},
			}
			spec.Paths["/"+*m.End_point+"/diff"] = diffItem
		}
	}

	return spec
}

// buildModelSchema converts a DataModel's fields into an OpenAPI schema object.
func buildModelSchema(m models.DataModel) oaSchema {
	schema := oaSchema{
		Type:       "object",
		Properties: map[string]*oaSchema{},
	}
	required := []string{}

	// Sort fields for deterministic output
	names := make([]string, 0, len(m.Fields))
	for n := range m.Fields {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		field := m.Fields[name]
		if field.JSON == nil || field.Type == nil {
			continue
		}
		fs := fieldTypeToSchema(*field.Type)
		if field.Nullable != nil && *field.Nullable {
			fs.Nullable = true
		}
		schema.Properties[*field.JSON] = &fs
		if field.Required_on_insert != nil && *field.Required_on_insert {
			required = append(required, *field.JSON)
		}
	}
	if len(required) > 0 {
		schema.Required = required
	}
	return schema
}

// buildGetParams returns query parameters for a GET endpoint: pagination, sort, and per-field filters.
func buildGetParams(m models.DataModel, _ string) []oaParameter {
	params := []oaParameter{
		{Name: "page", In: "query", Schema: oaSchema{Type: "integer"}, Description: "Page number (default 1)"},
		{Name: "page_size", In: "query", Schema: oaSchema{Type: "integer"}, Description: "Results per page (default 25)"},
		{Name: "sort_by", In: "query", Schema: oaSchema{Type: "string"}, Description: "Sort columns, e.g. field~asc,field2~desc"},
	}

	// Per-field filter params, sorted for deterministic output
	names := make([]string, 0, len(m.Fields))
	for n := range m.Fields {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		field := m.Fields[name]
		if field.JSON == nil || field.Type == nil {
			continue
		}
		fs := fieldTypeToSchema(*field.Type)
		params = append(params, oaParameter{
			Name:   *field.JSON,
			In:     "query",
			Schema: fs,
		})
	}
	return params
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func fieldTypeToSchema(typeName string) oaSchema {
	switch typeName {
	case "int":
		return oaSchema{Type: "integer"}
	case "float":
		return oaSchema{Type: "number"}
	case "bool":
		return oaSchema{Type: "boolean"}
	case "json":
		return oaSchema{Type: "object"}
	case "time":
		return oaSchema{Type: "string", Format: "date-time"}
	case "uuid":
		return oaSchema{Type: "string", Format: "uuid"}
	default:
		return oaSchema{Type: "string"}
	}
}

func isEnabled(allowed *models.End_pointsAllowed, method string) bool {
	if allowed == nil {
		return false
	}
	switch method {
	case "GET":
		return allowed.GET != nil
	case "POST":
		return allowed.POST != nil
	case "PUT":
		return allowed.PUT != nil
	case "DELETE":
		return allowed.DELETE != nil
	case "POST_GROUP":
		return allowed.POST_GROUP != nil
	case "PUT_GROUP":
		return allowed.PUT_GROUP != nil
	case "DELETE_GROUP":
		return allowed.DELETE_GROUP != nil
	}
	return false
}

func bodyRef(schemaRef string, array bool) *oaRequestBody {
	var ref oaSchemaRef
	if array {
		ref = oaSchemaRef{Type: "array", Items: &oaSchema{}}
		ref.Items = &oaSchema{}
		// Encode the $ref inside Items
		_ = schemaRef
	} else {
		ref = oaSchemaRef{Ref: schemaRef}
	}
	return &oaRequestBody{
		Required: true,
		Content:  map[string]oaMediaType{"application/json": {Schema: ref}},
	}
}

func bodyArrayRef(schemaRef string) *oaRequestBody {
	return &oaRequestBody{
		Required: true,
		Content: map[string]oaMediaType{
			"application/json": {Schema: oaSchemaRef{
				Type:  "array",
				Items: &oaSchema{Type: "object"},
			}},
		},
	}
}

func taskResponse() map[string]oaResponse {
	return map[string]oaResponse{
		"200": {
			Description: "Task queued",
			Content: map[string]oaMediaType{
				"application/json": {Schema: oaSchemaRef{Type: "object"}},
			},
		},
	}
}

func titleFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func sanitizeName(s string) string {
	result := strings.Builder{}
	nextUpper := true
	for _, c := range s {
		if c == ' ' || c == '-' || c == '_' {
			nextUpper = true
			continue
		}
		if nextUpper {
			result.WriteRune([]rune(strings.ToUpper(string(c)))[0])
			nextUpper = false
		} else {
			result.WriteRune(c)
		}
	}
	return result.String()
}
