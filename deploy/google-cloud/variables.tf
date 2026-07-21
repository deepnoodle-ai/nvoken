variable "project_id" {
  description = "Google Cloud project that owns the nvoken deployment."
  type        = string

  validation {
    condition     = trimspace(var.project_id) != ""
    error_message = "project_id must not be blank."
  }
}

variable "region" {
  description = "Google Cloud region for every regional resource."
  type        = string
  default     = "us-central1"
}

variable "name" {
  description = "Base resource name."
  type        = string
  default     = "nvoken"

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{1,20}[a-z0-9]$", var.name))
    error_message = "name must be 3-22 lowercase letters, digits, or hyphens and start with a letter."
  }
}

variable "environment" {
  description = "Short deployment environment name."
  type        = string
  default     = "dev"

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{0,8}[a-z0-9]$", var.environment))
    error_message = "environment must be 2-10 lowercase letters, digits, or hyphens."
  }
}

variable "image_tag" {
  description = "Unique immutable image tag, normally the full Git commit SHA."
  type        = string

  validation {
    condition = (
      trimspace(var.image_tag) != "" &&
      lower(var.image_tag) != "latest" &&
      can(regex("^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$", var.image_tag))
    )
    error_message = "image_tag must be a valid unique tag and must not be latest."
  }
}

variable "anthropic_api_key_secret_id" {
  description = "Existing Secret Manager secret ID whose latest version contains ANTHROPIC_API_KEY."
  type        = string
  default     = null
  nullable    = true

  validation {
    condition     = var.anthropic_api_key_secret_id == null || trimspace(var.anthropic_api_key_secret_id) != ""
    error_message = "anthropic_api_key_secret_id must be null or nonblank."
  }
}

variable "openai_api_key_secret_id" {
  description = "Existing Secret Manager secret ID whose latest version contains OPENAI_API_KEY."
  type        = string
  default     = null
  nullable    = true

  validation {
    condition     = var.openai_api_key_secret_id == null || trimspace(var.openai_api_key_secret_id) != ""
    error_message = "openai_api_key_secret_id must be null or nonblank."
  }
}

variable "callback_signing_key_secret_id" {
  description = "Existing Secret Manager secret ID whose latest version contains the installation callback HMAC key. Null disables callback admission."
  type        = string
  default     = null
  nullable    = true

  validation {
    condition     = var.callback_signing_key_secret_id == null || trimspace(var.callback_signing_key_secret_id) != ""
    error_message = "callback_signing_key_secret_id must be null or nonblank."
  }
}

variable "callback_signing_key_id" {
  description = "Nonsecret receiver-facing identifier for the callback signing key."
  type        = string
  default     = "nvoken/installation/callback"

  validation {
    condition     = trimspace(var.callback_signing_key_id) != "" && length(var.callback_signing_key_id) <= 255
    error_message = "callback_signing_key_id must be nonblank and at most 255 characters."
  }
}

variable "callback_signing_key_version" {
  description = "Positive receiver-facing version of the callback signing key. Update with the Secret Manager version rollout."
  type        = number
  default     = 1

  validation {
    condition     = var.callback_signing_key_version >= 1 && var.callback_signing_key_version == floor(var.callback_signing_key_version)
    error_message = "callback_signing_key_version must be a positive whole number."
  }
}

variable "runtime_tenant_ref" {
  description = "Optional tenant constraint for the installation Runtime credential."
  type        = string
  default     = null
  nullable    = true
}

variable "invocation_execution_mode" {
  description = "Invocation execution topology. The Google paved path defaults to request-bound Cloud Tasks."
  type        = string
  default     = "cloud_tasks"

  validation {
    condition     = contains(["embedded", "cloud_tasks"], var.invocation_execution_mode)
    error_message = "invocation_execution_mode must be embedded or cloud_tasks."
  }
}

variable "min_instances" {
  description = "Service-level minimum instances. Combined mode requires at least one poller."
  type        = number
  default     = 1

  validation {
    condition     = var.min_instances >= 1 && var.min_instances == floor(var.min_instances)
    error_message = "min_instances must be a whole number of at least 1."
  }
}

variable "max_instances" {
  description = "Maximum Cloud Run service instances."
  type        = number
  default     = 3

  validation {
    condition     = var.max_instances >= 1 && var.max_instances == floor(var.max_instances)
    error_message = "max_instances must be a positive whole number."
  }
}

variable "request_concurrency" {
  description = "Maximum concurrent HTTP requests per service instance."
  type        = number
  default     = 16

  validation {
    condition     = var.request_concurrency >= 1 && var.request_concurrency <= 1000 && var.request_concurrency == floor(var.request_concurrency)
    error_message = "request_concurrency must be a whole number from 1 through 1000."
  }
}

variable "runtime_request_timeout_seconds" {
  description = "Cloud Run request ceiling; must exceed the application-led SSE rotation lifetime."
  type        = number
  default     = 3600

  validation {
    condition     = var.runtime_request_timeout_seconds >= 60 && var.runtime_request_timeout_seconds <= 3600 && var.runtime_request_timeout_seconds == floor(var.runtime_request_timeout_seconds)
    error_message = "runtime_request_timeout_seconds must be a whole number from 60 through 3600."
  }
}

variable "stream_max_lifetime_seconds" {
  description = "Application-led SSE rotation lifetime."
  type        = number
  default     = 3300

  validation {
    condition     = var.stream_max_lifetime_seconds >= 30 && var.stream_max_lifetime_seconds == floor(var.stream_max_lifetime_seconds)
    error_message = "stream_max_lifetime_seconds must be a whole number of at least 30."
  }
}

variable "stream_poll_interval_seconds" {
  description = "Postgres correctness-poll interval for active Session streams."
  type        = number
  default     = 1

  validation {
    condition     = var.stream_poll_interval_seconds >= 1 && var.stream_poll_interval_seconds == floor(var.stream_poll_interval_seconds)
    error_message = "stream_poll_interval_seconds must be a positive whole number."
  }
}

variable "stream_keepalive_interval_seconds" {
  description = "SSE comment keepalive interval."
  type        = number
  default     = 15

  validation {
    condition     = var.stream_keepalive_interval_seconds >= 1 && var.stream_keepalive_interval_seconds == floor(var.stream_keepalive_interval_seconds)
    error_message = "stream_keepalive_interval_seconds must be a positive whole number."
  }
}

variable "stream_write_timeout_seconds" {
  description = "Maximum duration of one SSE write before the slow client is disconnected."
  type        = number
  default     = 10

  validation {
    condition     = var.stream_write_timeout_seconds >= 1 && var.stream_write_timeout_seconds == floor(var.stream_write_timeout_seconds)
    error_message = "stream_write_timeout_seconds must be a positive whole number."
  }
}

variable "live_event_buffer" {
  description = "Bounded per-process live-event buffer depth."
  type        = number
  default     = 64

  validation {
    condition     = var.live_event_buffer >= 1 && var.live_event_buffer <= 10000 && var.live_event_buffer == floor(var.live_event_buffer)
    error_message = "live_event_buffer must be a whole number from 1 through 10000."
  }
}

variable "redis_memory_size_gb" {
  description = "Memory allocated to the private basic-tier Memorystore instance used only for live Pub/Sub."
  type        = number
  default     = 1

  validation {
    condition     = var.redis_memory_size_gb >= 1 && var.redis_memory_size_gb == floor(var.redis_memory_size_gb)
    error_message = "redis_memory_size_gb must be a positive whole number."
  }
}

variable "engine_concurrency" {
  description = "Maximum simultaneously claimed Invocations per service instance."
  type        = number
  default     = 4

  validation {
    condition     = var.engine_concurrency >= 1 && var.engine_concurrency == floor(var.engine_concurrency)
    error_message = "engine_concurrency must be a positive whole number."
  }
}

variable "executor_max_instances" {
  description = "Maximum private executor instances; executor minimum is fixed at zero."
  type        = number
  default     = 10

  validation {
    condition     = var.executor_max_instances >= 1 && var.executor_max_instances == floor(var.executor_max_instances)
    error_message = "executor_max_instances must be a positive whole number."
  }
}

variable "executor_request_concurrency" {
  description = "Maximum held Cloud Tasks requests per private executor instance."
  type        = number
  default     = 4

  validation {
    condition     = var.executor_request_concurrency >= 1 && var.executor_request_concurrency <= 1000 && var.executor_request_concurrency == floor(var.executor_request_concurrency)
    error_message = "executor_request_concurrency must be a whole number from 1 through 1000."
  }
}

variable "executor_database_max_connections" {
  description = "Maximum Postgres pool connections per private executor instance; one is reserved for cancellation notifications."
  type        = number
  default     = 4

  validation {
    condition     = var.executor_database_max_connections >= 2 && var.executor_database_max_connections == floor(var.executor_database_max_connections)
    error_message = "executor_database_max_connections must be a whole number of at least 2."
  }
}

variable "task_queue_max_concurrent_dispatches" {
  description = "Maximum concurrent Cloud Tasks deliveries; cannot exceed total executor request capacity."
  type        = number
  default     = 40

  validation {
    condition     = var.task_queue_max_concurrent_dispatches >= 1 && var.task_queue_max_concurrent_dispatches == floor(var.task_queue_max_concurrent_dispatches)
    error_message = "task_queue_max_concurrent_dispatches must be a positive whole number."
  }
}

variable "task_queue_max_dispatches_per_second" {
  description = "Regional execution queue rate limit."
  type        = number
  default     = 100

  validation {
    condition     = var.task_queue_max_dispatches_per_second > 0 && var.task_queue_max_dispatches_per_second <= 500
    error_message = "task_queue_max_dispatches_per_second must be greater than zero and at most 500."
  }
}

variable "task_queue_max_attempts" {
  description = "Finite transport delivery attempts before reconciliation is required."
  type        = number
  default     = 10

  validation {
    condition     = var.task_queue_max_attempts >= 1 && var.task_queue_max_attempts == floor(var.task_queue_max_attempts)
    error_message = "task_queue_max_attempts must be a positive whole number."
  }
}

variable "task_queue_max_retry_duration_seconds" {
  description = "Finite transport retry window."
  type        = number
  default     = 3600

  validation {
    condition     = var.task_queue_max_retry_duration_seconds >= 60 && var.task_queue_max_retry_duration_seconds == floor(var.task_queue_max_retry_duration_seconds)
    error_message = "task_queue_max_retry_duration_seconds must be a whole number of at least 60."
  }
}

variable "task_dispatch_deadline_seconds" {
  description = "Cloud Tasks and executor request deadline; the platform maximum is 1800 seconds."
  type        = number
  default     = 1800

  validation {
    condition     = var.task_dispatch_deadline_seconds >= 60 && var.task_dispatch_deadline_seconds <= 1800 && var.task_dispatch_deadline_seconds == floor(var.task_dispatch_deadline_seconds)
    error_message = "task_dispatch_deadline_seconds must be a whole number from 60 through 1800."
  }
}

variable "executor_attempt_timeout_seconds" {
  description = "Application attempt ceiling, leaving time for durable settlement before task timeout."
  type        = number
  default     = 1795

  validation {
    condition     = var.executor_attempt_timeout_seconds >= 1 && var.executor_attempt_timeout_seconds == floor(var.executor_attempt_timeout_seconds)
    error_message = "executor_attempt_timeout_seconds must be a positive whole number."
  }
}

variable "synthetic_dispatch_delay_seconds" {
  description = "Optional staging-only delay before synthetic settlement, used to prove request draining."
  type        = number
  default     = 0

  validation {
    condition     = var.synthetic_dispatch_delay_seconds >= 0 && var.synthetic_dispatch_delay_seconds <= 300 && var.synthetic_dispatch_delay_seconds == floor(var.synthetic_dispatch_delay_seconds)
    error_message = "synthetic_dispatch_delay_seconds must be a whole number from 0 through 300."
  }
}

variable "monitoring_notification_channels" {
  description = "Existing Monitoring notification channel resource names attached to every nvoken alert policy. Production requires at least one tested channel."
  type        = list(string)
  default     = []

  validation {
    condition = alltrue([
      for channel in var.monitoring_notification_channels :
      can(regex("^projects/[^/]+/notificationChannels/[^/]+$", channel))
    ])
    error_message = "monitoring_notification_channels entries must be full projects/.../notificationChannels/... resource names."
  }
}

variable "enable_monitoring_dashboard" {
  description = "Create the single Terraform-managed nvoken operations dashboard."
  type        = bool
  default     = true
}

variable "monitoring_alert_thresholds" {
  description = "Conservative alert thresholds. Count conditions alert when the aligned value is greater than the configured value."
  type = object({
    public_5xx_count               = number
    aged_dispatch_count            = number
    dispatch_publish_failure_count = number
    executor_retry_count           = number
    executor_auth_count            = number
    task_delivery_rejection_count  = number
    provider_failure_count         = number
    callback_exhaustion_count      = number
    callback_worker_failure_count  = number
    database_connections           = number
    database_storage_utilization   = number
    database_unhealthy_state       = number
  })
  default = {
    public_5xx_count               = 5
    aged_dispatch_count            = 0
    dispatch_publish_failure_count = 0
    executor_retry_count           = 0
    executor_auth_count            = 0
    task_delivery_rejection_count  = 5
    provider_failure_count         = 5
    callback_exhaustion_count      = 0
    callback_worker_failure_count  = 2
    database_connections           = 80
    database_storage_utilization   = 0.85
    database_unhealthy_state       = 0
  }

  validation {
    condition = (
      var.monitoring_alert_thresholds.public_5xx_count >= 0 &&
      var.monitoring_alert_thresholds.aged_dispatch_count >= 0 &&
      var.monitoring_alert_thresholds.dispatch_publish_failure_count >= 0 &&
      var.monitoring_alert_thresholds.executor_retry_count >= 0 &&
      var.monitoring_alert_thresholds.executor_auth_count >= 0 &&
      var.monitoring_alert_thresholds.task_delivery_rejection_count >= 0 &&
      var.monitoring_alert_thresholds.provider_failure_count >= 1 &&
      var.monitoring_alert_thresholds.callback_exhaustion_count >= 0 &&
      var.monitoring_alert_thresholds.callback_worker_failure_count >= 1 &&
      var.monitoring_alert_thresholds.database_connections >= 1 &&
      var.monitoring_alert_thresholds.database_storage_utilization > 0 &&
      var.monitoring_alert_thresholds.database_storage_utilization < 1 &&
      var.monitoring_alert_thresholds.database_unhealthy_state >= 0
    )
    error_message = "Monitoring count thresholds must be nonnegative, sustained-failure and connection thresholds must be positive, and database_storage_utilization must be between 0 and 1."
  }
}

variable "monitoring_alert_windows_seconds" {
  description = "Alert condition windows in whole minutes; zero is allowed for discrete failure conditions."
  type = object({
    public_5xx               = number
    uptime                   = number
    aged_dispatch            = number
    dispatch_publish_failure = number
    executor_retry           = number
    executor_auth            = number
    task_delivery_rejection  = number
    provider_failure         = number
    callback_exhaustion      = number
    callback_worker_failure  = number
    database_capacity        = number
    database_health          = number
  })
  default = {
    public_5xx               = 300
    uptime                   = 180
    aged_dispatch            = 300
    dispatch_publish_failure = 0
    executor_retry           = 300
    executor_auth            = 0
    task_delivery_rejection  = 300
    provider_failure         = 300
    callback_exhaustion      = 0
    callback_worker_failure  = 300
    database_capacity        = 300
    database_health          = 300
  }

  validation {
    condition = alltrue([
      for seconds in values(var.monitoring_alert_windows_seconds) :
      seconds >= 0 && seconds <= 3600 && seconds == floor(seconds) && seconds % 60 == 0
    ])
    error_message = "Monitoring alert windows must be whole-minute values from 0 through 3600 seconds."
  }
}

variable "database_max_connections" {
  description = "Maximum Postgres pool connections per service instance."
  type        = number
  default     = 10

  validation {
    condition     = var.database_max_connections >= 2 && var.database_max_connections == floor(var.database_max_connections)
    error_message = "database_max_connections must be a whole number of at least 2; cancellation notifications reserve one connection."
  }
}

variable "shutdown_timeout_seconds" {
  description = "Total process shutdown budget; Cloud Run currently allows ten seconds."
  type        = number
  default     = 8

  validation {
    condition     = var.shutdown_timeout_seconds >= 2 && var.shutdown_timeout_seconds < 10 && var.shutdown_timeout_seconds == floor(var.shutdown_timeout_seconds)
    error_message = "shutdown_timeout_seconds must be a whole number from 2 through 9."
  }
}

variable "engine_drain_grace_seconds" {
  description = "Cooperative engine drain within the total shutdown budget."
  type        = number
  default     = 7

  validation {
    condition     = var.engine_drain_grace_seconds >= 1 && var.engine_drain_grace_seconds == floor(var.engine_drain_grace_seconds)
    error_message = "engine_drain_grace_seconds must be a positive whole number."
  }
}

variable "engine_execution_segment_ceiling_seconds" {
  description = "Maximum model-execution segment before durable settlement; checkpointing is not yet available."
  type        = number
  default     = 900

  validation {
    condition     = var.engine_execution_segment_ceiling_seconds >= 2 && var.engine_execution_segment_ceiling_seconds == floor(var.engine_execution_segment_ceiling_seconds)
    error_message = "engine_execution_segment_ceiling_seconds must be a whole number of at least 2."
  }
}

variable "engine_settlement_reserve_seconds" {
  description = "Time reserved after model cancellation for fenced settlement."
  type        = number
  default     = 5

  validation {
    condition     = var.engine_settlement_reserve_seconds >= 1 && var.engine_settlement_reserve_seconds == floor(var.engine_settlement_reserve_seconds)
    error_message = "engine_settlement_reserve_seconds must be a positive whole number."
  }
}

variable "invocation_default_wall_clock_timeout_seconds" {
  description = "Default logical wall-clock limit for an Invocation."
  type        = number
  default     = 1800
}

variable "invocation_default_active_execution_timeout_seconds" {
  description = "Default active model-execution limit for an Invocation."
  type        = number
  default     = 1800
}

variable "invocation_default_max_iterations" {
  description = "Default model-request limit for an Invocation."
  type        = number
  default     = 1
}

variable "invocation_max_wall_clock_timeout_seconds" {
  description = "Installation maximum logical wall-clock limit."
  type        = number
  default     = 86400
}

variable "invocation_max_active_execution_timeout_seconds" {
  description = "Installation maximum active model-execution limit."
  type        = number
  default     = 86400
}

variable "invocation_max_output_tokens" {
  description = "Installation maximum requested output-token limit."
  type        = number
  default     = 1000000
}

variable "invocation_max_estimated_cost_microusd" {
  description = "Installation maximum requested estimated-cost limit in integer micro-USD."
  type        = number
  default     = 1000000000
}

variable "invocation_max_iterations" {
  description = "Installation maximum model-request limit."
  type        = number
  default     = 100
}

variable "cpu" {
  description = "Cloud Run vCPU limit."
  type        = string
  default     = "1"
}

variable "memory" {
  description = "Cloud Run memory limit. Instance-based CPU requires at least 512Mi."
  type        = string
  default     = "512Mi"
}

variable "network_cidr" {
  description = "CIDR for the dedicated direct-VPC-egress subnet."
  type        = string
  default     = "10.42.0.0/24"
}

variable "private_services_prefix_length" {
  description = "Prefix length allocated to private services access."
  type        = number
  default     = 16
}

variable "database_version" {
  description = "Cloud SQL PostgreSQL version tested by nvoken."
  type        = string
  default     = "POSTGRES_17"
}

variable "database_name" {
  description = "Postgres database name."
  type        = string
  default     = "nvoken"
}

variable "database_user" {
  description = "Postgres application user."
  type        = string
  default     = "nvoken"
}

variable "database_tier" {
  description = "Cloud SQL machine tier."
  type        = string
  default     = "db-custom-1-3840"
}

variable "database_availability_type" {
  description = "Cloud SQL availability type. REGIONAL is recommended for production."
  type        = string
  default     = "ZONAL"

  validation {
    condition     = contains(["ZONAL", "REGIONAL"], var.database_availability_type)
    error_message = "database_availability_type must be ZONAL or REGIONAL."
  }
}

variable "database_deletion_protection" {
  description = "Protect the Cloud SQL instance and its settings from deletion."
  type        = bool
  default     = true
}

variable "service_deletion_protection" {
  description = "Protect the Cloud Run service from accidental Terraform deletion."
  type        = bool
  default     = false
}

variable "labels" {
  description = "Additional labels for supported resources."
  type        = map(string)
  default     = {}
}
