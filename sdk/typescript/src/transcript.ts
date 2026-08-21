// Imported from the generated modules rather than the barrel deliberately,
// for the reason `invocation-status.ts` gives: this subpath has to stay
// reachable without pulling the runtime client in. `StreamPreview` comes from
// `stream.js`, which does import the client — but only as a type, so the
// import is erased and nothing of the client survives into the bundle.
import type { SessionCompaction } from "./generated/models/SessionCompaction.js";
import type { SessionMessage } from "./generated/models/SessionMessage.js";
import type { StreamPreview } from "./stream.js";

/**
 * What a transcript should draw, derived from a reduced stream snapshot.
 *
 * The Reducer stops one step short of what a consumer actually renders. It
 * gives you the durable messages, the compaction passes, and the live deltas,
 * each correct on its own — but a tool result arrives in a different message
 * from the call it answers, a compaction is a boundary rather than a message,
 * and one turn's deltas arrive as several content indices. Every consumer that
 * draws a conversation closes that gap, and before this module they each closed
 * it themselves.
 *
 * Deliberately free of React, of any UI library, and of the runtime client:
 * these are shaping rules over protocol values, so they belong next to the
 * protocol rather than next to whichever renderer happened to need them first.
 */

/**
 * Content blocks travel as open provider-neutral objects.
 *
 * Their spelling is not uniform, and cannot be. The SDK rewrites the blocks it
 * models (`tool_use_id` → `toolUseId`) and passes the rest through in the
 * runtime's own snake_case, and a caller reading the HTTP API directly holds
 * every block in wire spelling. So a field is read through {@link blockField},
 * which accepts either, rather than by guessing which side of that line a given
 * block falls on.
 */
export type Block = { type?: string; [key: string]: unknown };

/**
 * A block's value under its camelCase name or its wire name, whichever is
 * present.
 *
 * Both branches are load-bearing, which is not obvious and has been doubted.
 * A snapshot from this SDK's Reducer carries `toolUseId`; the same transcript
 * read straight from the HTTP API carries `tool_use_id`, because nothing has
 * modeled it on the way past. Dropping the wire branch silently strands every
 * tool result on the second path — the call spins forever and its answer
 * renders as an orphan row.
 */
export function blockField(block: Block, camel: string, wire: string): unknown {
  return block[camel] ?? block[wire];
}

/**
 * A tool call and the result that answered it, once folded together.
 *
 * Named for the folding rather than for the protocol: `ToolCall` at the package
 * root is the API's own resource, and this is a render-time pairing of two
 * content blocks. They are different things and should not share a name.
 */
export type RenderedToolCall = {
  block: Block;
  result?: Block;
};

/** True once the call has a result — the only thing that ends its spinner. */
export function isSettled(call: RenderedToolCall): boolean {
  return call.result !== undefined;
}

export function isErrorResult(call: RenderedToolCall): boolean {
  return call.result !== undefined && blockField(call.result, "isError", "is_error") === true;
}

/**
 * An image or document the runtime stored for a turn. The transcript returns a
 * reference, never the bytes: media is addressed by digest and size, so there
 * is nothing to render a thumbnail from and nothing to re-send from history.
 */
export type MediaReference = {
  kind: "image" | "document";
  mediaType?: string;
  title?: string;
  bytes?: number;
  digest?: string;
};

export function mediaReference(block: Block): MediaReference | null {
  if (block.type !== "image" && block.type !== "document") return null;
  const mediaType = blockField(block, "mediaType", "media_type");
  const bytes = block.bytes;
  return {
    kind: block.type,
    mediaType: typeof mediaType === "string" ? mediaType : undefined,
    title: typeof block.title === "string" ? block.title : undefined,
    bytes: typeof bytes === "number" ? bytes : undefined,
    digest: typeof block.digest === "string" ? block.digest : undefined,
  };
}

/**
 * Anything with content blocks on it.
 *
 * The fold reads nothing else, and callers hold the same messages in different
 * shapes: a stream snapshot has them modeled by this SDK, and a REST read has
 * them in wire spelling with string timestamps. Rather than adapt one into the
 * other to reuse one function, the function is stated in terms of the only
 * field it touches.
 */
export type HasContent = { content?: unknown };

/**
 * One visible block, still carrying its position in the original message.
 *
 * Folding removes `tool_result` blocks, so a block's position in `visible` is
 * not its position in the message. The original position matters because
 * `(messageId, contentIndex)` is the identity a durable block shares with the
 * live preview that streamed it ({@link PreviewBlock}`.index` carries the same
 * value), and a consumer keys its rows on that identity to make the
 * preview-to-durable handoff an update rather than a swap. Before this type,
 * consumers recovered the index by reference equality against
 * {@link blocksOf}, which only worked because the fold happened to reuse the
 * caller's block objects.
 */
export type RenderedBlock = {
  block: Block;
  contentIndex: number;
};

export type RenderedMessage<M = SessionMessage> = {
  message: M;
  visible: RenderedBlock[];
  toolCalls: Map<string, RenderedToolCall>;
};

export function blocksOf(message: HasContent): Block[] {
  return (message.content ?? []) as Block[];
}

/**
 * Fold each tool_result into its originating tool_use so results render on the
 * call's card instead of as orphan tool rows. A tool_result usually arrives in
 * a later message than its call, so the call index spans the whole input — and
 * it is built before any pairing, so a result finds its call even when the
 * caller's collection holds the result first (a newest-first page, or two
 * transcript windows merged out of order). A result with no call anywhere in
 * the input is a genuine orphan and stays visible. Messages left with nothing
 * visible are dropped, which is how a result-only message disappears once its
 * result is paired.
 */
export function foldMessages<M extends HasContent>(messages: M[]): RenderedMessage<M>[] {
  const calls = new Map<string, RenderedToolCall>();
  for (const message of messages) {
    for (const block of blocksOf(message)) {
      if (block.type === "tool_use" && typeof block.id === "string") {
        calls.set(block.id, { block });
      }
    }
  }
  const rendered: RenderedMessage<M>[] = [];
  for (const message of messages) {
    const visible: RenderedBlock[] = [];
    const ownCalls = new Map<string, RenderedToolCall>();
    for (const [contentIndex, block] of blocksOf(message).entries()) {
      if (block.type === "tool_use" && typeof block.id === "string") {
        ownCalls.set(block.id, calls.get(block.id)!);
        visible.push({ block, contentIndex });
        continue;
      }
      if (block.type === "tool_result") {
        const toolUseId = blockField(block, "toolUseId", "tool_use_id");
        const call = typeof toolUseId === "string" ? calls.get(toolUseId) : undefined;
        if (call) {
          // Results are folded in input order, so when malformed input carries
          // two results for one call, the last one wins — the same result the
          // one-pass fold produced.
          call.result = block;
          continue;
        }
      }
      visible.push({ block, contentIndex });
    }
    if (visible.length > 0) rendered.push({ message, visible, toolCalls: ownCalls });
  }
  return rendered;
}

export type TranscriptEntry =
  | { kind: "message"; key: string; rendered: RenderedMessage }
  | { kind: "compaction"; key: string; compaction: SessionCompaction };

export type TranscriptMessageEntry = Extract<TranscriptEntry, { kind: "message" }>;

/**
 * The transcript with its compaction passes in place.
 *
 * A compaction never edits the canonical transcript — it replaces the private
 * projection the model is given from that point back — so it belongs in the
 * reading order as a boundary, not as a message. `coversThrough` is the last
 * canonical sequence a pass folded away, so the marker goes before the first
 * message past it: the messages above are what the model now sees only as a
 * summary. A pass whose boundary lands on a message the fold dropped still
 * lands in the right gap, because the placement tests sequence, not position.
 */
export function buildTranscript(
  rendered: readonly RenderedMessage[],
  compactions: readonly SessionCompaction[],
): TranscriptEntry[] {
  const ordered = [...compactions].sort(
    (left, right) =>
      left.coversThrough - right.coversThrough ||
      left.createdAt.getTime() - right.createdAt.getTime(),
  );
  const entries: TranscriptEntry[] = [];
  let index = 0;
  const flush = (before?: number) => {
    while (
      index < ordered.length &&
      (before === undefined || ordered[index].coversThrough < before)
    ) {
      entries.push({ kind: "compaction", key: ordered[index].id, compaction: ordered[index] });
      index += 1;
    }
  };
  for (const item of rendered) {
    flush(item.message.sequence);
    entries.push({ kind: "message", key: item.message.id, rendered: item });
  }
  flush();
  return entries;
}

export type PreviewBlock = {
  index: number;
  kind: "thinking" | "text";
  text: string;
};

/** One in-flight assistant turn, shaped like the message it will become. */
export type PreviewRow = {
  key: string;
  invocationId: string;
  blocks: PreviewBlock[];
};

/**
 * Group the live deltas into one row per assistant turn.
 *
 * Deltas arrive per content index, and a turn that thinks before it answers
 * produces at least two of them. Rendering a row each would put two avatars
 * and two "Assistant" labels on screen that collapse into one the moment the
 * durable message lands — so they are grouped by `messageId`, which is the
 * identity the saved message will land under. Grouping on the destination is
 * what makes the handoff a row update rather than one row disappearing as
 * another takes its place.
 *
 * Only the kinds a transcript renders produce blocks. A preview of tool
 * arguments names a call whose result is not written yet, and showing a
 * half-typed argument object reads as noise, so it is counted and dropped.
 *
 * Previews for an Invocation that has already settled are dropped. The
 * Reducer clears them itself on the terminal change, but only for the statuses
 * its version knows; filtering here means a status it has not learned yet
 * cannot leave a ghost row streaming under a finished turn.
 */
export function groupPreviews(
  previews: StreamPreview[],
  settledInvocations: ReadonlySet<string> = new Set(),
): PreviewRow[] {
  const rows = new Map<string, PreviewRow>();
  for (const preview of previews) {
    if (settledInvocations.has(preview.invocationId)) continue;
    if (preview.kind !== "text" && preview.kind !== "thinking") continue;
    if (!preview.delta.trim()) continue;
    const key = preview.messageId;
    const row = rows.get(key) ?? {
      key,
      invocationId: preview.invocationId,
      blocks: [],
    };
    row.blocks.push({
      index: preview.contentIndex,
      kind: preview.kind,
      text: preview.delta,
    });
    rows.set(key, row);
  }
  for (const row of rows.values()) {
    row.blocks.sort((left, right) => left.index - right.index);
  }
  return [...rows.values()];
}
