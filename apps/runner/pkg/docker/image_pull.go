// Copyright 2025 Daytona Platforms Inc.
// SPDX-License-Identifier: AGPL-3.0

package docker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"

	"github.com/daytonaio/common-go/pkg/log"
	"github.com/daytonaio/common-go/pkg/timer"
	"github.com/daytonaio/runner/pkg/api/dto"

	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/pkg/jsonmessage"
)

func (d *DockerClient) PullImage(ctx context.Context, imageName string, reg *dto.RegistryDTO, sandboxId *string) (*image.InspectResponse, error) {
	defer timer.Timer()()

	tag := "latest"
	lastColonIndex := strings.LastIndex(imageName, ":")
	if lastColonIndex != -1 {
		tag = imageName[lastColonIndex+1:]
	}

	if tag != "latest" {
		inspect, err := d.apiClient.ImageInspect(ctx, imageName)
		if err == nil {
			return &inspect, nil
		}
	}

	d.logger.InfoContext(ctx, "Pulling image", "imageName", imageName)

	if sandboxId != nil {
		d.pullTracker.Add(*sandboxId)
		defer d.pullTracker.Remove(*sandboxId)
	}

	auth, err := getRegistryAuth(ctx, reg)
	if err != nil {
		return nil, err
	}

	responseBody, err := d.apiClient.ImagePull(ctx, imageName, image.PullOptions{
		RegistryAuth: auth,
		Platform:     "linux/amd64",
	})
	if err != nil {
		return nil, err
	}
	defer responseBody.Close()

	err = jsonmessage.DisplayJSONMessagesStream(responseBody, io.Writer(&log.DebugLogWriter{}), 0, true, nil)
	if err != nil {
		return nil, err
	}

	d.logger.InfoContext(ctx, "Image pulled successfully", "imageName", imageName)

	inspect, err := d.apiClient.ImageInspect(ctx, imageName)
	if err != nil {
		return nil, err
	}

	return &inspect, nil
}

// getRegistryAuth returns a base64-encoded Docker auth header for the
// registry. For ECR URLs the resolver performs sts:AssumeRole + ECR
// GetAuthorizationToken on cache miss and reuses the cached token otherwise.
func getRegistryAuth(ctx context.Context, reg *dto.RegistryDTO) (string, error) {
	if !shouldResolveAuth(reg) {
		// Sometimes registry auth fails if "" is sent, so sending "empty" instead.
		return "empty", nil
	}

	username, password, err := resolveRegistryCredentials(ctx, reg)
	if err != nil {
		return "", err
	}

	authConfig := registry.AuthConfig{
		Username: username,
		Password: password,
	}
	encodedJSON, err := json.Marshal(authConfig)
	if err != nil {
		return "empty", nil
	}

	return base64.URLEncoding.EncodeToString(encodedJSON), nil
}
