# frozen_string_literal: true

require 'json'
require 'uri'

module Daytona
  class Daytona
    include Instrumentation

    # @return [Daytona::Config]
    attr_reader :config

    # @return [DaytonaApiClient]
    attr_reader :api_client

    # @return [DaytonaApiClient::SandboxApi]
    attr_reader :sandbox_api

    # @return [Daytona::VolumeService]
    attr_reader :volume

    # @return [DaytonaApiClient::ObjectStorageApi]
    attr_reader :object_storage_api

    # @return [DaytonaApiClient::SnapshotsApi]
    attr_reader :snapshots_api

    # @return [Daytona::SnapshotService]
    attr_reader :snapshot

    # @param config [Daytona::Config] Configuration options. Defaults to Daytona::Config.new
    def initialize(config = Config.new)
      @config = config
      ensure_access_token_defined

      otel_enabled = config._experimental&.dig('otel_enabled') || config.read_env('DAYTONA_EXPERIMENTAL_OTEL_ENABLED') == 'true'
      @otel_state = (::Daytona.init_otel(Sdk::VERSION) if otel_enabled)

      @api_client = build_api_client
      @sandbox_api = DaytonaApiClient::SandboxApi.new(api_client)
      @config_api = DaytonaApiClient::ConfigApi.new(api_client)
      @volume = VolumeService.new(DaytonaApiClient::VolumesApi.new(api_client), otel_state:)
      @object_storage_api = DaytonaApiClient::ObjectStorageApi.new(api_client)
      @snapshots_api = DaytonaApiClient::SnapshotsApi.new(api_client)
      @snapshot = SnapshotService.new(snapshots_api:, object_storage_api:, default_region_id: config.target,
                                      otel_state:)
    end

    # Shuts down OTel providers, flushing any pending telemetry data.
    #
    # @return [void]
    def close
      ::Daytona.shutdown_otel(@otel_state)
      @otel_state = nil
    end

    # Creates a sandbox with the specified parameters
    #
    # @param params [Daytona::CreateSandboxFromSnapshotParams, Daytona::CreateSandboxFromImageParams, Nil] Sandbox creation parameters
    # @return [Daytona::Sandbox] The created sandbox
    # @raise [Daytona::Sdk::Error] If auto_stop_interval or auto_archive_interval is negative
    def create(params = nil, on_snapshot_create_logs: nil)
      if params.nil?
        params = CreateSandboxFromSnapshotParams.new(language: CodeLanguage::PYTHON)
      elsif params.language.nil?
        params.language = CodeLanguage::PYTHON
      end

      unless CodeLanguage::ALL.include?(params.language.to_s.to_sym)
        raise ArgumentError,
              "Invalid #{CODE_TOOLBOX_LANGUAGE_LABEL}: #{params.language}. Supported languages: #{CodeLanguage::ALL.join(', ')}"
      end

      _create(params, on_snapshot_create_logs:)
    end

    # Deletes a Sandbox.
    #
    # @param sandbox [Daytona::Sandbox]
    # @return [void]
    def delete(sandbox) = sandbox.delete

    # Gets a Sandbox by its ID.
    #
    # @param id [String]
    # @return [Daytona::Sandbox]
    def get(id)
      sandbox_dto = sandbox_api.get_sandbox(id)
      to_sandbox(sandbox_dto:)
    end

    # Returns a paginated list of Sandboxes matching the given query.
    #
    # @param query [Daytona::ListSandboxesQuery, nil] Query parameters for filtering, sorting, and pagination
    # @return [Daytona::ListSandboxesResponse] Paginated list of Sandboxes with cursor for the next page
    # @raise [Daytona::Sdk::Error]
    #
    # @example
    #   cursor = nil
    #   loop do
    #     result = daytona.list(Daytona::ListSandboxesQuery.new(
    #       cursor: cursor,
    #       limit: 10,
    #       labels: { 'env' => 'dev' },
    #       states: ['started'],
    #       sort: 'createdAt',
    #       order: 'desc'
    #     ))
    #     result.items.each { |sandbox| puts sandbox.id }
    #     cursor = result.next_cursor
    #     break unless cursor
    #   end
    def list(query = nil)
      q = query || ListSandboxesQuery.new

      opts = {
        cursor: q.cursor,
        limit: q.limit,
        id: q.id,
        name: q.name,
        labels: q.labels ? JSON.dump(q.labels) : nil,
        states: q.states,
        snapshots: q.snapshots,
        region_ids: q.targets,
        min_cpu: q.min_cpu,
        max_cpu: q.max_cpu,
        min_memory_gi_b: q.min_memory_gi_b,
        max_memory_gi_b: q.max_memory_gi_b,
        min_disk_gi_b: q.min_disk_gi_b,
        max_disk_gi_b: q.max_disk_gi_b,
        is_public: q.is_public,
        is_recoverable: q.is_recoverable,
        created_at_after: q.created_at_after,
        created_at_before: q.created_at_before,
        last_event_after: q.last_activity_after,
        last_event_before: q.last_activity_before,
        sort: q.sort,
        order: q.order
      }.compact

      response = sandbox_api.list_sandboxes(opts)

      ListSandboxesResponse.new(
        next_cursor: response.next_cursor,
        items: response.items.map { |sandbox_dto| to_sandbox(sandbox_dto: sandbox_dto) }
      )
    end

    # Starts a Sandbox and waits for it to be ready.
    #
    # @param sandbox [Daytona::Sandbox]
    # @param timeout [Numeric] Maximum wait time in seconds (defaults to 60 s).
    # @return [void]
    def start(sandbox, timeout = Sandbox::DEFAULT_TIMEOUT) = sandbox.start(timeout)

    # Stops a Sandbox and waits for it to be stopped.
    #
    # @param sandbox [Daytona::Sandbox]
    # @param timeout [Numeric] Maximum wait time in seconds (defaults to 60 s).
    # @return [void]
    def stop(sandbox, timeout = Sandbox::DEFAULT_TIMEOUT) = sandbox.stop(timeout)

    instrument :create, :delete, :get, :list, :start, :stop, component: 'Daytona'

    private

    # @return [Daytona::OtelState, nil]
    attr_reader :otel_state

    # Creates a sandbox with the specified parameters
    #
    # @param params [Daytona::CreateSandboxFromSnapshotParams, Daytona::CreateSandboxFromImageParams] Sandbox creation parameters
    # @param timeout [Numeric] Maximum wait time in seconds (defaults to 60 s).
    # @param on_snapshot_create_logs [Proc]
    # @return [Daytona::Sandbox] The created sandbox
    # @raise [Daytona::Sdk::Error] If auto_stop_interval or auto_archive_interval is negative
    def _create(params, timeout: 60, on_snapshot_create_logs: nil)
      raise Sdk::Error, 'Timeout must be a non-negative number' if timeout.negative?

      start_time = Time.now

      raise Sdk::Error, 'auto_stop_interval must be a non-negative integer' if params.auto_stop_interval&.negative?

      if params.auto_archive_interval&.negative?
        raise Sdk::Error, 'auto_archive_interval must be a non-negative integer'
      end

      labels = params.labels&.dup || {}
      labels[CODE_TOOLBOX_LANGUAGE_LABEL] = params.language.to_s if params.language

      create_sandbox = DaytonaApiClient::CreateSandbox.new(
        user: params.os_user,
        env: params.env_vars || {},
        labels: labels,
        public: params.public,
        target: config.target,
        auto_stop_interval: params.auto_stop_interval,
        auto_archive_interval: params.auto_archive_interval,
        auto_delete_interval: params.auto_delete_interval,
        volumes: params.volumes,
        network_block_all: params.network_block_all,
        network_allow_list: params.network_allow_list
      )

      create_sandbox.snapshot = params.snapshot if params.respond_to?(:snapshot)

      if params.respond_to?(:image) && params.image.is_a?(String)
        create_sandbox.build_info = DaytonaApiClient::CreateBuildInfo.new(
          dockerfile_content: Image.base(params.image).dockerfile
        )
      elsif params.respond_to?(:image) && params.image.is_a?(Image)
        create_sandbox.build_info = DaytonaApiClient::CreateBuildInfo.new(
          context_hashes: SnapshotService.process_image_context(object_storage_api, params.image),
          dockerfile_content: params.image.dockerfile
        )
      end

      if params.respond_to?(:resources)
        create_sandbox.cpu = params.resources&.cpu
        create_sandbox.memory = params.resources&.memory
        create_sandbox.disk = params.resources&.disk
        create_sandbox.gpu = params.resources&.gpu
      end

      response = sandbox_api.create_sandbox(create_sandbox)

      if response.state == DaytonaApiClient::SandboxState::PENDING_BUILD && on_snapshot_create_logs
        # Wait for state to change from PENDING_BUILD before fetching logs
        while response.state == DaytonaApiClient::SandboxState::PENDING_BUILD
          sleep(1)
          response = sandbox_api.get_sandbox(response.id)
        end

        # Get build logs URL from API
        build_logs_response = sandbox_api.get_build_logs_url(response.id)
        uri = URI.parse("#{build_logs_response.url}?follow=true")

        headers = {}
        sandbox_api.api_client.update_params_for_auth!(headers, nil, ['bearer'])
        Util.stream_async(uri:, headers:, on_chunk: on_snapshot_create_logs)
      end

      sandbox = to_sandbox(sandbox_dto: response)

      if sandbox.state != DaytonaApiClient::SandboxState::STARTED
        sandbox.wait_for_sandbox_start([0.001, timeout - (Time.now - start_time)].max)
      end

      sandbox
    end

    # @return [void]
    # @raise [Daytona::Sdk::Error]
    def ensure_access_token_defined
      return if config.api_key

      raise Sdk::Error, 'API key or JWT token is required' unless config.jwt_token
      raise Sdk::Error, 'Organization ID is required when using JWT token' unless config.organization_id
    end

    # @return [DaytonaApiClient::ApiClient]
    def build_api_client
      DaytonaApiClient::ApiClient.new(api_client_config).tap do |client|
        client.default_headers[HEADER_SOURCE] = SOURCE_RUBY
        client.default_headers[HEADER_SDK_VERSION] = Sdk::VERSION
        client.default_headers[HEADER_ORGANIZATION_ID] = config.organization_id if config.jwt_token
        client.user_agent = "sdk-ruby/#{Sdk::VERSION}"
      end
    end

    # @return [DaytonaApiClient::Configuration]
    def api_client_config
      DaytonaApiClient::Configuration.new.configure do |api_config|
        uri = URI(config.api_url)
        api_config.scheme = uri.scheme
        api_config.host = uri.authority # Includes hostname:port
        api_config.base_path = uri.path

        api_config.access_token_getter = proc { config.api_key || config.jwt_token }
        api_config
      end
    end

    # @param sandbox_dto [DaytonaApiClient::Sandbox]
    # @return [Daytona::Sandbox]
    def to_sandbox(sandbox_dto:)
      Sandbox.new(
        sandbox_dto:,
        config:,
        sandbox_api:,
        otel_state: @otel_state
      )
    end

    SOURCE_RUBY = 'sdk-ruby'
    private_constant :SOURCE_RUBY

    HEADER_SOURCE = 'X-Daytona-Source'
    private_constant :HEADER_SOURCE

    HEADER_SDK_VERSION = 'X-Daytona-SDK-Version'
    private_constant :HEADER_SDK_VERSION

    HEADER_ORGANIZATION_ID = 'X-Daytona-Organization-ID'
    private_constant :HEADER_ORGANIZATION_ID
  end
end
