package catalogsdk

import (
	"reflect"
	"testing"
)

type sampleInput struct {
	Hostname    string  `json:"hostname" title:"Hostname" pattern:"^[a-z]+$"`
	Size        string  `json:"size" enum:"small,large" default:"small"`
	Cores       int     `json:"cores,omitempty" minimum:"1" maximum:"64"`
	Ratio       float64 `json:"ratio,omitempty"`
	Backup      bool    `json:"backup,omitempty" default:"true"`
	Application string  `json:"application" portal:"cmdb:application"`
	Ignored     string  `json:"-"`
	internal    string
}

func SampleWorkflow() {}

func TestSchemaFromStructTags(t *testing.T) {
	schema, err := Schema(sampleInput{})
	if err != nil {
		t.Fatal(err)
	}
	if got := schema["required"]; !reflect.DeepEqual(got, []string{"hostname", "size", "application"}) {
		t.Errorf("required = %v", got)
	}
	if got := schema["x-order"]; !reflect.DeepEqual(got, []string{"hostname", "size", "cores", "ratio", "backup", "application"}) {
		t.Errorf("x-order = %v", got)
	}
	properties := schema["properties"].(map[string]any)
	if _, present := properties["Ignored"]; present {
		t.Error(`json:"-" fields must be left out`)
	}
	size := properties["size"].(map[string]any)
	if !reflect.DeepEqual(size["enum"], []any{"small", "large"}) || size["default"] != "small" || size["title"] != "Size" {
		t.Errorf("size = %v", size)
	}
	cores := properties["cores"].(map[string]any)
	if cores["type"] != "integer" || cores["minimum"] != float64(1) || cores["maximum"] != float64(64) {
		t.Errorf("cores = %v", cores)
	}
	if properties["ratio"].(map[string]any)["type"] != "number" || properties["backup"].(map[string]any)["default"] != true {
		t.Errorf("ratio/backup = %v / %v", properties["ratio"], properties["backup"])
	}
	if properties["application"].(map[string]any)["x-portal-source"] != "cmdb:application" {
		t.Errorf("application = %v", properties["application"])
	}
}

func TestSchemaRejectsWhatFormsCannotShow(t *testing.T) {
	if _, err := Schema(struct {
		Tags []string `json:"tags"`
	}{}); err == nil {
		t.Error("a slice field must be refused")
	}
	if _, err := Schema(struct {
		Count int `json:"count" portal:"cmdb:application"`
	}{}); err == nil {
		t.Error("a CMDB picker must be a string")
	}
	if _, err := Schema("not a struct"); err == nil {
		t.Error("a non-struct input must be refused")
	}
}

func TestBuildNamesTheWorkflowLikeTemporal(t *testing.T) {
	manifest, err := Build(Item{Name: "sample", Version: "1.0.0", Title: "Sample", Owner: "Team", TaskQueue: "team", Workflow: SampleWorkflow, Input: sampleInput{}})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Target.WorkflowType != "SampleWorkflow" || manifest.Target.TaskQueue != "team" {
		t.Errorf("target = %+v", manifest.Target)
	}
	if manifest.Visibility == nil {
		t.Error("visibility must encode as a list, not null")
	}
	if _, err := Build(Item{Name: "sample", Input: sampleInput{}}); err == nil {
		t.Error("an item without a workflow must be refused")
	}
}
