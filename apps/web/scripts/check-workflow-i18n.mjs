import { readFileSync, readdirSync } from "node:fs";
import ts from "typescript";

const appRoot = new URL("../", import.meta.url);
const read = relative => readFileSync(new URL(relative, appRoot), "utf8");
const catalog = ts.createSourceFile("workflow-copy.ts", read("lib/workflow-copy.ts"), ts.ScriptTarget.Latest, true);
const messages = new Map();
const failures = [];
const map = catalog.statements.find(node => ts.isVariableStatement(node) && node.declarationList.declarations[0].name.getText(catalog) === "workflowCopy").declarationList.declarations[0].initializer.expression;
const placeholders = source => [...source.matchAll(/\{([A-Za-z][A-Za-z0-9_]*)\}/g)].map(match => match[1]).sort().join(",");
for (const property of map.properties) {
  const key = property.name.text;
  const translation = property.initializer.text;
  if (messages.has(key)) failures.push(`Duplicate key: ${key}`);
  if (key !== key.trim() || !translation?.trim()) failures.push(`Invalid translation: ${key}`);
  if (placeholders(key) !== placeholders(translation)) failures.push(`Changed placeholders: ${key}`);
  messages.set(key, translation);
}

// These strings are executable syntax, provider brands, example inputs or
// technical identifiers. They must stay intact when interface language changes.
const technical = new Set([
  "@openreview implement", "@openreview revise …", "@openreview approve", "CI / tests, CI / build", "Codex", "Claude CLI", "openreview:implement", "UTC", "GitHub", "GitLab", "https://gitlab.example.com/api/v4", "RainLib/open-review-platform", "main", "services/**", "**/generated/**", "security.no-secrets", "security.credentials", "main or release/*", "https://tracker.example/SEC-123", "sha", "Issue",
]);
const components = ["finding-feedback-dashboard", "agent-work-manager", "approval-policy-manager", "copy-evidence-button", "enterprise-settings-tabs", "policy-page-header", "provider-issue-agent-task", "provider-issue-retry", "rule-approval-manager", "rule-binding-manager", "rule-catalog-browser", "rule-exception-manager", "rule-rollout-manager", "rule-set-composer", "rule-set-governance-actions", "rule-test-lab"];
const ruleRoot = "app/(console)/[org]/rules";
const files = [
  ...components.map(name => `components/console/${name}.tsx`),
  `${ruleRoot}/page.tsx`,
  ...readdirSync(new URL(ruleRoot, appRoot), { withFileTypes: true }).filter(item => item.isDirectory()).map(item => `${ruleRoot}/${item.name}/page.tsx`),
  "app/(console)/[org]/agent-work/page.tsx", "app/(console)/[org]/settings/approvals/page.tsx", "app/(console)/[org]/provider-issues/page.tsx",
];
const humanAttributes = new Set(["label", "title", "aria-label", "placeholder", "description", "eyebrow", "emptyTitle", "emptyDetail"]);
const decode = source => source.replaceAll("&apos;", "'").replaceAll("&quot;", '"').replaceAll("&amp;", "&").replaceAll("&gt;", ">").replaceAll("&lt;", "<");
for (const file of files) {
  const ast = ts.createSourceFile(file, read(file), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  function fail(node, message) {
    const line = ast.getLineAndCharacterOfPosition(node.getStart(ast)).line + 1;
    failures.push(`${file}:${line}: ${message}`);
  }
  function protectedText(node) {
    for (let parent = node.parent; parent; parent = parent.parent) {
      if (ts.isJsxElement(parent) && ["code", "pre"].includes(parent.openingElement.tagName.getText(ast))) return true;
    }
    return false;
  }
  function visit(node) {
    if (ts.isCallExpression(node) && node.expression.getText(ast) === "t" && ts.isStringLiteral(node.arguments[0])) {
      const key = node.arguments[0].text.trim();
      if (!messages.has(key)) fail(node, `Unregistered translation: ${key}`);
      for (let parent = node.parent; parent && !ts.isStatement(parent); parent = parent.parent) {
        if (ts.isBinaryExpression(parent) && [ts.SyntaxKind.EqualsEqualsToken, ts.SyntaxKind.EqualsEqualsEqualsToken, ts.SyntaxKind.ExclamationEqualsToken, ts.SyntaxKind.ExclamationEqualsEqualsToken].includes(parent.operatorToken.kind)) fail(node, "Translation must not change a machine-state comparison");
      }
      return;
    }
    if (!protectedText(node)) {
      const value = ts.isJsxText(node) ? decode(node.text.replace(/\s+/g, " ").trim()) : ts.isStringLiteral(node) && ts.isJsxAttribute(node.parent) && humanAttributes.has(node.parent.name.getText(ast)) ? node.text.trim() : undefined;
      if (value && /[A-Za-z]{2}/.test(value) && !technical.has(value)) fail(node, `Unlocalized interface copy: ${value}`);
    }
    ts.forEachChild(node, visit);
  }
  visit(ast);
}
if (failures.length) {
  console.error(failures.join("\n"));
  process.exitCode = 1;
} else {
  console.log(`Workflow i18n: ${messages.size} messages, ${files.length} source files; no missing static copy, invalid placeholders or translated state comparisons.`);
}
