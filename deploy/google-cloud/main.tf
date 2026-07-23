data "google_project" "current" {
  project_id = var.project_id
}

locals {
  resource_name         = "${var.name}-${var.environment}"
  runtime_account_id    = length(local.resource_name) <= 30 ? local.resource_name : "${substr(local.resource_name, 0, 21)}-${substr(sha256(local.resource_name), 0, 8)}"
  build_account_name    = "${local.resource_name}-build"
  build_account_id      = length(local.build_account_name) <= 30 ? local.build_account_name : "${substr(local.resource_name, 0, 21)}-${substr(sha256(local.build_account_name), 0, 8)}"
  migrate_account_name  = "${local.resource_name}-migrate"
  migrate_account_id    = length(local.migrate_account_name) <= 30 ? local.migrate_account_name : "${substr(local.resource_name, 0, 21)}-${substr(sha256(local.migrate_account_name), 0, 8)}"
  executor_account_name = "${local.resource_name}-executor"
  executor_account_id   = length(local.executor_account_name) <= 30 ? local.executor_account_name : "${substr(local.resource_name, 0, 21)}-${substr(sha256(local.executor_account_name), 0, 8)}"
  task_caller_name      = "${local.resource_name}-task-call"
  task_caller_id        = length(local.task_caller_name) <= 30 ? local.task_caller_name : "${substr(local.resource_name, 0, 21)}-${substr(sha256(local.task_caller_name), 0, 8)}"
  build_source_bucket   = "${var.project_id}-${substr(sha256("${var.project_id}/${local.resource_name}"), 0, 12)}-nvoken-build"
  image                 = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.images.repository_id}/nvokend:${var.image_tag}"
  public_base_url       = var.public_base_url == null ? "https://${local.resource_name}-${data.google_project.current.number}.${var.region}.run.app" : trimsuffix(trimspace(var.public_base_url), "/")
  labels = merge(var.labels, {
    application = "nvoken"
    environment = var.environment
    managed_by  = "terraform"
  })
  provider_secrets = merge(
    var.anthropic_api_key_secret_id == null ? {} : { ANTHROPIC_API_KEY = var.anthropic_api_key_secret_id },
    var.openai_api_key_secret_id == null ? {} : { OPENAI_API_KEY = var.openai_api_key_secret_id },
  )
  credential_encryption_secrets = var.provider_credential_encryption_keys_secret_id == null ? {} : {
    PROVIDER_CREDENTIAL_ENCRYPTION_KEYS = var.provider_credential_encryption_keys_secret_id
  }
  database_url = format(
    "postgres://%s:%s@%s:5432/%s?sslmode=require",
    urlencode(var.database_user),
    urlencode(random_password.database.result),
    google_sql_database_instance.runtime.private_ip_address,
    urlencode(var.database_name),
  )
}

resource "google_project_service" "required" {
  for_each = toset([
    "artifactregistry.googleapis.com",
    "cloudbuild.googleapis.com",
    "cloudtasks.googleapis.com",
    "compute.googleapis.com",
    "iam.googleapis.com",
    "logging.googleapis.com",
    "monitoring.googleapis.com",
    "redis.googleapis.com",
    "run.googleapis.com",
    "secretmanager.googleapis.com",
    "servicenetworking.googleapis.com",
    "sqladmin.googleapis.com",
    "storage.googleapis.com",
  ])

  project            = var.project_id
  service            = each.value
  disable_on_destroy = false
}

resource "google_artifact_registry_repository" "images" {
  project       = var.project_id
  location      = var.region
  repository_id = local.resource_name
  description   = "Immutable nvoken service images"
  format        = "DOCKER"
  labels        = local.labels

  depends_on = [google_project_service.required]
}

resource "google_service_account" "build" {
  project      = var.project_id
  account_id   = local.build_account_id
  display_name = "${local.resource_name} Cloud Build"

  depends_on = [google_project_service.required]
}

resource "google_storage_bucket" "build_source" {
  project                     = var.project_id
  name                        = local.build_source_bucket
  location                    = var.region
  force_destroy               = false
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  labels                      = local.labels

  lifecycle_rule {
    condition {
      age = 7
    }
    action {
      type = "Delete"
    }
  }

  depends_on = [google_project_service.required]
}

resource "google_artifact_registry_repository_iam_member" "build_writer" {
  project    = var.project_id
  location   = google_artifact_registry_repository.images.location
  repository = google_artifact_registry_repository.images.name
  role       = "roles/artifactregistry.writer"
  member     = "serviceAccount:${google_service_account.build.email}"
}

resource "google_project_iam_member" "build_logging" {
  project = var.project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.build.email}"
}

resource "google_storage_bucket_iam_member" "build_source_reader" {
  bucket = google_storage_bucket.build_source.name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${google_service_account.build.email}"
}

resource "terraform_data" "build_ready" {
  input = google_service_account.build.email

  depends_on = [
    google_artifact_registry_repository_iam_member.build_writer,
    google_project_iam_member.build_logging,
    google_storage_bucket_iam_member.build_source_reader,
  ]
}

resource "google_compute_network" "runtime" {
  project                 = var.project_id
  name                    = "${local.resource_name}-vpc"
  auto_create_subnetworks = false
  routing_mode            = "REGIONAL"

  depends_on = [google_project_service.required]
}

resource "google_compute_subnetwork" "runtime" {
  project                  = var.project_id
  name                     = "${local.resource_name}-${var.region}"
  region                   = var.region
  network                  = google_compute_network.runtime.id
  ip_cidr_range            = var.network_cidr
  private_ip_google_access = true
}

resource "google_compute_global_address" "private_services" {
  project       = var.project_id
  name          = "${local.resource_name}-private-services"
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = var.private_services_prefix_length
  network       = google_compute_network.runtime.id
}

resource "google_service_networking_connection" "private_services" {
  network                 = google_compute_network.runtime.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.private_services.name]

  depends_on = [google_project_service.required]
}

resource "google_redis_instance" "live_events" {
  project                 = var.project_id
  name                    = "${local.resource_name}-live"
  display_name            = "${local.resource_name} live event fan-out"
  region                  = var.region
  tier                    = "BASIC"
  memory_size_gb          = var.redis_memory_size_gb
  redis_version           = "REDIS_7_2"
  authorized_network      = google_compute_network.runtime.id
  connect_mode            = "DIRECT_PEERING"
  auth_enabled            = true
  transit_encryption_mode = "SERVER_AUTHENTICATION"
  labels                  = local.labels

  depends_on = [google_project_service.required]
}

resource "google_sql_database_instance" "runtime" {
  project             = var.project_id
  name                = "${local.resource_name}-postgres"
  region              = var.region
  database_version    = var.database_version
  deletion_protection = var.database_deletion_protection

  settings {
    tier                        = var.database_tier
    edition                     = "ENTERPRISE"
    availability_type           = var.database_availability_type
    disk_type                   = "PD_SSD"
    disk_size                   = 20
    disk_autoresize             = true
    deletion_protection_enabled = var.database_deletion_protection

    backup_configuration {
      enabled                        = true
      point_in_time_recovery_enabled = true
      transaction_log_retention_days = 7

      backup_retention_settings {
        retained_backups = 7
        retention_unit   = "COUNT"
      }
    }

    insights_config {
      query_insights_enabled  = true
      query_string_length     = 1024
      record_application_tags = false
      record_client_address   = false
    }

    ip_configuration {
      ipv4_enabled    = false
      private_network = google_compute_network.runtime.id
      ssl_mode        = "ENCRYPTED_ONLY"
    }

    user_labels = local.labels
  }

  depends_on = [
    google_project_service.required,
    google_service_networking_connection.private_services,
  ]
}

resource "google_sql_database" "runtime" {
  project  = var.project_id
  name     = var.database_name
  instance = google_sql_database_instance.runtime.name
}

resource "random_password" "database" {
  length  = 32
  special = false
}

resource "google_sql_user" "runtime" {
  project  = var.project_id
  name     = var.database_user
  instance = google_sql_database_instance.runtime.name
  password = random_password.database.result
}

resource "random_password" "runtime_api_key" {
  length  = 48
  special = false
}

resource "random_password" "bootstrap_owner_secret" {
  length  = 48
  special = false
}

resource "random_id" "credential_delivery_key" {
  byte_length = 32
}

resource "google_secret_manager_secret" "database_url" {
  project   = var.project_id
  secret_id = "${local.resource_name}-database-url"
  labels    = local.labels

  replication {
    auto {}
  }

  depends_on = [google_project_service.required]
}

resource "google_secret_manager_secret_version" "database_url" {
  secret      = google_secret_manager_secret.database_url.id
  secret_data = local.database_url

  depends_on = [google_sql_database.runtime, google_sql_user.runtime]
}

resource "google_secret_manager_secret" "runtime_api_key" {
  project   = var.project_id
  secret_id = "${local.resource_name}-runtime-api-key"
  labels    = local.labels

  replication {
    auto {}
  }

  depends_on = [google_project_service.required]
}

resource "google_secret_manager_secret_version" "runtime_api_key" {
  secret      = google_secret_manager_secret.runtime_api_key.id
  secret_data = random_password.runtime_api_key.result
}

resource "google_secret_manager_secret" "bootstrap_owner_secret" {
  project   = var.project_id
  secret_id = "${local.resource_name}-bootstrap-owner"
  labels    = local.labels

  replication {
    auto {}
  }

  depends_on = [google_project_service.required]
}

resource "google_secret_manager_secret_version" "bootstrap_owner_secret" {
  secret      = google_secret_manager_secret.bootstrap_owner_secret.id
  secret_data = random_password.bootstrap_owner_secret.result
}

resource "google_secret_manager_secret" "credential_delivery_key" {
  project   = var.project_id
  secret_id = "${local.resource_name}-credential-delivery-key"
  labels    = local.labels

  replication {
    auto {}
  }

  depends_on = [google_project_service.required]
}

resource "google_secret_manager_secret_version" "credential_delivery_key" {
  secret      = google_secret_manager_secret.credential_delivery_key.id
  secret_data = random_id.credential_delivery_key.b64_url
}

resource "google_secret_manager_secret" "redis_auth" {
  project   = var.project_id
  secret_id = "${local.resource_name}-redis-auth"
  labels    = local.labels

  replication {
    auto {}
  }

  depends_on = [google_project_service.required]
}

resource "google_secret_manager_secret_version" "redis_auth" {
  secret      = google_secret_manager_secret.redis_auth.id
  secret_data = google_redis_instance.live_events.auth_string
}

resource "google_service_account" "runtime" {
  project      = var.project_id
  account_id   = local.runtime_account_id
  display_name = "${local.resource_name} Cloud Run runtime"
}

resource "google_service_account" "migrate" {
  project      = var.project_id
  account_id   = local.migrate_account_id
  display_name = "${local.resource_name} database migration"
}

resource "google_service_account" "executor" {
  project      = var.project_id
  account_id   = local.executor_account_id
  display_name = "${local.resource_name} private executor"
}

resource "google_service_account" "task_caller" {
  project      = var.project_id
  account_id   = local.task_caller_id
  display_name = "${local.resource_name} Cloud Tasks OIDC caller"
}

resource "google_secret_manager_secret_iam_member" "generated" {
  for_each = {
    bootstrap_owner         = google_secret_manager_secret.bootstrap_owner_secret.secret_id
    credential_delivery_key = google_secret_manager_secret.credential_delivery_key.secret_id
    database_url            = google_secret_manager_secret.database_url.secret_id
    runtime_api_key         = google_secret_manager_secret.runtime_api_key.secret_id
    redis_auth              = google_secret_manager_secret.redis_auth.secret_id
  }

  project   = var.project_id
  secret_id = each.value
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.runtime.email}"
}

resource "google_secret_manager_secret_iam_member" "provider_runtime" {
  for_each = var.invocation_execution_mode == "embedded" ? local.provider_secrets : {}

  project   = var.project_id
  secret_id = each.value
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.runtime.email}"
}

resource "google_secret_manager_secret_iam_member" "provider_executor" {
  for_each = var.invocation_execution_mode == "cloud_tasks" ? local.provider_secrets : {}

  project   = var.project_id
  secret_id = each.value
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.executor.email}"
}

resource "google_secret_manager_secret_iam_member" "credential_encryption_runtime" {
  for_each = local.credential_encryption_secrets

  project   = var.project_id
  secret_id = each.value
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.runtime.email}"
}

resource "google_secret_manager_secret_iam_member" "credential_encryption_executor" {
  for_each = var.invocation_execution_mode == "cloud_tasks" ? local.credential_encryption_secrets : {}

  project   = var.project_id
  secret_id = each.value
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.executor.email}"
}

resource "google_secret_manager_secret_iam_member" "callback_runtime" {
  count = var.callback_signing_key_secret_id == null ? 0 : 1

  project   = var.project_id
  secret_id = var.callback_signing_key_secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.runtime.email}"
}

resource "google_secret_manager_secret_iam_member" "migration_database" {
  project   = var.project_id
  secret_id = google_secret_manager_secret.database_url.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.migrate.email}"
}

resource "google_secret_manager_secret_iam_member" "executor_database" {
  project   = var.project_id
  secret_id = google_secret_manager_secret.database_url.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.executor.email}"
}

resource "google_secret_manager_secret_iam_member" "executor_redis_auth" {
  project   = var.project_id
  secret_id = google_secret_manager_secret.redis_auth.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.executor.email}"
}

resource "google_cloud_tasks_queue_iam_member" "runtime_cloud_tasks_enqueuer" {
  project  = var.project_id
  location = google_cloud_tasks_queue.execution.location
  name     = google_cloud_tasks_queue.execution.name
  role     = "roles/cloudtasks.enqueuer"
  member   = "serviceAccount:${google_service_account.runtime.email}"
}

resource "google_cloud_tasks_queue_iam_member" "runtime_cloud_tasks_viewer" {
  project  = var.project_id
  location = google_cloud_tasks_queue.execution.location
  name     = google_cloud_tasks_queue.execution.name
  role     = "roles/cloudtasks.viewer"
  member   = "serviceAccount:${google_service_account.runtime.email}"
}

resource "google_service_account_iam_member" "runtime_acts_as_task_caller" {
  service_account_id = google_service_account.task_caller.name
  role               = "roles/iam.serviceAccountUser"
  member             = "serviceAccount:${google_service_account.runtime.email}"
}

resource "google_cloud_tasks_queue" "execution" {
  project  = var.project_id
  location = var.region
  name     = "${local.resource_name}-execution"

  rate_limits {
    max_dispatches_per_second = var.task_queue_max_dispatches_per_second
    max_concurrent_dispatches = var.task_queue_max_concurrent_dispatches
  }

  retry_config {
    max_attempts       = var.task_queue_max_attempts
    max_retry_duration = "${var.task_queue_max_retry_duration_seconds}s"
    min_backoff        = "1s"
    max_backoff        = "60s"
    max_doublings      = 5
  }

  stackdriver_logging_config {
    sampling_ratio = 1
  }

  depends_on = [google_project_service.required]
}

resource "google_cloud_run_v2_job" "migrate" {
  project             = var.project_id
  name                = "${local.resource_name}-migrate"
  location            = var.region
  deletion_protection = false
  labels              = local.labels

  template {
    task_count  = 1
    parallelism = 1

    template {
      service_account = google_service_account.migrate.email
      timeout         = "600s"
      max_retries     = 0

      containers {
        name  = "nvokend"
        image = local.image
        args  = ["migrate"]

        env {
          name = "DATABASE_URL"
          value_source {
            secret_key_ref {
              secret  = google_secret_manager_secret.database_url.secret_id
              version = "latest"
            }
          }
        }

        env {
          name  = "MIGRATION_TIMEOUT"
          value = "5m"
        }

        env {
          name  = "NVOKEN_CURRENT_BUILD_VERSION"
          value = var.previous_build_version
        }

        env {
          name  = "NVOKEN_CURRENT_SCHEMA_VERSION"
          value = tostring(var.previous_schema_version)
        }

        env {
          name  = "NVOKEN_MIGRATION_MODE"
          value = var.migration_mode
        }

        resources {
          limits = {
            cpu    = var.cpu
            memory = var.memory
          }
        }
      }

      vpc_access {
        egress = "PRIVATE_RANGES_ONLY"

        network_interfaces {
          network    = google_compute_network.runtime.id
          subnetwork = google_compute_subnetwork.runtime.id
        }
      }
    }
  }

  depends_on = [
    google_project_service.required,
    google_secret_manager_secret_iam_member.migration_database,
    google_secret_manager_secret_version.database_url,
  ]
}

resource "google_cloud_run_v2_service" "executor" {
  project              = var.project_id
  name                 = "${local.resource_name}-executor"
  location             = var.region
  ingress              = "INGRESS_TRAFFIC_INTERNAL_ONLY"
  invoker_iam_disabled = false
  deletion_protection  = var.service_deletion_protection
  labels               = local.labels

  lifecycle {
    precondition {
      condition     = var.task_queue_max_concurrent_dispatches <= var.executor_max_instances * var.executor_request_concurrency
      error_message = "task queue concurrency cannot exceed declared executor request capacity."
    }
    precondition {
      condition     = var.executor_attempt_timeout_seconds + var.engine_settlement_reserve_seconds <= var.task_dispatch_deadline_seconds
      error_message = "executor attempt timeout must leave the configured settlement reserve inside the task deadline."
    }
    precondition {
      condition     = var.engine_execution_segment_ceiling_seconds <= var.executor_attempt_timeout_seconds
      error_message = "engine execution segment ceiling cannot exceed the executor attempt timeout."
    }
    precondition {
      condition     = var.task_dispatch_deadline_seconds <= 1800
      error_message = "Cloud Tasks HTTP dispatch deadline cannot exceed 1800 seconds."
    }
  }

  scaling {
    min_instance_count = 0
    max_instance_count = var.executor_max_instances
  }

  template {
    labels = {
      nvoken_schema_version = tostring(var.schema_version)
    }

    service_account                  = google_service_account.executor.email
    timeout                          = "${var.task_dispatch_deadline_seconds}s"
    max_instance_request_concurrency = var.executor_request_concurrency

    containers {
      name  = "nvokend"
      image = local.image
      args  = ["serve"]

      ports {
        name           = "http1"
        container_port = 8080
      }

      env {
        name  = "NVOKEN_PROCESS_ROLE"
        value = "executor"
      }

      env {
        name  = "INVOCATION_EXECUTION_MODE"
        value = var.invocation_execution_mode
      }

      dynamic "env" {
        for_each = var.invocation_execution_mode == "cloud_tasks" ? local.provider_secrets : {}

        content {
          name = env.key
          value_source {
            secret_key_ref {
              secret  = env.value
              version = "latest"
            }
          }
        }
      }

      dynamic "env" {
        for_each = local.credential_encryption_secrets

        content {
          name = env.key
          value_source {
            secret_key_ref {
              secret  = env.value
              version = "latest"
            }
          }
        }
      }

      dynamic "env" {
        for_each = var.provider_credential_active_key_id == null ? [] : [var.provider_credential_active_key_id]

        content {
          name  = "PROVIDER_CREDENTIAL_ACTIVE_KEY_ID"
          value = env.value
        }
      }

      env {
        name = "DATABASE_URL"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.database_url.secret_id
            version = "latest"
          }
        }
      }

      env {
        name  = "DATABASE_MAX_CONNS"
        value = tostring(var.executor_database_max_connections)
      }

      env {
        name  = "REDIS_URL"
        value = "rediss://${google_redis_instance.live_events.host}:${google_redis_instance.live_events.port}/0"
      }

      env {
        name = "REDIS_PASSWORD"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.redis_auth.secret_id
            version = "latest"
          }
        }
      }

      env {
        name  = "REDIS_CA_CERT"
        value = join("\n", [for certificate in google_redis_instance.live_events.server_ca_certs : certificate.cert])
      }

      env {
        name  = "LIVE_EVENT_BUFFER"
        value = tostring(var.live_event_buffer)
      }

      env {
        name  = "DISPATCH_QUEUE"
        value = "execution"
      }

      env {
        name  = "EXECUTOR_ATTEMPT_TIMEOUT"
        value = "${var.executor_attempt_timeout_seconds}s"
      }

      env {
        name  = "ENGINE_EXECUTION_SEGMENT_CEILING"
        value = "${var.engine_execution_segment_ceiling_seconds}s"
      }

      env {
        name  = "ENGINE_SETTLEMENT_RESERVE"
        value = "${var.engine_settlement_reserve_seconds}s"
      }

      env {
        name  = "DISPATCH_SYNTHETIC_ATTEMPT_DELAY"
        value = "${var.synthetic_dispatch_delay_seconds}s"
      }

      env {
        name  = "CLOUD_TASKS_DISPATCH_DEADLINE"
        value = "${var.task_dispatch_deadline_seconds}s"
      }

      env {
        name  = "SHUTDOWN_TIMEOUT"
        value = "${var.shutdown_timeout_seconds}s"
      }

      resources {
        limits = {
          cpu    = var.cpu
          memory = var.memory
        }

        cpu_idle          = true
        startup_cpu_boost = true
      }

      startup_probe {
        timeout_seconds   = 2
        period_seconds    = 3
        failure_threshold = 20

        http_get {
          path = "/health"
          port = 8080
        }
      }

      liveness_probe {
        timeout_seconds   = 2
        period_seconds    = 30
        failure_threshold = 3

        http_get {
          path = "/health"
          port = 8080
        }
      }
    }

    vpc_access {
      egress = "PRIVATE_RANGES_ONLY"

      network_interfaces {
        network    = google_compute_network.runtime.id
        subnetwork = google_compute_subnetwork.runtime.id
      }
    }
  }

  traffic {
    type    = "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST"
    percent = 100
  }

  depends_on = [
    google_cloud_run_v2_job.migrate,
    google_project_service.required,
    google_secret_manager_secret_iam_member.executor_database,
    google_secret_manager_secret_iam_member.executor_redis_auth,
    google_secret_manager_secret_iam_member.provider_executor,
    google_secret_manager_secret_iam_member.credential_encryption_executor,
    google_secret_manager_secret_version.database_url,
    google_secret_manager_secret_version.redis_auth,
  ]
}

resource "google_cloud_run_v2_service_iam_member" "task_caller_invokes_executor" {
  project  = var.project_id
  location = google_cloud_run_v2_service.executor.location
  name     = google_cloud_run_v2_service.executor.name
  role     = "roles/run.invoker"
  member   = "serviceAccount:${google_service_account.task_caller.email}"
}

resource "google_cloud_run_v2_service" "runtime" {
  project              = var.project_id
  name                 = local.resource_name
  location             = var.region
  ingress              = "INGRESS_TRAFFIC_ALL"
  invoker_iam_disabled = true
  deletion_protection  = var.service_deletion_protection
  labels               = local.labels

  lifecycle {
    precondition {
      condition     = var.anthropic_api_key_secret_id != null || var.openai_api_key_secret_id != null
      error_message = "Set anthropic_api_key_secret_id, openai_api_key_secret_id, or both."
    }
    precondition {
      condition     = (var.provider_credential_encryption_keys_secret_id == null) == (var.provider_credential_active_key_id == null)
      error_message = "Set provider_credential_encryption_keys_secret_id and provider_credential_active_key_id together."
    }
    precondition {
      condition     = var.max_instances >= var.min_instances
      error_message = "max_instances must be greater than or equal to min_instances."
    }
    precondition {
      condition     = var.engine_drain_grace_seconds <= var.shutdown_timeout_seconds - 1
      error_message = "engine_drain_grace_seconds must leave at least one second inside shutdown_timeout_seconds."
    }
    precondition {
      condition     = var.engine_settlement_reserve_seconds < var.engine_execution_segment_ceiling_seconds
      error_message = "engine_settlement_reserve_seconds must be less than engine_execution_segment_ceiling_seconds."
    }
    precondition {
      condition = (
        var.stream_max_lifetime_seconds + var.stream_write_timeout_seconds < var.runtime_request_timeout_seconds &&
        var.stream_keepalive_interval_seconds < var.stream_max_lifetime_seconds &&
        var.stream_poll_interval_seconds < var.stream_max_lifetime_seconds
      )
      error_message = "SSE rotation plus its write bound must fit inside the Cloud Run request timeout, and polling/keepalive intervals must be shorter than the rotation lifetime."
    }
    precondition {
      condition = (
        var.invocation_default_total_timeout_seconds >= 1 &&
        var.invocation_default_total_timeout_seconds == floor(var.invocation_default_total_timeout_seconds) &&
        var.invocation_default_total_timeout_seconds <= var.invocation_max_total_timeout_seconds &&
        var.invocation_max_total_timeout_seconds <= 604800 &&
        var.invocation_max_total_timeout_seconds == floor(var.invocation_max_total_timeout_seconds) &&
        var.invocation_default_active_timeout_seconds >= 1 &&
        var.invocation_default_active_timeout_seconds == floor(var.invocation_default_active_timeout_seconds) &&
        var.invocation_default_active_timeout_seconds <= var.invocation_max_active_timeout_seconds &&
        var.invocation_max_active_timeout_seconds <= 604800
        && var.invocation_max_active_timeout_seconds == floor(var.invocation_max_active_timeout_seconds)
      )
      error_message = "Invocation time defaults must be positive, no greater than their maxima, and maxima cannot exceed seven days."
    }
    precondition {
      condition = (
        var.invocation_default_max_iterations >= 1 &&
        var.invocation_default_max_iterations == floor(var.invocation_default_max_iterations) &&
        var.invocation_default_max_iterations <= var.invocation_max_iterations &&
        var.invocation_max_iterations <= 10000 &&
        var.invocation_max_iterations == floor(var.invocation_max_iterations) &&
        var.invocation_max_output_tokens >= 1 && var.invocation_max_output_tokens <= 10000000 && var.invocation_max_output_tokens == floor(var.invocation_max_output_tokens) &&
        var.invocation_max_estimated_cost_microusd >= 1 && var.invocation_max_estimated_cost_microusd <= 1000000000000 && var.invocation_max_estimated_cost_microusd == floor(var.invocation_max_estimated_cost_microusd)
      )
      error_message = "Invocation count and cost defaults/maxima exceed nvoken's fixed safety limits."
    }
  }

  scaling {
    min_instance_count = var.min_instances
    max_instance_count = var.max_instances
  }

  template {
    labels = {
      nvoken_schema_version = tostring(var.schema_version)
    }

    service_account                  = google_service_account.runtime.email
    timeout                          = "${var.runtime_request_timeout_seconds}s"
    max_instance_request_concurrency = var.request_concurrency

    containers {
      name  = "nvokend"
      image = local.image
      args  = ["serve"]

      ports {
        name           = "http1"
        container_port = 8080
      }

      env {
        name  = "NVOKEN_PROCESS_ROLE"
        value = "combined"
      }

      env {
        name  = "INVOCATION_EXECUTION_MODE"
        value = var.invocation_execution_mode
      }

      env {
        name  = "NVOKEN_PUBLIC_BASE_URL"
        value = local.public_base_url
      }

      env {
        name  = "NVOKEN_TRUST_FORWARDED_CLIENT_IP"
        value = "true"
      }

      env {
        name = "DATABASE_URL"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.database_url.secret_id
            version = "latest"
          }
        }
      }

      dynamic "env" {
        for_each = var.retain_legacy_runtime_key ? [1] : []

        content {
          name = "RUNTIME_API_KEY"
          value_source {
            secret_key_ref {
              secret  = google_secret_manager_secret.runtime_api_key.secret_id
              version = "latest"
            }
          }
        }
      }

      env {
        name = "BOOTSTRAP_OWNER_SECRET"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.bootstrap_owner_secret.secret_id
            version = "latest"
          }
        }
      }

      env {
        name = "CREDENTIAL_DELIVERY_KEY"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.credential_delivery_key.secret_id
            version = "latest"
          }
        }
      }

      dynamic "env" {
        for_each = var.invocation_execution_mode == "embedded" ? local.provider_secrets : {}

        content {
          name = env.key
          value_source {
            secret_key_ref {
              secret  = env.value
              version = "latest"
            }
          }
        }
      }

      dynamic "env" {
        for_each = local.credential_encryption_secrets

        content {
          name = env.key
          value_source {
            secret_key_ref {
              secret  = env.value
              version = "latest"
            }
          }
        }
      }

      dynamic "env" {
        for_each = var.provider_credential_active_key_id == null ? [] : [var.provider_credential_active_key_id]

        content {
          name  = "PROVIDER_CREDENTIAL_ACTIVE_KEY_ID"
          value = env.value
        }
      }

      dynamic "env" {
        for_each = var.callback_signing_key_secret_id == null ? [] : [var.callback_signing_key_secret_id]

        content {
          name = "CALLBACK_SIGNING_KEY"
          value_source {
            secret_key_ref {
              secret  = env.value
              version = "latest"
            }
          }
        }
      }

      env {
        name  = "CALLBACK_SIGNING_KEY_ID"
        value = var.callback_signing_key_id
      }

      env {
        name  = "CALLBACK_SIGNING_KEY_VERSION"
        value = tostring(var.callback_signing_key_version)
      }

      env {
        name  = "CALLBACK_DRAIN_GRACE"
        value = "${var.engine_drain_grace_seconds}s"
      }

      dynamic "env" {
        for_each = var.runtime_tenant_key == null ? [] : [var.runtime_tenant_key]

        content {
          name  = "RUNTIME_TENANT_KEY"
          value = env.value
        }
      }

      env {
        name  = "DATABASE_MAX_CONNS"
        value = tostring(var.database_max_connections)
      }

      env {
        name  = "REDIS_URL"
        value = "rediss://${google_redis_instance.live_events.host}:${google_redis_instance.live_events.port}/0"
      }

      env {
        name = "REDIS_PASSWORD"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.redis_auth.secret_id
            version = "latest"
          }
        }
      }

      env {
        name  = "REDIS_CA_CERT"
        value = join("\n", [for certificate in google_redis_instance.live_events.server_ca_certs : certificate.cert])
      }

      env {
        name  = "LIVE_EVENT_BUFFER"
        value = tostring(var.live_event_buffer)
      }

      env {
        name  = "STREAM_POLL_INTERVAL"
        value = "${var.stream_poll_interval_seconds}s"
      }

      env {
        name  = "STREAM_KEEPALIVE_INTERVAL"
        value = "${var.stream_keepalive_interval_seconds}s"
      }

      env {
        name  = "STREAM_MAX_LIFETIME"
        value = "${var.stream_max_lifetime_seconds}s"
      }

      env {
        name  = "STREAM_WRITE_TIMEOUT"
        value = "${var.stream_write_timeout_seconds}s"
      }

      env {
        name  = "DISPATCH_QUEUE"
        value = "execution"
      }

      env {
        name  = "CLOUD_TASKS_QUEUE"
        value = google_cloud_tasks_queue.execution.id
      }

      env {
        name  = "CLOUD_TASKS_EXECUTOR_URL"
        value = google_cloud_run_v2_service.executor.uri
      }

      env {
        name  = "CLOUD_TASKS_OIDC_SERVICE_ACCOUNT"
        value = google_service_account.task_caller.email
      }

      env {
        name  = "CLOUD_TASKS_OIDC_AUDIENCE"
        value = google_cloud_run_v2_service.executor.uri
      }

      env {
        name  = "CLOUD_TASKS_DISPATCH_DEADLINE"
        value = "${var.task_dispatch_deadline_seconds}s"
      }

      env {
        name  = "ENGINE_CONCURRENCY"
        value = tostring(var.engine_concurrency)
      }

      env {
        name  = "ENGINE_DRAIN_GRACE"
        value = "${var.engine_drain_grace_seconds}s"
      }

      env {
        name  = "ENGINE_EXECUTION_SEGMENT_CEILING"
        value = "${var.engine_execution_segment_ceiling_seconds}s"
      }

      env {
        name  = "ENGINE_SETTLEMENT_RESERVE"
        value = "${var.engine_settlement_reserve_seconds}s"
      }

      env {
        name  = "INVOCATION_DEFAULT_TOTAL_TIMEOUT"
        value = "${var.invocation_default_total_timeout_seconds}s"
      }

      env {
        name  = "INVOCATION_DEFAULT_ACTIVE_TIMEOUT"
        value = "${var.invocation_default_active_timeout_seconds}s"
      }

      env {
        name  = "INVOCATION_DEFAULT_WAITING_TIMEOUT"
        value = "${var.invocation_default_waiting_timeout_seconds}s"
      }

      env {
        name  = "INVOCATION_DEFAULT_MAX_ITERATIONS"
        value = tostring(var.invocation_default_max_iterations)
      }

      env {
        name  = "INVOCATION_MAX_TOTAL_TIMEOUT"
        value = "${var.invocation_max_total_timeout_seconds}s"
      }

      env {
        name  = "INVOCATION_MAX_ACTIVE_TIMEOUT"
        value = "${var.invocation_max_active_timeout_seconds}s"
      }

      env {
        name  = "INVOCATION_MAX_WAITING_TIMEOUT"
        value = "${var.invocation_max_waiting_timeout_seconds}s"
      }

      env {
        name  = "INVOCATION_MAX_OUTPUT_TOKENS"
        value = tostring(var.invocation_max_output_tokens)
      }

      env {
        name  = "INVOCATION_MAX_ESTIMATED_COST_MICROUSD"
        value = tostring(var.invocation_max_estimated_cost_microusd)
      }

      env {
        name  = "INVOCATION_MAX_ITERATIONS"
        value = tostring(var.invocation_max_iterations)
      }

      env {
        name  = "SHUTDOWN_TIMEOUT"
        value = "${var.shutdown_timeout_seconds}s"
      }

      resources {
        limits = {
          cpu    = var.cpu
          memory = var.memory
        }

        cpu_idle          = false
        startup_cpu_boost = true
      }

      startup_probe {
        initial_delay_seconds = 0
        timeout_seconds       = 2
        period_seconds        = 3
        failure_threshold     = 20

        http_get {
          path = "/health"
          port = 8080
        }
      }

      liveness_probe {
        timeout_seconds   = 2
        period_seconds    = 30
        failure_threshold = 3

        http_get {
          path = "/health"
          port = 8080
        }
      }
    }

    vpc_access {
      egress = "PRIVATE_RANGES_ONLY"

      network_interfaces {
        network    = google_compute_network.runtime.id
        subnetwork = google_compute_subnetwork.runtime.id
      }
    }
  }

  traffic {
    type    = "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST"
    percent = 100
  }

  depends_on = [
    google_cloud_run_v2_job.migrate,
    google_cloud_run_v2_service_iam_member.task_caller_invokes_executor,
    google_cloud_tasks_queue_iam_member.runtime_cloud_tasks_enqueuer,
    google_cloud_tasks_queue_iam_member.runtime_cloud_tasks_viewer,
    google_service_account_iam_member.runtime_acts_as_task_caller,
    google_project_service.required,
    google_secret_manager_secret_iam_member.generated,
    google_secret_manager_secret_iam_member.callback_runtime,
    google_secret_manager_secret_iam_member.credential_encryption_runtime,
    google_secret_manager_secret_iam_member.provider_runtime,
    google_secret_manager_secret_version.database_url,
    google_secret_manager_secret_version.bootstrap_owner_secret,
    google_secret_manager_secret_version.credential_delivery_key,
    google_secret_manager_secret_version.redis_auth,
    google_secret_manager_secret_version.runtime_api_key,
  ]
}

resource "google_cloud_run_v2_job" "dispatch_smoke" {
  project             = var.project_id
  name                = "${local.resource_name}-dispatch-smoke"
  location            = var.region
  deletion_protection = false
  labels              = local.labels

  template {
    task_count  = 1
    parallelism = 1

    template {
      service_account = google_service_account.migrate.email
      timeout         = "300s"
      max_retries     = 0

      containers {
        name  = "nvokend"
        image = local.image
        args  = ["dispatch-smoke"]

        env {
          name = "DATABASE_URL"
          value_source {
            secret_key_ref {
              secret  = google_secret_manager_secret.database_url.secret_id
              version = "latest"
            }
          }
        }

        env {
          name  = "DISPATCH_QUEUE"
          value = "execution"
        }

        resources {
          limits = {
            cpu    = var.cpu
            memory = var.memory
          }
        }
      }

      vpc_access {
        egress = "PRIVATE_RANGES_ONLY"

        network_interfaces {
          network    = google_compute_network.runtime.id
          subnetwork = google_compute_subnetwork.runtime.id
        }
      }
    }
  }

  depends_on = [google_cloud_run_v2_service.runtime]
}
