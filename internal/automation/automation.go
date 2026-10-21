// Package automation connects the Service Catalog to Temporal. Teams own the
// workflows that fulfil their catalog items and run them on their own task
// queues; the platform runs one small workflow of its own per approved
// request, which starts the team's workflow as a child and records what it
// returned on the request. Keeping that wrapper on the platform's side means
// the request is closed out durably however long the team's workflow takes,
// and gives the platform one place to add mid-workflow approvals later.
package automation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/PlatformHelios/servicemap-graph-poc/internal/cmdb"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// Names the platform registers on its own task queue.
const (
	FulfilmentWorkflowName = "CatalogFulfilment"
	RecordOutcomeActivity  = "RecordCatalogOutcome"
	DefaultTaskQueue       = "servicemap-portal"
)

// Config is read from TEMPORAL_ADDRESS, TEMPORAL_NAMESPACE, and
// TEMPORAL_TASK_QUEUE. Without an address the catalog has no automation.
type Config struct {
	Address   string
	Namespace string
	TaskQueue string
	UIURL     string // TEMPORAL_UI_URL, for links from the Workflow Analyzer
}

func ConfigFromEnv() Config {
	cfg := Config{
		Address:   strings.TrimSpace(os.Getenv("TEMPORAL_ADDRESS")),
		Namespace: strings.TrimSpace(os.Getenv("TEMPORAL_NAMESPACE")),
		TaskQueue: strings.TrimSpace(os.Getenv("TEMPORAL_TASK_QUEUE")),
		UIURL:     strings.TrimRight(strings.TrimSpace(os.Getenv("TEMPORAL_UI_URL")), "/"),
	}
	if cfg.Namespace == "" {
		cfg.Namespace = "default"
	}
	if cfg.TaskQueue == "" {
		cfg.TaskQueue = DefaultTaskQueue
	}
	return cfg
}

// Engine starts fulfilment workflows. It implements cmdb.CatalogAutomation.
type Engine struct {
	client    client.Client
	taskQueue string
	namespace string
	uiURL     string
}

// Connect makes a lazy client: the platform starts even while Temporal is
// down, and starting a workflow fails (marking the request failed) until it is up.
func Connect(cfg Config, logger *slog.Logger) (*Engine, error) {
	temporalClient, err := client.NewLazyClient(client.Options{HostPort: cfg.Address, Namespace: cfg.Namespace, Logger: tlog.NewStructuredLogger(logger)})
	if err != nil {
		return nil, fmt.Errorf("create Temporal client for %s: %w", cfg.Address, err)
	}
	return &Engine{client: temporalClient, taskQueue: cfg.TaskQueue, namespace: cfg.Namespace, uiURL: cfg.UIURL}, nil
}

func (e *Engine) Close() {
	e.client.Close()
}

// WorkflowID is the id a request's fulfilment runs under. It is derived from
// the request, so starting it twice finds the first run instead of a second.
func WorkflowID(requestID string) string {
	return "catalog-" + requestID
}

func (e *Engine) StartCatalogFulfilment(ctx context.Context, fulfilment cmdb.CatalogFulfilment) (string, error) {
	id := WorkflowID(fulfilment.RequestID)
	startCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := e.client.ExecuteWorkflow(startCtx, client.StartWorkflowOptions{
		ID:                    id,
		TaskQueue:             e.taskQueue,
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		Memo:                  map[string]any{"requestId": fulfilment.RequestID, "catalogItem": fulfilment.CatalogItem, "version": fulfilment.Version},
	}, FulfilmentWorkflowName, fulfilment)
	var started *serviceerror.WorkflowExecutionAlreadyStarted
	if err != nil && !errors.As(err, &started) {
		return "", err
	}
	return id, nil
}

// Fulfil is the platform's workflow for one approved request: run the team's
// workflow on its task queue, then record the outcome on the request.
func Fulfil(ctx workflow.Context, fulfilment cmdb.CatalogFulfilment) error {
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID: WorkflowID(fulfilment.RequestID) + "-" + fulfilment.WorkflowType,
		TaskQueue:  fulfilment.TaskQueue,
	})
	outcome := cmdb.CatalogOutcome{Succeeded: true}
	if err := workflow.ExecuteChildWorkflow(childCtx, fulfilment.WorkflowType, fulfilment.Inputs).Get(childCtx, &outcome.Outputs); err != nil {
		outcome = cmdb.CatalogOutcome{Error: failureMessage(err)}
	}
	activityCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{InitialInterval: time.Second, MaximumInterval: time.Minute},
	})
	return workflow.ExecuteActivity(activityCtx, RecordOutcomeActivity, fulfilment.RequestID, outcome).Get(activityCtx, nil)
}

// failureMessage keeps the team's own error text rather than Temporal's
// wrapping ("child workflow execution error (...): activity error (...)").
func failureMessage(err error) string {
	var application *temporal.ApplicationError
	if errors.As(err, &application) {
		return application.Message()
	}
	var timeout *temporal.TimeoutError
	if errors.As(err, &timeout) {
		return "the fulfilment workflow timed out"
	}
	var canceled *temporal.CanceledError
	if errors.As(err, &canceled) {
		return "the fulfilment workflow was cancelled"
	}
	return err.Error()
}

// Activities record outcomes through the catalog service.
type Activities struct {
	Service *cmdb.Service
}

func (a *Activities) RecordCatalogOutcome(ctx context.Context, requestID string, outcome cmdb.CatalogOutcome) error {
	_, err := a.Service.RecordCatalogOutcome(ctx, requestID, outcome)
	return err
}

// RunWorker runs the platform's worker until ctx ends, retrying while
// Temporal is unreachable so the API never waits on it to start.
func (e *Engine) RunWorker(ctx context.Context, service *cmdb.Service, logger *slog.Logger) {
	for ctx.Err() == nil {
		w := worker.New(e.client, e.taskQueue, worker.Options{})
		w.RegisterWorkflowWithOptions(Fulfil, workflow.RegisterOptions{Name: FulfilmentWorkflowName})
		w.RegisterActivityWithOptions((&Activities{Service: service}).RecordCatalogOutcome, activity.RegisterOptions{Name: RecordOutcomeActivity})
		if err := w.Start(); err != nil {
			logger.WarnContext(ctx, "Temporal worker could not start; retrying", "taskQueue", e.taskQueue, "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(15 * time.Second):
				continue
			}
		}
		logger.InfoContext(ctx, "Temporal worker started", "taskQueue", e.taskQueue)
		<-ctx.Done()
		w.Stop()
		return
	}
}
