import assert from "node:assert/strict";
import test from "node:test";

import {
  ACTIVE_TURN_STATUSES,
  isTerminalTurnStatus,
  isTurnOver,
  TERMINAL_TURN_STATUSES,
  TurnStatus,
} from "../turn-status.js";

const CLASSIFICATION: Record<string, boolean> = {
  queued: false,
  running: false,
  waiting: false,
  budget_hold: false,
  completed: true,
  incomplete: true,
  failed: true,
  cancelled: true,
};

test("every declared Turn status has one terminal classification", () => {
  assert.deepEqual(Object.values(TurnStatus).sort(), Object.keys(CLASSIFICATION).sort());
  for (const [status, terminal] of Object.entries(CLASSIFICATION)) {
    assert.equal(isTerminalTurnStatus(status as never), terminal, status);
  }
  assert.deepEqual(
    [...TERMINAL_TURN_STATUSES].sort(),
    Object.keys(CLASSIFICATION).filter((status) => CLASSIFICATION[status]).sort(),
  );
  assert.deepEqual(
    [...ACTIVE_TURN_STATUSES].sort(),
    Object.keys(CLASSIFICATION).filter((status) => !CLASSIFICATION[status]).sort(),
  );
});

test("budget hold remains active because the Turn can resume", () => {
  assert.equal(isTerminalTurnStatus("budget_hold"), false);
  assert.equal(isTurnOver({ status: "budget_hold", terminal: false }), false);
});

test("the change terminal field is authoritative for future statuses", () => {
  assert.equal(isTurnOver({ status: "completed", terminal: true }), true);
  assert.equal(isTurnOver({ status: "completed" }), true);
  assert.equal(isTurnOver({ status: "future_terminal" as never, terminal: true }), true);
  assert.equal(isTurnOver({ status: "running", terminal: false }), false);
});
