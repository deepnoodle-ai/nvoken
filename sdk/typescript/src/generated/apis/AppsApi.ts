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

import * as runtime from '../runtime.js';
import {
    type AnonymousTokenRequest,
    AnonymousTokenRequestFromJSON,
    AnonymousTokenRequestToJSON,
} from '../models/AnonymousTokenRequest.js';
import {
    type AnonymousTokenResponse,
    AnonymousTokenResponseFromJSON,
    AnonymousTokenResponseToJSON,
} from '../models/AnonymousTokenResponse.js';
import {
    type App,
    AppFromJSON,
    AppToJSON,
} from '../models/App.js';
import {
    type AppList,
    AppListFromJSON,
    AppListToJSON,
} from '../models/AppList.js';
import {
    type AppRegistration,
    AppRegistrationFromJSON,
    AppRegistrationToJSON,
} from '../models/AppRegistration.js';
import {
    type AppSigningKey,
    AppSigningKeyFromJSON,
    AppSigningKeyToJSON,
} from '../models/AppSigningKey.js';
import {
    type AppSigningKeyList,
    AppSigningKeyListFromJSON,
    AppSigningKeyListToJSON,
} from '../models/AppSigningKeyList.js';
import {
    type AppSigningKeyPurpose,
    AppSigningKeyPurposeFromJSON,
    AppSigningKeyPurposeToJSON,
} from '../models/AppSigningKeyPurpose.js';
import {
    type AppSigningKeySecret,
    AppSigningKeySecretFromJSON,
    AppSigningKeySecretToJSON,
} from '../models/AppSigningKeySecret.js';
import {
    type ClientKey,
    ClientKeyFromJSON,
    ClientKeyToJSON,
} from '../models/ClientKey.js';
import {
    type ClientKeyList,
    ClientKeyListFromJSON,
    ClientKeyListToJSON,
} from '../models/ClientKeyList.js';
import {
    type CreateClientKeyRequest,
    CreateClientKeyRequestFromJSON,
    CreateClientKeyRequestToJSON,
} from '../models/CreateClientKeyRequest.js';
import {
    type ErrorResponse,
    ErrorResponseFromJSON,
    ErrorResponseToJSON,
} from '../models/ErrorResponse.js';
import {
    type MintAppSigningKeyRequest,
    MintAppSigningKeyRequestFromJSON,
    MintAppSigningKeyRequestToJSON,
} from '../models/MintAppSigningKeyRequest.js';
import {
    type RegisterAppRequest,
    RegisterAppRequestFromJSON,
    RegisterAppRequestToJSON,
} from '../models/RegisterAppRequest.js';
import {
    type UpdateAppRequest,
    UpdateAppRequestFromJSON,
    UpdateAppRequestToJSON,
} from '../models/UpdateAppRequest.js';

export interface ActivateAppSigningKeyRequest {
    appId: string;
    purpose: AppSigningKeyPurpose;
    version: number;
}

export interface ArchiveAppRequest {
    appId: string;
}

export interface CreateAppClientKeyRequest {
    appId: string;
    createClientKeyRequest: CreateClientKeyRequest;
}

export interface GetAppRequest {
    appId: string;
}

export interface IssueAnonymousTokenRequest {
    appId: string;
    origin: string;
    idempotencyKey: string;
    anonymousTokenRequest: AnonymousTokenRequest;
}

export interface ListAppClientKeysRequest {
    appId: string;
}

export interface ListAppSigningKeysRequest {
    appId: string;
}

export interface ListAppsRequest {
    externalRef?: string;
    status?: ListAppsStatusEnum;
}

export interface MintAppSigningKeyOperationRequest {
    appId: string;
    mintAppSigningKeyRequest: MintAppSigningKeyRequest;
}

export interface RegisterAppOperationRequest {
    registerAppRequest: RegisterAppRequest;
}

export interface RestoreAppRequest {
    appId: string;
}

export interface RetireAppSigningKeyRequest {
    appId: string;
    purpose: AppSigningKeyPurpose;
    version: number;
}

export interface RevokeAppClientKeyRequest {
    appId: string;
    keyId: string;
}

export interface UpdateAppOperationRequest {
    appId: string;
    updateAppRequest: UpdateAppRequest;
}

/**
 *
 */
export class AppsApi extends runtime.BaseAPI {

    /**
     * Creates request options for activateAppSigningKey without sending the request
     */
    async activateAppSigningKeyRequestOpts(requestParameters: ActivateAppSigningKeyRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['appId'] == null) {
            throw new runtime.RequiredError(
                'appId',
                'Required parameter "appId" was null or undefined when calling activateAppSigningKey().'
            );
        }

        if (requestParameters['purpose'] == null) {
            throw new runtime.RequiredError(
                'purpose',
                'Required parameter "purpose" was null or undefined when calling activateAppSigningKey().'
            );
        }

        if (requestParameters['version'] == null) {
            throw new runtime.RequiredError(
                'version',
                'Required parameter "version" was null or undefined when calling activateAppSigningKey().'
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

        let urlPath = `/v1/apps/{app_id}/signing-keys/{purpose}/{version}/activate`;
        urlPath = urlPath.replace('{app_id}', encodeURIComponent(String(requestParameters['appId'])));
        urlPath = urlPath.replace('{purpose}', encodeURIComponent(String(requestParameters['purpose'])));
        urlPath = urlPath.replace('{version}', encodeURIComponent(String(requestParameters['version'])));

        return {
            path: urlPath,
            method: 'POST',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Moves signing to the named version. The delivery transport resolves the key per send, so this takes effect on the next delivery with no cache to invalidate anywhere. Activating the version that is already signing changes nothing.  Do this only once your receiver verifies against the new secret.
     * Sign with an existing signing key version
     */
    async activateAppSigningKeyRaw(requestParameters: ActivateAppSigningKeyRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<AppSigningKey>> {
        const requestOptions = await this.activateAppSigningKeyRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => AppSigningKeyFromJSON(jsonValue));
    }

    /**
     * Moves signing to the named version. The delivery transport resolves the key per send, so this takes effect on the next delivery with no cache to invalidate anywhere. Activating the version that is already signing changes nothing.  Do this only once your receiver verifies against the new secret.
     * Sign with an existing signing key version
     */
    async activateAppSigningKey(requestParameters: ActivateAppSigningKeyRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<AppSigningKey> {
        const response = await this.activateAppSigningKeyRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for archiveApp without sending the request
     */
    async archiveAppRequestOpts(requestParameters: ArchiveAppRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['appId'] == null) {
            throw new runtime.RequiredError(
                'appId',
                'Required parameter "appId" was null or undefined when calling archiveApp().'
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

        let urlPath = `/v1/apps/{app_id}`;
        urlPath = urlPath.replace('{app_id}', encodeURIComponent(String(requestParameters['appId'])));

        return {
            path: urlPath,
            method: 'DELETE',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Marks the App out of service. Nothing is destroyed and no other resource\'s lifecycle changes: the App\'s credentials keep authenticating, its client keys stay registered, and its Agent Definitions are untouched.  While archived, exactly these operations return `409 app_archived`: Session create and fork, Invocation create, Invocation resume, Agent Definition create and replace, client-key create, App-bound credential issuance, provider-key create, and credit allocation. Everything else behaves as on a live App — reads and lists, cancel, interrupt, nudges, tool-result submission, Session update and erasure, App `PATCH`, and credential, client-key, and provider-key rotation and revocation — so a draining host can let running turns settle and then clean up. Usage reporting keeps counting the App\'s spend.  Archiving requires the same authority as updating the App: an Org or installation credential. A credential bound to the App cannot archive or restore it. Repeating the call is a successful no-op.
     * Archive an app
     */
    async archiveAppRaw(requestParameters: ArchiveAppRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<void>> {
        const requestOptions = await this.archiveAppRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.VoidApiResponse(response);
    }

    /**
     * Marks the App out of service. Nothing is destroyed and no other resource\'s lifecycle changes: the App\'s credentials keep authenticating, its client keys stay registered, and its Agent Definitions are untouched.  While archived, exactly these operations return `409 app_archived`: Session create and fork, Invocation create, Invocation resume, Agent Definition create and replace, client-key create, App-bound credential issuance, provider-key create, and credit allocation. Everything else behaves as on a live App — reads and lists, cancel, interrupt, nudges, tool-result submission, Session update and erasure, App `PATCH`, and credential, client-key, and provider-key rotation and revocation — so a draining host can let running turns settle and then clean up. Usage reporting keeps counting the App\'s spend.  Archiving requires the same authority as updating the App: an Org or installation credential. A credential bound to the App cannot archive or restore it. Repeating the call is a successful no-op.
     * Archive an app
     */
    async archiveApp(requestParameters: ArchiveAppRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<void> {
        await this.archiveAppRaw(requestParameters, initOverrides);
    }

    /**
     * Creates request options for createAppClientKey without sending the request
     */
    async createAppClientKeyRequestOpts(requestParameters: CreateAppClientKeyRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['appId'] == null) {
            throw new runtime.RequiredError(
                'appId',
                'Required parameter "appId" was null or undefined when calling createAppClientKey().'
            );
        }

        if (requestParameters['createClientKeyRequest'] == null) {
            throw new runtime.RequiredError(
                'createClientKeyRequest',
                'Required parameter "createClientKeyRequest" was null or undefined when calling createAppClientKey().'
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

        let urlPath = `/v1/apps/{app_id}/client-keys`;
        urlPath = urlPath.replace('{app_id}', encodeURIComponent(String(requestParameters['appId'])));

        return {
            path: urlPath,
            method: 'POST',
            headers: headerParameters,
            query: queryParameters,
            body: CreateClientKeyRequestToJSON(requestParameters['createClientKeyRequest']),
        };
    }

    /**
     * Registers one standard-base64-encoded, exactly 32-byte Ed25519 public key. nvoken stores no seed or private key and never returns the public bytes. At most five keys may exist for one App so hosts can overlap a bounded rotation. Duplicate public bytes within one App are rejected; another App may independently register the same bytes.  A conforming App-issued JWT signed by an active key is accepted by the browser-direct runtime boundary.
     * Register an Ed25519 client-token verification key
     */
    async createAppClientKeyRaw(requestParameters: CreateAppClientKeyRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<ClientKey>> {
        const requestOptions = await this.createAppClientKeyRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => ClientKeyFromJSON(jsonValue));
    }

    /**
     * Registers one standard-base64-encoded, exactly 32-byte Ed25519 public key. nvoken stores no seed or private key and never returns the public bytes. At most five keys may exist for one App so hosts can overlap a bounded rotation. Duplicate public bytes within one App are rejected; another App may independently register the same bytes.  A conforming App-issued JWT signed by an active key is accepted by the browser-direct runtime boundary.
     * Register an Ed25519 client-token verification key
     */
    async createAppClientKey(requestParameters: CreateAppClientKeyRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<ClientKey> {
        const response = await this.createAppClientKeyRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for getApp without sending the request
     */
    async getAppRequestOpts(requestParameters: GetAppRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['appId'] == null) {
            throw new runtime.RequiredError(
                'appId',
                'Required parameter "appId" was null or undefined when calling getApp().'
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

        let urlPath = `/v1/apps/{app_id}`;
        urlPath = urlPath.replace('{app_id}', encodeURIComponent(String(requestParameters['appId'])));

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Returns one registered App. App keys and Org console presentations receive `404` for Apps outside their durable containment boundary.
     * Get one app
     */
    async getAppRaw(requestParameters: GetAppRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<App>> {
        const requestOptions = await this.getAppRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => AppFromJSON(jsonValue));
    }

    /**
     * Returns one registered App. App keys and Org console presentations receive `404` for Apps outside their durable containment boundary.
     * Get one app
     */
    async getApp(requestParameters: GetAppRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<App> {
        const response = await this.getAppRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for issueAnonymousToken without sending the request
     */
    async issueAnonymousTokenRequestOpts(requestParameters: IssueAnonymousTokenRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['appId'] == null) {
            throw new runtime.RequiredError(
                'appId',
                'Required parameter "appId" was null or undefined when calling issueAnonymousToken().'
            );
        }

        if (requestParameters['origin'] == null) {
            throw new runtime.RequiredError(
                'origin',
                'Required parameter "origin" was null or undefined when calling issueAnonymousToken().'
            );
        }

        if (requestParameters['idempotencyKey'] == null) {
            throw new runtime.RequiredError(
                'idempotencyKey',
                'Required parameter "idempotencyKey" was null or undefined when calling issueAnonymousToken().'
            );
        }

        if (requestParameters['anonymousTokenRequest'] == null) {
            throw new runtime.RequiredError(
                'anonymousTokenRequest',
                'Required parameter "anonymousTokenRequest" was null or undefined when calling issueAnonymousToken().'
            );
        }

        const queryParameters: any = {};

        const headerParameters: runtime.HTTPHeaders = {};

        headerParameters['Content-Type'] = 'application/json';

        if (requestParameters['origin'] != null) {
            headerParameters['Origin'] = String(requestParameters['origin']);
        }

        if (requestParameters['idempotencyKey'] != null) {
            headerParameters['Idempotency-Key'] = String(requestParameters['idempotencyKey']);
        }


        let urlPath = `/v1/apps/{app_id}/anonymous-tokens`;
        urlPath = urlPath.replace('{app_id}', encodeURIComponent(String(requestParameters['appId'])));

        return {
            path: urlPath,
            method: 'POST',
            headers: headerParameters,
            query: queryParameters,
            body: AnonymousTokenRequestToJSON(requestParameters['anonymousTokenRequest']),
        };
    }

    /**
     * Public, credential-free exchange for Apps that explicitly enable anonymous access. The request must carry exactly one canonical Origin that appears in the App\'s browser allowlist. Browser JavaScript does not set this header; the user agent supplies the page\'s actual Origin. Omit `visitor_token` on a first visit; persist every successful returned visitor token as an opaque replacement and present it on renewal to preserve the same visitor partition, tenant-scoped Agent, fixed thirty-day expiry, allowance, and canonical Session. Never discard a stored visitor token only because a network, `429`, or `5xx` response occurred.  Reuse one `Idempotency-Key` while retrying the same logical exchange. Exact retries recover the same visitor result without another rate slot; changed input conflicts. The access token lasts at most 15 minutes and never beyond visitor expiry. Responses are exact-origin CORS-enabled and use `Cache-Control: no-store`. Neither opaque token proves a human identity or supports individual revocation.
     * Mint anonymous browser access for one configured App
     */
    async issueAnonymousTokenRaw(requestParameters: IssueAnonymousTokenRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<AnonymousTokenResponse>> {
        const requestOptions = await this.issueAnonymousTokenRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => AnonymousTokenResponseFromJSON(jsonValue));
    }

    /**
     * Public, credential-free exchange for Apps that explicitly enable anonymous access. The request must carry exactly one canonical Origin that appears in the App\'s browser allowlist. Browser JavaScript does not set this header; the user agent supplies the page\'s actual Origin. Omit `visitor_token` on a first visit; persist every successful returned visitor token as an opaque replacement and present it on renewal to preserve the same visitor partition, tenant-scoped Agent, fixed thirty-day expiry, allowance, and canonical Session. Never discard a stored visitor token only because a network, `429`, or `5xx` response occurred.  Reuse one `Idempotency-Key` while retrying the same logical exchange. Exact retries recover the same visitor result without another rate slot; changed input conflicts. The access token lasts at most 15 minutes and never beyond visitor expiry. Responses are exact-origin CORS-enabled and use `Cache-Control: no-store`. Neither opaque token proves a human identity or supports individual revocation.
     * Mint anonymous browser access for one configured App
     */
    async issueAnonymousToken(requestParameters: IssueAnonymousTokenRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<AnonymousTokenResponse> {
        const response = await this.issueAnonymousTokenRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for listAppClientKeys without sending the request
     */
    async listAppClientKeysRequestOpts(requestParameters: ListAppClientKeysRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['appId'] == null) {
            throw new runtime.RequiredError(
                'appId',
                'Required parameter "appId" was null or undefined when calling listAppClientKeys().'
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

        let urlPath = `/v1/apps/{app_id}/client-keys`;
        urlPath = urlPath.replace('{app_id}', encodeURIComponent(String(requestParameters['appId'])));

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Lists the App\'s Ed25519 client-token verification-key records in creation order. Responses contain only the generated key ID, display name, SHA-256 fingerprint, and creation time; public-key bytes are never returned. This route requires the same host App-write authority as updating the visible App. Cross-App targets return `404`.
     * List an App\'s client-token verification keys
     */
    async listAppClientKeysRaw(requestParameters: ListAppClientKeysRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<ClientKeyList>> {
        const requestOptions = await this.listAppClientKeysRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => ClientKeyListFromJSON(jsonValue));
    }

    /**
     * Lists the App\'s Ed25519 client-token verification-key records in creation order. Responses contain only the generated key ID, display name, SHA-256 fingerprint, and creation time; public-key bytes are never returned. This route requires the same host App-write authority as updating the visible App. Cross-App targets return `404`.
     * List an App\'s client-token verification keys
     */
    async listAppClientKeys(requestParameters: ListAppClientKeysRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<ClientKeyList> {
        const response = await this.listAppClientKeysRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for listAppSigningKeys without sending the request
     */
    async listAppSigningKeysRequestOpts(requestParameters: ListAppSigningKeysRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['appId'] == null) {
            throw new runtime.RequiredError(
                'appId',
                'Required parameter "appId" was null or undefined when calling listAppSigningKeys().'
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

        let urlPath = `/v1/apps/{app_id}/signing-keys`;
        urlPath = urlPath.replace('{app_id}', encodeURIComponent(String(requestParameters['appId'])));

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Lists every receiver-facing version the App holds and marks the one that is signing, so a rotation can be started or resumed from observed state. Key material is never returned: plaintext is delivered exactly once, at registration or at mint.  The internal `anonymous_token` key is not part of this surface. It never leaves nvoken, so there is no receiver to rotate it around.  Like every route here, this one requires the app-less registration-class credential that provisions these keys. An App cannot read, rotate, or retire its own receiver credential.
     * List an App\'s callback and webhook signing key versions
     */
    async listAppSigningKeysRaw(requestParameters: ListAppSigningKeysRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<AppSigningKeyList>> {
        const requestOptions = await this.listAppSigningKeysRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => AppSigningKeyListFromJSON(jsonValue));
    }

    /**
     * Lists every receiver-facing version the App holds and marks the one that is signing, so a rotation can be started or resumed from observed state. Key material is never returned: plaintext is delivered exactly once, at registration or at mint.  The internal `anonymous_token` key is not part of this surface. It never leaves nvoken, so there is no receiver to rotate it around.  Like every route here, this one requires the app-less registration-class credential that provisions these keys. An App cannot read, rotate, or retire its own receiver credential.
     * List an App\'s callback and webhook signing key versions
     */
    async listAppSigningKeys(requestParameters: ListAppSigningKeysRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<AppSigningKeyList> {
        const response = await this.listAppSigningKeysRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for listApps without sending the request
     */
    async listAppsRequestOpts(requestParameters: ListAppsRequest): Promise<runtime.RequestOpts> {
        const queryParameters: any = {};

        if (requestParameters['externalRef'] != null) {
            queryParameters['external_ref'] = requestParameters['externalRef'];
        }

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

        let urlPath = `/v1/apps`;

        return {
            path: urlPath,
            method: 'GET',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Returns the Apps this caller can see. An App key sees only that App, an Org console presentation sees the Apps contained by its Org, and an installation-admin key sees every registered App. An exact `external_ref` filter narrows that visible set during the staged console migration. Archived Apps are excluded unless `status` asks for them.
     * List registered apps
     */
    async listAppsRaw(requestParameters: ListAppsRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<AppList>> {
        const requestOptions = await this.listAppsRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => AppListFromJSON(jsonValue));
    }

    /**
     * Returns the Apps this caller can see. An App key sees only that App, an Org console presentation sees the Apps contained by its Org, and an installation-admin key sees every registered App. An exact `external_ref` filter narrows that visible set during the staged console migration. Archived Apps are excluded unless `status` asks for them.
     * List registered apps
     */
    async listApps(requestParameters: ListAppsRequest = {}, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<AppList> {
        const response = await this.listAppsRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for mintAppSigningKey without sending the request
     */
    async mintAppSigningKeyRequestOpts(requestParameters: MintAppSigningKeyOperationRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['appId'] == null) {
            throw new runtime.RequiredError(
                'appId',
                'Required parameter "appId" was null or undefined when calling mintAppSigningKey().'
            );
        }

        if (requestParameters['mintAppSigningKeyRequest'] == null) {
            throw new runtime.RequiredError(
                'mintAppSigningKeyRequest',
                'Required parameter "mintAppSigningKeyRequest" was null or undefined when calling mintAppSigningKey().'
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

        let urlPath = `/v1/apps/{app_id}/signing-keys`;
        urlPath = urlPath.replace('{app_id}', encodeURIComponent(String(requestParameters['appId'])));

        return {
            path: urlPath,
            method: 'POST',
            headers: headerParameters,
            query: queryParameters,
            body: MintAppSigningKeyRequestToJSON(requestParameters['mintAppSigningKeyRequest']),
        };
    }

    /**
     * Writes version `n+1` and returns its plaintext exactly once. There is no way to read it again.  Rotation is a sequence rather than a swap, because a receiver\'s rejection of a signature is not retryable: a `401` settles the ToolCall as a delivery failure instead of re-arming it. So mint leaves nvoken signing with version `n`. Add the new secret to your verifier beside the old one — you already select by the delivered `X-Nvoken-Signing-Key-Id` and `X-Nvoken-Signing-Key-Version`, so holding two entries is configuration, not new code — then activate, then retire. Done in that order, no delivery ever fails verification.  Set `activate` only when there is no working verifier left to protect, which is what makes recovering a lost secret one call instead of three.  A purpose holds at most two versions. Minting a third is refused until the superseded one is retired, because no receiver could tell which pair it is meant to hold.
     * Mint the next signing key version for one purpose
     */
    async mintAppSigningKeyRaw(requestParameters: MintAppSigningKeyOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<AppSigningKeySecret>> {
        const requestOptions = await this.mintAppSigningKeyRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => AppSigningKeySecretFromJSON(jsonValue));
    }

    /**
     * Writes version `n+1` and returns its plaintext exactly once. There is no way to read it again.  Rotation is a sequence rather than a swap, because a receiver\'s rejection of a signature is not retryable: a `401` settles the ToolCall as a delivery failure instead of re-arming it. So mint leaves nvoken signing with version `n`. Add the new secret to your verifier beside the old one — you already select by the delivered `X-Nvoken-Signing-Key-Id` and `X-Nvoken-Signing-Key-Version`, so holding two entries is configuration, not new code — then activate, then retire. Done in that order, no delivery ever fails verification.  Set `activate` only when there is no working verifier left to protect, which is what makes recovering a lost secret one call instead of three.  A purpose holds at most two versions. Minting a third is refused until the superseded one is retired, because no receiver could tell which pair it is meant to hold.
     * Mint the next signing key version for one purpose
     */
    async mintAppSigningKey(requestParameters: MintAppSigningKeyOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<AppSigningKeySecret> {
        const response = await this.mintAppSigningKeyRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for registerApp without sending the request
     */
    async registerAppRequestOpts(requestParameters: RegisterAppOperationRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['registerAppRequest'] == null) {
            throw new runtime.RequiredError(
                'registerAppRequest',
                'Required parameter "registerAppRequest" was null or undefined when calling registerApp().'
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

        let urlPath = `/v1/apps`;

        return {
            path: urlPath,
            method: 'POST',
            headers: headerParameters,
            query: queryParameters,
            body: RegisterAppRequestToJSON(requestParameters['registerAppRequest']),
        };
    }

    /**
     * Registers one host application and creates its default tenant, returning the generated `app_id` and independent callback and webhook HMAC keys. The plaintext signing keys are returned only in this response; store them in the receiver\'s secret manager. nvoken stores only authenticated ciphertext and selects a key from the durable App scope of each delivery. Registration is unavailable when the service\'s encryption keyring is not configured.  Registration requires an Org console presentation or installation-admin key; an App key cannot mint siblings. Org callers always create Apps in their own Org and may omit `org_id`. Installation machine credentials may choose any registered Org or temporarily leave ownership unset during the staged console migration. An installation issuer token requires `admin: true` to assign an Org. Names identify Apps and are unique, so re-registering an existing name is rejected.
     * Register an app
     */
    async registerAppRaw(requestParameters: RegisterAppOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<AppRegistration>> {
        const requestOptions = await this.registerAppRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => AppRegistrationFromJSON(jsonValue));
    }

    /**
     * Registers one host application and creates its default tenant, returning the generated `app_id` and independent callback and webhook HMAC keys. The plaintext signing keys are returned only in this response; store them in the receiver\'s secret manager. nvoken stores only authenticated ciphertext and selects a key from the durable App scope of each delivery. Registration is unavailable when the service\'s encryption keyring is not configured.  Registration requires an Org console presentation or installation-admin key; an App key cannot mint siblings. Org callers always create Apps in their own Org and may omit `org_id`. Installation machine credentials may choose any registered Org or temporarily leave ownership unset during the staged console migration. An installation issuer token requires `admin: true` to assign an Org. Names identify Apps and are unique, so re-registering an existing name is rejected.
     * Register an app
     */
    async registerApp(requestParameters: RegisterAppOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<AppRegistration> {
        const response = await this.registerAppRaw(requestParameters, initOverrides);
        return await response.value();
    }

    /**
     * Creates request options for restoreApp without sending the request
     */
    async restoreAppRequestOpts(requestParameters: RestoreAppRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['appId'] == null) {
            throw new runtime.RequiredError(
                'appId',
                'Required parameter "appId" was null or undefined when calling restoreApp().'
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

        let urlPath = `/v1/apps/{app_id}/restore`;
        urlPath = urlPath.replace('{app_id}', encodeURIComponent(String(requestParameters['appId'])));

        return {
            path: urlPath,
            method: 'POST',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Clears the App\'s archive tombstone and reopens admission. Nothing else is restored, and the App\'s Org may still be archived. Restoring a live App is a successful no-op.
     * Restore an archived app
     */
    async restoreAppRaw(requestParameters: RestoreAppRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<void>> {
        const requestOptions = await this.restoreAppRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.VoidApiResponse(response);
    }

    /**
     * Clears the App\'s archive tombstone and reopens admission. Nothing else is restored, and the App\'s Org may still be archived. Restoring a live App is a successful no-op.
     * Restore an archived app
     */
    async restoreApp(requestParameters: RestoreAppRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<void> {
        await this.restoreAppRaw(requestParameters, initOverrides);
    }

    /**
     * Creates request options for retireAppSigningKey without sending the request
     */
    async retireAppSigningKeyRequestOpts(requestParameters: RetireAppSigningKeyRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['appId'] == null) {
            throw new runtime.RequiredError(
                'appId',
                'Required parameter "appId" was null or undefined when calling retireAppSigningKey().'
            );
        }

        if (requestParameters['purpose'] == null) {
            throw new runtime.RequiredError(
                'purpose',
                'Required parameter "purpose" was null or undefined when calling retireAppSigningKey().'
            );
        }

        if (requestParameters['version'] == null) {
            throw new runtime.RequiredError(
                'version',
                'Required parameter "version" was null or undefined when calling retireAppSigningKey().'
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

        let urlPath = `/v1/apps/{app_id}/signing-keys/{purpose}/{version}`;
        urlPath = urlPath.replace('{app_id}', encodeURIComponent(String(requestParameters['appId'])));
        urlPath = urlPath.replace('{purpose}', encodeURIComponent(String(requestParameters['purpose'])));
        urlPath = urlPath.replace('{version}', encodeURIComponent(String(requestParameters['version'])));

        return {
            path: urlPath,
            method: 'DELETE',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Deletes one version after signing has moved off it and your receiver has dropped it. The version that is currently signing is refused, so a mistaken retire fails loudly rather than silencing every delivery the App makes.  Nothing expires on a timer. Retirement is always an explicit call.
     * Retire a superseded signing key version
     */
    async retireAppSigningKeyRaw(requestParameters: RetireAppSigningKeyRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<void>> {
        const requestOptions = await this.retireAppSigningKeyRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.VoidApiResponse(response);
    }

    /**
     * Deletes one version after signing has moved off it and your receiver has dropped it. The version that is currently signing is refused, so a mistaken retire fails loudly rather than silencing every delivery the App makes.  Nothing expires on a timer. Retirement is always an explicit call.
     * Retire a superseded signing key version
     */
    async retireAppSigningKey(requestParameters: RetireAppSigningKeyRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<void> {
        await this.retireAppSigningKeyRaw(requestParameters, initOverrides);
    }

    /**
     * Creates request options for revokeAppClientKey without sending the request
     */
    async revokeAppClientKeyRequestOpts(requestParameters: RevokeAppClientKeyRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['appId'] == null) {
            throw new runtime.RequiredError(
                'appId',
                'Required parameter "appId" was null or undefined when calling revokeAppClientKey().'
            );
        }

        if (requestParameters['keyId'] == null) {
            throw new runtime.RequiredError(
                'keyId',
                'Required parameter "keyId" was null or undefined when calling revokeAppClientKey().'
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

        let urlPath = `/v1/apps/{app_id}/client-keys/{key_id}`;
        urlPath = urlPath.replace('{app_id}', encodeURIComponent(String(requestParameters['appId'])));
        urlPath = urlPath.replace('{key_id}', encodeURIComponent(String(requestParameters['keyId'])));

        return {
            path: urlPath,
            method: 'DELETE',
            headers: headerParameters,
            query: queryParameters,
        };
    }

    /**
     * Deletes only the named App-owned verification record. A repeated, unknown, or cross-App key ID returns the same `404`. Agent Definitions and App configuration are never changed by revocation.
     * Revoke an App client-token verification key
     */
    async revokeAppClientKeyRaw(requestParameters: RevokeAppClientKeyRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<void>> {
        const requestOptions = await this.revokeAppClientKeyRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.VoidApiResponse(response);
    }

    /**
     * Deletes only the named App-owned verification record. A repeated, unknown, or cross-App key ID returns the same `404`. Agent Definitions and App configuration are never changed by revocation.
     * Revoke an App client-token verification key
     */
    async revokeAppClientKey(requestParameters: RevokeAppClientKeyRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<void> {
        await this.revokeAppClientKeyRaw(requestParameters, initOverrides);
    }

    /**
     * Creates request options for updateApp without sending the request
     */
    async updateAppRequestOpts(requestParameters: UpdateAppOperationRequest): Promise<runtime.RequestOpts> {
        if (requestParameters['appId'] == null) {
            throw new runtime.RequiredError(
                'appId',
                'Required parameter "appId" was null or undefined when calling updateApp().'
            );
        }

        if (requestParameters['updateAppRequest'] == null) {
            throw new runtime.RequiredError(
                'updateAppRequest',
                'Required parameter "updateAppRequest" was null or undefined when calling updateApp().'
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

        let urlPath = `/v1/apps/{app_id}`;
        urlPath = urlPath.replace('{app_id}', encodeURIComponent(String(requestParameters['appId'])));

        return {
            path: urlPath,
            method: 'PATCH',
            headers: headerParameters,
            query: queryParameters,
            body: UpdateAppRequestToJSON(requestParameters['updateAppRequest']),
        };
    }

    /**
     * Updates an App\'s display name, callback timeout, browser configuration, anonymous access mode, or credit policy. An installation administrator may also transfer the App to another registered Org by changing `org_id`. Org- and App-scoped callers receive `404` outside their containment boundary, and cannot move an App. The unique `name` and transitional `external_ref` cannot be changed.
     * Update an app
     */
    async updateAppRaw(requestParameters: UpdateAppOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<runtime.ApiResponse<App>> {
        const requestOptions = await this.updateAppRequestOpts(requestParameters);
        const response = await this.request(requestOptions, initOverrides);

        return new runtime.JSONApiResponse(response, (jsonValue) => AppFromJSON(jsonValue));
    }

    /**
     * Updates an App\'s display name, callback timeout, browser configuration, anonymous access mode, or credit policy. An installation administrator may also transfer the App to another registered Org by changing `org_id`. Org- and App-scoped callers receive `404` outside their containment boundary, and cannot move an App. The unique `name` and transitional `external_ref` cannot be changed.
     * Update an app
     */
    async updateApp(requestParameters: UpdateAppOperationRequest, initOverrides?: RequestInit | runtime.InitOverrideFunction): Promise<App> {
        const response = await this.updateAppRaw(requestParameters, initOverrides);
        return await response.value();
    }

}

/**
 * @export
 */
export const ListAppsStatusEnum = {
    Active: 'active',
    Archived: 'archived',
    All: 'all'
} as const;
export type ListAppsStatusEnum = typeof ListAppsStatusEnum[keyof typeof ListAppsStatusEnum];
