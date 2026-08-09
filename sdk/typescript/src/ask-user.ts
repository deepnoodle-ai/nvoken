/**
 * The `ask_user` convention.
 *
 * A structured question to the end user is a host tool, not a new resource.
 * The park/webhook/resume machinery already *is* "block until someone answers",
 * so nvoken needs no pending-interaction state and no response endpoint to
 * deliver this. What it does need to supply is a standard shape, so the model
 * and the host UI agree on what a question looks like without every
 * integration inventing its own.
 *
 * This is a convention, not runtime behaviour: nvoken treats `ask_user` like
 * any other host tool. Adopting it costs nothing and means a UI written
 * against one agent renders questions from another. The shape matches dive's
 * `toolkit` ask_user, so an agent already written against that needs no
 * translation layer.
 */
import type { HostTool } from "./client.js";

/** The well-known name. No `nvoken_` prefix: the host executes it, not the runtime. */
export const ASK_USER_TOOL_NAME = "ask_user";

export type AskUserKind = "confirm" | "select" | "multiselect" | "input";

export interface AskUserOption {
  value: string;
  label: string;
  description?: string;
  default?: boolean;
}

/** What the model sends. */
export interface AskUserInput {
  question: string;
  type: AskUserKind;
  options?: AskUserOption[];
  /** Pre-filled answer: `"true"`/`"false"` for confirm, text for input. */
  default?: string;
  min_select?: number;
  max_select?: number;
  multiline?: boolean;
}

/**
 * What the host returns as the tool result.
 *
 * `canceled` is not an error: a user declining to answer is a legitimate
 * outcome the model should see and reason about, whereas an error result would
 * read to it as a broken tool.
 */
export interface AskUserOutput {
  response?: string;
  values?: string[];
  canceled: boolean;
}

/** The tool input schema, in the bounded subset nvoken admits. */
export const ASK_USER_INPUT_SCHEMA = {
  type: "object",
  properties: {
    question: {
      type: "string",
      minLength: 1,
      maxLength: 2000,
      description: "The question to put to the user.",
    },
    type: {
      type: "string",
      enum: ["confirm", "select", "multiselect", "input"],
      description: "How the user answers.",
    },
    options: {
      type: "array",
      maxItems: 20,
      description: "Choices for select and multiselect. Ignored otherwise.",
      items: {
        type: "object",
        properties: {
          value: { type: "string", minLength: 1, maxLength: 200 },
          label: { type: "string", minLength: 1, maxLength: 200 },
          description: { type: "string", maxLength: 500 },
          default: { type: "boolean" },
        },
        required: ["value", "label"],
        additionalProperties: false,
      },
    },
    default: {
      type: "string",
      maxLength: 2000,
      description: 'Pre-filled answer: "true"/"false" for confirm, text for input.',
    },
    min_select: { type: "integer", minimum: 0, maximum: 20 },
    max_select: { type: "integer", minimum: 0, maximum: 20 },
    multiline: { type: "boolean" },
  },
  required: ["question", "type"],
  additionalProperties: false,
} as const;

const DEFAULT_DESCRIPTION =
  "Ask the user a question and wait for their answer. Use it when a decision "
  + "is genuinely theirs to make, not to confirm work you can verify yourself.";

/**
 * A ready-to-use host tool declaration. Supply a handler that renders the
 * question and resolves to an {@link AskUserOutput}.
 */
export function askUserTool(
  handler: (input: AskUserInput) => AskUserOutput | Promise<AskUserOutput>,
  description: string = DEFAULT_DESCRIPTION,
): HostTool<AskUserInput & Record<string, never>> {
  return {
    mode: "host",
    name: ASK_USER_TOOL_NAME,
    description,
    inputSchema: ASK_USER_INPUT_SCHEMA as unknown as Record<string, unknown>,
    handler: (input) => handler(input) as never,
  };
}
