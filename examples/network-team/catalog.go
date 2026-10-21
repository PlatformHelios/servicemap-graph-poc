package main

import "github.com/PlatformHelios/servicemap-graph-poc/pkg/catalogsdk"

// ServerRequest is the catalog item this team offers. The form comes from
// ProvisionServerInput and the result panel from ProvisionServerResult, so a
// change to the workflow's input ships with the form in the same commit. Bump
// Version whenever anything here or in those types changes.
var ServerRequest = catalogsdk.Item{
	Name:        "server-request",
	Version:     "1.0.0",
	Title:       "Request a server",
	Description: "A virtual machine with an address reserved for the application it hosts. Network Engineering reviews every request before it is built.",
	Owner:       "Network Engineering",
	Visibility:  []string{"Platform Engineering"},
	Approvals: []catalogsdk.Approval{
		{Name: "Network engineering review", Assignees: []string{"Network Engineering"}, Instructions: "Check the size and environment are sensible for the application, then approve to start the build."},
	},
	SLABusinessDays: 3,
	TaskQueue:       TaskQueue,
	Workflow:        ProvisionServer,
	Input:           ProvisionServerInput{},
	Output:          ProvisionServerResult{},
}

// TaskQueue is where this team's worker listens.
const TaskQueue = "network-engineering"
