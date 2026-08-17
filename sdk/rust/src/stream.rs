use std::collections::{BTreeMap, BTreeSet};
use std::time::Duration;

use async_stream::try_stream;
use futures_util::{pin_mut, StreamExt};
use reqwest::header::{HeaderName, ACCEPT, AUTHORIZATION};
use serde::{Deserialize, Serialize};
use serde_json::Value;

use crate::client::{InvocationHandle, NvokenError, StreamOptions};
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
    pub cursor: Option<String>,
}

/// One message the model is writing, accumulated from the fragments of one
/// content block. `delta` carries the fragments for every `kind`, because one
/// accumulator handles all of them.
#[derive(Debug, Clone, Default, Eq, PartialEq, Serialize, Deserialize)]
pub struct StreamPreview {
    pub invocation_id: String,
    pub attempt: u64,
    /// The saved message this preview is building. It is the key: the handoff
    /// to the saved message updates a row that already has its permanent
    /// identity, rather than one row disappearing and another taking its place.
    pub message_id: String,
    pub content_index: u32,
    pub kind: String,
    pub delta: String,
    /// Present on `tool_arguments` previews, naming the call being written.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub tool_call_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub name: Option<String>,
}

#[derive(Debug, Default)]
pub struct Reducer {
    messages: BTreeMap<u64, models::SessionMessage>,
    changes: BTreeMap<(String, u64), models::InvocationChange>,
    previews: BTreeMap<(String, u32), StreamPreview>,
    latest_attempts: BTreeMap<String, u64>,
    terminal_invocations: BTreeSet<String>,
    cursor: Option<String>,
}

impl Reducer {
    pub fn apply(&mut self, event: &StreamEvent) -> Result<(), NvokenError> {
        match event.event_type.as_str() {
            "message.delta" => {
                let delta: models::MessageDeltaEvent = serde_json::from_value(event.data.clone())
                    .map_err(|error| {
                    NvokenError::unexpected(format!(
                        "decode {} event payload: {error}",
                        event.event_type
                    ))
                })?;
                self.append_preview(delta);
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
                // An absent Invocation is scope: discard previews for the whole
                // Session.
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
        let update: models::TranscriptUpdateEvent = serde_json::from_value(event.data.clone())
            .map_err(|error| {
                NvokenError::unexpected(format!(
                    "decode {} event payload: {error}",
                    event.event_type
                ))
            })?;
        // Messages before changes, so a turn is never marked settled before its
        // final message exists.
        for message in update.messages {
            if message.role == models::SessionMessageRole::Assistant {
                if let Some(invocation_id) = &message.invocation_id {
                    self.discard_previews(invocation_id);
                }
            }
            self.messages.insert(message.sequence, message);
        }
        for change in update.invocation_changes {
            if crate::invocation_status::is_turn_over(&change) {
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
            .or_else(|| (!update.cursor.is_empty()).then_some(update.cursor));
        if cursor.is_some() {
            self.cursor = cursor;
        }
        Ok(())
    }

    /// Whether a change carrying a terminal status has arrived for this turn.
    /// That is the terminal signal, and there is no other.
    pub fn settled(&self, invocation_id: &str) -> bool {
        self.terminal_invocations.contains(invocation_id)
    }

    pub fn snapshot(&self) -> ReducedSnapshot {
        ReducedSnapshot {
            messages: self.messages.values().cloned().collect(),
            invocation_changes: self.changes.values().cloned().collect(),
            previews: self.previews.values().cloned().collect(),
            cursor: self.cursor.clone(),
        }
    }

    fn append_preview(&mut self, delta: models::MessageDeltaEvent) {
        if self.terminal_invocations.contains(&delta.invocation_id) {
            return;
        }
        if let Some(latest) = self.latest_attempts.get(&delta.invocation_id).copied() {
            if delta.attempt < latest {
                return;
            }
            if delta.attempt > latest {
                self.discard_previews(&delta.invocation_id);
            }
        }
        self.latest_attempts
            .insert(delta.invocation_id.clone(), delta.attempt);
        let key = (delta.message_id.clone(), delta.content_index);
        let preview = self.previews.entry(key).or_insert_with(|| StreamPreview {
            invocation_id: delta.invocation_id.clone(),
            attempt: delta.attempt,
            message_id: delta.message_id.clone(),
            content_index: delta.content_index,
            ..StreamPreview::default()
        });
        preview.attempt = delta.attempt;
        preview.kind = kind_name(delta.kind);
        preview.delta.push_str(&delta.delta);
        if delta.tool_call_id.is_some() {
            preview.tool_call_id = delta.tool_call_id;
        }
        if delta.name.is_some() {
            preview.name = delta.name;
        }
    }

    fn discard_previews(&mut self, invocation_id: &str) {
        self.previews
            .retain(|_, preview| preview.invocation_id != invocation_id);
        self.latest_attempts.remove(invocation_id);
    }
}

fn kind_name(kind: models::MessageDeltaKind) -> String {
    kind.to_string()
}

/// Follow one turn. The stream is filtered to it, and it ends once a change for
/// that turn carries a terminal status.
pub fn stream_handle(
    handle: &InvocationHandle,
) -> impl futures_core::Stream<Item = Result<StreamEvent, NvokenError>> + '_ {
    stream_handle_with_options(handle, StreamOptions::default())
}

pub fn stream_handle_with_options(
    handle: &InvocationHandle,
    options: StreamOptions,
) -> impl futures_core::Stream<Item = Result<StreamEvent, NvokenError>> + '_ {
    try_stream! {
        let session_id = handle.require_session_id().await?;
        let mut reducer = Reducer::default();
        let inner = read_stream(
            handle,
            session_id,
            Some(handle.invocation_id.clone()),
            options,
        );
        pin_mut!(inner);
        while let Some(item) = inner.next().await {
            let event = item?;
            reducer.apply(&event)?;
            yield event;
            if reducer.settled(&handle.invocation_id) {
                break;
            }
        }
    }
}

/// The one read loop. It reconnects from its last durable cursor on any
/// connection end. A `connection.closing` frame says only that, and a silent
/// drop says nothing at all, so neither is a reason to stop. Unfiltered it
/// never ends on its own: the stream stays open while the Session is idle and
/// a turn started later appears on it.
pub fn read_stream(
    handle: &InvocationHandle,
    session_id: String,
    invocation_id: Option<String>,
    options: StreamOptions,
) -> impl futures_core::Stream<Item = Result<StreamEvent, NvokenError>> + '_ {
    try_stream! {
        let mut cursor: Option<String> = None;
        let mut retry = Duration::from_secs(1);
        loop {
            let path = routes::STREAM_SESSION.replace(
                "{session_id}",
                &crate::apis::urlencode(&session_id),
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
            if let Some(invocation_id) = &invocation_id {
                request = request.query(&[("invocation_id", invocation_id)]);
            }
            if let Some(deltas) = options.deltas {
                request = request.query(&[("deltas", deltas)]);
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
                    // A frame carrying only `retry:` is a control frame, not an
                    // event. The runtime opens every stream with one. Its
                    // bookkeeping is applied above; there is nothing to yield.
                    if event.data.is_null() {
                        continue;
                    }
                    yield event;
                }
            }
            for event in decoder.finish()? {
                if event.id.is_some() {
                    cursor.clone_from(&event.id);
                }
                if event.data.is_null() {
                    continue;
                }
                yield event;
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
