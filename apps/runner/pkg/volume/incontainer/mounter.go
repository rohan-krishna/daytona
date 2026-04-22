// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: AGPL-3.0

// Package incontainer implements a volume.Mounter that performs the mount
// inside the sandbox container instead of on the runner host. The runner's
// responsibility is limited to:
//
//  1. Minting short-lived, bucket-scoped AWS credentials via STS AssumeRole
//     with an inline session policy (so the credentials that end up inside
//     the sandbox cannot touch any other S3 resource and expire on a timer).
//  2. Bind-mounting a mount-s3 binary into the container (read-only).
//  3. Injecting an env payload containing the volume list + scoped creds.
//
// The in-container daemon consumes the env payload and invokes mount-s3 per
// volume at startup.
package incontainer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/daytonaio/runner/pkg/volume"
)

// MountS3BinaryContainerPath is the well-known path the runner bind-mounts
// the mount-s3 binary to inside every sandbox using an in-container volume
// backend ("s3fuse" or "experimental").
const MountS3BinaryContainerPath = "/usr/local/bin/daytona-mount-s3"

// Env var names exchanged between the runner and the in-container daemon.
// The daemon translates these to the AWS_* names mount-s3 expects, so the
// runner-issued values don't bleed into any user shell via PATH-scraping of
// standard AWS_* vars.
const (
	EnvVolumesJSON        = "DAYTONA_INCONTAINER_VOLUMES"
	EnvMountS3Binary      = "DAYTONA_INCONTAINER_MOUNT_S3_BINARY"
	EnvAWSRegion          = "DAYTONA_INCONTAINER_AWS_REGION"
	EnvAWSEndpointURL     = "DAYTONA_INCONTAINER_AWS_ENDPOINT_URL"
	EnvAWSAccessKeyID     = "DAYTONA_INCONTAINER_AWS_ACCESS_KEY_ID"
	EnvAWSSecretAccessKey = "DAYTONA_INCONTAINER_AWS_SECRET_ACCESS_KEY" //nolint:gosec // env var name, not a credential
	EnvAWSSessionToken    = "DAYTONA_INCONTAINER_AWS_SESSION_TOKEN"     //nolint:gosec // env var name, not a credential
	EnvAWSCredsExpiration = "DAYTONA_INCONTAINER_AWS_CREDS_EXPIRATION"
)

// defaultSessionDuration is the STS session TTL applied when the operator
// does not override it. AWS caps role-chained sessions at 1h but supports up
// to 12h on a direct AssumeRole; we default to 12h so most sandbox lifetimes
// fit comfortably in a single mount.
const defaultSessionDuration = 12 * time.Hour

// stsClient is the narrow subset of sts.Client we actually use. Extracted
// as an interface so tests can stub it and so we don't import the full
// client surface elsewhere.
type stsClient interface {
	AssumeRole(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
}

// Config controls how the runner-side mounter mints credentials.
//
// SECURITY: The credentials that end up inside the sandbox are STS session
// credentials scoped via an inline session policy to only the buckets the
// sandbox actually mounts. They cannot be used against any other S3
// resource and expire after SessionDuration.
type Config struct {
	// AWSRegion is forwarded to the sandbox so mount-s3 knows where to sign.
	AWSRegion string
	// AWSEndpointURL is an optional custom endpoint (e.g. MinIO). Forwarded
	// to the sandbox as-is.
	AWSEndpointURL string
	// AssumeRoleARN is the IAM role the runner calls sts:AssumeRole on.
	// Required for this backend to operate — runner's base credentials must
	// have sts:AssumeRole permission on this role.
	AssumeRoleARN string
	// SessionDuration is the TTL of the minted session credentials. Capped
	// by the role's MaxSessionDuration. Defaults to 12h when zero.
	SessionDuration time.Duration
	// MountS3BinaryHostPath is the host path to mount-s3 that gets bind-
	// mounted RO into each sandbox at MountS3BinaryContainerPath.
	MountS3BinaryHostPath string
	// SessionNamePrefix is an optional prefix on the STS RoleSessionName.
	// Defaults to "daytona-sandbox".
	SessionNamePrefix string
}

// Mounter is a volume.Mounter whose host-side methods are deliberate no-ops.
// The mount happens inside the sandbox container; the only runner-side work
// is credential minting via STS.
type Mounter struct {
	cfg Config
	sts stsClient
}

func NewMounter(cfg Config, client stsClient) *Mounter {
	if cfg.SessionDuration == 0 {
		cfg.SessionDuration = defaultSessionDuration
	}
	if cfg.SessionNamePrefix == "" {
		cfg.SessionNamePrefix = "daytona-sandbox"
	}
	return &Mounter{cfg: cfg, sts: client}
}

// Host-side lifecycle — all no-ops. The mount lives inside the container
// and is torn down naturally when the container exits.

func (m *Mounter) Mount(_ context.Context, _ string, _ string) error { return nil }
func (m *Mounter) Unmount(_ context.Context, _ string) error         { return nil }
func (m *Mounter) IsMounted(_ string) bool                           { return false }
func (m *Mounter) WaitUntilReady(_ context.Context, _ string) error  { return nil }

// ContainerBinds returns the RO binds every in-container-backend sandbox
// needs regardless of volume count (just the mount-s3 binary).
func (m *Mounter) ContainerBinds() []string {
	if m.cfg.MountS3BinaryHostPath == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s:%s:ro", m.cfg.MountS3BinaryHostPath, MountS3BinaryContainerPath)}
}

// ContainerEnv calls STS to mint session credentials scoped to the given
// volumes' buckets and returns the env vars the in-container daemon needs.
func (m *Mounter) ContainerEnv(ctx context.Context, volumes []volume.Volume) ([]string, error) {
	if len(volumes) == 0 {
		return nil, nil
	}
	if m.sts == nil || m.cfg.AssumeRoleARN == "" {
		return nil, fmt.Errorf("incontainer mounter is not fully configured (missing STS client or AssumeRoleARN)")
	}

	buckets := collectBuckets(volumes)
	policy, err := buildSessionPolicy(buckets)
	if err != nil {
		return nil, fmt.Errorf("build session policy: %w", err)
	}

	sessionName := fmt.Sprintf("%s-%d", m.cfg.SessionNamePrefix, time.Now().UnixNano())
	if len(sessionName) > 64 {
		sessionName = sessionName[:64]
	}

	out, err := m.sts.AssumeRole(ctx, &sts.AssumeRoleInput{
		RoleArn:         aws.String(m.cfg.AssumeRoleARN),
		RoleSessionName: aws.String(sessionName),
		DurationSeconds: aws.Int32(int32(m.cfg.SessionDuration / time.Second)),
		Policy:          aws.String(policy),
	})
	if err != nil {
		return nil, fmt.Errorf("sts AssumeRole failed: %w", err)
	}
	if out.Credentials == nil {
		return nil, fmt.Errorf("sts AssumeRole returned no credentials")
	}

	volumesJSON, err := json.Marshal(volumes)
	if err != nil {
		return nil, fmt.Errorf("marshal volumes: %w", err)
	}

	env := []string{
		EnvVolumesJSON + "=" + string(volumesJSON),
		EnvMountS3Binary + "=" + MountS3BinaryContainerPath,
		EnvAWSAccessKeyID + "=" + aws.ToString(out.Credentials.AccessKeyId),
		EnvAWSSecretAccessKey + "=" + aws.ToString(out.Credentials.SecretAccessKey),
		EnvAWSSessionToken + "=" + aws.ToString(out.Credentials.SessionToken),
	}
	if out.Credentials.Expiration != nil {
		env = append(env, EnvAWSCredsExpiration+"="+out.Credentials.Expiration.UTC().Format(time.RFC3339))
	}
	if m.cfg.AWSRegion != "" {
		env = append(env, EnvAWSRegion+"="+m.cfg.AWSRegion)
	}
	if m.cfg.AWSEndpointURL != "" {
		env = append(env, EnvAWSEndpointURL+"="+m.cfg.AWSEndpointURL)
	}
	return env, nil
}

// collectBuckets returns the unique set of bucket names referenced by the
// volume list, preserving input order.
func collectBuckets(volumes []volume.Volume) []string {
	seen := make(map[string]struct{}, len(volumes))
	buckets := make([]string, 0, len(volumes))
	for _, v := range volumes {
		if v.VolumeID == "" {
			continue
		}
		if _, ok := seen[v.VolumeID]; ok {
			continue
		}
		seen[v.VolumeID] = struct{}{}
		buckets = append(buckets, v.VolumeID)
	}
	return buckets
}

// buildSessionPolicy produces an inline session policy that limits the
// AssumeRole session to only the buckets mounted by this sandbox. The
// session's effective permissions are the intersection of the role's policy
// and this document, so this can only narrow — never broaden — what the
// role already grants.
func buildSessionPolicy(buckets []string) (string, error) {
	if len(buckets) == 0 {
		return "", fmt.Errorf("no buckets provided")
	}

	bucketArns := make([]string, 0, len(buckets))
	objectArns := make([]string, 0, len(buckets))
	for _, b := range buckets {
		bucketArns = append(bucketArns, fmt.Sprintf("arn:aws:s3:::%s", b))
		objectArns = append(objectArns, fmt.Sprintf("arn:aws:s3:::%s/*", b))
	}

	doc := map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{
			{
				"Effect":   "Allow",
				"Action":   []string{"s3:ListBucket", "s3:GetBucketLocation"},
				"Resource": bucketArns,
			},
			{
				"Effect": "Allow",
				"Action": []string{
					"s3:GetObject",
					"s3:PutObject",
					"s3:DeleteObject",
					"s3:AbortMultipartUpload",
					"s3:ListMultipartUploadParts",
				},
				"Resource": objectArns,
			},
		},
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// Compile-time check that Mounter satisfies both interfaces.
var (
	_ volume.Mounter            = (*Mounter)(nil)
	_ volume.InContainerMounter = (*Mounter)(nil)
)
