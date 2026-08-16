import { readFile } from "node:fs/promises";
import { extname } from "node:path";

import { Client, documentBlock, imageBlock, textBlock } from "../index.js";

const imageMediaTypes: Record<string, "image/gif" | "image/jpeg" | "image/png"> = {
  ".gif": "image/gif",
  ".jpg": "image/jpeg",
  ".jpeg": "image/jpeg",
  ".png": "image/png",
};

const filePath = process.env.NVOKEN_MEDIA_PATH ?? "chart.png";
const encoded = (await readFile(filePath)).toString("base64");
const imageMediaType = imageMediaTypes[extname(filePath).toLowerCase()];

// A document keeps its filename for providers that require one; an image does
// not carry a title at all.
const mediaBlock = imageMediaType === undefined
  ? documentBlock("application/pdf", encoded, filePath)
  : imageBlock(imageMediaType, encoded);

const client = new Client({
  baseUrl: process.env.NVOKEN_BASE_URL ?? "http://localhost:8080",
  apiKey: process.env.NVOKEN_API_KEY ?? "",
});

const agent = client.agent({
  agentKey: "invoice-review",
});

// Bytes are inlined, so nvoken never fetches a URL. Public message reads later
// return a digest reference rather than these bytes; keep your own copy.
console.log(
  "agent>",
  await agent.text([textBlock("Which line item changed?"), mediaBlock]),
);
