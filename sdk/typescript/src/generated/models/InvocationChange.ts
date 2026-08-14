/* tslint:disable */
/* eslint-disable */
/**
 * nvoken API
 * nvoken runs agent turns for you. You describe a turn — an Agent Definition and some input — and nvoken queues it, runs it in the background, keeps running it across restarts and failures, and lets you either watch it live or come back for the result later.  Your application stays in charge of what your agents are and when they run. nvoken owns the conversation: it stores the messages, tracks the state of every turn, and handles talking to the model providers.  ## Getting started  `POST /v1/invocations` starts a turn and returns a `202` right away. From there:  - Follow it live with `GET /v1/invocations/{invocation_id}/stream`, or   read `GET /v1/invocations/{invocation_id}/result` whenever you want the   finished answer. Disconnecting never cancels anything. - If your agent uses tools you run yourself, the turn stops with status   `waiting` and lists what it needs. Run them, post the results to   `/tool-results`, and the turn continues where it left off. - Sessions carry conversation history from one turn to the next. They   last until you delete them, or until a retention window you set runs   out.  Also here: tools nvoken calls back to over HTTPS, remote MCP servers, structured output validated against your JSON Schema, reusable Agent Definitions, image and document input, your own model provider keys, and spending limits.  ## Authorization  Machine credentials use `nvk_…` bearer API keys. A configured trusted console may also present a short-lived Ed25519 issuer token; this is an authentication presentation only and does not create a user identity model in nvoken.  Browser-direct callers use narrow JWTs, exact-origin CORS, client-safe projections, and App/tenant/user admission limits. A host may mint client tokens from its own Ed25519 key. An App that explicitly enables anonymous access may instead let nvoken mint a short-lived access token and renewable visitor token from one allowed public origin.  - App-scoped Runtime credentials may call every Runtime operation and GET /v1/identity. - App-scoped Viewer credentials may call Runtime reads and GET /v1/identity. - App-scoped Operator credentials may call every Runtime operation, GET /v1/identity, and all credential lifecycle operations. - Org-scoped Viewer and Operator credentials are management and reporting   identities only. They resolve no tenant and cannot perform Runtime   operations; Operators can register and manage Apps and their App   credentials, while Viewers have read-only access.  Tenant, Session, operation, and expiry constraints only narrow these grants.  ## Familiar names  Where a name already means something in other agent APIs, nvoken uses it the same way rather than inventing its own. `metadata` follows OpenAI\'s limits of 16 keys, 64-character names, and 512-byte values. `output_text` is the assistant\'s text joined into one string. `reasoning.effort` takes `low`, `medium`, `high`, `xhigh`, and `max`. `stop_reason: end_turn`, status `running`, and the `commentary` and `final_answer` message phases are the same idea you have seen elsewhere. If you have integrated another agent API, these should need no translation.  ## You can always read back what applied  Anything nvoken decides on your behalf is readable on the resource that used it. You never have to work out what happened by combining the request you sent with your own assumptions about nvoken\'s defaults — just read the resource.  A turn reports the `limits` it is really running under, after defaults and minimums; the `agent_definition` it ran with, exactly as stored; and `provenance`, which records what actually served the request. A Session reports its compaction (summarization) policy with `auto` already resolved to a real number and a real model, and its retention window as accepted. New settings will work the same way: a default you cannot read back is a setting only the server knows about.  ## Streaming  A turn and an Invocation are the same thing. The endpoint summaries say turn; the schemas say Invocation.  Two streams carry the same frames. `GET /v1/invocations/{invocation_id}/stream` follows one turn and ends when that turn settles. `GET /v1/sessions/{session_id}/transcript/stream` follows every turn in a Session, and is the surface to use for a conversation. `POST /v1/invocations` with `Accept: text/event-stream` admits and streams one turn inline.  ### Saved frames and live frames  Every frame is one or the other, and the difference decides what you may store. A saved frame carries an SSE `id`. That ID is your resume position and the only value you need to keep. A live frame carries no `id`: it is a preview or a control signal, it is never stored, and it is never replayed.  The Invocation stream\'s saved frames are `invocation.accepted`, `invocation.update`, and `invocation.result`. The Session stream\'s only saved frame is `transcript.update`. Every other frame on either stream is live.  ### Resuming and finishing  The resume position has one name: `cursor`. It is the field on a durable frame and the query parameter that resumes a stream. Server-Sent Events mirrors it onto the `id:` line and accepts the `Last-Event-ID` header in the parameter\'s place, because a faithful SSE binding must; those are the binding\'s mechanics, not two more names for the value, and another binding would carry the same one name. `cursor` wins when a request supplies both. Cursors are Session-scoped on both streams, so a position taken from one stream resumes the other.  Reconnecting to a turn that has already settled always yields `invocation.result` followed by `stream.end` with reason `terminal`, at any cursor. Both are valid signals that a turn is over, and a client may exit on either.  `invocation.accepted` is emitted only by the inline `POST` path. The `GET` stream never sends it, so a client that admits separately never sees it. The nvoken SDKs synthesize an equivalent locally so their callers see the same first event either way.  An `invocation.update` never carries a terminal status. Terminal state arrives as `invocation.result` and nowhere else on that stream. The `invocation` it carries is re-read when the frame is written, so it is current state with a resume position attached rather than a snapshot taken at the cursor.  ### Previews  `output_text.delta` and `thinking.delta` preview one model iteration. Their identity is `(invocation_id, attempt, iteration, content_index)`. Accumulate by that tuple, and discard everything provisional when `attempt` increases, when `stream.resync` arrives, when the saved message lands, and when the turn reaches a terminal status. One model iteration produces exactly one saved assistant message, so previews sharing an `(invocation_id, attempt, iteration)` build one message. Never store preview text as a message, and never use it to decide whether a turn succeeded.  `attempt`, `iteration`, `revision`, and `sequence` count from 1. `content_index` counts from 0.  ### Compatibility  Stream events may gain fields over time. Ignore fields you do not recognize rather than refusing the frame. New enum values may appear too. Treat an unknown `stream.resync` reason as `live_delivery_gap` and discard your previews. Treat an unknown `stream.end` reason as `rotate` and reconnect with your last saved `id`. Reconnecting is always safe: a turn that has settled re-yields its result.  ### Transport  The protocol is the frames and their durability rules. Server-Sent Events is how they travel today and the only binding. Three mechanics belong to SSE itself: the `id:` line, the `retry:` opener, and comment keepalives. Everything else, resumption included, lives in the frames.  ### What the stream carries  Frames carry structure and text. Images and documents travel as descriptors, with media type, size in bytes, and a `sha256:` digest, never as inline bytes. Frame sizes stay bounded by text no matter what a turn produced.
 *
 * The version of the OpenAPI document: 0.1.0
 *
 *
 * NOTE: This class is auto generated by OpenAPI Generator (https://openapi-generator.tech).
 * https://openapi-generator.tech
 * Do not edit the class manually.
 */

import { mapValues } from '../runtime.js';
import type { InvocationFailure } from './InvocationFailure.js';
import {
    InvocationFailureFromJSON,
    InvocationFailureFromJSONTyped,
    InvocationFailureToJSON,
    InvocationFailureToJSONTyped,
} from './InvocationFailure.js';
import type { InvocationStatus } from './InvocationStatus.js';
import {
    InvocationStatusFromJSON,
    InvocationStatusFromJSONTyped,
    InvocationStatusToJSON,
    InvocationStatusToJSONTyped,
} from './InvocationStatus.js';
import type { ModelUsage } from './ModelUsage.js';
import {
    ModelUsageFromJSON,
    ModelUsageFromJSONTyped,
    ModelUsageToJSON,
    ModelUsageToJSONTyped,
} from './ModelUsage.js';
import type { ToolCallSummary } from './ToolCallSummary.js';
import {
    ToolCallSummaryFromJSON,
    ToolCallSummaryFromJSONTyped,
    ToolCallSummaryToJSON,
    ToolCallSummaryToJSONTyped,
} from './ToolCallSummary.js';
import type { ModelProvenance } from './ModelProvenance.js';
import {
    ModelProvenanceFromJSON,
    ModelProvenanceFromJSONTyped,
    ModelProvenanceToJSON,
    ModelProvenanceToJSONTyped,
} from './ModelProvenance.js';
import type { InvocationStopReason } from './InvocationStopReason.js';
import {
    InvocationStopReasonFromJSON,
    InvocationStopReasonFromJSONTyped,
    InvocationStopReasonToJSON,
    InvocationStopReasonToJSONTyped,
} from './InvocationStopReason.js';
import type { CreditBlock } from './CreditBlock.js';
import {
    CreditBlockFromJSON,
    CreditBlockFromJSONTyped,
    CreditBlockToJSON,
    CreditBlockToJSONTyped,
} from './CreditBlock.js';
import type { StructuredOutputProvenance } from './StructuredOutputProvenance.js';
import {
    StructuredOutputProvenanceFromJSON,
    StructuredOutputProvenanceFromJSONTyped,
    StructuredOutputProvenanceToJSON,
    StructuredOutputProvenanceToJSONTyped,
} from './StructuredOutputProvenance.js';

/**
 * One lifecycle step of one turn, as the Session transcript stream and
 * the JSON transcript deliver it. Order changes by `revision` and fold
 * to the highest one to get current state.
 *
 * A change carries what a turn's own projection carries about where it
 * stands: `stop_reason` for a turn that ended, `credit_block` for one
 * paused on spend, and `tool_calls` for what its tools are doing, with
 * `arguments` on the ones waiting for you to run them. You never need a
 * second request to find out why a turn is not moving.
 *
 * @export
 * @interface InvocationChange
 */
export interface InvocationChange {
    /**
     * Opaque identifier with the public `inv_` prefix. Treat the body as opaque.
     * @type {string}
     * @memberof InvocationChange
     */
    invocationId: string;
    /**
     *
     * @type {number}
     * @memberof InvocationChange
     */
    revision: number;
    /**
     *
     * @type {InvocationStatus}
     * @memberof InvocationChange
     */
    status: InvocationStatus;
    /**
     * Why the turn stopped. Present once it has stopped, so an
     * `incomplete` change names the limit that ran out.
     *
     * @type {InvocationStopReason}
     * @memberof InvocationChange
     */
    stopReason?: InvocationStopReason | null;
    /**
     * Present while the turn is paused on spending capacity.
     * @type {CreditBlock}
     * @memberof InvocationChange
     */
    creditBlock?: CreditBlock | null;
    /**
     * Every tool call this turn has made, with its current status.
     * Omitted when the turn has made none. A tool call changing state is
     * itself a change, so a failed or long-running call is visible
     * before its result message arrives, and a client that was
     * disconnected sees it on replay.
     *
     * @type {Array<ToolCallSummary>}
     * @memberof InvocationChange
     */
    toolCalls?: Array<ToolCallSummary>;
    /**
     *
     * @type {number}
     * @memberof InvocationChange
     */
    throughMessageSequence: number | null;
    /**
     *
     * @type {InvocationFailure}
     * @memberof InvocationChange
     */
    error: InvocationFailure | null;
    /**
     *
     * @type {ModelUsage}
     * @memberof InvocationChange
     */
    usage: ModelUsage | null;
    /**
     *
     * @type {ModelProvenance}
     * @memberof InvocationChange
     */
    provenance: ModelProvenance | null;
    /**
     *
     * @type {{ [key: string]: any; }}
     * @memberof InvocationChange
     */
    structuredOutput: { [key: string]: any; } | null;
    /**
     *
     * @type {StructuredOutputProvenance}
     * @memberof InvocationChange
     */
    structuredOutputProvenance: StructuredOutputProvenance | null;
    /**
     *
     * @type {Date}
     * @memberof InvocationChange
     */
    occurredAt: Date;
}



/**
 * Check if a given object implements the InvocationChange interface.
 */
export function instanceOfInvocationChange(value: object): value is InvocationChange {
    if (!('invocationId' in value) || value['invocationId'] === undefined) return false;
    if (!('revision' in value) || value['revision'] === undefined) return false;
    if (!('status' in value) || value['status'] === undefined) return false;
    if (!('throughMessageSequence' in value) || value['throughMessageSequence'] === undefined) return false;
    if (!('error' in value) || value['error'] === undefined) return false;
    if (!('usage' in value) || value['usage'] === undefined) return false;
    if (!('provenance' in value) || value['provenance'] === undefined) return false;
    if (!('structuredOutput' in value) || value['structuredOutput'] === undefined) return false;
    if (!('structuredOutputProvenance' in value) || value['structuredOutputProvenance'] === undefined) return false;
    if (!('occurredAt' in value) || value['occurredAt'] === undefined) return false;
    return true;
}

export function InvocationChangeFromJSON(json: any): InvocationChange {
    return InvocationChangeFromJSONTyped(json, false);
}

export function InvocationChangeFromJSONTyped(json: any, ignoreDiscriminator: boolean): InvocationChange {
    if (json == null) {
        return json;
    }
    return {

        'invocationId': json['invocation_id'],
        'revision': json['revision'],
        'status': InvocationStatusFromJSON(json['status']),
        'stopReason': json['stop_reason'] == null ? undefined : InvocationStopReasonFromJSON(json['stop_reason']),
        'creditBlock': json['credit_block'] == null ? undefined : CreditBlockFromJSON(json['credit_block']),
        'toolCalls': json['tool_calls'] == null ? undefined : ((json['tool_calls'] as Array<any>).map(ToolCallSummaryFromJSON)),
        'throughMessageSequence': json['through_message_sequence'],
        'error': InvocationFailureFromJSON(json['error']),
        'usage': ModelUsageFromJSON(json['usage']),
        'provenance': ModelProvenanceFromJSON(json['provenance']),
        'structuredOutput': json['structured_output'],
        'structuredOutputProvenance': StructuredOutputProvenanceFromJSON(json['structured_output_provenance']),
        'occurredAt': (new Date(json['occurred_at'])),
    };
}

export function InvocationChangeToJSON(json: any): InvocationChange {
    return InvocationChangeToJSONTyped(json, false);
}

export function InvocationChangeToJSONTyped(value?: InvocationChange | null, ignoreDiscriminator: boolean = false): any {
    if (value == null) {
        return value;
    }

    return {

        'invocation_id': value['invocationId'],
        'revision': value['revision'],
        'status': InvocationStatusToJSON(value['status']),
        'stop_reason': InvocationStopReasonToJSON(value['stopReason']),
        'credit_block': CreditBlockToJSON(value['creditBlock']),
        'tool_calls': value['toolCalls'] == null ? undefined : ((value['toolCalls'] as Array<any>).map(ToolCallSummaryToJSON)),
        'through_message_sequence': value['throughMessageSequence'],
        'error': InvocationFailureToJSON(value['error']),
        'usage': ModelUsageToJSON(value['usage']),
        'provenance': ModelProvenanceToJSON(value['provenance']),
        'structured_output': value['structuredOutput'],
        'structured_output_provenance': StructuredOutputProvenanceToJSON(value['structuredOutputProvenance']),
        'occurred_at': value['occurredAt'].toISOString(),
    };
}
