import assert from "node:assert/strict";
import { test } from "node:test";
import { findingPreviewText, parseFindingNarrative, parseFindingSuggestion } from "./finding-format.ts";

test("formats emphasis, inline code and fenced code without interpreting model HTML", () => {
  const source = "**Risk:** `get_user` runs. <script>alert(1)</script>\n\n```go\nfmt.Println(\"safe text\")\n```";
  assert.deepEqual(parseFindingNarrative(source), [
    { kind: "paragraph", inline: [
      { kind: "strong", value: "Risk:" },
      { kind: "text", value: " " },
      { kind: "code", value: "get_user" },
      { kind: "text", value: " runs. <script>alert(1)</script>" },
    ] },
    { kind: "code", value: "fmt.Println(\"safe text\")" },
  ]);
});

test("retains unclosed markers and model links as text rather than active links", () => {
  assert.deepEqual(parseFindingNarrative("**unfinished [file](https://example.invalid)"), [
    { kind: "paragraph", inline: [{ kind: "text", value: "**unfinished [file](https://example.invalid)" }] },
  ]);
});

test("renders a bounded heading and ordered/unordered evidence hierarchy without activating links", () => {
  assert.deepEqual(parseFindingNarrative("# Risk\n- **Impact:** `auth.go`\n- [source](https://example.invalid)\n\n1. Validate input\n2. Add a test"), [
    { kind: "heading", level: 3, inline: [{ kind: "text", value: "Risk" }] },
    { kind: "list", ordered: false, items: [
      [{ kind: "strong", value: "Impact:" }, { kind: "text", value: " " }, { kind: "code", value: "auth.go" }],
      [{ kind: "text", value: "[source](https://example.invalid)" }],
    ] },
    { kind: "list", ordered: true, items: [
      [{ kind: "text", value: "Validate input" }],
      [{ kind: "text", value: "Add a test" }],
    ] },
  ]);
});

test("compact previews remove broken Markdown delimiters from a truncated finding", () => {
  assert.equal(findingPreviewText("## Risk\n**Critical** `handler.go` may fail. **Unfinished"), "Risk Critical handler.go may fail. Unfinished");
  assert.equal(findingPreviewText("```go\nreturn err\n```"), "return err");
});

test("unfenced code suggestions preserve hash comments instead of creating headings", () => {
  const source = "# Add next.config.ts\n# apps/web/next.config.ts\nimport type { NextConfig } from 'next'\nconst nextConfig = { output: 'standalone' }";
  assert.deepEqual(parseFindingSuggestion(source), [{ kind: "code", value: source }]);
  assert.deepEqual(parseFindingSuggestion("## Recommended approach\n- Add a test\n- Verify the result"), [
    { kind: "heading", level: 3, inline: [{ kind: "text", value: "Recommended approach" }] },
    { kind: "list", ordered: false, items: [
      [{ kind: "text", value: "Add a test" }],
      [{ kind: "text", value: "Verify the result" }],
    ] },
  ]);
});
