// Generated modules directly rather than the barrel, so this subpath stays
// reachable without pulling the runtime client in.
import type { Conversation } from "./generated/models/Conversation.js";
import type { ConversationMessage } from "./generated/models/ConversationMessage.js";
import type { ToolCallSummary } from "./generated/models/ToolCallSummary.js";
import type { TurnChange } from "./generated/models/TurnChange.js";
import { isTurnOver } from "./turn-status.js";
import { blocksOf } from "./transcript.js";

/**
 * Which turn is live, and whether the agent still owes the transcript output.
 *
 * Sources disagree about this and each is stale in its own direction. The
 * stream is authoritative but reports a settlement one frame after the message
 * that ended the turn; a Conversation read is a point-in-time snapshot taken when a
 * consumer connects and again when a turn settles. Reconciling them in one
 * place is what keeps a header badge, a composer, and a transcript's working
 * indicator from telling three different stories — and every consumer that
 * draws a live conversation has to do it.
 */

/**
 * Whether a change ends its turn.
 *
 * nvoken states it on the change itself (`terminal`), and `isTurnOver` prefers
 * that field, falling back to the status set for a server too old to send it.
 * Keeping a local copy of that set is how a consumer comes to disagree with the
 * runtime about whether a turn is over — the set has four members and eight
 * statuses to get wrong.
 */
function settles(change: TurnChange): boolean {
  return isTurnOver(change);
}

/** The highest revision seen for each Turn — its current state. */
export function latestChanges(changes: TurnChange[]): TurnChange[] {
  const byTurn = new Map<string, TurnChange>();
  for (const change of changes) {
    const existing = byTurn.get(change.turnId);
    if (!existing || change.revision > existing.revision) {
      byTurn.set(change.turnId, change);
    }
  }
  return [...byTurn.values()];
}

export function settledTurns(changes: TurnChange[]): Set<string> {
  const settled = new Set<string>();
  for (const change of latestChanges(changes)) {
    if (settles(change)) settled.add(change.turnId);
  }
  return settled;
}

/**
 * What a parked turn is waiting on, read from its own lifecycle change.
 *
 * There is one tool-call collection, and a call somebody is expected to run is
 * the one carrying the arguments to run it with. Filtering on that is the whole
 * rule, and it is why this needs no Conversation read on a timer.
 */
export function parkedCalls(
  changes: TurnChange[],
  turnId: string | null,
): ToolCallSummary[] {
  if (!turnId) return [];
  const change = latestChanges(changes).find(
    (candidate) => candidate.turnId === turnId,
  );
  return (change?.toolCalls ?? []).filter((call) => call.arguments !== undefined);
}

export type Activity = {
  /** The Turn the Conversation is running, or null when it is idle. */
  turnId: string | null;
  /** Its status, live from the stream where the stream has an opinion. */
  status: string | null;
};

/**
 * Reconcile the stream's view of the Conversation with what a consumer knows from
 * outside it.
 *
 * The stream wins for any Turn it has reported on: it is where a turn's
 * status changes first, and preferring the other sources leaves a composer's
 * stop button up for a round trip after the answer is on screen, and a header
 * badge reading `queued` for a turn already streaming text.
 *
 * Two claims come from outside the stream, and each is live only until the
 * stream has an opinion of its own. `admitted` is a turn this caller just
 * started, which covers the gap between the create call returning and the
 * reopened stream's first frame — without it a composer drops back to its idle
 * state for a beat and invites a second send. The Conversation read covers a turn
 * some other client started between frames.
 */
export function resolveActivity(
  changes: TurnChange[],
  conversation: Conversation | null,
  admitted: Activity | null = null,
): Activity {
  const latest = latestChanges(changes);
  const running = latest
    .filter((change) => !settles(change))
    .sort((left, right) => left.occurredAt.getTime() - right.occurredAt.getTime())
    .at(-1);
  if (running) {
    return { turnId: running.turnId, status: running.status };
  }
  const known = new Set(latest.map((change) => change.turnId));
  const conversationClaim: Activity | null = conversation?.activeTurnId
    ? {
        turnId: conversation.activeTurnId,
        status: conversation.activeTurnStatus ?? "running",
      }
    : null;
  for (const claim of [admitted, conversationClaim]) {
    if (claim?.turnId && !known.has(claim.turnId)) return claim;
  }
  return { turnId: null, status: null };
}

/**
 * Whether the agent still owes this turn output, as opposed to the turn merely
 * not having settled yet.
 *
 * The runtime publishes the assistant message that ends a turn one frame
 * before the Turn's terminal status, so a plain "is the Turn
 * active" test stays true for a beat after the answer is fully on screen.
 * Reporting work in that window makes an indicator flash in and out under the
 * finished message. An assistant message with no tool_use block is the model's
 * last word — the loop has nothing left to run — so the work is over whether
 * or not the bookkeeping has caught up.
 */
export function awaitingOutput(
  messages: ConversationMessage[],
  turnId: string | null,
): boolean {
  if (!turnId) return false;
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index];
    if (message.turnId !== turnId) continue;
    if (message.role !== "assistant") continue;
    return blocksOf(message).some((block) => block.type === "tool_use");
  }
  // The turn is live and has produced nothing yet.
  return true;
}

export type SettlementNotice = {
  kind: "failed" | "cancelled" | "incomplete";
  message: string;
};

/**
 * The notice to show under a transcript, when the newest settlement was not a
 * clean finish. Suppressed once a later turn completes.
 */
export function settlementNotice(changes: TurnChange[]): SettlementNotice | null {
  const latest = latestChanges(changes);
  const problems = latest
    .filter((change) => {
      const status: string = change.status;
      return status === "failed" || status === "cancelled" || status === "incomplete";
    })
    .sort((left, right) => left.occurredAt.getTime() - right.occurredAt.getTime());
  const newest = problems.at(-1);
  if (!newest) return null;
  const recovered = latest.some(
    (change) =>
      change.status === "completed" &&
      change.occurredAt.getTime() > newest.occurredAt.getTime(),
  );
  if (recovered) return null;
  const status: string = newest.status;
  if (status === "cancelled") {
    return { kind: "cancelled", message: "The last turn was cancelled." };
  }
  if (status === "incomplete") {
    return {
      kind: "incomplete",
      message: "The last turn stopped early — it reached a limit before finishing.",
    };
  }
  const detail = newest.error?.message;
  return {
    kind: "failed",
    message: detail ? `The last turn failed: ${detail}` : "The last turn failed.",
  };
}
