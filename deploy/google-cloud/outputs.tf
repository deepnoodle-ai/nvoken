output "artifact_repository" {
  description = "Artifact Registry Docker repository URL."
  value       = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.images.repository_id}"
}

output "project_id" {
  description = "Google Cloud project that owns the deployment."
  value       = var.project_id
}

output "region" {
  description = "Google Cloud deployment region."
  value       = var.region
}

output "image" {
  description = "Immutable image reference configured for the migration job and service."
  value       = local.image
}

output "service_name" {
  description = "Cloud Run service name."
  value       = google_cloud_run_v2_service.runtime.name
}

output "service_url" {
  description = "Public Cloud Run service URL."
  value       = google_cloud_run_v2_service.runtime.uri
}

output "executor_service_name" {
  description = "Private request-bound executor Cloud Run service name."
  value       = google_cloud_run_v2_service.executor.name
}

output "executor_service_url" {
  description = "Stable private executor service URL used for task OIDC audience and target."
  value       = google_cloud_run_v2_service.executor.uri
}

output "execution_queue" {
  description = "Fully qualified regional Cloud Tasks execution queue."
  value       = google_cloud_tasks_queue.execution.id
}

output "dispatch_smoke_job_name" {
  description = "Cloud Run Job that creates one harmless synthetic dispatch."
  value       = google_cloud_run_v2_job.dispatch_smoke.name
}

output "migration_job_name" {
  description = "Cloud Run migration job name."
  value       = google_cloud_run_v2_job.migrate.name
}

output "runtime_api_key_secret_id" {
  description = "Secret Manager secret containing the generated Runtime bearer key."
  value       = google_secret_manager_secret.runtime_api_key.secret_id
}

output "service_account_email" {
  description = "Dedicated Cloud Run service identity."
  value       = google_service_account.runtime.email
}

output "build_service_account_name" {
  description = "Fully qualified least-privilege service account used by Cloud Build."
  value       = google_service_account.build.name
}

output "build_source_bucket" {
  description = "Restricted Cloud Storage bucket used to stage Cloud Build source archives."
  value       = google_storage_bucket.build_source.name
}

output "cloud_sql_instance" {
  description = "Private Cloud SQL instance connection name."
  value       = google_sql_database_instance.runtime.connection_name
}

output "maximum_engine_concurrency" {
  description = "Configured upper bound across all Cloud Run service instances."
  value       = var.max_instances * var.engine_concurrency
}

output "maximum_database_connections" {
  description = "Configured upper bound across all Cloud Run service instances."
  value       = var.max_instances * var.database_max_connections
}

output "maximum_executor_concurrency" {
  description = "Declared upper bound on concurrent private executor requests."
  value       = var.executor_max_instances * var.executor_request_concurrency
}

output "maximum_executor_database_connections" {
  description = "Declared upper bound on private executor Postgres connections."
  value       = var.executor_max_instances * var.executor_database_max_connections
}
