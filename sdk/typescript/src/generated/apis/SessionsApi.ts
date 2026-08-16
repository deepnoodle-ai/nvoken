/* tslint:disable */
/* eslint-disable */
/**
 * nvoken API
 * nvoken runs agent turns for you. You describe a turn — an Agent Definition and some input — and nvoken queues it, runs it in the background, keeps running it across restarts and failures, and lets you either watch it live or come back for the result later.  Your application stays in charge of what your agents are and when they run. nvoken owns the conversation: it stores the messages, tracks the state of every turn, and handles talking to the model providers.  ## Getting started  `POST /v1/invocations` starts a turn and returns a `202` right away. From there:  - Follow it live with `GET /v1/sessions/{session_id}/stream`, or read   `GET /v1/invocations/{invocation_id}/result` whenever you want the   finished answer. Disconnecting never cancels anything. - If your agent uses tools you run yourself, the turn stops with status   `waiting` and lists what it needs. Run them, post the results to   `/tool-results`, and the turn continues where it left off. - Sessions carry conversation history from one turn to the next. They   last until you delete them, or until a retention window you set runs   out.  Also here: tools nvoken calls back to over HTTPS, remote MCP servers, structured output validated against your JSON Schema, reusable Agent Definitions, image and document input, your own model provider keys, and spending limits.  ## Authorization  Machine credentials use `nvk_…` bearer API keys. A configured trusted console may also present a short-lived Ed25519 issuer token; this is an authentication presentation only and does not create a user identity model in nvoken.  Browser-direct callers use narrow JWTs, exact-origin CORS, and App/tenant/user admission limits. A host may mint client tokens from its own Ed25519 key. An App that explicitly enables anonymous access may instead let nvoken mint a short-lived access token and renewable visitor token from one allowed public origin.  A browser grant sees less, and it sees it in the same shape. There is one schema per resource, and the fields a browser may not see are simply omitted from its responses, which is why those fields are not required. There are no parallel browser schemas and no response unions, so every payload decodes against one type and nothing about the shape has to be inferred from the credential that fetched it. `GET /v1/identity` reports which kind of caller you are, under `authentication.method`.  - App-scoped Runtime credentials may call every Runtime operation and GET /v1/identity. - App-scoped Viewer credentials may call Runtime reads and GET /v1/identity. - App-scoped Operator credentials may call every Runtime operation, GET /v1/identity, and all credential lifecycle operations. - Org-scoped Viewer and Operator credentials are management and reporting   identities only. They resolve no tenant and cannot perform Runtime   operations; Operators can register and manage Apps and their App   credentials, while Viewers have read-only access.  Tenant, Session, operation, and expiry constraints only narrow these grants.  ## Familiar names  Where a name already means something in other agent APIs, nvoken uses it the same way rather than inventing its own. `metadata` follows OpenAI\'s limits of 16 keys, 64-character names, and 512-byte values. `output_text` is the assistant\'s text joined into one string. `reasoning.effort` takes `low`, `medium`, `high`, `xhigh`, and `max`. `stop_reason: end_turn`, status `running`, and the `commentary` and `final_answer` message phases are the same idea you have seen elsewhere. If you have integrated another agent API, these should need no translation.  ## You can always read back what applied  Anything nvoken decides on your behalf is readable on the resource that used it. You never have to work out what happened by combining the request you sent with your own assumptions about nvoken\'s defaults — just read the resource.  A turn reports the `limits` it is really running under, after defaults and minimums; the `agent_definition` it ran with, exactly as stored; and `provenance`, which records what actually served the request. A Session reports its compaction (summarization) policy with `auto` already resolved to a real number and a real model, and its retention window as accepted. New settings will work the same way: a default you cannot read back is a setting only the server knows about.  ## Streaming  A turn and an Invocation are the same thing. The endpoint summaries say turn; the schemas say Invocation.  There is one stream: `GET /v1/sessions/{session_id}/stream`. It carries every turn in the Session, which is what a conversation needs. Add `invocation_id` to narrow it to one turn. That is a filter on one log, not a second endpoint, because cursors were always Session-scoped and a position from a filtered read resumes an unfiltered one.  Admission is separate from streaming. `POST /v1/invocations` returns the turn, and that response is your acknowledgment; then you open the stream. A dropped connection costs you nothing, because the turn already exists and you already hold its ID.  ### Saved frames and live frames  Every frame is one or the other, and the difference decides what you may store. A saved frame carries an SSE `id`. That ID is your resume position and the only value you need to keep. A live frame carries no `id`: it is a preview or a control signal, it is never stored, and it is never replayed.  `transcript.update` is the one saved frame. `message.delta`, `stream.resync`, and `stream.end` are live.  ### Folding a transcript.update  It carries `cursor`, `messages`, and `invocation_changes`. Messages append by `sequence` and are never sent twice. Changes are an append log keyed by `(invocation_id, revision)`; fold to the highest revision for current state. Within one frame apply messages before changes, so a turn is never marked settled before its final message exists.  ### Resuming and finishing  The resume position has one name: `cursor`. It is the field on a durable frame and the query parameter that resumes a stream. Server-Sent Events mirrors it onto the `id:` line and accepts the `Last-Event-ID` header in the parameter\'s place, because a faithful SSE binding must; those are the binding\'s mechanics, not two more names for the value, and another binding would carry the same one name. `cursor` wins when a request supplies both.  **A turn is over when a change for it carries a terminal status.** That is the terminal signal, and there is no other. It is saved, so it replays on reconnect like every other change, at any cursor. Read `GET /v1/invocations/{invocation_id}` if you want the composed result.  The change tells you so directly: `terminal` is true on exactly the change that ends the turn. Read it instead of testing `status` against a set of your own, so a status added later cannot silently change what your code believes a turn\'s end is.  `stream.end` never speaks about turns. It says this connection is closing and nothing more, so a client that has not seen its terminal change reconnects and keeps reading.  ### The stream stays open  An unfiltered stream is a subscription. It stays open while the Session is idle, keepalives and all, and a turn started later by anyone appears on it. You do not poll. A server may still reclaim an idle connection, and when it does it says `stream.end` with reason `idle`, which means reconnect when you next need to read rather than immediately.  A filtered stream closes once it has delivered that turn\'s terminal change. A client following the exit rule has already left.  ### Previews  `message.delta` previews a message the model is writing. `kind` says what kind of content it is and `delta` carries the fragment, for every kind. Accumulate by `(message_id, content_index)`, and discard everything provisional when `attempt` increases, when `stream.resync` arrives, when the saved message with that ID lands, and when the turn\'s change carries a terminal status. Never store preview text as a message, and never use it to decide whether a turn succeeded.  `attempt`, `revision`, and `sequence` count from 1. `content_index` counts from 0.  ### Compatibility  Stream events may gain fields over time. Ignore fields you do not recognize rather than refusing the frame. New enum values may appear too. Treat an unknown `message.delta` kind as content you do not render. Treat an unknown `stream.resync` reason as `live_delivery_gap` and discard your previews. Treat an unknown `stream.end` reason as `rotate` and reconnect with your last saved `id`. Reconnecting is always safe.  ### Transport  The protocol is the frames and their durability rules. Server-Sent Events is how they travel today and the only binding. Three mechanics belong to SSE itself: the `id:` line, the `retry:` opener, and comment keepalives. Everything else, resumption included, lives in the frames.  ### What the stream carries  Frames carry structure and text. Images and documents travel as descriptors, with media type, size in bytes, and a `sha256:` digest, never as inline bytes. Frame sizes stay bounded by text no matter what a turn produced.
 *
 * The version of the OpenAPI document: 0.1.0
 *
 *
 * NOTE: This class is auto generated by OpenAPI Generator (https://openapi-generator.tech).
 * https://openapi-generator.tech
 * Do not edit the class manually.
 */

import * as runtime from '../runtime.js';
import {
    type CreateSessionRequest,
    CreateSessionRequestFromJSON,
    CreateSessionRequestToJSON,
} from '../models/CreateSessionRequest.js';
import {
    type ErrorResponse,
    ErrorResponseFromJSON,
    ErrorResponseToJSON,
} from '../models/ErrorResponse.js';
import {
    type ForkSessionRequest,
    ForkSessionRequestFromJSON,
    ForkSessionRequestToJSON,
} from '../models/ForkSessionRequest.js';
import {
    type Session,
    SessionFromJSON,
    SessionToJSON,
} from '../models/Session.js';
import {
    type SessionCompactionList,
    SessionCompactionListFromJSON,
    SessionCompactionListToJSON,
} from '../models/SessionCompactionList.js';
import {
    type SessionList,
    SessionListFromJSON,
    SessionListToJSON,
} from '../models/SessionList.js';
import {
    type SessionMessageList,
    SessionMessageListFromJSON,
    SessionMessageListToJSON,
} from '../models/SessionMessageList.js';
import {
    type SessionStreamEvent,
    SessionStreamEventFromJSON,
    SessionStreamEventToJSON,
} from '../models/SessionStreamEvent.js';
import {
    type TranscriptSnapshot,
    TranscriptSnapshotFromJSON,
    TranscriptSnapshotToJSON,
} from '../models/TranscriptSnapshot.js';
import {
    type UpdateSessionRequest,
    UpdateSessionRequestFromJSON,
    UpdateSessionRequestToJSON,
} from '../models/UpdateSessionRequest.js';

export interface CreateSessionOperationRequest {
    createSessionRequest: CreateSessionRequest;
}

export interface DeleteSessionRequest {
    sessionId: string;
}

export interface ForkSessionOperationRequest {
    sessionId: string;
    forkSessionRequest: ForkSessionRequest;
}

export interface GetSessionRequest {
    sessionId: string;
}

export interface GetSessionTranscriptRequest {
    sessionId: string;
    cursor?: string;
    pageToken?: string;
    limit?: number;
}

export interface ListSessionCompactionsRequest {
    sessionId: string;
    cursor?: string;
    limit?: number;
}

export interface ListSessionMessagesRequest {
    sessionId: string;
    cursor?: string;
    limit?: number;
    order?: ListSessionMessagesOrderEnum;
}

export interface ListSessionsRequest {
    tenantKey?: string;
    defaultTenant?: boolean;
    userKey?: string;
    agentId?: string;
    agentKey?: string;
    sessionKey?: string;
    cursor?: string;
    limit?: number;
}

export interface StreamSessionRequest {
    sessionId: string;
    invocationId?: string;
    cursor?: string;
    deltas?: boolean;
    lastEventID?: string;
}

export interface UpdateSessionOperationRequest {
    sessionId: string;
    updateSessionRequest: UpdateSessionRequest;
}

/**
 *
 */
export class SessionsApi extends runtime.BaseAPI {

    /**
     * Creates request options for createSession without sending the request
     */
    async createSessionRequestOpts(requestParameters: CreateSessionOperationRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['createSessionRequest'] == null) {
            throw new runtime.RequiredError(
                'createSessionRequest',
                'Required parameter "createSessionRequest" was null or undefined when calling createSession().'
            );
        }

        const queryParameters: any = {};

        const headerParameters: runtime.HTTPHeaders = {};

        headerParameters['Content-Type'] = 'application/json';

        if (this.configuration && this.configuration.accessToken) {
            const token = this.configuration.accessToken;
            const tokenString = await token("bearerAuth", []);

            if (tokenString) {
                headerParameters["Authorization"] = `Bearer ${tokenString}`;
            }
        }

        let urlPath = `/v1/sessions`;

        return {
            path: urlPath,
            method: 'POST',
            headers: headerParameters,
            query: queryParameters,
            body: CreateSessionRequestToJSON(requestParameters['createSessionRequest']),
        };
    }

    /**
     * Creates an empty Session, optionally seeded with history you already have. Use this when you want a conversation to exist before the first turn runs — to show it in a UI, or to import messages from elsewhere.  Every field is optional. Leave out both `agent_id` and `agent_key` and the Session starts unbound: `agent_id` stays null until the first turn binds it permanently. A supplied Agent must already exist in the selected tenant.
     * Create or seed a Session without creating an Invocation
     */
    async createSessionRaw(requestParameters: CreateSessionOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<Session>> {
        const requestOptions = await this.createSessionRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => SessionFromJSON(jsonValue));
    }

    /**
     * Creates an empty Session, optionally seeded with history you already have. Use this when you want a conversation to exist before the first turn runs — to show it in a UI, or to import messages from elsewhere.  Every field is optional. Leave out both `agent_id` and `agent_key` and the Session starts unbound: `agent_id` stays null until the first turn binds it permanently. A supplied Agent must already exist in the selected tenant.
     * Create or seed a Session without creating an Invocation
     */
    async createSession(requestParameters: CreateSessionOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<Session> {
        const response = await this.createSessionRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for deleteSession without sending the request
     */
    async deleteSessionRequestOpts(requestParameters: DeleteSessionRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['sessionId'] == null) {
            throw new runtime.RequiredError(
                'sessionId',
                'Required parameter "sessionId" was null or undefined when calling deleteSession().'
            );
        }

        const queryParameters: any = {};

        const headerParameters: runtime.HTTPHeaders = {};

        if (this.configuration && this.configuration.accessToken) {
            const token = this.configuration.accessToken;
            const tokenString = await token("bearerAuth", []);

            if (tokenString) {
                headerParameters["Authorization"] = `Bearer ${tokenString}`;
            }
        }

        let urlPath = `/v1/sessions/{session_id}`;
        urlPath = urlPath.replace('{session_id}', encodeURIComponent(String(requestParameters['sessionId'])));

        return {
            path: urlPath,
            method: 'DELETE',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Removes the Session, its Invocations, transcript messages, checkpoints, tool calls, provider artifacts, compactions, provider-key and MCP bindings, and undelivered webhooks. The erasure is immediate and irreversible; a subsequent read is `not_found`.  A turn still running is stopped, but no cancellation is recorded — there is nothing left to record it against, and no `invocation.ended` webhook fires for it. If you need a record that the turn ended, cancel it and wait for its final state before deleting.  An unknown `session_id`, or one outside your scope, returns `not_found`. So if you lose the response and retry, you can safely treat `404` as \"already deleted\". Deleting requires the Runtime or Operator profile; a Viewer credential cannot erase a transcript.  **Deleting Sessions is not the same as deleting a user\'s account.** nvoken has no record that an account was deleted, so to honour a deletion request you must first stop starting new turns for that tenant, then page through `GET /v1/sessions` and delete until the list comes back empty. Otherwise a request arriving mid-sweep creates a new Session behind you.  Two consequences to plan for. Content-free Invocation, model-call, and tool-call facts remain for usage reporting, with the Invocation marked erased; prompts, responses, and tool payloads do not. The deleted turns\' idempotency keys become reusable, since deduplication only holds while the original turn still exists.
     * Erase a Session and everything under it
     */
    async deleteSessionRaw(requestParameters: DeleteSessionRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<void>> {
        const requestOptions = await this.deleteSessionRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.VoidApiResponse(response);
    }

    /**
     * Removes the Session, its Invocations, transcript messages, checkpoints, tool calls, provider artifacts, compactions, provider-key and MCP bindings, and undelivered webhooks. The erasure is immediate and irreversible; a subsequent read is `not_found`.  A turn still running is stopped, but no cancellation is recorded — there is nothing left to record it against, and no `invocation.ended` webhook fires for it. If you need a record that the turn ended, cancel it and wait for its final state before deleting.  An unknown `session_id`, or one outside your scope, returns `not_found`. So if you lose the response and retry, you can safely treat `404` as \"already deleted\". Deleting requires the Runtime or Operator profile; a Viewer credential cannot erase a transcript.  **Deleting Sessions is not the same as deleting a user\'s account.** nvoken has no record that an account was deleted, so to honour a deletion request you must first stop starting new turns for that tenant, then page through `GET /v1/sessions` and delete until the list comes back empty. Otherwise a request arriving mid-sweep creates a new Session behind you.  Two consequences to plan for. Content-free Invocation, model-call, and tool-call facts remain for usage reporting, with the Invocation marked erased; prompts, responses, and tool payloads do not. The deleted turns\' idempotency keys become reusable, since deduplication only holds while the original turn still exists.
     * Erase a Session and everything under it
     */
    async deleteSession(requestParameters: DeleteSessionRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<void> {
        await this.deleteSessionRaw(requestParameters, initOverrides);
    }

    /**
     * Creates request options for forkSession without sending the request
     */
    async forkSessionRequestOpts(requestParameters: ForkSessionOperationRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['sessionId'] == null) {
            throw new runtime.RequiredError(
                'sessionId',
                'Required parameter "sessionId" was null or undefined when calling forkSession().'
            );
        }

        if (requestParameters['forkSessionRequest'] == null) {
            throw new runtime.RequiredError(
                'forkSessionRequest',
                'Required parameter "forkSessionRequest" was null or undefined when calling forkSession().'
            );
        }

        const queryParameters: any = {};

        const headerParameters: runtime.HTTPHeaders = {};

        headerParameters['Content-Type'] = 'application/json';

        if (this.configuration && this.configuration.accessToken) {
            const token = this.configuration.accessToken;
            const tokenString = await token("bearerAuth", []);

            if (tokenString) {
                headerParameters["Authorization"] = `Bearer ${tokenString}`;
            }
        }

        let urlPath = `/v1/sessions/{session_id}/fork`;
        urlPath = urlPath.replace('{session_id}', encodeURIComponent(String(requestParameters['sessionId'])));

        return {
            path: urlPath,
            method: 'POST',
            headers: headerParameters,
            query: queryParameters,
            body: ForkSessionRequestToJSON(requestParameters['forkSessionRequest']),
        };
    }

    /**
     * Creates a new Session in the source Session\'s tenant and Agent scope, copying every canonical message through `from_message` inclusively. The source is untouched. The child stores durable Session and message lineage, but copied messages no longer belong to the source Invocations. Their `origin`, per-turn `user_key`, and resolved message phase are preserved.  Usage and compaction summaries are not copied. Child usage starts at zero and the child starts uncompacted. Retention and metadata come only from `session_options` on this request; no Session option is inherited. A `session_key` has the same tenant/Agent-scoped upsert behavior as Session creation.
     * Copy a Session prefix into a new Session
     */
    async forkSessionRaw(requestParameters: ForkSessionOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<Session>> {
        const requestOptions = await this.forkSessionRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => SessionFromJSON(jsonValue));
    }

    /**
     * Creates a new Session in the source Session\'s tenant and Agent scope, copying every canonical message through `from_message` inclusively. The source is untouched. The child stores durable Session and message lineage, but copied messages no longer belong to the source Invocations. Their `origin`, per-turn `user_key`, and resolved message phase are preserved.  Usage and compaction summaries are not copied. Child usage starts at zero and the child starts uncompacted. Retention and metadata come only from `session_options` on this request; no Session option is inherited. A `session_key` has the same tenant/Agent-scoped upsert behavior as Session creation.
     * Copy a Session prefix into a new Session
     */
    async forkSession(requestParameters: ForkSessionOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<Session> {
        const response = await this.forkSessionRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for getSession without sending the request
     */
    async getSessionRequestOpts(requestParameters: GetSessionRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['sessionId'] == null) {
            throw new runtime.RequiredError(
                'sessionId',
                'Required parameter "sessionId" was null or undefined when calling getSession().'
            );
        }

        const queryParameters: any = {};

        const headerParameters: runtime.HTTPHeaders = {};

        if (this.configuration && this.configuration.accessToken) {
            const token = this.configuration.accessToken;
            const tokenString = await token("bearerAuth", []);

            if (tokenString) {
                headerParameters["Authorization"] = `Bearer ${tokenString}`;
            }
        }

        let urlPath = `/v1/sessions/{session_id}`;
        urlPath = urlPath.replace('{session_id}', encodeURIComponent(String(requestParameters['sessionId'])));

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * An App credential without a tenant constraint may resolve a Session in any tenant partition in that App. A tenant-constrained credential resolves only Sessions in its partition. Missing, incompatible, and undisclosable resources use `not_found`; a credential denied the read operation itself receives `forbidden`.
     * Read authoritative Session identity and current state
     */
    async getSessionRaw(requestParameters: GetSessionRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<Session>> {
        const requestOptions = await this.getSessionRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => SessionFromJSON(jsonValue));
    }

    /**
     * An App credential without a tenant constraint may resolve a Session in any tenant partition in that App. A tenant-constrained credential resolves only Sessions in its partition. Missing, incompatible, and undisclosable resources use `not_found`; a credential denied the read operation itself receives `forbidden`.
     * Read authoritative Session identity and current state
     */
    async getSession(requestParameters: GetSessionRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<Session> {
        const response = await this.getSessionRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for getSessionTranscript without sending the request
     */
    async getSessionTranscriptRequestOpts(requestParameters: GetSessionTranscriptRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['sessionId'] == null) {
            throw new runtime.RequiredError(
                'sessionId',
                'Required parameter "sessionId" was null or undefined when calling getSessionTranscript().'
            );
        }

        const queryParameters: any = {};

        if (requestParameters['cursor'] != null) {
            queryParameters['cursor'] = requestParameters['cursor'];
        }

        if (requestParameters['pageToken'] != null) {
            queryParameters['page_token'] = requestParameters['pageToken'];
        }

        if (requestParameters['limit'] != null) {
            queryParameters['limit'] = requestParameters['limit'];
        }

        const headerParameters: runtime.HTTPHeaders = {};

        if (this.configuration && this.configuration.accessToken) {
            const token = this.configuration.accessToken;
            const tokenString = await token("bearerAuth", []);

            if (tokenString) {
                headerParameters["Authorization"] = `Bearer ${tokenString}`;
            }
        }

        let urlPath = `/v1/sessions/{session_id}/transcript`;
        urlPath = urlPath.replace('{session_id}', encodeURIComponent(String(requestParameters['sessionId'])));

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Returns the Session\'s stored messages plus a running log of turn state changes.  To catch up rather than re-read everything, pass a `cursor` you received earlier as `cursor` and you get only what is new since then. Within one read, keep passing `page_token` until `has_more` is false — all pages come from the same consistent snapshot, so the transcript cannot shift under you mid-read.
     * Drain a fixed-cut incremental transcript snapshot
     */
    async getSessionTranscriptRaw(requestParameters: GetSessionTranscriptRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<TranscriptSnapshot>> {
        const requestOptions = await this.getSessionTranscriptRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => TranscriptSnapshotFromJSON(jsonValue));
    }

    /**
     * Returns the Session\'s stored messages plus a running log of turn state changes.  To catch up rather than re-read everything, pass a `cursor` you received earlier as `cursor` and you get only what is new since then. Within one read, keep passing `page_token` until `has_more` is false — all pages come from the same consistent snapshot, so the transcript cannot shift under you mid-read.
     * Drain a fixed-cut incremental transcript snapshot
     */
    async getSessionTranscript(requestParameters: GetSessionTranscriptRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<TranscriptSnapshot> {
        const response = await this.getSessionTranscriptRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for listSessionCompactions without sending the request
     */
    async listSessionCompactionsRequestOpts(requestParameters: ListSessionCompactionsRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['sessionId'] == null) {
            throw new runtime.RequiredError(
                'sessionId',
                'Required parameter "sessionId" was null or undefined when calling listSessionCompactions().'
            );
        }

        const queryParameters: any = {};

        if (requestParameters['cursor'] != null) {
            queryParameters['cursor'] = requestParameters['cursor'];
        }

        if (requestParameters['limit'] != null) {
            queryParameters['limit'] = requestParameters['limit'];
        }

        const headerParameters: runtime.HTTPHeaders = {};

        if (this.configuration && this.configuration.accessToken) {
            const token = this.configuration.accessToken;
            const tokenString = await token("bearerAuth", []);

            if (tokenString) {
                headerParameters["Authorization"] = `Bearer ${tokenString}`;
            }
        }

        let urlPath = `/v1/sessions/{session_id}/compactions`;
        urlPath = urlPath.replace('{session_id}', encodeURIComponent(String(requestParameters['sessionId'])));

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Lists every attempt nvoken made to summarize this Session\'s history, newest first. Use it to understand why the model\'s context looks the way it does.  An `applied` record includes the summary that took effect and what the summarizing call cost. A `fell_through` record tells you why the attempt was not usable, and includes usage when a model call happened before it failed.
     * Page through immutable Session compaction records
     */
    async listSessionCompactionsRaw(requestParameters: ListSessionCompactionsRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<SessionCompactionList>> {
        const requestOptions = await this.listSessionCompactionsRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => SessionCompactionListFromJSON(jsonValue));
    }

    /**
     * Lists every attempt nvoken made to summarize this Session\'s history, newest first. Use it to understand why the model\'s context looks the way it does.  An `applied` record includes the summary that took effect and what the summarizing call cost. A `fell_through` record tells you why the attempt was not usable, and includes usage when a model call happened before it failed.
     * Page through immutable Session compaction records
     */
    async listSessionCompactions(requestParameters: ListSessionCompactionsRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<SessionCompactionList> {
        const response = await this.listSessionCompactionsRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for listSessionMessages without sending the request
     */
    async listSessionMessagesRequestOpts(requestParameters: ListSessionMessagesRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['sessionId'] == null) {
            throw new runtime.RequiredError(
                'sessionId',
                'Required parameter "sessionId" was null or undefined when calling listSessionMessages().'
            );
        }

        const queryParameters: any = {};

        if (requestParameters['cursor'] != null) {
            queryParameters['cursor'] = requestParameters['cursor'];
        }

        if (requestParameters['limit'] != null) {
            queryParameters['limit'] = requestParameters['limit'];
        }

        if (requestParameters['order'] != null) {
            queryParameters['order'] = requestParameters['order'];
        }

        const headerParameters: runtime.HTTPHeaders = {};

        if (this.configuration && this.configuration.accessToken) {
            const token = this.configuration.accessToken;
            const tokenString = await token("bearerAuth", []);

            if (tokenString) {
                headerParameters["Authorization"] = `Bearer ${tokenString}`;
            }
        }

        let urlPath = `/v1/sessions/{session_id}/messages`;
        urlPath = urlPath.replace('{session_id}', encodeURIComponent(String(requestParameters['sessionId'])));

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Returns persisted SessionMessage rows in sequence order, ascending by default. The opaque cursor is bound to the authenticated caller, the Session, and the direction it was issued for. This history endpoint contains no lifecycle or live-preview copies.  Use `order=desc` to read the newest messages first. A conversation\'s interesting end is its recent end, and reaching it ascending costs a walk through every older message: the tail of a three thousand message Session is one page descending and fifteen round trips ascending.
     * Page through the canonical Session transcript
     */
    async listSessionMessagesRaw(requestParameters: ListSessionMessagesRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<SessionMessageList>> {
        const requestOptions = await this.listSessionMessagesRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => SessionMessageListFromJSON(jsonValue));
    }

    /**
     * Returns persisted SessionMessage rows in sequence order, ascending by default. The opaque cursor is bound to the authenticated caller, the Session, and the direction it was issued for. This history endpoint contains no lifecycle or live-preview copies.  Use `order=desc` to read the newest messages first. A conversation\'s interesting end is its recent end, and reaching it ascending costs a walk through every older message: the tail of a three thousand message Session is one page descending and fifteen round trips ascending.
     * Page through the canonical Session transcript
     */
    async listSessionMessages(requestParameters: ListSessionMessagesRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<SessionMessageList> {
        const response = await this.listSessionMessagesRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for listSessions without sending the request
     */
    async listSessionsRequestOpts(requestParameters: ListSessionsRequest): Promise<runtime.RequestOpts> {
        const queryParameters: any = {};

        if (requestParameters['tenantKey'] != null) {
            queryParameters['tenant_key'] = requestParameters['tenantKey'];
        }

        if (requestParameters['defaultTenant'] != null) {
            queryParameters['default_tenant'] = requestParameters['defaultTenant'];
        }

        if (requestParameters['userKey'] != null) {
            queryParameters['user_key'] = requestParameters['userKey'];
        }

        if (requestParameters['agentId'] != null) {
            queryParameters['agent_id'] = requestParameters['agentId'];
        }

        if (requestParameters['agentKey'] != null) {
            queryParameters['agent_key'] = requestParameters['agentKey'];
        }

        if (requestParameters['sessionKey'] != null) {
            queryParameters['session_key'] = requestParameters['sessionKey'];
        }

        if (requestParameters['cursor'] != null) {
            queryParameters['cursor'] = requestParameters['cursor'];
        }

        if (requestParameters['limit'] != null) {
            queryParameters['limit'] = requestParameters['limit'];
        }

        const headerParameters: runtime.HTTPHeaders = {};

        if (this.configuration && this.configuration.accessToken) {
            const token = this.configuration.accessToken;
            const tokenString = await token("bearerAuth", []);

            if (tokenString) {
                headerParameters["Authorization"] = `Bearer ${tokenString}`;
            }
        }

        let urlPath = `/v1/sessions`;

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Lists Sessions, newest first, each with the state of its currently running turn if it has one. Filters combine with AND. Tenant filtering and cursors work the same as on the Invocation list. `agent_id` and `agent_key` are mutually exclusive.
     * List authoritative Sessions
     */
    async listSessionsRaw(requestParameters: ListSessionsRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<SessionList>> {
        const requestOptions = await this.listSessionsRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => SessionListFromJSON(jsonValue));
    }

    /**
     * Lists Sessions, newest first, each with the state of its currently running turn if it has one. Filters combine with AND. Tenant filtering and cursors work the same as on the Invocation list. `agent_id` and `agent_key` are mutually exclusive.
     * List authoritative Sessions
     */
    async listSessions(requestParameters: ListSessionsRequest = {}, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<SessionList> {
        const response = await this.listSessionsRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for streamSession without sending the request
     */
    async streamSessionRequestOpts(requestParameters: StreamSessionRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['sessionId'] == null) {
            throw new runtime.RequiredError(
                'sessionId',
                'Required parameter "sessionId" was null or undefined when calling streamSession().'
            );
        }

        const queryParameters: any = {};

        if (requestParameters['invocationId'] != null) {
            queryParameters['invocation_id'] = requestParameters['invocationId'];
        }

        if (requestParameters['cursor'] != null) {
            queryParameters['cursor'] = requestParameters['cursor'];
        }

        if (requestParameters['deltas'] != null) {
            queryParameters['deltas'] = requestParameters['deltas'];
        }

        const headerParameters: runtime.HTTPHeaders = {};

        if (requestParameters['lastEventID'] != null) {
            headerParameters['Last-Event-ID'] = String(requestParameters['lastEventID']);
        }

        if (this.configuration && this.configuration.accessToken) {
            const token = this.configuration.accessToken;
            const tokenString = await token("bearerAuth", []);

            if (tokenString) {
                headerParameters["Authorization"] = `Bearer ${tokenString}`;
            }
        }

        let urlPath = `/v1/sessions/{session_id}/stream`;
        urlPath = urlPath.replace('{session_id}', encodeURIComponent(String(requestParameters['sessionId'])));

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * The one stream. It carries the Session\'s messages and the lifecycle changes of every turn in it, live, and can be resumed after a dropped connection. It covers the same records as the JSON transcript endpoint.  Every non-empty `transcript.update` frame carries `id: <cursor>`. That opaque ID is your resume position and the only value you need to store — reconnect with it and you continue exactly where you left off. `message.delta`, `stream.resync`, and `stream.end` never carry an `id`, because they are live previews and control frames rather than saved records.  Previews can be lost. If you receive `stream.resync`, discard the preview text you have accumulated and wait for the saved messages to arrive. Set `deltas=false` to skip previews entirely; nothing about replay, resumption, or how the stream ends changes.  ## Following one turn  Pass `invocation_id` and every frame is narrowed to that turn: messages it produced, its lifecycle changes, its previews. The connection closes once that turn\'s terminal change has been delivered. Cursors are Session-scoped either way, so a position taken from a filtered read resumes an unfiltered one and the other way round.  Without `invocation_id` this is a subscription. It stays open while the Session is idle and a turn started later by anyone appears on it, so there is nothing to poll.  ## Knowing a turn is over  A turn is over when an `invocation_changes` entry for it carries a terminal status. That is the signal, and there is no other. It is saved, so it replays at any cursor. Read `GET /v1/invocations/{invocation_id}` for the composed result.  `stream.end` is about this connection and never about a turn. Reason `rotate` means the server is cycling the connection, so reconnect now with your last `cursor`. Reason `idle` means it is reclaiming an idle connection, so reconnect when you next need to read; nothing is lost while you are away. Reason `slow_consumer` means this connection could not keep up. A connection that just drops carries no meaning: reconnect and resume. Disconnecting never cancels a running turn.  ## Mechanics  The `cursor` query parameter wins over the `Last-Event-ID` header. Because this endpoint uses bearer authentication, you need an SSE client that can set the `Authorization` header — the browser\'s built-in `EventSource` cannot. The server suggests a 1000 ms reconnect delay.  This stream is strictly forward: a message past your cursor is never sent again. A message\'s `phase` is worked out when it is read, so this stream is not the place to learn which message was the answer. Derive that instead from facts you already hold: a turn has a final answer only once it settled `completed` with stop reason `end_turn`, and that answer is the turn\'s last assistant message.  Browser and machine callers receive the same frame types, including `thinking` previews. A browser payload carries fewer fields on the same schema.
     * Follow a Session over Server-Sent Events
     */
    async streamSessionRaw(requestParameters: StreamSessionRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<SessionStreamEvent>> {
        const requestOptions = await this.streamSessionRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => SessionStreamEventFromJSON(jsonValue));
    }

    /**
     * The one stream. It carries the Session\'s messages and the lifecycle changes of every turn in it, live, and can be resumed after a dropped connection. It covers the same records as the JSON transcript endpoint.  Every non-empty `transcript.update` frame carries `id: <cursor>`. That opaque ID is your resume position and the only value you need to store — reconnect with it and you continue exactly where you left off. `message.delta`, `stream.resync`, and `stream.end` never carry an `id`, because they are live previews and control frames rather than saved records.  Previews can be lost. If you receive `stream.resync`, discard the preview text you have accumulated and wait for the saved messages to arrive. Set `deltas=false` to skip previews entirely; nothing about replay, resumption, or how the stream ends changes.  ## Following one turn  Pass `invocation_id` and every frame is narrowed to that turn: messages it produced, its lifecycle changes, its previews. The connection closes once that turn\'s terminal change has been delivered. Cursors are Session-scoped either way, so a position taken from a filtered read resumes an unfiltered one and the other way round.  Without `invocation_id` this is a subscription. It stays open while the Session is idle and a turn started later by anyone appears on it, so there is nothing to poll.  ## Knowing a turn is over  A turn is over when an `invocation_changes` entry for it carries a terminal status. That is the signal, and there is no other. It is saved, so it replays at any cursor. Read `GET /v1/invocations/{invocation_id}` for the composed result.  `stream.end` is about this connection and never about a turn. Reason `rotate` means the server is cycling the connection, so reconnect now with your last `cursor`. Reason `idle` means it is reclaiming an idle connection, so reconnect when you next need to read; nothing is lost while you are away. Reason `slow_consumer` means this connection could not keep up. A connection that just drops carries no meaning: reconnect and resume. Disconnecting never cancels a running turn.  ## Mechanics  The `cursor` query parameter wins over the `Last-Event-ID` header. Because this endpoint uses bearer authentication, you need an SSE client that can set the `Authorization` header — the browser\'s built-in `EventSource` cannot. The server suggests a 1000 ms reconnect delay.  This stream is strictly forward: a message past your cursor is never sent again. A message\'s `phase` is worked out when it is read, so this stream is not the place to learn which message was the answer. Derive that instead from facts you already hold: a turn has a final answer only once it settled `completed` with stop reason `end_turn`, and that answer is the turn\'s last assistant message.  Browser and machine callers receive the same frame types, including `thinking` previews. A browser payload carries fewer fields on the same schema.
     * Follow a Session over Server-Sent Events
     */
    async streamSession(requestParameters: StreamSessionRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<SessionStreamEvent> {
        const response = await this.streamSessionRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for updateSession without sending the request
     */
    async updateSessionRequestOpts(requestParameters: UpdateSessionOperationRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['sessionId'] == null) {
            throw new runtime.RequiredError(
                'sessionId',
                'Required parameter "sessionId" was null or undefined when calling updateSession().'
            );
        }

        if (requestParameters['updateSessionRequest'] == null) {
            throw new runtime.RequiredError(
                'updateSessionRequest',
                'Required parameter "updateSessionRequest" was null or undefined when calling updateSession().'
            );
        }

        const queryParameters: any = {};

        const headerParameters: runtime.HTTPHeaders = {};

        headerParameters['Content-Type'] = 'application/json';

        if (this.configuration && this.configuration.accessToken) {
            const token = this.configuration.accessToken;
            const tokenString = await token("bearerAuth", []);

            if (tokenString) {
                headerParameters["Authorization"] = `Bearer ${tokenString}`;
            }
        }

        let urlPath = `/v1/sessions/{session_id}`;
        urlPath = urlPath.replace('{session_id}', encodeURIComponent(String(requestParameters['sessionId'])));

        return {
            path: urlPath,
            method: 'PATCH',
            headers: headerParameters,
            query: queryParameters,
            body: UpdateSessionRequestToJSON(requestParameters['updateSessionRequest']),
        };
    }

    /**
     * Merges host metadata into the Session. A present key replaces its value, an explicit `null` deletes that key, and a key the patch does not mention survives.  Merge rather than replace, because independent writers share this map — a conversation UI writing a title, correlation tooling writing a trace id — and a full replacement would make each silently discard the other\'s keys. The merge happens under the Session lock, so two concurrent patches compose instead of one overwriting the other\'s read.  `\"metadata\": null` is refused rather than guessed at: it could mean \"clear everything\" or \"leave it alone\", and either reading is destructive or silent. Delete keys one at a time.  Bounds apply to the merged result, not to the patch, so a patch that deletes as many keys as it adds is not refused for a count it never produces. Requires the `update_session` operation.
     * Update a Session
     */
    async updateSessionRaw(requestParameters: UpdateSessionOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<Session>> {
        const requestOptions = await this.updateSessionRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => SessionFromJSON(jsonValue));
    }

    /**
     * Merges host metadata into the Session. A present key replaces its value, an explicit `null` deletes that key, and a key the patch does not mention survives.  Merge rather than replace, because independent writers share this map — a conversation UI writing a title, correlation tooling writing a trace id — and a full replacement would make each silently discard the other\'s keys. The merge happens under the Session lock, so two concurrent patches compose instead of one overwriting the other\'s read.  `\"metadata\": null` is refused rather than guessed at: it could mean \"clear everything\" or \"leave it alone\", and either reading is destructive or silent. Delete keys one at a time.  Bounds apply to the merged result, not to the patch, so a patch that deletes as many keys as it adds is not refused for a count it never produces. Requires the `update_session` operation.
     * Update a Session
     */
    async updateSession(requestParameters: UpdateSessionOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<Session> {
        const response = await this.updateSessionRaw(requestParameters, initOverrides);
        return await response.value();
    }

}

/**
 * @export
 */
export const ListSessionMessagesOrderEnum = {
    ListOrderAscending: 'asc',
    ListOrderDescending: 'desc'
} as const;
export type ListSessionMessagesOrderEnum = typeof ListSessionMessagesOrderEnum[keyof typeof ListSessionMessagesOrderEnum];
