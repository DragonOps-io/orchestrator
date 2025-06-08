package terraform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/hashicorp/terraform-exec/tfexec"
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
