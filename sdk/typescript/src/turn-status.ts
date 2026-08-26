import { TurnStatus } from "./generated/models/TurnStatus.js";

export { TurnStatus };

export const TERMINAL_TURN_STATUSES: readonly TurnStatus[] = [
  TurnStatus.Completed,
  TurnStatus.Incomplete,
  TurnStatus.Failed,
  TurnStatus.Cancelled,
] as const;

const TERMINAL = new Set<TurnStatus>(TERMINAL_TURN_STATUSES);

export const ACTIVE_TURN_STATUSES: readonly TurnStatus[] = Object.values(TurnStatus)
  .filter((status) => !TERMINAL.has(status));

export function isTerminalTurnStatus(status: TurnStatus): boolean {
  return TERMINAL.has(status);
}

export function isTurnOver(value: TurnStatus | { status: TurnStatus; terminal?: boolean }): boolean {
  return typeof value === "string"
    ? isTerminalTurnStatus(value)
    : value.terminal ?? isTerminalTurnStatus(value.status);
}
