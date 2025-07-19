package terraform

import (
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
)

func TestHasECSResourceChanges(t *testing.T) {
	tests := []struct {
		name     string
		plan     *tfjson.Plan
		expected bool
	}{
		{
			name:     "nil plan",
			plan:     nil,
			expected: false,
		},
		{
			name: "no resource changes",
			plan: &tfjson.Plan{
				ResourceChanges: nil,
			},
			expected: false,
		},
		{
			name: "non-ECS resource changes",
			plan: &tfjson.Plan{
				ResourceChanges: []*tfjson.ResourceChange{
					{
						Type: "aws_s3_bucket",
						Change: &tfjson.Change{
							Actions: []tfjson.Action{"create"},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "ECS service create",
			plan: &tfjson.Plan{
				ResourceChanges: []*tfjson.ResourceChange{
					{
						Type: "aws_ecs_service",
						Change: &tfjson.Change{
							Actions: []tfjson.Action{"create"},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "ECS task definition update",
			plan: &tfjson.Plan{
				ResourceChanges: []*tfjson.ResourceChange{
					{
						Type: "aws_ecs_task_definition",
						Change: &tfjson.Change{
							Actions: []tfjson.Action{"update"},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "ECS service no-op",
			plan: &tfjson.Plan{
				ResourceChanges: []*tfjson.ResourceChange{
					{
						Type: "aws_ecs_service",
						Change: &tfjson.Change{
							Actions: []tfjson.Action{"no-op"},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "mixed changes with ECS update",
			plan: &tfjson.Plan{
				ResourceChanges: []*tfjson.ResourceChange{
					{
						Type: "aws_s3_bucket",
						Change: &tfjson.Change{
							Actions: []tfjson.Action{"create"},
						},
					},
					{
						Type: "aws_ecs_service",
						Change: &tfjson.Change{
							Actions: []tfjson.Action{"update"},
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasECSResourceChanges(tt.plan)
			if result != tt.expected {
				t.Errorf("hasECSResourceChanges() = %v, expected %v", result, tt.expected)
			}
		})
	}
}