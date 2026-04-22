// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: AGPL-3.0

// Package volumemount runs inside the sandbox container and performs the
// in-container volume mount driven by the env payload injected by the runner.
// This is the sandbox-side counterpart of pkg/volume/incontainer in the
// runner.
//
// Env contract (must match pkg/volume/incontainer in runner):
//
//	DAYTONA_INCONTAINER_VOLUMES           JSON-encoded []Volume
//	DAYTONA_INCONTAINER_MOUNT_S3_BINARY   absolute path to mount-s3 binary
//	DAYTONA_INCONTAINER_AWS_REGION        optional, AWS region
//	DAYTONA_INCONTAINER_AWS_ENDPOINT_URL  optional, S3-compatible endpoint
//	DAYTONA_INCONTAINER_AWS_ACCESS_KEY_ID optional
//	DAYTONA_INCONTAINER_AWS_SECRET_ACCESS_KEY optional
package volumemount

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	envVolumesJSON        = "DAYTONA_INCONTAINER_VOLUMES"
	envMountS3Binary      = "DAYTONA_INCONTAINER_MOUNT_S3_BINARY"
	envAWSRegion          = "DAYTONA_INCONTAINER_AWS_REGION"
	envAWSEndpointURL     = "DAYTONA_INCONTAINER_AWS_ENDPOINT_URL"
	envAWSAccessKeyID     = "DAYTONA_INCONTAINER_AWS_ACCESS_KEY_ID"
	envAWSSecretAccessKey = "DAYTONA_INCONTAINER_AWS_SECRET_ACCESS_KEY" //nolint:gosec // env var name, not a credential
	envAWSSessionToken    = "DAYTONA_INCONTAINER_AWS_SESSION_TOKEN"     //nolint:gosec // env var name, not a credential
	envAWSCredsExpiration = "DAYTONA_INCONTAINER_AWS_CREDS_EXPIRATION"
)

// Volume mirrors volume.Volume in the runner.
type Volume struct {
	VolumeID  string `json:"volumeId"`
	MountPath string `json:"mountPath"`
	Subpath   string `json:"subpath,omitempty"`
}

// MountAll reads the env payload and mounts every declared volume. It is
// idempotent — already-mounted paths are skipped.
//
// MountAll never errors fatally: a failed mount is logged and skipped so the
// rest of the daemon can still come up. Callers should surface readiness
// signals through their own paths.
func MountAll(ctx context.Context, logger *slog.Logger) {
	raw := os.Getenv(envVolumesJSON)
	if raw == "" {
		return
	}

	binary := os.Getenv(envMountS3Binary)
	if binary == "" {
		logger.Warn("in-container volume spec present but mount-s3 binary path is empty", "env", envMountS3Binary)
		return
	}
	if _, err := os.Stat(binary); err != nil {
		logger.Warn("in-container mount-s3 binary not found; skipping volume mounts", "path", binary, "error", err)
		return
	}

	var volumes []Volume
	if err := json.Unmarshal([]byte(raw), &volumes); err != nil {
		logger.Error("failed to parse in-container volume spec", "error", err)
		return
	}

	if exp := os.Getenv(envAWSCredsExpiration); exp != "" {
		logger.Info("in-container volume credentials expire at", "expires_at", exp)
	}

	for _, v := range volumes {
		if err := mountOne(ctx, logger, binary, v); err != nil {
			logger.Error("failed to mount in-container volume", "volumeId", v.VolumeID, "mountPath", v.MountPath, "error", err)
			continue
		}
	}
}

func mountOne(ctx context.Context, logger *slog.Logger, binary string, v Volume) error {
	if v.VolumeID == "" || v.MountPath == "" {
		return fmt.Errorf("invalid volume entry: volumeId=%q mountPath=%q", v.VolumeID, v.MountPath)
	}

	if err := os.MkdirAll(v.MountPath, 0755); err != nil {
		return fmt.Errorf("create mountpoint: %w", err)
	}

	if isMountpoint(v.MountPath) {
		logger.Debug("volume already mounted in-container", "volumeId", v.VolumeID, "mountPath", v.MountPath)
		return nil
	}

	args := []string{
		"--allow-other",
		"--allow-delete",
		"--allow-overwrite",
		"--file-mode", "0666",
		"--dir-mode", "0777",
	}
	if v.Subpath != "" {
		// mount-s3 expects a trailing slash on --prefix.
		prefix := v.Subpath
		if prefix[len(prefix)-1] != '/' {
			prefix += "/"
		}
		args = append(args, "--prefix", prefix)
	}
	args = append(args, v.VolumeID, v.MountPath)

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = append(os.Environ(), translatedEnv()...)

	logger.Info("mounting in-container volume", "volumeId", v.VolumeID, "mountPath", v.MountPath, "subpath", v.Subpath)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mount-s3 failed: %w: %s", err, string(out))
	}

	if err := waitUntilReady(ctx, v.MountPath); err != nil {
		return fmt.Errorf("mount not ready: %w", err)
	}
	logger.Info("mounted in-container volume", "volumeId", v.VolumeID, "mountPath", v.MountPath)
	return nil
}

// translatedEnv maps the DAYTONA_INCONTAINER_AWS_* env vars the runner
// injected onto the AWS_* names mount-s3 expects. Using dedicated names on the
// runner side avoids leaking AWS_* vars into the user's shell by default.
//
// The credentials passed here are short-lived STS session credentials scoped
// (by the runner) to only the buckets this sandbox mounts. AWS_SESSION_TOKEN
// is therefore required — its presence is what tells the AWS SDK to treat
// the access key as a temporary session credential.
func translatedEnv() []string {
	var env []string
	if v := os.Getenv(envAWSRegion); v != "" {
		env = append(env, "AWS_REGION="+v)
	}
	if v := os.Getenv(envAWSEndpointURL); v != "" {
		env = append(env, "AWS_ENDPOINT_URL="+v)
	}
	if v := os.Getenv(envAWSAccessKeyID); v != "" {
		env = append(env, "AWS_ACCESS_KEY_ID="+v)
	}
	if v := os.Getenv(envAWSSecretAccessKey); v != "" {
		env = append(env, "AWS_SECRET_ACCESS_KEY="+v)
	}
	if v := os.Getenv(envAWSSessionToken); v != "" {
		env = append(env, "AWS_SESSION_TOKEN="+v)
	}
	return env
}

func isMountpoint(path string) bool {
	// Compare the device ID of path to its parent: if they differ, it's a mount.
	cleaned := filepath.Clean(path)
	parent := filepath.Dir(cleaned)

	pi, err := os.Stat(cleaned)
	if err != nil {
		return false
	}
	pp, err := os.Stat(parent)
	if err != nil {
		return false
	}

	pDev, ok1 := statDev(pi)
	parentDev, ok2 := statDev(pp)
	if !ok1 || !ok2 {
		return false
	}
	return pDev != parentDev
}

func waitUntilReady(ctx context.Context, path string) error {
	const maxAttempts = 50
	const sleep = 100 * time.Millisecond

	for i := 0; i < maxAttempts; i++ {
		if !isMountpoint(path) {
			return fmt.Errorf("mount disappeared during readiness check")
		}
		if _, err := os.ReadDir(path); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleep):
		}
	}
	return fmt.Errorf("mount did not become ready within timeout")
}
