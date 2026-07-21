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

variable "runtime_tenant_ref" {
  description = "Optional tenant constraint for the installation Runtime credential."
  type        = string
  default     = null
  nullable    = true
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

variable "engine_concurrency" {
  description = "Maximum simultaneously claimed Invocations per service instance."
  type        = number
  default     = 4

  validation {
    condition     = var.engine_concurrency >= 1 && var.engine_concurrency == floor(var.engine_concurrency)
    error_message = "engine_concurrency must be a positive whole number."
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
