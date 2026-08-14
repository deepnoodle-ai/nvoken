/* tslint:disable */
/* eslint-disable */
/**
 * nvoken API
 * nvoken runs agent turns for you. You describe a turn — an Agent Definition and some input — and nvoken queues it, runs it in the background, keeps running it across restarts and failures, and lets you either watch it live or come back for the result later.  Your application stays in charge of what your agents are and when they run. nvoken owns the conversation: it stores the messages, tracks the state of every turn, and handles talking to the model providers.  ## Getting started  `POST /v1/invocations` starts a turn and returns a `202` right away. From there:  - Follow it live with `GET /v1/invocations/{invocation_id}/stream`, or   read `GET /v1/invocations/{invocation_id}/result` whenever you want the   finished answer. Disconnecting never cancels anything. - If your agent uses tools you run yourself, the turn stops with status   `waiting` and lists what it needs. Run them, post the results to   `/tool-results`, and the turn continues where it left off. - Sessions carry conversation history from one turn to the next. They   last until you delete them, or until a retention window you set runs   out.  Also here: tools nvoken calls back to over HTTPS, remote MCP servers, structured output validated against your JSON Schema, reusable Agent Definitions, image and document input, your own model provider keys, and spending limits.  ## Authorization  Machine credentials use `nvk_…` bearer API keys. A configured trusted console may also present a short-lived Ed25519 issuer token; this is an authentication presentation only and does not create a user identity model in nvoken.  Browser-direct callers use narrow JWTs, exact-origin CORS, and App/tenant/user admission limits. A host may mint client tokens from its own Ed25519 key. An App that explicitly enables anonymous access may instead let nvoken mint a short-lived access token and renewable visitor token from one allowed public origin.  A browser grant sees less, and it sees it in the same shape. There is one schema per resource, and the fields a browser may not see are simply omitted from its responses, which is why those fields are not required. There are no parallel browser schemas and no response unions, so every payload decodes against one type and nothing about the shape has to be inferred from the credential that fetched it. `GET /v1/identity` reports which kind of caller you are, under `authentication.method`.  - App-scoped Runtime credentials may call every Runtime operation and GET /v1/identity. - App-scoped Viewer credentials may call Runtime reads and GET /v1/identity. - App-scoped Operator credentials may call every Runtime operation, GET /v1/identity, and all credential lifecycle operations. - Org-scoped Viewer and Operator credentials are management and reporting   identities only. They resolve no tenant and cannot perform Runtime   operations; Operators can register and manage Apps and their App   credentials, while Viewers have read-only access.  Tenant, Session, operation, and expiry constraints only narrow these grants.  ## Familiar names  Where a name already means something in other agent APIs, nvoken uses it the same way rather than inventing its own. `metadata` follows OpenAI\'s limits of 16 keys, 64-character names, and 512-byte values. `output_text` is the assistant\'s text joined into one string. `reasoning.effort` takes `low`, `medium`, `high`, `xhigh`, and `max`. `stop_reason: end_turn`, status `running`, and the `commentary` and `final_answer` message phases are the same idea you have seen elsewhere. If you have integrated another agent API, these should need no translation.  ## You can always read back what applied  Anything nvoken decides on your behalf is readable on the resource that used it. You never have to work out what happened by combining the request you sent with your own assumptions about nvoken\'s defaults — just read the resource.  A turn reports the `limits` it is really running under, after defaults and minimums; the `agent_definition` it ran with, exactly as stored; and `provenance`, which records what actually served the request. A Session reports its compaction (summarization) policy with `auto` already resolved to a real number and a real model, and its retention window as accepted. New settings will work the same way: a default you cannot read back is a setting only the server knows about.  ## Streaming  A turn and an Invocation are the same thing. The endpoint summaries say turn; the schemas say Invocation.  Two streams carry the same frames. `GET /v1/invocations/{invocation_id}/stream` follows one turn and ends when that turn settles. `GET /v1/sessions/{session_id}/transcript/stream` follows every turn in a Session, and is the surface to use for a conversation. `POST /v1/invocations` with `Accept: text/event-stream` admits and streams one turn inline.  ### Saved frames and live frames  Every frame is one or the other, and the difference decides what you may store. A saved frame carries an SSE `id`. That ID is your resume position and the only value you need to keep. A live frame carries no `id`: it is a preview or a control signal, it is never stored, and it is never replayed.  The Invocation stream\'s saved frames are `invocation.accepted`, `invocation.update`, and `invocation.result`. The Session stream\'s only saved frame is `transcript.update`. Every other frame on either stream is live.  ### Resuming and finishing  The resume position has one name: `cursor`. It is the field on a durable frame and the query parameter that resumes a stream. Server-Sent Events mirrors it onto the `id:` line and accepts the `Last-Event-ID` header in the parameter\'s place, because a faithful SSE binding must; those are the binding\'s mechanics, not two more names for the value, and another binding would carry the same one name. `cursor` wins when a request supplies both. Cursors are Session-scoped on both streams, so a position taken from one stream resumes the other.  Reconnecting to a turn that has already settled always yields `invocation.result` followed by `stream.end` with reason `terminal`, at any cursor. Both are valid signals that a turn is over, and a client may exit on either.  `invocation.accepted` is emitted only by the inline `POST` path. The `GET` stream never sends it, so a client that admits separately never sees it. The nvoken SDKs synthesize an equivalent locally so their callers see the same first event either way.  An `invocation.update` never carries a terminal status. Terminal state arrives as `invocation.result` and nowhere else on that stream. The `invocation` it carries is re-read when the frame is written, so it is current state with a resume position attached rather than a snapshot taken at the cursor.  ### Previews  `output_text.delta` and `thinking.delta` preview one model iteration. Their identity is `(invocation_id, attempt, iteration, content_index)`. Accumulate by that tuple, and discard everything provisional when `attempt` increases, when `stream.resync` arrives, when the saved message lands, and when the turn reaches a terminal status. One model iteration produces exactly one saved assistant message, so previews sharing an `(invocation_id, attempt, iteration)` build one message. Never store preview text as a message, and never use it to decide whether a turn succeeded.  `attempt`, `iteration`, `revision`, and `sequence` count from 1. `content_index` counts from 0.  ### Compatibility  Stream events may gain fields over time. Ignore fields you do not recognize rather than refusing the frame. New enum values may appear too. Treat an unknown `stream.resync` reason as `live_delivery_gap` and discard your previews. Treat an unknown `stream.end` reason as `rotate` and reconnect with your last saved `id`. Reconnecting is always safe: a turn that has settled re-yields its result.  ### Transport  The protocol is the frames and their durability rules. Server-Sent Events is how they travel today and the only binding. Three mechanics belong to SSE itself: the `id:` line, the `retry:` opener, and comment keepalives. Everything else, resumption included, lives in the frames.  ### What the stream carries  Frames carry structure and text. Images and documents travel as descriptors, with media type, size in bytes, and a `sha256:` digest, never as inline bytes. Frame sizes stay bounded by text no matter what a turn produced.
 *
 * The version of the OpenAPI document: 0.1.0
 *
 *
 * NOTE: This class is auto generated by OpenAPI Generator (https://openapi-generator.tech).
 * https://openapi-generator.tech
 * Do not edit the class manually.
 */

import { mapValues } from '../runtime.js';
import type { Money } from './Money.js';
import {
    MoneyFromJSON,
    MoneyFromJSONTyped,
    MoneyToJSON,
    MoneyToJSONTyped,
} from './Money.js';

/**
 *
 * @export
 * @interface TraceSpan
 */
export interface TraceSpan {
    /**
     * Lowercase W3C trace ID. Possession does not grant access.
     * @type {string}
     * @memberof TraceSpan
     */
    traceId: string;
    /**
     *
     * @type {string}
     * @memberof TraceSpan
     */
    spanId: string;
    /**
     *
     * @type {string}
     * @memberof TraceSpan
     */
    parentSpanId?: string;
    /**
     *
     * @type {string}
     * @memberof TraceSpan
     */
    name: string;
    /**
     * OpenTelemetry span kind; known values include internal, client, server, producer, consumer, and unspecified.
     * @type {string}
     * @memberof TraceSpan
     */
    kind: string;
    /**
     * GenAI operation name. Dive currently emits invoke_agent, chat, and execute_tool.
     * @type {string}
     * @memberof TraceSpan
     */
    operation?: string;
    /**
     *
     * @type {TraceSpanStatusEnum}
     * @memberof TraceSpan
     */
    status: TraceSpanStatusEnum;
    /**
     *
     * @type {Date}
     * @memberof TraceSpan
     */
    startedAt: Date;
    /**
     *
     * @type {Date}
     * @memberof TraceSpan
     */
    endedAt?: Date;
    /**
     *
     * @type {number}
     * @memberof TraceSpan
     */
    durationMs?: number;
    /**
     *
     * @type {string}
     * @memberof TraceSpan
     */
    provider?: string;
    /**
     *
     * @type {string}
     * @memberof TraceSpan
     */
    requestModel?: string;
    /**
     *
     * @type {string}
     * @memberof TraceSpan
     */
    responseModel?: string;
    /**
     *
     * @type {string}
     * @memberof TraceSpan
     */
    toolName?: string;
    /**
     *
     * @type {string}
     * @memberof TraceSpan
     */
    errorType?: string;
    /**
     * Provider-reported input tokens; cache fields may overlap this total.
     * @type {number}
     * @memberof TraceSpan
     */
    inputTokens?: number;
    /**
     * Provider-reported output tokens; reasoning tokens are a subset when reported.
     * @type {number}
     * @memberof TraceSpan
     */
    outputTokens?: number;
    /**
     * Input tokens used to create a provider cache entry; do not add blindly to input_tokens.
     * @type {number}
     * @memberof TraceSpan
     */
    cacheCreationInputTokens?: number;
    /**
     * Input tokens served from provider cache; do not add blindly to input_tokens.
     * @type {number}
     * @memberof TraceSpan
     */
    cacheReadInputTokens?: number;
    /**
     * Reasoning tokens included within output_tokens when the provider reports them.
     * @type {number}
     * @memberof TraceSpan
     */
    reasoningTokens?: number;
    /**
     *
     * @type {Array<string>}
     * @memberof TraceSpan
     */
    finishReasons?: Array<string>;
    /**
     * Time from model-call start to the first streamed output token.
     * @type {number}
     * @memberof TraceSpan
     */
    timeToFirstTokenMs?: number;
    /**
     *
     * @type {Money}
     * @memberof TraceSpan
     */
    modelCost?: Money;
}


/**
 * @export
 */
export const TraceSpanStatusEnum = {
    Unset: 'unset',
    Ok: 'ok',
    Error: 'error'
} as const;
export type TraceSpanStatusEnum = typeof TraceSpanStatusEnum[keyof typeof TraceSpanStatusEnum];


/**
 * Check if a given object implements the TraceSpan interface.
 */
export function instanceOfTraceSpan(value: object): value is TraceSpan {
    if (!('traceId' in value) || value['traceId'] === undefined) return false;
    if (!('spanId' in value) || value['spanId'] === undefined) return false;
    if (!('name' in value) || value['name'] === undefined) return false;
    if (!('kind' in value) || value['kind'] === undefined) return false;
    if (!('status' in value) || value['status'] === undefined) return false;
    if (!('startedAt' in value) || value['startedAt'] === undefined) return false;
    return true;
}

export function TraceSpanFromJSON(json: any): TraceSpan {
    return TraceSpanFromJSONTyped(json, false);
}

export function TraceSpanFromJSONTyped(json: any, ignoreDiscriminator: boolean): TraceSpan {
    if (json == null) {
        return json;
    }
    return {

        'traceId': json['trace_id'],
        'spanId': json['span_id'],
        'parentSpanId': json['parent_span_id'] == null ? undefined : json['parent_span_id'],
        'name': json['name'],
        'kind': json['kind'],
        'operation': json['operation'] == null ? undefined : json['operation'],
        'status': json['status'],
        'startedAt': (new Date(json['started_at'])),
        'endedAt': json['ended_at'] == null ? undefined : (new Date(json['ended_at'])),
        'durationMs': json['duration_ms'] == null ? undefined : json['duration_ms'],
        'provider': json['provider'] == null ? undefined : json['provider'],
        'requestModel': json['request_model'] == null ? undefined : json['request_model'],
        'responseModel': json['response_model'] == null ? undefined : json['response_model'],
        'toolName': json['tool_name'] == null ? undefined : json['tool_name'],
        'errorType': json['error_type'] == null ? undefined : json['error_type'],
        'inputTokens': json['input_tokens'] == null ? undefined : json['input_tokens'],
        'outputTokens': json['output_tokens'] == null ? undefined : json['output_tokens'],
        'cacheCreationInputTokens': json['cache_creation_input_tokens'] == null ? undefined : json['cache_creation_input_tokens'],
        'cacheReadInputTokens': json['cache_read_input_tokens'] == null ? undefined : json['cache_read_input_tokens'],
        'reasoningTokens': json['reasoning_tokens'] == null ? undefined : json['reasoning_tokens'],
        'finishReasons': json['finish_reasons'] == null ? undefined : json['finish_reasons'],
        'timeToFirstTokenMs': json['time_to_first_token_ms'] == null ? undefined : json['time_to_first_token_ms'],
        'modelCost': json['model_cost'] == null ? undefined : MoneyFromJSON(json['model_cost']),
    };
}

export function TraceSpanToJSON(json: any): TraceSpan {
    return TraceSpanToJSONTyped(json, false);
}

export function TraceSpanToJSONTyped(value?: TraceSpan | null, ignoreDiscriminator: boolean = false): any {
    if (value == null) {
        return value;
    }

    return {

        'trace_id': value['traceId'],
        'span_id': value['spanId'],
        'parent_span_id': value['parentSpanId'],
        'name': value['name'],
        'kind': value['kind'],
        'operation': value['operation'],
        'status': value['status'],
        'started_at': value['startedAt'].toISOString(),
        'ended_at': value['endedAt'] == null ? value['endedAt'] : value['endedAt'].toISOString(),
        'duration_ms': value['durationMs'],
        'provider': value['provider'],
        'request_model': value['requestModel'],
        'response_model': value['responseModel'],
        'tool_name': value['toolName'],
        'error_type': value['errorType'],
        'input_tokens': value['inputTokens'],
        'output_tokens': value['outputTokens'],
        'cache_creation_input_tokens': value['cacheCreationInputTokens'],
        'cache_read_input_tokens': value['cacheReadInputTokens'],
        'reasoning_tokens': value['reasoningTokens'],
        'finish_reasons': value['finishReasons'],
        'time_to_first_token_ms': value['timeToFirstTokenMs'],
        'model_cost': MoneyToJSON(value['modelCost']),
    };
}
