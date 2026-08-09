export const MAX_MEDIA_INPUT_BLOCKS = 8;
export const MAX_IMAGE_INPUT_BYTES = 5 * 1024 * 1024;
export const MAX_DOCUMENT_INPUT_BYTES = 16 * 1024 * 1024;
export const MAX_MEDIA_INPUT_BYTES = 16 * 1024 * 1024;
export const MAX_MEDIA_TITLE_CHARACTERS = 255;
export const MEDIA_PREFLIGHT_CODE = "media_preflight_failed";

export const IMAGE_MEDIA_TYPES = [
  "image/gif",
  "image/jpeg",
  "image/png",
  "image/webp",
] as const;
export const DOCUMENT_MEDIA_TYPES = ["application/pdf"] as const;

export type MediaIssueCode = "invalid_media" | "unsupported_media_type" | "limit_exceeded";

export interface MediaIssue {
  code: MediaIssueCode;
  path: string;
  message: string;
}

interface RawBlock {
  type?: unknown;
  text?: unknown;
  source?: unknown;
  title?: unknown;
}

const base64Pattern = /^[A-Za-z0-9+/]+={0,2}$/;

/**
 * mediaInputIssue reproduces the checks the Runtime can make without decoding
 * bytes. Format sniffing, pixel bounds, and per-model modality support are
 * Runtime-only and stay server-side.
 */
export function mediaInputIssue(content: readonly unknown[]): MediaIssue | undefined {
  let mediaBlocks = 0;
  let mediaBytes = 0;
  for (let index = 0; index < content.length; index += 1) {
    const path = `input.content[${index}]`;
    const block = content[index] as RawBlock | null;
    if (block === null || typeof block !== "object") {
      return issue("invalid_media", path, "block must be an object");
    }
    if (block.type === "text") {
      if (typeof block.text !== "string" || block.text.trim().length === 0) {
        return issue("invalid_media", `${path}.text`, "text must not be blank");
      }
      if (block.source !== undefined || block.title !== undefined) {
        return issue("invalid_media", path, "text blocks accept only text");
      }
      continue;
    }
    if (block.type !== "image" && block.type !== "document") {
      return issue("invalid_media", `${path}.type`, "type must be text, image, or document");
    }
    if (block.text !== undefined) {
      return issue("invalid_media", path, "media blocks must not carry text");
    }
    if (block.type === "image" && block.title !== undefined) {
      return issue("invalid_media", `${path}.title`, "title is allowed only for document blocks");
    }
    if (block.title !== undefined) {
      if (
        typeof block.title !== "string"
        || block.title.trim().length === 0
        || [...block.title].length > MAX_MEDIA_TITLE_CHARACTERS
      ) {
        return issue(
          "invalid_media",
          `${path}.title`,
          `title must be 1 to ${MAX_MEDIA_TITLE_CHARACTERS} characters`,
        );
      }
    }
    const source = block.source as {
      mediaType?: unknown;
      data?: unknown;
      url?: unknown;
    } | undefined | null;
    if (source === undefined || source === null || typeof source !== "object") {
      return issue("invalid_media", `${path}.source`, "source is required");
    }
    const hasData = source.data !== undefined;
    const hasURL = source.url !== undefined;
    if (hasData === hasURL) {
      return issue(
        "invalid_media",
        `${path}.source`,
        "source requires exactly one of data or url",
      );
    }
    const allowed: readonly string[] = block.type === "image"
      ? IMAGE_MEDIA_TYPES
      : DOCUMENT_MEDIA_TYPES;
    if (hasURL) {
      if (!validPublicHTTPSURL(source.url)) {
        return issue(
          "invalid_media",
          `${path}.source.url`,
          "url must be an HTTPS URL",
        );
      }
      if (source.mediaType !== undefined
        && (typeof source.mediaType !== "string" || !allowed.includes(source.mediaType))) {
        return issue(
          "unsupported_media_type",
          `${path}.source.media_type`,
          `media_type must be one of ${allowed.join(", ")}`,
        );
      }
      mediaBlocks += 1;
      if (mediaBlocks > MAX_MEDIA_INPUT_BLOCKS) {
        return issue(
          "limit_exceeded",
          path,
          `input carries at most ${MAX_MEDIA_INPUT_BLOCKS} media blocks`,
        );
      }
      continue;
    }
    if (typeof source.mediaType !== "string" || !allowed.includes(source.mediaType)) {
      return issue(
        "unsupported_media_type",
        `${path}.source.media_type`,
        `media_type must be one of ${allowed.join(", ")}`,
      );
    }
    if (typeof source.data !== "string" || !base64Pattern.test(source.data)
      || source.data.length % 4 !== 0) {
      return issue(
        "invalid_media",
        `${path}.source.data`,
        "data must be standard padded base64 without whitespace",
      );
    }
    const decoded = decodedByteLength(source.data);
    if (decoded === 0) {
      return issue(
        "invalid_media",
        `${path}.source.data`,
        "data must be standard padded base64 without whitespace",
      );
    }
    const limit = block.type === "image" ? MAX_IMAGE_INPUT_BYTES : MAX_DOCUMENT_INPUT_BYTES;
    if (decoded > limit) {
      return issue(
        "limit_exceeded",
        `${path}.source.data`,
        `data must decode to at most ${limit} bytes`,
      );
    }
    mediaBlocks += 1;
    mediaBytes += decoded;
    if (mediaBlocks > MAX_MEDIA_INPUT_BLOCKS) {
      return issue(
        "limit_exceeded",
        path,
        `input carries at most ${MAX_MEDIA_INPUT_BLOCKS} media blocks`,
      );
    }
    if (mediaBytes > MAX_MEDIA_INPUT_BYTES) {
      return issue(
        "limit_exceeded",
        path,
        `input media must decode to at most ${MAX_MEDIA_INPUT_BYTES} bytes`,
      );
    }
  }
  return undefined;
}

function validPublicHTTPSURL(value: unknown): boolean {
  if (typeof value !== "string") return false;
  try {
    const parsed = new URL(value);
    return parsed.protocol === "https:" && parsed.hostname.length > 0;
  } catch {
    return false;
  }
}

function decodedByteLength(data: string): number {
  const padding = data.endsWith("==") ? 2 : data.endsWith("=") ? 1 : 0;
  return (data.length / 4) * 3 - padding;
}

function issue(code: MediaIssueCode, path: string, message: string): MediaIssue {
  return { code, path, message };
}
