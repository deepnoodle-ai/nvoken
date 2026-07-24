use std::collections::{BTreeMap, BTreeSet};
use std::time::Duration;

use async_stream::try_stream;
use futures_util::StreamExt;
use reqwest::header::{HeaderName, ACCEPT, AUTHORIZATION};
use serde::{Deserialize, Serialize};
use serde_json::Value;

use crate::client::{InvocationHandle, NvokenError};
use crate::models;
use crate::routes;

#[derive(Debug, Clone)]
pub struct StreamEvent {
    pub id: Option<String>,
    pub event_type: String,
    pub data: Value,
    pub retry: Option<Duration>,
}

#[derive(Debug, Clone, Default)]
pub struct ReducedSnapshot {
    pub messages: Vec<models::SessionMessage>,
    pub invocation_changes: Vec<models::InvocationChange>,
    pub previews: Vec<StreamPreview>,
    pub resume_cursor: Option<String>,
}

#[derive(Debug, Clone, Default, Eq, PartialEq, Serialize, Deserialize)]
pub struct StreamPreview {
    pub invocation_id: String,
    pub attempt: u64,
    pub iteration: u32,
    pub content_index: u32,
    pub output_text: String,
    pub thinking: String,
}

#[derive(Debug, Default)]
pub struct Reducer {
    messages: BTreeMap<u64, models::SessionMessage>,
    changes: BTreeMap<(String, u64), models::InvocationChange>,
    previews: BTreeMap<(String, u64, u32, u32), StreamPreview>,
    latest_attempts: BTreeMap<String, u64>,
    terminal_invocations: BTreeSet<String>,
    cursor: Option<String>,
}

impl Reducer {
    pub fn apply(&mut self, event: &StreamEvent) -> Result<(), NvokenError> {
        match event.event_type.as_str() {
            "output_text.delta" => {
                let delta: models::OutputTextDeltaEvent =
                    serde_json::from_value(event.data.clone()).map_err(|error| {
                        NvokenError::unexpected(format!(
                            "decode {} event payload: {error}",
                            event.event_type
                        ))
                    })?;
                self.append_preview(
                    delta.invocation_id,
                    delta.attempt,
                    delta.iteration,
                    delta.content_index,
                    delta.text,
                    String::new(),
                );
                return Ok(());
            }
            "thinking.delta" => {
                let delta: models::ThinkingDeltaEvent = serde_json::from_value(event.data.clone())
                    .map_err(|error| {
                        NvokenError::unexpected(format!(
                            "decode {} event payload: {error}",
                            event.event_type
                        ))
                    })?;
                self.append_preview(
                    delta.invocation_id,
                    delta.attempt,
                    delta.iteration,
                    delta.content_index,
                    String::new(),
                    delta.thinking,
                );
                return Ok(());
            }
            "stream.resync" => {
                let resync: models::StreamResyncEvent = serde_json::from_value(event.data.clone())
                    .map_err(|error| {
                        NvokenError::unexpected(format!(
                            "decode {} event payload: {error}",
                            event.event_type
                        ))
                    })?;
                if let Some(invocation_id) = resync.invocation_id {
                    self.discard_previews(&invocation_id);
                } else {
                    self.previews.clear();
                    self.latest_attempts.clear();
                }
                return Ok(());
            }
            _ => {}
        }
        if event.event_type != "transcript.update" {
            return Ok(());
        }
        let update: models::TranscriptUpdate =
            serde_json::from_value(event.data.clone()).map_err(|error| {
                NvokenError::unexpected(format!(
                    "decode {} event payload: {error}",
                    event.event_type
                ))
            })?;
        for message in update.messages {
            if message.role == models::SessionMessageRole::Assistant {
                self.discard_previews(&message.invocation_id);
            }
            self.messages.insert(message.sequence, message);
        }
        for change in update.invocation_changes {
            if matches!(
                change.status,
                models::InvocationStatus::Completed
                    | models::InvocationStatus::Failed
                    | models::InvocationStatus::Cancelled
            ) {
                self.terminal_invocations
                    .insert(change.invocation_id.clone());
                self.discard_previews(&change.invocation_id);
            }
            self.changes
                .insert((change.invocation_id.clone(), change.revision), change);
        }
        let cursor = event
            .id
            .as_ref()
            .filter(|value| !value.is_empty())
            .cloned()
            .or_else(|| (!update.resume_cursor.is_empty()).then_some(update.resume_cursor));
        if cursor.is_some() {
            self.cursor = cursor;
        }
        Ok(())
    }

    pub fn snapshot(&self) -> ReducedSnapshot {
        ReducedSnapshot {
            messages: self.messages.values().cloned().collect(),
            invocation_changes: self.changes.values().cloned().collect(),
            previews: self.previews.values().cloned().collect(),
            resume_cursor: self.cursor.clone(),
        }
    }

    fn append_preview(
        &mut self,
        invocation_id: String,
        attempt: u64,
        iteration: u32,
        content_index: u32,
        output_text: String,
        thinking: String,
    ) {
        if self.terminal_invocations.contains(&invocation_id) {
            return;
        }
        if let Some(latest) = self.latest_attempts.get(&invocation_id).copied() {
            if attempt < latest {
                return;
            }
            if attempt > latest {
                self.discard_previews(&invocation_id);
            }
        }
        self.latest_attempts.insert(invocation_id.clone(), attempt);
        let key = (invocation_id.clone(), attempt, iteration, content_index);
        let preview = self.previews.entry(key).or_insert_with(|| StreamPreview {
            invocation_id,
            attempt,
            iteration,
            content_index,
            ..StreamPreview::default()
        });
        preview.output_text.push_str(&output_text);
        preview.thinking.push_str(&thinking);
    }

    fn discard_previews(&mut self, invocation_id: &str) {
        self.previews
            .retain(|(candidate, _, _, _), _| candidate != invocation_id);
        self.latest_attempts.remove(invocation_id);
    }
}

pub fn stream_handle(
    handle: &InvocationHandle,
) -> impl futures_core::Stream<Item = Result<StreamEvent, NvokenError>> + '_ {
    try_stream! {
        let mut cursor: Option<String> = None;
        let mut retry = Duration::from_secs(1);
        'invocation: loop {
            let path = routes::STREAM_INVOCATION.replace(
                "{invocation_id}",
                &crate::apis::urlencode(&handle.invocation_id),
            );
            let url = format!("{}{}", handle.client.configuration.base_path, path);
            let mut request = handle
                .client
                .stream_client
                .get(url)
                .header(ACCEPT, "text/event-stream");
            if let Some(token) = &handle.client.configuration.bearer_access_token {
                request = request.header(AUTHORIZATION, format!("Bearer {token}"));
            }
            if let Some(cursor) = &cursor {
                request = request.header(HeaderName::from_static("last-event-id"), cursor);
            }
            let response = match request.send().await {
                Ok(response) => response,
                Err(_) => {
                    tokio::time::sleep(retry).await;
                    continue;
                }
            };
            if !response.status().is_success() {
                let status = response.status();
                let headers = response.headers().clone();
                let body = response.json::<Value>().await.unwrap_or(Value::Null);
                Err(NvokenError::response_with_headers(status, body, &headers))?;
                continue;
            }
            let mut decoder = Decoder::default();
            let mut bytes = response.bytes_stream();
            while let Some(chunk) = bytes.next().await {
                let chunk = match chunk {
                    Ok(chunk) => chunk,
                    Err(_) => break,
                };
                for event in decoder.push(&chunk)? {
                    if let Some(value) = event.retry {
                        retry = value.min(Duration::from_secs(30));
                    }
                    if event.id.is_some() {
                        cursor.clone_from(&event.id);
                    }
                    let settled = event.event_type == "invocation.result";
                    yield event;
                    if settled {
                        break 'invocation;
                    }
                }
            }
            for event in decoder.finish()? {
                if event.id.is_some() {
                    cursor.clone_from(&event.id);
                }
                let settled = event.event_type == "invocation.result";
                yield event;
                if settled {
                    break 'invocation;
                }
            }
            tokio::time::sleep(retry).await;
        }
    }
}

#[derive(Default)]
struct Decoder {
    buffer: String,
    event_type: Option<String>,
    event_id: Option<String>,
    retry: Option<Duration>,
    data: Vec<String>,
}

impl Decoder {
    fn push(&mut self, bytes: &[u8]) -> Result<Vec<StreamEvent>, NvokenError> {
        self.buffer.push_str(
            std::str::from_utf8(bytes)
                .map_err(|error| NvokenError::unexpected(error.to_string()))?,
        );
        let mut events = Vec::new();
        while let Some(newline) = self.buffer.find('\n') {
            let line = self.buffer[..newline].trim_end_matches('\r').to_owned();
            self.buffer.drain(..=newline);
            if let Some(event) = self.line(&line)? {
                events.push(event);
            }
        }
        Ok(events)
    }

    fn finish(&mut self) -> Result<Vec<StreamEvent>, NvokenError> {
        let mut events = Vec::new();
        if !self.buffer.is_empty() {
            let line = std::mem::take(&mut self.buffer);
            if let Some(event) = self.line(&line)? {
                events.push(event);
            }
        }
        if let Some(event) = self.dispatch()? {
            events.push(event);
        }
        Ok(events)
    }

    fn line(&mut self, line: &str) -> Result<Option<StreamEvent>, NvokenError> {
        if line.is_empty() {
            return self.dispatch();
        }
        if line.starts_with(':') {
            return Ok(None);
        }
        let (field, value) = line
            .split_once(':')
            .map(|(field, value)| (field, value.strip_prefix(' ').unwrap_or(value)))
            .unwrap_or((line, ""));
        match field {
            "event" => self.event_type = Some(value.to_owned()),
            "id" => self.event_id = Some(value.to_owned()),
            "retry" => {
                if let Ok(milliseconds) = value.parse::<u64>() {
                    self.retry = Some(Duration::from_millis(milliseconds));
                }
            }
            "data" => self.data.push(value.to_owned()),
            _ => {}
        }
        Ok(None)
    }

    fn dispatch(&mut self) -> Result<Option<StreamEvent>, NvokenError> {
        if self.event_type.is_none()
            && self.event_id.is_none()
            && self.retry.is_none()
            && self.data.is_empty()
        {
            return Ok(None);
        }
        let raw_data = std::mem::take(&mut self.data);
        let data = if raw_data.is_empty() {
            Value::Null
        } else {
            let joined = raw_data.join("\n");
            serde_json::from_str(&joined).map_err(|error| {
                NvokenError::unexpected(format!("decode SSE data {joined:?}: {error}"))
            })?
        };
        Ok(Some(StreamEvent {
            id: self.event_id.take(),
            event_type: self
                .event_type
                .take()
                .unwrap_or_else(|| "message".to_owned()),
            data,
            retry: self.retry.take(),
        }))
    }
}
