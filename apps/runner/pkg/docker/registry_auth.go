// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: AGPL-3.0

package docker

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"time"

	common_cache "github.com/daytonaio/common-go/pkg/cache"
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

type ecrToken struct {
	username, password string
}

// ecrTokenCache memoizes ECR Docker auth pairs keyed by "<url>|<roleArn>"
// so we don't hit sts:AssumeRole + ecr:GetAuthorizationToken on every pull.
// Per-entry TTL from AuthorizationData.ExpiresAt; ttlcache evicts in the background.
var ecrTokenCache = common_cache.NewMapCache[ecrToken](context.Background())

// ecrTokenRefreshBuffer trims AuthorizationData.ExpiresAt so we refresh
// ahead of AWS-side expiry to absorb clock skew.
const ecrTokenRefreshBuffer = 30 * time.Minute

// shouldResolveAuth gates calls to resolveRegistryCredentials. ECR URLs
// always go through the resolver — even with no stored creds, the runner's
// default credential chain may have ECR access (self-hosted EC2 case).
func shouldResolveAuth(reg *dto.RegistryDTO) bool {
	if reg == nil {
		return false
	}
	if reg.HasAuth() {
		return true
	}
	_, isEcr := isEcrRegistry(reg.Url)
	return isEcr
}

// resolveRegistryCredentials returns (username, password) to use against the
// registry. ECR URLs trigger ECR GetAuthorizationToken; if Username carries a
// role ARN, the runner first does sts:AssumeRole (with Password as ExternalId
// and RoleSessionName seed). With no role ARN it falls through to the default
// credential chain. Non-ECR returns the stored basic-auth pair as-is.
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

	var roleArn, externalID string
	if reg.Username != nil {
		roleArn = *reg.Username
	}
	if reg.Password != nil {
		externalID = *reg.Password
	}

	cacheKey := reg.Url + "|" + roleArn + "|" + externalID
	if entry, err := ecrTokenCache.Get(ctx, cacheKey); err == nil && entry != nil {
		return entry.username, entry.password, nil
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return "", "", fmt.Errorf("failed to load AWS config: %w", err)
	}

	// If a role ARN is provided, assume it and use the assumed creds for ECR.
	// Otherwise fall through to the default credential chain — useful for
	// self-hosted runners with EC2 instance profiles or env creds that can call
	// ECR directly without a cross-account hop.
	if roleArn != "" {
		sessionName := "daytona"
		if externalID != "" {
			sessionName = "daytona-" + externalID + "-pull"
		}
		stsClient := sts.NewFromConfig(cfg)
		provider := stscreds.NewAssumeRoleProvider(stsClient, roleArn, func(o *stscreds.AssumeRoleOptions) {
			o.RoleSessionName = sessionName
			if externalID != "" {
				o.ExternalID = aws.String(externalID)
			}
		})
		cfg.Credentials = aws.NewCredentialsCache(provider)
	}

	resp, err := ecr.NewFromConfig(cfg).GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		return "", "", fmt.Errorf("failed to get ECR authorization token: %w", err)
	}
	if len(resp.AuthorizationData) == 0 || resp.AuthorizationData[0].AuthorizationToken == nil {
		return "", "", fmt.Errorf("ECR returned no authorization data")
	}

	authData := resp.AuthorizationData[0]
	decoded, err := base64.StdEncoding.DecodeString(*authData.AuthorizationToken)
	if err != nil {
		return "", "", fmt.Errorf("failed to decode ECR auth token: %w", err)
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("unexpected ECR token format")
	}

	ttl := 11 * time.Hour // fallback if AWS omits ExpiresAt
	if authData.ExpiresAt != nil {
		ttl = time.Until(authData.ExpiresAt.Add(-ecrTokenRefreshBuffer))
	}
	// Skip caching if the token is already (nearly) expired — caching it would
	// poison subsequent pulls. Also avoids ttlcache's "ttl<=0 means never expires"
	// footgun. The current pull uses the freshly-fetched creds either way.
	if ttl > 0 {
		_ = ecrTokenCache.Set(ctx, cacheKey, ecrToken{username: parts[0], password: parts[1]}, ttl)
	}
	return parts[0], parts[1], nil
}
