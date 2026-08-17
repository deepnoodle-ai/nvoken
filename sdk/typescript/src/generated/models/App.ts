/* tslint:disable */
/* eslint-disable */
/**
 * nvoken API
 * nvoken runs agent turns for you. You describe a turn — an Agent Definition and some input — and nvoken queues it, runs it in the background, keeps running it across restarts and failures, and lets you either watch it live or come back for the result later.  One turn is one Invocation. `Invocation` is the name in every path, field, schema, and error, and it is the word to reach for when you need to be exact. `turn` is the English for the same thing, and this document uses it in explanatory prose. They are not two resources, so there is no relationship between them for you to model.  Your application stays in charge of what your agents are and when they run. nvoken owns the conversation: it stores the messages, tracks the state of every turn, and handles talking to the model providers.  ## Getting started  `POST /v1/invocations` starts a turn and returns a `202` right away. From there:  - Follow it live with `GET /v1/sessions/{session_id}/stream`, or read   `GET /v1/invocations/{invocation_id}/result` whenever you want the   finished answer. Disconnecting never cancels anything. - If your agent uses tools you run yourself, the turn stops with status   `waiting` and lists what it needs. Run them, post the results to   `/tool-results`, and the turn continues where it left off. - Sessions carry conversation history from one turn to the next. They   last until you delete them, or until a retention window you set runs   out.  Also here: tools nvoken calls back to over HTTPS, remote MCP servers, structured output validated against your JSON Schema, reusable Agent Definitions, image and document input, your own model provider keys, and spending limits.  ## Authorization  Machine credentials use `nvk_…` bearer API keys. A configured trusted console may also present a short-lived Ed25519 issuer token; this is an authentication presentation only and does not create a user identity model in nvoken.  Browser-direct callers use narrow JWTs, exact-origin CORS, and App/tenant/user admission limits. A host may mint client tokens from its own Ed25519 key. An App that explicitly enables anonymous access may instead let nvoken mint a short-lived access token and renewable visitor token from one allowed public origin.  A browser grant sees less, and it sees it in the same shape. There is one schema per resource, and the fields a browser may not see are simply omitted from its responses, which is why those fields are not required. There are no parallel browser schemas and no response unions, so every payload decodes against one type and nothing about the shape has to be inferred from the credential that fetched it. `GET /v1/identity` reports which kind of caller you are, under `authentication.method`.  - App-scoped Runtime credentials may call every Runtime operation and GET /v1/identity. - App-scoped Viewer credentials may call Runtime reads and GET /v1/identity. - App-scoped Operator credentials may call every Runtime operation, GET /v1/identity, and all credential lifecycle operations. - Org-scoped Viewer and Operator credentials are management and reporting   identities only. They resolve no tenant and cannot perform Runtime   operations; Operators can register and manage Apps and their App   credentials, while Viewers have read-only access.  Tenant, Session, operation, and expiry constraints only narrow these grants.  ## Familiar names  Where a name already means something in other agent APIs, nvoken uses it the same way rather than inventing its own. `metadata` follows OpenAI\'s limits of 16 keys, 64-character names, and 512-byte values. `output_text` is the assistant\'s text joined into one string. `reasoning.effort` takes `low`, `medium`, `high`, `xhigh`, and `max`. `stop_reason: end_turn`, status `running`, and the `commentary` and `final_answer` message phases are the same idea you have seen elsewhere. If you have integrated another agent API, these should need no translation.  ## Keys and IDs  Every identifier here is one of two things, and its name tells you which.  A `*_key` is a name you choose. `tenant_key`, `agent_key`, `session_key`, and `definition_key` name a resource. Send one and nvoken either creates that resource or hands back the one your name already refers to, so the same request twice gives you the same resource rather than two. `user_key` is a label rather than a name: it records who a Session was opened for so you can filter on it later, and it is not a permission boundary. `idempotency_key` names one attempt, not a resource.  An `id` is an identifier nvoken mints. It carries a typed prefix, `sess_` for a Session and `inv_` for an Invocation, and everything after that prefix is opaque. Match on the prefix if it helps you route. Never parse past it, and never build one yourself.  A resource calls its own identifier `id`. A reference to a different resource carries that resource\'s name, so `session_id` on an Invocation is the Session it belongs to. There is no third pattern.  Paths only ever take IDs. Keys travel in request bodies and query filters. Every keyed resource reports both its key and its `id`, so you can always get from your name to nvoken\'s identifier by reading the resource, and you never have to keep a mapping of your own.  ## You can always read back what applied  Anything nvoken decides on your behalf is readable on the resource that used it. You never have to work out what happened by combining the request you sent with your own assumptions about nvoken\'s defaults — just read the resource.  A turn reports the `limits` it is really running under, after defaults and minimums; the `definition` it ran with, exactly as stored; and `provenance`, which records what actually served the request. A Session reports its compaction (summarization) policy with `auto` already resolved to a real number and a real model, and its retention window as accepted. New settings will work the same way: a default you cannot read back is a setting only the server knows about.  ## Streaming  There is one stream: `GET /v1/sessions/{session_id}/stream`. It carries every turn in the Session, which is what a conversation needs. Add `invocation_id` to narrow it to one turn. That is a filter on one log, not a second endpoint, because cursors were always Session-scoped and a position from a filtered read resumes an unfiltered one.  Admission is separate from streaming. `POST /v1/invocations` returns the turn, and that response is your acknowledgment; then you open the stream. A dropped connection costs you nothing, because the turn already exists and you already hold its ID.  ### Saved frames and live frames  Every frame is one or the other, and the difference decides what you may store. A saved frame carries an SSE `id`. That ID is your resume position and the only value you need to keep. A live frame carries no `id`: it is a preview or a control signal, it is never stored, and it is never replayed.  `transcript.update` is the one saved frame. `message.delta`, `stream.resync`, and `connection.closing` are live.  ### Folding a transcript.update  It carries `cursor`, `messages`, and `invocation_changes`. Messages append by `sequence` and are never sent twice. Changes are an append log keyed by `(invocation_id, revision)`; fold to the highest revision for current state. Within one frame apply messages before changes, so a turn is never marked settled before its final message exists.  ### Resuming and finishing  The resume position has one name: `cursor`. It is the field on a durable frame and the query parameter that resumes a stream. Server-Sent Events mirrors it onto the `id:` line and accepts the `Last-Event-ID` header in the parameter\'s place, because a faithful SSE binding must; those are the binding\'s mechanics, not two more names for the value, and another binding would carry the same one name. `cursor` wins when a request supplies both.  **A turn is over when a change for it carries a terminal status.** That is the terminal signal, and there is no other. It is saved, so it replays on reconnect like every other change, at any cursor. Read `GET /v1/invocations/{invocation_id}` if you want the composed result.  The change tells you so directly: `terminal` is true on exactly the change that ends the turn. Read it instead of testing `status` against a set of your own, so a status added later cannot silently change what your code believes a turn\'s end is.  `connection.closing` says exactly what it says and nothing more. A client that has not seen its terminal change reconnects and keeps reading.  ### The stream stays open  An unfiltered stream is a subscription. It stays open while the Session is idle, keepalives and all, and a turn started later by anyone appears on it. You do not poll. A server may still reclaim an idle connection, and when it does it says `connection.closing` with reason `idle`, which means reconnect when you next need to read rather than immediately.  A filtered stream closes once it has delivered that turn\'s terminal change. A client following the exit rule has already left.  ### Previews  `message.delta` previews a message the model is writing. `kind` says what kind of content it is and `delta` carries the fragment, for every kind. Accumulate by `(message_id, content_index)`, and discard everything provisional when `attempt` increases, when `stream.resync` arrives, when the saved message with that ID lands, and when the turn\'s change carries a terminal status. Never store preview text as a message, and never use it to decide whether a turn succeeded.  `attempt`, `revision`, and `sequence` count from 1. `content_index` counts from 0.  ### Compatibility  Stream events may gain fields over time. Ignore fields you do not recognize rather than refusing the frame. New enum values may appear too. Treat an unknown `message.delta` kind as content you do not render. Treat an unknown `stream.resync` reason as `live_delivery_gap` and discard your previews. Treat an unknown `connection.closing` reason as `rotate` and reconnect with your last saved `id`. Reconnecting is always safe.  ### Transport  The protocol is the frames and their durability rules. Server-Sent Events is how they travel today and the only binding. Three mechanics belong to SSE itself: the `id:` line, the `retry:` opener, and comment keepalives. Everything else, resumption included, lives in the frames.  ### What the stream carries  Frames carry structure and text. Images and documents travel as descriptors, with media type, size in bytes, and a `sha256:` digest, never as inline bytes. Frame sizes stay bounded by text no matter what a turn produced.
 *
 * The version of the OpenAPI document: 0.1.0
 *
 *
 * NOTE: This class is auto generated by OpenAPI Generator (https://openapi-generator.tech).
 * https://openapi-generator.tech
 * Do not edit the class manually.
 */

import { mapValues } from '../runtime.js';
import type { AppDefaultRateLimits } from './AppDefaultRateLimits.js';
import {
    AppDefaultRateLimitsFromJSON,
    AppDefaultRateLimitsFromJSONTyped,
    AppDefaultRateLimitsToJSON,
    AppDefaultRateLimitsToJSONTyped,
} from './AppDefaultRateLimits.js';
import type { CreditPolicy } from './CreditPolicy.js';
import {
    CreditPolicyFromJSON,
    CreditPolicyFromJSONTyped,
    CreditPolicyToJSON,
    CreditPolicyToJSONTyped,
} from './CreditPolicy.js';
import type { AnonymousAccess } from './AnonymousAccess.js';
import {
    AnonymousAccessFromJSON,
    AnonymousAccessFromJSONTyped,
    AnonymousAccessToJSON,
    AnonymousAccessToJSONTyped,
} from './AnonymousAccess.js';
import type { BrowserAccess } from './BrowserAccess.js';
import {
    BrowserAccessFromJSON,
    BrowserAccessFromJSONTyped,
    BrowserAccessToJSON,
    BrowserAccessToJSONTyped,
} from './BrowserAccess.js';

/**
 *
 * @export
 * @interface App
 */
export interface App {
    /**
     * The generated nvoken app identifier.
     * @type {string}
     * @memberof App
     */
    id: string;
    /**
     * Durable owning Org. Nullable only during the staged console
     * migration and tightened after every existing App is claimed.
     *
     * @type {string}
     * @memberof App
     */
    orgId: string | null;
    /**
     * The unique host-chosen name for this app.
     * @type {string}
     * @memberof App
     */
    name: string;
    /**
     * Transitional console-owner reference retained only to stamp
     * existing Apps into Orgs during rollout. It is not an authorization
     * boundary and is removed by the gated cleanup migration.
     *
     * @type {string}
     * @memberof App
     */
    externalRef: string | null;
    /**
     * Human-facing label; `name` stays the unique handle.
     * @type {string}
     * @memberof App
     */
    displayName: string | null;
    /**
     * Resolved deadline for each callback HTTP request whose tool does
     * not name one of its own. Defaults to 10. Webhook delivery is
     * unaffected.
     *
     * This ceiling is 60 while a single tool may declare up to 300 in
     * `callback.timeout_seconds`. The asymmetry is the point: this value
     * governs every callback the App makes, so raising it to cover one
     * slow tool would make a hung delivery of a fast one invisible for
     * just as long. Slow is named per tool.
     *
     * @type {number}
     * @memberof App
     */
    callbackTimeoutSeconds: number;
    /**
     * App-wide admission ceilings shared by machine, client-token, and
     * anonymous-token callers. Null means unlimited machine admission;
     * browser access requires finite values.
     *
     * @type {AppDefaultRateLimits}
     * @memberof App
     */
    defaultRateLimits: AppDefaultRateLimits | null;
    /**
     * Complete browser-direct configuration. Null means browser access
     * is disabled and client JWTs receive no browser CORS permission.
     *
     * @type {BrowserAccess}
     * @memberof App
     */
    browserAccess: BrowserAccess | null;
    /**
     * Managed public-browser mode. Null means nvoken will not mint
     * anonymous tokens for this App.
     *
     * @type {AnonymousAccess}
     * @memberof App
     */
    anonymousAccess: AnonymousAccess | null;
    /**
     *
     * @type {CreditPolicy}
     * @memberof App
     */
    creditPolicy: CreditPolicy;
    /**
     *
     * @type {Date}
     * @memberof App
     */
    createdAt: Date;
    /**
     * When the App was archived, or null while it is live. An archived
     * App refuses admission and grant-minting with `409 app_archived`
     * while every read, settlement, erasure, configuration, and
     * revocation path stays open. Its credentials keep authenticating.
     *
     * @type {Date}
     * @memberof App
     */
    archivedAt: Date | null;
}



/**
 * Check if a given object implements the App interface.
 */
export function instanceOfApp(value: object): value is App {
    if (!('id' in value) || value['id'] === undefined) return false;
    if (!('orgId' in value) || value['orgId'] === undefined) return false;
    if (!('name' in value) || value['name'] === undefined) return false;
    if (!('externalRef' in value) || value['externalRef'] === undefined) return false;
    if (!('displayName' in value) || value['displayName'] === undefined) return false;
    if (!('callbackTimeoutSeconds' in value) || value['callbackTimeoutSeconds'] === undefined) return false;
    if (!('defaultRateLimits' in value) || value['defaultRateLimits'] === undefined) return false;
    if (!('browserAccess' in value) || value['browserAccess'] === undefined) return false;
    if (!('anonymousAccess' in value) || value['anonymousAccess'] === undefined) return false;
    if (!('creditPolicy' in value) || value['creditPolicy'] === undefined) return false;
    if (!('createdAt' in value) || value['createdAt'] === undefined) return false;
    if (!('archivedAt' in value) || value['archivedAt'] === undefined) return false;
    return true;
}

export function AppFromJSON(json: any): App {
    return AppFromJSONTyped(json, false);
}

export function AppFromJSONTyped(json: any, ignoreDiscriminator: boolean): App {
    if (json == null) {
        return json;
    }
    return {

        'id': json['id'],
        'orgId': json['org_id'],
        'name': json['name'],
        'externalRef': json['external_ref'],
        'displayName': json['display_name'],
        'callbackTimeoutSeconds': json['callback_timeout_seconds'],
        'defaultRateLimits': AppDefaultRateLimitsFromJSON(json['default_rate_limits']),
        'browserAccess': BrowserAccessFromJSON(json['browser_access']),
        'anonymousAccess': AnonymousAccessFromJSON(json['anonymous_access']),
        'creditPolicy': CreditPolicyFromJSON(json['credit_policy']),
        'createdAt': (new Date(json['created_at'])),
        'archivedAt': (json['archived_at'] == null ? null : new Date(json['archived_at'])),
    };
}

export function AppToJSON(json: any): App {
    return AppToJSONTyped(json, false);
}

export function AppToJSONTyped(value?: App | null, ignoreDiscriminator: boolean = false): any {
    if (value == null) {
        return value;
    }

    return {

        'id': value['id'],
        'org_id': value['orgId'],
        'name': value['name'],
        'external_ref': value['externalRef'],
        'display_name': value['displayName'],
        'callback_timeout_seconds': value['callbackTimeoutSeconds'],
        'default_rate_limits': AppDefaultRateLimitsToJSON(value['defaultRateLimits']),
        'browser_access': BrowserAccessToJSON(value['browserAccess']),
        'anonymous_access': AnonymousAccessToJSON(value['anonymousAccess']),
        'credit_policy': CreditPolicyToJSON(value['creditPolicy']),
        'created_at': value['createdAt'].toISOString(),
        'archived_at': value['archivedAt'] == null ? value['archivedAt'] : value['archivedAt'].toISOString(),
    };
}
