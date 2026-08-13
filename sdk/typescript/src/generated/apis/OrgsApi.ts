/* tslint:disable */
/* eslint-disable */
/**
 * nvoken API
 * nvoken runs agent turns for you. You describe a turn — an Agent Definition and some input — and nvoken queues it, runs it in the background, keeps running it across restarts and failures, and lets you either watch it live or come back for the result later.  Your application stays in charge of what your agents are and when they run. nvoken owns the conversation: it stores the messages, tracks the state of every turn, and handles talking to the model providers.  ## Getting started  `POST /v1/invocations` starts a turn and returns a `202` right away. From there:  - Follow it live with `GET /v1/invocations/{invocation_id}/stream`, or   read `GET /v1/invocations/{invocation_id}/result` whenever you want the   finished answer. Disconnecting never cancels anything. - If your agent uses tools you run yourself, the turn stops with status   `waiting` and lists what it needs. Run them, post the results to   `/tool-results`, and the turn continues where it left off. - Sessions carry conversation history from one turn to the next. They   last until you delete them, or until a retention window you set runs   out.  Also here: tools nvoken calls back to over HTTPS, remote MCP servers, structured output validated against your JSON Schema, reusable Agent Definitions, image and document input, your own model provider keys, and spending limits.  ## Authorization  Machine credentials use `nvk_…` bearer API keys. A configured trusted console may also present a short-lived Ed25519 issuer token; this is an authentication presentation only and does not create a user identity model in nvoken.  Browser-direct callers use narrow JWTs, exact-origin CORS, client-safe projections, and App/tenant/user admission limits. A host may mint client tokens from its own Ed25519 key. An App that explicitly enables anonymous access may instead let nvoken mint a short-lived access token and renewable visitor token from one allowed public origin.  - App-scoped Runtime credentials may call every Runtime operation and GET /v1/identity. - App-scoped Viewer credentials may call Runtime reads and GET /v1/identity. - App-scoped Operator credentials may call every Runtime operation, GET /v1/identity, and all credential lifecycle operations. - Org-scoped Viewer and Operator credentials are management and reporting   identities only. They resolve no tenant and cannot perform Runtime   operations; Operators can register and manage Apps and their App   credentials, while Viewers have read-only access.  Tenant, Session, operation, and expiry constraints only narrow these grants.  ## Familiar names  Where a name already means something in other agent APIs, nvoken uses it the same way rather than inventing its own. `metadata` follows OpenAI\'s limits of 16 keys, 64-character names, and 512-byte values. `output_text` is the assistant\'s text joined into one string. `reasoning.effort` takes `low`, `medium`, `high`, `xhigh`, and `max`. `stop_reason: end_turn`, status `running`, and the `commentary` and `final_answer` message phases are the same idea you have seen elsewhere. If you have integrated another agent API, these should need no translation.  ## You can always read back what applied  Anything nvoken decides on your behalf is readable on the resource that used it. You never have to work out what happened by combining the request you sent with your own assumptions about nvoken\'s defaults — just read the resource.  A turn reports the `limits` it is really running under, after defaults and minimums; the `agent_definition` it ran with, exactly as stored; and `provenance`, which records what actually served the request. A Session reports its compaction (summarization) policy with `auto` already resolved to a real number and a real model, and its retention window as accepted. New settings will work the same way: a default you cannot read back is a setting only the server knows about.  ## Streaming  A turn and an Invocation are the same thing. The endpoint summaries say turn; the schemas say Invocation.  Two streams carry the same frames. `GET /v1/invocations/{invocation_id}/stream` follows one turn and ends when that turn settles. `GET /v1/sessions/{session_id}/transcript/stream` follows every turn in a Session, and is the surface to use for a conversation. `POST /v1/invocations` with `Accept: text/event-stream` admits and streams one turn inline.  ### Saved frames and live frames  Every frame is one or the other, and the difference decides what you may store. A saved frame carries an SSE `id`. That ID is your resume position and the only value you need to keep. A live frame carries no `id`: it is a preview or a control signal, it is never stored, and it is never replayed.  The Invocation stream\'s saved frames are `invocation.accepted`, `invocation.update`, and `invocation.result`. The Session stream\'s only saved frame is `transcript.update`. Every other frame on either stream is live.  ### Resuming and finishing  The resume position has four spellings and one value: the SSE `id` line, `resume_cursor` inside a frame payload, the `cursor` query parameter, and the `Last-Event-ID` header. Send it back as `cursor` or as `Last-Event-ID`; `cursor` wins when a request carries both. Cursors are Session-scoped on both streams, so a position taken from one stream resumes the other.  Reconnecting to a turn that has already settled always yields `invocation.result` followed by `stream.end` with reason `terminal`, at any cursor. Both are valid signals that a turn is over, and a client may exit on either.  `invocation.accepted` is emitted only by the inline `POST` path. The `GET` stream never sends it, so a client that admits separately never sees it. The nvoken SDKs synthesize an equivalent locally so their callers see the same first event either way.  An `invocation.update` never carries a terminal status. Terminal state arrives as `invocation.result` and nowhere else on that stream. The `invocation` it carries is re-read when the frame is written, so it is current state with a resume position attached rather than a snapshot taken at the cursor.  ### Previews  `output_text.delta` and `thinking.delta` preview one model iteration. Their identity is `(invocation_id, attempt, iteration, content_index)`. Accumulate by that tuple, and discard everything provisional when `attempt` increases, when `stream.resync` arrives, when the saved message lands, and when the turn reaches a terminal status. One model iteration produces exactly one saved assistant message, so previews sharing an `(invocation_id, attempt, iteration)` build one message. Never store preview text as a message, and never use it to decide whether a turn succeeded.  `attempt`, `iteration`, `revision`, and `sequence` count from 1. `content_index` counts from 0.  ### Compatibility  Stream events may gain fields over time. Ignore fields you do not recognize rather than refusing the frame. New enum values may appear too. Treat an unknown `stream.resync` reason as `live_delivery_gap` and discard your previews. Treat an unknown `stream.end` reason as `rotate` and reconnect with your last saved `id`. Reconnecting is always safe: a turn that has settled re-yields its result.  ### Transport  The protocol is the frames and their durability rules. Server-Sent Events is how they travel today and the only binding. Three mechanics belong to SSE itself: the `id:` line, the `retry:` opener, and comment keepalives. Everything else, resumption included, lives in the frames.  ### What the stream carries  Frames carry structure and text. Images and documents travel as descriptors, with media type, size in bytes, and a `sha256:` digest, never as inline bytes. Frame sizes stay bounded by text no matter what a turn produced.
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
    type Org,
    OrgFromJSON,
    OrgToJSON,
} from '../models/Org.js';
import {
    type OrgList,
    OrgListFromJSON,
    OrgListToJSON,
} from '../models/OrgList.js';
import {
    type RegisterOrgRequest,
    RegisterOrgRequestFromJSON,
    RegisterOrgRequestToJSON,
} from '../models/RegisterOrgRequest.js';
import {
    type UpdateOrgRequest,
    UpdateOrgRequestFromJSON,
    UpdateOrgRequestToJSON,
} from '../models/UpdateOrgRequest.js';

export interface ArchiveOrgRequest {
    orgId: string;
}

export interface GetOrgRequest {
    orgId: string;
}

export interface ListOrgsRequest {
    status?: ListOrgsStatusEnum;
}

export interface RegisterOrgOperationRequest {
    registerOrgRequest: RegisterOrgRequest;
}

export interface RestoreOrgRequest {
    orgId: string;
}

export interface UpdateOrgOperationRequest {
    orgId: string;
    updateOrgRequest: UpdateOrgRequest;
}

/**
 *
 */
export class OrgsApi extends runtime.BaseAPI {

    /**
     * Creates request options for archiveOrg without sending the request
     */
    async archiveOrgRequestOpts(requestParameters: ArchiveOrgRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['orgId'] == null) {
            throw new runtime.RequiredError(
                'orgId',
                'Required parameter "orgId" was null or undefined when calling archiveOrg().'
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

        let urlPath = `/v1/orgs/{org_id}`;
        urlPath = urlPath.replace('{org_id}', encodeURIComponent(String(requestParameters['orgId'])));

        return {
            path: urlPath,
            method: 'DELETE',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Marks the Org out of service. Every App it owns must be archived first; this is an explicit precondition rather than a cascade, so no irreversible side effect hides inside a reversible operation.  Nothing is destroyed. An archived Org refuses App registration into it and Org-bound credential issuance with `409 org_archived`, while Org reads and org-scoped reporting stay open. Archiving requires the same authority as updating the Org, and repeating it is a successful no-op.
     * Archive an org
     */
    async archiveOrgRaw(requestParameters: ArchiveOrgRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<void>> {
        const requestOptions = await this.archiveOrgRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.VoidApiResponse(response);
    }

    /**
     * Marks the Org out of service. Every App it owns must be archived first; this is an explicit precondition rather than a cascade, so no irreversible side effect hides inside a reversible operation.  Nothing is destroyed. An archived Org refuses App registration into it and Org-bound credential issuance with `409 org_archived`, while Org reads and org-scoped reporting stay open. Archiving requires the same authority as updating the Org, and repeating it is a successful no-op.
     * Archive an org
     */
    async archiveOrg(requestParameters: ArchiveOrgRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<void> {
        await this.archiveOrgRaw(requestParameters, initOverrides);
    }

    /**
     * Creates request options for getOrg without sending the request
     */
    async getOrgRequestOpts(requestParameters: GetOrgRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['orgId'] == null) {
            throw new runtime.RequiredError(
                'orgId',
                'Required parameter "orgId" was null or undefined when calling getOrg().'
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

        let urlPath = `/v1/orgs/{org_id}`;
        urlPath = urlPath.replace('{org_id}', encodeURIComponent(String(requestParameters['orgId'])));

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Returns one registered Org. An Org-scoped caller receives `404` for any other Org, so identifiers cannot be probed across the ownership boundary. Installation issuer tokens require `admin: true`.
     * Get one org
     */
    async getOrgRaw(requestParameters: GetOrgRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<Org>> {
        const requestOptions = await this.getOrgRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => OrgFromJSON(jsonValue));
    }

    /**
     * Returns one registered Org. An Org-scoped caller receives `404` for any other Org, so identifiers cannot be probed across the ownership boundary. Installation issuer tokens require `admin: true`.
     * Get one org
     */
    async getOrg(requestParameters: GetOrgRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<Org> {
        const response = await this.getOrgRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for listOrgs without sending the request
     */
    async listOrgsRequestOpts(requestParameters: ListOrgsRequest): Promise<runtime.RequestOpts> {
        const queryParameters: any = {};

        if (requestParameters['status'] != null) {
            queryParameters['status'] = requestParameters['status'];
        }

        const headerParameters: runtime.HTTPHeaders = {};

        if (this.configuration && this.configuration.accessToken) {
            const token = this.configuration.accessToken;
            const tokenString = await token("bearerAuth", []);

            if (tokenString) {
                headerParameters["Authorization"] = `Bearer ${tokenString}`;
            }
        }

        let urlPath = `/v1/orgs`;

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Returns the Orgs visible to the caller. Installation machine credentials and admin issuer tokens see every Org; an Org-scoped credential sees only its own Org. App-scoped credentials and non-admin installation issuer tokens cannot list Orgs.  Archived Orgs are excluded unless `status` asks for them.
     * List registered orgs
     */
    async listOrgsRaw(requestParameters: ListOrgsRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<OrgList>> {
        const requestOptions = await this.listOrgsRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => OrgListFromJSON(jsonValue));
    }

    /**
     * Returns the Orgs visible to the caller. Installation machine credentials and admin issuer tokens see every Org; an Org-scoped credential sees only its own Org. App-scoped credentials and non-admin installation issuer tokens cannot list Orgs.  Archived Orgs are excluded unless `status` asks for them.
     * List registered orgs
     */
    async listOrgs(requestParameters: ListOrgsRequest = {}, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<OrgList> {
        const response = await this.listOrgsRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for registerOrg without sending the request
     */
    async registerOrgRequestOpts(requestParameters: RegisterOrgOperationRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['registerOrgRequest'] == null) {
            throw new runtime.RequiredError(
                'registerOrgRequest',
                'Required parameter "registerOrgRequest" was null or undefined when calling registerOrg().'
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

        let urlPath = `/v1/orgs`;

        return {
            path: urlPath,
            method: 'POST',
            headers: headerParameters,
            query: queryParameters,
            body: RegisterOrgRequestToJSON(requestParameters['registerOrgRequest']),
        };
    }

    /**
     * Registers a thin customer ownership boundary. This requires an installation Operator credential or an admin issuer token. When `external_ref` names an existing Org, the existing resource is returned unchanged so register-on-first-login is race-safe and idempotent.  Orgs are never hard-deleted; `DELETE /v1/orgs/{org_id}` archives.
     * Register an org
     */
    async registerOrgRaw(requestParameters: RegisterOrgOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<Org>> {
        const requestOptions = await this.registerOrgRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => OrgFromJSON(jsonValue));
    }

    /**
     * Registers a thin customer ownership boundary. This requires an installation Operator credential or an admin issuer token. When `external_ref` names an existing Org, the existing resource is returned unchanged so register-on-first-login is race-safe and idempotent.  Orgs are never hard-deleted; `DELETE /v1/orgs/{org_id}` archives.
     * Register an org
     */
    async registerOrg(requestParameters: RegisterOrgOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<Org> {
        const response = await this.registerOrgRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for restoreOrg without sending the request
     */
    async restoreOrgRequestOpts(requestParameters: RestoreOrgRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['orgId'] == null) {
            throw new runtime.RequiredError(
                'orgId',
                'Required parameter "orgId" was null or undefined when calling restoreOrg().'
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

        let urlPath = `/v1/orgs/{org_id}/restore`;
        urlPath = urlPath.replace('{org_id}', encodeURIComponent(String(requestParameters['orgId'])));

        return {
            path: urlPath,
            method: 'POST',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Clears the Org\'s archive tombstone. There is no ordering precondition and nothing else is restored: Apps archived before the Org stay archived. Restoring a live Org is a successful no-op.
     * Restore an archived org
     */
    async restoreOrgRaw(requestParameters: RestoreOrgRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<void>> {
        const requestOptions = await this.restoreOrgRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.VoidApiResponse(response);
    }

    /**
     * Clears the Org\'s archive tombstone. There is no ordering precondition and nothing else is restored: Apps archived before the Org stay archived. Restoring a live Org is a successful no-op.
     * Restore an archived org
     */
    async restoreOrg(requestParameters: RestoreOrgRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<void> {
        await this.restoreOrgRaw(requestParameters, initOverrides);
    }

    /**
     * Creates request options for updateOrg without sending the request
     */
    async updateOrgRequestOpts(requestParameters: UpdateOrgOperationRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['orgId'] == null) {
            throw new runtime.RequiredError(
                'orgId',
                'Required parameter "orgId" was null or undefined when calling updateOrg().'
            );
        }

        if (requestParameters['updateOrgRequest'] == null) {
            throw new runtime.RequiredError(
                'updateOrgRequest',
                'Required parameter "updateOrgRequest" was null or undefined when calling updateOrg().'
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

        let urlPath = `/v1/orgs/{org_id}`;
        urlPath = urlPath.replace('{org_id}', encodeURIComponent(String(requestParameters['orgId'])));

        return {
            path: urlPath,
            method: 'PATCH',
            headers: headerParameters,
            query: queryParameters,
            body: UpdateOrgRequestToJSON(requestParameters['updateOrgRequest']),
        };
    }

    /**
     * Updates the Org\'s human-facing display name. Installation issuer tokens require `admin: true`.
     * Update an org
     */
    async updateOrgRaw(requestParameters: UpdateOrgOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<Org>> {
        const requestOptions = await this.updateOrgRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => OrgFromJSON(jsonValue));
    }

    /**
     * Updates the Org\'s human-facing display name. Installation issuer tokens require `admin: true`.
     * Update an org
     */
    async updateOrg(requestParameters: UpdateOrgOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<Org> {
        const response = await this.updateOrgRaw(requestParameters, initOverrides);
        return await response.value();
    }

}

/**
 * @export
 */
export const ListOrgsStatusEnum = {
    Active: 'active',
    Archived: 'archived',
    All: 'all'
} as const;
export type ListOrgsStatusEnum = typeof ListOrgsStatusEnum[keyof typeof ListOrgsStatusEnum];
