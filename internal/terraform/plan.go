package terraform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/hashicorp/terraform-exec/tfexec"
	tfjson "github.com/hashicorp/terraform-json"
	"os"
	"strings"
)

func PlanGroupTerraform(ctx context.Context, awsCfg aws.Config, randomId string, stateBucketName string, stackPath string, execPath string, roleArn *string) error {
	tf, err := tfexec.NewTerraform(stackPath, execPath)
	if err != nil {
		return fmt.Errorf("error running NewTerraform: %s", err)
	}

	var initOptions []tfexec.InitOption
	if roleArn != nil {
		initOptions = append(initOptions, tfexec.BackendConfig(fmt.Sprintf("role_arn=%s", *roleArn)))
	}

	err = tf.Init(ctx, initOptions...)
	if err != nil {
		return fmt.Errorf("error running Init: %s", err)
	}

	// create the plan file
	// /groups/groupId/network/network_resource_label_here/terraform files --> path
	planFilePath := fmt.Sprintf("%s/plan.txt", stackPath)
	planFile, err := os.Create(planFilePath)
	if err != nil {
		return fmt.Errorf("error creating plan file: %s", err)
	}
	defer planFile.Close()

	// get the s3key
	pathWithoutHome := "groups" + strings.Split(stackPath, "groups")[1]
	s3Path := "plans/" + randomId + "/" + strings.Join(strings.Split(pathWithoutHome, "/")[0:2], "/") + "/" + strings.Join(strings.Split(pathWithoutHome, "/")[2:], "/") + "/plan.txt"

	// create the s3 client
	s3Client := s3.NewFromConfig(awsCfg)

	// do the plan
	var changes bool
	changes, err = tf.Plan(ctx, tfexec.PlanOption(tfexec.Out(planFilePath)))
	if err != nil {
		// put error in the file so the user can see it when they get the plan
		_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: &stateBucketName,
			Key:    &s3Path,
			Body:   bytes.NewReader([]byte(err.Error())),
		})
		return nil
	}

	if changes {
		var out string
		out, err = tf.ShowPlanFileRaw(ctx, fmt.Sprintf("%s/plan.txt", stackPath))
		if err != nil {
			return fmt.Errorf("error running ShowPlanFileRaw: %s", err)
		}

		var b []byte
		b, err = json.Marshal(out)
		if err != nil {
			return err
		}

		_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: &stateBucketName,
			Key:    &s3Path,
			Body:   bytes.NewReader(b),
		})
		if err != nil {
			return fmt.Errorf("error uploading plan file to S3: %s", err)
		}
	} else {
		_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: &stateBucketName,
			Key:    &s3Path,
			Body:   bytes.NewReader([]byte("no changes in diff")),
		})
		return nil
	}
	return nil
}

//func PlanGroupTerraformWithDestroyTargets(ctx context.Context, awsCfg aws.Config, randomId string, stateBucketName string, stackPath string, execPath string, terraformResourcesToDelete []string, roleArn *string) error {
//	tf, err := tfexec.NewTerraform(stackPath, execPath)
//	if err != nil {
//		return fmt.Errorf("error running NewTerraform: %s", err)
//	}
//
//	var initOptions []tfexec.InitOption
//	if roleArn != nil {
//		initOptions = append(initOptions, tfexec.BackendConfig(fmt.Sprintf("role_arn=%s", *roleArn)))
//	}
//
//	err = tf.Init(ctx, initOptions...)
//	if err != nil {
//		return fmt.Errorf("error running Init: %s", err)
//	}
//
//	// create the plan file
//	// /groups/groupId/network/network_resource_label_here/terraform files --> path
//	planFilePath := fmt.Sprintf("%s/plan.txt", stackPath)
//	planFile, err := os.Create(planFilePath)
//	if err != nil {
//		return fmt.Errorf("error creating plan file: %s", err)
//	}
//	defer planFile.Close()
//
//	// get the s3key
//	pathWithoutHome := "groups" + strings.Split(stackPath, "groups")[1]
//	s3Path := "plans/" + randomId + "/" + strings.Join(strings.Split(pathWithoutHome, "/")[0:2], "/") + "/" + strings.Join(strings.Split(pathWithoutHome, "/")[2:], "/") + "/destroy-plan.txt"
//
//	// create the s3 client
//	s3Client := s3.NewFromConfig(awsCfg)
//
//	// Build plan options: destroy + targets
//
//	// Run terraform plan with destroy and with targets
//	if out, err = tf.Plan(ctx, planOptions...); err != nil {
//		return fmt.Errorf("error running terraform plan: %w", err)
//	}
//
//	// do the plan
//	var changes bool
//	var planOptions []tfexec.PlanOption
//	planOptions = append(planOptions, tfexec.Destroy(true))
//	planOptions = append(planOptions, tfexec.Out(planFilePath))
//	for _, t := range targets {
//		planOptions = append(planOptions, tfexec.Target(t))
//	}
//
//	changes, err = tf.Plan(ctx, planOptions...)
//	if err != nil {
//		// put error in the file so the user can see it when they get the plan
//		_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
//			Bucket: &stateBucketName,
//			Key:    &s3Path,
//			Body:   bytes.NewReader([]byte(err.Error())),
//		})
//		return nil
//	}
//
//	if changes {
//		var out string
//		out, err = tf.ShowPlanFileRaw(ctx, fmt.Sprintf("%s/plan.txt", stackPath))
//		if err != nil {
//			return fmt.Errorf("error running ShowPlanFileRaw: %s", err)
//		}
//
//		var b []byte
//		b, err = json.Marshal(out)
//		if err != nil {
//			return err
//		}
//
//		_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
//			Bucket: &stateBucketName,
//			Key:    &s3Path,
//			Body:   bytes.NewReader(b),
//		})
//		if err != nil {
//			return fmt.Errorf("error uploading plan file to S3: %s", err)
//		}
//	} else {
//		_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
//			Bucket: &stateBucketName,
//			Key:    &s3Path,
//			Body:   bytes.NewReader([]byte("no changes in diff")),
//		})
//		return nil
//	}
//	return nil
//}

func PlanAppTerraform(ctx context.Context, awsCfg aws.Config, randomId string, stateBucketName string, stackPath string, execPath string, roleArn *string) error {
	tf, err := tfexec.NewTerraform(stackPath, execPath)
	if err != nil {
		return fmt.Errorf("error running NewTerraform: %s", err)
	}

	var initOptions []tfexec.InitOption
	if roleArn != nil {
		initOptions = append(initOptions, tfexec.BackendConfig(fmt.Sprintf("role_arn=%s", *roleArn)))
	}

	err = tf.Init(ctx, initOptions...)
	if err != nil {
		return fmt.Errorf("error running Init: %s", err)
	}

	// create the plan file
	//fmt.Sprintf("/apps/%s/%s", app.ID, env.ID)
	planFilePath := fmt.Sprintf("%s/plan.txt", stackPath) // apps/app_id/env_id/application/plan.txt
	planFile, err := os.Create(planFilePath)
	if err != nil {
		return fmt.Errorf("error creating plan file: %s", err)
	}
	defer planFile.Close()

	// get the s3key
	pathWithoutHome := "apps" + strings.Split(stackPath, "apps")[1]
	s3Path := "plans/" + randomId + "/" + pathWithoutHome + "/plan.txt"
	fmt.Println("s3Path: ", s3Path)

	// create the s3 client
	s3Client := s3.NewFromConfig(awsCfg)

	// do the plan
	var changes bool
	changes, err = tf.Plan(ctx, tfexec.PlanOption(tfexec.Out(planFilePath)))
	if err != nil {
		// put error in the file so the user can see it when they get the plan
		_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: &stateBucketName,
			Key:    &s3Path,
			Body:   bytes.NewReader([]byte(err.Error())),
		})
		return nil
	}

	if changes {
		var out string
		out, err = tf.ShowPlanFileRaw(ctx, planFilePath)
		if err != nil {
			return fmt.Errorf("error running ShowPlanFileRaw: %s", err)
		}

		var b []byte
		b, err = json.Marshal(out)
		if err != nil {
			return err
		}

		_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: &stateBucketName,
			Key:    &s3Path,
			Body:   bytes.NewReader(b),
		})
		if err != nil {
			return fmt.Errorf("error uploading plan file to S3: %s", err)
		}
	} else {
		_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: &stateBucketName,
			Key:    &s3Path,
			Body:   bytes.NewReader([]byte("no changes in diff")),
		})
		return nil
	}
	return nil
}

func DestroyTerraformTargetsPlan(ctx context.Context, stackPath string, execPath string, targets []string, roleArn *string) error {
	tf, err := tfexec.NewTerraform(stackPath, execPath)
	if err != nil {
		return fmt.Errorf("error running NewTerraform: %s", err)
	}
	var initOptions []tfexec.InitOption
	if roleArn != nil {
		initOptions = append(initOptions, tfexec.BackendConfig(fmt.Sprintf("role_arn=%s", *roleArn)))
	}
	err = tf.Init(ctx, initOptions...)
	if err != nil {
		return fmt.Errorf("error running Init: %s", err)
	}

	return nil
}

// CheckForECSServiceChanges runs terraform plan and checks if there are any ECS service or task definition changes
// that would trigger a new ECS deployment. Returns true if ECS changes are detected.
func CheckForECSServiceChanges(ctx context.Context, stackPath string, execPath string, roleArn *string) (bool, error) {
	tf, err := tfexec.NewTerraform(stackPath, execPath)
	if err != nil {
		return false, fmt.Errorf("error running NewTerraform: %s", err)
	}

	var initOptions []tfexec.InitOption
	if roleArn != nil {
		initOptions = append(initOptions, tfexec.BackendConfig(fmt.Sprintf("role_arn=%s", *roleArn)))
	}

	err = tf.Init(ctx, initOptions...)
	if err != nil {
		return false, fmt.Errorf("error running Init: %s", err)
	}

	// Create a temporary plan file
	planFilePath := fmt.Sprintf("%s/ecs-check-plan.tfplan", stackPath)
	defer func() {
		os.Remove(planFilePath)
	}()

	// Run terraform plan
	hasChanges, err := tf.Plan(ctx, tfexec.Out(planFilePath))
	if err != nil {
		return false, fmt.Errorf("error running Plan: %s", err)
	}

	// If no changes at all, no ECS changes
	if !hasChanges {
		return false, nil
	}

	// Parse the plan file to check for ECS changes
	plan, err := tf.ShowPlanFile(ctx, planFilePath)
	if err != nil {
		return false, fmt.Errorf("error reading plan file: %s", err)
	}

	return hasECSResourceChanges(plan), nil
}

// hasECSResourceChanges analyzes the terraform plan to detect ECS-related changes
// that would trigger a new deployment
func hasECSResourceChanges(plan *tfjson.Plan) bool {
	if plan == nil || plan.ResourceChanges == nil {
		return false
	}

	// ECS resource types that would trigger a deployment when changed
	ecsResourceTypes := map[string]bool{
		"aws_ecs_service":         true,
		"aws_ecs_task_definition": true,
	}

	for _, resourceChange := range plan.ResourceChanges {
		if resourceChange == nil || resourceChange.Change == nil {
			continue
		}

		// Check if this is an ECS resource type
		if ecsResourceTypes[resourceChange.Type] {
			// Check if this change would actually affect the resource
			// (create, update, replace operations)
			if resourceChange.Change.Actions != nil {
				for _, action := range resourceChange.Change.Actions {
					switch action {
					case "create", "update", "replace":
						return true
					case "delete":
						// Deletion alone doesn't trigger a new deployment
						continue
					case "no-op":
						// No-op means no changes
						continue
					}
				}
			}
		}
	}

	return false
}
