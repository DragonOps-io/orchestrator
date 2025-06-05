package terraform

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hc-install/product"
	"github.com/hashicorp/hc-install/releases"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"os"
	"path/filepath"
)

const terraformVersion = "1.6.0"

// GenerateKubeconfig fetches EKS cluster info and writes a kubeconfig to a temporary path.
// Returns the full path to the generated kubeconfig file.
func GenerateKubeconfig(ctx context.Context, cfg aws.Config, clusterName, region string) (string, error) {
	eksClient := eks.NewFromConfig(cfg)

	out, err := eksClient.DescribeCluster(ctx, &eks.DescribeClusterInput{
		Name: aws.String(clusterName),
	})
	if err != nil {
		return "", fmt.Errorf("failed to describe EKS cluster %s: %w", clusterName, err)
	}
	cluster := out.Cluster

	decodedCA, err := base64.StdEncoding.DecodeString(*cluster.CertificateAuthority.Data)
	if err != nil {
		return "", fmt.Errorf("failed to decode CA data: %w", err)
	}

	kubeConfig := clientcmdapi.NewConfig()
	kubeConfig.Clusters[clusterName] = &clientcmdapi.Cluster{
		Server:                   aws.ToString(cluster.Endpoint),
		CertificateAuthorityData: decodedCA,
	}
	kubeConfig.AuthInfos[clusterName] = &clientcmdapi.AuthInfo{
		Exec: &clientcmdapi.ExecConfig{
			APIVersion: "client.authentication.k8s.io/v1beta1",
			Command:    "aws",
			Args: []string{
				"eks", "get-token",
				"--cluster-name", clusterName,
				"--region", region,
			},
		},
	}
	kubeConfig.Contexts[clusterName] = &clientcmdapi.Context{
		Cluster:  clusterName,
		AuthInfo: clusterName,
	}
	kubeConfig.CurrentContext = clusterName

	// Write to temp kubeconfig file
	kubeconfigPath := filepath.Join(os.TempDir(), fmt.Sprintf("kubeconfig-%s", clusterName))
	if err = clientcmd.WriteToFile(*kubeConfig, kubeconfigPath); err != nil {
		return "", fmt.Errorf("failed to write kubeconfig to file: %w", err)
	}

	return kubeconfigPath, nil
}

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
