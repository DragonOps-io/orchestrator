package group

import (
	"context"
	"fmt"
	"github.com/DragonOps-io/types"
	magicmodel "github.com/Ilios-LLC/magicmodel-go/model"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"os/exec"
	"slices"
	"strings"
	"time"
)

func handleWireguardUpdates(mm *magicmodel.Operator, network types.Network, awsCfg aws.Config) error {
	// create ssm client
	client := ssm.NewFromConfig(awsCfg)
	runInitCommands := false
	if network.WireguardPublicKey == "" {
		runInitCommands = true
		privateKey, err := generateWireGuardKey()
		if err != nil {
			return err
		}
		publicKey, err := generateWireGuardPublicKey(privateKey)
		if err != nil {
			return err
		}

		network.WireguardPublicKey = strings.TrimSpace(publicKey)
		network.WireguardPrivateKey = strings.TrimSpace(privateKey)

		o := mm.Save(&network)
		if o.Err != nil {
			return o.Err
		}

		// create parameters in ssm
		err = types.UpdatePublicPrivateKeyParameters(context.Background(), &publicKey, &privateKey, network.ID, awsCfg)
		if err != nil {
			return err
		}
	}

	// update parameter in ssm (in case of port or ip range change
	var clients []types.VpnClient
	o := mm.All(&clients)
	if o.Err != nil {
		return o.Err
	}

	clientIPPublicKeyMap := make(map[string]string)
	for _, c := range clients {
		networkIds := make([]string, 0)
		for _, n := range c.Networks {
			networkIds = append(networkIds, n.ID)
		}
		if slices.Contains(networkIds, network.ID) {
			clientIPPublicKeyMap[c.ClientIP] = c.PublicKey
		}
	}

	configFile := types.CreateServerConfigFile(network.WireguardIP, network.WireguardPort, network.WireguardPrivateKey, clientIPPublicKeyMap)

	err := types.UpdateServerConfigFileParameter(context.Background(), configFile, network.ID, awsCfg)
	if err != nil {
		return err
	}

	commandId, err := types.RunSSMCommands(context.Background(), runInitCommands, network.WireguardInstanceID, awsCfg, network.ID)
	if err != nil {
		return err
	}
	time.Sleep(3 * time.Second) // Have to sleep because there is a delay in the invocation
	err = types.WaitForCommandSuccess(context.Background(), client, *commandId, network.WireguardInstanceID)
	if err != nil {
		return err
	}
	return nil
}

func generateWireGuardKey() (string, error) {
	cmd := exec.Command("wg", "genkey")
	key, err := cmd.Output()
	if err != nil {
		fmt.Println("error when generating private key:  ", string(key))
		return "", err
	}

	return string(key), nil
}

func generateWireGuardPublicKey(privateKey string) (string, error) {
	cmd := exec.Command("wg", "pubkey")
	cmd.Stdin = strings.NewReader(privateKey)
	publicKey, err := cmd.Output()
	if err != nil {
		fmt.Println("error when generating public key:  ", string(publicKey))
		return "", err
	}
	return string(publicKey), nil
}
