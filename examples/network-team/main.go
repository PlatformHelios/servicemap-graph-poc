// Command network-team stands in for a team's own repository: it holds the
// team's Temporal workflow, the catalog item that offers it, and the commands
// its CI pipeline and its worker deployment run.
//
//	go run ./examples/network-team manifest            print the generated manifest
//	go run ./examples/network-team analyze -as ID      check it against the live platform (CI gate)
//	go run ./examples/network-team publish -as ID      publish it to the portal (what CI runs)
//	go run ./examples/network-team worker              run the team's Temporal worker
//	go run ./examples/network-team seed                create the demo groups and people
//
// It lives in this repository so the sample runs from one checkout; in
// practice it is the team's repository and imports catalogsdk as a dependency.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/PlatformHelios/servicemap-graph-poc/pkg/catalogsdk"
	"go.temporal.io/sdk/client"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/worker"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: network-team manifest | analyze | publish | worker | seed")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "manifest":
		err = printManifest()
	case "analyze":
		err = analyze(ctx, os.Args[2:])
	case "publish":
		err = publish(ctx, os.Args[2:])
	case "worker":
		err = runWorker(ctx, os.Args[2:])
	case "seed":
		err = seed(ctx, os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func printManifest() error {
	manifest, err := catalogsdk.Build(ServerRequest)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(manifest)
}

// analyze is the CI check before publishing: the portal's Workflow Analyzer
// looks at the item against live groups, CMDB records, and Temporal workers.
// Blocking findings fail the step.
func analyze(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("analyze", flag.ContinueOnError)
	portal := flags.String("portal", envOr("PORTAL_URL", "http://127.0.0.1:8080"), "portal base URL")
	actor := flags.String("as", os.Getenv("PORTAL_ACTOR"), "identity the pipeline runs as (a member of the owner group)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *actor == "" {
		return errors.New("-as is required: the pipeline identity (run seed to create one)")
	}
	manifest, err := catalogsdk.Build(ServerRequest)
	if err != nil {
		return err
	}
	analysis, err := catalogsdk.Analyze(ctx, *portal, *actor, manifest)
	if err != nil {
		return err
	}
	if len(analysis.Findings) == 0 {
		fmt.Printf("%s %s: no findings\n", manifest.Name, manifest.Version)
	}
	for _, finding := range analysis.Findings {
		fmt.Printf("%-7s %-24s %s\n", strings.ToUpper(finding.Severity), finding.Code, finding.Message)
	}
	if analysis.Blocking() {
		return errors.New("blocking findings: fix them before publishing")
	}
	return nil
}

// publish is the CI step: generate the manifest from code and send it.
func publish(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("publish", flag.ContinueOnError)
	portal := flags.String("portal", envOr("PORTAL_URL", "http://127.0.0.1:8080"), "portal base URL")
	actor := flags.String("as", os.Getenv("PORTAL_ACTOR"), "identity the pipeline publishes as (a member of the owner group)")
	source := flags.String("source", os.Getenv("CATALOG_SOURCE"), "where this was published from, e.g. repo@commit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *actor == "" {
		return errors.New("-as is required: the pipeline identity (run seed to create one)")
	}
	item := ServerRequest
	item.Source = *source
	manifest, err := catalogsdk.Build(item)
	if err != nil {
		return err
	}
	result, err := catalogsdk.Publish(ctx, *portal, *actor, manifest)
	if err != nil {
		return err
	}
	var published struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	}
	_ = json.Unmarshal(result, &published)
	fmt.Printf("Published %s %s as %s\n", manifest.Name, published.Version, published.ID)
	return nil
}

// runWorker is the team's deployment: it executes ProvisionServer whenever
// the portal hands it an approved request.
func runWorker(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("worker", flag.ContinueOnError)
	address := flags.String("temporal", envOr("TEMPORAL_ADDRESS", "localhost:7233"), "Temporal frontend address")
	namespace := flags.String("namespace", envOr("TEMPORAL_NAMESPACE", "default"), "Temporal namespace")
	if err := flags.Parse(args); err != nil {
		return err
	}
	temporalClient, err := client.Dial(client.Options{HostPort: *address, Namespace: *namespace, Logger: tlog.NewStructuredLogger(slog.Default())})
	if err != nil {
		return fmt.Errorf("connect to Temporal at %s: %w", *address, err)
	}
	defer temporalClient.Close()
	w := worker.New(temporalClient, TaskQueue, worker.Options{})
	w.RegisterWorkflow(ProvisionServer)
	w.RegisterActivity(AllocateAddress)
	w.RegisterActivity(BuildVirtualMachine)
	fmt.Printf("Network engineering worker listening on task queue %s\n", TaskQueue)
	return w.Run(stopOn(ctx))
}

func stopOn(ctx context.Context) <-chan any {
	stop := make(chan any)
	go func() {
		<-ctx.Done()
		close(stop)
	}()
	return stop
}

// seed creates the people and groups the sample needs, as the platform super
// admin. It finds records by name first, so it is safe to run again.
func seed(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("seed", flag.ContinueOnError)
	portal := flags.String("portal", envOr("PORTAL_URL", "http://127.0.0.1:8080"), "portal base URL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	api := portalClient{base: strings.TrimRight(*portal, "/")}
	ids := map[string]string{}
	for _, record := range []struct {
		kind, name string
		properties map[string]any
	}{
		{"entitlement", "CREQ-catalog-use", nil},
		{"entitlement", "CREQ-catalog-publish", nil},
		{"group", "Network Engineering", nil},
		{"group", "Platform Engineering", nil},
		{"identity", "Nora Quinn", map[string]any{"title": "Network engineer"}},
		{"identity", "Priya Shah", map[string]any{"title": "Platform engineer"}},
		{"identity", "Network Engineering CI", map[string]any{"title": "Pipeline identity for the network-team repository"}},
		{"ci", "Billing Portal", map[string]any{"ciType": "application", "critical": false, "hosted": "internal", "description": "Customer billing web application"}},
	} {
		id, err := api.findOrCreate(ctx, record.kind, record.name, record.properties)
		if err != nil {
			return err
		}
		ids[record.name] = id
	}
	for _, link := range []struct{ kind, from, to string }{
		{"member", "Network Engineering", "Nora Quinn"},
		{"member", "Network Engineering", "Network Engineering CI"},
		{"member", "Platform Engineering", "Priya Shah"},
		{"permissions", "CREQ-catalog-use", "Nora Quinn"},
		{"permissions", "CREQ-catalog-use", "Priya Shah"},
		{"permissions", "CREQ-catalog-publish", "Network Engineering CI"},
	} {
		if err := api.link(ctx, link.kind, ids[link.from], ids[link.to]); err != nil {
			return err
		}
	}
	fmt.Printf("Seeded. Requester Priya Shah = %s, approver Nora Quinn = %s, pipeline identity = %s, application Billing Portal = %s\n", ids["Priya Shah"], ids["Nora Quinn"], ids["Network Engineering CI"], ids["Billing Portal"])
	return nil
}

type portalClient struct{ base string }

func (p portalClient) call(ctx context.Context, method, path string, body any, into any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, p.base+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Actor-Id", "platform-super-admin")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode >= 300 {
		return fmt.Errorf("%s %s: HTTP %d %s", method, path, response.StatusCode, strings.TrimSpace(string(raw)))
	}
	if into != nil {
		return json.Unmarshal(raw, into)
	}
	return nil
}

func (p portalClient) findOrCreate(ctx context.Context, kind, name string, properties map[string]any) (string, error) {
	var existing []struct {
		ID         string         `json:"id"`
		Properties map[string]any `json:"properties"`
	}
	if err := p.call(ctx, http.MethodGet, "/api/nodes/"+kind+"?q="+url.QueryEscape(name), nil, &existing); err != nil {
		return "", err
	}
	for _, record := range existing {
		if record.Properties["name"] == name {
			return record.ID, nil
		}
	}
	all := map[string]any{"name": name}
	for key, value := range properties {
		all[key] = value
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := p.call(ctx, http.MethodPost, "/api/nodes/"+kind, map[string]any{"properties": all}, &created); err != nil {
		return "", err
	}
	fmt.Printf("created %s %s (%s)\n", kind, name, created.ID)
	return created.ID, nil
}

func (p portalClient) link(ctx context.Context, kind, from, to string) error {
	err := p.call(ctx, http.MethodGet, "/api/relationships/"+kind+"/"+url.PathEscape(from)+"/"+url.PathEscape(to), nil, nil)
	if err == nil {
		return nil
	}
	return p.call(ctx, http.MethodPost, "/api/relationships/"+kind, map[string]any{"fromId": from, "toId": to, "properties": map[string]any{}}, nil)
}
