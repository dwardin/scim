package scim

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/elimity-com/scim/errors"
	"github.com/elimity-com/scim/internal/filter"
	"github.com/elimity-com/scim/optional"
	"github.com/elimity-com/scim/schema"
)

// unmarshal unifies the unmarshal of the requests.
func unmarshal(data []byte, v interface{}) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	return d.Decode(v)
}

// ResourceType specifies the metadata about a resource type.
type ResourceType struct {
	// ID is the resource type's server unique id. This is often the same value as the "name" attribute.
	ID optional.String
	// Name is the resource type name. This name is referenced by the "meta.resourceType" attribute in all resources.
	Name string
	// Description is the resource type's human-readable description.
	Description optional.String
	// Endpoint is the resource type's HTTP-addressable endpoint relative to the Base URL of the service provider,
	// e.g., "/Users".
	Endpoint string
	// Schema is the resource type's primary/base schema.
	Schema schema.Schema
	// SchemaExtensions is a list of the resource type's schema extensions.
	SchemaExtensions []SchemaExtension

	// Handler is the set of callback method that connect the SCIM server with a provider of the resource type.
	Handler ResourceHandler
}

func (t ResourceType) getRaw() map[string]interface{} {
	return map[string]interface{}{
		"schemas":          []string{"urn:ietf:params:scim:schemas:core:2.0:ResourceType"},
		"id":               t.ID.Value(),
		"name":             t.Name,
		"description":      t.Description.Value(),
		"endpoint":         t.Endpoint,
		"schema":           t.Schema.ID,
		"schemaExtensions": t.getRawSchemaExtensions(),
	}
}

func (t ResourceType) getRawSchemaExtensions() []map[string]interface{} {
	schemas := make([]map[string]interface{}, 0)
	for _, e := range t.SchemaExtensions {
		schemas = append(schemas, map[string]interface{}{
			"schema":   e.Schema.ID,
			"required": e.Required,
		})
	}
	return schemas
}

func (t ResourceType) getSchemaExtensions(r *http.Request) []schema.Schema {
	var extensions []schema.Schema
	for _, e := range t.SchemaExtensions {
		if e.LoadDynamically {
			extensions = append(extensions, e.SchemaLoader.LoadSchema(r))
		} else {
			extensions = append(extensions, e.Schema)
		}
	}
	return extensions
}

func (t ResourceType) schemaWithCommon() schema.Schema {
	s := t.Schema

	externalID := schema.SimpleCoreAttribute(
		schema.SimpleStringParams(schema.StringParams{
			CaseExact:  true,
			Mutability: schema.AttributeMutabilityReadWrite(),
			Name:       schema.CommonAttributeExternalID,
			Uniqueness: schema.AttributeUniquenessNone(),
		}),
	)

	s.Attributes = append(s.Attributes, externalID)

	return s
}

func (t ResourceType) validate(raw []byte, method string, r *http.Request) (ResourceAttributes, *errors.ScimError) {
	var m map[string]interface{}
	if err := unmarshal(raw, &m); err != nil {
		return ResourceAttributes{}, &errors.ScimErrorInvalidSyntax
	}

	attributes, scimErr := t.schemaWithCommon().Validate(m)
	if scimErr != nil {
		return ResourceAttributes{}, scimErr
	}

	for _, extension := range t.SchemaExtensions {
		extensionField := m[extension.Schema.ID]
		if extensionField == nil {
			if extension.Required {
				err := errors.ScimError{
					ScimType: errors.ScimErrorInvalidValue.ScimType,
					Detail:   errors.ScimErrorInvalidValue.Detail + " Missing extension name: " + extension.Schema.Name.Value() + ", Extension ID: " + extension.Schema.ID,
					Status:   errors.ScimErrorInvalidValue.Status,
				}
				return ResourceAttributes{}, &err
			}
			continue
		}

		var extensionAttributes map[string]interface{}
		var scimErr *errors.ScimError

		if extension.LoadDynamically {
			extensionAttributes, scimErr = extension.SchemaLoader.LoadSchema(r).Validate(extensionField)
		} else {
			extensionAttributes, scimErr = extension.Schema.Validate(extensionField)
		}
		if scimErr != nil {
			return ResourceAttributes{}, scimErr
		}

		attributes[extension.Schema.ID] = extensionAttributes
	}

	return attributes, nil
}

func (t ResourceType) validateOperationValue(op PatchOperation, r *http.Request) (map[string]interface{}, *errors.ScimError) {
	var (
		path             = op.Path
		attributeName    = path.AttributePath.AttributeName
		subAttributeName = path.AttributePath.SubAttributeName()
	)
	// The sub attribute can come from: `emails.value` or `emails[type eq "work"].value`.
	// The path filter `x.y[].z` is impossible
	if subAttributeName == "" {
		subAttributeName = path.SubAttributeName()
	}

	var mapValue map[string]interface{}
	switch v := op.Value.(type) {
	case map[string]interface{}:
		mapValue = v

	default:
		if subAttributeName == "" {
			mapValue = map[string]interface{}{attributeName: v}
			break
		}
		mapValue = map[string]interface{}{attributeName: map[string]interface{}{
			subAttributeName: v,
		}}
	}

	// Check if it's a patch on an extension.
	if attributeName != "" {
		if id := path.AttributePath.URI(); id != "" {
			for _, ext := range t.SchemaExtensions {
				if strings.EqualFold(id, ext.Schema.ID) {
					if ext.LoadDynamically {
						return ext.SchemaLoader.LoadSchema(r).ValidatePatchOperation(op.Op, mapValue, true)
					} else {
						return ext.Schema.ValidatePatchOperation(op.Op, mapValue, true)
					}
				}
			}
		}
	}

	return t.schemaWithCommon().ValidatePatchOperationValue(op.Op, mapValue)
}

// validatePatch parse and validate PATCH request.
func (t ResourceType) validatePatch(r *http.Request) (PatchRequest, *errors.ScimError) {
	data, err := ioutil.ReadAll(r.Body)
	if err != nil {
		err := errors.ScimError{
			ScimType: errors.ScimErrorInvalidSyntax.ScimType,
			Detail:   errors.ScimErrorInvalidSyntax.Detail + " Failed to read request body. ",
			Status:   errors.ScimErrorInvalidSyntax.Status,
		}
		return PatchRequest{}, &err
	}

	var req struct {
		Schemas    []string
		Operations []struct {
			Op, Path string
			Value    interface{}
		}
	}
	if err := unmarshal(data, &req); err != nil {
		err := errors.ScimError{
			ScimType: errors.ScimErrorInvalidSyntax.ScimType,
			Detail:   errors.ScimErrorInvalidSyntax.Detail + " Failed to parse request body.",
			Status:   errors.ScimErrorInvalidSyntax.Status,
		}
		return PatchRequest{}, &err
	}

	// The body of an HTTP PATCH request MUST contain the attribute "Operations",
	// whose value is an array of one or more PATCH operations.
	if len(req.Operations) < 1 {
		err := errors.ScimError{
			ScimType: errors.ScimErrorInvalidValue.ScimType,
			Detail:   errors.ScimErrorInvalidValue.Detail + " Zero operations found in request body.",
			Status:   errors.ScimErrorInvalidValue.Status,
		}
		return PatchRequest{}, &err
	}

	// Evaluation continues until all operations are successfully applied or until an error condition is encountered.
	patchReq := PatchRequest{
		Schemas: req.Schemas,
	}
	for index, v := range req.Operations {
		validator, err := filter.NewPathValidator(v.Path, t.schemaWithCommon(), t.getSchemaExtensions(r)...)
		switch v.Op = strings.ToLower(v.Op); v.Op {
		case PatchOperationAdd, PatchOperationReplace:
			// Removed to allow Null values through on Add and Replace operations - to accomodate an Azure AD known issue when we may need to send a different default value in case of encountering a null - eg. a whitespace, and then logically translate it to a null which normally would be a removal but in those cases it comes through as a replace or add - PATCH - additional field - add and wipe using whitespace - test passed
			// if v.Value == nil {
			// 	err := errors.ScimError{
			// 		ScimType: errors.ScimErrorInvalidFilter.ScimType,
			// 		Detail:   errors.ScimErrorInvalidFilter.Detail + " Operation number: " + fmt.Sprint(index+1) + ", has a null value.",
			// 		Status:   errors.ScimErrorInvalidFilter.Status,
			// 	}
			// 	return PatchRequest{}, &err
			// }
			if v.Path != "" && err != nil {
				err2 := errors.ScimError{
					ScimType: errors.ScimErrorInvalidPath.ScimType,
					Detail:   errors.ScimErrorInvalidPath.Detail + " Operation number: " + fmt.Sprint(index+1) + ", has failed validation. " + err.Error(),
					Status:   errors.ScimErrorInvalidPath.Status,
				}
				return PatchRequest{}, &err2
			}
		case PatchOperationRemove:
			if err != nil {
				err2 := errors.ScimError{
					ScimType: errors.ScimErrorInvalidPath.ScimType,
					Detail:   errors.ScimErrorInvalidPath.Detail + " Operation number: " + fmt.Sprint(index+1) + ", has failed validation. " + err.Error(),
					Status:   errors.ScimErrorInvalidPath.Status,
				}
				return PatchRequest{}, &err2
			}
		default:
			err := errors.ScimError{
				ScimType: errors.ScimErrorInvalidFilter.ScimType,
				Detail:   errors.ScimErrorInvalidFilter.Detail + " Operation number: " + fmt.Sprint(index+1) + ", has an unrecognized operation type.",
				Status:   errors.ScimErrorInvalidFilter.Status,
			}
			return PatchRequest{}, &err
		}
		op := PatchOperation{
			Op:    strings.ToLower(v.Op),
			Value: v.Value,
		}

		// If err is nil, then it means that there is a valid path.
		if err == nil {
			if err := validator.Validate(); err != nil {
				err2 := errors.ScimError{
					ScimType: errors.ScimErrorInvalidPath.ScimType,
					Detail:   errors.ScimErrorInvalidPath.Detail + " Operation number: " + fmt.Sprint(index+1) + ", has failed validation. " + err.Error(),
					Status:   errors.ScimErrorInvalidPath.Status,
				}
				return PatchRequest{}, &err2
			}
			p := validator.Path()
			op.Path = &p

			val, err := t.validateOperationValue(op, r)

			if err != nil {
				err2 := errors.ScimError{
					ScimType: errors.ScimErrorInvalidValue.ScimType,
					Detail:   errors.ScimErrorInvalidValue.Detail + " Operation number: " + fmt.Sprint(index+1) + ", has failed validation. " + err.Error(),
					Status:   errors.ScimErrorInvalidValue.Status,
				}
				return PatchRequest{}, &err2
			} else {
				// set the value here - this is to support any coercion of auto-correction that may have happend in the validator itself - eg. coercing text booleans, back into boolean types - allowance for Azure AD not complying with the SCIM specification in certain places
				if op.Path.AttributePath.SubAttributeName() == "" {
					if op.Path.SubAttribute == nil || (*op.Path.SubAttribute) == "" {
						// active: true
						op.Value = val[op.Path.AttributePath.AttributeName]
					} else {
						// name.giveName: myName
						subAttrMap, ok := val[op.Path.AttributePath.AttributeName].(map[string]interface{})

						if ok && subAttrMap != nil {
							op.Value = subAttrMap[*op.Path.SubAttribute]
						}
					}
				} else {
					// name.giveName: myName
					subAttrMap, ok := val[op.Path.AttributePath.AttributeName].(map[string]interface{})

					if ok && subAttrMap != nil {
						op.Value = subAttrMap[op.Path.AttributePath.SubAttributeName()]
					}
				}
			}
		}
		patchReq.Operations = append(patchReq.Operations, op)
	}

	return patchReq, nil
}

// SchemaExtension is one of the resource type's schema extensions.
type SchemaExtension struct {
	// Schema is the URI of an extended schema, e.g., "urn:edu:2.0:Staff".
	Schema schema.Schema
	// Required is a boolean value that specifies whether or not the schema extension is required for the resource
	// type. If true, a resource of this type MUST include this schema extension and also include any attributes
	// declared as required in this schema extension. If false, a resource of this type MAY omit this schema
	// extension.
	Required bool
	// This was added to support schemas that get evaluated/constructed on the fly based on DB configs in a SaaS system
	LoadDynamically bool
	// Function that implements the loading mechanism
	SchemaLoader SchemaLoader
}

type SchemaLoader interface {
	// loads the schema in an arbitrary way - request is included for context
	LoadSchema(r *http.Request) schema.Schema
}
