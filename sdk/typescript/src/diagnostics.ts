import type { Invocation, ModelProvider } from "./generated/models/index.js";

import { NvokenError } from "./client.js";

export interface InvocationDiagnosticOptions {
  includeLogGuidance?: boolean;
}

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
  provider: ModelProvider,
  options: InvocationDiagnosticOptions = {},
): string {
  const reason = invocation.error
    ? `${invocation.error.code}: ${terminalSentence(invocation.error.message)}`
    : terminalSentence(invocation.status);
  const details = invocation.error?.details
    ? ` Safe details: ${JSON.stringify(invocation.error.details)}.`
    : "";
  const modelHelp = invocation.error?.code === "provider_error"
    ? ` Check available model IDs at ${modelDocumentation(provider)}.`
    : "";
  const logGuidance = options.includeLogGuidance
    ? ` Inspect structured daemon logs for invocation_id=${invocationId}; private upstream response bodies are intentionally omitted.`
    : "";
  return `Invocation ${invocationId} ${invocation.status}: ${reason}${details}${modelHelp}${logGuidance}`;
}

function terminalSentence(value: string): string {
  const trimmed = value.trim();
  return /[.!?]$/.test(trimmed) ? trimmed : `${trimmed}.`;
}

function modelDocumentation(provider: ModelProvider): string {
  return provider === "openai"
    ? "https://developers.openai.com/api/docs/models"
    : "https://platform.claude.com/docs/en/about-claude/models/overview";
}
