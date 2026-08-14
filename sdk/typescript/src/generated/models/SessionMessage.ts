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
import type { SessionContentBlock } from './SessionContentBlock.js';
import {
    SessionContentBlockFromJSON,
    SessionContentBlockFromJSONTyped,
    SessionContentBlockToJSON,
    SessionContentBlockToJSONTyped,
} from './SessionContentBlock.js';
import type { SessionMessageRole } from './SessionMessageRole.js';
import {
    SessionMessageRoleFromJSON,
    SessionMessageRoleFromJSONTyped,
    SessionMessageRoleToJSON,
    SessionMessageRoleToJSONTyped,
} from './SessionMessageRole.js';
import type { MessagePhase } from './MessagePhase.js';
import {
    MessagePhaseFromJSON,
    MessagePhaseFromJSONTyped,
    MessagePhaseToJSON,
    MessagePhaseToJSONTyped,
} from './MessagePhase.js';

/**
 *
 * @export
 * @interface SessionMessage
 */
export interface SessionMessage {
    /**
     * Opaque identifier with the public `msg_` prefix. Treat the body as opaque.
     * @type {string}
     * @memberof SessionMessage
     */
    id: string;
    /**
     * Opaque identifier with the public `sess_` prefix. Treat the body as opaque.
     * @type {string}
     * @memberof SessionMessage
     */
    sessionId: string;
    /**
     * Opaque identifier with the public `agent_` prefix. Treat the body as opaque.
     * @type {string}
     * @memberof SessionMessage
     */
    agentId: string;
    /**
     * The Invocation that wrote this row, or null for seeded and copied
     * history. `origin` and `copied_from_message_id` explain that null.
     *
     * @type {string}
     * @memberof SessionMessage
     */
    invocationId: string | null;
    /**
     * Per-turn host end-user attribution. Copied rows preserve it;
     * seeded history may omit it.
     *
     * @type {string}
     * @memberof SessionMessage
     */
    userKey: string | null;
    /**
     *
     * @type {number}
     * @memberof SessionMessage
     */
    sequence: number;
    /**
     *
     * @type {SessionMessageRole}
     * @memberof SessionMessage
     */
    role: SessionMessageRole;
    /**
     *
     * @type {Array<SessionContentBlock>}
     * @memberof SessionMessage
     */
    content: Array<SessionContentBlock>;
    /**
     * Present only when the message content was asserted by the host at
     * Session creation. Messages nvoken wrote carry no origin marker;
     * fork copies preserve the source marker.
     *
     * @type {SessionMessageOriginEnum}
     * @memberof SessionMessage
     */
    origin?: SessionMessageOriginEnum;
    /**
     * Present only on a fork copy; names its source message.
     * @type {string}
     * @memberof SessionMessage
     */
    copiedFromMessageId?: string;
    /**
     * Whether this assistant message is the turn's actual answer or something
     * along the way. Absent on user and tool messages, which have no phase.
     *
     * It is worked out when you read, from how the turn ended, and it is
     * frozen onto forked copies so a fork keeps the phases it had. Your own
     * input never carries a phase.
     *
     * Because it is worked out on read, it is authoritative on ordinary
     * reads and on `invocation.result`, and not on the Session
     * transcript stream: a message delivered there before its turn
     * settled carries `commentary` forever, and no later frame corrects
     * it. On that stream, derive the answer instead. A turn has a final
     * answer only once it settled `completed` with stop reason
     * `end_turn`, and that answer is the turn's last assistant message.
     * That is the same rule nvoken applies.
     *
     * @type {MessagePhase}
     * @memberof SessionMessage
     */
    phase?: MessagePhase;
    /**
     *
     * @type {Date}
     * @memberof SessionMessage
     */
    createdAt: Date;
}


/**
 * @export
 */
export const SessionMessageOriginEnum = {
    Seeded: 'seeded'
} as const;
export type SessionMessageOriginEnum = typeof SessionMessageOriginEnum[keyof typeof SessionMessageOriginEnum];


/**
 * Check if a given object implements the SessionMessage interface.
 */
export function instanceOfSessionMessage(value: object): value is SessionMessage {
    if (!('id' in value) || value['id'] === undefined) return false;
    if (!('sessionId' in value) || value['sessionId'] === undefined) return false;
    if (!('agentId' in value) || value['agentId'] === undefined) return false;
    if (!('invocationId' in value) || value['invocationId'] === undefined) return false;
    if (!('userKey' in value) || value['userKey'] === undefined) return false;
    if (!('sequence' in value) || value['sequence'] === undefined) return false;
    if (!('role' in value) || value['role'] === undefined) return false;
    if (!('content' in value) || value['content'] === undefined) return false;
    if (!('createdAt' in value) || value['createdAt'] === undefined) return false;
    return true;
}

export function SessionMessageFromJSON(json: any): SessionMessage {
    return SessionMessageFromJSONTyped(json, false);
}

export function SessionMessageFromJSONTyped(json: any, ignoreDiscriminator: boolean): SessionMessage {
    if (json == null) {
        return json;
    }
    return {

        'id': json['id'],
        'sessionId': json['session_id'],
        'agentId': json['agent_id'],
        'invocationId': json['invocation_id'],
        'userKey': json['user_key'],
        'sequence': json['sequence'],
        'role': SessionMessageRoleFromJSON(json['role']),
        'content': ((json['content'] as Array<any>).map(SessionContentBlockFromJSON)),
        'origin': json['origin'] == null ? undefined : json['origin'],
        'copiedFromMessageId': json['copied_from_message_id'] == null ? undefined : json['copied_from_message_id'],
        'phase': json['phase'] == null ? undefined : MessagePhaseFromJSON(json['phase']),
        'createdAt': (new Date(json['created_at'])),
    };
}

export function SessionMessageToJSON(json: any): SessionMessage {
    return SessionMessageToJSONTyped(json, false);
}

export function SessionMessageToJSONTyped(value?: SessionMessage | null, ignoreDiscriminator: boolean = false): any {
    if (value == null) {
        return value;
    }

    return {

        'id': value['id'],
        'session_id': value['sessionId'],
        'agent_id': value['agentId'],
        'invocation_id': value['invocationId'],
        'user_key': value['userKey'],
        'sequence': value['sequence'],
        'role': SessionMessageRoleToJSON(value['role']),
        'content': ((value['content'] as Array<any>).map(SessionContentBlockToJSON)),
        'origin': value['origin'],
        'copied_from_message_id': value['copiedFromMessageId'],
        'phase': MessagePhaseToJSON(value['phase']),
        'created_at': value['createdAt'].toISOString(),
    };
}
