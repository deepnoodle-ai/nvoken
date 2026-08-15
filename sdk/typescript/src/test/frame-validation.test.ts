import assert from "node:assert/strict";
import test from "node:test";
import { NvokenError } from "../client.js";
import { Reducer } from "../stream.js";

const CHANGE = {
  invocation_id: "invk_1",
  revision: 1,
  status: "completed",
  terminal: true,
  through_message_sequence: null,
  error: null,
  structured_output: null,
  occurred_at: "2026-08-15T00:00:00Z",
};

function frame(changes: object[]) {
  return {
    type: "transcript.update",
    id: "cursor-1",
    data: {
      type: "transcript.update",
      session_id: "sesn_1",
      messages: [],
      invocation_changes: changes,
      cursor: "cursor-1",
    },
  };
}

// The generated decoders copy what is there and leave the rest undefined, so a
// required field the server never sent becomes a plausible value rather than an
// error. For `terminal` that is a confident wrong answer: the turn reads as
// still running.
test("refuses a change missing a required field", () => {
  const { terminal: _dropped, ...incomplete } = CHANGE;
  const reducer = new Reducer();
  assert.throws(
    () => reducer.apply(frame([incomplete])),
    (error: unknown) =>
      error instanceof NvokenError && /invocation change/.test(error.message),
  );
});

test("accepts the same change once it is complete", () => {
  const reducer = new Reducer();
  reducer.apply(frame([CHANGE]));
  assert.equal(reducer.settled("invk_1"), true);
});

// The Session subscription folds through the Reducer and never calls the
// filtered stream's decoder, so validating only there would leave the busier
// path lenient.
test("refuses a frame missing a required field", () => {
  const reducer = new Reducer();
  const { cursor: _dropped, ...incomplete } = frame([CHANGE]).data;
  assert.throws(
    () => reducer.apply({ type: "transcript.update", data: incomplete }),
    (error: unknown) =>
      error instanceof NvokenError && /transcript\.update/.test(error.message),
  );
});

// Frames gain fields over time and a reader must keep going. Requiring what the
// contract requires is the other half of that rule, not a contradiction of it.
test("ignores fields it does not recognize", () => {
  const reducer = new Reducer();
  reducer.apply(frame([{ ...CHANGE, added_later: 1 }]));
  assert.equal(reducer.settled("invk_1"), true);
});
