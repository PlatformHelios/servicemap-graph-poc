package automation

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/PlatformHelios/servicemap-graph-poc/internal/cmdb"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/workflowservice/v1"
)

// What the Workflow Analyzer reads from Temporal: whether workers are polling
// a task queue, and how a workflow type's recent runs went, down to the
// activities they ran. It implements cmdb.AutomationInsight.

const (
	recentRunLimit   = 50 // runs summarized per workflow type
	historySampleMax = 5  // closed runs whose histories are read for activities
)

func (e *Engine) PortalTaskQueue() string {
	return e.taskQueue
}

func (e *Engine) UIURL() string {
	if e.uiURL == "" {
		return ""
	}
	return e.uiURL + "/namespaces/" + e.namespace
}

func (e *Engine) TaskQueueWorkers(ctx context.Context, taskQueue string) cmdb.WorkerStatus {
	status := cmdb.WorkerStatus{TaskQueue: taskQueue}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	identities := map[string]bool{}
	var lastSeen time.Time
	for _, queueType := range []enumspb.TaskQueueType{enumspb.TASK_QUEUE_TYPE_WORKFLOW, enumspb.TASK_QUEUE_TYPE_ACTIVITY} {
		response, err := e.client.DescribeTaskQueue(ctx, taskQueue, queueType)
		if err != nil {
			status.Error = err.Error()
			return status
		}
		for _, poller := range response.GetPollers() {
			identities[poller.GetIdentity()] = true
			if seen := poller.GetLastAccessTime().AsTime(); seen.After(lastSeen) {
				lastSeen = seen
			}
		}
		if queueType == enumspb.TASK_QUEUE_TYPE_WORKFLOW {
			status.WorkflowPollers = len(response.GetPollers())
		} else {
			status.ActivityPollers = len(response.GetPollers())
		}
	}
	for identity := range identities {
		status.Workers = append(status.Workers, identity)
	}
	sort.Strings(status.Workers)
	if !lastSeen.IsZero() {
		status.LastSeen = lastSeen.UTC().Format(time.RFC3339)
	}
	return status
}

func (e *Engine) RecentRuns(ctx context.Context, workflowType string) cmdb.RunSummary {
	summary := cmdb.RunSummary{WorkflowType: workflowType, StatusCounts: map[string]int{}, Activities: []cmdb.ActivityStat{}, Recent: []cmdb.RunRecord{}}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	response, err := e.client.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
		Namespace: e.namespace,
		PageSize:  recentRunLimit,
		Query:     fmt.Sprintf("WorkflowType = '%s'", strings.ReplaceAll(workflowType, "'", "")),
	})
	if err != nil {
		summary.Error = err.Error()
		return summary
	}
	var completedSeconds float64
	sampled := 0
	activities := map[string]*cmdb.ActivityStat{}
	for _, execution := range response.GetExecutions() {
		status := runStatus(execution.GetStatus())
		summary.Total++
		summary.StatusCounts[status]++
		record := cmdb.RunRecord{WorkflowID: execution.GetExecution().GetWorkflowId(), RunID: execution.GetExecution().GetRunId(), Status: status, StartedAt: execution.GetStartTime().AsTime().UTC().Format(time.RFC3339)}
		if execution.GetCloseTime() != nil {
			closed := execution.GetCloseTime().AsTime()
			record.ClosedAt = closed.UTC().Format(time.RFC3339)
			record.Seconds = closed.Sub(execution.GetStartTime().AsTime()).Seconds()
			if status == "completed" {
				completedSeconds += record.Seconds
			}
			if sampled < historySampleMax {
				sampled++
				e.sampleActivities(ctx, record.WorkflowID, record.RunID, activities)
			}
		}
		if len(summary.Recent) < 10 {
			summary.Recent = append(summary.Recent, record)
		}
	}
	if completed := summary.StatusCounts["completed"]; completed > 0 {
		summary.AverageSeconds = completedSeconds / float64(completed)
	}
	for _, stat := range activities {
		if stat.Completed > 0 {
			stat.AverageSeconds /= float64(stat.Completed)
		}
		summary.Activities = append(summary.Activities, *stat)
	}
	sort.Slice(summary.Activities, func(i, j int) bool { return summary.Activities[i].Position < summary.Activities[j].Position })
	return summary
}

// sampleActivities reads one run's history and adds what its activities did
// to the totals: how often each ran, failed, and how long it took. Position is
// where the activity first appears in a run, which orders them in the diagram.
func (e *Engine) sampleActivities(ctx context.Context, workflowID, runID string, activities map[string]*cmdb.ActivityStat) {
	iterator := e.client.GetWorkflowHistory(ctx, workflowID, runID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	scheduled := map[int64]*historypb.HistoryEvent{}
	position := 0
	for iterator.HasNext() {
		event, err := iterator.Next()
		if err != nil {
			return
		}
		switch event.GetEventType() {
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
			scheduled[event.GetEventId()] = event
			name := event.GetActivityTaskScheduledEventAttributes().GetActivityType().GetName()
			stat, ok := activities[name]
			if !ok {
				stat = &cmdb.ActivityStat{Name: name, Position: position}
				activities[name] = stat
			} else if position < stat.Position {
				stat.Position = position
			}
			position++
			stat.Scheduled++
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED:
			if start, ok := scheduled[event.GetActivityTaskCompletedEventAttributes().GetScheduledEventId()]; ok {
				stat := activities[start.GetActivityTaskScheduledEventAttributes().GetActivityType().GetName()]
				stat.Completed++
				stat.AverageSeconds += event.GetEventTime().AsTime().Sub(start.GetEventTime().AsTime()).Seconds()
			}
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_FAILED, enumspb.EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT:
			var scheduledID int64
			if attributes := event.GetActivityTaskFailedEventAttributes(); attributes != nil {
				scheduledID = attributes.GetScheduledEventId()
			} else {
				scheduledID = event.GetActivityTaskTimedOutEventAttributes().GetScheduledEventId()
			}
			if start, ok := scheduled[scheduledID]; ok {
				stat := activities[start.GetActivityTaskScheduledEventAttributes().GetActivityType().GetName()]
				stat.Failed++
				if attributes := event.GetActivityTaskFailedEventAttributes(); attributes != nil && stat.LastError == "" {
					stat.LastError = attributes.GetFailure().GetMessage()
				}
			}
		}
	}
}

func runStatus(status enumspb.WorkflowExecutionStatus) string {
	switch status {
	case enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING:
		return "running"
	case enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED:
		return "completed"
	case enumspb.WORKFLOW_EXECUTION_STATUS_FAILED:
		return "failed"
	case enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED:
		return "canceled"
	case enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED:
		return "terminated"
	case enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:
		return "timed-out"
	case enumspb.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW:
		return "continued"
	default:
		return "unknown"
	}
}
