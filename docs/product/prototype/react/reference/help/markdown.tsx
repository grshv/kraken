import {Button} from "@/components/ui/button";
// Minimal, safe Markdown renderer. No raw HTML passthrough: input is treated
// as text. Supports headings (#..######), unordered lists (-/*), ordered
// lists (1.), fenced code blocks (```), inline `code`, **bold**, *italic* and
// links. help:<slug> links resolve to in-article navigation callbacks.
//
// Block-level parsing is line-oriented; inline formatting is tokenized per
// text run. Anything unrecognized is rendered as a paragraph.
import type { ReactNode } from "react";

export type MarkdownLinkTarget = { slug: string; text: string };

// sectionID is the anchor of the n-th "##" heading, shared by the renderer
// and the table of contents.
export function sectionID(n: number): string {
  return `section-${n + 1}`;
}

// splitArticle separates the lead (the first paragraph, shown under the
// title) from the body and lists the "##" headings for the table of contents.
// Fenced code is skipped so a "## " inside a code block is not a heading.
export function splitArticle(markdown: string): {
  lead: string;
  body: string;
  outline: string[];
} {
  const lines = markdown.replace(/\r\n/g, "\n").split("\n");
  let start = 0;
  while (start < lines.length && !lines[start].trim()) start++;
  let lead = "";
  let end = start;
  const plain = (l: string) =>
    !!l.trim() && !/^(#|```|\s*[-*]\s|\s*\d+\.\s|>)/.test(l);
  if (plain(lines[start] ?? "")) {
    while (end < lines.length && plain(lines[end])) end++;
    lead = lines.slice(start, end).join(" ").trim();
  }
  const body = lead ? lines.slice(end).join("\n") : markdown;
  const outline: string[] = [];
  let fenced = false;
  for (const l of body.split("\n")) {
    if (/^```/.test(l)) fenced = !fenced;
    else if (!fenced) {
      const m = /^##\s+(.*)$/.exec(l);
      if (m) outline.push(m[1].trim());
    }
  }
  return { lead, body, outline };
}

export function renderMarkdown(
  markdown: string,
  onHelpLink?: (slug: string) => void,
): ReactNode[] {
  const lines = markdown.replace(/\r\n/g, "\n").split("\n");
  const blocks: ReactNode[] = [];
  let i = 0;
  let key = 0;
  let section = 0;
  const k = () => `md-${key++}`;

  while (i < lines.length) {
    const line = lines[i];

    // Fenced code block
    const fence = /^```(.*)$/.exec(line);
    if (fence) {
      const lang = fence[1].trim();
      const buf: string[] = [];
      i++;
      while (i < lines.length && !/^```$/.test(lines[i])) {
        buf.push(lines[i]);
        i++;
      }
      i++; // closing fence
      blocks.push(
        <pre
          key={k()}
          className="m-0 mb-3 overflow-auto rounded-lg border bg-muted p-3 font-mono text-sm leading-[1.6]"
          data-lang={lang || undefined}
        >
          <code>{buf.join("\n")}</code>
        </pre>,
      );
      continue;
    }

    // Heading
    const h = /^(#{1,6})\s+(.*)$/.exec(line);
    if (h) {
      const level = h[1].length;
      const text = h[2].trim();
      const Tag = `h${Math.min(level + 1, 6)}` as unknown as "h1";
      blocks.push(
        <Tag
          key={k()}
          id={level === 2 ? sectionID(section++) : undefined}
          className={`m-0 scroll-mt-4 font-semibold ${level <= 1 ? "text-xl" : "text-lg"}`}
        >
          {inline(text, onHelpLink)}
        </Tag>,
      );
      i++;
      continue;
    }

    // Unordered list
    if (/^\s*[-*]\s+/.test(line)) {
      const items: ReactNode[] = [];
      while (i < lines.length && /^\s*[-*]\s+/.test(lines[i])) {
        const text = lines[i].replace(/^\s*[-*]\s+/, "");
        items.push(<li key={`li-${key++}`}>{inline(text, onHelpLink)}</li>);
        i++;
      }
      blocks.push(
        <ul key={k()} className="m-0 mb-3 list-disc pl-[22px] leading-[1.6]">
          {items}
        </ul>,
      );
      continue;
    }

    // Ordered list
    if (/^\s*\d+\.\s+/.test(line)) {
      const items: ReactNode[] = [];
      while (i < lines.length && /^\s*\d+\.\s+/.test(lines[i])) {
        const text = lines[i].replace(/^\s*\d+\.\s+/, "");
        items.push(<li key={`li-${key++}`}>{inline(text, onHelpLink)}</li>);
        i++;
      }
      blocks.push(
        <ol key={k()} className="m-0 mb-3 list-decimal pl-[22px] leading-[1.6]">
          {items}
        </ol>,
      );
      continue;
    }

    // Blank line: skip, don't emit an empty paragraph
    if (line.trim() === "") {
      i++;
      continue;
    }

    // Paragraph: gather consecutive non-blank, non-block lines
    const para: string[] = [];
    while (
      i < lines.length &&
      lines[i].trim() !== "" &&
      !/^(#{1,6})\s+/.test(lines[i]) &&
      !/^\s*[-*]\s+/.test(lines[i]) &&
      !/^\s*\d+\.\s+/.test(lines[i]) &&
      !/^```/.test(lines[i])
    ) {
      para.push(lines[i]);
      i++;
    }
    blocks.push(
      <p key={k()} className="m-0 mb-3 leading-[1.6]">
        {inline(para.join(" "), onHelpLink)}
      </p>,
    );
  }

  return blocks;
}

// Tokenize a single text run into inline elements. Order matters: links first
// (they may contain other inline markers in their text), then code, bold, italic.
// renderInline formats one run of inline Markdown (code, bold, links).
export function renderInline(
  text: string,
  onHelpLink?: (slug: string) => void,
): ReactNode[] {
  return inline(text, onHelpLink);
}

function inline(
  text: string,
  onHelpLink?: (slug: string) => void,
): ReactNode[] {
  const nodes: ReactNode[] = [];
  let rest = text;
  let key = 0;
  // Pattern matches: [label](target) where target is help:slug or a URL.
  const link = /\[([^\]]*)\]\(([^)]+)\)/;
  while (rest.length) {
    const m = link.exec(rest);
    if (!m) {
      nodes.push(...inlineCode(rest, key++));
      break;
    }
    if (m.index > 0) {
      nodes.push(...inlineCode(rest.slice(0, m.index), key++));
    }
    const label = m[1];
    const target = m[2].trim();
    const help = /^help:(.+)$/.exec(target);
    if (help) {
      nodes.push(
        <Button variant="ghost"
          key={`a-${key++}`}
          type="button"
          className="inline h-auto p-0 text-primary font-medium whitespace-normal hover:bg-transparent hover:underline"
          onClick={() => onHelpLink?.(help[1])}
        >
          {label}
        </Button>,
      );
    } else {
      nodes.push(
        <a
          key={`a-${key++}`}
          href={target}
          className="text-accent font-medium hover:underline"
          rel="noreferrer noopener"
          target="_blank"
        >
          {label}
        </a>,
      );
    }
    rest = rest.slice(m.index + m[0].length);
  }
  return nodes;
}

// Split on `code` then **bold** then *italic*. Code segments are not re-scanned
// for bold/italic (matches CommonMark intent: code is opaque).
function inlineCode(text: string, baseKey: number): ReactNode[] {
  const parts = text.split(/(`[^`]+`)/);
  const out: ReactNode[] = [];
  parts.forEach((part, idx) => {
    if (!part) return;
    const code = /^`([^`]+)`$/.exec(part);
    if (code) {
      out.push(<code key={`c-${baseKey}-${idx}`}>{code[1]}</code>);
      return;
    }
    out.push(...bold(part, `${baseKey}-${idx}`));
  });
  return out;
}

function bold(text: string, baseKey: string): ReactNode[] {
  const parts = text.split(/(\*\*[^*]+\*\*)/);
  const out: ReactNode[] = [];
  parts.forEach((part, idx) => {
    if (!part) return;
    const m = /^\*\*([^*]+)\*\*$/.exec(part);
    if (m) {
      out.push(
        <strong key={`b-${baseKey}-${idx}`}>
          {italic(m[1], `${baseKey}-${idx}`)}
        </strong>,
      );
      return;
    }
    out.push(...italic(part, `${baseKey}-${idx}`));
  });
  return out;
}

function italic(text: string, baseKey: string): ReactNode[] {
  const parts = text.split(/(\*[^*]+\*)/);
  const out: ReactNode[] = [];
  parts.forEach((part, idx) => {
    if (!part) return;
    const m = /^\*([^*]+)\*$/.exec(part);
    if (m) {
      out.push(<em key={`i-${baseKey}-${idx}`}>{m[1]}</em>);
      return;
    }
    out.push(part);
  });
  return out;
}
