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

import * as runtime from '../runtime.js';
import {
    type ErrorResponse,
    ErrorResponseFromJSON,
    ErrorResponseToJSON,
} from '../models/ErrorResponse.js';
import {
    type Memory,
    MemoryFromJSON,
    MemoryToJSON,
} from '../models/Memory.js';
import {
    type MemoryKind,
    MemoryKindFromJSON,
    MemoryKindToJSON,
} from '../models/MemoryKind.js';
import {
    type MemoryList,
    MemoryListFromJSON,
    MemoryListToJSON,
} from '../models/MemoryList.js';
import {
    type MemorySearchMode,
    MemorySearchModeFromJSON,
    MemorySearchModeToJSON,
} from '../models/MemorySearchMode.js';

export interface DeleteMemoryRequest {
    memoryId: string;
}

export interface GetMemoryRequest {
    memoryId: string;
}

export interface ListMemoriesRequest {
    agentId: string;
    tenantKey?: string;
    userKey?: string;
    query?: string;
    searchMode?: MemorySearchMode;
    kind?: MemoryKind;
    cursor?: string;
    limit?: number;
}

/**
 *
 */
export class MemoriesApi extends runtime.BaseAPI {

    /**
     * Creates request options for deleteMemory without sending the request
     */
    async deleteMemoryRequestOpts(requestParameters: DeleteMemoryRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['memoryId'] == null) {
            throw new runtime.RequiredError(
                'memoryId',
                'Required parameter "memoryId" was null or undefined when calling deleteMemory().'
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

        let urlPath = `/v1/memories/{memory_id}`;
        urlPath = urlPath.replace('{memory_id}', encodeURIComponent(String(requestParameters['memoryId'])));

        return {
            path: urlPath,
            method: 'DELETE',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Erase one durable memory and its embedding projection
     */
    async deleteMemoryRaw(requestParameters: DeleteMemoryRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<void>> {
        const requestOptions = await this.deleteMemoryRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.VoidApiResponse(response);
    }

    /**
     * Erase one durable memory and its embedding projection
     */
    async deleteMemory(requestParameters: DeleteMemoryRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<void> {
        await this.deleteMemoryRaw(requestParameters, initOverrides);
    }

    /**
     * Creates request options for getMemory without sending the request
     */
    async getMemoryRequestOpts(requestParameters: GetMemoryRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['memoryId'] == null) {
            throw new runtime.RequiredError(
                'memoryId',
                'Required parameter "memoryId" was null or undefined when calling getMemory().'
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

        let urlPath = `/v1/memories/{memory_id}`;
        urlPath = urlPath.replace('{memory_id}', encodeURIComponent(String(requestParameters['memoryId'])));

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Read one durable memory
     */
    async getMemoryRaw(requestParameters: GetMemoryRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<Memory>> {
        const requestOptions = await this.getMemoryRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => MemoryFromJSON(jsonValue));
    }

    /**
     * Read one durable memory
     */
    async getMemory(requestParameters: GetMemoryRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<Memory> {
        const response = await this.getMemoryRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for listMemories without sending the request
     */
    async listMemoriesRequestOpts(requestParameters: ListMemoriesRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['agentId'] == null) {
            throw new runtime.RequiredError(
                'agentId',
                'Required parameter "agentId" was null or undefined when calling listMemories().'
            );
        }

        const queryParameters: any = {};

        if (requestParameters['agentId'] != null) {
            queryParameters['agent_id'] = requestParameters['agentId'];
        }

        if (requestParameters['tenantKey'] != null) {
            queryParameters['tenant_key'] = requestParameters['tenantKey'];
        }

        if (requestParameters['userKey'] != null) {
            queryParameters['user_key'] = requestParameters['userKey'];
        }

        if (requestParameters['query'] != null) {
            queryParameters['query'] = requestParameters['query'];
        }

        if (requestParameters['searchMode'] != null) {
            queryParameters['search_mode'] = requestParameters['searchMode'];
        }

        if (requestParameters['kind'] != null) {
            queryParameters['kind'] = requestParameters['kind'];
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

        let urlPath = `/v1/memories`;

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Reads the authenticated App\'s memory store without changing eviction recency. `hybrid` is the default search mode and combines lexical and semantic rank. During indexing or an embedding outage it still returns keyword results with explicit incomplete coverage. `semantic` fails typed when no query embedding can be produced.
     * Browse or search durable Agent memories
     */
    async listMemoriesRaw(requestParameters: ListMemoriesRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<MemoryList>> {
        const requestOptions = await this.listMemoriesRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => MemoryListFromJSON(jsonValue));
    }

    /**
     * Reads the authenticated App\'s memory store without changing eviction recency. `hybrid` is the default search mode and combines lexical and semantic rank. During indexing or an embedding outage it still returns keyword results with explicit incomplete coverage. `semantic` fails typed when no query embedding can be produced.
     * Browse or search durable Agent memories
     */
    async listMemories(requestParameters: ListMemoriesRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<MemoryList> {
        const response = await this.listMemoriesRaw(requestParameters, initOverrides);
        return await response.value();
    }

}
