package terraform

import (
	"context"
	"fmt"
	"github.com/hashicorp/terraform-exec/tfexec"
)

func ApplyTerraform(ctx context.Context, stackPath string, execPath string, roleArn *string) (map[string]tfexec.OutputMeta, error) {
	tf, err := tfexec.NewTerraform(stackPath, execPath)
	if err != nil {
		return nil, fmt.Errorf("error running NewTerraform: %s", err)
	}

	var initOptions []tfexec.InitOption
	if roleArn != nil {
		initOptions = append(initOptions, tfexec.BackendConfig(fmt.Sprintf("role_arn=%s", *roleArn)))
	}
	err = tf.Init(ctx, initOptions...)
	if err != nil {
		return nil, fmt.Errorf("error running Init: %s", err)
	}

	err = tf.Apply(ctx)
	if err != nil {
		return nil, fmt.Errorf("error running Apply: %s", err)
	}

	outputs, err := tf.Output(ctx)
	if err != nil {
		return nil, fmt.Errorf("error retrieving outputs: %s", err)
	}
	return outputs, nil
}

func ApplyTerraformWithTargets(ctx context.Context, stackPath string, execPath string, targets []string, roleArn *string) (map[string]tfexec.OutputMeta, error) {
	tf, err := tfexec.NewTerraform(stackPath, execPath)
	if err != nil {
		return nil, fmt.Errorf("error running NewTerraform: %s", err)
	}

	var initOptions []tfexec.InitOption
	if roleArn != nil {
		initOptions = append(initOptions, tfexec.BackendConfig(fmt.Sprintf("role_arn=%s", *roleArn)))
	}
	err = tf.Init(ctx, initOptions...)
	if err != nil {
		return nil, fmt.Errorf("error running Init: %s", err)
	}

	err = tf.Apply(ctx)
	if err != nil {
		return nil, fmt.Errorf("error running Apply: %s", err)
	}

	outputs, err := tf.Output(ctx)
	if err != nil {
		return nil, fmt.Errorf("error retrieving outputs: %s", err)
	}
	return outputs, nil
}
