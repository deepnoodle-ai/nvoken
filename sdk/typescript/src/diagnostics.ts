import type { Invocation } from "./generated/models/index.js";

import { NvokenError } from "./client.js";
import {
  invocationFailureMessage,
  type InvocationDiagnosticOptions,
} from "./invocation-error.js";

export type { InvocationDiagnosticOptions } from "./invocation-error.js";

/** Render a public one-line diagnostic without exposing an internal stack. */
export function formatNvokenError(error: unknown): string {
  if (error instanceof NvokenError) {
    const code = error.code ? ` code=${error.code}` : "";
    const request = error.requestId ? ` request_id=${error.requestId}` : "";
    return `nvoken error [${error.category}]${code}${request}: ${error.message}`;
  }
  return error instanceof Error ? error.message : String(error);
}

/** Render a terminal Invocation failure using only public, normalized fields. */
export function formatInvocationFailure(
  invocationId: string,
  invocation: Pick<Invocation, "status" | "error">,
  provider: string,
  options: InvocationDiagnosticOptions = {},
): string {
  return invocationFailureMessage(invocationId, invocation, provider, options);
}
