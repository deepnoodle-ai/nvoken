// Imported from the generated module rather than the barrel deliberately. This
// subpath has to stay reachable without pulling the runtime client in, and that
// file has no imports of its own.
import { InvocationStatus } from "./generated/models/InvocationStatus.js";

/**
 * Every Invocation status, as values rather than as a type.
 *
 * Re-exported here because `listInvocations({ status })` filters server-side
 * and takes values, so a caller who could only reach the type had to hand-write
 * the list — which is the exact fork this module exists to prevent.
 */
export { InvocationStatus };

/**
 * The statuses that mean a turn has stopped for good.
 *
 * Exported so no caller has to keep a copy. This is the same reason
 * `answerableToolCalls` exists: the filter is part of the protocol, so
 * rediscovering it in every application is how one of them gets it wrong.
 */
export const TERMINAL_INVOCATION_STATUSES: readonly InvocationStatus[] = [
  "completed",
  "incomplete",
  "failed",
  "cancelled",
];

const TERMINAL = new Set<string>(TERMINAL_INVOCATION_STATUSES);

/**
 * The statuses that mean a turn is still going — what
 * `listInvocations({ status: [...] })` wants for a teardown, sweep, or
 * reconciler.
 *
 * Derived from the enum rather than written out, so a status added to the
 * contract lands here on the next release without anyone remembering to add
 * it. That is the safe direction: a turn you did not know about shows up in
 * "still live" and gets waited on, rather than being silently dropped from the
 * sweep that was supposed to find it.
 */
export const ACTIVE_INVOCATION_STATUSES: readonly InvocationStatus[] =
  Object.values(InvocationStatus).filter((status) => !TERMINAL.has(status));

/**
 * Whether a status means the turn is over.
 *
 * There are eight statuses and four of them are terminal, so the interesting
 * mistake is writing the *other* four out. `queued`, `running`, `waiting`, and
 * `budget_hold` differ only in what unblocks them — a `budget_hold` turn stopped on
 * spending capacity with its deadlines on hold, and resumes on its own once
 * its account is funded — and a turn wrongly believed to be finished is one
 * nobody settles, reattaches to, or cancels before erasing its Session.
 *
 * A status this build does not recognize is therefore reported as *not*
 * terminal, which is the safe direction: you wait on a turn that already
 * ended, rather than abandoning one that has not.
 */
export function isTerminalStatus(status: string): boolean {
  return TERMINAL.has(status);
}

/**
 * Whether this change ends the turn. **This is the terminal signal, and there
 * is no other.** There is no result frame, and no other frame ends a turn.
 *
 * It answers for the change, not for the turn: a replayed `running` change
 * reports false even after the turn has ended, which is what lets you fold
 * messages before changes and never mark a turn settled before its final
 * message exists.
 *
 * Either witness suffices. The field and the status always agree when both are
 * present — nvoken computes one from the other — so accepting either keeps this
 * correct against a server too old to send the field.
 *
 * `status` is typed as `string` rather than the generated union deliberately.
 * The contract says new enum values may appear, so a caller holding a status
 * this build has never heard of is a case that has to be expressible; an
 * `InvocationChange` is still assignable.
 */
export function isTurnOver(change: {
  status: string;
  terminal?: boolean;
}): boolean {
  return change.terminal === true || isTerminalStatus(change.status);
}
