import type { Invocation } from "./generated/models/index.js";

export interface InvocationDiagnosticOptions {
  includeLogGuidance?: boolean;
}

export function invocationFailureMessage(
  invocationId: string,
  invocation: Pick<Invocation, "status" | "error"> & { stopReason?: Invocation["stopReason"] },
  provider?: string,
  options: InvocationDiagnosticOptions = {},
): string {
  // An `incomplete` turn carries no error, so its stop reason is the only thing
  // that names the budget that stopped it.
  const reason = invocation.error
    ? `${invocation.error.code}: ${terminalSentence(invocation.error.message)}`
    : terminalSentence(invocation.stopReason ?? invocation.status);
  const details = invocation.error?.details
    ? ` Safe details: ${JSON.stringify(invocation.error.details)}.`
    : "";
  const documentation = provider ? modelDocumentation(provider) : undefined;
  const modelHelp = invocation.error?.code === "provider_error" && documentation
    ? ` Check available model IDs at ${documentation}.`
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

// Provider identity is an open string, so an unknown provider gets no link
// rather than another vendor's model list. Guessing would point a reader at
// documentation that cannot contain the model they asked for.
const MODEL_DOCUMENTATION: Record<string, string> = {
  anthropic: "https://platform.claude.com/docs/en/about-claude/models/overview",
  openai: "https://developers.openai.com/api/docs/models",
  xai: "https://docs.x.ai/docs/models",
  google: "https://ai.google.dev/gemini-api/docs/models",
};

function modelDocumentation(provider: string): string | undefined {
  return MODEL_DOCUMENTATION[provider];
}
