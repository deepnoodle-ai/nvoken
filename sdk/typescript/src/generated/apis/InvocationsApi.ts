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

import * as runtime from '../runtime.js';
import {
    type CreateInvocationRequest,
    CreateInvocationRequestFromJSON,
    CreateInvocationRequestToJSON,
} from '../models/CreateInvocationRequest.js';
import {
    type CreateNudgeRequest,
    CreateNudgeRequestFromJSON,
    CreateNudgeRequestToJSON,
} from '../models/CreateNudgeRequest.js';
import {
    type ErrorResponse,
    ErrorResponseFromJSON,
    ErrorResponseToJSON,
} from '../models/ErrorResponse.js';
import {
    type Invocation,
    InvocationFromJSON,
    InvocationToJSON,
} from '../models/Invocation.js';
import {
    type InvocationList,
    InvocationListFromJSON,
    InvocationListToJSON,
} from '../models/InvocationList.js';
import {
    type InvocationLogList,
    InvocationLogListFromJSON,
    InvocationLogListToJSON,
} from '../models/InvocationLogList.js';
import {
    type InvocationResult,
    InvocationResultFromJSON,
    InvocationResultToJSON,
} from '../models/InvocationResult.js';
import {
    type InvocationStatus,
    InvocationStatusFromJSON,
    InvocationStatusToJSON,
} from '../models/InvocationStatus.js';
import {
    type InvocationTimeline,
    InvocationTimelineFromJSON,
    InvocationTimelineToJSON,
} from '../models/InvocationTimeline.js';
import {
    type Nudge,
    NudgeFromJSON,
    NudgeToJSON,
} from '../models/Nudge.js';
import {
    type NudgeAcknowledgement,
    NudgeAcknowledgementFromJSON,
    NudgeAcknowledgementToJSON,
} from '../models/NudgeAcknowledgement.js';
import {
    type NudgeList,
    NudgeListFromJSON,
    NudgeListToJSON,
} from '../models/NudgeList.js';
import {
    type NudgeStatus,
    NudgeStatusFromJSON,
    NudgeStatusToJSON,
} from '../models/NudgeStatus.js';
import {
    type ResumeInvocationRequest,
    ResumeInvocationRequestFromJSON,
    ResumeInvocationRequestToJSON,
} from '../models/ResumeInvocationRequest.js';
import {
    type SubmitHostToolResultsRequest,
    SubmitHostToolResultsRequestFromJSON,
    SubmitHostToolResultsRequestToJSON,
} from '../models/SubmitHostToolResultsRequest.js';
import {
    type SubmitHostToolResultsResponse,
    SubmitHostToolResultsResponseFromJSON,
    SubmitHostToolResultsResponseToJSON,
} from '../models/SubmitHostToolResultsResponse.js';
import {
    type ToolCallList,
    ToolCallListFromJSON,
    ToolCallListToJSON,
} from '../models/ToolCallList.js';
import {
    type Trace,
    TraceFromJSON,
    TraceToJSON,
} from '../models/Trace.js';
import {
    type TraceList,
    TraceListFromJSON,
    TraceListToJSON,
} from '../models/TraceList.js';

export interface CancelInvocationRequest {
    invocationId: string;
}

export interface CancelNudgeRequest {
    invocationId: string;
    nudgeId: string;
}

export interface CreateInvocationOperationRequest {
    createInvocationRequest: CreateInvocationRequest;
    xAnthropicApiKey?: string;
    xOpenaiApiKey?: string;
    xGeminiApiKey?: string;
    xXaiApiKey?: string;
}

export interface CreateNudgeOperationRequest {
    invocationId: string;
    createNudgeRequest: CreateNudgeRequest;
}

export interface GetInvocationRequest {
    invocationId: string;
}

export interface GetInvocationResultRequest {
    invocationId: string;
}

export interface GetInvocationTimelineRequest {
    invocationId: string;
}

export interface GetTraceRequest {
    traceId: string;
}

export interface InterruptInvocationRequest {
    invocationId: string;
}

export interface ListInvocationLogsRequest {
    invocationId: string;
    cursor?: string;
    limit?: number;
    traceId?: string;
}

export interface ListInvocationTracesRequest {
    invocationId: string;
    cursor?: string;
    limit?: number;
}

export interface ListInvocationsRequest {
    tenantKey?: string;
    defaultTenant?: boolean;
    userKey?: string;
    sessionId?: string;
    agentId?: string;
    agentKey?: string;
    status?: Array<InvocationStatus>;
    ended?: boolean;
    endedSince?: Date;
    cursor?: string;
    limit?: number;
}

export interface ListNudgesRequest {
    invocationId: string;
    status?: NudgeStatus;
    cursor?: string;
    limit?: number;
}

export interface ListToolCallsRequest {
    invocationId: string;
    cursor?: string;
    limit?: number;
}

export interface ResumeInvocationOperationRequest {
    invocationId: string;
    resumeInvocationRequest: ResumeInvocationRequest;
}

export interface SubmitHostToolResultsOperationRequest {
    invocationId: string;
    submitHostToolResultsRequest: SubmitHostToolResultsRequest;
}

/**
 *
 */
export class InvocationsApi extends runtime.BaseAPI {

    /**
     * Creates request options for cancelInvocation without sending the request
     */
    async cancelInvocationRequestOpts(requestParameters: CancelInvocationRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['invocationId'] == null) {
            throw new runtime.RequiredError(
                'invocationId',
                'Required parameter "invocationId" was null or undefined when calling cancelInvocation().'
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

        let urlPath = `/v1/invocations/{invocation_id}/cancel`;
        urlPath = urlPath.replace('{invocation_id}', encodeURIComponent(String(requestParameters['invocationId'])));

        return {
            path: urlPath,
            method: 'POST',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Stops a turn and discards what it produced. The turn ends `cancelled` and its work does not carry into the next turn — use interrupt instead if you want to keep it.  Safe to repeat. Cancelling a turn that already finished returns it unchanged rather than failing. A successful response means the cancellation is recorded and will stick. Work already sent to the model provider stops as soon as it can, so you may still be billed for what had run by then.  Send an empty request body.
     * Stop an Invocation and discard its work
     */
    async cancelInvocationRaw(requestParameters: CancelInvocationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<Invocation>> {
        const requestOptions = await this.cancelInvocationRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => InvocationFromJSON(jsonValue));
    }

    /**
     * Stops a turn and discards what it produced. The turn ends `cancelled` and its work does not carry into the next turn — use interrupt instead if you want to keep it.  Safe to repeat. Cancelling a turn that already finished returns it unchanged rather than failing. A successful response means the cancellation is recorded and will stick. Work already sent to the model provider stops as soon as it can, so you may still be billed for what had run by then.  Send an empty request body.
     * Stop an Invocation and discard its work
     */
    async cancelInvocation(requestParameters: CancelInvocationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<Invocation> {
        const response = await this.cancelInvocationRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for cancelNudge without sending the request
     */
    async cancelNudgeRequestOpts(requestParameters: CancelNudgeRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['invocationId'] == null) {
            throw new runtime.RequiredError(
                'invocationId',
                'Required parameter "invocationId" was null or undefined when calling cancelNudge().'
            );
        }

        if (requestParameters['nudgeId'] == null) {
            throw new runtime.RequiredError(
                'nudgeId',
                'Required parameter "nudgeId" was null or undefined when calling cancelNudge().'
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

        let urlPath = `/v1/invocations/{invocation_id}/nudges/{nudge_id}/cancel`;
        urlPath = urlPath.replace('{invocation_id}', encodeURIComponent(String(requestParameters['invocationId'])));
        urlPath = urlPath.replace('{nudge_id}', encodeURIComponent(String(requestParameters['nudgeId'])));

        return {
            path: urlPath,
            method: 'POST',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Withdraws direction you sent with `/nudges`, as long as the turn has not picked it up yet. Cancelling something already cancelled returns it unchanged, so retrying is safe.  Cancelling races the turn, and whichever happens first wins outright: you either withdraw it cleanly or the turn uses it. It is never half-applied. If the turn got there first, you get a conflict and the entry stays `drained`.
     * Withdraw a Nudge the Invocation has not taken
     */
    async cancelNudgeRaw(requestParameters: CancelNudgeRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<Nudge>> {
        const requestOptions = await this.cancelNudgeRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => NudgeFromJSON(jsonValue));
    }

    /**
     * Withdraws direction you sent with `/nudges`, as long as the turn has not picked it up yet. Cancelling something already cancelled returns it unchanged, so retrying is safe.  Cancelling races the turn, and whichever happens first wins outright: you either withdraw it cleanly or the turn uses it. It is never half-applied. If the turn got there first, you get a conflict and the entry stays `drained`.
     * Withdraw a Nudge the Invocation has not taken
     */
    async cancelNudge(requestParameters: CancelNudgeRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<Nudge> {
        const response = await this.cancelNudgeRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for createInvocation without sending the request
     */
    async createInvocationRequestOpts(requestParameters: CreateInvocationOperationRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['createInvocationRequest'] == null) {
            throw new runtime.RequiredError(
                'createInvocationRequest',
                'Required parameter "createInvocationRequest" was null or undefined when calling createInvocation().'
            );
        }

        const queryParameters: any = {};

        const headerParameters: runtime.HTTPHeaders = {};

        headerParameters['Content-Type'] = 'application/json';

        if (requestParameters['xAnthropicApiKey'] != null) {
            headerParameters['X-Anthropic-Api-Key'] = String(requestParameters['xAnthropicApiKey']);
        }

        if (requestParameters['xOpenaiApiKey'] != null) {
            headerParameters['X-Openai-Api-Key'] = String(requestParameters['xOpenaiApiKey']);
        }

        if (requestParameters['xGeminiApiKey'] != null) {
            headerParameters['X-Gemini-Api-Key'] = String(requestParameters['xGeminiApiKey']);
        }

        if (requestParameters['xXaiApiKey'] != null) {
            headerParameters['X-Xai-Api-Key'] = String(requestParameters['xXaiApiKey']);
        }

        if (this.configuration && this.configuration.accessToken) {
            const token = this.configuration.accessToken;
            const tokenString = await token("bearerAuth", []);

            if (tokenString) {
                headerParameters["Authorization"] = `Bearer ${tokenString}`;
            }
        }

        let urlPath = `/v1/invocations`;

        return {
            path: urlPath,
            method: 'POST',
            headers: headerParameters,
            query: queryParameters,
            body: CreateInvocationRequestToJSON(requestParameters['createInvocationRequest']),
        };
    }

    /**
     * Starts one agent turn and returns immediately. In a single database transaction nvoken resolves the deliberately created Agent, selects its Agent Definition revision, finds or creates the Session, appends your input as one message, and queues the turn. Admission never creates an Agent or reusable configuration. You get a response only after that transaction commits, so a `202` means the work is safely recorded and will run even if nvoken restarts. The model does not run on this request — it runs in the background, and you follow it with the stream or by polling.  Pick the Session with either `session_id` or `session_key`, not both. A Session ID must belong to the Agent you named, or to a Session created without an Agent — in which case this turn binds that Agent permanently. An App credential without a tenant constraint may omit `tenant_key` and use whichever tenant the Session already belongs to. A credential locked to one tenant cannot reach another; naming a different one returns `403 forbidden` without revealing whether the resource exists.  ## Retrying safely  Send `idempotency_key` and you can retry this request without risking a second turn. A repeat with the same key returns the original turn and does not add your input again, even if that turn has already finished. Keys are scoped to the tenant and Agent.  A repeat counts as the same request only if the Session selector, the Agent, explicit revision, per-turn overrides, `metadata`, `context`, `webhook`, `on_budget_exhausted`, and input all match. The original admitted revision is returned even if its Definition has advanced. Values are compared as sent, so omitting an override is not the same as supplying one that happens to equal the Definition. Key order inside JSON objects does not matter; array order does. Change anything material and you get `idempotency_conflict` rather than a surprise second turn.  `user_key` is the one exception, because it is the Session\'s rather than the turn\'s: omitting it asserts nothing and inherits what the Session already holds, while sending a different one conflicts.  ## When the Session is already busy  A Session runs one turn at a time, and `if_active` decides what happens when you start another. The default, `reject`, returns `session_invocation_active`.  `supersede` cancels the running turn and starts yours in its place, atomically — there is no moment where the Session has no turn or two turns. It requires permission to both create and cancel. Retrying the same request returns your original turn and never cancels newer work that started in the meantime.  `interrupt` needs the same permission but stops the running turn cleanly instead of discarding its work. If that turn can stop immediately, yours starts in the same transaction. If it is mid-step, nvoken records the interrupt and this request waits for it. If it has not stopped by the time the wait is up, you get `session_invocation_active` with `details.interrupt_requested = true` — the interrupt is still in effect, so just send the request again.  ## Retired models  A deprecated model keeps working. On and after its `retires_at` date, new turns are refused with `422 model_retired`, and `details` tells you what to do about it: the `model` you asked for, its `retires_at` date, the exact `replacement` provider and id to switch to, and the request `path`. Retrying an idempotency key from before the retirement still returns that original turn.  ## Size limits  A text-only body may be up to 1 MiB. A body with images or documents may be up to 24 MiB, and within that: at most 8 media blocks, 16 MiB of decoded media in total, 5 MiB per image, and 16 MiB per document. Anything over these is rejected before a turn is created.  URLs are fetched after the idempotency check and before anything is saved, so a retry does not download twice. nvoken accepts public HTTPS only, stops reading at the size limit, and checks what the bytes actually are. It stores them and never fetches the URL again.  ## Streaming  This response is the acknowledgment. Once you hold the returned `id`, follow the turn with `GET /v1/sessions/{session_id}/stream?invocation_id=…`. Admission and streaming are separate requests on purpose: a dropped stream costs you nothing, because the turn already exists and no reconnect can create a second one.
     * Start one Invocation in the background
     */
    async createInvocationRaw(requestParameters: CreateInvocationOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<Invocation>> {
        const requestOptions = await this.createInvocationRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => InvocationFromJSON(jsonValue));
    }

    /**
     * Starts one agent turn and returns immediately. In a single database transaction nvoken resolves the deliberately created Agent, selects its Agent Definition revision, finds or creates the Session, appends your input as one message, and queues the turn. Admission never creates an Agent or reusable configuration. You get a response only after that transaction commits, so a `202` means the work is safely recorded and will run even if nvoken restarts. The model does not run on this request — it runs in the background, and you follow it with the stream or by polling.  Pick the Session with either `session_id` or `session_key`, not both. A Session ID must belong to the Agent you named, or to a Session created without an Agent — in which case this turn binds that Agent permanently. An App credential without a tenant constraint may omit `tenant_key` and use whichever tenant the Session already belongs to. A credential locked to one tenant cannot reach another; naming a different one returns `403 forbidden` without revealing whether the resource exists.  ## Retrying safely  Send `idempotency_key` and you can retry this request without risking a second turn. A repeat with the same key returns the original turn and does not add your input again, even if that turn has already finished. Keys are scoped to the tenant and Agent.  A repeat counts as the same request only if the Session selector, the Agent, explicit revision, per-turn overrides, `metadata`, `context`, `webhook`, `on_budget_exhausted`, and input all match. The original admitted revision is returned even if its Definition has advanced. Values are compared as sent, so omitting an override is not the same as supplying one that happens to equal the Definition. Key order inside JSON objects does not matter; array order does. Change anything material and you get `idempotency_conflict` rather than a surprise second turn.  `user_key` is the one exception, because it is the Session\'s rather than the turn\'s: omitting it asserts nothing and inherits what the Session already holds, while sending a different one conflicts.  ## When the Session is already busy  A Session runs one turn at a time, and `if_active` decides what happens when you start another. The default, `reject`, returns `session_invocation_active`.  `supersede` cancels the running turn and starts yours in its place, atomically — there is no moment where the Session has no turn or two turns. It requires permission to both create and cancel. Retrying the same request returns your original turn and never cancels newer work that started in the meantime.  `interrupt` needs the same permission but stops the running turn cleanly instead of discarding its work. If that turn can stop immediately, yours starts in the same transaction. If it is mid-step, nvoken records the interrupt and this request waits for it. If it has not stopped by the time the wait is up, you get `session_invocation_active` with `details.interrupt_requested = true` — the interrupt is still in effect, so just send the request again.  ## Retired models  A deprecated model keeps working. On and after its `retires_at` date, new turns are refused with `422 model_retired`, and `details` tells you what to do about it: the `model` you asked for, its `retires_at` date, the exact `replacement` provider and id to switch to, and the request `path`. Retrying an idempotency key from before the retirement still returns that original turn.  ## Size limits  A text-only body may be up to 1 MiB. A body with images or documents may be up to 24 MiB, and within that: at most 8 media blocks, 16 MiB of decoded media in total, 5 MiB per image, and 16 MiB per document. Anything over these is rejected before a turn is created.  URLs are fetched after the idempotency check and before anything is saved, so a retry does not download twice. nvoken accepts public HTTPS only, stops reading at the size limit, and checks what the bytes actually are. It stores them and never fetches the URL again.  ## Streaming  This response is the acknowledgment. Once you hold the returned `id`, follow the turn with `GET /v1/sessions/{session_id}/stream?invocation_id=…`. Admission and streaming are separate requests on purpose: a dropped stream costs you nothing, because the turn already exists and no reconnect can create a second one.
     * Start one Invocation in the background
     */
    async createInvocation(requestParameters: CreateInvocationOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<Invocation> {
        const response = await this.createInvocationRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for createNudge without sending the request
     */
    async createNudgeRequestOpts(requestParameters: CreateNudgeOperationRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['invocationId'] == null) {
            throw new runtime.RequiredError(
                'invocationId',
                'Required parameter "invocationId" was null or undefined when calling createNudge().'
            );
        }

        if (requestParameters['createNudgeRequest'] == null) {
            throw new runtime.RequiredError(
                'createNudgeRequest',
                'Required parameter "createNudgeRequest" was null or undefined when calling createNudge().'
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

        let urlPath = `/v1/invocations/{invocation_id}/nudges`;
        urlPath = urlPath.replace('{invocation_id}', encodeURIComponent(String(requestParameters['invocationId'])));

        return {
            path: urlPath,
            method: 'POST',
            headers: headerParameters,
            query: queryParameters,
            body: CreateNudgeRequestToJSON(requestParameters['createNudgeRequest']),
        };
    }

    /**
     * Sends extra direction to a turn that is already running — \"focus on the marine segment\" — without stopping it and without losing the work you are steering. Use this when a long turn is heading the wrong way and you want to correct it in place.  Compare with `if_active: supersede` on a new Invocation, which replaces the running turn and discards what it had produced. Steering a long turn that way throws away exactly the work you were trying to redirect.  **A nudge is not an interrupt, and it is not immediate.** The turn picks it up at its next clean stopping point: when it starts its next step, when it pauses for you to run a tool, or when a turn that thought it was finished re-enters its loop to answer you. A model call or tool run already in flight is never aborted to deliver it. A turn you have interrupted is never given more work — the interrupt wins and the direction you staged expires unused.  Nudges and Invocations never turn into each other. Posting to `/v1/invocations` against a busy Session behaves exactly as its `if_active` setting says; it never quietly becomes a nudge, and a nudge never quietly becomes a new turn.  If the turn ends without ever picking it up, your Nudge is marked `expired` at that moment and has no effect on any later turn. Check `GET .../nudges` to see whether it was used or missed. Whether to re-send missed direction as the next turn\'s input is your call.  `content` must be text — a string, or an array of text blocks. Images and documents are fine on a turn\'s own input but are refused here, because a turn resuming in place carries text only, and silently dropping your attachment would be worse than telling you now.  Requires the same permission as cancelling the turn.
     * Send extra direction to a running Invocation
     */
    async createNudgeRaw(requestParameters: CreateNudgeOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<NudgeAcknowledgement>> {
        const requestOptions = await this.createNudgeRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => NudgeAcknowledgementFromJSON(jsonValue));
    }

    /**
     * Sends extra direction to a turn that is already running — \"focus on the marine segment\" — without stopping it and without losing the work you are steering. Use this when a long turn is heading the wrong way and you want to correct it in place.  Compare with `if_active: supersede` on a new Invocation, which replaces the running turn and discards what it had produced. Steering a long turn that way throws away exactly the work you were trying to redirect.  **A nudge is not an interrupt, and it is not immediate.** The turn picks it up at its next clean stopping point: when it starts its next step, when it pauses for you to run a tool, or when a turn that thought it was finished re-enters its loop to answer you. A model call or tool run already in flight is never aborted to deliver it. A turn you have interrupted is never given more work — the interrupt wins and the direction you staged expires unused.  Nudges and Invocations never turn into each other. Posting to `/v1/invocations` against a busy Session behaves exactly as its `if_active` setting says; it never quietly becomes a nudge, and a nudge never quietly becomes a new turn.  If the turn ends without ever picking it up, your Nudge is marked `expired` at that moment and has no effect on any later turn. Check `GET .../nudges` to see whether it was used or missed. Whether to re-send missed direction as the next turn\'s input is your call.  `content` must be text — a string, or an array of text blocks. Images and documents are fine on a turn\'s own input but are refused here, because a turn resuming in place carries text only, and silently dropping your attachment would be worse than telling you now.  Requires the same permission as cancelling the turn.
     * Send extra direction to a running Invocation
     */
    async createNudge(requestParameters: CreateNudgeOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<NudgeAcknowledgement> {
        const response = await this.createNudgeRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for getInvocation without sending the request
     */
    async getInvocationRequestOpts(requestParameters: GetInvocationRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['invocationId'] == null) {
            throw new runtime.RequiredError(
                'invocationId',
                'Required parameter "invocationId" was null or undefined when calling getInvocation().'
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

        let urlPath = `/v1/invocations/{invocation_id}`;
        urlPath = urlPath.replace('{invocation_id}', encodeURIComponent(String(requestParameters['invocationId'])));

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * The turn\'s current state, including anything that went wrong after it started.  A credential that can authenticate but lacks permission for this read gets `forbidden`. A turn belonging to another tenant is reported as `not_found` rather than `forbidden`, so you cannot use this endpoint to discover whether an ID exists outside your scope.
     * Read authoritative Invocation identity and state
     */
    async getInvocationRaw(requestParameters: GetInvocationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<Invocation>> {
        const requestOptions = await this.getInvocationRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => InvocationFromJSON(jsonValue));
    }

    /**
     * The turn\'s current state, including anything that went wrong after it started.  A credential that can authenticate but lacks permission for this read gets `forbidden`. A turn belonging to another tenant is reported as `not_found` rather than `forbidden`, so you cannot use this endpoint to discover whether an ID exists outside your scope.
     * Read authoritative Invocation identity and state
     */
    async getInvocation(requestParameters: GetInvocationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<Invocation> {
        const response = await this.getInvocationRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for getInvocationResult without sending the request
     */
    async getInvocationResultRequestOpts(requestParameters: GetInvocationResultRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['invocationId'] == null) {
            throw new runtime.RequiredError(
                'invocationId',
                'Required parameter "invocationId" was null or undefined when calling getInvocationResult().'
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

        let urlPath = `/v1/invocations/{invocation_id}/result`;
        urlPath = urlPath.replace('{invocation_id}', encodeURIComponent(String(requestParameters['invocationId'])));

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Returns the turn and the messages it produced, at any status. This is the convenient read for \"what did the agent say?\" — `output_text` gives you the assistant\'s text already joined into a single string, so you do not have to walk the message blocks yourself.  The turn and its messages are read from one consistent database snapshot, so you will never see a finished turn whose last message is missing.  Authentication, tenant scoping, and the not-found behavior are the same as reading the Invocation on its own.
     * Read an Invocation together with its messages
     */
    async getInvocationResultRaw(requestParameters: GetInvocationResultRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<InvocationResult>> {
        const requestOptions = await this.getInvocationResultRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => InvocationResultFromJSON(jsonValue));
    }

    /**
     * Returns the turn and the messages it produced, at any status. This is the convenient read for \"what did the agent say?\" — `output_text` gives you the assistant\'s text already joined into a single string, so you do not have to walk the message blocks yourself.  The turn and its messages are read from one consistent database snapshot, so you will never see a finished turn whose last message is missing.  Authentication, tenant scoping, and the not-found behavior are the same as reading the Invocation on its own.
     * Read an Invocation together with its messages
     */
    async getInvocationResult(requestParameters: GetInvocationResultRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<InvocationResult> {
        const response = await this.getInvocationResultRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for getInvocationTimeline without sending the request
     */
    async getInvocationTimelineRequestOpts(requestParameters: GetInvocationTimelineRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['invocationId'] == null) {
            throw new runtime.RequiredError(
                'invocationId',
                'Required parameter "invocationId" was null or undefined when calling getInvocationTimeline().'
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

        let urlPath = `/v1/invocations/{invocation_id}/timeline`;
        urlPath = urlPath.replace('{invocation_id}', encodeURIComponent(String(requestParameters['invocationId'])));

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Assembles lifecycle waits, model calls, tool calls, nudges, and compactions from one database snapshot. It contains timings and usage, never prompts, responses, tool arguments, results, or error text. After Session erasure it degrades to the retained facts-only skeleton.
     * Read the durable execution waterfall for one Invocation
     */
    async getInvocationTimelineRaw(requestParameters: GetInvocationTimelineRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<InvocationTimeline>> {
        const requestOptions = await this.getInvocationTimelineRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => InvocationTimelineFromJSON(jsonValue));
    }

    /**
     * Assembles lifecycle waits, model calls, tool calls, nudges, and compactions from one database snapshot. It contains timings and usage, never prompts, responses, tool arguments, results, or error text. After Session erasure it degrades to the retained facts-only skeleton.
     * Read the durable execution waterfall for one Invocation
     */
    async getInvocationTimeline(requestParameters: GetInvocationTimelineRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<InvocationTimeline> {
        const response = await this.getInvocationTimelineRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for getTrace without sending the request
     */
    async getTraceRequestOpts(requestParameters: GetTraceRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['traceId'] == null) {
            throw new runtime.RequiredError(
                'traceId',
                'Required parameter "traceId" was null or undefined when calling getTrace().'
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

        let urlPath = `/v1/traces/{trace_id}`;
        urlPath = urlPath.replace('{trace_id}', encodeURIComponent(String(requestParameters['traceId'])));

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Returns a content-free projection of up to 200 OpenTelemetry spans. Use the pageable Invocation log endpoint with `trace_id` for associated logs. `is_partial` says when the agent root has not arrived or the bounded read omitted spans. nvoken grounds the trace\'s Invocation attribution in its durable Invocation record before returning it; knowing a W3C trace ID grants no authority.
     * Read one hosted agent trace
     */
    async getTraceRaw(requestParameters: GetTraceRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<Trace>> {
        const requestOptions = await this.getTraceRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => TraceFromJSON(jsonValue));
    }

    /**
     * Returns a content-free projection of up to 200 OpenTelemetry spans. Use the pageable Invocation log endpoint with `trace_id` for associated logs. `is_partial` says when the agent root has not arrived or the bounded read omitted spans. nvoken grounds the trace\'s Invocation attribution in its durable Invocation record before returning it; knowing a W3C trace ID grants no authority.
     * Read one hosted agent trace
     */
    async getTrace(requestParameters: GetTraceRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<Trace> {
        const response = await this.getTraceRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for interruptInvocation without sending the request
     */
    async interruptInvocationRequestOpts(requestParameters: InterruptInvocationRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['invocationId'] == null) {
            throw new runtime.RequiredError(
                'invocationId',
                'Required parameter "invocationId" was null or undefined when calling interruptInvocation().'
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

        let urlPath = `/v1/invocations/{invocation_id}/interrupt`;
        urlPath = urlPath.replace('{invocation_id}', encodeURIComponent(String(requestParameters['invocationId'])));

        return {
            path: urlPath,
            method: 'POST',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Asks a running turn to stop at its next clean stopping point. It ends `completed` with `stop_reason: interrupted`, and everything it produced — the model\'s replies and any tool results — stays in the conversation for the next turn. That is the whole difference from cancelling, which throws the turn\'s work away.  The request is recorded and safe to repeat. What happens next depends on what the turn was doing:  - Between steps (`queued`, `waiting`, or `running` with nothing   actively executing) it stops before this call returns. Any tool   calls you still owed results for are closed out, so submitting one   afterwards returns `409`. - Mid-step, nvoken records the request and returns the turn still   `running`. It stops at the next checkpoint, at worst one model call   later. Watch the stream or re-read the turn to see it end.  Interrupting a turn that has already finished changes nothing and returns it as-is. A turn that was asked for structured output but never produced a valid object ends `failed` with `structured_output_unsatisfied` rather than `completed`. Either way usage is reported in full and billed, because the work was kept.  Send an empty request body.
     * Stop an Invocation but keep what it produced
     */
    async interruptInvocationRaw(requestParameters: InterruptInvocationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<Invocation>> {
        const requestOptions = await this.interruptInvocationRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => InvocationFromJSON(jsonValue));
    }

    /**
     * Asks a running turn to stop at its next clean stopping point. It ends `completed` with `stop_reason: interrupted`, and everything it produced — the model\'s replies and any tool results — stays in the conversation for the next turn. That is the whole difference from cancelling, which throws the turn\'s work away.  The request is recorded and safe to repeat. What happens next depends on what the turn was doing:  - Between steps (`queued`, `waiting`, or `running` with nothing   actively executing) it stops before this call returns. Any tool   calls you still owed results for are closed out, so submitting one   afterwards returns `409`. - Mid-step, nvoken records the request and returns the turn still   `running`. It stops at the next checkpoint, at worst one model call   later. Watch the stream or re-read the turn to see it end.  Interrupting a turn that has already finished changes nothing and returns it as-is. A turn that was asked for structured output but never produced a valid object ends `failed` with `structured_output_unsatisfied` rather than `completed`. Either way usage is reported in full and billed, because the work was kept.  Send an empty request body.
     * Stop an Invocation but keep what it produced
     */
    async interruptInvocation(requestParameters: InterruptInvocationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<Invocation> {
        const response = await this.interruptInvocationRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for listInvocationLogs without sending the request
     */
    async listInvocationLogsRequestOpts(requestParameters: ListInvocationLogsRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['invocationId'] == null) {
            throw new runtime.RequiredError(
                'invocationId',
                'Required parameter "invocationId" was null or undefined when calling listInvocationLogs().'
            );
        }

        const queryParameters: any = {};

        if (requestParameters['cursor'] != null) {
            queryParameters['cursor'] = requestParameters['cursor'];
        }

        if (requestParameters['limit'] != null) {
            queryParameters['limit'] = requestParameters['limit'];
        }

        if (requestParameters['traceId'] != null) {
            queryParameters['trace_id'] = requestParameters['traceId'];
        }

        const headerParameters: runtime.HTTPHeaders = {};

        if (this.configuration && this.configuration.accessToken) {
            const token = this.configuration.accessToken;
            const tokenString = await token("bearerAuth", []);

            if (tokenString) {
                headerParameters["Authorization"] = `Bearer ${tokenString}`;
            }
        }

        let urlPath = `/v1/invocations/{invocation_id}/logs`;
        urlPath = urlPath.replace('{invocation_id}', encodeURIComponent(String(requestParameters['invocationId'])));

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Returns the content-free structured lifecycle logs associated by the Invocation ID. Arbitrary attributes and raw error values are omitted. `status` is `disabled` when this installation has no configured observation store.
     * Page through hosted structured logs for one Invocation
     */
    async listInvocationLogsRaw(requestParameters: ListInvocationLogsRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<InvocationLogList>> {
        const requestOptions = await this.listInvocationLogsRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => InvocationLogListFromJSON(jsonValue));
    }

    /**
     * Returns the content-free structured lifecycle logs associated by the Invocation ID. Arbitrary attributes and raw error values are omitted. `status` is `disabled` when this installation has no configured observation store.
     * Page through hosted structured logs for one Invocation
     */
    async listInvocationLogs(requestParameters: ListInvocationLogsRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<InvocationLogList> {
        const response = await this.listInvocationLogsRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for listInvocationTraces without sending the request
     */
    async listInvocationTracesRequestOpts(requestParameters: ListInvocationTracesRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['invocationId'] == null) {
            throw new runtime.RequiredError(
                'invocationId',
                'Required parameter "invocationId" was null or undefined when calling listInvocationTraces().'
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

        let urlPath = `/v1/invocations/{invocation_id}/traces`;
        urlPath = urlPath.replace('{invocation_id}', encodeURIComponent(String(requestParameters['invocationId'])));

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Returns newest-first, content-free summaries exported from Dive through OpenTelemetry. A child-only trace is returned as `is_partial: true` while its agent root is still open or if the process exits before that root is exported. Traces remain diagnostic and best-effort; the durable Invocation timeline is the execution authority. `status` is `disabled` when this installation has no configured observation store.
     * Page through hosted agent traces for one Invocation
     */
    async listInvocationTracesRaw(requestParameters: ListInvocationTracesRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<TraceList>> {
        const requestOptions = await this.listInvocationTracesRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => TraceListFromJSON(jsonValue));
    }

    /**
     * Returns newest-first, content-free summaries exported from Dive through OpenTelemetry. A child-only trace is returned as `is_partial: true` while its agent root is still open or if the process exits before that root is exported. Traces remain diagnostic and best-effort; the durable Invocation timeline is the execution authority. `status` is `disabled` when this installation has no configured observation store.
     * Page through hosted agent traces for one Invocation
     */
    async listInvocationTraces(requestParameters: ListInvocationTracesRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<TraceList> {
        const response = await this.listInvocationTracesRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for listInvocations without sending the request
     */
    async listInvocationsRequestOpts(requestParameters: ListInvocationsRequest): Promise<runtime.RequestOpts> {
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

        if (requestParameters['sessionId'] != null) {
            queryParameters['session_id'] = requestParameters['sessionId'];
        }

        if (requestParameters['agentId'] != null) {
            queryParameters['agent_id'] = requestParameters['agentId'];
        }

        if (requestParameters['agentKey'] != null) {
            queryParameters['agent_key'] = requestParameters['agentKey'];
        }

        if (requestParameters['status'] != null) {
            queryParameters['status'] = requestParameters['status'];
        }

        if (requestParameters['ended'] != null) {
            queryParameters['ended'] = requestParameters['ended'];
        }

        if (requestParameters['endedSince'] != null) {
            queryParameters['ended_since'] = (requestParameters['endedSince'] as any).toISOString();
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

        let urlPath = `/v1/invocations`;

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Returns newest-first durable Invocation state. Exact filters combine with AND. An App credential without a tenant constraint may list all tenant partitions in that App, one named partition with `tenant_key`, or the default partition with `default_tenant=true`. A tenant-constrained credential is always scoped to its partition. The opaque cursor is bound to the normalized filter set and credential tenant scope. `agent_id` and `agent_key` are mutually exclusive; both normalize to the resolved Agent ID for cursor binding, so an equivalent cursor may resume under either spelling.  ## `ended=true` makes this a reconciliation feed  Set it and the same operation reverses into a feed: every Invocation that reached a terminal status, **oldest first by the moment it ended**, each appearing exactly once. Walk it and append by `id`.  This is the backstop for settlement. `invocation.ended` webhooks are delivered at least once, which narrows the window but does not close it: a delivery that never lands leaves a turn nobody settles, and that failure is silent — no error, just a ledger row that was never written. Reading the feed to the end is how you find out.  The default listing cannot do that job. Newest-first over current state means a turn that ends while you page moves under you, and filtering by terminal status gives you a set with no position in it. Ending order is the only order you can resume.  Start with `ended_since`, or with no position at all to begin at the oldest retained turn. Then send back `next_cursor`, which in this mode is present on every response including an empty page, so a consumer that catches up keeps its position without special-casing. Keep going while `has_more` is true; when it is false you are caught up and can wait before asking again.  `complete_through` is returned in this mode and is the instant the feed is complete to. Turns that ended after it are held back until their settling transactions are certainly visible, because a turn that appeared behind your cursor would be one you never see again. It trails the present by a bounded interval, so a consumer is always slightly behind and never wrong; it is also the number to alarm on, since a `complete_through` that stops advancing means settlement has stalled rather than that nothing ended.  A cursor carries its mode, so one from the default listing cannot resume the feed and the reverse is also refused. Erased Sessions take their Invocations with them, so a turn deleted before you read it never appears; reconcile before you erase.
     * List authoritative Invocations, or walk the ones that ended
     */
    async listInvocationsRaw(requestParameters: ListInvocationsRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<InvocationList>> {
        const requestOptions = await this.listInvocationsRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => InvocationListFromJSON(jsonValue));
    }

    /**
     * Returns newest-first durable Invocation state. Exact filters combine with AND. An App credential without a tenant constraint may list all tenant partitions in that App, one named partition with `tenant_key`, or the default partition with `default_tenant=true`. A tenant-constrained credential is always scoped to its partition. The opaque cursor is bound to the normalized filter set and credential tenant scope. `agent_id` and `agent_key` are mutually exclusive; both normalize to the resolved Agent ID for cursor binding, so an equivalent cursor may resume under either spelling.  ## `ended=true` makes this a reconciliation feed  Set it and the same operation reverses into a feed: every Invocation that reached a terminal status, **oldest first by the moment it ended**, each appearing exactly once. Walk it and append by `id`.  This is the backstop for settlement. `invocation.ended` webhooks are delivered at least once, which narrows the window but does not close it: a delivery that never lands leaves a turn nobody settles, and that failure is silent — no error, just a ledger row that was never written. Reading the feed to the end is how you find out.  The default listing cannot do that job. Newest-first over current state means a turn that ends while you page moves under you, and filtering by terminal status gives you a set with no position in it. Ending order is the only order you can resume.  Start with `ended_since`, or with no position at all to begin at the oldest retained turn. Then send back `next_cursor`, which in this mode is present on every response including an empty page, so a consumer that catches up keeps its position without special-casing. Keep going while `has_more` is true; when it is false you are caught up and can wait before asking again.  `complete_through` is returned in this mode and is the instant the feed is complete to. Turns that ended after it are held back until their settling transactions are certainly visible, because a turn that appeared behind your cursor would be one you never see again. It trails the present by a bounded interval, so a consumer is always slightly behind and never wrong; it is also the number to alarm on, since a `complete_through` that stops advancing means settlement has stalled rather than that nothing ended.  A cursor carries its mode, so one from the default listing cannot resume the feed and the reverse is also refused. Erased Sessions take their Invocations with them, so a turn deleted before you read it never appears; reconcile before you erase.
     * List authoritative Invocations, or walk the ones that ended
     */
    async listInvocations(requestParameters: ListInvocationsRequest = {}, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<InvocationList> {
        const response = await this.listInvocationsRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for listNudges without sending the request
     */
    async listNudgesRequestOpts(requestParameters: ListNudgesRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['invocationId'] == null) {
            throw new runtime.RequiredError(
                'invocationId',
                'Required parameter "invocationId" was null or undefined when calling listNudges().'
            );
        }

        const queryParameters: any = {};

        if (requestParameters['status'] != null) {
            queryParameters['status'] = requestParameters['status'];
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

        let urlPath = `/v1/invocations/{invocation_id}/nudges`;
        urlPath = urlPath.replace('{invocation_id}', encodeURIComponent(String(requestParameters['invocationId'])));

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Lists the direction you have sent to this turn with `/nudges`, in the order the turn will pick it up. Entries stay listed after they are used or missed, so you can answer \"what did the user say, and did the model ever see it?\"  Check `status` on each entry: `drained` means the turn used it, `expired` means the turn ended first, `cancelled` means you withdrew it.
     * List Nudges for an Invocation
     */
    async listNudgesRaw(requestParameters: ListNudgesRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<NudgeList>> {
        const requestOptions = await this.listNudgesRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => NudgeListFromJSON(jsonValue));
    }

    /**
     * Lists the direction you have sent to this turn with `/nudges`, in the order the turn will pick it up. Entries stay listed after they are used or missed, so you can answer \"what did the user say, and did the model ever see it?\"  Check `status` on each entry: `drained` means the turn used it, `expired` means the turn ended first, `cancelled` means you withdrew it.
     * List Nudges for an Invocation
     */
    async listNudges(requestParameters: ListNudgesRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<NudgeList> {
        const response = await this.listNudgesRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for listToolCalls without sending the request
     */
    async listToolCallsRequestOpts(requestParameters: ListToolCallsRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['invocationId'] == null) {
            throw new runtime.RequiredError(
                'invocationId',
                'Required parameter "invocationId" was null or undefined when calling listToolCalls().'
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

        let urlPath = `/v1/invocations/{invocation_id}/tool-calls`;
        urlPath = urlPath.replace('{invocation_id}', encodeURIComponent(String(requestParameters['invocationId'])));

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Returns ToolCalls in execution discovery order. Every execution mode is included. The records contain lifecycle and timing facts only. Tool inputs and results remain in the canonical Session transcript.  Callback records include a delivery object. Its terminal outcome, attempt count, and last HTTP status remain available after the bounded delivery transport row is pruned. These records use the same authentication, tenant scope, Session constraint, and nondisclosing not_found behavior as the Invocation read. Deleting the Session deletes these records with the rest of its subtree.
     * Page through durable ToolCall execution records
     */
    async listToolCallsRaw(requestParameters: ListToolCallsRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<ToolCallList>> {
        const requestOptions = await this.listToolCallsRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => ToolCallListFromJSON(jsonValue));
    }

    /**
     * Returns ToolCalls in execution discovery order. Every execution mode is included. The records contain lifecycle and timing facts only. Tool inputs and results remain in the canonical Session transcript.  Callback records include a delivery object. Its terminal outcome, attempt count, and last HTTP status remain available after the bounded delivery transport row is pruned. These records use the same authentication, tenant scope, Session constraint, and nondisclosing not_found behavior as the Invocation read. Deleting the Session deletes these records with the rest of its subtree.
     * Page through durable ToolCall execution records
     */
    async listToolCalls(requestParameters: ListToolCallsRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<ToolCallList> {
        const response = await this.listToolCallsRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for resumeInvocation without sending the request
     */
    async resumeInvocationRequestOpts(requestParameters: ResumeInvocationOperationRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['invocationId'] == null) {
            throw new runtime.RequiredError(
                'invocationId',
                'Required parameter "invocationId" was null or undefined when calling resumeInvocation().'
            );
        }

        if (requestParameters['resumeInvocationRequest'] == null) {
            throw new runtime.RequiredError(
                'resumeInvocationRequest',
                'Required parameter "resumeInvocationRequest" was null or undefined when calling resumeInvocation().'
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

        let urlPath = `/v1/invocations/{invocation_id}/resume`;
        urlPath = urlPath.replace('{invocation_id}', encodeURIComponent(String(requestParameters['invocationId'])));

        return {
            path: urlPath,
            method: 'POST',
            headers: headerParameters,
            query: queryParameters,
            body: ResumeInvocationRequestToJSON(requestParameters['resumeInvocationRequest']),
        };
    }

    /**
     * Continues a turn that paused because one of its own spending limits ran out. Send `limits` containing only the limit that ran out, raised above both its old value and what the turn has already used, and still within what your installation allows.  If the turn paused because the tenant ran out of credits rather than on a limit of its own, allocate credits to that account instead — this endpoint refuses it, and funding the account continues the turn on its own. Deadlines never pause a turn, so they never bring you here.
     * Raise a paused Invocation\'s limit and continue it
     */
    async resumeInvocationRaw(requestParameters: ResumeInvocationOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<Invocation>> {
        const requestOptions = await this.resumeInvocationRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => InvocationFromJSON(jsonValue));
    }

    /**
     * Continues a turn that paused because one of its own spending limits ran out. Send `limits` containing only the limit that ran out, raised above both its old value and what the turn has already used, and still within what your installation allows.  If the turn paused because the tenant ran out of credits rather than on a limit of its own, allocate credits to that account instead — this endpoint refuses it, and funding the account continues the turn on its own. Deadlines never pause a turn, so they never bring you here.
     * Raise a paused Invocation\'s limit and continue it
     */
    async resumeInvocation(requestParameters: ResumeInvocationOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<Invocation> {
        const response = await this.resumeInvocationRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for submitHostToolResults without sending the request
     */
    async submitHostToolResultsRequestOpts(requestParameters: SubmitHostToolResultsOperationRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['invocationId'] == null) {
            throw new runtime.RequiredError(
                'invocationId',
                'Required parameter "invocationId" was null or undefined when calling submitHostToolResults().'
            );
        }

        if (requestParameters['submitHostToolResultsRequest'] == null) {
            throw new runtime.RequiredError(
                'submitHostToolResultsRequest',
                'Required parameter "submitHostToolResultsRequest" was null or undefined when calling submitHostToolResults().'
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

        let urlPath = `/v1/invocations/{invocation_id}/tool-results`;
        urlPath = urlPath.replace('{invocation_id}', encodeURIComponent(String(requestParameters['invocationId'])));

        return {
            path: urlPath,
            method: 'POST',
            headers: headerParameters,
            query: queryParameters,
            body: SubmitHostToolResultsRequestToJSON(requestParameters['submitHostToolResultsRequest']),
        };
    }

    /**
     * Atomically accepts one bounded batch for a waiting Invocation. The first committed result for each ToolCall wins. An equal replay is acknowledged as deduplicated; a changed replay conflicts. Partial batches leave the Invocation waiting. Closing the final pending call queues the same Invocation and its successor dispatch before returning `202`.  This command accepts only host- or callback-mode calls owned by the path Invocation and authenticated tenant scope. It is not a generic Session append endpoint. The body is limited to 1 MiB; each result content value is valid JSON limited to 256 KiB and 32 nesting levels.  `content` accepts any JSON value and the stored transcript retains it verbatim. Before a result reaches the model, a string or an array of content blocks passes through unchanged; any other value is serialized to its compact JSON text and sent as a string, so the model sees the same bytes a host that pre-stringifies would send.
     * Submit durable results for pending host and callback ToolCalls
     */
    async submitHostToolResultsRaw(requestParameters: SubmitHostToolResultsOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<SubmitHostToolResultsResponse>> {
        const requestOptions = await this.submitHostToolResultsRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => SubmitHostToolResultsResponseFromJSON(jsonValue));
    }

    /**
     * Atomically accepts one bounded batch for a waiting Invocation. The first committed result for each ToolCall wins. An equal replay is acknowledged as deduplicated; a changed replay conflicts. Partial batches leave the Invocation waiting. Closing the final pending call queues the same Invocation and its successor dispatch before returning `202`.  This command accepts only host- or callback-mode calls owned by the path Invocation and authenticated tenant scope. It is not a generic Session append endpoint. The body is limited to 1 MiB; each result content value is valid JSON limited to 256 KiB and 32 nesting levels.  `content` accepts any JSON value and the stored transcript retains it verbatim. Before a result reaches the model, a string or an array of content blocks passes through unchanged; any other value is serialized to its compact JSON text and sent as a string, so the model sees the same bytes a host that pre-stringifies would send.
     * Submit durable results for pending host and callback ToolCalls
     */
    async submitHostToolResults(requestParameters: SubmitHostToolResultsOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<SubmitHostToolResultsResponse> {
        const response = await this.submitHostToolResultsRaw(requestParameters, initOverrides);
        return await response.value();
    }

}
