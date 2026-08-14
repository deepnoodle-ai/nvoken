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
import type { InvocationContextItem } from './InvocationContextItem.js';
import {
    InvocationContextItemFromJSON,
    InvocationContextItemFromJSONTyped,
    InvocationContextItemToJSON,
    InvocationContextItemToJSONTyped,
} from './InvocationContextItem.js';
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
import type { ResolvedLimits } from './ResolvedLimits.js';
import {
    ResolvedLimitsFromJSON,
    ResolvedLimitsFromJSONTyped,
    ResolvedLimitsToJSON,
    ResolvedLimitsToJSONTyped,
} from './ResolvedLimits.js';
import type { AgentDefinition } from './AgentDefinition.js';
import {
    AgentDefinitionFromJSON,
    AgentDefinitionFromJSONTyped,
    AgentDefinitionToJSON,
    AgentDefinitionToJSONTyped,
} from './AgentDefinition.js';
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
 *
 * @export
 * @interface Invocation
 */
export interface Invocation {
    /**
     * Opaque identifier with the public `inv_` prefix. Treat the body as opaque.
     * @type {string}
     * @memberof Invocation
     */
    id: string;
    /**
     * Opaque identifier with the public `agent_` prefix. Treat the body as opaque.
     * @type {string}
     * @memberof Invocation
     */
    agentId: string;
    /**
     * Opaque identifier with the public `sess_` prefix. Treat the body as opaque.
     * @type {string}
     * @memberof Invocation
     */
    sessionId: string;
    /**
     * Your own label for the end user this turn belongs to. Useful for
     * filtering lists. It is not a security boundary — no request is
     * ever refused because of it, so do not rely on it to keep one
     * user's data away from another.
     *
     * @type {string}
     * @memberof Invocation
     */
    userKey: string | null;
    /**
     * Stable App-owned Agent Definition identifier with the public `def_` prefix. Treat the body as opaque.
     * @type {string}
     * @memberof Invocation
     */
    agentDefinitionId: string;
    /**
     * Immutable Agent Definition revision admitted for this turn.
     * @type {number}
     * @memberof Invocation
     */
    agentDefinitionRevision: number;
    /**
     * The agent definition this turn actually ran with, stored when the turn
     * started and returned exactly as it was. Request headers for remote MCP
     * servers are never stored and never appear here.
     *
     * Present on `GET /v1/invocations/{id}` and on the result. Null in list
     * items, where `agent_definition_id` and `agent_definition_revision`
     * identify it instead.
     *
     * @type {AgentDefinition}
     * @memberof Invocation
     */
    agentDefinition: AgentDefinition | null;
    /**
     * The ordered context payload accepted with this turn, before
     * transcript deduplication. Null when omitted and in Invocation list
     * items. Present on admission, point reads, results, and stream
     * Invocation projections. Context is immutable and order-sensitive
     * for idempotency.
     *
     * @type {Array<InvocationContextItem>}
     * @memberof Invocation
     */
    context: Array<InvocationContextItem> | null;
    /**
     * Only present on the `POST /v1/invocations` response. False when this
     * call created a new turn, true when your idempotency key matched one
     * that already existed and you got that one back.
     *
     * @type {boolean}
     * @memberof Invocation
     */
    deduplicated?: boolean;
    /**
     *
     * @type {InvocationStatus}
     * @memberof Invocation
     */
    status: InvocationStatus;
    /**
     * Why the turn stopped or paused. Present on `completed`,
     * `incomplete`, and `paused`; null on every other status — a failure
     * keeps `error` as the authority. Treat an unrecognized value as an
     * ordinary end.
     *
     * @type {InvocationStopReason}
     * @memberof Invocation
     */
    stopReason: InvocationStopReason | null;
    /**
     * Tenant credit account for an insufficient-credits stop, otherwise null.
     * @type {CreditBlock}
     * @memberof Invocation
     */
    creditBlock: CreditBlock | null;
    /**
     * Execution attempts this Invocation has been claimed for. It
     * increases on every claim, so an attempt increase across a
     * `running → queued → running` transition is the retry signal that
     * status alone cannot give, and it is the durable anchor for
     * discarding provisional output from an earlier attempt. Zero
     * before the first claim.
     *
     * @type {number}
     * @memberof Invocation
     */
    attempt: number;
    /**
     *
     * @type {InvocationFailure}
     * @memberof Invocation
     */
    error: InvocationFailure | null;
    /**
     * One normalized terminal aggregate, not a billing ledger.
     * @type {ModelUsage}
     * @memberof Invocation
     */
    usage: ModelUsage | null;
    /**
     *
     * @type {ModelProvenance}
     * @memberof Invocation
     */
    provenance: ModelProvenance | null;
    /**
     * The object the model produced, already checked against the schema
     * you asked for. Null until the turn finishes successfully, and
     * always null if you did not ask for structured output.
     *
     * @type {{ [key: string]: any; }}
     * @memberof Invocation
     */
    structuredOutput: { [key: string]: any; } | null;
    /**
     *
     * @type {StructuredOutputProvenance}
     * @memberof Invocation
     */
    structuredOutputProvenance: StructuredOutputProvenance | null;
    /**
     * Your own data, stored when the turn was created and returned exactly as
     * you sent it.
     *
     * @type {{ [key: string]: string; }}
     * @memberof Invocation
     */
    metadata: { [key: string]: string; } | null;
    /**
     *
     * @type {ResolvedLimits}
     * @memberof Invocation
     */
    limits: ResolvedLimits;
    /**
     *
     * @type {number}
     * @memberof Invocation
     */
    activeExecutionMs: number;
    /**
     * The deadline currently enforced by the runtime. Null while the
     * Invocation is waiting without an explicit waiting timeout; the
     * explicit waiting deadline while bounded; otherwise the total-time
     * deadline for queued, running, and terminal Invocations.
     *
     * @type {Date}
     * @memberof Invocation
     */
    deadlineAt: Date | null;
    /**
     *
     * @type {Date}
     * @memberof Invocation
     */
    createdAt: Date;
    /**
     *
     * @type {Date}
     * @memberof Invocation
     */
    updatedAt: Date;
    /**
     *
     * @type {Date}
     * @memberof Invocation
     */
    endedAt: Date | null;
    /**
     * Every tool call this turn has made, with its current status.
     * Omitted when the turn has made none.
     *
     * @type {Array<ToolCallSummary>}
     * @memberof Invocation
     */
    toolCalls?: Array<ToolCallSummary>;
}



/**
 * Check if a given object implements the Invocation interface.
 */
export function instanceOfInvocation(value: object): value is Invocation {
    if (!('id' in value) || value['id'] === undefined) return false;
    if (!('agentId' in value) || value['agentId'] === undefined) return false;
    if (!('sessionId' in value) || value['sessionId'] === undefined) return false;
    if (!('userKey' in value) || value['userKey'] === undefined) return false;
    if (!('agentDefinitionId' in value) || value['agentDefinitionId'] === undefined) return false;
    if (!('agentDefinitionRevision' in value) || value['agentDefinitionRevision'] === undefined) return false;
    if (!('agentDefinition' in value) || value['agentDefinition'] === undefined) return false;
    if (!('context' in value) || value['context'] === undefined) return false;
    if (!('status' in value) || value['status'] === undefined) return false;
    if (!('stopReason' in value) || value['stopReason'] === undefined) return false;
    if (!('creditBlock' in value) || value['creditBlock'] === undefined) return false;
    if (!('attempt' in value) || value['attempt'] === undefined) return false;
    if (!('error' in value) || value['error'] === undefined) return false;
    if (!('usage' in value) || value['usage'] === undefined) return false;
    if (!('provenance' in value) || value['provenance'] === undefined) return false;
    if (!('structuredOutput' in value) || value['structuredOutput'] === undefined) return false;
    if (!('structuredOutputProvenance' in value) || value['structuredOutputProvenance'] === undefined) return false;
    if (!('metadata' in value) || value['metadata'] === undefined) return false;
    if (!('limits' in value) || value['limits'] === undefined) return false;
    if (!('activeExecutionMs' in value) || value['activeExecutionMs'] === undefined) return false;
    if (!('deadlineAt' in value) || value['deadlineAt'] === undefined) return false;
    if (!('createdAt' in value) || value['createdAt'] === undefined) return false;
    if (!('updatedAt' in value) || value['updatedAt'] === undefined) return false;
    if (!('endedAt' in value) || value['endedAt'] === undefined) return false;
    return true;
}

export function InvocationFromJSON(json: any): Invocation {
    return InvocationFromJSONTyped(json, false);
}

export function InvocationFromJSONTyped(json: any, ignoreDiscriminator: boolean): Invocation {
    if (json == null) {
        return json;
    }
    return {

        'id': json['id'],
        'agentId': json['agent_id'],
        'sessionId': json['session_id'],
        'userKey': json['user_key'],
        'agentDefinitionId': json['agent_definition_id'],
        'agentDefinitionRevision': json['agent_definition_revision'],
        'agentDefinition': AgentDefinitionFromJSON(json['agent_definition']),
        'context': (json['context'] == null ? null : (json['context'] as Array<any>).map(InvocationContextItemFromJSON)),
        'deduplicated': json['deduplicated'] == null ? undefined : json['deduplicated'],
        'status': InvocationStatusFromJSON(json['status']),
        'stopReason': InvocationStopReasonFromJSON(json['stop_reason']),
        'creditBlock': CreditBlockFromJSON(json['credit_block']),
        'attempt': json['attempt'],
        'error': InvocationFailureFromJSON(json['error']),
        'usage': ModelUsageFromJSON(json['usage']),
        'provenance': ModelProvenanceFromJSON(json['provenance']),
        'structuredOutput': json['structured_output'],
        'structuredOutputProvenance': StructuredOutputProvenanceFromJSON(json['structured_output_provenance']),
        'metadata': json['metadata'],
        'limits': ResolvedLimitsFromJSON(json['limits']),
        'activeExecutionMs': json['active_execution_ms'],
        'deadlineAt': (json['deadline_at'] == null ? null : new Date(json['deadline_at'])),
        'createdAt': (new Date(json['created_at'])),
        'updatedAt': (new Date(json['updated_at'])),
        'endedAt': (json['ended_at'] == null ? null : new Date(json['ended_at'])),
        'toolCalls': json['tool_calls'] == null ? undefined : ((json['tool_calls'] as Array<any>).map(ToolCallSummaryFromJSON)),
    };
}

export function InvocationToJSON(json: any): Invocation {
    return InvocationToJSONTyped(json, false);
}

export function InvocationToJSONTyped(value?: Invocation | null, ignoreDiscriminator: boolean = false): any {
    if (value == null) {
        return value;
    }

    return {

        'id': value['id'],
        'agent_id': value['agentId'],
        'session_id': value['sessionId'],
        'user_key': value['userKey'],
        'agent_definition_id': value['agentDefinitionId'],
        'agent_definition_revision': value['agentDefinitionRevision'],
        'agent_definition': AgentDefinitionToJSON(value['agentDefinition']),
        'context': (value['context'] == null ? null : (value['context'] as Array<any>).map(InvocationContextItemToJSON)),
        'deduplicated': value['deduplicated'],
        'status': InvocationStatusToJSON(value['status']),
        'stop_reason': InvocationStopReasonToJSON(value['stopReason']),
        'credit_block': CreditBlockToJSON(value['creditBlock']),
        'attempt': value['attempt'],
        'error': InvocationFailureToJSON(value['error']),
        'usage': ModelUsageToJSON(value['usage']),
        'provenance': ModelProvenanceToJSON(value['provenance']),
        'structured_output': value['structuredOutput'],
        'structured_output_provenance': StructuredOutputProvenanceToJSON(value['structuredOutputProvenance']),
        'metadata': value['metadata'],
        'limits': ResolvedLimitsToJSON(value['limits']),
        'active_execution_ms': value['activeExecutionMs'],
        'deadline_at': value['deadlineAt'] == null ? value['deadlineAt'] : value['deadlineAt'].toISOString(),
        'created_at': value['createdAt'].toISOString(),
        'updated_at': value['updatedAt'].toISOString(),
        'ended_at': value['endedAt'] == null ? value['endedAt'] : value['endedAt'].toISOString(),
        'tool_calls': value['toolCalls'] == null ? undefined : ((value['toolCalls'] as Array<any>).map(ToolCallSummaryToJSON)),
    };
}
