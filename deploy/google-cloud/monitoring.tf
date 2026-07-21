locals {
  monitoring_runbook_url = "https://github.com/deepnoodle-ai/nvoken/blob/main/deploy/google-cloud/runbooks.md"
  monitoring_user_labels = {
    application = "nvoken"
    environment = var.environment
  }
  application_log_filter = join(" AND ", [
    "resource.type=\"cloud_run_revision\"",
    "(resource.labels.service_name=\"${google_cloud_run_v2_service.runtime.name}\" OR resource.labels.service_name=\"${google_cloud_run_v2_service.executor.name}\")",
  ])
  dispatch_alerts = {
    aged_pending = {
      filter    = "jsonPayload.event=\"dispatch_aged_pending\""
      threshold = var.monitoring_alert_thresholds.aged_dispatch_count
      duration  = var.monitoring_alert_windows_seconds.aged_dispatch
      runbook   = "aged-runnable-work"
      severity  = "WARNING"
    }
    stale_published = {
      filter    = "jsonPayload.event=\"dispatch_stale_published\""
      threshold = var.monitoring_alert_thresholds.aged_dispatch_count
      duration  = var.monitoring_alert_windows_seconds.aged_dispatch
      runbook   = "aged-runnable-work"
      severity  = "WARNING"
    }
    publish_failure = {
      filter    = "jsonPayload.event=\"dispatch_publish_failure\""
      threshold = var.monitoring_alert_thresholds.dispatch_publish_failure_count
      duration  = var.monitoring_alert_windows_seconds.dispatch_publish_failure
      runbook   = "dispatch-publication-failure"
      severity  = "ERROR"
    }
    executor_retry = {
      filter    = "resource.labels.service_name=\"${google_cloud_run_v2_service.executor.name}\" AND jsonPayload.event=\"dispatch_attempt_retry\""
      threshold = var.monitoring_alert_thresholds.executor_retry_count
      duration  = var.monitoring_alert_windows_seconds.executor_retry
      runbook   = "executor-delivery-rejections"
      severity  = "ERROR"
    }
    executor_auth = {
      filter    = "resource.labels.service_name=\"${google_cloud_run_v2_service.executor.name}\" AND (httpRequest.status=401 OR httpRequest.status=403)"
      threshold = var.monitoring_alert_thresholds.executor_auth_count
      duration  = var.monitoring_alert_windows_seconds.executor_auth
      runbook   = "executor-delivery-rejections"
      severity  = "CRITICAL"
    }
  }
}

resource "google_monitoring_uptime_check_config" "runtime" {
  project      = var.project_id
  display_name = "${local.resource_name} public health"
  checker_type = "STATIC_IP_CHECKERS"
  period       = "60s"
  timeout      = "10s"

  selected_regions = ["USA", "EUROPE", "ASIA_PACIFIC"]

  monitored_resource {
    type = "uptime_url"
    labels = {
      host       = trimsuffix(trimprefix(google_cloud_run_v2_service.runtime.uri, "https://"), "/")
      project_id = var.project_id
    }
  }

  http_check {
    path           = "/health"
    port           = 443
    request_method = "GET"
    use_ssl        = true
    validate_ssl   = true
  }

  depends_on = [google_project_service.required]
}

resource "google_logging_metric" "invocation_outcomes" {
  project     = var.project_id
  name        = "${local.resource_name}-invocation-outcomes"
  description = "Terminal nvoken Invocation outcomes. The status label is a bounded lifecycle enum."
  filter      = "${local.application_log_filter} AND jsonPayload.event=\"invocation_settled\""

  metric_descriptor {
    display_name = "nvoken Invocation outcomes"
    metric_kind  = "DELTA"
    value_type   = "INT64"
    unit         = "1"

    labels {
      key         = "status"
      value_type  = "STRING"
      description = "Bounded terminal Invocation status."
    }
  }

  label_extractors = {
    status = "EXTRACT(jsonPayload.status)"
  }

  depends_on = [google_project_service.required]
}

resource "google_logging_metric" "provider_outcomes" {
  project     = var.project_id
  name        = "${local.resource_name}-provider-outcomes"
  description = "Provider generation outcomes. Only the bounded success or failed outcome is extracted."
  filter      = "${local.application_log_filter} AND jsonPayload.event=\"provider_generation\""

  metric_descriptor {
    display_name = "nvoken provider outcomes"
    metric_kind  = "DELTA"
    value_type   = "INT64"
    unit         = "1"

    labels {
      key         = "outcome"
      value_type  = "STRING"
      description = "Bounded provider outcome: success or failed."
    }
  }

  label_extractors = {
    outcome = "EXTRACT(jsonPayload.outcome)"
  }

  depends_on = [google_project_service.required]
}

resource "google_logging_metric" "provider_latency" {
  project         = var.project_id
  name            = "${local.resource_name}-provider-latency"
  description     = "Provider generation latency in milliseconds for successful generation outcomes."
  filter          = "${local.application_log_filter} AND jsonPayload.event=\"provider_generation\" AND jsonPayload.outcome=\"success\" AND jsonPayload.generation_latency_ms>=0"
  value_extractor = "EXTRACT(jsonPayload.generation_latency_ms)"

  metric_descriptor {
    display_name = "nvoken provider latency"
    metric_kind  = "DELTA"
    value_type   = "DISTRIBUTION"
    unit         = "ms"
  }

  bucket_options {
    exponential_buckets {
      growth_factor      = 2
      num_finite_buckets = 18
      scale              = 10
    }
  }

  depends_on = [google_project_service.required]
}

resource "google_logging_metric" "callback_events" {
  project     = var.project_id
  name        = "${local.resource_name}-callback-events"
  description = "Callback retry and terminal delivery events with bounded event and status labels."
  filter = join(" AND ", [
    "resource.type=\"cloud_run_revision\"",
    "resource.labels.service_name=\"${google_cloud_run_v2_service.runtime.name}\"",
    "(jsonPayload.event=\"callback_delivery_retry\" OR jsonPayload.event=\"callback_delivery_settled\" OR jsonPayload.event=\"callback_delivery_abandoned\")",
  ])

  metric_descriptor {
    display_name = "nvoken callback events"
    metric_kind  = "DELTA"
    value_type   = "INT64"
    unit         = "1"

    labels {
      key         = "event"
      value_type  = "STRING"
      description = "Bounded callback event name."
    }

    labels {
      key         = "delivery_status"
      value_type  = "STRING"
      description = "Bounded terminal delivery status when present."
    }
  }

  label_extractors = {
    event           = "EXTRACT(jsonPayload.event)"
    delivery_status = "EXTRACT(jsonPayload.delivery_status)"
  }

  depends_on = [google_project_service.required]
}

resource "google_logging_metric" "callback_worker_failures" {
  project     = var.project_id
  name        = "${local.resource_name}-callback-worker-failures"
  description = "Callback worker claim, processing, recovery, or prune failures."
  filter = join(" AND ", [
    "resource.type=\"cloud_run_revision\"",
    "resource.labels.service_name=\"${google_cloud_run_v2_service.runtime.name}\"",
    "(jsonPayload.event=\"callback_claim_failed\" OR jsonPayload.event=\"callback_process_failed\" OR jsonPayload.event=\"callback_recovery_failed\" OR jsonPayload.event=\"callback_prune_failed\")",
  ])

  metric_descriptor {
    display_name = "nvoken callback worker failures"
    metric_kind  = "DELTA"
    value_type   = "INT64"
    unit         = "1"

    labels {
      key         = "event"
      value_type  = "STRING"
      description = "Bounded callback worker failure event."
    }
  }

  label_extractors = {
    event = "EXTRACT(jsonPayload.event)"
  }

  depends_on = [google_project_service.required]
}

resource "google_logging_metric" "executor_attempts" {
  project     = var.project_id
  name        = "${local.resource_name}-executor-attempts"
  description = "Request-bound executor attempt decisions with a bounded handler outcome."
  filter = join(" AND ", [
    "resource.type=\"cloud_run_revision\"",
    "resource.labels.service_name=\"${google_cloud_run_v2_service.executor.name}\"",
    "(jsonPayload.event=\"dispatch_attempt_decided\" OR jsonPayload.event=\"dispatch_attempt_retry\")",
  ])

  metric_descriptor {
    display_name = "nvoken executor attempts"
    metric_kind  = "DELTA"
    value_type   = "INT64"
    unit         = "1"

    labels {
      key         = "handler_outcome"
      value_type  = "STRING"
      description = "Bounded executor handler outcome."
    }
  }

  label_extractors = {
    handler_outcome = "EXTRACT(jsonPayload.handler_outcome)"
  }

  depends_on = [google_project_service.required]
}

resource "google_logging_metric" "dispatch_age" {
  project         = var.project_id
  name            = "${local.resource_name}-dispatch-age"
  description     = "Oldest age in milliseconds reported by each bounded aged-dispatch event."
  filter          = "${local.application_log_filter} AND (jsonPayload.event=\"dispatch_aged_pending\" OR jsonPayload.event=\"dispatch_stale_published\") AND jsonPayload.oldest_age_ms>=0"
  value_extractor = "EXTRACT(jsonPayload.oldest_age_ms)"

  metric_descriptor {
    display_name = "nvoken aged dispatch age"
    metric_kind  = "DELTA"
    value_type   = "DISTRIBUTION"
    unit         = "ms"

    labels {
      key         = "event"
      value_type  = "STRING"
      description = "Bounded aged-dispatch event name."
    }
  }

  label_extractors = {
    event = "EXTRACT(jsonPayload.event)"
  }

  bucket_options {
    exponential_buckets {
      growth_factor      = 2
      num_finite_buckets = 20
      scale              = 1000
    }
  }

  depends_on = [google_project_service.required]
}

resource "google_logging_metric" "dispatch_failures" {
  for_each = local.dispatch_alerts

  project = var.project_id
  name    = "${local.resource_name}-${replace(each.key, "_", "-")}"
  filter  = "resource.type=\"cloud_run_revision\" AND ${each.value.filter}"

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"
    unit        = "1"
  }

  depends_on = [google_project_service.required]
}

resource "google_monitoring_alert_policy" "runtime_health" {
  project      = var.project_id
  display_name = "${local.resource_name}: public Runtime unavailable or 5xx"
  combiner     = "OR"
  severity     = "CRITICAL"
  user_labels  = local.monitoring_user_labels

  conditions {
    display_name = "sustained public 5xx"

    condition_threshold {
      filter          = "metric.type=\"run.googleapis.com/request_count\" AND resource.type=\"cloud_run_revision\" AND resource.label.service_name=\"${google_cloud_run_v2_service.runtime.name}\" AND metric.label.response_code_class=\"5xx\""
      comparison      = "COMPARISON_GT"
      threshold_value = var.monitoring_alert_thresholds.public_5xx_count
      duration        = "${var.monitoring_alert_windows_seconds.public_5xx}s"

      aggregations {
        alignment_period     = "60s"
        per_series_aligner   = "ALIGN_SUM"
        cross_series_reducer = "REDUCE_SUM"
        group_by_fields      = ["resource.label.service_name"]
      }
    }
  }

  conditions {
    display_name = "public health check failing"

    condition_threshold {
      filter          = "metric.type=\"monitoring.googleapis.com/uptime_check/check_passed\" AND resource.type=\"uptime_url\" AND metric.label.check_id=\"${google_monitoring_uptime_check_config.runtime.uptime_check_id}\""
      comparison      = "COMPARISON_LT"
      threshold_value = 1
      duration        = "${var.monitoring_alert_windows_seconds.uptime}s"

      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_FRACTION_TRUE"
      }

      trigger {
        percent = 0.5
      }
    }
  }

  alert_strategy {
    auto_close = "1800s"
  }

  documentation {
    content   = "Runbook: [Runtime unavailable or 5xx](${local.monitoring_runbook_url}#runtime-unavailable-or-5xx)."
    mime_type = "text/markdown"
  }

  notification_channels = var.monitoring_notification_channels
}

resource "google_monitoring_alert_policy" "dispatch_failures" {
  for_each = google_logging_metric.dispatch_failures

  project      = var.project_id
  display_name = "${local.resource_name}: ${replace(each.key, "_", " ")}"
  combiner     = "OR"
  severity     = local.dispatch_alerts[each.key].severity
  user_labels  = local.monitoring_user_labels

  conditions {
    display_name = "${replace(each.key, "_", " ")} observed"

    condition_threshold {
      filter          = "metric.type=\"logging.googleapis.com/user/${each.value.name}\" AND resource.type=\"cloud_run_revision\""
      comparison      = "COMPARISON_GT"
      threshold_value = local.dispatch_alerts[each.key].threshold
      duration        = "${local.dispatch_alerts[each.key].duration}s"

      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_SUM"
      }
    }
  }

  alert_strategy {
    auto_close = "1800s"
  }

  documentation {
    content   = "Runbook: [${replace(each.key, "_", " ")}](${local.monitoring_runbook_url}#${local.dispatch_alerts[each.key].runbook})."
    mime_type = "text/markdown"
  }

  notification_channels = var.monitoring_notification_channels
}

resource "google_monitoring_alert_policy" "task_delivery_rejections" {
  project      = var.project_id
  display_name = "${local.resource_name}: repeated Cloud Tasks delivery rejection"
  combiner     = "OR"
  severity     = "ERROR"
  user_labels  = local.monitoring_user_labels

  conditions {
    display_name = "repeated non-OK task attempts"

    condition_threshold {
      filter          = "metric.type=\"cloudtasks.googleapis.com/queue/task_attempt_count\" AND resource.type=\"cloud_tasks_queue\" AND resource.label.queue_id=\"${google_cloud_tasks_queue.execution.name}\" AND resource.label.location=\"${var.region}\" AND metric.label.response_code!=\"ok\""
      comparison      = "COMPARISON_GT"
      threshold_value = var.monitoring_alert_thresholds.task_delivery_rejection_count
      duration        = "${var.monitoring_alert_windows_seconds.task_delivery_rejection}s"

      aggregations {
        alignment_period     = "60s"
        per_series_aligner   = "ALIGN_SUM"
        cross_series_reducer = "REDUCE_SUM"
        group_by_fields      = ["resource.label.queue_id"]
      }
    }
  }

  alert_strategy {
    auto_close = "1800s"
  }

  documentation {
    content   = "Runbook: [Executor delivery rejections](${local.monitoring_runbook_url}#executor-delivery-rejections)."
    mime_type = "text/markdown"
  }

  notification_channels = var.monitoring_notification_channels
}

resource "google_monitoring_alert_policy" "provider_failures" {
  project      = var.project_id
  display_name = "${local.resource_name}: repeated provider failure"
  combiner     = "OR"
  severity     = "ERROR"
  user_labels  = local.monitoring_user_labels

  conditions {
    display_name = "provider failures sustained"

    condition_threshold {
      filter          = "metric.type=\"logging.googleapis.com/user/${google_logging_metric.provider_outcomes.name}\" AND resource.type=\"cloud_run_revision\" AND metric.label.outcome=\"failed\""
      comparison      = "COMPARISON_GT"
      threshold_value = var.monitoring_alert_thresholds.provider_failure_count
      duration        = "${var.monitoring_alert_windows_seconds.provider_failure}s"

      aggregations {
        alignment_period     = "60s"
        per_series_aligner   = "ALIGN_SUM"
        cross_series_reducer = "REDUCE_SUM"
      }
    }
  }

  alert_strategy {
    auto_close = "1800s"
  }

  documentation {
    content   = "Runbook: [Repeated provider failure](${local.monitoring_runbook_url}#repeated-provider-failure)."
    mime_type = "text/markdown"
  }

  notification_channels = var.monitoring_notification_channels
}

resource "google_monitoring_alert_policy" "callback_failures" {
  project      = var.project_id
  display_name = "${local.resource_name}: callback exhaustion or worker failure"
  combiner     = "OR"
  severity     = "ERROR"
  user_labels  = local.monitoring_user_labels

  conditions {
    display_name = "callback delivery failed"

    condition_threshold {
      filter          = "metric.type=\"logging.googleapis.com/user/${google_logging_metric.callback_events.name}\" AND resource.type=\"cloud_run_revision\" AND metric.label.event=\"callback_delivery_settled\" AND metric.label.delivery_status=\"failed\""
      comparison      = "COMPARISON_GT"
      threshold_value = var.monitoring_alert_thresholds.callback_exhaustion_count
      duration        = "${var.monitoring_alert_windows_seconds.callback_exhaustion}s"

      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_SUM"
      }
    }
  }

  conditions {
    display_name = "callback worker failures sustained"

    condition_threshold {
      filter          = "metric.type=\"logging.googleapis.com/user/${google_logging_metric.callback_worker_failures.name}\" AND resource.type=\"cloud_run_revision\""
      comparison      = "COMPARISON_GT"
      threshold_value = var.monitoring_alert_thresholds.callback_worker_failure_count
      duration        = "${var.monitoring_alert_windows_seconds.callback_worker_failure}s"

      aggregations {
        alignment_period     = "60s"
        per_series_aligner   = "ALIGN_SUM"
        cross_series_reducer = "REDUCE_SUM"
      }
    }
  }

  alert_strategy {
    auto_close = "1800s"
  }

  documentation {
    content   = "Runbook: [Callback exhaustion or worker failure](${local.monitoring_runbook_url}#callback-exhaustion-or-worker-failure)."
    mime_type = "text/markdown"
  }

  notification_channels = var.monitoring_notification_channels
}

resource "google_monitoring_alert_policy" "database_capacity" {
  project      = var.project_id
  display_name = "${local.resource_name}: Cloud SQL capacity exhaustion"
  combiner     = "OR"
  severity     = "CRITICAL"
  user_labels  = local.monitoring_user_labels

  conditions {
    display_name = "database connections near limit"

    condition_threshold {
      filter          = "metric.type=\"cloudsql.googleapis.com/database/postgresql/num_backends\" AND resource.type=\"cloudsql_database\" AND resource.label.database_id=\"${var.project_id}:${google_sql_database_instance.runtime.name}\""
      comparison      = "COMPARISON_GT"
      threshold_value = var.monitoring_alert_thresholds.database_connections
      duration        = "${var.monitoring_alert_windows_seconds.database_capacity}s"

      aggregations {
        alignment_period     = "60s"
        per_series_aligner   = "ALIGN_MAX"
        cross_series_reducer = "REDUCE_SUM"
        group_by_fields      = ["resource.label.database_id"]
      }
    }
  }

  conditions {
    display_name = "database storage near limit"

    condition_threshold {
      filter          = "metric.type=\"cloudsql.googleapis.com/database/disk/utilization\" AND resource.type=\"cloudsql_database\" AND resource.label.database_id=\"${var.project_id}:${google_sql_database_instance.runtime.name}\""
      comparison      = "COMPARISON_GT"
      threshold_value = var.monitoring_alert_thresholds.database_storage_utilization
      duration        = "${var.monitoring_alert_windows_seconds.database_capacity}s"

      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_MAX"
      }
    }
  }

  alert_strategy {
    auto_close = "1800s"
  }

  documentation {
    content   = "Runbook: [Cloud SQL capacity exhaustion](${local.monitoring_runbook_url}#cloud-sql-capacity-exhaustion)."
    mime_type = "text/markdown"
  }

  notification_channels = var.monitoring_notification_channels
}

resource "google_monitoring_alert_policy" "database_health" {
  project      = var.project_id
  display_name = "${local.resource_name}: Cloud SQL failed or unknown"
  combiner     = "OR"
  severity     = "CRITICAL"
  user_labels  = local.monitoring_user_labels

  conditions {
    display_name = "database instance failed or unknown"

    condition_threshold {
      filter          = "metric.type=\"cloudsql.googleapis.com/database/instance_state\" AND resource.type=\"cloudsql_database\" AND resource.label.database_id=\"${var.project_id}:${google_sql_database_instance.runtime.name}\" AND (metric.label.state=\"FAILED\" OR metric.label.state=\"UNKNOWN_STATE\")"
      comparison      = "COMPARISON_GT"
      threshold_value = var.monitoring_alert_thresholds.database_unhealthy_state
      duration        = "${var.monitoring_alert_windows_seconds.database_health}s"

      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_COUNT_TRUE"
      }
    }
  }

  alert_strategy {
    auto_close = "1800s"
  }

  documentation {
    content   = "Runbook: [Cloud SQL failed or unknown](${local.monitoring_runbook_url}#cloud-sql-failed-or-unknown)."
    mime_type = "text/markdown"
  }

  notification_channels = var.monitoring_notification_channels
}

resource "google_monitoring_dashboard" "runtime" {
  count = var.enable_monitoring_dashboard ? 1 : 0

  project = var.project_id
  dashboard_json = jsonencode({
    displayName = "${local.resource_name} operations"
    gridLayout = {
      columns = "2"
      widgets = [
        {
          title = "How to read this dashboard"
          text = {
            format  = "MARKDOWN"
            content = "No data is **unknown**, not success. Log-based metrics start only after Terraform creates them. Aged runnable work is represented by nvoken aged-dispatch events plus Cloud Tasks attempt delay; Cloud Tasks exposes queue depth and attempt delay, not oldest-task age. Redis is lossy live-preview transport, never transcript or execution authority. Start with the linked alert runbook."
          }
        },
        {
          title = "Public health checks"
          xyChart = {
            dataSets = [{
              plotType       = "LINE"
              legendTemplate = "check passed"
              timeSeriesQuery = {
                timeSeriesFilter = {
                  filter = "metric.type=\"monitoring.googleapis.com/uptime_check/check_passed\" AND resource.type=\"uptime_url\" AND metric.label.check_id=\"${google_monitoring_uptime_check_config.runtime.uptime_check_id}\""
                  aggregation = {
                    alignmentPeriod  = "60s"
                    perSeriesAligner = "ALIGN_FRACTION_TRUE"
                  }
                }
              }
            }]
          }
        },
        {
          title = "Public request volume"
          xyChart = {
            dataSets = [{
              plotType       = "LINE"
              legendTemplate = "$${metric.labels.response_code_class}"
              timeSeriesQuery = {
                timeSeriesFilter = {
                  filter = "metric.type=\"run.googleapis.com/request_count\" AND resource.type=\"cloud_run_revision\" AND resource.label.service_name=\"${google_cloud_run_v2_service.runtime.name}\""
                  aggregation = {
                    alignmentPeriod  = "60s"
                    perSeriesAligner = "ALIGN_RATE"
                  }
                }
              }
            }]
          }
        },
        {
          title = "Public request latency p95"
          xyChart = {
            dataSets = [{
              plotType = "LINE"
              timeSeriesQuery = {
                timeSeriesFilter = {
                  filter = "metric.type=\"run.googleapis.com/request_latencies\" AND resource.type=\"cloud_run_revision\" AND resource.label.service_name=\"${google_cloud_run_v2_service.runtime.name}\""
                  aggregation = {
                    alignmentPeriod  = "60s"
                    perSeriesAligner = "ALIGN_PERCENTILE_95"
                  }
                }
              }
            }]
          }
        },
        {
          title = "Invocation terminal outcomes"
          xyChart = {
            dataSets = [{
              plotType       = "STACKED_BAR"
              legendTemplate = "$${metric.labels.status}"
              timeSeriesQuery = {
                timeSeriesFilter = {
                  filter = "metric.type=\"logging.googleapis.com/user/${google_logging_metric.invocation_outcomes.name}\" AND resource.type=\"cloud_run_revision\""
                  aggregation = {
                    alignmentPeriod  = "60s"
                    perSeriesAligner = "ALIGN_RATE"
                  }
                }
              }
            }]
          }
        },
        {
          title = "Provider outcomes"
          xyChart = {
            dataSets = [{
              plotType       = "STACKED_BAR"
              legendTemplate = "$${metric.labels.outcome}"
              timeSeriesQuery = {
                timeSeriesFilter = {
                  filter = "metric.type=\"logging.googleapis.com/user/${google_logging_metric.provider_outcomes.name}\" AND resource.type=\"cloud_run_revision\""
                  aggregation = {
                    alignmentPeriod  = "60s"
                    perSeriesAligner = "ALIGN_RATE"
                  }
                }
              }
            }]
          }
        },
        {
          title = "Provider latency p95"
          xyChart = {
            dataSets = [{
              plotType = "LINE"
              timeSeriesQuery = {
                timeSeriesFilter = {
                  filter = "metric.type=\"logging.googleapis.com/user/${google_logging_metric.provider_latency.name}\" AND resource.type=\"cloud_run_revision\""
                  aggregation = {
                    alignmentPeriod  = "60s"
                    perSeriesAligner = "ALIGN_PERCENTILE_95"
                  }
                }
              }
            }]
          }
        },
        {
          title = "Callback retries and terminal outcomes"
          xyChart = {
            dataSets = [{
              plotType       = "STACKED_BAR"
              legendTemplate = "$${metric.labels.event} / $${metric.labels.delivery_status}"
              timeSeriesQuery = {
                timeSeriesFilter = {
                  filter = "metric.type=\"logging.googleapis.com/user/${google_logging_metric.callback_events.name}\" AND resource.type=\"cloud_run_revision\""
                  aggregation = {
                    alignmentPeriod  = "60s"
                    perSeriesAligner = "ALIGN_RATE"
                  }
                }
              }
            }]
          }
        },
        {
          title = "Aged runnable delivery p95"
          xyChart = {
            dataSets = [{
              plotType       = "LINE"
              legendTemplate = "$${metric.labels.event}"
              timeSeriesQuery = {
                timeSeriesFilter = {
                  filter = "metric.type=\"logging.googleapis.com/user/${google_logging_metric.dispatch_age.name}\" AND resource.type=\"cloud_run_revision\""
                  aggregation = {
                    alignmentPeriod  = "60s"
                    perSeriesAligner = "ALIGN_PERCENTILE_95"
                  }
                }
              }
            }]
          }
        },
        {
          title = "Dispatch health events"
          xyChart = {
            dataSets = [for key, metric in google_logging_metric.dispatch_failures : {
              plotType       = "LINE"
              legendTemplate = replace(key, "_", " ")
              timeSeriesQuery = {
                timeSeriesFilter = {
                  filter = "metric.type=\"logging.googleapis.com/user/${metric.name}\" AND resource.type=\"cloud_run_revision\""
                  aggregation = {
                    alignmentPeriod  = "60s"
                    perSeriesAligner = "ALIGN_RATE"
                  }
                }
              }
            }]
          }
        },
        {
          title = "Executor attempt outcomes"
          xyChart = {
            dataSets = [{
              plotType       = "STACKED_BAR"
              legendTemplate = "$${metric.labels.handler_outcome}"
              timeSeriesQuery = {
                timeSeriesFilter = {
                  filter = "metric.type=\"logging.googleapis.com/user/${google_logging_metric.executor_attempts.name}\" AND resource.type=\"cloud_run_revision\""
                  aggregation = {
                    alignmentPeriod  = "60s"
                    perSeriesAligner = "ALIGN_RATE"
                  }
                }
              }
            }]
          }
        },
        {
          title = "Cloud Tasks queue depth"
          xyChart = {
            dataSets = [{
              plotType = "LINE"
              timeSeriesQuery = {
                timeSeriesFilter = {
                  filter = "metric.type=\"cloudtasks.googleapis.com/queue/depth\" AND resource.type=\"cloud_tasks_queue\" AND resource.label.queue_id=\"${google_cloud_tasks_queue.execution.name}\" AND resource.label.location=\"${var.region}\""
                  aggregation = {
                    alignmentPeriod  = "60s"
                    perSeriesAligner = "ALIGN_MAX"
                  }
                }
              }
            }]
          }
        },
        {
          title = "Cloud Tasks attempt delay p95"
          xyChart = {
            dataSets = [{
              plotType = "LINE"
              timeSeriesQuery = {
                timeSeriesFilter = {
                  filter = "metric.type=\"cloudtasks.googleapis.com/queue/task_attempt_delays\" AND resource.type=\"cloud_tasks_queue\" AND resource.label.queue_id=\"${google_cloud_tasks_queue.execution.name}\" AND resource.label.location=\"${var.region}\""
                  aggregation = {
                    alignmentPeriod  = "60s"
                    perSeriesAligner = "ALIGN_PERCENTILE_95"
                  }
                }
              }
            }]
          }
        },
        {
          title = "Cloud SQL connections"
          xyChart = {
            dataSets = [{
              plotType = "LINE"
              timeSeriesQuery = {
                timeSeriesFilter = {
                  filter = "metric.type=\"cloudsql.googleapis.com/database/postgresql/num_backends\" AND resource.type=\"cloudsql_database\" AND resource.label.database_id=\"${var.project_id}:${google_sql_database_instance.runtime.name}\""
                  aggregation = {
                    alignmentPeriod    = "60s"
                    perSeriesAligner   = "ALIGN_MAX"
                    crossSeriesReducer = "REDUCE_SUM"
                    groupByFields      = ["resource.label.database_id"]
                  }
                }
              }
            }]
          }
        },
        {
          title = "Cloud SQL storage utilization"
          xyChart = {
            dataSets = [{
              plotType = "LINE"
              timeSeriesQuery = {
                timeSeriesFilter = {
                  filter = "metric.type=\"cloudsql.googleapis.com/database/disk/utilization\" AND resource.type=\"cloudsql_database\" AND resource.label.database_id=\"${var.project_id}:${google_sql_database_instance.runtime.name}\""
                  aggregation = {
                    alignmentPeriod  = "60s"
                    perSeriesAligner = "ALIGN_MAX"
                  }
                }
              }
            }]
          }
        },
        {
          title = "Cloud SQL instance state"
          xyChart = {
            dataSets = [{
              plotType       = "STACKED_BAR"
              legendTemplate = "$${metric.labels.state}"
              timeSeriesQuery = {
                timeSeriesFilter = {
                  filter = "metric.type=\"cloudsql.googleapis.com/database/instance_state\" AND resource.type=\"cloudsql_database\" AND resource.label.database_id=\"${var.project_id}:${google_sql_database_instance.runtime.name}\""
                  aggregation = {
                    alignmentPeriod  = "60s"
                    perSeriesAligner = "ALIGN_COUNT_TRUE"
                  }
                }
              }
            }]
          }
        },
        {
          title = "Redis memory utilization"
          xyChart = {
            dataSets = [{
              plotType = "LINE"
              timeSeriesQuery = {
                timeSeriesFilter = {
                  filter = "metric.type=\"redis.googleapis.com/stats/memory/usage_ratio\" AND resource.type=\"redis_instance\" AND resource.label.instance_id=\"${google_redis_instance.live_events.name}\" AND resource.label.region=\"${var.region}\""
                  aggregation = {
                    alignmentPeriod  = "60s"
                    perSeriesAligner = "ALIGN_MAX"
                  }
                }
              }
            }]
          }
        },
        {
          title = "Redis server uptime"
          xyChart = {
            dataSets = [{
              plotType = "LINE"
              timeSeriesQuery = {
                timeSeriesFilter = {
                  filter = "metric.type=\"redis.googleapis.com/server/uptime\" AND resource.type=\"redis_instance\" AND resource.label.instance_id=\"${google_redis_instance.live_events.name}\" AND resource.label.region=\"${var.region}\""
                  aggregation = {
                    alignmentPeriod  = "60s"
                    perSeriesAligner = "ALIGN_MIN"
                  }
                }
              }
            }]
          }
        },
        {
          title = "Redis connected clients"
          xyChart = {
            dataSets = [{
              plotType = "LINE"
              timeSeriesQuery = {
                timeSeriesFilter = {
                  filter = "metric.type=\"redis.googleapis.com/clients/connected\" AND resource.type=\"redis_instance\" AND resource.label.instance_id=\"${google_redis_instance.live_events.name}\" AND resource.label.region=\"${var.region}\""
                  aggregation = {
                    alignmentPeriod  = "60s"
                    perSeriesAligner = "ALIGN_MAX"
                  }
                }
              }
            }]
          }
        },
      ]
    }
  })
}
