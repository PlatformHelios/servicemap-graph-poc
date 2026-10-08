// Package catalogsdk is what a team imports to offer its Temporal workflows in
// the Service Catalog. The team describes the item next to the workflow, and
// the form is generated from the workflow's input type, so the form, the
// approvals, and the workflow always come from the same commit:
//
//	var ServerRequest = catalogsdk.Item{
//		Name: "server-request", Version: "1.0.0", Title: "Request a server",
//		Owner: "Network Engineering", Visibility: []string{"Platform Engineering"},
//		Approvals: []catalogsdk.Approval{{Name: "Network review", Assignees: []string{"Network Engineering"}}},
//		TaskQueue: "network-engineering", Workflow: ProvisionServer,
//		Input: ProvisionServerInput{}, Output: ProvisionServerResult{},
//	}
//
// CI then runs Build and Publish. Input fields are described with struct tags:
//
//	json:"name,omitempty"   field key; without omitempty the field is required
//	title:"..."             form label
//	description:"..."       help text
//	enum:"a,b,c"            allowed values (a dropdown)
//	default:"..."           initial value
//	minimum, maximum        number bounds
//	minLength, maxLength    string length bounds
//	pattern:"..."           string regular expression
//	portal:"cmdb:<ciType>"  the value is a CMDB record of that CI type (a picker)
//
// The package depends only on the standard library so any team can vendor it.
package catalogsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Item is a catalog item as a team declares it in code.
type Item struct {
	Name            string // stable slug, e.g. server-request
	Version         string // major.minor.patch; bump it whenever anything changes
	Title           string
	Description     string
	Owner           string   // owning group, by name or id
	Visibility      []string // groups whose members may raise it; the owner always can
	Approvals       []Approval
	SLABusinessDays int
	TaskQueue       string // where the team's worker listens
	Workflow        any    // the workflow function; its name is the workflow type
	WorkflowType    string // overrides the name taken from Workflow
	Input           any    // zero value of the workflow's input struct
	Output          any    // zero value of the workflow's result struct (optional)
	Source          string // where it was published from, e.g. repo@commit
}

// Approval is collected by the portal before the workflow starts.
type Approval struct {
	Name         string   `json:"name"`
	Assignees    []string `json:"assignees"`      // groups or identities, by name or id
	Rule         string   `json:"rule,omitempty"` // any (default) or all
	Instructions string   `json:"instructions,omitempty"`
}

// Manifest is the document the portal accepts at POST /api/catalog-items.
type Manifest struct {
	Name            string         `json:"name"`
	Version         string         `json:"version"`
	Title           string         `json:"title"`
	Description     string         `json:"description,omitempty"`
	Owner           string         `json:"owner"`
	Visibility      []string       `json:"visibility"`
	Approvals       []Approval     `json:"approvals,omitempty"`
	SLABusinessDays int            `json:"slaBusinessDays,omitempty"`
	Inputs          map[string]any `json:"inputs"`
	Outputs         map[string]any `json:"outputs,omitempty"`
	Target          Target         `json:"target"`
	Source          string         `json:"source,omitempty"`
}

type Target struct {
	TaskQueue    string `json:"taskQueue"`
	WorkflowType string `json:"workflowType"`
}

// Build turns an Item into the manifest the portal accepts.
func Build(item Item) (Manifest, error) {
	workflowType := item.WorkflowType
	if workflowType == "" {
		if item.Workflow == nil {
			return Manifest{}, errors.New("set Workflow (the workflow function) or WorkflowType")
		}
		name, err := functionName(item.Workflow)
		if err != nil {
			return Manifest{}, err
		}
		workflowType = name
	}
	if item.Input == nil {
		return Manifest{}, errors.New("set Input to the zero value of the workflow's input struct")
	}
	inputs, err := Schema(item.Input)
	if err != nil {
		return Manifest{}, fmt.Errorf("input schema: %w", err)
	}
	manifest := Manifest{
		Name: item.Name, Version: item.Version, Title: item.Title, Description: item.Description,
		Owner: item.Owner, Visibility: append([]string{}, item.Visibility...), Approvals: item.Approvals,
		SLABusinessDays: item.SLABusinessDays, Inputs: inputs, Source: item.Source,
		Target: Target{TaskQueue: item.TaskQueue, WorkflowType: workflowType},
	}
	if item.Output != nil {
		if manifest.Outputs, err = Schema(item.Output); err != nil {
			return Manifest{}, fmt.Errorf("output schema: %w", err)
		}
	}
	return manifest, nil
}

// functionName is the name Temporal registers a workflow function under by
// default: the function's name without its package path.
func functionName(fn any) (string, error) {
	value := reflect.ValueOf(fn)
	if value.Kind() != reflect.Func {
		return "", fmt.Errorf("Workflow must be a function, not %T", fn)
	}
	full := runtime.FuncForPC(value.Pointer()).Name()
	name := full[strings.LastIndex(full, ".")+1:]
	return strings.TrimSuffix(name, "-fm"), nil
}

// Schema generates a JSON Schema (draft 2020-12) for a flat struct of scalar
// fields. Field order is kept in x-order so the form shows fields as declared.
func Schema(value any) (map[string]any, error) {
	structType := reflect.TypeOf(value)
	for structType != nil && structType.Kind() == reflect.Pointer {
		structType = structType.Elem()
	}
	if structType == nil || structType.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%T is not a struct", value)
	}
	properties := map[string]any{}
	required := []string{}
	order := []string{}
	for index := 0; index < structType.NumField(); index++ {
		field := structType.Field(index)
		if !field.IsExported() {
			continue
		}
		key, optional := jsonName(field)
		if key == "-" {
			continue
		}
		property, err := fieldSchema(field)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", field.Name, err)
		}
		properties[key] = property
		order = append(order, key)
		if !optional {
			required = append(required, key)
		}
	}
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
		"x-order":              order,
	}, nil
}

func jsonName(field reflect.StructField) (string, bool) {
	tag := field.Tag.Get("json")
	name, options, _ := strings.Cut(tag, ",")
	if name == "" {
		name = field.Name
	}
	return name, strings.Contains(","+options+",", ",omitempty,")
}

func fieldSchema(field reflect.StructField) (map[string]any, error) {
	kind := field.Type.Kind()
	property := map[string]any{}
	switch kind {
	case reflect.String:
		property["type"] = "string"
	case reflect.Bool:
		property["type"] = "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		property["type"] = "integer"
	case reflect.Float32, reflect.Float64:
		property["type"] = "number"
	default:
		return nil, fmt.Errorf("catalog forms take strings, numbers, and booleans, not %s", field.Type)
	}
	for _, key := range []string{"title", "description", "pattern"} {
		if text := field.Tag.Get(key); text != "" {
			property[key] = text
		}
	}
	if property["title"] == nil {
		property["title"] = field.Name
	}
	if enum := field.Tag.Get("enum"); enum != "" {
		values := []any{}
		for _, item := range strings.Split(enum, ",") {
			value, err := parseValue(strings.TrimSpace(item), kind)
			if err != nil {
				return nil, fmt.Errorf("enum: %w", err)
			}
			values = append(values, value)
		}
		property["enum"] = values
	}
	if text, present := field.Tag.Lookup("default"); present {
		value, err := parseValue(text, kind)
		if err != nil {
			return nil, fmt.Errorf("default: %w", err)
		}
		property["default"] = value
	}
	for _, key := range []string{"minimum", "maximum", "minLength", "maxLength"} {
		if text := field.Tag.Get(key); text != "" {
			number, err := strconv.ParseFloat(text, 64)
			if err != nil {
				return nil, fmt.Errorf("%s must be a number, not %q", key, text)
			}
			property[key] = number
		}
	}
	if source := field.Tag.Get("portal"); source != "" {
		if kind != reflect.String {
			return nil, errors.New(`portal:"cmdb:<ciType>" fields hold the record id, so they must be strings`)
		}
		property["x-portal-source"] = source
	}
	return property, nil
}

func parseValue(text string, kind reflect.Kind) (any, error) {
	switch kind {
	case reflect.String:
		return text, nil
	case reflect.Bool:
		return strconv.ParseBool(text)
	case reflect.Float32, reflect.Float64:
		return strconv.ParseFloat(text, 64)
	default:
		return strconv.ParseInt(text, 10, 64)
	}
}

// Finding is one thing the portal's Workflow Analyzer flagged.
type Finding struct {
	Severity string `json:"severity"` // error, warning, info
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// Analysis is the part of the analyzer's answer a pipeline acts on.
type Analysis struct {
	Findings []Finding `json:"findings"`
}

// Blocking reports whether any finding would stop the item working (or stop
// it publishing), which a pipeline should treat as a failed check.
func (a Analysis) Blocking() bool {
	for _, finding := range a.Findings {
		if finding.Severity == "error" {
			return true
		}
	}
	return false
}

// Analyze asks the portal to check a manifest exactly as publishing would,
// against live groups, CMDB records, and Temporal workers, without storing
// it. Run it in CI before Publish.
func Analyze(ctx context.Context, portalURL, actorID string, manifest Manifest) (Analysis, error) {
	var analysis Analysis
	result, err := post(ctx, strings.TrimRight(portalURL, "/")+"/api/catalog-analysis", actorID, manifest)
	if err != nil {
		return analysis, fmt.Errorf("the portal could not analyze %s %s: %w", manifest.Name, manifest.Version, err)
	}
	return analysis, json.Unmarshal(result, &analysis)
}

// Publish sends a manifest to the portal. In CI the caller is the pipeline's
// identity, which must belong to the item's owner group and hold the
// catalog-publish entitlement. Until the portal has SSO that identity is
// passed in the X-Actor-Id header; with SSO it becomes the pipeline's OIDC token.
func Publish(ctx context.Context, portalURL, actorID string, manifest Manifest) (json.RawMessage, error) {
	result, err := post(ctx, strings.TrimRight(portalURL, "/")+"/api/catalog-items", actorID, manifest)
	if err != nil {
		return nil, fmt.Errorf("the portal refused %s %s: %w", manifest.Name, manifest.Version, err)
	}
	return result, nil
}

func post(ctx context.Context, url, actorID string, manifest Manifest) (json.RawMessage, error) {
	body, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Actor-Id", actorID)
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	result, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(result, &failure) == nil && failure.Error != "" {
			return nil, errors.New(failure.Error)
		}
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return result, nil
}
