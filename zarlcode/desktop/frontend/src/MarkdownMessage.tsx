import { createContext, memo, useContext, useId, useState } from "react";
import Markdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import rehypeHighlight from "rehype-highlight";
import rehypeKatex from "rehype-katex";
import remend from "remend";
import { useStreamedText } from "./useStreamedText";
import "katex/dist/katex.min.css";
import { MermaidDiagram } from "./MermaidDiagram";
import type { Element, RootContent } from "hast";

const Streaming = createContext(false);
function codeText(node: RootContent): string {
  if (node.type === "text") return node.value;
  return "children" in node ? node.children.map(codeText).join("") : "";
}

// Explicit web links open externally; generated footnote anchors stay within
// this message. Relative/file links never navigate the privileged workbench.
function safeURL(value: string): string {
  if (value.startsWith("#md-")) return value;
  try {
    const url = new URL(value);
    return url.protocol === "https:" || url.protocol === "http:"
      ? url.href
      : "";
  } catch {
    return "";
  }
}

// Keep component identities stable across streamed renders so links and code
// nodes update in place rather than remounting on every delta.
const components: Components = {
  pre: function CodeBlock({ node, children, ...props }) {
    const streaming = useContext(Streaming);
    const code = node?.children.find(
      (child) => child.type === "element" && child.tagName === "code",
    ) as Element | undefined;
    const languages = code?.properties.className;
    if (
      code &&
      Array.isArray(languages) &&
      languages.includes("language-mermaid")
    ) {
      return (
        <MermaidDiagram
          source={codeText(code).replace(/\n$/, "")}
          streaming={streaming}
        />
      );
    }
    return <pre {...props}>{children}</pre>;
  },
  img: ({ alt }) => (
    <span className="markdown-image">
      {alt ? `[Image: ${alt}]` : "[Image]"}
    </span>
  ),
  a: function SafeLink({ href, children, node: _node, ...props }) {
    const [error, setError] = useState("");
    if (!href) return <span>{children}</span>;
    const local = href.startsWith("#md-");
    return (
      <>
        <a
          {...props}
          href={href}
          target={local ? undefined : "_blank"}
          rel={local ? undefined : "noopener noreferrer"}
          onClick={async (event) => {
            if (local) {
              event.preventDefault();
              const owner = event.currentTarget.closest(".markdown-message");
              const id = href.slice(1);
              const target = Array.from(
                owner?.querySelectorAll<HTMLElement>("[id]") ?? [],
              ).find((element) => element.id === id);
              target?.scrollIntoView({ block: "nearest" });
              target?.focus({ preventScroll: true });
              return;
            }
            if (!("_wails" in window)) return;
            event.preventDefault();
            try {
              const { Browser } = await import("@wailsio/runtime");
              await Browser.OpenURL(href);
              setError("");
            } catch {
              setError("Link could not be opened");
            }
          }}
        >
          {children}
        </a>
        {error && (
          <span className="markdown-link-error" role="alert">
            {error}
          </span>
        )}
      </>
    );
  },
};

export const MarkdownMessage = memo(function MarkdownMessage({
  text,
  streaming = false,
}: {
  text: string;
  streaming?: boolean;
}) {
  const display = useStreamedText(text, streaming);
  const prefix = `md-${useId().replace(/[^a-zA-Z0-9]/g, "")}-`;
  // Repair display-only open delimiters. Finished/cancelled messages always
  // render the exact source, including intentional literal unmatched syntax.
  const source = streaming ? remend(display) : display;
  return (
    <div
      className="message-text markdown-message"
      data-streaming={streaming || undefined}
    >
      <Streaming.Provider value={streaming}>
        <Markdown
          remarkPlugins={[remarkGfm, remarkMath]}
          rehypePlugins={[
            [rehypeHighlight, { detect: false, ignoreMissing: true }],
            [
              rehypeKatex,
              {
                trust: false,
                strict: "ignore",
                throwOnError: false,
                maxExpand: 200,
                maxSize: 20,
              },
            ],
          ]}
          remarkRehypeOptions={{ clobberPrefix: prefix }}
          skipHtml
          urlTransform={safeURL}
          components={components}
        >
          {source}
        </Markdown>
      </Streaming.Provider>
    </div>
  );
});
