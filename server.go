package scim

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/elimity-com/scim/errors"
	"github.com/elimity-com/scim/schema"
	"github.com/scim2/filter-parser/v2"
)

const (
	defaultStartIndex = 1
	fallbackCount     = 100
)

func getFilter(r *http.Request) (filter.Expression, error) {
	rawFilter := strings.TrimSpace(r.URL.Query().Get("filter"))
	decodedFilter, _ := url.QueryUnescape(rawFilter)
	if decodedFilter != "" {
		return filter.ParseFilter([]byte(decodedFilter))
	}
	return nil, nil
}

func getIntQueryParam(r *http.Request, key string, def int) (int, error) {
	strVal := r.URL.Query().Get(key)

	if strVal == "" {
		return def, nil
	}

	if intVal, err := strconv.Atoi(strVal); err == nil {
		return intVal, nil
	}

	return 0, fmt.Errorf("invalid query parameter, \"%s\" must be an integer", key)
}

func parseIdentifier(path, endpoint string) (string, error) {
	return url.PathUnescape(strings.TrimPrefix(path, endpoint+"/"))
}

// Server represents a SCIM server which implements the HTTP-based SCIM protocol that makes managing identities in multi-
// domain scenarios easier to support via a standardized service.
type Server struct {
	Config        ServiceProviderConfig
	Prefix        string
	ResourceTypes []ResourceType
}

// ServeHTTP dispatches the request to the handler whose pattern most closely matches the request URL.
func (s Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/scim+json")

	path := strings.TrimPrefix(r.URL.Path, s.Prefix)

	switch {
	case path == "/Me":
		errorHandler(w, r, &errors.ScimError{
			Status: http.StatusNotImplemented,
		})
		return
	case path == "/Schemas" && r.Method == http.MethodGet:
		s.schemasHandler(w, r)
		return
	case strings.HasPrefix(path, "/Schemas/") && r.Method == http.MethodGet:
		s.schemaHandler(w, r, strings.TrimPrefix(path, "/Schemas/"))
		return
	case path == "/ResourceTypes" && r.Method == http.MethodGet:
		s.resourceTypesHandler(w, r)
		return
	case strings.HasPrefix(path, "/ResourceTypes/") && r.Method == http.MethodGet:
		s.resourceTypeHandler(w, r, strings.TrimPrefix(path, "/ResourceTypes/"))
		return
	case path == "/ServiceProviderConfig":
		s.serviceProviderConfigHandler(w, r)
		return
	case path == "/":
		// For Azure AD testing connectivity - it expects a 200 at the root
		w.WriteHeader(200)
		w.Write([]byte("OK"))
		return
	}

	for _, resourceType := range s.ResourceTypes {
		if path == resourceType.Endpoint {
			switch r.Method {
			case http.MethodPost:
				s.resourcePostHandler(w, r, resourceType)
				return
			case http.MethodGet:
				s.resourcesGetHandler(w, r, resourceType)
				return
			}
		}

		if strings.HasPrefix(path, resourceType.Endpoint+"/") {
			id, err := parseIdentifier(path, resourceType.Endpoint)
			if err != nil {
				break
			}

			switch r.Method {
			case http.MethodGet:
				s.resourceGetHandler(w, r, id, resourceType)
				return
			case http.MethodPut:
				s.resourcePutHandler(w, r, id, resourceType)
				return
			case http.MethodPatch:
				s.resourcePatchHandler(w, r, id, resourceType)
				return
			case http.MethodDelete:
				s.resourceDeleteHandler(w, r, id, resourceType)
				return
			}
		}
	}

	errorHandler(w, r, &errors.ScimError{
		Detail: "Specified endpoint does not exist.",
		Status: http.StatusNotFound,
	})
}

// getSchema extracts the schemas from the resources types defined in the server with given id.
func (s Server) getSchema(id string, r *http.Request) schema.Schema {
	for _, resourceType := range s.ResourceTypes {
		if resourceType.Schema.ID == id {
			return resourceType.Schema
		}
		for _, extension := range resourceType.SchemaExtensions {
			if extension.Schema.ID == id {
				if extension.LoadDynamically {
					return extension.SchemaLoader.LoadSchema(r)
				} else {
					return extension.Schema
				}
			}
		}
	}
	return schema.Schema{}
}

// getSchemas extracts all the schemas from the resources types defined in the server. Duplicate IDs will be ignored.
func (s Server) getSchemas(r *http.Request) []schema.Schema {
	ids := make([]string, 0)
	schemas := make([]schema.Schema, 0)
	for _, resourceType := range s.ResourceTypes {
		if !contains(ids, resourceType.Schema.ID) {
			schemas = append(schemas, resourceType.Schema)
		}
		ids = append(ids, resourceType.Schema.ID)
		for _, extension := range resourceType.SchemaExtensions {
			if !contains(ids, extension.Schema.ID) {
				if extension.LoadDynamically {
					schemas = append(schemas, extension.SchemaLoader.LoadSchema(r))
				} else {
					schemas = append(schemas, extension.Schema)
				}
			}
			ids = append(ids, extension.Schema.ID)
		}
	}
	return schemas
}

func (s Server) parseRequestParams(r *http.Request) (ListRequestParams, *errors.ScimError) {
	invalidParams := make([]string, 0)

	defaultCount := s.Config.getItemsPerPage()
	count, countErr := getIntQueryParam(r, "count", defaultCount)
	if countErr != nil {
		invalidParams = append(invalidParams, "count")
	}
	if count > defaultCount {
		// Ensure the count isn't more then the allowable max.
		count = defaultCount
	}
	if count < 0 {
		// A negative value shall be interpreted as 0.
		count = 0
	}

	startIndex, indexErr := getIntQueryParam(r, "startIndex", defaultStartIndex)
	if indexErr != nil {
		invalidParams = append(invalidParams, "startIndex")
	}
	if startIndex < 1 {
		startIndex = defaultStartIndex
	}

	if len(invalidParams) > 1 {
		scimErr := errors.ScimErrorBadParams(invalidParams)
		return ListRequestParams{}, &scimErr
	}

	filterExpr, filterExprErr := getFilter(r)
	if filterExprErr != nil {
		return ListRequestParams{}, &errors.ScimErrorInvalidFilter
	}

	return ListRequestParams{
		Count:      count,
		Filter:     filterExpr,
		StartIndex: startIndex,
	}, nil
}
