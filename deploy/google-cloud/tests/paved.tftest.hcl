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
      length(google_service_account.migrate.account_id) <= 30 &&
      length(google_service_account.executor.account_id) <= 30 &&
      length(google_service_account.task_caller.account_id) <= 30
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

  assert {
    condition = (
      google_cloud_run_v2_service.executor.ingress == "INGRESS_TRAFFIC_INTERNAL_ONLY" &&
      google_cloud_run_v2_service.executor.invoker_iam_disabled == false &&
      google_cloud_run_v2_service.executor.scaling[0].min_instance_count == 0 &&
      google_cloud_run_v2_service.executor.template[0].containers[0].resources[0].cpu_idle == true
    )
    error_message = "The executor must be private, IAM-protected, request-bound, and scale to zero."
  }

  assert {
    condition = (
      google_cloud_run_v2_service_iam_member.task_caller_invokes_executor.role == "roles/run.invoker" &&
      google_service_account_iam_member.runtime_acts_as_task_caller.role == "roles/iam.serviceAccountUser"
    )
    error_message = "Only the dedicated OIDC caller may invoke the executor, and the publisher may act as that identity."
  }

  assert {
    condition = (
      google_cloud_tasks_queue_iam_member.runtime_cloud_tasks_enqueuer.name == google_cloud_tasks_queue.execution.name &&
      google_cloud_tasks_queue_iam_member.runtime_cloud_tasks_enqueuer.role == "roles/cloudtasks.enqueuer" &&
      google_cloud_tasks_queue_iam_member.runtime_cloud_tasks_viewer.name == google_cloud_tasks_queue.execution.name &&
      google_cloud_tasks_queue_iam_member.runtime_cloud_tasks_viewer.role == "roles/cloudtasks.viewer"
    )
    error_message = "Publisher Cloud Tasks permissions must be scoped to the execution queue."
  }

  assert {
    condition = (
      google_cloud_tasks_queue.execution.rate_limits[0].max_concurrent_dispatches <= var.executor_max_instances * var.executor_request_concurrency &&
      google_cloud_run_v2_service.executor.template[0].timeout == "1800s" &&
      one([for item in google_cloud_run_v2_service.executor.template[0].containers[0].env : item.value if item.name == "EXECUTOR_ATTEMPT_TIMEOUT"]) == "1795s" &&
      one([for item in google_cloud_run_v2_service.executor.template[0].containers[0].env : item.value if item.name == "ENGINE_EXECUTION_SEGMENT_CEILING"]) == "900s" &&
      one([for item in google_cloud_run_v2_service.executor.template[0].containers[0].env : item.value if item.name == "ENGINE_SETTLEMENT_RESERVE"]) == "5s" &&
      var.engine_execution_segment_ceiling_seconds <= var.executor_attempt_timeout_seconds
    )
    error_message = "Queue concurrency and attempt timing must fit inside declared executor capacity and deadline."
  }

  assert {
    condition = (
      one([for item in google_cloud_run_v2_service.runtime.template[0].containers[0].env : item.value if item.name == "NVOKEN_PROCESS_ROLE"]) == "combined" &&
      one([for item in google_cloud_run_v2_service.executor.template[0].containers[0].env : item.value if item.name == "NVOKEN_PROCESS_ROLE"]) == "executor" &&
      one([for item in google_cloud_run_v2_service.runtime.template[0].containers[0].env : item.value if item.name == "INVOCATION_EXECUTION_MODE"]) == "cloud_tasks" &&
      one([for item in google_cloud_run_v2_service.executor.template[0].containers[0].env : item.value if item.name == "INVOCATION_EXECUTION_MODE"]) == "cloud_tasks" &&
      length([for item in google_cloud_run_v2_service.runtime.template[0].containers[0].env : item if item.name == "CLOUD_TASKS_EXECUTOR_URL"]) == 1 &&
      length([for item in google_cloud_run_v2_service.runtime.template[0].containers[0].env : item if item.name == "CLOUD_TASKS_OIDC_AUDIENCE"]) == 1
    )
    error_message = "Process roles and the stable executor URL/audience must be explicit."
  }

  assert {
    condition = (
      !contains([for item in google_cloud_run_v2_service.executor.template[0].containers[0].env : item.name], "RUNTIME_API_KEY") &&
      contains([for item in google_cloud_run_v2_service.executor.template[0].containers[0].env : item.name], "ANTHROPIC_API_KEY") &&
      !contains([for item in google_cloud_run_v2_service.runtime.template[0].containers[0].env : item.name], "ANTHROPIC_API_KEY") &&
      length(google_secret_manager_secret_iam_member.provider_executor) == 1 &&
      length(google_secret_manager_secret_iam_member.provider_runtime) == 0
    )
    error_message = "Cloud Tasks mode must give provider credentials only to the private generating role."
  }

  assert {
    condition     = google_cloud_run_v2_job.dispatch_smoke.template[0].template[0].containers[0].args == tolist(["dispatch-smoke"])
    error_message = "The staging proof must use the same image's synthetic dispatch command."
  }

  assert {
    condition     = length(google_monitoring_alert_policy.dispatch_failures) == 5
    error_message = "Aged dispatch, publication failure, sustained executor retry, and authentication signals must be alertable."
  }

  assert {
    condition = (
      google_monitoring_alert_policy.dispatch_failures["aged_pending"].conditions[0].condition_threshold[0].duration == "300s" &&
      google_monitoring_alert_policy.dispatch_failures["stale_published"].conditions[0].condition_threshold[0].duration == "300s" &&
      google_monitoring_alert_policy.dispatch_failures["executor_retry"].conditions[0].condition_threshold[0].duration == "300s" &&
      google_monitoring_alert_policy.dispatch_failures["publish_failure"].conditions[0].condition_threshold[0].duration == "0s"
    )
    error_message = "Aged dispatch alerts must require sustained evidence without delaying discrete failure alerts."
  }
}

run "embedded_mode_moves_provider_secrets" {
  command = plan

  variables {
    project_id                   = "example-project"
    environment                  = "test"
    image_tag                    = "0123456789abcdef"
    anthropic_api_key_secret_id  = "nvoken-test-anthropic"
    invocation_execution_mode    = "embedded"
    database_deletion_protection = false
    service_deletion_protection  = false
  }

  assert {
    condition = (
      contains([for item in google_cloud_run_v2_service.runtime.template[0].containers[0].env : item.name], "ANTHROPIC_API_KEY") &&
      !contains([for item in google_cloud_run_v2_service.executor.template[0].containers[0].env : item.name], "ANTHROPIC_API_KEY") &&
      length(google_secret_manager_secret_iam_member.provider_runtime) == 1 &&
      length(google_secret_manager_secret_iam_member.provider_executor) == 0
    )
    error_message = "Embedded mode must give provider credentials only to the combined generating role."
  }
}

run "notification_channels_are_wired" {
  command = plan

  variables {
    project_id                       = "example-project"
    environment                      = "test"
    image_tag                        = "0123456789abcdef"
    anthropic_api_key_secret_id      = "nvoken-test-anthropic"
    database_deletion_protection     = false
    service_deletion_protection      = false
    monitoring_notification_channels = ["projects/example-project/notificationChannels/123456789"]
  }

  assert {
    condition = alltrue([
      for policy in google_monitoring_alert_policy.dispatch_failures :
      policy.notification_channels == tolist(["projects/example-project/notificationChannels/123456789"])
    ])
    error_message = "Every dispatch alert policy must attach the configured notification channels."
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
      length(google_service_account.executor.account_id) <= 30,
      length(google_service_account.task_caller.account_id) <= 30,
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
      contains([for item in google_cloud_run_v2_service.executor.template[0].containers[0].env : item.name], name)
    ]) && length(google_secret_manager_secret_iam_member.provider_executor) == 2
    error_message = "Both provider secrets must be injectable together into the generating role."
  }
}

run "segment_ceiling_outside_attempt_is_rejected" {
  command = plan

  variables {
    project_id                       = "example-project"
    environment                      = "test"
    image_tag                        = "abcdef0123456789"
    anthropic_api_key_secret_id      = "nvoken-test-anthropic"
    executor_attempt_timeout_seconds = 800
    database_deletion_protection     = false
  }

  expect_failures = [google_cloud_run_v2_service.executor]
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
