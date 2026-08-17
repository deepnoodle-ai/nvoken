import assert from "node:assert/strict";
import test from "node:test";
import {
  InvocationStatus,
  isTerminalStatus,
  isTurnOver,
  TERMINAL_INVOCATION_STATUSES,
} from "../index.js";

// The classification is exhaustive over the contract's enum, so a status added
// later fails here until someone says which side it belongs on. That is the
// whole point of exporting the predicate: hosts stopped keeping their own copy,
// so this is now the copy that has to be right.
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

test("classifies every status the contract declares", () => {
  const declared = Object.values(InvocationStatus).sort();
  assert.deepEqual(declared, Object.keys(CLASSIFICATION).sort());
  for (const [status, terminal] of Object.entries(CLASSIFICATION)) {
    assert.equal(isTerminalStatus(status), terminal, status);
  }
  assert.deepEqual(
    [...TERMINAL_INVOCATION_STATUSES].sort(),
    Object.keys(CLASSIFICATION)
      .filter((status) => CLASSIFICATION[status])
      .sort(),
  );
});

test("treats a budget-held turn as unfinished", () => {
  // It stopped on spending capacity with its deadlines on hold. It still owns
  // the Session and resumes on its own once the account is funded, so a caller
  // that reads it as over abandons a turn that is still going.
  assert.equal(isTerminalStatus("budget_hold"), false);
  assert.equal(isTurnOver({ status: "budget_hold", terminal: false }), false);
});

test("reads an unrecognized status as unfinished", () => {
  assert.equal(isTerminalStatus("some_status_added_later"), false);
});

test("accepts either witness that the turn ended", () => {
  // Both are present and agreeing in normal operation. The status alone covers
  // a server too old to send the field; the field alone covers a status this
  // build has never heard of.
  assert.equal(isTurnOver({ status: "completed", terminal: true }), true);
  assert.equal(isTurnOver({ status: "completed" }), true);
  assert.equal(isTurnOver({ status: "something_new", terminal: true }), true);
  assert.equal(isTurnOver({ status: "running", terminal: false }), false);
});

test("answers for the change, not for the turn", () => {
  // A replayed `running` change from a turn that has since ended reports false,
  // which is what lets a reader fold messages before changes and never mark a
  // turn settled before its final message exists.
  assert.equal(isTurnOver({ status: "running", terminal: false }), false);
});
