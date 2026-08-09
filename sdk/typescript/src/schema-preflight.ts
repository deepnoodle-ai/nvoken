export const MAX_OUTPUT_SCHEMA_BYTES = 32 * 1024;
export const MAX_OUTPUT_SCHEMA_DEPTH = 16;
export const MAX_OUTPUT_PATTERN_BYTES = 1024;
export const SCHEMA_PREFLIGHT_CODE = "schema_preflight_failed";

export type SchemaIssueCode =
  | "unsupported_keyword"
  | "invalid_schema"
  | "limit_exceeded";

export interface SchemaIssue {
  code: SchemaIssueCode;
  path: string;
  keyword?: string;
  message: string;
}

const allowedKeywords = new Set([
  "type",
  "title",
  "description",
  "properties",
  "required",
  "additionalProperties",
  "items",
  "enum",
  "pattern",
  "minLength",
  "maxLength",
  "minItems",
  "maxItems",
  "uniqueItems",
  "minimum",
  "maximum",
]);

export function outputSchemaIssue(schema: Record<string, unknown>): SchemaIssue | undefined {
  let compact: string | undefined;
  try {
    compact = JSON.stringify(schema);
  } catch {
    return issue("invalid_schema", "", undefined, "schema must be JSON serializable");
  }
  if (compact === undefined) {
    return issue("invalid_schema", "", undefined, "schema must be JSON serializable");
  }
  if (new TextEncoder().encode(compact).byteLength > MAX_OUTPUT_SCHEMA_BYTES) {
    return issue(
      "limit_exceeded",
      "",
      undefined,
      `compact schema exceeds ${MAX_OUTPUT_SCHEMA_BYTES} bytes`,
    );
  }
  return validateSchemaNode(schema, "", 1, true);
}

function validateSchemaNode(
  node: Record<string, unknown>,
  path: string,
  depth: number,
  root: boolean,
): SchemaIssue | undefined {
  if (depth > MAX_OUTPUT_SCHEMA_DEPTH) {
    return issue(
      "limit_exceeded",
      path,
      undefined,
      `schema exceeds the maximum nesting depth of ${MAX_OUTPUT_SCHEMA_DEPTH}`,
    );
  }
  for (const keyword of Object.keys(node).sort()) {
    if (!allowedKeywords.has(keyword)) {
      return memberIssue(
        "unsupported_keyword",
        path,
        keyword,
        `unsupported schema keyword ${JSON.stringify(keyword)}`,
      );
    }
  }
  const typeName = node.type;
  if (typeof typeName !== "string" || !supportedType(typeName)) {
    return memberIssue(
      "invalid_schema",
      path,
      "type",
      "every schema position requires one supported string type",
    );
  }
  if (root && typeName !== "object") {
    return memberIssue("invalid_schema", path, "type", "schema root type must be object");
  }
  for (const keyword of ["description", "title"] as const) {
    if (keyword in node && typeof node[keyword] !== "string") {
      return memberIssue(
        "invalid_schema",
        path,
        keyword,
        `schema ${keyword} must be a string`,
      );
    }
  }
  for (
    const keyword of ["maxItems", "maxLength", "minItems", "minLength"] as const
  ) {
    if (keyword in node && !nonnegativeInteger(node[keyword])) {
      return memberIssue(
        "invalid_schema",
        path,
        keyword,
        `schema ${keyword} must be a nonnegative integer`,
      );
    }
  }
  for (const keyword of ["maximum", "minimum"] as const) {
    if (keyword in node && !jsonNumber(node[keyword])) {
      return memberIssue(
        "invalid_schema",
        path,
        keyword,
        `schema ${keyword} must be a number`,
      );
    }
  }
  if ("uniqueItems" in node && typeof node.uniqueItems !== "boolean") {
    return memberIssue(
      "invalid_schema",
      path,
      "uniqueItems",
      "schema uniqueItems must be a boolean",
    );
  }
  const boundIssue = validateBoundOrder(node, path);
  if (boundIssue) return boundIssue;
  if (typeName !== "string") {
    const keyword = firstPresent(node, "maxLength", "minLength", "pattern");
    if (keyword) {
      return memberIssue(
        "invalid_schema",
        path,
        keyword,
        "string schema keywords require type string",
      );
    }
  }
  if (typeName !== "array") {
    const keyword = firstPresent(node, "maxItems", "minItems", "uniqueItems");
    if (keyword) {
      return memberIssue(
        "invalid_schema",
        path,
        keyword,
        "array schema bounds require type array",
      );
    }
  }
  if (typeName !== "number" && typeName !== "integer") {
    const keyword = firstPresent(node, "maximum", "minimum");
    if (keyword) {
      return memberIssue(
        "invalid_schema",
        path,
        keyword,
        "numeric schema keywords require type number or integer",
      );
    }
  }
  if (root && "enum" in node) {
    return memberIssue(
      "invalid_schema",
      path,
      "enum",
      "schema root enum is not supported",
    );
  }
  if ("pattern" in node) {
    if (typeof node.pattern !== "string") {
      return memberIssue(
        "invalid_schema",
        path,
        "pattern",
        "schema pattern must be a string",
      );
    }
    if (new TextEncoder().encode(node.pattern).byteLength > MAX_OUTPUT_PATTERN_BYTES) {
      return memberIssue(
        "limit_exceeded",
        path,
        "pattern",
        `schema pattern exceeds the maximum size of ${MAX_OUTPUT_PATTERN_BYTES} bytes`,
      );
    }
  }
  if ("enum" in node && (!Array.isArray(node.enum) || node.enum.length === 0)) {
    return memberIssue(
      "invalid_schema",
      path,
      "enum",
      "schema enum must be a nonempty array",
    );
  }
  const hasProperties = "properties" in node;
  const hasRequired = "required" in node;
  const hasAdditional = "additionalProperties" in node;
  if ((hasProperties || hasRequired || hasAdditional) && typeName !== "object") {
    const keyword = firstPresent(
      node,
      "additionalProperties",
      "properties",
      "required",
    )!;
    return memberIssue(
      "invalid_schema",
      path,
      keyword,
      "object schema keywords require type object",
    );
  }
  const propertyNames = new Set<string>();
  if (hasProperties) {
    if (!isObject(node.properties)) {
      return memberIssue(
        "invalid_schema",
        path,
        "properties",
        "schema properties must be an object",
      );
    }
    for (const name of Object.keys(node.properties).sort()) {
      const child = node.properties[name];
      const childPath = pointer(pointer(path, "properties"), name);
      if (!isObject(child) || Object.keys(child).length === 0) {
        return issue(
          "invalid_schema",
          childPath,
          undefined,
          `property ${JSON.stringify(name)} must contain a schema object`,
        );
      }
      propertyNames.add(name);
      const childIssue = validateSchemaNode(child, childPath, depth + 1, false);
      if (childIssue) return childIssue;
    }
  }
  if (hasRequired) {
    if (!Array.isArray(node.required)) {
      return memberIssue(
        "invalid_schema",
        path,
        "required",
        "schema required must be an array of property names",
      );
    }
    const seen = new Set<string>();
    for (const [index, value] of node.required.entries()) {
      const itemPath = pointer(pointer(path, "required"), String(index));
      if (typeof value !== "string" || value.length === 0) {
        return issue(
          "invalid_schema",
          itemPath,
          "required",
          "schema required must contain nonempty strings",
        );
      }
      if (seen.has(value)) {
        return issue(
          "invalid_schema",
          itemPath,
          "required",
          "schema required must not contain duplicates",
        );
      }
      if (!propertyNames.has(value)) {
        return issue(
          "invalid_schema",
          itemPath,
          "required",
          `required property ${JSON.stringify(value)} is not declared`,
        );
      }
      seen.add(value);
    }
  }
  if (hasAdditional) {
    const additional = node.additionalProperties;
    if (typeof additional !== "boolean") {
      const childPath = pointer(path, "additionalProperties");
      if (!isObject(additional)) {
        return memberIssue(
          "invalid_schema",
          path,
          "additionalProperties",
          "additionalProperties must be a boolean or schema object",
        );
      }
      if (Object.keys(additional).length === 0) {
        return issue(
          "invalid_schema",
          childPath,
          "additionalProperties",
          "additionalProperties must contain a schema object",
        );
      }
      const childIssue = validateSchemaNode(additional, childPath, depth + 1, false);
      if (childIssue) return childIssue;
    }
  }
  const hasItems = "items" in node;
  if (typeName === "array" && !hasItems) {
    return memberIssue("invalid_schema", path, "items", "array schemas require items");
  }
  if (hasItems) {
    if (typeName !== "array") {
      return memberIssue(
        "invalid_schema",
        path,
        "items",
        "schema items requires type array",
      );
    }
    if (!isObject(node.items) || Object.keys(node.items).length === 0) {
      return memberIssue(
        "invalid_schema",
        path,
        "items",
        "schema items must be a schema object",
      );
    }
    const childIssue = validateSchemaNode(
      node.items,
      pointer(path, "items"),
      depth + 1,
      false,
    );
    if (childIssue) return childIssue;
  }
  return undefined;
}

function validateBoundOrder(
  node: Record<string, unknown>,
  path: string,
): SchemaIssue | undefined {
  if (compareNumbers(node.minLength, node.maxLength) > 0) {
    return memberIssue(
      "invalid_schema",
      path,
      "maxLength",
      "schema minLength must not exceed maxLength",
    );
  }
  if (compareNumbers(node.minItems, node.maxItems) > 0) {
    return memberIssue(
      "invalid_schema",
      path,
      "maxItems",
      "schema minItems must not exceed maxItems",
    );
  }
  if (compareNumbers(node.minimum, node.maximum) > 0) {
    return memberIssue(
      "invalid_schema",
      path,
      "maximum",
      "schema minimum must not exceed maximum",
    );
  }
  return undefined;
}

function compareNumbers(left: unknown, right: unknown): number {
  if (!jsonNumber(left) || !jsonNumber(right)) return 0;
  return left - right;
}

function jsonNumber(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

function nonnegativeInteger(value: unknown): boolean {
  return jsonNumber(value) && Number.isInteger(value) && value >= 0;
}

function supportedType(value: string): boolean {
  return ["object", "array", "string", "number", "integer", "boolean"].includes(value);
}

function firstPresent(
  node: Record<string, unknown>,
  ...keywords: string[]
): string | undefined {
  return keywords.find((keyword) => keyword in node);
}

function isObject(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function memberIssue(
  code: SchemaIssueCode,
  path: string,
  keyword: string,
  message: string,
): SchemaIssue {
  return issue(code, pointer(path, keyword), keyword, message);
}

function issue(
  code: SchemaIssueCode,
  path: string,
  keyword: string | undefined,
  message: string,
): SchemaIssue {
  return { code, path, keyword, message };
}

function pointer(path: string, member: string): string {
  return `${path}/${member.replaceAll("~", "~0").replaceAll("/", "~1")}`;
}
