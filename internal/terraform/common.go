package terraform

import (
	"context"
	"errors"
	"fmt"
	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hc-install/product"
	"github.com/hashicorp/hc-install/releases"
)

const terraformVersion = "1.6.0"

func PrepareTerraform(ctx context.Context) (*string, error) {

	installer := &releases.ExactVersion{
		Product: product.Terraform,
		Version: version.Must(version.NewVersion(terraformVersion)),
	}

	execPath, err := installer.Install(ctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("error installing terraform. looks like your internet might not be fast enough")
		}
		return nil, fmt.Errorf("error installing Terraform: %s", err)
	}
	return &execPath, nil
}
