/* tslint:disable */
/* eslint-disable */
/**
 * nvoken API
 * nvoken runs agent turns for you. You describe a turn — an Agent Definition and some input — and nvoken queues it, runs it in the background, keeps running it across restarts and failures, and lets you either watch it live or come back for the result later.  One turn is one Invocation. `Invocation` is the name in every path, field, schema, and error, and it is the word to reach for when you need to be exact. `turn` is the English for the same thing, and this document uses it in explanatory prose. They are not two resources, so there is no relationship between them for you to model.  Your application stays in charge of what your agents are and when they run. nvoken owns the conversation: it stores the messages, tracks the state of every turn, and handles talking to the model providers.  ## Getting started  `POST /v1/invocations` starts a turn and returns a `202` right away. From there:  - Follow it live with `GET /v1/sessions/{session_id}/stream`, or read   `GET /v1/invocations/{invocation_id}/result` whenever you want the   finished answer. Disconnecting never cancels anything. - If your agent uses tools you run yourself, the turn stops with status   `waiting` and lists what it needs. Run them, post the results to   `/tool-results`, and the turn continues where it left off. - Sessions carry conversation history from one turn to the next. They   last until you delete them, or until a retention window you set runs   out.  Also here: tools nvoken calls back to over HTTPS, remote MCP servers, structured output validated against your JSON Schema, reusable Agent Definitions, image and document input, your own model provider keys, and spending limits.  ## Authorization  Machine credentials use `nvk_…` bearer API keys. A configured trusted console may also present a short-lived Ed25519 issuer token; this is an authentication presentation only and does not create a user identity model in nvoken.  Browser-direct callers use narrow JWTs, exact-origin CORS, and App/tenant/user admission limits. A host may mint client tokens from its own Ed25519 key. An App that explicitly enables anonymous access may instead let nvoken mint a short-lived access token and renewable visitor token from one allowed public origin.  A browser grant sees less, and it sees it in the same shape. There is one schema per resource, and the fields a browser may not see are simply omitted from its responses, which is why those fields are not required. There are no parallel browser schemas and no response unions, so every payload decodes against one type and nothing about the shape has to be inferred from the credential that fetched it. `GET /v1/identity` reports which kind of caller you are, under `authentication.method`.  - Full App keys can read and mutate every resource owned by their App. - Read-only App keys can read the same non-secret App and runtime data but   cannot mutate anything, including their own key lineage. - Installation-admin keys manage Orgs, Apps, and App keys but resolve no   App data. Short-lived console presentations provide fixed Org or admin   control-plane and reporting access.  Tenant and user assertion headers narrow individual requests. Durable API keys carry no tenant, Session, profile, or operation constraints.  ## Acting for one tenant or one end user  An app-wide credential can reach every tenant in its App, which means an id that arrives from the wrong place — a stale link, a mixed-up webhook, a tampered form field — is an id it can act on. The usual answer is for the host to re-read the resource and compare its `tenant_key` and `user_key` before every call. Say it once instead:  ``` X-Nvoken-Tenant-Key: acme X-Nvoken-User-Key: user-7c1f ```  Both headers are accepted on every request and narrow that one request to the scope they name. Anything outside it is reported as `not_found`, so a Session or Invocation belonging to another tenant or another end user cannot be read, cancelled, interrupted, forked, answered, or erased — and you learn nothing about whether the id exists. Writes take the same scope: an omitted `tenant_key` or `user_key` in the body inherits it, and one naming somebody else is refused.  Two rules keep them honest. They may only narrow, so a credential already bound to one tenant refuses a header naming another with `forbidden` rather than silently returning nothing. And a record with no `user_key` at all is outside every `X-Nvoken-User-Key` assertion rather than inside all of them, because a Session nobody claimed is not one you can claim by asserting.  Each header takes 1 to 255 characters and may appear once. A blank, oversized, or repeated header is `invalid_request` rather than ignored: an assertion nobody notices dropping is worse than no assertion at all.  A browser token already carries its tenant and its end user, so it neither needs these headers nor is allowed to send them.  ## Familiar names  Where a name already means something in other agent APIs, nvoken uses it the same way rather than inventing its own. `metadata` follows OpenAI\'s limits of 16 keys, 64-character names, and 512-byte values. `output_text` is the assistant\'s text joined into one string. `reasoning.effort` takes `low`, `medium`, `high`, `xhigh`, and `max`. `stop_reason: end_turn`, status `running`, and the `commentary` and `final_answer` message phases are the same idea you have seen elsewhere. If you have integrated another agent API, these should need no translation.  ## Keys and IDs  Every identifier here is one of two things, and its name tells you which.  A `*_key` is a name you choose. `tenant_key`, `agent_key`, `session_key`, and `definition_key` name a resource. Send one and nvoken either creates that resource or hands back the one your name already refers to, so the same request twice gives you the same resource rather than two. `user_key` is a label rather than a name: it records who a Session was opened for, so you can filter on it later — and on an Agent whose Definition sets `memory.scope: user`, it also selects which memories that Agent can recall. It is fixed by the request that opens a Session and a later turn cannot change it. `idempotency_key` names one attempt, not a resource.  An `id` is an identifier nvoken mints. It carries a typed prefix, `sess_` for a Session and `inv_` for an Invocation, and everything after that prefix is opaque. Match on the prefix if it helps you route. Never parse past it, and never build one yourself.  A resource calls its own identifier `id`. A reference to a different resource carries that resource\'s name, so `session_id` on an Invocation is the Session it belongs to. There is no third pattern.  Paths only ever take IDs. Keys travel in request bodies and query filters. Every keyed resource reports both its key and its `id`, so you can always get from your name to nvoken\'s identifier by reading the resource, and you never have to keep a mapping of your own.  ## You can always read back what applied  Anything nvoken decides on your behalf is readable on the resource that used it. You never have to work out what happened by combining the request you sent with your own assumptions about nvoken\'s defaults — just read the resource.  A turn reports the `limits` it is really running under, after defaults and minimums; the `definition` it ran with, exactly as stored; and `provenance`, which records what actually served the request. A Session reports its compaction (summarization) policy with `auto` already resolved to a real number and a real model, and its retention window as accepted. New settings will work the same way: a default you cannot read back is a setting only the server knows about.  ## Streaming  There is one stream: `GET /v1/sessions/{session_id}/stream`. It carries every turn in the Session, which is what a conversation needs. Add `invocation_id` to narrow it to one turn. That is a filter on one log, not a second endpoint, because cursors were always Session-scoped and a position from a filtered read resumes an unfiltered one.  Admission is separate from streaming. `POST /v1/invocations` returns the turn, and that response is your acknowledgment; then you open the stream. A dropped connection costs you nothing, because the turn already exists and you already hold its ID.  ### Saved frames and live frames  Every frame is one or the other, and the difference decides what you may store. A saved frame carries an SSE `id`. That ID is your resume position and the only value you need to keep. A live frame carries no `id`: it is a preview or a control signal, it is never stored, and it is never replayed.  `transcript.update` is the one saved frame. `message.delta`, `stream.resync`, and `connection.closing` are live.  ### Folding a transcript.update  It carries `cursor`, `messages`, and `invocation_changes`. Messages append by `sequence` and are never sent twice. Changes are an append log keyed by `(invocation_id, revision)`; fold to the highest revision for current state. Within one frame apply messages before changes, so a turn is never marked settled before its final message exists.  ### Resuming and finishing  The resume position has one name: `cursor`. It is the field on a durable frame and the query parameter that resumes a stream. Server-Sent Events mirrors it onto the `id:` line and accepts the `Last-Event-ID` header in the parameter\'s place, because a faithful SSE binding must; those are the binding\'s mechanics, not two more names for the value, and another binding would carry the same one name. `cursor` wins when a request supplies both.  **A turn is over when a change for it carries a terminal status.** That is the terminal signal, and there is no other. It is saved, so it replays on reconnect like every other change, at any cursor. Read `GET /v1/invocations/{invocation_id}` if you want the composed result.  The change tells you so directly: `terminal` is true on exactly the change that ends the turn. Read it instead of testing `status` against a set of your own, so a status added later cannot silently change what your code believes a turn\'s end is.  `connection.closing` says exactly what it says and nothing more. A client that has not seen its terminal change reconnects and keeps reading.  ### The stream stays open  An unfiltered stream is a subscription. It stays open while the Session is idle, keepalives and all, and a turn started later by anyone appears on it. You do not poll. A server may still reclaim an idle connection, and when it does it says `connection.closing` with reason `idle`, which means reconnect when you next need to read rather than immediately.  A filtered stream closes once it has delivered that turn\'s terminal change. A client following the exit rule has already left.  ### Previews  `message.delta` previews a message the model is writing. `kind` says what kind of content it is and `delta` carries the fragment, for every kind. Accumulate by `(message_id, content_index)`, and discard everything provisional when `attempt` increases, when `stream.resync` arrives, when the saved message with that ID lands, and when the turn\'s change carries a terminal status. Never store preview text as a message, and never use it to decide whether a turn succeeded.  `attempt`, `revision`, and `sequence` count from 1. `content_index` counts from 0.  ### Compatibility  Stream events may gain fields over time. Ignore fields you do not recognize rather than refusing the frame. New enum values may appear too. Treat an unknown `message.delta` kind as content you do not render. Treat an unknown `stream.resync` reason as `live_delivery_gap` and discard your previews. Treat an unknown `connection.closing` reason as `rotate` and reconnect with your last saved `id`. Reconnecting is always safe.  ### Transport  The protocol is the frames and their durability rules. Server-Sent Events is how they travel today and the only binding. Three mechanics belong to SSE itself: the `id:` line, the `retry:` opener, and comment keepalives. Everything else, resumption included, lives in the frames.  ### What the stream carries  Frames carry structure and text. Images and documents travel as descriptors, with media type, size in bytes, and a `sha256:` digest, never as inline bytes. Frame sizes stay bounded by text no matter what a turn produced.
 *
 * The version of the OpenAPI document: 0.1.0
 *
 *
 * NOTE: This class is auto generated by OpenAPI Generator (https://openapi-generator.tech).
 * https://openapi-generator.tech
 * Do not edit the class manually.
 */

import { mapValues } from '../runtime.js';
import type { AgentDefinitionOverrides } from './AgentDefinitionOverrides.js';
import {
    AgentDefinitionOverridesFromJSON,
    AgentDefinitionOverridesFromJSONTyped,
    AgentDefinitionOverridesToJSON,
    AgentDefinitionOverridesToJSONTyped,
} from './AgentDefinitionOverrides.js';
import type { WebhookTarget } from './WebhookTarget.js';
import {
    WebhookTargetFromJSON,
    WebhookTargetFromJSONTyped,
    WebhookTargetToJSON,
    WebhookTargetToJSONTyped,
} from './WebhookTarget.js';
import type { InvocationContextItem } from './InvocationContextItem.js';
import {
    InvocationContextItemFromJSON,
    InvocationContextItemFromJSONTyped,
    InvocationContextItemToJSON,
    InvocationContextItemToJSONTyped,
} from './InvocationContextItem.js';
import type { InvocationInput } from './InvocationInput.js';
import {
    InvocationInputFromJSON,
    InvocationInputFromJSONTyped,
    InvocationInputToJSON,
    InvocationInputToJSONTyped,
} from './InvocationInput.js';
import type { InvocationTrigger } from './InvocationTrigger.js';
import {
    InvocationTriggerFromJSON,
    InvocationTriggerFromJSONTyped,
    InvocationTriggerToJSON,
    InvocationTriggerToJSONTyped,
} from './InvocationTrigger.js';
import type { ProviderKeySelection } from './ProviderKeySelection.js';
import {
    ProviderKeySelectionFromJSON,
    ProviderKeySelectionFromJSONTyped,
    ProviderKeySelectionToJSON,
    ProviderKeySelectionToJSONTyped,
} from './ProviderKeySelection.js';
import type { SessionOptions } from './SessionOptions.js';
import {
    SessionOptionsFromJSON,
    SessionOptionsFromJSONTyped,
    SessionOptionsToJSON,
    SessionOptionsToJSONTyped,
} from './SessionOptions.js';
import type { MCPServerHeaders } from './MCPServerHeaders.js';
import {
    MCPServerHeadersFromJSON,
    MCPServerHeadersFromJSONTyped,
    MCPServerHeadersToJSON,
    MCPServerHeadersToJSONTyped,
} from './MCPServerHeaders.js';

/**
 *
 * @export
 * @interface CreateInvocationRequest
 */
export interface CreateInvocationRequest {
    /**
     * Opaque identifier with the public `agent_` prefix. Treat the body as opaque.
     * @type {string}
     * @memberof CreateInvocationRequest
     */
    agentId?: string;
    /**
     * Stable caller-controlled Agent key, unique within the effective
     * tenant. Mutually exclusive with `agent_id`.
     *
     * @type {string}
     * @memberof CreateInvocationRequest
     */
    agentKey?: string;
    /**
     * Optional tenant partition. For Session-key resolution or a new
     * Session, precedence is credential constraint, this explicit value,
     * then the default partition. For Session-ID resolution, an App
     * credential without a tenant constraint may omit it and use the
     * stored partition.
     *
     * @type {string}
     * @memberof CreateInvocationRequest
     */
    tenantKey?: string;
    /**
     * Who this turn is for. The first request that opens a Session fixes
     * its `user_key`, including fixing it to absent; every later turn
     * either sends the same one or leaves it out and inherits it. A turn
     * naming a different end user is refused with
     * `session_user_key_conflict`.
     *
     * It is a filter, and on an Agent whose Definition sets
     * `memory.scope: user` it is also the memory partition — it decides
     * whose durable memories the model can recall — so it is required on
     * the turn that opens a Session for such an Agent.
     *
     * @type {string}
     * @memberof CreateInvocationRequest
     */
    userKey?: string;
    /**
     * The exact ToolCall and parent Invocation that caused this turn.
     * nvoken verifies the pair, inherits and enforces its tenant and user
     * scope, and keeps it as immutable idempotency evidence. Accepted
     * only from machine credentials. One ToolCall may trigger multiple
     * children with different idempotency keys.
     *
     * @type {InvocationTrigger}
     * @memberof CreateInvocationRequest
     */
    triggeredBy?: InvocationTrigger;
    /**
     * Existing Session to continue. Mutually exclusive with session_key.
     * @type {string}
     * @memberof CreateInvocationRequest
     */
    sessionId?: string;
    /**
     * Caller key resolved within (effective tenant partition,
     * Agent, session_key). Mutually exclusive with session_id.
     *
     * @type {string}
     * @memberof CreateInvocationRequest
     */
    sessionKey?: string;
    /**
     * Settings stored on the Session itself, rather than on this turn.
     *
     * On a new Session these are saved. On a Session that already exists
     * they are checked rather than applied: matching values are fine, and
     * a different value returns `session_options_conflict` telling you
     * which paths disagreed. This keeps two callers from silently
     * reconfiguring each other's conversation. Send
     * `on_conflict: "join"` when you mean "reach whatever Session is
     * there" rather than "it should be configured like this".
     *
     * If no compaction policy is stored yet, this turn can install one,
     * because the policy needs a model to validate against and only a turn
     * supplies that.
     *
     * A Session's title and other descriptive labels are not here. They
     * are `metadata`, written with `PATCH /v1/sessions/{session_id}`.
     *
     * @type {SessionOptions}
     * @memberof CreateInvocationRequest
     */
    sessionOptions?: SessionOptions;
    /**
     * Your own data to attach to this turn — a ticket number, a trace
     * ID, whatever helps you tie it back to your system. nvoken stores
     * it and hands it back untouched.
     *
     * It is fixed once the turn is created and counts as part of the
     * request for idempotency. Retrying with the same `idempotency_key`
     * but different metadata is treated as a different request and
     * returns a conflict rather than updating it. A genuine retry of the
     * same original request carries the same values anyway.
     *
     * Session metadata is a separate thing and can be changed — see
     * `PATCH /v1/sessions/{session_id}`.
     *
     * @type {{ [key: string]: string; }}
     * @memberof CreateInvocationRequest
     */
    metadata?: { [key: string]: string; };
    /**
     * Your key for making retries safe. Send the same unchanged request
     * again after a 5xx, a timeout, a dropped connection, or any case
     * where you never saw the response, and you get the original turn
     * back instead of starting a second one.
     *
     * Keys are scoped to the tenant and resolved Agent, so the same key
     * under a different tenant or Agent is a different request.
     * Deduplication lasts as long as the original turn still exists.
     *
     * @type {string}
     * @memberof CreateInvocationRequest
     */
    idempotencyKey: string;
    /**
     * What to do when the Session already has a turn running. A Session
     * runs one turn at a time.
     *
     * - `reject` (the default) refuses this request with
     *   `session_invocation_active` and leaves the running turn alone.
     * - `supersede` cancels the running turn and starts this one in its
     *   place. The cancelled turn's work is discarded and does not carry
     *   forward — "discard and redo".
     * - `interrupt` asks the running turn to stop cleanly and starts
     *   this one only once it has, so this turn builds on what the
     *   stopped one produced — "stop and redo".
     *
     * Omitting the field and sending `reject` are the same request for
     * idempotency purposes.
     *
     * @type {CreateInvocationRequestIfActiveEnum}
     * @memberof CreateInvocationRequest
     */
    ifActive?: CreateInvocationRequestIfActiveEnum;
    /**
     * What to do when the turn runs out of one of its consumption
     * limits. `stop` ends it as `incomplete`. `hold` leaves it as
     * `budget_hold` so you can raise the limit and continue it.
     *
     * Covers the iteration, output-token, and per-turn estimated-cost
     * limits, and exhausted tenant credits. Deadlines are not covered —
     * a turn that runs out of time always ends and can never be resumed.
     *
     * @type {CreateInvocationRequestOnBudgetExhaustedEnum}
     * @memberof CreateInvocationRequest
     */
    onBudgetExhausted?: CreateInvocationRequestOnBudgetExhaustedEnum;
    /**
     * Ordered application-owned state snapshots to record before this
     * turn's input. Send a name again to supersede its prior value. An
     * unchanged latest value is deduplicated from the transcript, while
     * this exact pre-deduplication payload remains part of the Invocation
     * and of idempotency comparison.
     *
     * A Session may observe at most 16 distinct names over its lifetime.
     * Names are stored and shown to the model with the reserved `app-`
     * prefix, which callers must omit here. Context is not part of the
     * Agent Definition and never advances its revision.
     *
     * @type {Array<InvocationContextItem>}
     * @memberof CreateInvocationRequest
     */
    context?: Array<InvocationContextItem>;
    /**
     *
     * @type {InvocationInput}
     * @memberof CreateInvocationRequest
     */
    input: InvocationInput;
    /**
     *
     * @type {WebhookTarget}
     * @memberof CreateInvocationRequest
     */
    webhook?: WebhookTarget;
    /**
     * Optional one-turn revision pin, ahead of Session and Agent pins.
     * @type {number}
     * @memberof CreateInvocationRequest
     */
    definitionRevision?: number;
    /**
     *
     * @type {AgentDefinitionOverrides}
     * @memberof CreateInvocationRequest
     */
    overrides?: AgentDefinitionOverrides;
    /**
     * Per-Invocation secret headers keyed to MCP server names in the
     * selected Agent Definition. Encrypted for this turn and never stored
     * in, hashed into, or returned with the Agent Definition.
     *
     * @type {Array<MCPServerHeaders>}
     * @memberof CreateInvocationRequest
     */
    mcpServerHeaders?: Array<MCPServerHeaders>;
    /**
     * Which key pays for the model on this turn. Names a source; never
     * contains a secret.
     *
     * Leave it out and nvoken works down its default order: your app's stored
     * key for that provider, then a self-hosted installation's environment
     * key (`config_byok`), then platform funding if the installation allows
     * it.
     *
     * Whichever source is chosen is fixed when the turn starts. A turn never
     * silently falls through to a different payer partway through, so the
     * bill cannot move once work has begun.
     *
     * @type {Array<ProviderKeySelection>}
     * @memberof CreateInvocationRequest
     */
    providerKeys?: Array<ProviderKeySelection>;
}


/**
 * @export
 */
export const CreateInvocationRequestIfActiveEnum = {
    Reject: 'reject',
    Supersede: 'supersede',
    Interrupt: 'interrupt'
} as const;
export type CreateInvocationRequestIfActiveEnum = typeof CreateInvocationRequestIfActiveEnum[keyof typeof CreateInvocationRequestIfActiveEnum];

/**
 * @export
 */
export const CreateInvocationRequestOnBudgetExhaustedEnum = {
    Stop: 'stop',
    Hold: 'hold'
} as const;
export type CreateInvocationRequestOnBudgetExhaustedEnum = typeof CreateInvocationRequestOnBudgetExhaustedEnum[keyof typeof CreateInvocationRequestOnBudgetExhaustedEnum];


/**
 * Check if a given object implements the CreateInvocationRequest interface.
 */
export function instanceOfCreateInvocationRequest(value: object): value is CreateInvocationRequest {
    if (!('idempotencyKey' in value) || value['idempotencyKey'] === undefined) return false;
    if (!('input' in value) || value['input'] === undefined) return false;
    return true;
}

export function CreateInvocationRequestFromJSON(json: any): CreateInvocationRequest {
    return CreateInvocationRequestFromJSONTyped(json, false);
}

export function CreateInvocationRequestFromJSONTyped(json: any, ignoreDiscriminator: boolean): CreateInvocationRequest {
    if (json == null) {
        return json;
    }
    return {

        'agentId': json['agent_id'] == null ? undefined : json['agent_id'],
        'agentKey': json['agent_key'] == null ? undefined : json['agent_key'],
        'tenantKey': json['tenant_key'] == null ? undefined : json['tenant_key'],
        'userKey': json['user_key'] == null ? undefined : json['user_key'],
        'triggeredBy': json['triggered_by'] == null ? undefined : InvocationTriggerFromJSON(json['triggered_by']),
        'sessionId': json['session_id'] == null ? undefined : json['session_id'],
        'sessionKey': json['session_key'] == null ? undefined : json['session_key'],
        'sessionOptions': json['session_options'] == null ? undefined : SessionOptionsFromJSON(json['session_options']),
        'metadata': json['metadata'] == null ? undefined : json['metadata'],
        'idempotencyKey': json['idempotency_key'],
        'ifActive': json['if_active'] == null ? undefined : json['if_active'],
        'onBudgetExhausted': json['on_budget_exhausted'] == null ? undefined : json['on_budget_exhausted'],
        'context': json['context'] == null ? undefined : ((json['context'] as Array<any>).map(InvocationContextItemFromJSON)),
        'input': InvocationInputFromJSON(json['input']),
        'webhook': json['webhook'] == null ? undefined : WebhookTargetFromJSON(json['webhook']),
        'definitionRevision': json['definition_revision'] == null ? undefined : json['definition_revision'],
        'overrides': json['overrides'] == null ? undefined : AgentDefinitionOverridesFromJSON(json['overrides']),
        'mcpServerHeaders': json['mcp_server_headers'] == null ? undefined : ((json['mcp_server_headers'] as Array<any>).map(MCPServerHeadersFromJSON)),
        'providerKeys': json['provider_keys'] == null ? undefined : ((json['provider_keys'] as Array<any>).map(ProviderKeySelectionFromJSON)),
    };
}

export function CreateInvocationRequestToJSON(json: any): CreateInvocationRequest {
    return CreateInvocationRequestToJSONTyped(json, false);
}

export function CreateInvocationRequestToJSONTyped(value?: CreateInvocationRequest | null, ignoreDiscriminator: boolean = false): any {
    if (value == null) {
        return value;
    }

    return {

        'agent_id': value['agentId'],
        'agent_key': value['agentKey'],
        'tenant_key': value['tenantKey'],
        'user_key': value['userKey'],
        'triggered_by': InvocationTriggerToJSON(value['triggeredBy']),
        'session_id': value['sessionId'],
        'session_key': value['sessionKey'],
        'session_options': SessionOptionsToJSON(value['sessionOptions']),
        'metadata': value['metadata'],
        'idempotency_key': value['idempotencyKey'],
        'if_active': value['ifActive'],
        'on_budget_exhausted': value['onBudgetExhausted'],
        'context': value['context'] == null ? undefined : ((value['context'] as Array<any>).map(InvocationContextItemToJSON)),
        'input': InvocationInputToJSON(value['input']),
        'webhook': WebhookTargetToJSON(value['webhook']),
        'definition_revision': value['definitionRevision'],
        'overrides': AgentDefinitionOverridesToJSON(value['overrides']),
        'mcp_server_headers': value['mcpServerHeaders'] == null ? undefined : ((value['mcpServerHeaders'] as Array<any>).map(MCPServerHeadersToJSON)),
        'provider_keys': value['providerKeys'] == null ? undefined : ((value['providerKeys'] as Array<any>).map(ProviderKeySelectionToJSON)),
    };
}
