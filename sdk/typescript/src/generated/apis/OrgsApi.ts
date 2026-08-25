/* tslint:disable */
/* eslint-disable */
/**
 * nvoken API
 * nvoken runs agent turns for you. You describe a turn — an Agent Definition and some input — and nvoken queues it, runs it in the background, keeps running it across restarts and failures, and lets you either watch it live or come back for the result later.  One turn is one Invocation. `Invocation` is the name in every path, field, schema, and error, and it is the word to reach for when you need to be exact. `turn` is the English for the same thing, and this document uses it in explanatory prose. They are not two resources, so there is no relationship between them for you to model.  Your application stays in charge of what your agents are and when they run. nvoken owns the conversation: it stores the messages, tracks the state of every turn, and handles talking to the model providers.  ## Getting started  `POST /v1/invocations` starts a turn and returns a `202` right away. From there:  - Follow it live with `GET /v1/invocations/{invocation_id}/stream`, or read   `GET /v1/invocations/{invocation_id}/result` whenever you want the   finished answer. Disconnecting never cancels anything. - If your agent uses tools you run yourself, the turn stops with status   `waiting` and lists what it needs. Run them, post the results to   `/tool-results`, and the turn continues where it left off. - Sessions carry conversation history from one turn to the next. They   last until you delete them, or until a retention window you set runs   out.  Also here: tools nvoken calls back to over HTTPS, remote MCP servers, structured output validated against your JSON Schema, reusable Agent Definitions, image and document input, your own model provider keys, and spending limits.  ## Authorization  Machine credentials use `nvk_…` bearer API keys. A configured trusted console may also present a short-lived Ed25519 issuer token; this is an authentication presentation only and does not create a user identity model in nvoken.  Browser-direct callers use narrow JWTs, exact-origin CORS, and App/tenant/user admission limits. A host may mint client tokens from its own Ed25519 key. An App that explicitly enables anonymous access may instead let nvoken mint a short-lived access token and renewable visitor token from one allowed public origin.  A browser grant sees less, and it sees it in the same shape. There is one schema per resource, and the fields a browser may not see are simply omitted from its responses, which is why those fields are not required. There are no parallel browser schemas and no response unions, so every payload decodes against one type and nothing about the shape has to be inferred from the credential that fetched it. `GET /v1/identity` reports which kind of caller you are, under `authentication.method`.  - Full App keys can read and mutate every resource owned by their App. - Read-only App keys can read the same non-secret App and runtime data but   cannot mutate anything, including their own key lineage. - Installation-admin keys manage Orgs, Apps, and App keys but resolve no   App data. Short-lived console presentations provide fixed Org or admin   control-plane and reporting access.  Tenant and user assertion headers narrow individual requests. Durable API keys carry no tenant, Session, profile, or operation constraints.  ## Acting for one tenant or one end user  An app-wide credential can reach every tenant in its App, which means an id that arrives from the wrong place — a stale link, a mixed-up webhook, a tampered form field — is an id it can act on. The usual answer is for the host to re-read the resource and compare its `tenant_key` and `user_key` before every call. Say it once instead:  ``` X-Nvoken-Tenant-Key: acme X-Nvoken-User-Key: user-7c1f ```  Both headers are accepted on every request and narrow that one request to the scope they name. Anything outside it is reported as `not_found`, so a Session or Invocation belonging to another tenant or another end user cannot be read, cancelled, interrupted, forked, answered, or erased — and you learn nothing about whether the id exists. Writes take the same scope: an omitted `tenant_key` or `user_key` in the body inherits it, and one naming somebody else is refused.  Two rules keep them honest. They may only narrow, so a credential already bound to one tenant refuses a header naming another with `forbidden` rather than silently returning nothing. And a record with no `user_key` at all is outside every `X-Nvoken-User-Key` assertion rather than inside all of them, because a Session nobody claimed is not one you can claim by asserting.  Each header takes 1 to 255 characters and may appear once. A blank, oversized, or repeated header is `invalid_request` rather than ignored: an assertion nobody notices dropping is worse than no assertion at all.  A browser token already carries its tenant and its end user, so it neither needs these headers nor is allowed to send them.  ## Familiar names  Where a name already means something in other agent APIs, nvoken uses it the same way rather than inventing its own. `metadata` follows OpenAI\'s limits of 16 keys, 64-character names, and 512-byte values. `output_text` is the assistant\'s text joined into one string. `reasoning.effort` takes `low`, `medium`, `high`, `xhigh`, and `max`. `stop_reason: end_turn`, status `running`, and the `commentary` and `final_answer` message phases are the same idea you have seen elsewhere. If you have integrated another agent API, these should need no translation.  ## Keys and IDs  Every identifier here is one of two things, and its name tells you which.  A `*_key` is a name you choose. `tenant_key`, `agent_key`, `session_key`, and `definition_key` name a resource. Send one and nvoken either creates that reusable resource or hands back the one your name already refers to, so the same request twice gives you the same resource rather than two. `user_key` is a label rather than a name: it records who a Session was opened for, so you can filter on it later — and on an Agent whose Definition sets `memory.scope: user`, it also selects which memories that Agent can recall. It is fixed by the request that opens a Session and a later turn cannot change it. `idempotency_key` names one attempt, not a resource.  An `id` is an identifier nvoken mints. It carries a typed prefix, `sess_` for a Session and `inv_` for an Invocation, and everything after that prefix is opaque. Match on the prefix if it helps you route. Never parse past it, and never build one yourself.  A resource calls its own identifier `id`. A reference to a different resource carries that resource\'s name. `session_id` on an Invocation is the reusable conversation it belongs to, or null for a standalone turn. There is no third pattern.  Paths only ever take IDs. Keys travel in request bodies and query filters. Every keyed resource reports both its key and its `id`, so you can always get from your name to nvoken\'s identifier by reading the resource, and you never have to keep a mapping of your own.  ## You can always read back what applied  Anything nvoken decides on your behalf is readable on the resource that used it. You never have to work out what happened by combining the request you sent with your own assumptions about nvoken\'s defaults — just read the resource.  A turn reports the `limits` it is really running under, after defaults and minimums; the `definition` it ran with, exactly as stored; and `provenance`, which records what actually served the request. A Session reports its compaction (summarization) policy with `auto` already resolved to a real number and a real model, and its retention window as accepted. New settings will work the same way: a default you cannot read back is a setting only the server knows about.  ## Streaming  `GET /v1/invocations/{invocation_id}/stream` follows exactly one turn and closes after its terminal change is delivered. For standalone work its cursor is scoped to that Invocation and exposes no carrier ID. For a conversation-bound turn it uses the Session cursor scope, so the same cursor can resume the aggregate Session stream.  `GET /v1/sessions/{session_id}/stream` is the durable conversation subscription: it carries every turn in the Session and stays open while the conversation is idle. A standalone Invocation cursor cannot be used on this route because standalone work has no public Session.  Admission is separate from streaming. `POST /v1/invocations` returns the turn, and that response is your acknowledgment; then you open the stream. A dropped connection costs you nothing, because the turn already exists and you already hold its ID.  ### Saved frames and live frames  Every frame is one or the other, and the difference decides what you may store. A saved frame carries an SSE `id`. That ID is your resume position and the only value you need to keep. A live frame carries no `id`: it is a preview or a control signal, it is never stored, and it is never replayed.  `transcript.update` is the one saved frame. `message.delta`, `stream.resync`, and `connection.closing` are live.  ### Folding a transcript.update  It carries `cursor`, `messages`, and `invocation_changes`. Messages append by `sequence` and are never sent twice. Changes are an append log keyed by `(invocation_id, revision)`; fold to the highest revision for current state. Within one frame apply messages before changes, so a turn is never marked settled before its final message exists.  ### Resuming and finishing  The resume position has one name: `cursor`. It is the field on a durable frame and the query parameter that resumes a stream. Server-Sent Events mirrors it onto the `id:` line and accepts the `Last-Event-ID` header in the parameter\'s place, because a faithful SSE binding must; those are the binding\'s mechanics, not two more names for the value, and another binding would carry the same one name. `cursor` wins when a request supplies both.  **A turn is over when a change for it carries a terminal status.** That is the terminal signal, and there is no other. It is saved, so it replays on reconnect like every other change, at any cursor. Read `GET /v1/invocations/{invocation_id}` if you want the composed result.  The change tells you so directly: `terminal` is true on exactly the change that ends the turn. Read it instead of testing `status` against a set of your own, so a status added later cannot silently change what your code believes a turn\'s end is.  `connection.closing` says exactly what it says and nothing more. A client that has not seen its terminal change reconnects and keeps reading.  ### The stream stays open  An unfiltered stream is a subscription. It stays open while the Session is idle, keepalives and all, and a turn started later by anyone appears on it. You do not poll. A server may still reclaim an idle connection, and when it does it says `connection.closing` with reason `idle`, which means reconnect when you next need to read rather than immediately.  A filtered stream closes once it has delivered that turn\'s terminal change. A client following the exit rule has already left.  ### Previews  `message.delta` previews a message the model is writing. `kind` says what kind of content it is and `delta` carries the fragment, for every kind. Accumulate by `(message_id, content_index)`, and discard everything provisional when `attempt` increases, when `stream.resync` arrives, when the saved message with that ID lands, and when the turn\'s change carries a terminal status. Never store preview text as a message, and never use it to decide whether a turn succeeded.  `attempt`, `revision`, and `sequence` count from 1. `content_index` counts from 0.  ### Compatibility  Stream events may gain fields over time. Ignore fields you do not recognize rather than refusing the frame. New enum values may appear too. Treat an unknown `message.delta` kind as content you do not render. Treat an unknown `stream.resync` reason as `live_delivery_gap` and discard your previews. Treat an unknown `connection.closing` reason as `rotate` and reconnect with your last saved `id`. Reconnecting is always safe.  ### Transport  The protocol is the frames and their durability rules. Server-Sent Events is how they travel today and the only binding. Three mechanics belong to SSE itself: the `id:` line, the `retry:` opener, and comment keepalives. Everything else, resumption included, lives in the frames.  ### What the stream carries  Frames carry structure and text. Images and documents travel as descriptors, with media type, size in bytes, and a `sha256:` digest, never as inline bytes. Frame sizes stay bounded by text no matter what a turn produced.
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
     * Returns one registered Org. An Org console presentation receives `404` for any other Org, so identifiers cannot be probed across the ownership boundary. Installation issuer tokens require `admin: true`.
     * Get one org
     */
    async getOrgRaw(requestParameters: GetOrgRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<Org>> {
        const requestOptions = await this.getOrgRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => OrgFromJSON(jsonValue));
    }

    /**
     * Returns one registered Org. An Org console presentation receives `404` for any other Org, so identifiers cannot be probed across the ownership boundary. Installation issuer tokens require `admin: true`.
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
     * Returns the Orgs visible to the caller. Installation-admin keys and admin issuer tokens see every Org; an Org console presentation sees only its own Org. App keys and non-admin installation issuer tokens cannot list Orgs.  Archived Orgs are excluded unless `status` asks for them.
     * List registered orgs
     */
    async listOrgsRaw(requestParameters: ListOrgsRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<OrgList>> {
        const requestOptions = await this.listOrgsRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => OrgListFromJSON(jsonValue));
    }

    /**
     * Returns the Orgs visible to the caller. Installation-admin keys and admin issuer tokens see every Org; an Org console presentation sees only its own Org. App keys and non-admin installation issuer tokens cannot list Orgs.  Archived Orgs are excluded unless `status` asks for them.
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
     * Registers a thin customer ownership boundary. This requires an installation-admin key or an admin issuer token. When `external_ref` names an existing Org, the existing resource is returned unchanged so register-on-first-login is race-safe and idempotent.  Orgs are never hard-deleted; `DELETE /v1/orgs/{org_id}` archives.
     * Register an org
     */
    async registerOrgRaw(requestParameters: RegisterOrgOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<Org>> {
        const requestOptions = await this.registerOrgRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => OrgFromJSON(jsonValue));
    }

    /**
     * Registers a thin customer ownership boundary. This requires an installation-admin key or an admin issuer token. When `external_ref` names an existing Org, the existing resource is returned unchanged so register-on-first-login is race-safe and idempotent.  Orgs are never hard-deleted; `DELETE /v1/orgs/{org_id}` archives.
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
