import type { Invocation, ModelProvider } from "./generated/models/index.js";

export interface InvocationDiagnosticOptions {
  includeLogGuidance?: boolean;
}

export function invocationFailureMessage(
  invocationId: string,
  invocation: Pick<Invocation, "status" | "error">,
  provider?: ModelProvider,
  options: InvocationDiagnosticOptions = {},
): string {
  const reason = invocation.error
    ? `${invocation.error.code}: ${terminalSentence(invocation.error.message)}`
    : terminalSentence(invocation.status);
  const details = invocation.error?.details
    ? ` Safe details: ${JSON.stringify(invocation.error.details)}.`
    : "";
  const modelHelp = invocation.error?.code === "provider_error" && provider
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
