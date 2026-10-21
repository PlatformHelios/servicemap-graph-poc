package main

import (
	"context"
	"fmt"
	"hash/fnv"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// The network engineering team's automation: provisioning a server. The
// struct tags below are the form people fill in on the portal; catalog.go
// publishes them as the Request a server catalog item.

// ProvisionServerInput is the workflow's input and the catalog form.
type ProvisionServerInput struct {
	Hostname      string `json:"hostname" title:"Hostname" description:"Lowercase letters, digits, and hyphens." pattern:"^[a-z][a-z0-9-]{2,30}$"`
	Environment   string `json:"environment" title:"Environment" enum:"dev,test,prod" default:"dev"`
	Size          string `json:"size" title:"Size" enum:"small,medium,large,xlarge" default:"small" description:"There is no xlarge capacity in the lab, so xlarge shows what a failed request looks like."`
	Application   string `json:"application" title:"Application" portal:"cmdb:application" description:"The application this server will host."`
	Justification string `json:"justification,omitempty" title:"Justification" maxLength:"500"`
}

// ProvisionServerResult is what the requester sees once the server is built.
type ProvisionServerResult struct {
	Hostname   string `json:"hostname" title:"Hostname"`
	IPAddress  string `json:"ipAddress" title:"IP address"`
	Datacenter string `json:"datacenter" title:"Datacenter"`
}

// ProvisionServer reserves an address, then builds the virtual machine. Each
// step is an activity, so Temporal retries it and the workflow survives
// worker restarts mid-build.
func ProvisionServer(ctx workflow.Context, input ProvisionServerInput) (ProvisionServerResult, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	})
	logger := workflow.GetLogger(ctx)
	var address string
	if err := workflow.ExecuteActivity(ctx, AllocateAddress, input).Get(ctx, &address); err != nil {
		return ProvisionServerResult{}, err
	}
	logger.Info("address reserved", "hostname", input.Hostname, "ip", address)
	var result ProvisionServerResult
	if err := workflow.ExecuteActivity(ctx, BuildVirtualMachine, input, address).Get(ctx, &result); err != nil {
		return ProvisionServerResult{}, err
	}
	return result, nil
}

// AllocateAddress stands in for the team's IPAM call.
func AllocateAddress(ctx context.Context, input ProvisionServerInput) (string, error) {
	activity.GetLogger(ctx).Info("reserving an address", "hostname", input.Hostname, "environment", input.Environment)
	time.Sleep(2 * time.Second)
	hash := fnv.New32a()
	hash.Write([]byte(input.Hostname))
	sum := hash.Sum32()
	subnet := map[string]int{"dev": 10, "test": 20, "prod": 30}[input.Environment]
	return fmt.Sprintf("10.%d.%d.%d", subnet, sum%250+1, (sum>>8)%250+1), nil
}

// BuildVirtualMachine stands in for the team's hypervisor automation.
func BuildVirtualMachine(ctx context.Context, input ProvisionServerInput, address string) (ProvisionServerResult, error) {
	activity.GetLogger(ctx).Info("building the virtual machine", "hostname", input.Hostname, "size", input.Size)
	time.Sleep(3 * time.Second)
	datacenter := "lab-east-1"
	if input.Size == "xlarge" {
		return ProvisionServerResult{}, temporal.NewNonRetryableApplicationError(fmt.Sprintf("no xlarge capacity in %s; choose large or smaller", datacenter), "NoCapacity", nil)
	}
	return ProvisionServerResult{Hostname: input.Hostname + ".corp.example", IPAddress: address, Datacenter: datacenter}, nil
}
