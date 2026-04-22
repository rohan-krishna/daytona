/*
 * Copyright Daytona Platforms Inc.
 * SPDX-License-Identifier: AGPL-3.0
 */

import { ApiProperty, ApiSchema } from '@nestjs/swagger'
import { IsIn, IsNotEmpty, IsString } from 'class-validator'

@ApiSchema({ name: 'UpdateOrganizationDefaultVolumeBackend' })
export class UpdateOrganizationDefaultVolumeBackendDto {
  @ApiProperty({
    description:
      'The default volume backend for the organization. `s3fuse-legacy` mounts on the runner host using the runner’s AWS credentials (legacy behavior). `s3fuse` and `experimental` mount inside the sandbox using short-lived, bucket-scoped STS credentials.',
    example: 's3fuse-legacy',
    enum: ['s3fuse-legacy', 's3fuse', 'experimental'],
  })
  @IsString()
  @IsNotEmpty()
  @IsIn(['s3fuse-legacy', 's3fuse', 'experimental'])
  defaultVolumeBackend: string
}
