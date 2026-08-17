/* tslint:disable */
/* eslint-disable */
/**
 * nvoken API
 * nvoken runs agent turns for you. You describe a turn — an Agent Definition and some input — and nvoken queues it, runs it in the background, keeps running it across restarts and failures, and lets you either watch it live or come back for the result later.  One turn is one Invocation. `Invocation` is the name in every path, field, schema, and error, and it is the word to reach for when you need to be exact. `turn` is the English for the same thing, and this document uses it in explanatory prose. They are not two resources, so there is no relationship between them for you to model.  Your application stays in charge of what your agents are and when they run. nvoken owns the conversation: it stores the messages, tracks the state of every turn, and handles talking to the model providers.  ## Getting started  `POST /v1/invocations` starts a turn and returns a `202` right away. From there:  - Follow it live with `GET /v1/sessions/{session_id}/stream`, or read   `GET /v1/invocations/{invocation_id}/result` whenever you want the   finished answer. Disconnecting never cancels anything. - If your agent uses tools you run yourself, the turn stops with status   `waiting` and lists what it needs. Run them, post the results to   `/tool-results`, and the turn continues where it left off. - Sessions carry conversation history from one turn to the next. They   last until you delete them, or until a retention window you set runs   out.  Also here: tools nvoken calls back to over HTTPS, remote MCP servers, structured output validated against your JSON Schema, reusable Agent Definitions, image and document input, your own model provider keys, and spending limits.  ## Authorization  Machine credentials use `nvk_…` bearer API keys. A configured trusted console may also present a short-lived Ed25519 issuer token; this is an authentication presentation only and does not create a user identity model in nvoken.  Browser-direct callers use narrow JWTs, exact-origin CORS, and App/tenant/user admission limits. A host may mint client tokens from its own Ed25519 key. An App that explicitly enables anonymous access may instead let nvoken mint a short-lived access token and renewable visitor token from one allowed public origin.  A browser grant sees less, and it sees it in the same shape. There is one schema per resource, and the fields a browser may not see are simply omitted from its responses, which is why those fields are not required. There are no parallel browser schemas and no response unions, so every payload decodes against one type and nothing about the shape has to be inferred from the credential that fetched it. `GET /v1/identity` reports which kind of caller you are, under `authentication.method`.  - App-scoped Runtime credentials may call every Runtime operation and GET /v1/identity. - App-scoped Viewer credentials may call Runtime reads and GET /v1/identity. - App-scoped Operator credentials may call every Runtime operation, GET /v1/identity, and all credential lifecycle operations. - Org-scoped Viewer and Operator credentials are management and reporting   identities only. They resolve no tenant and cannot perform Runtime   operations; Operators can register and manage Apps and their App   credentials, while Viewers have read-only access.  Tenant, Session, operation, and expiry constraints only narrow these grants.  ## Acting for one tenant or one end user  An app-wide credential can reach every tenant in its App, which means an id that arrives from the wrong place — a stale link, a mixed-up webhook, a tampered form field — is an id it can act on. The usual answer is for the host to re-read the resource and compare its `tenant_key` and `user_key` before every call. Say it once instead:  ``` X-Nvoken-Tenant-Key: acme X-Nvoken-User-Key: user-7c1f ```  Both headers are accepted on every request and narrow that one request to the scope they name. Anything outside it is reported as `not_found`, so a Session or Invocation belonging to another tenant or another end user cannot be read, cancelled, interrupted, forked, answered, or erased — and you learn nothing about whether the id exists. Writes take the same scope: an omitted `tenant_key` or `user_key` in the body inherits it, and one naming somebody else is refused.  Two rules keep them honest. They may only narrow, so a credential already bound to one tenant refuses a header naming another with `forbidden` rather than silently returning nothing. And a record with no `user_key` at all is outside every `X-Nvoken-User-Key` assertion rather than inside all of them, because a Session nobody claimed is not one you can claim by asserting.  Each header takes 1 to 255 characters and may appear once. A blank, oversized, or repeated header is `invalid_request` rather than ignored: an assertion nobody notices dropping is worse than no assertion at all.  A browser token already carries its tenant and its end user, so it neither needs these headers nor is allowed to send them.  ## Familiar names  Where a name already means something in other agent APIs, nvoken uses it the same way rather than inventing its own. `metadata` follows OpenAI\'s limits of 16 keys, 64-character names, and 512-byte values. `output_text` is the assistant\'s text joined into one string. `reasoning.effort` takes `low`, `medium`, `high`, `xhigh`, and `max`. `stop_reason: end_turn`, status `running`, and the `commentary` and `final_answer` message phases are the same idea you have seen elsewhere. If you have integrated another agent API, these should need no translation.  ## Keys and IDs  Every identifier here is one of two things, and its name tells you which.  A `*_key` is a name you choose. `tenant_key`, `agent_key`, `session_key`, and `definition_key` name a resource. Send one and nvoken either creates that resource or hands back the one your name already refers to, so the same request twice gives you the same resource rather than two. `user_key` is a label rather than a name: it records who a Session was opened for, so you can filter on it later — and on an Agent whose Definition sets `memory.scope: user`, it also selects which memories that Agent can recall. It is fixed by the request that opens a Session and a later turn cannot change it. `idempotency_key` names one attempt, not a resource.  An `id` is an identifier nvoken mints. It carries a typed prefix, `sess_` for a Session and `inv_` for an Invocation, and everything after that prefix is opaque. Match on the prefix if it helps you route. Never parse past it, and never build one yourself.  A resource calls its own identifier `id`. A reference to a different resource carries that resource\'s name, so `session_id` on an Invocation is the Session it belongs to. There is no third pattern.  Paths only ever take IDs. Keys travel in request bodies and query filters. Every keyed resource reports both its key and its `id`, so you can always get from your name to nvoken\'s identifier by reading the resource, and you never have to keep a mapping of your own.  ## You can always read back what applied  Anything nvoken decides on your behalf is readable on the resource that used it. You never have to work out what happened by combining the request you sent with your own assumptions about nvoken\'s defaults — just read the resource.  A turn reports the `limits` it is really running under, after defaults and minimums; the `definition` it ran with, exactly as stored; and `provenance`, which records what actually served the request. A Session reports its compaction (summarization) policy with `auto` already resolved to a real number and a real model, and its retention window as accepted. New settings will work the same way: a default you cannot read back is a setting only the server knows about.  ## Streaming  There is one stream: `GET /v1/sessions/{session_id}/stream`. It carries every turn in the Session, which is what a conversation needs. Add `invocation_id` to narrow it to one turn. That is a filter on one log, not a second endpoint, because cursors were always Session-scoped and a position from a filtered read resumes an unfiltered one.  Admission is separate from streaming. `POST /v1/invocations` returns the turn, and that response is your acknowledgment; then you open the stream. A dropped connection costs you nothing, because the turn already exists and you already hold its ID.  ### Saved frames and live frames  Every frame is one or the other, and the difference decides what you may store. A saved frame carries an SSE `id`. That ID is your resume position and the only value you need to keep. A live frame carries no `id`: it is a preview or a control signal, it is never stored, and it is never replayed.  `transcript.update` is the one saved frame. `message.delta`, `stream.resync`, and `connection.closing` are live.  ### Folding a transcript.update  It carries `cursor`, `messages`, and `invocation_changes`. Messages append by `sequence` and are never sent twice. Changes are an append log keyed by `(invocation_id, revision)`; fold to the highest revision for current state. Within one frame apply messages before changes, so a turn is never marked settled before its final message exists.  ### Resuming and finishing  The resume position has one name: `cursor`. It is the field on a durable frame and the query parameter that resumes a stream. Server-Sent Events mirrors it onto the `id:` line and accepts the `Last-Event-ID` header in the parameter\'s place, because a faithful SSE binding must; those are the binding\'s mechanics, not two more names for the value, and another binding would carry the same one name. `cursor` wins when a request supplies both.  **A turn is over when a change for it carries a terminal status.** That is the terminal signal, and there is no other. It is saved, so it replays on reconnect like every other change, at any cursor. Read `GET /v1/invocations/{invocation_id}` if you want the composed result.  The change tells you so directly: `terminal` is true on exactly the change that ends the turn. Read it instead of testing `status` against a set of your own, so a status added later cannot silently change what your code believes a turn\'s end is.  `connection.closing` says exactly what it says and nothing more. A client that has not seen its terminal change reconnects and keeps reading.  ### The stream stays open  An unfiltered stream is a subscription. It stays open while the Session is idle, keepalives and all, and a turn started later by anyone appears on it. You do not poll. A server may still reclaim an idle connection, and when it does it says `connection.closing` with reason `idle`, which means reconnect when you next need to read rather than immediately.  A filtered stream closes once it has delivered that turn\'s terminal change. A client following the exit rule has already left.  ### Previews  `message.delta` previews a message the model is writing. `kind` says what kind of content it is and `delta` carries the fragment, for every kind. Accumulate by `(message_id, content_index)`, and discard everything provisional when `attempt` increases, when `stream.resync` arrives, when the saved message with that ID lands, and when the turn\'s change carries a terminal status. Never store preview text as a message, and never use it to decide whether a turn succeeded.  `attempt`, `revision`, and `sequence` count from 1. `content_index` counts from 0.  ### Compatibility  Stream events may gain fields over time. Ignore fields you do not recognize rather than refusing the frame. New enum values may appear too. Treat an unknown `message.delta` kind as content you do not render. Treat an unknown `stream.resync` reason as `live_delivery_gap` and discard your previews. Treat an unknown `connection.closing` reason as `rotate` and reconnect with your last saved `id`. Reconnecting is always safe.  ### Transport  The protocol is the frames and their durability rules. Server-Sent Events is how they travel today and the only binding. Three mechanics belong to SSE itself: the `id:` line, the `retry:` opener, and comment keepalives. Everything else, resumption included, lives in the frames.  ### What the stream carries  Frames carry structure and text. Images and documents travel as descriptors, with media type, size in bytes, and a `sha256:` digest, never as inline bytes. Frame sizes stay bounded by text no matter what a turn produced.
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
 * One turn.
 *
 * Some fields are audience-restricted: they are present for a machine
 * credential and omitted for a browser grant, which is why they are not
 * required. Omission is the whole mechanism, so one schema decodes every
 * response and nothing has to be guessed from the payload. The omitted
 * set here is `agent_id`, `user_key`, `agent_definition`, `context`, `credit_block`,
 * `usage`, `provenance`, `structured_output_provenance`, `metadata`, and
 * `limits`.
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
     *
     * @type {string}
     * @memberof Invocation
     */
    agentKey: string;
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
    userKey?: string | null;
    /**
     * Stable App-owned Agent Definition identifier with the public `def_` prefix. Treat the body as opaque.
     * @type {string}
     * @memberof Invocation
     */
    definitionId: string;
    /**
     * Immutable Agent Definition revision admitted for this turn.
     * @type {number}
     * @memberof Invocation
     */
    definitionRevision: number;
    /**
     * The agent definition this turn actually ran with, stored when the turn
     * started and returned exactly as it was. Request headers for remote MCP
     * servers are never stored and never appear here.
     *
     * Present on `GET /v1/invocations/{id}` and on the result. Null in list
     * items, where `definition_id` and `definition_revision`
     * identify it instead.
     *
     * @type {AgentDefinition}
     * @memberof Invocation
     */
    definition?: AgentDefinition | null;
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
    context?: Array<InvocationContextItem> | null;
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
    creditBlock?: CreditBlock | null;
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
    usage?: ModelUsage | null;
    /**
     *
     * @type {ModelProvenance}
     * @memberof Invocation
     */
    provenance?: ModelProvenance | null;
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
    structuredOutputProvenance?: StructuredOutputProvenance | null;
    /**
     * Your own data, stored when the turn was created and returned exactly as
     * you sent it.
     *
     * @type {{ [key: string]: string; }}
     * @memberof Invocation
     */
    metadata?: { [key: string]: string; } | null;
    /**
     *
     * @type {ResolvedLimits}
     * @memberof Invocation
     */
    limits?: ResolvedLimits;
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
    if (!('agentKey' in value) || value['agentKey'] === undefined) return false;
    if (!('sessionId' in value) || value['sessionId'] === undefined) return false;
    if (!('definitionId' in value) || value['definitionId'] === undefined) return false;
    if (!('definitionRevision' in value) || value['definitionRevision'] === undefined) return false;
    if (!('status' in value) || value['status'] === undefined) return false;
    if (!('stopReason' in value) || value['stopReason'] === undefined) return false;
    if (!('attempt' in value) || value['attempt'] === undefined) return false;
    if (!('error' in value) || value['error'] === undefined) return false;
    if (!('structuredOutput' in value) || value['structuredOutput'] === undefined) return false;
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
        'agentKey': json['agent_key'],
        'sessionId': json['session_id'],
        'userKey': json['user_key'] == null ? undefined : json['user_key'],
        'definitionId': json['definition_id'],
        'definitionRevision': json['definition_revision'],
        'definition': json['definition'] == null ? undefined : AgentDefinitionFromJSON(json['definition']),
        'context': json['context'] == null ? undefined : ((json['context'] as Array<any>).map(InvocationContextItemFromJSON)),
        'deduplicated': json['deduplicated'] == null ? undefined : json['deduplicated'],
        'status': InvocationStatusFromJSON(json['status']),
        'stopReason': InvocationStopReasonFromJSON(json['stop_reason']),
        'creditBlock': json['credit_block'] == null ? undefined : CreditBlockFromJSON(json['credit_block']),
        'attempt': json['attempt'],
        'error': InvocationFailureFromJSON(json['error']),
        'usage': json['usage'] == null ? undefined : ModelUsageFromJSON(json['usage']),
        'provenance': json['provenance'] == null ? undefined : ModelProvenanceFromJSON(json['provenance']),
        'structuredOutput': json['structured_output'],
        'structuredOutputProvenance': json['structured_output_provenance'] == null ? undefined : StructuredOutputProvenanceFromJSON(json['structured_output_provenance']),
        'metadata': json['metadata'] == null ? undefined : json['metadata'],
        'limits': json['limits'] == null ? undefined : ResolvedLimitsFromJSON(json['limits']),
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
        'agent_key': value['agentKey'],
        'session_id': value['sessionId'],
        'user_key': value['userKey'],
        'definition_id': value['definitionId'],
        'definition_revision': value['definitionRevision'],
        'definition': AgentDefinitionToJSON(value['definition']),
        'context': value['context'] == null ? undefined : ((value['context'] as Array<any>).map(InvocationContextItemToJSON)),
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
