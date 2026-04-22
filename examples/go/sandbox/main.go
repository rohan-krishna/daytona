// Copyright 2025 Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log"
	"time"

	"github.com/daytonaio/daytona/libs/sdk-go/pkg/daytona"
	"github.com/daytonaio/daytona/libs/sdk-go/pkg/options"
	"github.com/daytonaio/daytona/libs/sdk-go/pkg/types"
)

func main() {
	// Create a new Daytona client using environment variables
	// Set DAYTONA_API_KEY before running
	client, err := daytona.NewClient()
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()

	// Create a sandbox with Python
	log.Println("\nCreating sandbox...")
	params := types.SnapshotParams{
		SandboxBaseParams: types.SandboxBaseParams{
			Language: types.CodeLanguagePython,
			EnvVars: map[string]string{
				"EXAMPLE_VAR": "example_value",
			},
		},
	}

	sandbox, err := client.Create(ctx, params, options.WithTimeout(90*time.Second))
	if err != nil {
		log.Fatalf("Failed to create sandbox: %v", err)
	}

	log.Printf("✓ Created sandbox: %s (ID: %s)\n", sandbox.Name, sandbox.ID)

	// Get sandbox info
	homeDir, err := sandbox.GetUserHomeDir(ctx)
	if err != nil {
		log.Fatalf("Failed to get home directory: %v", err)
	}
	log.Printf("Home directory: %s\n", homeDir)

	workDir, err := sandbox.GetWorkingDir(ctx)
	if err != nil {
		log.Fatalf("Failed to get working directory: %v", err)
	}
	log.Printf("Working directory: %s\n", workDir)

	log.Println("Listing all sandboxes...")
	var cursor *string
	limit := 10
	sort := "createdAt"
	order := "desc"
	for {
		sandboxList, err := client.List(ctx, &daytona.ListSandboxesQuery{
			Cursor: cursor,
			Limit:  &limit,
			Labels: map[string]string{"env": "dev"},
			States: []string{"started"},
			Sort:   &sort,
			Order:  &order,
		})
		if err != nil {
			log.Fatalf("Failed to list sandboxes: %v", err)
		}
		for _, sb := range sandboxList.Items {
			log.Println(sb.ID)
		}
		cursor = sandboxList.NextCursor
		if cursor == nil {
			break
		}
	}

	// Delete the sandbox
	log.Println("\nCleaning up...")
	if err := sandbox.Delete(ctx); err != nil {
		log.Fatalf("Failed to delete sandbox: %v", err)
	}
	log.Println("✓ Sandbox deleted")

	log.Println("\n✓ All sandbox operations completed successfully!")
}
