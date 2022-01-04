package schema

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	datetime "github.com/di-wu/xsd-datetime"
	"github.com/elimity-com/scim/errors"
	"github.com/elimity-com/scim/optional"
)

// CoreAttribute represents those attributes that sit at the top level of the JSON object together with the common
// attributes (such as the resource "id").
type CoreAttribute struct {
	canonicalValues []string
	caseExact       bool
	description     optional.String
	multiValued     bool
	mutability      attributeMutability
	name            string
	referenceTypes  []AttributeReferenceType
	required        bool
	returned        attributeReturned
	subAttributes   Attributes
	typ             attributeType
	uniqueness      attributeUniqueness
}

var (
	validBooleanStrings = map[string]bool{"True": true, "False": false, "true": true, "false": false}
)

// ComplexCoreAttribute creates a complex attribute based on given parameters.
func ComplexCoreAttribute(params ComplexParams) CoreAttribute {
	checkAttributeName(params.Name)

	names := map[string]int{}
	var sa []CoreAttribute

	for i, a := range params.SubAttributes {
		name := strings.ToLower(a.name)
		if j, ok := names[name]; ok {
			panic(fmt.Errorf("duplicate name %q for sub-attributes %d and %d", name, i, j))
		}

		names[name] = i

		sa = append(sa, CoreAttribute{
			canonicalValues: a.canonicalValues,
			caseExact:       a.caseExact,
			description:     a.description,
			multiValued:     a.multiValued,
			mutability:      a.mutability,
			name:            a.name,
			referenceTypes:  a.referenceTypes,
			required:        a.required,
			returned:        a.returned,
			typ:             a.typ,
			uniqueness:      a.uniqueness,
		})
	}

	return CoreAttribute{
		description:   params.Description,
		multiValued:   params.MultiValued,
		mutability:    params.Mutability.m,
		name:          params.Name,
		required:      params.Required,
		returned:      params.Returned.r,
		subAttributes: sa,
		typ:           attributeDataTypeComplex,
		uniqueness:    params.Uniqueness.u,
	}
}

// SimpleCoreAttribute creates a non-complex attribute based on given parameters.
func SimpleCoreAttribute(params SimpleParams) CoreAttribute {
	checkAttributeName(params.name)

	return CoreAttribute{
		canonicalValues: params.canonicalValues,
		caseExact:       params.caseExact,
		description:     params.description,
		multiValued:     params.multiValued,
		mutability:      params.mutability,
		name:            params.name,
		referenceTypes:  params.referenceTypes,
		required:        params.required,
		returned:        params.returned,
		typ:             params.typ,
		uniqueness:      params.uniqueness,
	}
}

// AttributeType returns the attribute type.
func (a CoreAttribute) AttributeType() string {
	return a.typ.String()
}

// CanonicalValues returns the canonical values of the attribute.
func (a CoreAttribute) CanonicalValues() []string {
	return a.canonicalValues
}

// CaseExact returns whether the attribute is case exact.
func (a CoreAttribute) CaseExact() bool {
	return a.caseExact
}

// Description returns whether the description of the attribute.
func (a CoreAttribute) Description() string {
	return a.description.Value()
}

// HasSubAttributes returns whether the attribute is complex and has sub attributes.
func (a CoreAttribute) HasSubAttributes() bool {
	return a.typ == attributeDataTypeComplex && len(a.subAttributes) != 0
}

// MultiValued returns whether the attribute is multi valued.
func (a CoreAttribute) MultiValued() bool {
	return a.multiValued
}

// Mutability returns the mutability of the attribute.
func (a CoreAttribute) Mutability() string {
	raw, _ := a.mutability.MarshalJSON()
	return string(raw)
}

// Name returns the case insensitive name of the attribute.
func (a CoreAttribute) Name() string {
	return a.name
}

// ReferenceTypes returns the reference types of the attribute.
func (a CoreAttribute) ReferenceTypes() []AttributeReferenceType {
	return a.referenceTypes
}

// Required returns whether the attribute is required.
func (a CoreAttribute) Required() bool {
	return a.required
}

// Returned returns when the attribute need to be returned.
func (a CoreAttribute) Returned() string {
	raw, _ := a.returned.MarshalJSON()
	return string(raw)
}

// SubAttributes returns the sub attributes.
func (a CoreAttribute) SubAttributes() Attributes {
	return a.subAttributes
}

// Uniqueness returns the attributes uniqueness.
func (a CoreAttribute) Uniqueness() string {
	raw, _ := a.uniqueness.MarshalJSON()
	return string(raw)
}

func (a *CoreAttribute) getRawAttributes() map[string]interface{} {
	attributes := map[string]interface{}{
		"description": a.description.Value(),
		"multiValued": a.multiValued,
		"mutability":  a.mutability,
		"name":        a.name,
		"required":    a.required,
		"returned":    a.returned,
		"type":        a.typ,
	}

	if a.canonicalValues != nil {
		attributes["canonicalValues"] = a.canonicalValues
	}

	if a.referenceTypes != nil {
		attributes["referenceTypes"] = a.referenceTypes
	}

	rawSubAttributes := make([]map[string]interface{}, len(a.subAttributes))
	for i, subAttr := range a.subAttributes {
		rawSubAttributes[i] = subAttr.getRawAttributes()
	}

	if a.subAttributes != nil && len(a.subAttributes) != 0 {
		attributes["subAttributes"] = rawSubAttributes
	}

	if a.typ != attributeDataTypeComplex && a.typ != attributeDataTypeBoolean {
		attributes["caseExact"] = a.caseExact
		attributes["uniqueness"] = a.uniqueness
	}

	return attributes
}

func (a CoreAttribute) validate(attribute interface{}) (interface{}, *errors.ScimError) {
	// whether or not the attribute is required.
	if attribute == nil {
		if !a.required {
			return nil, nil
		}

		// the attribute is not present but required.
		err := errors.ScimError{
			ScimType: errors.ScimErrorInvalidValue.ScimType,
			Detail:   errors.ScimErrorInvalidValue.Detail + " Attribute name: " + a.name,
			Status:   errors.ScimErrorInvalidValue.Status,
		}
		return nil, &err
	}

	// whether the value of the attribute can be (re)defined
	// readOnly: the attribute SHALL NOT be modified.
	if a.mutability == attributeMutabilityReadOnly {
		return nil, nil
	}

	if !a.multiValued {
		return a.validateSingular(attribute)
	}

	switch arr := attribute.(type) {
	case map[string]interface{}:
		// return false if the multivalued attribute is empty.
		if a.required && len(arr) == 0 {
			err := errors.ScimError{
				ScimType: errors.ScimErrorInvalidValue.ScimType,
				Detail:   errors.ScimErrorInvalidValue.Detail + " Multivalued attribute was empty. Attribute name: " + a.name,
				Status:   errors.ScimErrorInvalidValue.Status,
			}
			return nil, &err
		}

		validMap := make(map[string]interface{}, len(arr))
		for k, v := range arr {
			for _, sub := range a.subAttributes {
				if !strings.EqualFold(sub.name, k) {
					continue
				}
				_, scimErr := sub.validate(v)
				if scimErr != nil {
					return nil, scimErr
				}
				validMap[sub.name] = v
			}
		}
		return validMap, nil

	case []interface{}:
		// return false if the multivalued attribute is empty.
		if a.required && len(arr) == 0 {
			err := errors.ScimError{
				ScimType: errors.ScimErrorInvalidValue.ScimType,
				Detail:   errors.ScimErrorInvalidValue.Detail + " Multivalued attribute was empty. Attribute name: " + a.name,
				Status:   errors.ScimErrorInvalidValue.Status,
			}
			return nil, &err
		}

		attributes := make([]interface{}, len(arr))
		for i, ele := range arr {
			attr, scimErr := a.validateSingular(ele)
			if scimErr != nil {
				return nil, scimErr
			}
			attributes[i] = attr
		}
		return attributes, nil

	default:
		// return false if the multivalued attribute is not a slice.
		err := errors.ScimError{
			ScimType: errors.ScimErrorInvalidValue.ScimType,
			Detail:   errors.ScimErrorInvalidValue.Detail + " Multivalued attribute was not an array. Attribute name: " + a.name,
			Status:   errors.ScimErrorInvalidValue.Status,
		}
		return nil, &err
	}
}

func (a CoreAttribute) validateSingular(attribute interface{}) (interface{}, *errors.ScimError) {
	switch a.typ {
	case attributeDataTypeBinary:
		bin, ok := attribute.(string)
		if !ok {
			err := errors.ScimError{
				ScimType: errors.ScimErrorInvalidValue.ScimType,
				Detail:   errors.ScimErrorInvalidValue.Detail + " Binary attribute not the right type. Attribute name: " + a.name,
				Status:   errors.ScimErrorInvalidValue.Status,
			}
			return nil, &err
		}

		match, err := regexp.MatchString(`^([A-Za-z0-9+/]{4})*([A-Za-z0-9+/]{3}=|[A-Za-z0-9+/]{2}==)?$`, bin)
		if err != nil {
			panic(err)
		}

		if !match {
			err := errors.ScimError{
				ScimType: errors.ScimErrorInvalidValue.ScimType,
				Detail:   errors.ScimErrorInvalidValue.Detail + " Attribute contains illegal characters for type: binary. Attribute name: " + a.name,
				Status:   errors.ScimErrorInvalidValue.Status,
			}
			return nil, &err
		}

		return bin, nil
	case attributeDataTypeBoolean:
		b, ok := attribute.(bool)

		// Azure AD sends booleans as strings in their PATCH operations - officially unrecognized bug, bug reported by a lot of people around the web - needs special handling like other places
		if !ok {
			s, s_ok := attribute.(string)

			// if a string, check if it's one of the right values
			if s_ok {
				if v, found := validBooleanStrings[s]; found {
					b = v     // set the value to the b variable which we ultimately return
					ok = true // overwrite the ok flag to prevent failure
					return b, nil
				} else {
					ok = false // failure - our attempt to parse the string gracefully failed
				}
			} else {
				ok = false // failure - our attempt to parse the string gracefully failed
			}
		}

		if !ok {
			err := errors.ScimError{
				ScimType: errors.ScimErrorInvalidValue.ScimType,
				Detail:   errors.ScimErrorInvalidValue.Detail + " Boolean attribute not the right type. Attribute name: " + a.name,
				Status:   errors.ScimErrorInvalidValue.Status,
			}
			return nil, &err
		}

		return b, nil
	case attributeDataTypeComplex:
		complex, ok := attribute.(map[string]interface{})
		if !ok {
			// manager must receive special treatment on behalf of Azure AD who submit the following style requests (non-compliant)
			/*
				{
					"schemas": [
						"urn:ietf:params:scim:api:messages:2.0:PatchOp"
					],
					"Operations": [
						{
							"op": "Add",
							"path": "urn:ietf:params:scim:schemas:extension:enterprise:2.0:User:manager", // <- this should end with manager.value
							"value": "274" // or this should be an object like this: "value": { "value": 274 }
						}
					]
				}
			*/
			if strings.EqualFold(strings.ToLower(a.name), "manager") {
				if manager, ok := attribute.(string); ok {
					return manager, nil // return the manager string
				} else {
					err := errors.ScimError{
						ScimType: errors.ScimErrorInvalidValue.ScimType,
						Detail:   errors.ScimErrorInvalidValue.Detail + " Complex attribute does not have the right structure. Attribute name: " + a.name,
						Status:   errors.ScimErrorInvalidValue.Status,
					}

					return nil, &err
				}
			} else {
				err := errors.ScimError{
					ScimType: errors.ScimErrorInvalidValue.ScimType,
					Detail:   errors.ScimErrorInvalidValue.Detail + " Complex attribute does not have the right structure. Attribute name: " + a.name,
					Status:   errors.ScimErrorInvalidValue.Status,
				}

				return nil, &err
			}
		}

		attributes := make(map[string]interface{})

		for _, sub := range a.subAttributes {
			var hit interface{}
			var found bool

			for k, v := range complex {
				if strings.EqualFold(sub.name, k) {
					if found {
						err := errors.ScimError{
							ScimType: errors.ScimErrorInvalidValue.ScimType,
							Detail:   errors.ScimErrorInvalidValue.Detail + " Duplicate attribute found inside of the complex attribute: " + a.name + ". Duplicate attribute name: " + sub.name,
							Status:   errors.ScimErrorInvalidValue.Status,
						}
						return nil, &err
					}

					found = true
					hit = v
				}
			}

			attr, scimErr := sub.validate(hit)
			if scimErr != nil {
				return nil, scimErr
			}

			attributes[sub.name] = attr
		}
		return attributes, nil
	case attributeDataTypeDateTime:
		date, ok := attribute.(string)
		if !ok {
			err := errors.ScimError{
				ScimType: errors.ScimErrorInvalidValue.ScimType,
				Detail:   errors.ScimErrorInvalidValue.Detail + " Date time attribute does not have the right type. Attribute name: " + a.name,
				Status:   errors.ScimErrorInvalidValue.Status,
			}
			return nil, &err
		}
		_, err := datetime.Parse(date)
		if err != nil {
			err := errors.ScimError{
				ScimType: errors.ScimErrorInvalidValue.ScimType,
				Detail:   errors.ScimErrorInvalidValue.Detail + " Date time attribute value is not in the right format - please ensure use supply date time in YYYY-MM-DDTHH:mm:ssZ format. Attribute name: " + a.name,
				Status:   errors.ScimErrorInvalidValue.Status,
			}
			return nil, &err
		}

		return date, nil
	case attributeDataTypeDecimal:
		switch n := attribute.(type) {
		case json.Number:
			f, err := n.Float64()
			if err != nil {
				err := errors.ScimError{
					ScimType: errors.ScimErrorInvalidValue.ScimType,
					Detail:   errors.ScimErrorInvalidValue.Detail + " Decimal attribute value failed to parse as a decimal. Attribute name: " + a.name,
					Status:   errors.ScimErrorInvalidValue.Status,
				}
				return nil, &err
			}

			return f, nil
		case float64:
			return n, nil
		default:
			err := errors.ScimError{
				ScimType: errors.ScimErrorInvalidValue.ScimType,
				Detail:   errors.ScimErrorInvalidValue.Detail + " Decimal attribute value failed submitted with wrong type. Attribute name: " + a.name,
				Status:   errors.ScimErrorInvalidValue.Status,
			}
			return nil, &err
		}
	case attributeDataTypeInteger:
		switch n := attribute.(type) {
		case json.Number:
			i, err := n.Int64()
			if err != nil {
				err := errors.ScimError{
					ScimType: errors.ScimErrorInvalidValue.ScimType,
					Detail:   errors.ScimErrorInvalidValue.Detail + " Integer attribute value failed to parse as an integer. Attribute name: " + a.name,
					Status:   errors.ScimErrorInvalidValue.Status,
				}
				return nil, &err
			}

			return i, nil
		case int, int8, int16, int32, int64:
			return n, nil
		default:
			err := errors.ScimError{
				ScimType: errors.ScimErrorInvalidValue.ScimType,
				Detail:   errors.ScimErrorInvalidValue.Detail + " Integer attribute value failed to parse as an integer. Attribute name: " + a.name,
				Status:   errors.ScimErrorInvalidValue.Status,
			}
			return nil, &err
		}
	case attributeDataTypeReference:
		s, ok := attribute.(string)
		if !ok {
			err := errors.ScimError{
				ScimType: errors.ScimErrorInvalidValue.ScimType,
				Detail:   errors.ScimErrorInvalidValue.Detail + " Reference attribute value is not of the right type. Attribute name: " + a.name,
				Status:   errors.ScimErrorInvalidValue.Status,
			}
			return nil, &err
		}

		return s, nil
	case attributeDataTypeString:
		s, ok := attribute.(string)
		if !ok {
			err := errors.ScimError{
				ScimType: errors.ScimErrorInvalidValue.ScimType,
				Detail:   errors.ScimErrorInvalidValue.Detail + " String attribute value is not of the right type. Attribute name: " + a.name,
				Status:   errors.ScimErrorInvalidValue.Status,
			}
			return nil, &err
		}

		return s, nil
	default:
		err := errors.ScimError{
			ScimType: errors.ScimErrorInvalidValue.ScimType,
			Detail:   errors.ScimErrorInvalidValue.Detail + " Unrecognized attribute type. Attribute name: " + a.name,
			Status:   errors.ScimErrorInvalidValue.Status,
		}
		return nil, &err
	}
}
