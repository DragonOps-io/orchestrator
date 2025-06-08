package terraform

import (
	"context"
	"fmt"
	"github.com/hashicorp/terraform-exec/tfexec"
)

func DestroyTerraform(ctx context.Context, stackPath string, execPath string, roleArn *string) (map[string]tfexec.OutputMeta, error) {
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

	err = tf.Destroy(ctx)
	if err != nil {
		return nil, fmt.Errorf("error running Destroy: %s", err)
	}

	outputs, err := tf.Output(ctx)
	if err != nil {
		return nil, fmt.Errorf("error retrieving outputs: %s", err)
	}
	return outputs, nil
}

func DestroyTerraformTargets(ctx context.Context, stackPath string, execPath string, targets []string, roleArn *string) error {
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

	// Prepare destroy options with targets
	var destroyOptions []tfexec.DestroyOption
	for _, t := range targets {
		destroyOptions = append(destroyOptions, tfexec.Target(t))
	}

	// Run 'terraform destroy' with targets
	if err = tf.Destroy(ctx, destroyOptions...); err != nil {
		return fmt.Errorf("error running terraform destroy: %w", err)
	}

	return nil
}
