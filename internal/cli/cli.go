package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/PlatformHelios/servicemap-graph-poc/internal/cmdb"
	"github.com/PlatformHelios/servicemap-graph-poc/internal/config"
	"github.com/PlatformHelios/servicemap-graph-poc/internal/geocode"
	"github.com/PlatformHelios/servicemap-graph-poc/internal/graph"
)

const usage = `Usage:
  servicemap init
  servicemap node <create|get|list|update|retire> --kind <kind> [options]
  servicemap relationship <create|get|list|update|retire> --kind <kind> [options]

Node kinds: identity, job-code, birthright, role, entitlement, group, ci, incident, change, event, request,
  workflow, workflow-step, workflow-run, task (the last four are managed through the workflow API or dashboard; here they can be
  listed, fetched, and retired: a finished run retires with its tasks, a pending task or active run is refused)
Groups carry name and description; membership is the member / member-of relationship from a group to an identity
Requests: requestType=vendor (Vendor Request Form) with the vendor's name, description, and contact details (contactName, contactPhone,
  contactEmail); created as state=draft and tied to the requester with drafted-request / drafted-request-for. Update state=submitted once
  the vendor name is set to submit (criticality is decided when the CI is created): the draft link is
  retired and form-submitted / submitted-by is created from the same identity. Submitted requests stay editable but callers cannot return
  them to draft. States: draft, submitted, in-review (an enabled workflow is running), fulfilled (the CI was created from the request),
  denied (every item of an access request was refused)
Access requests: requestType=access is raised complete through the dashboard's Access Request Form (API: GET /api/access-options?identityId=,
  POST /api/access-requests), not through node create. It asks for roles or entitlements the identity does not already hold (an application CI
  must has-role the role or be entitled-by the entitlement, and they need an Accountable owner); each item goes to its Accountable for approval, approved items go to the
  fulfilment step, and provisioning records permissions / permissioned-by from the role or entitlement to the identity
Workflows: built in the dashboard's Workflow Creator (API: /api/workflows) per catalog form, as ordered review or approval steps assigned
  to identities or groups, each exposing editable and required request fields. Submitting a form starts the enabled workflow; tasks are
  worked under Workflow Tasks (API: /api/tasks). Completing the last step creates the CI, links fulfilled-by / fulfills, and retires the
  request, its run, and its tasks (the CI is the live record; list them with --include-retired); returning a
  request sets it back to draft with returnComment / returnedAt for the requester to fix and resubmit
CI types: server, printer, data-connector, application, service, process, function, location, contract, vendor (fixed enum; add types through a reviewed software release)
CI categories: business, technology, security (required for service, process, and function CIs)
Contract types: basic-contract, msa, nda (required for contract CIs)
Vendor criticality: critical, important, business-support (required for vendor CIs, which also carry name, description, and
  contactName / contactPhone / contactEmail; phone numbers are stored as (555) 010-0100 or +<country code><digits>)
Application hosting: hosted=internal or external (required for application CIs); server, printer, data-connector and application CIs carry name and description
Location hours: hoursMonday..hoursSunday as HH:MM-HH:MM or "closed", with an IANA timezone property
Location coordinates: latitude/longitude/geoPrecision are set from the address automatically; pass both latitude and longitude to pin a site manually
Relationship kinds (forward / inverse): has-job-code / job-code-for, work-location / work-location-for (identity to location CI),
  member / member-of (group to identity), drafted-request / drafted-request-for and form-submitted / submitted-by (identity to request),
  requested-for / subject-of (request to identity), requests-access / requested-on (request to role or entitlement),
  workflow: has-step / step-of, next-step / previous-step, step-assigned-to / assigned-step, run-for / has-run, instance-of / has-instance,
  task-for / has-task, task-step / step-task, task-item / item-task (task to role or entitlement), task-assigned-to / assigned-task,
  acted-by / acted-on, fulfilled-by / fulfills (request to CI),
  qualifies-for / qualified-by, grants / granted-by (birthright to role or entitlement), includes / included-by, has-role / role-for (application CI to role),
  entitled-by / entitlement-for (application CI to entitlement),
  permissions / permissioned-by (role or entitlement to identity), affects / affected-by, changes / changed-by,
  assigned-to / assignee-of (incident to identity or group), observed-on / observed, depends-on / depended-on-by, hosts / hosted-by, uses / used-by, governs / governed-by (from a contract CI),
  provides / provided-by (service or function CI to service or function CI, or vendor CI to application, server, or printer CI),
  RACI on CIs, job codes, birthrights, roles, and entitlements: accountable / accountable-for (to identity), responsible / responsible-for,
  consulted / consulted-on, informed / informed-of (to identity or group)
  Relationships are stored once in the forward direction; the inverse is how the same edge reads from the other side.
  Retiring an edge moves it to the <TYPE>_RETIRED relationship type; --include-retired reads both.

Node options:
	--id <id>                    Required for get/update/retire; node IDs are assigned on create as PREFIX-000001
  --properties <json-object>   Properties for create/update
	--ci-ids <id,id,...>         Required for incident/change; optional for events and identities (work location); invalid for other nodes
  --include-retired            Include retired results for get/list (list shows at most 200 attached relationships per node; get shows all)

Relationship options:
  --from-id <id> --to-id <id>  Required except for list
  --properties <json-object>   Properties for create/update
  --include-retired            Include retired results for get/list

Connection environment:
  NEO4J_URI, NEO4J_USERNAME, NEO4J_PASSWORD, optional NEO4J_DATABASE

Example:
	servicemap node create --kind identity --properties '{"department":"Platform Engineering"}'
	servicemap node create --kind ci --properties '{"ciType":"server","name":"app-01"}'
	servicemap node create --kind incident --ci-ids CI-000001 --properties '{"name":"Application unavailable"}'
`

type command struct {
	group          string
	operation      string
	nodeKind       cmdb.NodeKind
	relationship   cmdb.RelationshipKind
	id             string
	fromID         string
	toID           string
	properties     map[string]any
	ciIDs          []string
	includeRetired bool
}

func Run(ctx context.Context, args []string, output io.Writer) error {
	parsed, err := parseCommand(args)
	if err != nil {
		return err
	}
	if parsed.group == "help" {
		_, err := io.WriteString(output, usage)
		return err
	}

	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	store, err := graph.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer store.Close(ctx)

	service := cmdb.NewService(store).WithGeocoder(geocode.NewCensus(nil))
	if parsed.group == "init" {
		if err := service.EnsureConstraints(ctx); err != nil {
			return err
		}
		return writeJSON(output, map[string]bool{"initialized": true})
	}
	result, err := execute(ctx, service, parsed)
	if err != nil {
		return err
	}
	return writeJSON(output, result)
}

func parseCommand(args []string) (command, error) {
	if len(args) == 0 || len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		return command{group: "help"}, nil
	}
	if args[0] == "init" {
		if len(args) != 1 {
			return command{}, fmt.Errorf("init does not accept arguments")
		}
		return command{group: "init"}, nil
	}
	if len(args) < 2 {
		return command{}, errors.New("expected node or relationship command; use --help for usage")
	}
	group, operation := args[0], args[1]
	if group != "node" && group != "relationship" {
		return command{}, fmt.Errorf("unknown command %q; use --help for usage", group)
	}
	switch operation {
	case "create", "get", "list", "update", "retire":
	default:
		return command{}, fmt.Errorf("unknown %s operation %q", group, operation)
	}

	flags := flag.NewFlagSet(group+" "+operation, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	kind := flags.String("kind", "", "node or relationship kind")
	id := flags.String("id", "", "node identifier for get/update/retire; assigned automatically on create")
	fromID := flags.String("from-id", "", "relationship source identifier")
	toID := flags.String("to-id", "", "relationship target identifier")
	propertiesJSON := flags.String("properties", "{}", "JSON object of properties")
	ciIDs := flags.String("ci-ids", "", "comma-separated CI identifiers for incident/change creation")
	includeRetired := flags.Bool("include-retired", false, "include retired records")
	if err := flags.Parse(args[2:]); err != nil {
		return command{}, err
	}
	if flags.NArg() != 0 {
		return command{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}

	parsed := command{group: group, operation: operation, includeRetired: *includeRetired}
	if *kind == "" {
		return command{}, fmt.Errorf("--kind is required")
	}
	if group == "node" {
		parsedKind, err := cmdb.ParseNodeKind(*kind)
		if err != nil {
			return command{}, err
		}
		parsed.nodeKind = parsedKind
		parsed.id = *id
		generatedID := operation == "create" && cmdb.RequiresGeneratedID(parsed.nodeKind)
		if operation != "list" && !generatedID && strings.TrimSpace(parsed.id) == "" {
			return command{}, fmt.Errorf("--id is required for node %s", operation)
		}
		if generatedID {
			if strings.TrimSpace(parsed.id) != "" {
				return command{}, fmt.Errorf("--id is assigned automatically for %s", parsed.nodeKind)
			}
			if strings.TrimSpace(*ciIDs) != "" && parsed.nodeKind != cmdb.Incident && parsed.nodeKind != cmdb.Change && parsed.nodeKind != cmdb.Event {
				return command{}, fmt.Errorf("--ci-ids is only valid when creating incident, change, or event records")
			}
			if cmdb.RequiresLinkedCIs(parsed.nodeKind) && strings.TrimSpace(*ciIDs) == "" {
				return command{}, fmt.Errorf("--ci-ids is required when creating an %s", parsed.nodeKind)
			}
			if strings.TrimSpace(*ciIDs) != "" {
				parsed.ciIDs = strings.Split(*ciIDs, ",")
			}
		} else if strings.TrimSpace(*ciIDs) != "" {
			return command{}, fmt.Errorf("--ci-ids is only valid when creating an incident, change, or event")
		}
	} else {
		parsedKind, err := cmdb.ParseRelationshipKind(*kind)
		if err != nil {
			return command{}, err
		}
		parsed.relationship = parsedKind
		parsed.fromID = *fromID
		parsed.toID = *toID
		if operation != "list" && (strings.TrimSpace(parsed.fromID) == "" || strings.TrimSpace(parsed.toID) == "") {
			return command{}, fmt.Errorf("--from-id and --to-id are required for relationship %s", operation)
		}
	}
	if operation == "create" || operation == "update" {
		properties, err := decodeProperties(*propertiesJSON)
		if err != nil {
			return command{}, err
		}
		parsed.properties = properties
	}
	return parsed, nil
}

func decodeProperties(value string) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(value))
	var properties map[string]any
	if err := decoder.Decode(&properties); err != nil {
		return nil, fmt.Errorf("parse --properties as a JSON object: %w", err)
	}
	if properties == nil {
		return nil, errors.New("--properties must be a JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("--properties must contain a single JSON object")
	}
	return properties, nil
}

func execute(ctx context.Context, service *cmdb.Service, parsed command) (any, error) {
	if parsed.group == "node" {
		switch parsed.operation {
		case "create":
			return service.CreateGeneratedNode(ctx, parsed.nodeKind, parsed.properties, parsed.ciIDs)
		case "get":
			return service.GetNode(ctx, parsed.nodeKind, parsed.id, parsed.includeRetired)
		case "list":
			nodes, _, err := service.ListNodes(ctx, parsed.nodeKind, cmdb.ListOptions{IncludeRetired: parsed.includeRetired})
			return nodes, err
		case "update":
			return service.UpdateNode(ctx, parsed.nodeKind, parsed.id, parsed.properties)
		case "retire":
			return service.RetireNode(ctx, parsed.nodeKind, parsed.id)
		}
	}
	switch parsed.operation {
	case "create":
		return service.CreateRelationship(ctx, parsed.relationship, parsed.fromID, parsed.toID, parsed.properties)
	case "get":
		return service.GetRelationship(ctx, parsed.relationship, parsed.fromID, parsed.toID, parsed.includeRetired)
	case "list":
		relationships, _, err := service.ListRelationships(ctx, parsed.relationship, cmdb.ListOptions{IncludeRetired: parsed.includeRetired})
		return relationships, err
	case "update":
		return service.UpdateRelationship(ctx, parsed.relationship, parsed.fromID, parsed.toID, parsed.properties)
	case "retire":
		return service.RetireRelationship(ctx, parsed.relationship, parsed.fromID, parsed.toID)
	}
	return nil, errors.New("unsupported command")
}

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
