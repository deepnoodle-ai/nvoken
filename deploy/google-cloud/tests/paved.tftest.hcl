mock_provider "google" {
  mock_resource "google_sql_database_instance" {
    defaults = {
      private_ip_address = "10.42.0.3"
      connection_name    = "example-project:us-central1:nvoken-test-postgres"
    }
  }

  mock_resource "google_cloud_run_v2_service" {
    defaults = {
      uri = "https://nvoken-test.example.run.app"
    }
  }
}

mock_provider "random" {
  mock_resource "random_password" {
    defaults = {
      result = "test-only-random-value-that-is-never-a-real-secret"
    }
  }
}

run "paved_defaults" {
  command = plan

  variables {
    project_id                   = "example-project"
    environment                  = "test"
    image_tag                    = "0123456789abcdef"
    anthropic_api_key_secret_id  = "nvoken-test-anthropic"
    database_deletion_protection = false
    service_deletion_protection  = false
  }

  assert {
    condition     = google_sql_database_instance.runtime.settings[0].ip_configuration[0].ipv4_enabled == false
    error_message = "Cloud SQL must not have a public IPv4 address."
  }

  assert {
    condition     = google_sql_database_instance.runtime.settings[0].ip_configuration[0].ssl_mode == "ENCRYPTED_ONLY"
    error_message = "Cloud SQL must reject unencrypted database connections."
  }

  assert {
    condition = (
      google_storage_bucket.build_source.uniform_bucket_level_access == true &&
      google_storage_bucket.build_source.public_access_prevention == "enforced" &&
      google_storage_bucket_iam_member.build_source_reader.bucket == google_storage_bucket.build_source.name
    )
    error_message = "Cloud Build source access must be scoped to a private, uniform-access bucket."
  }

  assert {
    condition = (
      length(google_service_account.runtime.account_id) <= 30 &&
      length(google_service_account.build.account_id) <= 30 &&
      length(google_service_account.migrate.account_id) <= 30
    )
    error_message = "Runtime, migration, and build identities must be valid and purpose-specific."
  }

  assert {
    condition     = google_cloud_run_v2_service.runtime.scaling[0].min_instance_count == 1
    error_message = "Combined mode must retain at least one poller."
  }

  assert {
    condition     = google_cloud_run_v2_service.runtime.invoker_iam_disabled == true
    error_message = "The public service must disable Cloud Run's edge IAM check and defer authentication to the Runtime bearer credential."
  }

  assert {
    condition     = google_cloud_run_v2_service.runtime.scaling[0].max_instance_count == 3
    error_message = "Default instance capacity must be bounded."
  }

  assert {
    condition     = google_cloud_run_v2_service.runtime.template[0].max_instance_request_concurrency == 16
    error_message = "HTTP request concurrency must be explicit."
  }

  assert {
    condition     = google_cloud_run_v2_service.runtime.template[0].containers[0].resources[0].cpu_idle == false
    error_message = "Combined mode must use instance-based CPU."
  }

  assert {
    condition     = google_cloud_run_v2_service.runtime.template[0].containers[0].startup_probe[0].http_get[0].path == "/health"
    error_message = "The startup probe must avoid Cloud Run's reserved paths ending in z."
  }

  assert {
    condition     = google_cloud_run_v2_service.runtime.template[0].containers[0].liveness_probe[0].http_get[0].path == "/health"
    error_message = "The liveness probe must avoid Cloud Run's reserved paths ending in z."
  }

  assert {
    condition     = one([for item in google_cloud_run_v2_service.runtime.template[0].containers[0].env : item.value if item.name == "ENGINE_CONCURRENCY"]) == "4"
    error_message = "Engine concurrency must be explicit."
  }

  assert {
    condition     = one([for item in google_cloud_run_v2_service.runtime.template[0].containers[0].env : item.value if item.name == "DATABASE_MAX_CONNS"]) == "10"
    error_message = "Database pool capacity must be explicit."
  }

  assert {
    condition     = one([for item in google_cloud_run_v2_service.runtime.template[0].containers[0].env : item.value if item.name == "SHUTDOWN_TIMEOUT"]) == "8s"
    error_message = "The total Cloud Run shutdown budget must be explicit."
  }

  assert {
    condition     = google_cloud_run_v2_job.migrate.template[0].parallelism == 1 && google_cloud_run_v2_job.migrate.template[0].task_count == 1
    error_message = "The migration operation must run as one task."
  }

  assert {
    condition     = google_cloud_run_v2_job.migrate.template[0].template[0].containers[0].args == tolist(["migrate"])
    error_message = "The migration job must use the same image's migrate command."
  }

  assert {
    condition     = google_cloud_run_v2_service.runtime.template[0].containers[0].args == tolist(["serve"])
    error_message = "The service must use the same image's serve command."
  }
}

run "long_names_produce_valid_service_accounts" {
  command = plan

  variables {
    project_id                   = "example-project"
    name                         = "nvoken-application-x"
    environment                  = "production"
    image_tag                    = "8899aabbccddeeff"
    openai_api_key_secret_id     = "nvoken-production-openai"
    database_deletion_protection = false
  }

  assert {
    condition = alltrue([
      length(google_service_account.runtime.account_id) <= 30,
      length(google_service_account.build.account_id) <= 30,
      length(google_service_account.migrate.account_id) <= 30,
    ])
    error_message = "Every derived service-account ID must fit Google's 30-character limit."
  }
}

run "both_providers_are_allowed" {
  command = plan

  variables {
    project_id                   = "example-project"
    environment                  = "test"
    image_tag                    = "fedcba9876543210"
    anthropic_api_key_secret_id  = "nvoken-test-anthropic"
    openai_api_key_secret_id     = "nvoken-test-openai"
    database_deletion_protection = false
  }

  assert {
    condition = alltrue([
      for name in ["ANTHROPIC_API_KEY", "OPENAI_API_KEY"] :
      contains([for item in google_cloud_run_v2_service.runtime.template[0].containers[0].env : item.name], name)
    ])
    error_message = "Both provider secrets must be injectable together."
  }
}

run "missing_provider_is_rejected" {
  command = plan

  variables {
    project_id                   = "example-project"
    environment                  = "test"
    image_tag                    = "0011223344556677"
    database_deletion_protection = false
  }

  expect_failures = [google_cloud_run_v2_service.runtime]
}
