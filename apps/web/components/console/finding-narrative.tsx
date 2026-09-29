import { parseFindingNarrative, parseFindingSuggestion, type FindingInline } from "@/lib/finding-format";
import { cn } from "@/lib/utils";

export function FindingNarrative({ text, className, variant = "finding" }: { text: string; className?: string; variant?: "finding" | "suggestion" }) {
  const inline = (parts: FindingInline[]) => parts.map((part, partIndex) => part.kind === "strong"
    ? <strong className="font-semibold text-[var(--ls-text)]" key={partIndex}>{part.value}</strong>
    : part.kind === "code"
      ? <code className="rounded bg-[var(--ls-surface-muted)] px-1 py-0.5 font-mono text-[0.9em] text-[var(--ls-text)]" key={partIndex}>{part.value}</code>
      : <span key={partIndex}>{part.value}</span>);
  return (
    <div className={cn("space-y-3 break-words text-sm leading-6", className)}>
      {(variant === "suggestion" ? parseFindingSuggestion(text) : parseFindingNarrative(text)).map((block, index) => {
        if (block.kind === "code") return <pre className="overflow-x-auto rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3 font-mono text-xs leading-5" key={index}><code>{block.value}</code></pre>;
        if (block.kind === "heading") return block.level === 3
          ? <h3 className="font-semibold text-[var(--ls-text)]" key={index}>{inline(block.inline)}</h3>
          : <h4 className="font-semibold text-[var(--ls-text)]" key={index}>{inline(block.inline)}</h4>;
        if (block.kind === "list") {
          const items = block.items.map((item, itemIndex) => <li key={itemIndex}>{inline(item)}</li>);
          return block.ordered
            ? <ol className="list-decimal space-y-1 pl-5" key={index}>{items}</ol>
            : <ul className="list-disc space-y-1 pl-5" key={index}>{items}</ul>;
        }
        return <p className="whitespace-pre-line" key={index}>{inline(block.inline)}</p>;
      })}
    </div>
  );
}
