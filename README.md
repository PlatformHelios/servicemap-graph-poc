# servicemap-graph-poc

A Go application for managing identity/access and CMDB records in Neo4j. The Go API is the backend for a React/Vite dashboard; a CLI is also available for terminal workflows.

## Quick start

Run every command from the repository root. Examples use Podman; with Docker, replace `podman` with `docker`.

### 1. Install the prerequisites

- [Podman](https://podman.io/docs/installation) with Compose (`podman compose version`), or [Docker](https://docs.docker.com/get-docker/) with Compose v2
- Free host ports 3000, 3100, 4317, 4318, 7474, 7687, 8080, 8081, 9090, and 9092. Stop any standalone Neo4j, OpenTelemetry collector, Loki, Grafana, Prometheus, or Kafka containers that already use them.
- Only for [developing on the host](#develop-on-the-host): Go 1.26 or later, and Node.js 20.19+ or 22.12+ with npm

### 2. Set environment variables

Compose reads `.env` from the repository root. It is optional; without it the defaults below apply.

```powershell
Copy-Item .env.example .env
```

| Variable | Default | Used for |
| --- | --- | --- |
| `NEO4J_PASSWORD` | `your_password` | Neo4j's initial password and the API's connection. At least 8 characters. Set it before the first start: Neo4j keeps the password it was created with until its volume is deleted. |

The API container's other settings (Neo4j URI, OTLP endpoint, listen address) are preset in `compose.yaml`; see [Configuration](#configuration) for everything the API reads.

### 3. Build the images

```powershell
podman compose build
```

This builds `api` (the Go API with the dashboard embedded) and `web` (the dashboard on nginx). The first build takes a few minutes.

### 4. Start the environment

```powershell
podman compose up -d
podman compose ps
```

The `api` container waits for Neo4j to report healthy (up to about two minutes on a first start) and applies the database schema itself; no separate `init` step is needed.

### 5. Open it

| What | URL |
| --- | --- |
| Dashboard | http://127.0.0.1:8080 |
| Swagger UI | http://127.0.0.1:8080/api/docs |
| Grafana (API logs under the Loki data source) | http://localhost:3000 |
| Neo4j Browser (user `neo4j`, password from step 2) | http://localhost:7474 |

Stop with `podman compose down`; add `-v` to also delete the data. After code changes, rebuild and restart with `podman compose up -d --build`.

To work on the code with live reload, run only the prerequisites in containers and the apps on the host; see [Develop on the host](#develop-on-the-host).

## Contents

- [Getting started](#getting-started)
- [Configuration](#configuration)
- [Web dashboard](#web-dashboard)
- [HTTP API](#http-api)
- [CLI](#cli)
- [Data model](#data-model)
- [Catalog requests and workflows](#catalog-requests-and-workflows)
- [Tests](#tests)

## Getting started

By default everything runs in containers. To work on the code with live reload, [develop on the host](#develop-on-the-host) instead.

### Run in containers

```powershell
podman compose up -d --build      # or: docker compose up -d --build
```

| Service | Host port | Purpose |
| --- | --- | --- |
| `api` | 127.0.0.1:8080 | Go API with the dashboard embedded, plus Swagger UI at `/api/docs` (`Dockerfile`). Waits for Neo4j, applies the schema on startup, and exports logs to the collector. |
| `web` | 127.0.0.1:8081 | Standalone dashboard served by nginx, proxying `/api` to the `api` service (`web/Dockerfile`). Can be deployed and scaled separately from the API. |
| `neo4j` | 7474 (browser), 7687 (Bolt) | Graph store. Password is `NEO4J_PASSWORD` from `.env` (see `.env.example`), default `your_password`. |
| `otel-collector` | 4317 (gRPC), 4318 (HTTP) | Receives OTLP from the API and forwards logs to Loki (`deploy/otel-collector.yaml`). |
| `loki` | 3100 | Log store, 24h retention (`deploy/loki.yaml`). |
| `prometheus` | 127.0.0.1:9090 | Scrapes itself, the collector, and Loki (`deploy/prometheus.yml`). |
| `grafana` | 3000 | Anonymous admin, with Loki and Prometheus data sources provisioned. |
| `kafka` | 9092 | Single-node KRaft broker. Containers on the compose network use `kafka:19092`. |

- **Rebuild after code changes:** run `podman compose up -d --build` again (or `podman compose up -d --build api web` for just the apps).
- **Stop:** `podman compose down`. Data lives in named volumes; add `-v` to delete it.
- **Logs:** `podman compose logs -f api`, or in Grafana under the Loki data source as `{service_name="servicemap-graph-poc"}`.
- **CLI:** the API image's entrypoint is the binary, so CLI commands run against the stack with `podman compose exec api /servicemap node list --kind identity`.

#### Building images directly

```powershell
podman build -t servicemap-api .         # context is the repo root
podman build -t servicemap-web web       # context is web/
```

The API image is a multi-stage build: it builds the dashboard with Node, embeds the output in the Go binary, and runs it on `distroless/static` as a non-root user, with `serve` as the default command. The web image listens on port 8080 and proxies `/api` to `API_UPSTREAM` (default `http://api:8080`). For example, against an API running on the host (Docker uses `host.docker.internal`):

```powershell
podman run --rm -p 8081:8080 -e API_UPSTREAM=http://host.containers.internal:8080 servicemap-web
```

### Develop on the host

Run the prerequisites in containers and the Go API and Vite dev server on the host. If the `api` container is running, stop it first with `podman compose stop api web`, since both use port 8080.

#### 1. Start the prerequisites

```powershell
podman compose up -d neo4j otel-collector loki prometheus grafana kafka
```

#### 2. Run the API

```powershell
$env:NEO4J_URI = "bolt://localhost:7687"
$env:NEO4J_USERNAME = "neo4j"
$env:NEO4J_PASSWORD = "your_password"
$env:OTEL_SERVICE_NAME = "servicemap-graph-poc"
$env:OTEL_EXPORTER_OTLP_ENDPOINT = "http://localhost:4318"

go run . init     # first run only: constraints, indexes, migrations
go run . serve
```

`init` needs an account with schema privileges. `serve` also applies constraints, indexes, and pending migrations at startup.

If `go env GOOS` reports something other than your OS (for example a persisted `go env -w GOOS=linux` for cross-compiling), `go run` and `go test` fail with "not a valid Win32 application". Override it for the terminal with `$env:GOOS = "windows"; $env:GOARCH = "amd64"`, or clear it with `go env -u GOOS GOARCH`.

#### 3. Run the dashboard

In a second terminal:

```powershell
Push-Location web
npm ci
npm run dev
```

Open `http://127.0.0.1:5173`. Vite proxies `/api` to the Go server at `http://127.0.0.1:8080`.

### Production build without containers

Build the frontend before building Go:

```powershell
Push-Location web
npm ci
npm run build
Pop-Location
go build -o cmdb.exe .
```

Vite writes the built app to `internal/httpapi/static`, where Go embeds and serves it, so Node.js is not needed at runtime. Start it with `go run . serve` or the built executable and open `http://127.0.0.1:8080`. The server binds to loopback by default and has no authentication; keep it on a trusted local machine.

## Configuration

| Variable | Required | Description |
| --- | --- | --- |
| `NEO4J_URI` | yes | Bolt URI, for example `bolt://localhost:7687`. |
| `NEO4J_USERNAME` | yes | Neo4j user. |
| `NEO4J_PASSWORD` | yes | Neo4j password. |
| `NEO4J_DATABASE` | no | Database name; defaults to the server's default database. |
| `HTTP_ADDR` | no | Listen address for `serve` (also `--addr`). Default `127.0.0.1:8080`; the container image sets `0.0.0.0:8080`. |
| `ACCESS_ANOMALY_INTERVAL` | no | How often access anomalies are recomputed. Default `15m`; `0` disables. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | no | OTLP/HTTP collector, for example `http://localhost:4318`. Unset logs to stdout only. |
| `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` | no | Logs-specific URL; overrides the general endpoint. |
| `OTEL_LOGS_EXPORTER` | no | Set to `none` to disable OTLP export. |
| `OTEL_SERVICE_NAME` | no | Service name on exported log records. |

### Logging

HTTP requests are always logged as JSON to stdout. When an OTLP endpoint is set, log records are also exported over OTLP/HTTP (to `/v1/logs` by default); standard OTLP header, TLS, timeout, and compression variables are supported.

Each log includes the method, route template, response status, duration, response size, and JSON bodies of `POST`/`PATCH` requests up to 8 KiB. Route templates are logged instead of raw paths so record IDs are not. Common password, token, secret, authorization, API-key, and cookie fields are redacted, and invalid or oversized bodies are omitted. Request bodies may still contain personal or business data, so configure log retention and access accordingly.

## Web dashboard

### Signed-in user

In the final rollout users sign in with Entra ID single sign-on, and what they can see follows from who they are. Until then the dashboard mocks sign-in:

- The user at the top of the rail is the **Platform Super Admin**, a development account with access to every view.
- The super admin can **act as** any configured identity (remembered for the browser session). The whole portal then follows that identity -- its Home page, its Workflow Tasks queue, and the default requester on the Access Request Form -- until **Back to super admin**.
- Every user lands on **Home**: their profile (department, location, job codes, groups, and the roles and entitlements they hold through birthrights, roles, or direct permissions), the work waiting on them (pending approvals and tasks, which open straight into the task dialog), and their open requests.
- The super admin's Home lists every open task and request on the platform instead. The super admin does not act on tasks directly but switches to an eligible identity to do so.

Anthos platform entitlements (for example `IAA-identities-read`, `CAT-incidents-admin`, `CREQ-accessrequestform-use`) control which tools an identity can use. The dashboard sends `X-Actor-Id` on every API call. The platform super admin (or a missing header, for local CLI tools) has full access; when acting as an identity, that identity's held entitlements (birthright, role includes, or direct permissions) resolve to capabilities that gate both the UI and the matching API routes. `GET /api/capabilities` returns the resolved set.

### Forms and pickers

- Forms use named fields for identifiers, names, department, and location. **Add attribute** adds other scalar properties (text, numbers, or booleans); relationship forms provide typed endpoints and optional attributes.
- Record pickers (RACI, incident assignment, affected CIs) are typeahead finders: type part of a name or ID and choose from the closest matches.
- Lists read 50 records at a time.

## HTTP API

The dashboard uses the same API as external clients. Interactive Swagger UI is at `http://127.0.0.1:8080/api/docs`, and the OpenAPI 3.1 document is at `/api/openapi.yaml` (Swagger UI assets are served locally with the embedded frontend).

```text
GET, POST           /api/nodes/{kind}
GET, PATCH, DELETE  /api/nodes/{kind}/{id}
GET, POST           /api/relationships/{kind}
GET, PATCH, DELETE  /api/relationships/{kind}/{fromId}/{toId}
GET, POST           /api/workflows
GET, PUT, DELETE    /api/workflows/{id}
GET                 /api/tasks
GET                 /api/tasks/{id}
POST                /api/tasks/{id}/actions
GET                 /api/access-options
POST                /api/access-requests
GET                 /api/access-anomalies
POST                /api/access-anomalies/refresh
GET                 /api/capabilities
GET                 /api/meta
GET                 /api/openapi.yaml
GET                 /api/docs
GET                 /api/health
```

- **Retirement:** `DELETE` marks a node or relationship retired; it never physically deletes it. Add `?includeRetired=true` to list/get requests to see retired records.
- **Paging:** pass `?limit=` (1-500) and `?offset=`, and read `X-Total-Count` / `X-Active-Count` from the response. Omit `limit` to read everything.
- **Search and filters:** `?q=` searches across a record's properties. The request list also accepts `?involvedId=` (requests an identity raised or that are for it) and `?requestType=vendor|access`.
- **Relationships in node responses:** a node list includes a sample of each node's directly attached relationships (at most 200 per node); `GET /api/nodes/{kind}/{id}` returns the full set.
- **Metadata:** `GET /api/meta` publishes the node and relationship kinds, forward and inverse labels, enums, and form/workflow definitions referenced throughout this README.

From PowerShell, build JSON and send it with `Invoke-RestMethod`:

```powershell
$body = @{ properties = @{ department = "Platform Engineering" } } | ConvertTo-Json -Depth 5
Invoke-RestMethod -Method Post -Uri "http://127.0.0.1:8080/api/nodes/identity" -ContentType "application/json" -Body $body
```

## CLI

All successful commands print JSON to stdout; errors go to stderr with a non-zero exit code. Run `go run . --help` for syntax.

Create a sample identity and access graph:

```powershell
$identity1 = go run . node create --kind identity --properties '{"department":"Platform Engineering"}' | ConvertFrom-Json
$identity2 = go run . node create --kind identity --properties '{"department":"Platform Engineering"}' | ConvertFrom-Json
$jobCode = go run . node create --kind job-code --properties '{"name":"Platform Engineer"}' | ConvertFrom-Json
$birthright = go run . node create --kind birthright --properties '{"name":"Platform Engineer"}' | ConvertFrom-Json
$role = go run . node create --kind role --properties '{"name":"Platform Engineering Role"}' | ConvertFrom-Json
$platformAdmin = go run . node create --kind entitlement --properties '{"name":"Platform Admin"}' | ConvertFrom-Json
$monitoringAdmin = go run . node create --kind entitlement --properties '{"name":"Monitoring Admin"}' | ConvertFrom-Json
$cloudAdmin = go run . node create --kind entitlement --properties '{"name":"Cloud Admin"}' | ConvertFrom-Json
go run . relationship create --kind HAS_JOB_CODE --from-id $identity1.id --to-id $jobCode.id
go run . relationship create --kind HAS_JOB_CODE --from-id $identity2.id --to-id $jobCode.id
go run . relationship create --kind QUALIFIES_FOR --from-id $jobCode.id --to-id $birthright.id
go run . relationship create --kind GRANTS --from-id $birthright.id --to-id $role.id
go run . relationship create --kind INCLUDES --from-id $role.id --to-id $platformAdmin.id
go run . relationship create --kind INCLUDES --from-id $role.id --to-id $monitoringAdmin.id
go run . relationship create --kind INCLUDES --from-id $role.id --to-id $cloudAdmin.id
```

Create CIs and link them:

```powershell
$server = go run . node create --kind ci --properties '{"ciType":"server","name":"app-01"}' | ConvertFrom-Json
$application = go run . node create --kind ci --properties '{"ciType":"application","name":"example-app"}' | ConvertFrom-Json
$connector = go run . node create --kind ci --properties '{"ciType":"data-connector","name":"example-connector"}' | ConvertFrom-Json
go run . node create --kind incident --ci-ids $server.id
go run . relationship create --kind DEPENDS_ON --from-id $server.id --to-id $connector.id
go run . node list --kind ci
```

Read, list, and update:

```powershell
go run . node get --kind identity --id $identity1.id
go run . node list --kind identity
go run . node update --kind identity --id $identity1.id --properties '{"location":"Remote"}'
go run . relationship list --kind HAS_JOB_CODE
```

`node list` includes a sample of each node's directly attached relationships (at most 200 per node; `node get` returns them all). It does not expand the full graph; use relationship lists to inspect other parts of the access chain. Retired relationships are omitted unless you pass `--include-retired`.

### Retirement

Retirement replaces deletion; there is no physical delete or reactivation command.

```powershell
go run . node retire --kind identity --id $identity1.id
go run . node get --kind identity --id $identity1.id --include-retired
go run . relationship get --kind HAS_JOB_CODE --from-id $identity1.id --to-id $jobCode.id --include-retired
```

- Retiring a node retires that node and its directly attached relationships in one transaction. Neighboring nodes and their other relationships are untouched.
- Every node carries `status` (`active` or `retired`, indexed per label). A retired relationship moves from its live type to a `<TYPE>_RETIRED` type with the same direction and properties (for example `HAS_JOB_CODE_RETIRED`), so live traversals never scan retired edges while history reads union both.
- `serve` and `init` apply constraints, indexes, and one-time migrations (status backfill, retired-edge move) at startup, recording each migration as a `CMDBMigration` node so it runs once.

## Data model

### Identifiers and properties

- IDs for every node kind are generated by the server; callers cannot supply them on create. Supply an existing ID only for get, update, retire, or relationship operations.
- Generated IDs use kind-specific prefixes such as `IDN-000001`, `JOB-000001`, `GRP-000001`, `CI-000001`, `INC-000001`, `REQ-000001`, `WFL-000001`, `STP-...`, `RUN-...`, and `TSK-...`. Existing IDs, including longer ULID-style IDs created during a brief generator change, are unchanged.
- For `job-code`, the generated ID is stored as the `code` property.
- Attributes are flat string, boolean, or numeric properties. Stable keys and lifecycle properties are managed by the application.

### Identity and access

Node kinds: `identity`, `job-code`, `birthright`, `role`, `entitlement`, and `group`. Groups carry a `name` and `description`; job codes, birthrights, roles, and entitlements carry a `description`.

```text
Identity -[HAS_JOB_CODE]-> JobCode
Group -[MEMBER]-> Identity
JobCode -[QUALIFIES_FOR]-> Birthright
Birthright -[GRANTS]-> Role or Entitlement
Role -[INCLUDES]-> Entitlement
```

An identity normally receives roles and entitlements through its job code's birthrights (`has-job-code` -> `qualifies-for` -> `grants`, plus entitlements included by those roles via `includes`; a birthright may also `grants` an entitlement directly). Access outside a birthright is an exception granted through an [access request](#access-requests).

Form pickers reconcile these links on save (creating new links and retiring ones that no longer apply):

| Form | Picker | Relationship |
| --- | --- | --- |
| Group | **Members** | `member` |
| Job code | **Birthrights** | `qualifies-for` |
| Birthright | **Job codes** | `qualifies-for` (typically 1:1, but more than one is allowed) |
| Birthright | **Roles**, **Entitlements** | `grants` |
| Role | **Included entitlements** | `includes` |
| Role, Entitlement | **Direct assignments** | `permissions` to identities |
| Identity | **Location** | `work-location` |
| Identity | **Job code and birthrights** | `has-job-code` |

The identity form's **Job code and birthrights** section is used on create and again when someone changes jobs. Because a birthright reaches an identity only through a job code, choosing a birthright resolves the job code that `qualifies-for` it (asking which, if several do) and reconciles `has-job-code` for the chosen and resolved job codes. A birthright no job code qualifies for cannot be chosen until that `qualifies-for` link exists.

**Access anomalies** (`GET /api/access-anomalies`, shown under the birthrights list) calls out identities that hold the same role or entitlement through more than one path: a birthright, a held role that `includes` the entitlement, and/or a direct `permissions` link. Because the check walks the whole access graph, the server recomputes it on a schedule (`ACCESS_ANOMALY_INTERVAL`) and stores the result in the graph; the panel shows that snapshot and when it was taken. The **Refresh** button (`POST /api/access-anomalies/refresh`) is only offered in development builds.

### Configuration items

A `ci` record must have a `ciType` from a fixed, code-reviewed enum. Adding a type or category requires a software change and redeployment; the API rejects unknown values. Every CI also carries a boolean `critical` flag (default false) for whether the item itself is business-critical.

| `ciType` | Properties |
| --- | --- |
| `server`, `printer`, `data-connector` | `name`, `description` |
| `application` | `name`, `description`, `hosted` (required: `internal` or `external`) |
| `service`, `process`, `function` | `name`, `description`, `category` (required at creation: `business`, `technology`, or `security`) |
| `location` | `name`, `siteId`, `addressLine1`, `addressLine2`, `city`, `state`, `postalCode`, `country`, `timezone` (IANA), `hoursMonday` ... `hoursSunday` |
| `contract` | `name`, `description`, `contractType` (required: `basic-contract`, `msa`, or `nda`) |
| `vendor` | `name`, `description`, `criticality` (required: `critical`, `important`, or `business-support`), `contactName`, `contactPhone`, `contactEmail` |

**Locations.** Hours are stored per weekday as values like `08:00-17:00` or `closed`; a closing time at or before the opening time runs past midnight. The API validates and normalizes these, and `cmdb.IsLocationOpenAt` evaluates whether a site is open at a given instant. When a location is created or its address changes, the server geocodes it with the US Census Bureau geocoder (`internal/geocode`, no API key needed) and stores `latitude`, `longitude`, and `geoPrecision` (`address` for a street match, `state` when it falls back to the state's center, or `manual` when coordinates are supplied directly). Geocoding failures never block a save. **Locations > Location Maps** plots sites on a US map (including Alaska and Hawaii), green when currently open and grey when closed.

**Vendors.** `criticality` is a vendor ranking, separate from the `critical` flag. `contactEmail` is validated and lower-cased. `contactPhone` is not free text: it is normalized to `(555) 010-0100` for North American numbers or `+` and digits for international ones, and incomplete numbers are rejected.

**Incidents, changes, and events.** Incident and change creation requires one or more active CI IDs; creation and linking are atomic, so if any CI is missing or retired the record is not created. Incidents carry a boolean `nodeDown` flag (default false) marking the affected node as unavailable, and an `assigned-to` link to the identity or group working them (the incident form's **Assigned to** picker). Events may optionally be linked to CIs.

This POC has no authentication, so administrative governance is through the reviewed release process, not a runtime admin role.

### Relationships

Every relationship is stored once, in its forward direction, and has a paired inverse label describing the same edge from the other endpoint. `GET /api/meta` returns both labels, plus `fromKinds` and `toKinds` for relationships whose endpoints may be more than one kind. The dashboard shows whichever label applies to the record you are viewing (in the record list, the Map inspector, and the Map edge labels for the selected node). For example, `CI-000001 -[hosts]-> CI-000002` reads as "CI-000001 hosts CI-000002" from the host and "CI-000002 hosted-by CI-000001" from the workload.

| Forward (from -> to) | Inverse (read from the `to` side) | Endpoints |
| --- | --- | --- |
| `has-job-code` | `job-code-for` | Identity -> Job code |
| `work-location` | `work-location-for` | Identity -> Location CI |
| `member` | `member-of` | Group -> Identity |
| `qualifies-for` | `qualified-by` | Job code -> Birthright |
| `grants` | `granted-by` | Birthright -> Role or Entitlement |
| `includes` | `included-by` | Role -> Entitlement |
| `has-role` | `role-for` | Application CI -> Role (a role belongs to one application) |
| `entitled-by` | `entitlement-for` | Application CI -> Entitlement (an entitlement belongs to one application) |
| `permissions` | `permissioned-by` | Role or Entitlement -> Identity (direct access outside a birthright; with `requestId`, `grantedAt`, `grantedBy`, `note`) |
| `drafted-request` | `drafted-request-for` | Identity -> Request (while the request is a draft) |
| `form-submitted` | `submitted-by` | Identity -> Request (once submitted) |
| `requested-for` | `subject-of` | Request -> Identity (who an access request is for) |
| `requests-access` | `requested-on` | Request -> Role or Entitlement (with `note`, `decision`, `decidedAt`, `decidedBy`, `fulfilledAt`) |
| `fulfilled-by` | `fulfills` | Request -> CI created from it |
| `has-step` | `step-of` | Workflow -> Workflow step |
| `next-step` | `previous-step` | Workflow step -> the step that follows |
| `step-assigned-to` | `assigned-step` | Workflow step -> Identity or Group |
| `run-for` | `has-run` | Workflow run -> Request |
| `instance-of` | `has-instance` | Workflow run -> Workflow |
| `task-for` | `has-task` | Task -> Workflow run |
| `task-step` | `step-task` | Task -> Workflow step |
| `task-item` | `item-task` | Task -> Role or Entitlement (the item an access task decides or provisions) |
| `task-assigned-to` | `assigned-task` | Task -> Identity or Group |
| `acted-by` | `acted-on` | Task -> Identity (with `action`, `comment`, `actedAt`) |
| `affects` | `affected-by` | Incident -> CI |
| `changes` | `changed-by` | Change -> CI |
| `assigned-to` | `assignee-of` | Incident -> Identity or Group |
| `observed-on` | `observed` | Event -> CI |
| `depends-on` | `depended-on-by` | CI (dependent) -> CI (dependency) |
| `hosts` | `hosted-by` | CI (host) -> CI (hosted workload) |
| `uses` | `used-by` | CI (consumer) -> CI (provider) |
| `governs` | `governed-by` | Contract CI -> CI (only contracts can start `governs`) |
| `provides` | `provided-by` | Service/Function CI -> Service/Function CI, or Vendor CI -> Application/Server/Printer CI |
| `accountable` | `accountable-for` | CI, Job code, Birthright, Role, or Entitlement -> Identity |
| `responsible` | `responsible-for` | CI, Job code, Birthright, Role, or Entitlement -> Identity or Group |
| `consulted` | `consulted-on` | CI, Job code, Birthright, Role, or Entitlement -> Identity or Group |
| `informed` | `informed-of` | CI, Job code, Birthright, Role, or Entitlement -> Identity or Group |

**RACI.** CIs, job codes, birthrights, roles, and entitlements (`raciKinds` in `GET /api/meta`) carry ownership through the last four relationships. Their dashboard forms have a **Details** tab and a **RACI** tab: Accountable is a single identity, while Responsible, Consulted, and Informed accept any mix of identities and groups. Saving the form creates or retires the matching relationships.

**CI type rules.** `provides` connects services and functions in any combination, and a vendor provides the applications, servers, and printers it supplies. These pairings are enforced per rule (a service cannot provide a server, and a vendor cannot provide a service); `GET /api/meta` publishes them as `ciTypeRules`, and the dashboard narrows the To list to match the chosen From record. `work-location` must end on a `location` CI; creating an identity with `ciIds` pointing at a location creates the link in the same request.

**Migrations.** The former `hosted-on` and `used-by` types were replaced by `hosts` and the inverse of `uses`, and the former `business-process` CI type was renamed `process`. `init` rewrites existing edges and records to the new forms.

## Catalog requests and workflows

`GET /api/meta` publishes `requestTypes` (`vendor`, `access`) and `requestStates` (`draft`, `submitted`, `in-review`, `fulfilled`, `denied`).

### Vendor requests

Some CIs are introduced through a request rather than created directly. A `request` record captures the data for a CI before it exists. The **Vendor Request Form** (`requestType` `vendor`) asks for the vendor's `name`, `description`, and contact details. The request mirrors the vendor CI's fields, but `criticality` is left to whoever creates the CI.

- Requests start in `draft` and may be saved incomplete. The form asks who the request is for and records it as `drafted-request` from that identity.
- Submitting sets `state` to `submitted`. The server checks the requester supplied what they must (vendor: `name`), stamps `submittedAt`, and replaces `drafted-request` with `form-submitted` from the same identity, in one write.
- A draft with no active requester cannot be submitted, callers cannot move a request back to draft, and `submittedAt` is managed by the application.
- Submitted requests remain editable (an admin role for this is planned); the requester is fixed once submitted.

**Catalog requests > Vendor Request Form** opens straight onto the form with **Save draft** and **Submit request**, and lists saved vendor requests beneath it so a draft can be reopened. Once submitted, the form also shows **Workflow progress**: each task raised, who it was assigned to, what they did, and whether the request SLA and step OLA are in or out of time.

### Access requests

Access outside a birthright is raised on **Catalog requests > Access Request Form** and granted as a direct `permissions` link from the role or entitlement to the identity.

- **What is offered:** a role or entitlement appears when it belongs to an application CI (`has-role` / `entitled-by`, set by the **Application** picker on its Details tab) and has an Accountable owner on its RACI tab. Items without both are hidden.
- **Choosing items:** the form asks who is **requesting** and who the access is **for**. `GET /api/access-options?identityId=` then lists, per application, what that identity may request, leaving out anything already held through a birthright, a held role, directly, or on another open request (returned as `held`, with how). Any number of items across applications may be ticked, each with an optional note for the approver.
- **Submitting:** there is no draft. `POST /api/access-requests` creates the request (`requestType` `access`, state `in-review`, `form-submitted` from the requester, `requested-for` the subject, one `requests-access` link per item with the note and a `decision` of `pending`) and starts the access workflow in the same write. Creating an `access` request through `POST /api/nodes/request` is refused.

The access workflow is a system workflow with a fixed shape, published as `accessWorkflowTemplate` in `GET /api/meta`:

1. **Approval**, with `assigneeRule` `item-accountable`. One approval task is raised per item, assigned to that item's Accountable (`task-item` names the item). The owner **approves** (decision `approved`, and a fulfilment task for that item is raised straight away while other items are still being decided) or **denies** with a reason (decision `denied`).
2. **Review (fulfilment)**, assigned to whoever provisions access, such as the `IAA-RequestFulfillment` group. They provision the access and mark it provisioned; the server sets the decision to `fulfilled` and creates the `permissions` link stamped with `requestId`, `grantedAt`, `grantedBy`, and the note.

In the Workflow Creator, choosing the Access Request Form loads this template with the steps locked; only step 2's assignees, the names, and the instructions can be changed. The workflow must be enabled before access requests can be submitted (none is seeded; the API explains what to do if it is missing).

Any item may be denied while the request stays active. When nothing is left pending or approved the request settles -- `fulfilled` if at least one item was provisioned, `denied` if every item was refused -- and the request, run, and tasks are retired as with vendor requests; the `permissions` links are the live record. Access task views carry `requestedFor`, the task's `item`, and `items` (every item with its decision), and the Access Request Form lists every access request with each item's decision and task trail.

### Workflows

Submitting a form starts a workflow when one is enabled for it. Workflows are built in **Catalog requests > Workflow Creator**, which lists saved workflows beneath their form. Reopen one to edit it; retired workflows appear under "Include retired", read in full, and can be copied into a new workflow.

**Definition.** A `workflow` has ordered `workflow-step` records linked with `has-step` and chained with `next-step`; each step is assigned to identities or groups with `step-assigned-to`. A step is either:

- a **review**: assignees look the form over, fill in the fields the step exposes, and complete it; or
- an **approval**: assignees approve under the step's rule, `any` one approver or `all` eligible approvers.

For each step the admin picks which request fields its assignees can edit and which of those are required before the step can be completed or approved. At most one workflow per form may be enabled, and an enabled workflow must require every CI property the requester does not supply (vendor: `criticality`), so a run can never dead-end. Steps run in order today; `order` and the `next-step` chain are kept so parallel branches can be added later. The workflow definition is never changed by runs.

**Runs and tasks.** When a request is submitted the server creates a `workflow-run` (`run-for` the request, `instance-of` the workflow), sets the request to `in-review`, and raises a `task` for the first step (`task-for` the run, `task-step` the step, `task-assigned-to` copied from the step).

Tasks are worked in **Catalog requests > Workflow Tasks** and from the user's Home page. The queue lists tasks assigned to the signed-in identity or to a group it belongs to. The user opens a task, enters the step's editable fields, and chooses **Complete** / **Approve** or **Return to requester** with a comment. Every action is recorded as `acted-by` from the task to the identity (with `action`, `comment`, and `actedAt`).

- **Completing a step** raises the next step's task.
- **Completing the last step** creates the CI from the request, links `fulfilled-by` from the request to the CI, sets the request to `fulfilled`, and retires the request, run, and tasks (with their relationships, except `fulfilled-by`) in one write. The CI is the live record; the closed-out request and tasks remain readable under "Include retired" / `includeDone=true`.
- **Returning a request** marks the task `rejected`, the run `returned`, and the request `draft` again with `returnComment` / `returnedAt` set and the `drafted-request` link restored. Resubmitting starts a fresh run.
- **Retiring by hand:** **Catalog requests > Workflow Runs** retires finished runs and their tasks (`DELETE /api/nodes/workflow-run/{id}`; an active run is refused), and Workflow Tasks retires completed, approved, or rejected tasks (`DELETE /api/nodes/task/{id}`; a pending task is refused).

Endpoints: `GET/POST /api/workflows`, `GET/PUT/DELETE /api/workflows/{id}`, `GET /api/tasks?actorId=&requestId=&includeDone=`, `GET /api/tasks/{id}`, and `POST /api/tasks/{id}/actions`. `GET /api/meta` publishes `requestFields`, `fulfilmentFields`, `stepTypes`, `approvalRules`, and `taskActions`. Workflow records are read-only through `/api/nodes/{kind}` and appear on the Map.

### SLA, OLA, and holidays

- **SLA** is configured on the workflow in Workflow Creator, as a number of business days. The Vendor and Access Request Forms show that a request has an SLA and how many days it allows.
- **OLA** can be enabled per workflow step with its own business-day target; the values are stamped onto each task when it is raised.
- The platform super admin's Home page has a **holiday calendar**. Marked weekdays are excluded from SLA and OLA counting, as are weekends.
- Task reviews show a filling circle for SLA and OLA that advances every 8 business hours from when the request or task starts: green while on track, yellow in the final 8-hour chunk, red when overdue, and a full green ring when met.

## Tests

```powershell
go test ./...
```

<<<<<<< HEAD
The Neo4j retirement integration test is skipped unless `NEO4J_URI`, `NEO4J_USERNAME`, `NEO4J_PASSWORD`, and `NEO4J_TEST_DATABASE` are set. It creates uniquely named records in that database and leaves them retired, so use a dedicated test database.
=======
The Neo4j retirement integration test is skipped unless `NEO4J_URI`, `NEO4J_USERNAME`, `NEO4J_PASSWORD`, and `NEO4J_TEST_DATABASE` are set. It creates uniquely named records in that database and leaves them retired, so configure a dedicated test database rather than a production database.

## Performance tests

[k6](https://k6.io/docs/get-started/installation/) tests live in `perf/`. `perf/smoke.js` ramps 10 virtual users over read-only API endpoints and fails if more than 1% of requests fail or p95 latency exceeds 500 ms. Start the server, then run:

```powershell
k6 run perf/smoke.js
k6 run -e BASE_URL=http://127.0.0.1:8080 -e ACTOR_ID=platform-super-admin perf/smoke.js
```
>>>>>>> 5ec9376678e7882802e7ea1e277f75d689d17945
