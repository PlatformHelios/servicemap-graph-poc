# Example: a team's catalog item

This directory plays the network engineering team's own repository. It holds:

| File | What it is |
| --- | --- |
| [workflows.go](workflows.go) | The team's Temporal workflow, `ProvisionServer`, and its activities. The struct tags on `ProvisionServerInput` are the catalog form. |
| [catalog.go](catalog.go) | The catalog item that offers the workflow: owner, visibility, approvals, SLA, and version. |
| [main.go](main.go) | The commands the team's CI pipeline (`publish`) and worker deployment (`worker`) run, plus `seed` for this demo. |

It lives in this repository so the sample runs from one checkout. In practice it is the team's repository and imports `pkg/catalogsdk` as a dependency.

## Run the demo

Start the stack, including Temporal (`podman compose up -d`, or `docker compose up -d`). Then run the API from your working tree, because the published images don't include the catalog yet. If something else already uses ports 7233 or 8233 (for example another Temporal container), point `TEMPORAL_ADDRESS` at that server instead of starting the compose one.

```powershell
podman compose up -d --build api web temporal   # or run the API on the host as below
```

On the host:

```powershell
$env:NEO4J_URI="bolt://localhost:7687"; $env:NEO4J_USERNAME="neo4j"; $env:NEO4J_PASSWORD="your_password"
$env:TEMPORAL_ADDRESS="localhost:7233"
go run . serve
```

In other terminals:

```powershell
# 1. Demo people and groups: Priya Shah (Platform Engineering) requests,
#    Nora Quinn (Network Engineering) approves, and "Network Engineering CI"
#    is the pipeline identity that publishes. Prints their ids.
go run ./examples/network-team seed

# 2. The team's worker, which runs ProvisionServer.
go run ./examples/network-team worker

# 3. What CI runs: generate the manifest from code, check it, and publish it.
go run ./examples/network-team manifest                 # see what is sent
go run ./examples/network-team analyze -as <pipeline identity id>   # fails on blocking findings
go run ./examples/network-team publish -as <pipeline identity id> -source network-team@local
```

Then open the dashboard:

1. Use the user menu to act as **Priya Shah**. Open **Catalog requests > Service Catalog**, choose **Request a server**, fill in the form, and submit. The request is `in-review`.
2. Act as **Nora Quinn**. The approval is on **Home** and in **Workflow Tasks**. Approve it. The request moves to `in-progress`, and the Temporal UI (http://localhost:8233) shows `catalog-REQ-…` and its child `ProvisionServer`.
3. After about five seconds the request is `fulfilled`. Act as Priya again to see the hostname and IP address under **Result**.

Choose size **xlarge** to see a failed request: the workflow reports that there is no capacity, and the request records why. Deny an approval (a reason is required) to see a denied request.

Open **Catalog requests > Workflow Analyzer** to see the item's whole path as a diagram, with request counts, worker health, the activities Temporal ran, and findings. Two warnings show up on purpose: Network Engineering members can approve requests they raised themselves, and the pipeline identity sits in the approver group. Stop the worker and choose **Re-analyze** to see what a missing worker looks like.

## Change the item

Edit `catalog.go` or the input struct, bump `Version`, and publish again. If you publish the same version with different content, the portal refuses it. Requests already in review finish on the version they started with.

Publishing also checks policy. Remove the approval while `Visibility` still names Platform Engineering, and the publish is refused.
