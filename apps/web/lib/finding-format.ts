export type FindingInline = { kind: "text" | "strong" | "code"; value: string };
export type FindingBlock =
  | { kind: "paragraph"; inline: FindingInline[] }
  | { kind: "heading"; level: 3 | 4; inline: FindingInline[] }
  | { kind: "list"; ordered: boolean; items: FindingInline[][] }
  | { kind: "code"; value: string };

function parseInline(text: string): FindingInline[] {
  const result: FindingInline[] = [];
  const pattern = /(\*\*[^*\n]+\*\*|`[^`\n]+`)/g;
  let cursor = 0;
  for (const match of text.matchAll(pattern)) {
    const index = match.index ?? cursor;
    if (index > cursor) result.push({ kind: "text", value: text.slice(cursor, index) });
    const token = match[0];
    result.push(token.startsWith("**")
      ? { kind: "strong", value: token.slice(2, -2) }
      : { kind: "code", value: token.slice(1, -1) });
    cursor = index + token.length;
  }
  if (cursor < text.length) result.push({ kind: "text", value: text.slice(cursor) });
  return result;
}

// A deliberately small, non-HTML formatter for model-supplied evidence. It
// never turns arbitrary model links into navigable URLs; verified provider
// links come from ProviderFileAnchor and ProviderReviewAnchor instead.
export function parseFindingNarrative(input: string): FindingBlock[] {
  const blocks: FindingBlock[] = [];
  for (const part of input.replace(/\r\n?/g, "\n").trim().split(/(```[\s\S]*?```)/g)) {
    if (!part.trim()) continue;
    if (part.startsWith("```") && part.endsWith("```")) {
      const content = part.slice(3, -3).replace(/^[^\n]*\n/, "").trimEnd();
      blocks.push({ kind: "code", value: content });
      continue;
    }
    for (const paragraph of part.split(/\n\s*\n/)) {
      const lines = paragraph.trim().split("\n");
      let prose: string[] = [];
      let list: { ordered: boolean; items: FindingInline[][] } | undefined;
      const flushProse = () => {
        if (prose.length) blocks.push({ kind: "paragraph", inline: parseInline(prose.join("\n")) });
        prose = [];
      };
      const flushList = () => {
        if (list) blocks.push({ kind: "list", ...list });
        list = undefined;
      };
      for (const line of lines) {
        const heading = /^\s{0,3}(#{1,4})\s+(.+?)\s*#*\s*$/.exec(line);
        if (heading) {
          flushProse();
          flushList();
          blocks.push({ kind: "heading", level: heading[1].length <= 2 ? 3 : 4, inline: parseInline(heading[2]) });
          continue;
        }
        const bullet = /^\s{0,3}[-*+]\s+(.+)$/.exec(line);
        const numbered = /^\s{0,3}\d+[.)]\s+(.+)$/.exec(line);
        if (bullet || numbered) {
          flushProse();
          const ordered = Boolean(numbered);
          if (list && list.ordered !== ordered) flushList();
          list ??= { ordered, items: [] };
          list.items.push(parseInline((bullet ?? numbered)![1].trim()));
          continue;
        }
        flushList();
        if (line.trim()) prose.push(line.trim());
      }
      flushProse();
      flushList();
    }
  }
  return blocks;
}

// Suggestions often contain an unfenced patch. Treat those as literal code so
// shell/Docker comments beginning with "#" are not mistaken for headings and
// the original indentation remains available to copy.
export function parseFindingSuggestion(input: string): FindingBlock[] {
  const normalized = input.replace(/\r\n?/g, "\n").trim();
  const hasCodeLine = normalized.split("\n").some((line) =>
    /^\s*(?:import\b|export\b|const\b|let\b|var\b|function\b|class\b|return\b|func\b|package\b|type\b|if\s*\(|HEALTHCHECK\b|FROM\b|RUN\b|COPY\b|ENV\b|SELECT\b|UPDATE\b|INSERT\b|ALTER\b|[{}])/.test(line),
  );
  if (normalized.includes("\n") && !normalized.includes("```") && hasCodeLine) {
    return [{ kind: "code", value: normalized }];
  }
  return parseFindingNarrative(input);
}

// The Explorer is a compact preview, not a second Markdown surface. A stored
// 240-character prefix can end midway through a marker, so remove lightweight
// formatting delimiters rather than showing broken ** or ` tokens to users.
export function findingPreviewText(input: string): string {
  return input.replace(/\r\n?/g, "\n")
    .replace(/```[^\n]*\n?/g, "")
    .replace(/(^|\n)\s{0,3}(?:#{1,4}\s+|[-*+]\s+|\d+[.)]\s+)/g, "$1")
    .replace(/\*\*|`/g, "")
    .replace(/\s+/g, " ")
    .trim();
}
