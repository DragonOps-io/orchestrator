module github.com/DragonOps-io/orchestrator

go 1.24.0

toolchain go1.24.2

require (
	github.com/DragonOps-io/types v0.0.0-20250619021444-931e1127e7fc
	github.com/Ilios-LLC/magicmodel-go v0.0.0-20250522145903-d5a2cd7d0cbf
	github.com/aws/aws-sdk-go-v2 v1.36.3
	github.com/aws/aws-sdk-go-v2/config v1.26.6
	github.com/aws/aws-sdk-go-v2/credentials v1.16.16
	github.com/aws/aws-sdk-go-v2/service/ecr v1.44.0
	github.com/aws/aws-sdk-go-v2/service/eks v1.65.0
	github.com/aws/aws-sdk-go-v2/service/route53 v1.37.0
	github.com/aws/aws-sdk-go-v2/service/s3 v1.55.1
	github.com/aws/aws-sdk-go-v2/service/secretsmanager v1.28.6
	github.com/aws/aws-sdk-go-v2/service/sqs v1.29.7
	github.com/aws/aws-sdk-go-v2/service/ssm v1.56.13
	github.com/aws/aws-sdk-go-v2/service/sts v1.26.7
	github.com/bugsnag/bugsnag-go/v2 v2.5.1
	github.com/hashicorp/go-version v1.6.0
	github.com/hashicorp/hc-install v0.6.0
	github.com/hashicorp/terraform-exec v0.19.0
	github.com/rs/zerolog v1.30.0
	github.com/spf13/cobra v1.8.0
	k8s.io/client-go v0.33.1
)

require (
	github.com/ProtonMail/go-crypto v0.0.0-20230717121422-5aa5874ade95 // indirect
	github.com/apparentlymart/go-textseg/v15 v15.0.0 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.6.2 // indirect
	github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue v1.12.3 // indirect
	github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression v1.6.3 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.14.11 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.3.34 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.6.34 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.7.3 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.3.9 // indirect
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.27.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/dynamodbstreams v1.17.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.11.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.3.11 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.8.11 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.11.11 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.17.9 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.18.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.21.7 // indirect
	github.com/aws/smithy-go v1.22.2 // indirect
	github.com/bugsnag/panicwrap v1.3.4 // indirect
	github.com/cloudflare/circl v1.3.8 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/fxamacker/cbor/v2 v2.7.0 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/terraform-json v0.17.1 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/kardianos/osext v0.0.0-20190222173326-2bc1f35cddc0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.19 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/stoewer/go-strcase v1.3.0 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/zclconf/go-cty v1.14.0 // indirect
	golang.org/x/crypto v0.38.0 // indirect
	golang.org/x/mod v0.17.0 // indirect
	golang.org/x/net v0.40.0 // indirect
	golang.org/x/oauth2 v0.27.0 // indirect
	golang.org/x/sys v0.33.0 // indirect
	golang.org/x/term v0.32.0 // indirect
	golang.org/x/text v0.25.0 // indirect
	golang.org/x/time v0.9.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	k8s.io/apimachinery v0.33.1 // indirect
	k8s.io/klog/v2 v2.130.1 // indirect
	k8s.io/utils v0.0.0-20241104100929-3ea5e8cea738 // indirect
	sigs.k8s.io/json v0.0.0-20241010143419-9aa6b5e7a4b3 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.6.0 // indirect
	sigs.k8s.io/yaml v1.4.0 // indirect
)
