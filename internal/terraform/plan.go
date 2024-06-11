package terraform

import (
	"context"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/hashicorp/terraform-exec/tfexec"
	"os"
	"strings"
)

func PlanTerraform(ctx context.Context, randomId string, stateBucketName string, stackPath string, execPath string, roleArn *string) error {
	tf, err := tfexec.NewTerraform(stackPath, execPath)
	if err != nil {
		return fmt.Errorf("error running NewTerraform: %s", err)
	}

	initOptions := []tfexec.InitOption{tfexec.Upgrade(true), tfexec.Reconfigure(true)}
	if roleArn != nil {
		initOptions = append(initOptions, tfexec.BackendConfig(fmt.Sprintf("role_arn=%s", *roleArn)))
	}
	err = tf.Init(ctx, initOptions...)
	if err != nil {
		return fmt.Errorf("error running Init: %s", err)
	}

	var ok bool
	// plans/groups/groupId/planId/network/network_resource_label_here/terraform files
	planFile, err := os.Create(fmt.Sprintf("%s/plan.json", stackPath))
	if err != nil {
		return fmt.Errorf("error creating plan file: %s", err)
	}
	defer planFile.Close()
	ok, err = tf.PlanJSON(ctx, planFile)
	if err != nil {
		return fmt.Errorf("error running Plan: %s", err)
	}

	if ok {
		// the diff is not empty -- save the plan file to S3
		var cfg aws.Config
		cfg, err = config.LoadDefaultConfig(ctx)
		if err != nil {
			return fmt.Errorf("error loading AWS config: %s", err)
		}
		s3Client := s3.NewFromConfig(cfg)
		s3Path := "/plans/" + strings.Join(strings.Split(stackPath, "/")[0:2], "/") + randomId + strings.Join(strings.Split(stackPath, "/")[2:], "/") + "/plan.json"
		_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: &stateBucketName,
			Key:    &s3Path,
			Body:   planFile,
		})
		if err != nil {
			return fmt.Errorf("error uploading plan file to S3: %s", err)
		}
	}

	return nil
}
