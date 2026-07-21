locals {
  resource_name        = "${var.name}-${var.environment}"
  runtime_account_id   = length(local.resource_name) <= 30 ? local.resource_name : "${substr(local.resource_name, 0, 21)}-${substr(sha256(local.resource_name), 0, 8)}"
  build_account_name   = "${local.resource_name}-build"
  build_account_id     = length(local.build_account_name) <= 30 ? local.build_account_name : "${substr(local.resource_name, 0, 21)}-${substr(sha256(local.build_account_name), 0, 8)}"
  migrate_account_name = "${local.resource_name}-migrate"
  migrate_account_id   = length(local.migrate_account_name) <= 30 ? local.migrate_account_name : "${substr(local.resource_name, 0, 21)}-${substr(sha256(local.migrate_account_name), 0, 8)}"
  build_source_bucket  = "${var.project_id}-${substr(sha256("${var.project_id}/${local.resource_name}"), 0, 12)}-nvoken-build"
  image                = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.images.repository_id}/nvokend:${var.image_tag}"
  labels = merge(var.labels, {
    application = "nvoken"
    environment = var.environment
    managed_by  = "terraform"
  })
  provider_secrets = merge(
    var.anthropic_api_key_secret_id == null ? {} : { ANTHROPIC_API_KEY = var.anthropic_api_key_secret_id },
    var.openai_api_key_secret_id == null ? {} : { OPENAI_API_KEY = var.openai_api_key_secret_id },
  )
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
    "compute.googleapis.com",
    "iam.googleapis.com",
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

resource "google_secret_manager_secret_iam_member" "generated" {
  for_each = {
    database_url    = google_secret_manager_secret.database_url.secret_id
    runtime_api_key = google_secret_manager_secret.runtime_api_key.secret_id
  }

  project   = var.project_id
  secret_id = each.value
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.runtime.email}"
}

resource "google_secret_manager_secret_iam_member" "provider" {
  for_each = local.provider_secrets

  project   = var.project_id
  secret_id = each.value
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.runtime.email}"
}

resource "google_secret_manager_secret_iam_member" "migration_database" {
  project   = var.project_id
  secret_id = google_secret_manager_secret.database_url.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.migrate.email}"
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
      condition     = var.max_instances >= var.min_instances
      error_message = "max_instances must be greater than or equal to min_instances."
    }
    precondition {
      condition     = var.engine_drain_grace_seconds <= var.shutdown_timeout_seconds - 1
      error_message = "engine_drain_grace_seconds must leave at least one second inside shutdown_timeout_seconds."
    }
  }

  scaling {
    min_instance_count = var.min_instances
    max_instance_count = var.max_instances
  }

  template {
    service_account                  = google_service_account.runtime.email
    timeout                          = "300s"
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
        name = "DATABASE_URL"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.database_url.secret_id
            version = "latest"
          }
        }
      }

      env {
        name = "RUNTIME_API_KEY"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.runtime_api_key.secret_id
            version = "latest"
          }
        }
      }

      dynamic "env" {
        for_each = local.provider_secrets

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
        for_each = var.runtime_tenant_ref == null ? [] : [var.runtime_tenant_ref]

        content {
          name  = "RUNTIME_TENANT_REF"
          value = env.value
        }
      }

      env {
        name  = "DATABASE_MAX_CONNS"
        value = tostring(var.database_max_connections)
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
          path = "/healthz"
          port = 8080
        }
      }

      liveness_probe {
        timeout_seconds   = 2
        period_seconds    = 30
        failure_threshold = 3

        http_get {
          path = "/healthz"
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
    google_secret_manager_secret_iam_member.generated,
    google_secret_manager_secret_iam_member.provider,
    google_secret_manager_secret_version.database_url,
    google_secret_manager_secret_version.runtime_api_key,
  ]
}
