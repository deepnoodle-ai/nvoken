/* tslint:disable */
/* eslint-disable */
/**
 * nvoken API
 * nvoken runs agent turns for you. You describe a turn — an Agent Definition and some input — and nvoken queues it, runs it in the background, keeps running it across restarts and failures, and lets you either watch it live or come back for the result later.  Your application stays in charge of what your agents are and when they run. nvoken owns the conversation: it stores the messages, tracks the state of every turn, and handles talking to the model providers.  ## Getting started  `POST /v1/invocations` starts a turn and returns a `202` right away. From there:  - Follow it live with `GET /v1/sessions/{session_id}/stream`, or read   `GET /v1/invocations/{invocation_id}/result` whenever you want the   finished answer. Disconnecting never cancels anything. - If your agent uses tools you run yourself, the turn stops with status   `waiting` and lists what it needs. Run them, post the results to   `/tool-results`, and the turn continues where it left off. - Sessions carry conversation history from one turn to the next. They   last until you delete them, or until a retention window you set runs   out.  Also here: tools nvoken calls back to over HTTPS, remote MCP servers, structured output validated against your JSON Schema, reusable Agent Definitions, image and document input, your own model provider keys, and spending limits.  ## Authorization  Machine credentials use `nvk_…` bearer API keys. A configured trusted console may also present a short-lived Ed25519 issuer token; this is an authentication presentation only and does not create a user identity model in nvoken.  Browser-direct callers use narrow JWTs, exact-origin CORS, and App/tenant/user admission limits. A host may mint client tokens from its own Ed25519 key. An App that explicitly enables anonymous access may instead let nvoken mint a short-lived access token and renewable visitor token from one allowed public origin.  A browser grant sees less, and it sees it in the same shape. There is one schema per resource, and the fields a browser may not see are simply omitted from its responses, which is why those fields are not required. There are no parallel browser schemas and no response unions, so every payload decodes against one type and nothing about the shape has to be inferred from the credential that fetched it. `GET /v1/identity` reports which kind of caller you are, under `authentication.method`.  - App-scoped Runtime credentials may call every Runtime operation and GET /v1/identity. - App-scoped Viewer credentials may call Runtime reads and GET /v1/identity. - App-scoped Operator credentials may call every Runtime operation, GET /v1/identity, and all credential lifecycle operations. - Org-scoped Viewer and Operator credentials are management and reporting   identities only. They resolve no tenant and cannot perform Runtime   operations; Operators can register and manage Apps and their App   credentials, while Viewers have read-only access.  Tenant, Session, operation, and expiry constraints only narrow these grants.  ## Familiar names  Where a name already means something in other agent APIs, nvoken uses it the same way rather than inventing its own. `metadata` follows OpenAI\'s limits of 16 keys, 64-character names, and 512-byte values. `output_text` is the assistant\'s text joined into one string. `reasoning.effort` takes `low`, `medium`, `high`, `xhigh`, and `max`. `stop_reason: end_turn`, status `running`, and the `commentary` and `final_answer` message phases are the same idea you have seen elsewhere. If you have integrated another agent API, these should need no translation.  ## You can always read back what applied  Anything nvoken decides on your behalf is readable on the resource that used it. You never have to work out what happened by combining the request you sent with your own assumptions about nvoken\'s defaults — just read the resource.  A turn reports the `limits` it is really running under, after defaults and minimums; the `agent_definition` it ran with, exactly as stored; and `provenance`, which records what actually served the request. A Session reports its compaction (summarization) policy with `auto` already resolved to a real number and a real model, and its retention window as accepted. New settings will work the same way: a default you cannot read back is a setting only the server knows about.  ## Streaming  A turn and an Invocation are the same thing. The endpoint summaries say turn; the schemas say Invocation.  There is one stream: `GET /v1/sessions/{session_id}/stream`. It carries every turn in the Session, which is what a conversation needs. Add `invocation_id` to narrow it to one turn. That is a filter on one log, not a second endpoint, because cursors were always Session-scoped and a position from a filtered read resumes an unfiltered one.  Admission is separate from streaming. `POST /v1/invocations` returns the turn, and that response is your acknowledgment; then you open the stream. A dropped connection costs you nothing, because the turn already exists and you already hold its ID.  ### Saved frames and live frames  Every frame is one or the other, and the difference decides what you may store. A saved frame carries an SSE `id`. That ID is your resume position and the only value you need to keep. A live frame carries no `id`: it is a preview or a control signal, it is never stored, and it is never replayed.  `transcript.update` is the one saved frame. `message.delta`, `stream.resync`, and `stream.end` are live.  ### Folding a transcript.update  It carries `cursor`, `messages`, and `invocation_changes`. Messages append by `sequence` and are never sent twice. Changes are an append log keyed by `(invocation_id, revision)`; fold to the highest revision for current state. Within one frame apply messages before changes, so a turn is never marked settled before its final message exists.  ### Resuming and finishing  The resume position has one name: `cursor`. It is the field on a durable frame and the query parameter that resumes a stream. Server-Sent Events mirrors it onto the `id:` line and accepts the `Last-Event-ID` header in the parameter\'s place, because a faithful SSE binding must; those are the binding\'s mechanics, not two more names for the value, and another binding would carry the same one name. `cursor` wins when a request supplies both.  **A turn is over when a change for it carries a terminal status.** That is the terminal signal, and there is no other. It is saved, so it replays on reconnect like every other change, at any cursor. Read `GET /v1/invocations/{invocation_id}` if you want the composed result.  `stream.end` never speaks about turns. It says this connection is closing and nothing more, so a client that has not seen its terminal change reconnects and keeps reading.  ### The stream stays open  An unfiltered stream is a subscription. It stays open while the Session is idle, keepalives and all, and a turn started later by anyone appears on it. You do not poll. A server may still reclaim an idle connection, and when it does it says `stream.end` with reason `idle`, which means reconnect when you next need to read rather than immediately.  A filtered stream closes once it has delivered that turn\'s terminal change. A client following the exit rule has already left.  ### Previews  `message.delta` previews a message the model is writing. `kind` says what kind of content it is and `delta` carries the fragment, for every kind. Accumulate by `(message_id, content_index)`, and discard everything provisional when `attempt` increases, when `stream.resync` arrives, when the saved message with that ID lands, and when the turn\'s change carries a terminal status. Never store preview text as a message, and never use it to decide whether a turn succeeded.  `attempt`, `revision`, and `sequence` count from 1. `content_index` counts from 0.  ### Compatibility  Stream events may gain fields over time. Ignore fields you do not recognize rather than refusing the frame. New enum values may appear too. Treat an unknown `message.delta` kind as content you do not render. Treat an unknown `stream.resync` reason as `live_delivery_gap` and discard your previews. Treat an unknown `stream.end` reason as `rotate` and reconnect with your last saved `id`. Reconnecting is always safe.  ### Transport  The protocol is the frames and their durability rules. Server-Sent Events is how they travel today and the only binding. Three mechanics belong to SSE itself: the `id:` line, the `retry:` opener, and comment keepalives. Everything else, resumption included, lives in the frames.  ### What the stream carries  Frames carry structure and text. Images and documents travel as descriptors, with media type, size in bytes, and a `sha256:` digest, never as inline bytes. Frame sizes stay bounded by text no matter what a turn produced.
 *
 * The version of the OpenAPI document: 0.1.0
 *
 *
 * NOTE: This class is auto generated by OpenAPI Generator (https://openapi-generator.tech).
 * https://openapi-generator.tech
 * Do not edit the class manually.
 */

import { mapValues } from '../runtime.js';
import type { SessionForkLineage } from './SessionForkLineage.js';
import {
    SessionForkLineageFromJSON,
    SessionForkLineageFromJSONTyped,
    SessionForkLineageToJSON,
    SessionForkLineageToJSONTyped,
} from './SessionForkLineage.js';
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
import type { SessionContext } from './SessionContext.js';
import {
    SessionContextFromJSON,
    SessionContextFromJSONTyped,
    SessionContextToJSON,
    SessionContextToJSONTyped,
} from './SessionContext.js';
import type { CompactionPolicy } from './CompactionPolicy.js';
import {
    CompactionPolicyFromJSON,
    CompactionPolicyFromJSONTyped,
    CompactionPolicyToJSON,
    CompactionPolicyToJSONTyped,
} from './CompactionPolicy.js';
import type { CreditBlock } from './CreditBlock.js';
import {
    CreditBlockFromJSON,
    CreditBlockFromJSONTyped,
    CreditBlockToJSON,
    CreditBlockToJSONTyped,
} from './CreditBlock.js';
import type { RetentionPolicy } from './RetentionPolicy.js';
import {
    RetentionPolicyFromJSON,
    RetentionPolicyFromJSONTyped,
    RetentionPolicyToJSON,
    RetentionPolicyToJSONTyped,
} from './RetentionPolicy.js';

/**
 * One conversation.
 *
 * Some fields are audience-restricted: they are present for a machine
 * credential and omitted for a browser grant, which is why they are not
 * required. Omission is the whole mechanism, so one schema decodes every
 * response and nothing has to be guessed from the payload. The omitted
 * set here is `agent_id`, `tenant_key`, `session_key`, `user_key`, `forked_from`,
 * `compaction`, `retention`, `expires_at`, `metadata`, `credit_block`,
 * `context`, and `usage`.
 *
 * @export
 * @interface Session
 */
export interface Session {
    /**
     * Opaque identifier with the public `sess_` prefix. Treat the body as opaque.
     * @type {string}
     * @memberof Session
     */
    id: string;
    /**
     * Null only while a Session created ahead of time has not run a turn yet.
     * The first turn binds the Agent, and after that it never changes.
     *
     * @type {string}
     * @memberof Session
     */
    agentId?: string | null;
    /**
     * Immutable effective tenant partition reference.
     * @type {string}
     * @memberof Session
     */
    tenantKey?: string | null;
    /**
     *
     * @type {string}
     * @memberof Session
     */
    sessionKey?: string | null;
    /**
     * Host-owned end-user label recorded when this Session was opened.
     * Filtering only; not an isolation boundary.
     *
     * @type {string}
     * @memberof Session
     */
    userKey?: string | null;
    /**
     * Durable source prefix lineage, or null for an original Session.
     * @type {SessionForkLineage}
     * @memberof Session
     */
    forkedFrom?: SessionForkLineage | null;
    /**
     * The automatic compaction policy this Session actually applies, or
     * null when it compacts nothing. It is echoed resolved: a request
     * that asked for `trigger_tokens: auto` reads back the integer that
     * resolved to, and a request that named no model reads back the
     * model the policy bound. Nothing here is ever the unresolved
     * request.
     *
     * @type {CompactionPolicy}
     * @memberof Session
     */
    compaction?: CompactionPolicy | null;
    /**
     * The idle retention window this Session was created with, or null
     * when it is retained until deleted explicitly. A window outside the
     * supported range is refused at creation rather than clamped, so
     * what is read back is always exactly what applies.
     *
     * @type {RetentionPolicy}
     * @memberof Session
     */
    retention?: RetentionPolicy | null;
    /**
     * When nvoken may automatically delete this Session, or null if it
     * has no retention window. The date moves forward every time a turn
     * starts and every time one finishes, so a Session in active use
     * never reaches it.
     *
     * @type {Date}
     * @memberof Session
     */
    expiresAt?: Date | null;
    /**
     * Host correlation data, returned verbatim. Set at creation through
     * `session_options.metadata` and changed with
     * `PATCH /v1/sessions/{session_id}`.
     *
     * @type {{ [key: string]: string; }}
     * @memberof Session
     */
    metadata?: { [key: string]: string; } | null;
    /**
     * The queued, running, waiting, or paused Invocation, if one exists.
     * @type {string}
     * @memberof Session
     */
    activeInvocationId: string | null;
    /**
     * Status of active_invocation_id; null exactly when that ID is null.
     * @type {SessionActiveInvocationStatusEnum}
     * @memberof Session
     */
    activeInvocationStatus: SessionActiveInvocationStatusEnum | null;
    /**
     * Tenant credit account blocking the active paused Invocation, otherwise null.
     * @type {CreditBlock}
     * @memberof Session
     */
    creditBlock?: CreditBlock | null;
    /**
     * Read-time retained-context estimate and the model window it is
     * measured against. Null until the Session has either a compaction
     * model or an Invocation primary model. The object remains present
     * for an uncataloged model, with `context_window_tokens: null`.
     *
     * @type {SessionContext}
     * @memberof Session
     */
    context?: SessionContext | null;
    /**
     * Read-time sum of this Session's non-null Invocation usage and
     * committed private compaction usage. Null until either exists. This
     * normalized estimate is not a billing ledger.
     *
     * @type {ModelUsage}
     * @memberof Session
     */
    usage?: ModelUsage | null;
    /**
     *
     * @type {Date}
     * @memberof Session
     */
    createdAt: Date;
    /**
     *
     * @type {Date}
     * @memberof Session
     */
    updatedAt: Date;
    /**
     * Tool calls of the active Invocation, with their current status.
     * The ones still yours to run carry `arguments` and `deadline_at`.
     * Omitted when no Invocation is active.
     *
     * @type {Array<ToolCallSummary>}
     * @memberof Session
     */
    toolCalls?: Array<ToolCallSummary>;
}


/**
 * @export
 */
export const SessionActiveInvocationStatusEnum = {
    Queued: 'queued',
    Running: 'running',
    Waiting: 'waiting',
    Paused: 'paused'
} as const;
export type SessionActiveInvocationStatusEnum = typeof SessionActiveInvocationStatusEnum[keyof typeof SessionActiveInvocationStatusEnum];


/**
 * Check if a given object implements the Session interface.
 */
export function instanceOfSession(value: object): value is Session {
    if (!('id' in value) || value['id'] === undefined) return false;
    if (!('activeInvocationId' in value) || value['activeInvocationId'] === undefined) return false;
    if (!('activeInvocationStatus' in value) || value['activeInvocationStatus'] === undefined) return false;
    if (!('createdAt' in value) || value['createdAt'] === undefined) return false;
    if (!('updatedAt' in value) || value['updatedAt'] === undefined) return false;
    return true;
}

export function SessionFromJSON(json: any): Session {
    return SessionFromJSONTyped(json, false);
}

export function SessionFromJSONTyped(json: any, ignoreDiscriminator: boolean): Session {
    if (json == null) {
        return json;
    }
    return {

        'id': json['id'],
        'agentId': json['agent_id'] == null ? undefined : json['agent_id'],
        'tenantKey': json['tenant_key'] == null ? undefined : json['tenant_key'],
        'sessionKey': json['session_key'] == null ? undefined : json['session_key'],
        'userKey': json['user_key'] == null ? undefined : json['user_key'],
        'forkedFrom': json['forked_from'] == null ? undefined : SessionForkLineageFromJSON(json['forked_from']),
        'compaction': json['compaction'] == null ? undefined : CompactionPolicyFromJSON(json['compaction']),
        'retention': json['retention'] == null ? undefined : RetentionPolicyFromJSON(json['retention']),
        'expiresAt': json['expires_at'] == null ? undefined : (new Date(json['expires_at'])),
        'metadata': json['metadata'] == null ? undefined : json['metadata'],
        'activeInvocationId': json['active_invocation_id'],
        'activeInvocationStatus': json['active_invocation_status'],
        'creditBlock': json['credit_block'] == null ? undefined : CreditBlockFromJSON(json['credit_block']),
        'context': json['context'] == null ? undefined : SessionContextFromJSON(json['context']),
        'usage': json['usage'] == null ? undefined : ModelUsageFromJSON(json['usage']),
        'createdAt': (new Date(json['created_at'])),
        'updatedAt': (new Date(json['updated_at'])),
        'toolCalls': json['tool_calls'] == null ? undefined : ((json['tool_calls'] as Array<any>).map(ToolCallSummaryFromJSON)),
    };
}

export function SessionToJSON(json: any): Session {
    return SessionToJSONTyped(json, false);
}

export function SessionToJSONTyped(value?: Session | null, ignoreDiscriminator: boolean = false): any {
    if (value == null) {
        return value;
    }

    return {

        'id': value['id'],
        'agent_id': value['agentId'],
        'tenant_key': value['tenantKey'],
        'session_key': value['sessionKey'],
        'user_key': value['userKey'],
        'forked_from': SessionForkLineageToJSON(value['forkedFrom']),
        'compaction': CompactionPolicyToJSON(value['compaction']),
        'retention': RetentionPolicyToJSON(value['retention']),
        'expires_at': value['expiresAt'] == null ? value['expiresAt'] : value['expiresAt'].toISOString(),
        'metadata': value['metadata'],
        'active_invocation_id': value['activeInvocationId'],
        'active_invocation_status': value['activeInvocationStatus'],
        'credit_block': CreditBlockToJSON(value['creditBlock']),
        'context': SessionContextToJSON(value['context']),
        'usage': ModelUsageToJSON(value['usage']),
        'created_at': value['createdAt'].toISOString(),
        'updated_at': value['updatedAt'].toISOString(),
        'tool_calls': value['toolCalls'] == null ? undefined : ((value['toolCalls'] as Array<any>).map(ToolCallSummaryToJSON)),
    };
}
