// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: AGPL-3.0

package volume

import "context"

// Mounter abstracts how a volume is mounted onto the host filesystem.
// The runner bind-mounts the resulting host path into the sandbox container.
// Implementations may use different backends (e.g. S3 FUSE, experimental, etc.).
type Mounter interface {
	// Mount ensures the volume is accessible at mountPath on the host.
	// volumeID is the backend-specific identifier (e.g. S3 bucket name).
	// The call must be idempotent — if already mounted, it returns nil.
	Mount(ctx context.Context, volumeID string, mountPath string) error

	// Unmount tears down the mount at the given path.
	Unmount(ctx context.Context, mountPath string) error

	// IsMounted reports whether mountPath is an active mountpoint.
	IsMounted(mountPath string) bool

	// WaitUntilReady blocks until the filesystem at mountPath is responsive
	// (i.e. Stat and ReadDir succeed). Implementations may return immediately
	// if the backend mounts synchronously.
	WaitUntilReady(ctx context.Context, mountPath string) error
}
