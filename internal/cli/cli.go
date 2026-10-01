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
	"github.com/PlatformHelios/servicemap-graph-poc/internal/graph"
)

const usage = `Usage:
  servicemap init
  servicemap node <create|get|list|update|retire> --kind <kind> [options]
  servicemap relationship <create|get|list|update|retire> --kind <kind> [options]

Node kinds: identity, job-code, birthright, role, entitlement
Relationship kinds: HAS_JOB_CODE, QUALIFIES_FOR, GRANTS, INCLUDES

Node options:
  --id <id>                    Required except for list
  --properties <json-object>   Properties for create/update
  --include-retired            Include retired results for get/list

Relationship options:
  --from-id <id> --to-id <id>  Required except for list
  --properties <json-object>   Properties for create/update
  --include-retired            Include retired results for get/list

Connection environment:
  NEO4J_URI, NEO4J_USERNAME, NEO4J_PASSWORD, optional NEO4J_DATABASE

Example:
  servicemap node create --kind identity --id e0001 --properties '{"department":"Platform Engineering"}'
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

	service := cmdb.NewService(store)
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
	id := flags.String("id", "", "node identifier")
	fromID := flags.String("from-id", "", "relationship source identifier")
	toID := flags.String("to-id", "", "relationship target identifier")
	propertiesJSON := flags.String("properties", "{}", "JSON object of properties")
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
		if operation != "list" && strings.TrimSpace(parsed.id) == "" {
			return command{}, fmt.Errorf("--id is required for node %s", operation)
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
			return service.CreateNode(ctx, parsed.nodeKind, parsed.id, parsed.properties)
		case "get":
			return service.GetNode(ctx, parsed.nodeKind, parsed.id, parsed.includeRetired)
		case "list":
			return service.ListNodes(ctx, parsed.nodeKind, parsed.includeRetired)
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
		return service.ListRelationships(ctx, parsed.relationship, parsed.includeRetired)
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
