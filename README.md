# servicemap-graph-poc

A Go CLI for managing an identity and access graph in Neo4j. The application layer is separate from the CLI so it can be reused by a future HTTP API.

## Requirements

- Go 1.26 or later
- Neo4j with Bolt connectivity enabled

## Configure the connection

In PowerShell, set the connection environment variables for the current terminal:

```powershell
$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:NEO4J_URI = "bolt://localhost:7687"
$env:NEO4J_USERNAME = "neo4j"
$env:NEO4J_PASSWORD = "your_password"
$env:NEO4J_DATABASE = "neo4j"
```

`NEO4J_DATABASE` is optional. For a Podman-managed named volume, mount the volume at `/data`; Neo4j's Bolt port must be published to the host, for example `--publish=7687:7687`.

Initialize uniqueness constraints once using an account with schema privileges:

```powershell
go run . init
```

## Graph model

The CLI manages these node kinds: `identity`, `job-code`, `birthright`, `role`, and `entitlement`. Relationships have a fixed direction and endpoint type:

```text
Identity -[HAS_JOB_CODE]-> JobCode
JobCode -[QUALIFIES_FOR]-> Birthright
Birthright -[GRANTS]-> Role
Role -[INCLUDES]-> Entitlement
```

`--id` is the stable key for each node kind; for `job-code`, it is stored as the `code` property. Identity attributes and other metadata are flat string, boolean, or numeric properties. Stable keys and lifecycle properties are managed by the application.

## Commands

Create the sample identity and access graph:

```powershell
go run . node create --kind identity --id e0001 --properties '{"department":"Platform Engineering"}'
go run . node create --kind identity --id e0002 --properties '{"department":"Platform Engineering"}'
go run . node create --kind job-code --id 0001 --properties '{"name":"Platform Engineer"}'
go run . node create --kind birthright --id platform-engineer --properties '{"name":"Platform Engineer"}'
go run . node create --kind role --id platform-engineering --properties '{"name":"Platform Engineering Role"}'
go run . node create --kind entitlement --id platform-admin --properties '{"name":"Platform Admin"}'
go run . node create --kind entitlement --id monitoring-admin --properties '{"name":"Monitoring Admin"}'
go run . node create --kind entitlement --id cloud-admin --properties '{"name":"Cloud Admin"}'
go run . relationship create --kind HAS_JOB_CODE --from-id e0001 --to-id 0001
go run . relationship create --kind HAS_JOB_CODE --from-id e0002 --to-id 0001
go run . relationship create --kind QUALIFIES_FOR --from-id 0001 --to-id platform-engineer
go run . relationship create --kind GRANTS --from-id platform-engineer --to-id platform-engineering
go run . relationship create --kind INCLUDES --from-id platform-engineering --to-id platform-admin
go run . relationship create --kind INCLUDES --from-id platform-engineering --to-id monitoring-admin
go run . relationship create --kind INCLUDES --from-id platform-engineering --to-id cloud-admin
```

Read, list, and update records:

```powershell
go run . node get --kind identity --id e0001
go run . node list --kind identity
go run . node update --kind identity --id e0001 --properties '{"location":"Remote"}'
go run . relationship list --kind HAS_JOB_CODE
```

`node list` includes each node's directly attached supported relationships. It does not expand the full graph; use separate relationship lists to inspect other parts of the access chain. By default, retired relationships are omitted, and `--include-retired` includes them.

Retirement replaces deletion. Retiring a node marks that node and its directly attached relationships as retired in one transaction. It does not retire neighboring nodes or their other relationships. Retired records are hidden from normal reads; add `--include-retired` to inspect them. There is no physical delete or reactivation command.

```powershell
go run . node retire --kind identity --id e0001
go run . node get --kind identity --id e0001 --include-retired
go run . relationship get --kind HAS_JOB_CODE --from-id e0001 --to-id 0001 --include-retired
```

All successful commands print JSON to standard output; errors go to standard error and return a non-zero exit code. Run `go run . --help` for command syntax.

## Web dashboard and API

Start the local dashboard after configuring Neo4j:

```powershell
go run . serve
```

Open `http://127.0.0.1:8080`. To use a different listen address, pass `--addr` or set `HTTP_ADDR`. The server binds to loopback by default and has no authentication; keep it on a trusted local machine.

HTTP requests are logged as JSON to stdout. To also export OpenTelemetry log records over OTLP/HTTP, configure a collector endpoint before starting the server:

```powershell
$env:OTEL_SERVICE_NAME = "servicemap-graph-poc"
$env:OTEL_EXPORTER_OTLP_ENDPOINT = "http://localhost:4318"
```

The exporter sends to `/v1/logs` by default; `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` can specify a logs-specific URL. Standard OTLP headers, TLS, timeout, and compression environment variables are supported. Logs include method, route template, response status, duration, response size, and JSON bodies for `POST`/`PATCH` requests up to 8 KiB. Common password, token, secret, authorization, API-key, and cookie fields are redacted; invalid or oversized bodies are omitted. Request bodies may contain personal or business data, so configure log retention and access accordingly. Route templates are used instead of raw paths so record IDs are not logged. Leave the endpoint unset to log locally only, or set `OTEL_LOGS_EXPORTER=none` to disable OTLP export.

The dashboard uses the same API as external clients:

```text
GET, POST       /api/nodes/{kind}
GET, PATCH, DELETE /api/nodes/{kind}/{id}
GET, POST       /api/relationships/{kind}
GET, PATCH, DELETE /api/relationships/{kind}/{fromId}/{toId}
GET             /api/meta
GET             /api/health
```

`DELETE` marks the node or relationship retired; it never physically deletes it. Add `?includeRetired=true` to list/get requests when you need retired records. A node-list response includes its directly attached supported relationships.

Dashboard forms use named fields for node identifiers, names, department, and location. Use **Add attribute** for other scalar properties; relationship forms provide typed endpoints and optional attributes. Attributes can be text, numbers, or booleans.

For PowerShell scripts, construct JSON and send it with `Invoke-RestMethod` rather than passing JSON as an argument to `go run`:

```powershell
$body = @{ id = "e0003"; properties = @{ department = "Platform Engineering" } } | ConvertTo-Json -Depth 5
Invoke-RestMethod -Method Post -Uri "http://127.0.0.1:8080/api/nodes/identity" -ContentType "application/json" -Body $body
```

## Tests

Run unit tests with:

```powershell
go test ./...
```

The Neo4j retirement integration test is skipped unless `NEO4J_URI`, `NEO4J_USERNAME`, `NEO4J_PASSWORD`, and `NEO4J_TEST_DATABASE` are set. It creates uniquely named records in that database and leaves them retired, so configure a dedicated test database rather than a production database.
