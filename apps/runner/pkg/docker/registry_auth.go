// Copyright 2025 Daytona Platforms Inc.
// SPDX-License-Identifier: AGPL-3.0

package docker

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"

	"github.com/daytonaio/runner/pkg/api/dto"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// ecrRegistryRegex matches private Amazon ECR registry hostnames.
// The capture group is the AWS region.
var ecrRegistryRegex = regexp.MustCompile(`^\d+\.dkr\.ecr\.([a-z0-9-]+)\.amazonaws\.com$`)

// isEcrRegistry reports whether the URL points at a private ECR registry.
// It also returns the parsed region.
func isEcrRegistry(url string) (region string, ok bool) {
	m := ecrRegistryRegex.FindStringSubmatch(url)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// resolveRegistryCredentials returns (username, password) to use against the
// registry. ECR URLs trigger sts:AssumeRole + ECR GetAuthorizationToken; the
// DTO's Username carries the role ARN and Password carries the orgId
// (ExternalId + RoleSessionName seed). Non-ECR returns the stored basic-auth
// pair as-is.
//
// TODO(ecr): swap NewAssumeRoleProvider for NewWebIdentityRoleProvider once
// Daytona is an OIDC provider, dropping the static AWS broker-cred env vars.
func resolveRegistryCredentials(ctx context.Context, reg *dto.RegistryDTO) (string, string, error) {
	if reg == nil {
		return "", "", nil
	}

	region, isEcr := isEcrRegistry(reg.Url)
	if !isEcr {
		if !reg.HasAuth() {
			return "", "", nil
		}
		return *reg.Username, *reg.Password, nil
	}

	if reg.Username == nil || *reg.Username == "" {
		return "", "", fmt.Errorf("ECR registry %s missing role ARN in username field", reg.Url)
	}
	roleArn := *reg.Username
	var externalID string
	if reg.Password != nil {
		externalID = *reg.Password
	}

	baseCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return "", "", fmt.Errorf("failed to load AWS config: %w", err)
	}

	sessionName := "daytona"
	if externalID != "" {
		sessionName = "daytona-" + externalID + "-pull"
	}

	stsClient := sts.NewFromConfig(baseCfg)
	provider := stscreds.NewAssumeRoleProvider(stsClient, roleArn, func(o *stscreds.AssumeRoleOptions) {
		o.RoleSessionName = sessionName
		if externalID != "" {
			o.ExternalID = aws.String(externalID)
		}
	})

	assumedCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(aws.NewCredentialsCache(provider)),
	)
	if err != nil {
		return "", "", fmt.Errorf("failed to load assumed-role AWS config: %w", err)
	}

	resp, err := ecr.NewFromConfig(assumedCfg).GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		return "", "", fmt.Errorf("failed to get ECR authorization token: %w", err)
	}
	if len(resp.AuthorizationData) == 0 || resp.AuthorizationData[0].AuthorizationToken == nil {
		return "", "", fmt.Errorf("ECR returned no authorization data")
	}

	decoded, err := base64.StdEncoding.DecodeString(*resp.AuthorizationData[0].AuthorizationToken)
	if err != nil {
		return "", "", fmt.Errorf("failed to decode ECR auth token: %w", err)
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("unexpected ECR token format")
	}
	return parts[0], parts[1], nil
}
