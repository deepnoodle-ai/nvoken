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
    type AgentDefinitionCreate,
    AgentDefinitionCreateFromJSON,
    AgentDefinitionCreateToJSON,
} from '../models/AgentDefinitionCreate.js';
import {
    type AgentDefinitionResource,
    AgentDefinitionResourceFromJSON,
    AgentDefinitionResourceToJSON,
} from '../models/AgentDefinitionResource.js';
import {
    type AgentDefinitionResourceList,
    AgentDefinitionResourceListFromJSON,
    AgentDefinitionResourceListToJSON,
} from '../models/AgentDefinitionResourceList.js';
import {
    type AgentDefinitionWrite,
    AgentDefinitionWriteFromJSON,
    AgentDefinitionWriteToJSON,
} from '../models/AgentDefinitionWrite.js';
import {
    type ErrorResponse,
    ErrorResponseFromJSON,
    ErrorResponseToJSON,
} from '../models/ErrorResponse.js';

export interface ArchiveAgentDefinitionRequest {
    agentDefinitionId: string;
}

export interface CreateAgentDefinitionRequest {
    idempotencyKey: string;
    agentDefinitionCreate: AgentDefinitionCreate;
}

export interface GetAgentDefinitionRequest {
    agentDefinitionId: string;
}

export interface GetAgentDefinitionRevisionRequest {
    agentDefinitionId: string;
    revision: number;
}

export interface ListAgentDefinitionsRequest {
    includeArchived?: boolean;
    cursor?: string;
    limit?: number;
}

export interface RestoreAgentDefinitionRequest {
    agentDefinitionId: string;
}

export interface UpdateAgentDefinitionRequest {
    ifMatch: string;
    agentDefinitionId: string;
    agentDefinitionWrite: AgentDefinitionWrite;
}

/**
 *
 */
export class AgentDefinitionsApi extends runtime.BaseAPI {

    /**
     * Creates request options for archiveAgentDefinition without sending the request
     */
    async archiveAgentDefinitionRequestOpts(requestParameters: ArchiveAgentDefinitionRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['agentDefinitionId'] == null) {
            throw new runtime.RequiredError(
                'agentDefinitionId',
                'Required parameter "agentDefinitionId" was null or undefined when calling archiveAgentDefinition().'
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

        let urlPath = `/v1/agent-definitions/{agent_definition_id}`;
        urlPath = urlPath.replace('{agent_definition_id}', encodeURIComponent(String(requestParameters['agentDefinitionId'])));

        return {
            path: urlPath,
            method: 'DELETE',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Marks the resource retired. Invocation admission that resolves it — through an Agent or a pinned revision, on every admission path including browser-direct client tokens — is then refused with `409 agent_definition_archived`.  Nothing is destroyed: the resource and every revision stay readable, and each existing Invocation keeps its pinned revision evidence. Archiving requires the same authority as replacing the definition, and repeating the call is a successful no-op.
     * Archive an Agent Definition
     */
    async archiveAgentDefinitionRaw(requestParameters: ArchiveAgentDefinitionRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<void>> {
        const requestOptions = await this.archiveAgentDefinitionRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.VoidApiResponse(response);
    }

    /**
     * Marks the resource retired. Invocation admission that resolves it — through an Agent or a pinned revision, on every admission path including browser-direct client tokens — is then refused with `409 agent_definition_archived`.  Nothing is destroyed: the resource and every revision stay readable, and each existing Invocation keeps its pinned revision evidence. Archiving requires the same authority as replacing the definition, and repeating the call is a successful no-op.
     * Archive an Agent Definition
     */
    async archiveAgentDefinition(requestParameters: ArchiveAgentDefinitionRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<void> {
        await this.archiveAgentDefinitionRaw(requestParameters, initOverrides);
    }

    /**
     * Creates request options for createAgentDefinition without sending the request
     */
    async createAgentDefinitionRequestOpts(requestParameters: CreateAgentDefinitionRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['idempotencyKey'] == null) {
            throw new runtime.RequiredError(
                'idempotencyKey',
                'Required parameter "idempotencyKey" was null or undefined when calling createAgentDefinition().'
            );
        }

        if (requestParameters['agentDefinitionCreate'] == null) {
            throw new runtime.RequiredError(
                'agentDefinitionCreate',
                'Required parameter "agentDefinitionCreate" was null or undefined when calling createAgentDefinition().'
            );
        }

        const queryParameters: any = {};

        const headerParameters: runtime.HTTPHeaders = {};

        headerParameters['Content-Type'] = 'application/json';

        if (requestParameters['idempotencyKey'] != null) {
            headerParameters['Idempotency-Key'] = String(requestParameters['idempotencyKey']);
        }

        if (this.configuration && this.configuration.accessToken) {
            const token = this.configuration.accessToken;
            const tokenString = await token("bearerAuth", []);

            if (tokenString) {
                headerParameters["Authorization"] = `Bearer ${tokenString}`;
            }
        }

        let urlPath = `/v1/agent-definitions`;

        return {
            path: urlPath,
            method: 'POST',
            headers: headerParameters,
            query: queryParameters,
            body: AgentDefinitionCreateToJSON(requestParameters['agentDefinitionCreate']),
        };
    }

    /**
     * Creates a stable App-owned resource at revision 1. Equal content in a separate create gets a separate ID. Retry the same canonical request with the same `Idempotency-Key` to receive the original revision-1 resource; changing the request under that key conflicts.
     * Create an Agent Definition resource
     */
    async createAgentDefinitionRaw(requestParameters: CreateAgentDefinitionRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<AgentDefinitionResource>> {
        const requestOptions = await this.createAgentDefinitionRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => AgentDefinitionResourceFromJSON(jsonValue));
    }

    /**
     * Creates a stable App-owned resource at revision 1. Equal content in a separate create gets a separate ID. Retry the same canonical request with the same `Idempotency-Key` to receive the original revision-1 resource; changing the request under that key conflicts.
     * Create an Agent Definition resource
     */
    async createAgentDefinition(requestParameters: CreateAgentDefinitionRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<AgentDefinitionResource> {
        const response = await this.createAgentDefinitionRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for getAgentDefinition without sending the request
     */
    async getAgentDefinitionRequestOpts(requestParameters: GetAgentDefinitionRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['agentDefinitionId'] == null) {
            throw new runtime.RequiredError(
                'agentDefinitionId',
                'Required parameter "agentDefinitionId" was null or undefined when calling getAgentDefinition().'
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

        let urlPath = `/v1/agent-definitions/{agent_definition_id}`;
        urlPath = urlPath.replace('{agent_definition_id}', encodeURIComponent(String(requestParameters['agentDefinitionId'])));

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Get the current Agent Definition revision
     */
    async getAgentDefinitionRaw(requestParameters: GetAgentDefinitionRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<AgentDefinitionResource>> {
        const requestOptions = await this.getAgentDefinitionRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => AgentDefinitionResourceFromJSON(jsonValue));
    }

    /**
     * Get the current Agent Definition revision
     */
    async getAgentDefinition(requestParameters: GetAgentDefinitionRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<AgentDefinitionResource> {
        const response = await this.getAgentDefinitionRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for getAgentDefinitionRevision without sending the request
     */
    async getAgentDefinitionRevisionRequestOpts(requestParameters: GetAgentDefinitionRevisionRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['agentDefinitionId'] == null) {
            throw new runtime.RequiredError(
                'agentDefinitionId',
                'Required parameter "agentDefinitionId" was null or undefined when calling getAgentDefinitionRevision().'
            );
        }

        if (requestParameters['revision'] == null) {
            throw new runtime.RequiredError(
                'revision',
                'Required parameter "revision" was null or undefined when calling getAgentDefinitionRevision().'
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

        let urlPath = `/v1/agent-definitions/{agent_definition_id}/revisions/{revision}`;
        urlPath = urlPath.replace('{agent_definition_id}', encodeURIComponent(String(requestParameters['agentDefinitionId'])));
        urlPath = urlPath.replace('{revision}', encodeURIComponent(String(requestParameters['revision'])));

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Get one immutable Agent Definition revision
     */
    async getAgentDefinitionRevisionRaw(requestParameters: GetAgentDefinitionRevisionRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<AgentDefinitionResource>> {
        const requestOptions = await this.getAgentDefinitionRevisionRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => AgentDefinitionResourceFromJSON(jsonValue));
    }

    /**
     * Get one immutable Agent Definition revision
     */
    async getAgentDefinitionRevision(requestParameters: GetAgentDefinitionRevisionRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<AgentDefinitionResource> {
        const response = await this.getAgentDefinitionRevisionRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for listAgentDefinitions without sending the request
     */
    async listAgentDefinitionsRequestOpts(requestParameters: ListAgentDefinitionsRequest): Promise<runtime.RequestOpts> {
        const queryParameters: any = {};

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

        let urlPath = `/v1/agent-definitions`;

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Returns this App\'s stable resources at their current revision, newest first and cursor-paginated. Archived resources are excluded unless `include_archived` is true, and then carry a non-null `archived_at`.
     * List the App\'s Agent Definition resources
     */
    async listAgentDefinitionsRaw(requestParameters: ListAgentDefinitionsRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<AgentDefinitionResourceList>> {
        const requestOptions = await this.listAgentDefinitionsRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => AgentDefinitionResourceListFromJSON(jsonValue));
    }

    /**
     * Returns this App\'s stable resources at their current revision, newest first and cursor-paginated. Archived resources are excluded unless `include_archived` is true, and then carry a non-null `archived_at`.
     * List the App\'s Agent Definition resources
     */
    async listAgentDefinitions(requestParameters: ListAgentDefinitionsRequest = {}, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<AgentDefinitionResourceList> {
        const response = await this.listAgentDefinitionsRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for restoreAgentDefinition without sending the request
     */
    async restoreAgentDefinitionRequestOpts(requestParameters: RestoreAgentDefinitionRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['agentDefinitionId'] == null) {
            throw new runtime.RequiredError(
                'agentDefinitionId',
                'Required parameter "agentDefinitionId" was null or undefined when calling restoreAgentDefinition().'
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

        let urlPath = `/v1/agent-definitions/{agent_definition_id}/restore`;
        urlPath = urlPath.replace('{agent_definition_id}', encodeURIComponent(String(requestParameters['agentDefinitionId'])));

        return {
            path: urlPath,
            method: 'POST',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Clears the resource\'s archive tombstone so Invocation admission resolves it again. Restoring a live resource is a successful no-op.
     * Restore an archived Agent Definition
     */
    async restoreAgentDefinitionRaw(requestParameters: RestoreAgentDefinitionRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<void>> {
        const requestOptions = await this.restoreAgentDefinitionRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.VoidApiResponse(response);
    }

    /**
     * Clears the resource\'s archive tombstone so Invocation admission resolves it again. Restoring a live resource is a successful no-op.
     * Restore an archived Agent Definition
     */
    async restoreAgentDefinition(requestParameters: RestoreAgentDefinitionRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<void> {
        await this.restoreAgentDefinitionRaw(requestParameters, initOverrides);
    }

    /**
     * Creates request options for updateAgentDefinition without sending the request
     */
    async updateAgentDefinitionRequestOpts(requestParameters: UpdateAgentDefinitionRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['ifMatch'] == null) {
            throw new runtime.RequiredError(
                'ifMatch',
                'Required parameter "ifMatch" was null or undefined when calling updateAgentDefinition().'
            );
        }

        if (requestParameters['agentDefinitionId'] == null) {
            throw new runtime.RequiredError(
                'agentDefinitionId',
                'Required parameter "agentDefinitionId" was null or undefined when calling updateAgentDefinition().'
            );
        }

        if (requestParameters['agentDefinitionWrite'] == null) {
            throw new runtime.RequiredError(
                'agentDefinitionWrite',
                'Required parameter "agentDefinitionWrite" was null or undefined when calling updateAgentDefinition().'
            );
        }

        const queryParameters: any = {};

        const headerParameters: runtime.HTTPHeaders = {};

        headerParameters['Content-Type'] = 'application/json';

        if (requestParameters['ifMatch'] != null) {
            headerParameters['If-Match'] = String(requestParameters['ifMatch']);
        }

        if (this.configuration && this.configuration.accessToken) {
            const token = this.configuration.accessToken;
            const tokenString = await token("bearerAuth", []);

            if (tokenString) {
                headerParameters["Authorization"] = `Bearer ${tokenString}`;
            }
        }

        let urlPath = `/v1/agent-definitions/{agent_definition_id}`;
        urlPath = urlPath.replace('{agent_definition_id}', encodeURIComponent(String(requestParameters['agentDefinitionId'])));

        return {
            path: urlPath,
            method: 'PUT',
            headers: headerParameters,
            query: queryParameters,
            body: AgentDefinitionWriteToJSON(requestParameters['agentDefinitionWrite']),
        };
    }

    /**
     * If the App currently selects this Definition for anonymous access, the replacement must retain client_interface and either omit memory or set memory.scope to user.
     * Replace an Agent Definition and create its next revision
     */
    async updateAgentDefinitionRaw(requestParameters: UpdateAgentDefinitionRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<AgentDefinitionResource>> {
        const requestOptions = await this.updateAgentDefinitionRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => AgentDefinitionResourceFromJSON(jsonValue));
    }

    /**
     * If the App currently selects this Definition for anonymous access, the replacement must retain client_interface and either omit memory or set memory.scope to user.
     * Replace an Agent Definition and create its next revision
     */
    async updateAgentDefinition(requestParameters: UpdateAgentDefinitionRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<AgentDefinitionResource> {
        const response = await this.updateAgentDefinitionRaw(requestParameters, initOverrides);
        return await response.value();
    }

}
