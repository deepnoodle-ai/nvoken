use std::collections::{BTreeMap, BTreeSet};
use std::time::Duration;

use async_stream::try_stream;
use futures_util::{pin_mut, StreamExt};
use reqwest::header::{HeaderName, ACCEPT, AUTHORIZATION};
use serde::{Deserialize, Serialize};
use serde_json::Value;

use crate::models;
use crate::routes;
use crate::{Client, NvokenError, Turn};

#[derive(Debug, Clone, Default)]
pub struct StreamOptions {
    pub deltas: Option<bool>,
    pub cursor: Option<String>,
    pub timeout: Option<Duration>,
}

#[derive(Debug, Clone)]
pub struct StreamEvent {
    pub id: Option<String>,
    pub event_type: String,
    pub data: Value,
    pub retry: Option<Duration>,
}

#[derive(Debug, Clone, Default)]
pub struct ReducedSnapshot {
    pub messages: Vec<models::ConversationMessage>,
    pub turn_changes: Vec<models::TurnChange>,
    pub previews: Vec<StreamPreview>,
    pub cursor: Option<String>,
}

/// One message the model is writing, accumulated from the fragments of one
/// content block. `delta` carries the fragments for every `kind`, because one
/// accumulator handles all of them.
#[derive(Debug, Clone, Default, Eq, PartialEq, Serialize, Deserialize)]
pub struct StreamPreview {
    pub turn_id: String,
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
    messages: BTreeMap<u64, models::ConversationMessage>,
    changes: BTreeMap<(String, u64), models::TurnChange>,
    previews: BTreeMap<(String, u32), StreamPreview>,
    latest_attempts: BTreeMap<String, u64>,
    terminal_turns: BTreeSet<String>,
    cursor: Option<String>,
}

impl Reducer {
    pub fn apply(&mut self, event: &StreamEvent) -> Result<(), NvokenError> {
        match event.event_type.as_str() {
            "message.delta" => {
                let delta: models::MessageDeltaEvent = serde_json::from_value(event.data.clone())
                    .map_err(|error| {
                    NvokenError::Api(format!(
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
                        NvokenError::Api(format!(
                            "decode {} event payload: {error}",
                            event.event_type
                        ))
                    })?;
                // An absent Turn is Conversation scope: discard all previews.
                if let Some(turn_id) = resync.turn_id {
                    self.discard_previews(&turn_id);
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
                NvokenError::Api(format!(
                    "decode {} event payload: {error}",
                    event.event_type
                ))
            })?;
        // Messages before changes, so a turn is never marked settled before its
        // final message exists.
        for message in update.messages {
            if message.role == models::ConversationMessageRole::Assistant {
                if let Some(turn_id) = &message.turn_id {
                    self.discard_previews(turn_id);
                }
            }
            self.messages.insert(message.sequence, message);
        }
        for change in update.turn_changes {
            if crate::turn_status::is_turn_over(&change) {
                self.terminal_turns.insert(change.turn_id.clone());
                self.discard_previews(&change.turn_id);
            }
            self.changes
                .insert((change.turn_id.clone(), change.revision), change);
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
    pub fn settled(&self, turn_id: &str) -> bool {
        self.terminal_turns.contains(turn_id)
    }

    pub fn snapshot(&self) -> ReducedSnapshot {
        ReducedSnapshot {
            messages: self.messages.values().cloned().collect(),
            turn_changes: self.changes.values().cloned().collect(),
            previews: self.previews.values().cloned().collect(),
            cursor: self.cursor.clone(),
        }
    }

    fn append_preview(&mut self, delta: models::MessageDeltaEvent) {
        if self.terminal_turns.contains(&delta.turn_id) {
            return;
        }
        if let Some(latest) = self.latest_attempts.get(&delta.turn_id).copied() {
            if delta.attempt < latest {
                return;
            }
            if delta.attempt > latest {
                self.discard_previews(&delta.turn_id);
            }
        }
        self.latest_attempts
            .insert(delta.turn_id.clone(), delta.attempt);
        let key = (delta.message_id.clone(), delta.content_index);
        let preview = self.previews.entry(key).or_insert_with(|| StreamPreview {
            turn_id: delta.turn_id.clone(),
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

    fn discard_previews(&mut self, turn_id: &str) {
        self.previews
            .retain(|_, preview| preview.turn_id != turn_id);
        self.latest_attempts.remove(turn_id);
    }
}

fn kind_name(kind: models::MessageDeltaKind) -> String {
    kind.to_string()
}

/// Follow one Turn, ending after its terminal change is delivered.
pub fn stream_turn(
    turn: &Turn,
    options: StreamOptions,
) -> impl futures_core::Stream<Item = Result<StreamEvent, NvokenError>> + '_ {
    try_stream! {
        let mut reducer = Reducer::default();
        let inner = read_stream_with_access(
            turn.client(),
            None,
            Some(turn.id.clone()),
            Some(turn.tenant.clone()),
            turn.user.clone(),
            options,
        );
        pin_mut!(inner);
        while let Some(item) = inner.next().await {
            let event = item?;
            reducer.apply(&event)?;
            yield event;
            if reducer.settled(&turn.id) {
                break;
            }
        }
    }
}

/// Follow current and future Turns in one Conversation. This may stay open idle.
pub fn stream_conversation<'a>(
    client: &'a Client,
    conversation_id: &'a str,
    options: StreamOptions,
) -> impl futures_core::Stream<Item = Result<StreamEvent, NvokenError>> + 'a {
    read_stream(client, Some(conversation_id.to_owned()), None, options)
}

/// Direct SSE read loop for one exact Turn or Conversation route.
pub fn read_stream(
    client: &Client,
    conversation_id: Option<String>,
    turn_id: Option<String>,
    options: StreamOptions,
) -> impl futures_core::Stream<Item = Result<StreamEvent, NvokenError>> + '_ {
    read_stream_with_access(client, conversation_id, turn_id, None, None, options)
}

fn with_turn_access(
    mut request: reqwest_middleware::RequestBuilder,
    tenant: Option<&str>,
    user: Option<&str>,
) -> reqwest_middleware::RequestBuilder {
    if let Some(tenant) = tenant {
        request = request.header("X-Nvoken-Tenant-Key", tenant);
    }
    if let Some(user) = user {
        request = request.header("X-Nvoken-User-Key", user);
    }
    request
}

fn read_stream_with_access(
    client: &Client,
    conversation_id: Option<String>,
    turn_id: Option<String>,
    tenant: Option<String>,
    user: Option<String>,
    options: StreamOptions,
) -> impl futures_core::Stream<Item = Result<StreamEvent, NvokenError>> + '_ {
    try_stream! {
        let mut cursor = options.cursor.clone();
        let deadline = options.timeout.map(|timeout| std::time::Instant::now() + timeout);
        let mut retry = Duration::from_secs(1);
        loop {
            if deadline.is_some_and(|deadline| std::time::Instant::now() >= deadline) {
                Err(NvokenError::Api("stream observation timed out".into()))?;
            }
            let path = if let Some(turn_id) = &turn_id {
                routes::STREAM_TURN.replace("{turn_id}", &crate::apis::urlencode(turn_id))
            } else {
                routes::STREAM_CONVERSATION.replace(
                    "{conversation_id}",
                    &crate::apis::urlencode(conversation_id.as_deref().expect("stream target")),
                )
            };
            let url = format!("{}{}", client.raw().base_path, path);
            let mut request = client
                .raw()
                .client
                .get(url)
                .header(ACCEPT, "text/event-stream");
            if let Some(token) = &client.raw().bearer_access_token {
                request = request.header(AUTHORIZATION, format!("Bearer {token}"));
            }
            request = with_turn_access(request, tenant.as_deref(), user.as_deref());
            if let Some(cursor) = &cursor {
                request = request.header(HeaderName::from_static("last-event-id"), cursor);
            }
            if let Some(deltas) = options.deltas {
                request = request.query(&[("deltas", deltas)]);
            }
            if let Some(deadline) = deadline {
                request = request.timeout(deadline.saturating_duration_since(std::time::Instant::now()));
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
                let _ = headers;
                Err(NvokenError::Api(format!("stream returned HTTP {status}: {body}")))?;
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
            std::str::from_utf8(bytes).map_err(|error| NvokenError::Api(error.to_string()))?,
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
            serde_json::from_str(&joined)
                .map_err(|error| NvokenError::Api(format!("decode SSE data {joined:?}: {error}")))?
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

#[cfg(test)]
mod tests {
    use super::*;
    use chrono::DateTime;
    use serde_json::json;

    fn event(event_type: &str, data: Value) -> StreamEvent {
        StreamEvent {
            id: None,
            event_type: event_type.into(),
            data,
            retry: None,
        }
    }

    #[test]
    fn reducer_uses_turn_ids_for_previews_changes_and_resync() {
        let mut reducer = Reducer::default();
        let delta = |fragment: &str| {
            event(
                "message.delta",
                json!({
                    "type": "message.delta",
                    "conversation_id": null,
                    "content_expires_at": null,
                    "turn_id": "turn_01kc514000e008000000000001",
                    "attempt": 1,
                    "message_id": "msg_01kc514000e008000000000001",
                    "content_index": 0,
                    "kind": "text",
                    "delta": fragment,
                    "emitted_at": "2026-08-26T12:00:00Z"
                }),
            )
        };
        reducer.apply(&delta("hel")).unwrap();
        reducer.apply(&delta("lo")).unwrap();
        assert_eq!(reducer.snapshot().previews[0].delta, "hello");

        reducer
            .apply(&event(
                "stream.resync",
                json!({
                    "type": "stream.resync",
                    "conversation_id": null,
                    "content_expires_at": null,
                    "turn_id": "turn_01kc514000e008000000000001",
                    "reason": "live_delivery_gap"
                }),
            ))
            .unwrap();
        assert!(reducer.snapshot().previews.is_empty());

        let change = models::TurnChange::new(
            "turn_01kc514000e008000000000001".into(),
            None,
            None,
            2,
            models::TurnStatus::Completed,
            true,
            None,
            None,
            None,
            DateTime::parse_from_rfc3339("2026-08-26T12:00:01Z").unwrap(),
        );
        let update = models::TranscriptUpdateEvent::new(
            models::transcript_update_event::Type::EventTranscriptUpdate,
            None,
            None,
            Vec::new(),
            vec![change],
            "cursor-2".into(),
        );
        reducer
            .apply(&StreamEvent {
                id: Some("cursor-2".into()),
                event_type: "transcript.update".into(),
                data: serde_json::to_value(update).unwrap(),
                retry: None,
            })
            .unwrap();
        let snapshot = reducer.snapshot();
        assert_eq!(
            snapshot.turn_changes[0].turn_id,
            "turn_01kc514000e008000000000001"
        );
        assert!(reducer.settled("turn_01kc514000e008000000000001"));
        assert_eq!(snapshot.cursor.as_deref(), Some("cursor-2"));
    }

    #[test]
    fn direct_turn_stream_carries_recovery_assertions() {
        let client = Client::with_base_url("test", "http://localhost");
        let request = with_turn_access(
            client
                .raw()
                .client
                .get("http://localhost/v1/turns/turn_01kc514000e008000000000001/stream"),
            Some("acme"),
            Some("alice"),
        )
        .build()
        .unwrap();
        assert_eq!(request.headers()["X-Nvoken-Tenant-Key"], "acme");
        assert_eq!(request.headers()["X-Nvoken-User-Key"], "alice");
    }
}
