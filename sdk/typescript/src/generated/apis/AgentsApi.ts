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
    type Agent,
    AgentFromJSON,
    AgentToJSON,
} from '../models/Agent.js';
import {
    type AgentList,
    AgentListFromJSON,
    AgentListToJSON,
} from '../models/AgentList.js';
import {
    type CreateAgentRequest,
    CreateAgentRequestFromJSON,
    CreateAgentRequestToJSON,
} from '../models/CreateAgentRequest.js';
import {
    type ErrorResponse,
    ErrorResponseFromJSON,
    ErrorResponseToJSON,
} from '../models/ErrorResponse.js';
import {
    type UpdateAgentRequest,
    UpdateAgentRequestFromJSON,
    UpdateAgentRequestToJSON,
} from '../models/UpdateAgentRequest.js';

export interface ArchiveAgentRequest {
    agentId: string;
}

export interface CreateAgentOperationRequest {
    createAgentRequest: CreateAgentRequest;
}

export interface GetAgentRequest {
    agentId: string;
}

export interface ListAgentsRequest {
    tenantKey?: string;
    agentKey?: string;
    agentDefinitionId?: string;
    includeArchived?: boolean;
    cursor?: string;
    limit?: number;
}

export interface RestoreAgentRequest {
    agentId: string;
}

export interface UpdateAgentOperationRequest {
    agentId: string;
    updateAgentRequest: UpdateAgentRequest;
}

/**
 *
 */
export class AgentsApi extends runtime.BaseAPI {

    /**
     * Creates request options for archiveAgent without sending the request
     */
    async archiveAgentRequestOpts(requestParameters: ArchiveAgentRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['agentId'] == null) {
            throw new runtime.RequiredError(
                'agentId',
                'Required parameter "agentId" was null or undefined when calling archiveAgent().'
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

        let urlPath = `/v1/agents/{agent_id}`;
        urlPath = urlPath.replace('{agent_id}', encodeURIComponent(String(requestParameters['agentId'])));

        return {
            path: urlPath,
            method: 'DELETE',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Archive an Agent
     */
    async archiveAgentRaw(requestParameters: ArchiveAgentRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<void>> {
        const requestOptions = await this.archiveAgentRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.VoidApiResponse(response);
    }

    /**
     * Archive an Agent
     */
    async archiveAgent(requestParameters: ArchiveAgentRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<void> {
        await this.archiveAgentRaw(requestParameters, initOverrides);
    }

    /**
     * Creates request options for createAgent without sending the request
     */
    async createAgentRequestOpts(requestParameters: CreateAgentOperationRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['createAgentRequest'] == null) {
            throw new runtime.RequiredError(
                'createAgentRequest',
                'Required parameter "createAgentRequest" was null or undefined when calling createAgent().'
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

        let urlPath = `/v1/agents`;

        return {
            path: urlPath,
            method: 'POST',
            headers: headerParameters,
            query: queryParameters,
            body: CreateAgentRequestToJSON(requestParameters['createAgentRequest']),
        };
    }

    /**
     * Creation is an upsert on `(tenant_key, agent_key)`, so an ensure-shaped call is safe to make on every request and needs no read first. The same keys backed by the same Agent Definition return the existing Agent with `200`; a different Definition pointer is `409 agent_key_conflict`, naming the Agent that holds the key. Keys held by an archived Agent are `409 agent_archived` — restore it or choose another key — rather than silently resolving onto a record that refuses every admission.  Resolution matches on the Definition pointer only. A differing `name` or `pinned_revision` in the request does not modify the existing Agent; use `PATCH /v1/agents/{agent_id}` to change either.  Name the Definition with either `agent_definition_id` or `agent_definition_key` — exactly one. The key spelling lets a caller declare an Agent entirely from keys it already owns, with no lookup first.
     * Create or resolve a tenant-scoped Agent
     */
    async createAgentRaw(requestParameters: CreateAgentOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<Agent>> {
        const requestOptions = await this.createAgentRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => AgentFromJSON(jsonValue));
    }

    /**
     * Creation is an upsert on `(tenant_key, agent_key)`, so an ensure-shaped call is safe to make on every request and needs no read first. The same keys backed by the same Agent Definition return the existing Agent with `200`; a different Definition pointer is `409 agent_key_conflict`, naming the Agent that holds the key. Keys held by an archived Agent are `409 agent_archived` — restore it or choose another key — rather than silently resolving onto a record that refuses every admission.  Resolution matches on the Definition pointer only. A differing `name` or `pinned_revision` in the request does not modify the existing Agent; use `PATCH /v1/agents/{agent_id}` to change either.  Name the Definition with either `agent_definition_id` or `agent_definition_key` — exactly one. The key spelling lets a caller declare an Agent entirely from keys it already owns, with no lookup first.
     * Create or resolve a tenant-scoped Agent
     */
    async createAgent(requestParameters: CreateAgentOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<Agent> {
        const response = await this.createAgentRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for getAgent without sending the request
     */
    async getAgentRequestOpts(requestParameters: GetAgentRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['agentId'] == null) {
            throw new runtime.RequiredError(
                'agentId',
                'Required parameter "agentId" was null or undefined when calling getAgent().'
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

        let urlPath = `/v1/agents/{agent_id}`;
        urlPath = urlPath.replace('{agent_id}', encodeURIComponent(String(requestParameters['agentId'])));

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Reads identity without creating work. Out-of-scope and undisclosable constrained resources use `not_found`.
     * Read one tenant-scoped Agent
     */
    async getAgentRaw(requestParameters: GetAgentRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<Agent>> {
        const requestOptions = await this.getAgentRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => AgentFromJSON(jsonValue));
    }

    /**
     * Reads identity without creating work. Out-of-scope and undisclosable constrained resources use `not_found`.
     * Read one tenant-scoped Agent
     */
    async getAgent(requestParameters: GetAgentRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<Agent> {
        const response = await this.getAgentRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for listAgents without sending the request
     */
    async listAgentsRequestOpts(requestParameters: ListAgentsRequest): Promise<runtime.RequestOpts> {
        const queryParameters: any = {};

        if (requestParameters['tenantKey'] != null) {
            queryParameters['tenant_key'] = requestParameters['tenantKey'];
        }

        if (requestParameters['agentKey'] != null) {
            queryParameters['agent_key'] = requestParameters['agentKey'];
        }

        if (requestParameters['agentDefinitionId'] != null) {
            queryParameters['agent_definition_id'] = requestParameters['agentDefinitionId'];
        }

        if (requestParameters['includeArchived'] != null) {
            queryParameters['include_archived'] = requestParameters['includeArchived'];
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

        let urlPath = `/v1/agents`;

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Returns newest-first Agents scoped to the caller\'s App. Filters combine with AND. Archived Agents are excluded unless requested.
     * List tenant-scoped Agents
     */
    async listAgentsRaw(requestParameters: ListAgentsRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<AgentList>> {
        const requestOptions = await this.listAgentsRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => AgentListFromJSON(jsonValue));
    }

    /**
     * Returns newest-first Agents scoped to the caller\'s App. Filters combine with AND. Archived Agents are excluded unless requested.
     * List tenant-scoped Agents
     */
    async listAgents(requestParameters: ListAgentsRequest = {}, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<AgentList> {
        const response = await this.listAgentsRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for restoreAgent without sending the request
     */
    async restoreAgentRequestOpts(requestParameters: RestoreAgentRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['agentId'] == null) {
            throw new runtime.RequiredError(
                'agentId',
                'Required parameter "agentId" was null or undefined when calling restoreAgent().'
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

        let urlPath = `/v1/agents/{agent_id}/restore`;
        urlPath = urlPath.replace('{agent_id}', encodeURIComponent(String(requestParameters['agentId'])));

        return {
            path: urlPath,
            method: 'POST',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Restore an archived Agent
     */
    async restoreAgentRaw(requestParameters: RestoreAgentRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<void>> {
        const requestOptions = await this.restoreAgentRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.VoidApiResponse(response);
    }

    /**
     * Restore an archived Agent
     */
    async restoreAgent(requestParameters: RestoreAgentRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<void> {
        await this.restoreAgentRaw(requestParameters, initOverrides);
    }

    /**
     * Creates request options for updateAgent without sending the request
     */
    async updateAgentRequestOpts(requestParameters: UpdateAgentOperationRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['agentId'] == null) {
            throw new runtime.RequiredError(
                'agentId',
                'Required parameter "agentId" was null or undefined when calling updateAgent().'
            );
        }

        if (requestParameters['updateAgentRequest'] == null) {
            throw new runtime.RequiredError(
                'updateAgentRequest',
                'Required parameter "updateAgentRequest" was null or undefined when calling updateAgent().'
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

        let urlPath = `/v1/agents/{agent_id}`;
        urlPath = urlPath.replace('{agent_id}', encodeURIComponent(String(requestParameters['agentId'])));

        return {
            path: urlPath,
            method: 'PATCH',
            headers: headerParameters,
            query: queryParameters,
            body: UpdateAgentRequestToJSON(requestParameters['updateAgentRequest']),
        };
    }

    /**
     * Rename or revision-pin an Agent
     */
    async updateAgentRaw(requestParameters: UpdateAgentOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<Agent>> {
        const requestOptions = await this.updateAgentRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => AgentFromJSON(jsonValue));
    }

    /**
     * Rename or revision-pin an Agent
     */
    async updateAgent(requestParameters: UpdateAgentOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<Agent> {
        const response = await this.updateAgentRaw(requestParameters, initOverrides);
        return await response.value();
    }

}
