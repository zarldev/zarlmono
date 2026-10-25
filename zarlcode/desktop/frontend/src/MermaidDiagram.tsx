import { useEffect, useId, useState } from "react";

// Mermaid has process-global configuration and temporary DOM. Serialize jobs;
// superseded/unmounted jobs are skipped before work and cannot publish results.
let queue: Promise<void> = Promise.resolve();
async function renderDiagram(id: string, source: string): Promise<string> {
  if (source.length > 20_000)
    throw new Error("Diagram exceeds 20,000 characters");
  // Diagram-local configuration and embedded resources are not a trust boundary.
  // Keep the native app's configuration authoritative, regardless of syntax.
  if (
    /%%\s*\{|^\s*---|<\s*\/?\s*[a-z!]|\b(?:https?:|file:|data:|javascript:|image\s*:)/im.test(
      source,
    )
  ) {
    throw new Error(
      "Diagram configuration, HTML and external resources are disabled",
    );
  }
  const { default: mermaid } = await import("mermaid");
  mermaid.initialize({
    startOnLoad: false,
    securityLevel: "strict",
    theme: "dark",
    htmlLabels: false,
    maxTextSize: 20_000,
    maxEdges: 200,
    suppressErrorRendering: true,
    secure: [
      "secure",
      "securityLevel",
      "startOnLoad",
      "maxTextSize",
      "maxEdges",
      "htmlLabels",
      "themeCSS",
      "flowchart",
      "dompurifyConfig",
    ],
    flowchart: { htmlLabels: false },
  });
  const container = document.createElement("div");
  container.style.cssText =
    "position:fixed;left:-100000px;top:0;visibility:hidden;pointer-events:none";
  document.body.append(container);
  try {
    const { svg } = await mermaid.render(id, source, container);
    // SVG is loaded as an image document, never injected into the privileged DOM.
    // Image context disables scripts, interaction and external-resource loading.
    return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
  } finally {
    container.remove();
  }
}

export function MermaidDiagram({
  source,
  streaming,
}: {
  source: string;
  streaming: boolean;
}) {
  const id = `diagram-${useId().replace(/[^a-zA-Z0-9]/g, "")}`;
  const [result, setResult] = useState<{
    source: string;
    url: string;
    error: string;
  }>({ source: "", url: "", error: "" });
  const [showSource, setShowSource] = useState(false);
  useEffect(() => {
    let cancelled = false;
    const timer = window.setTimeout(
      () => {
        const job = async () => {
          if (cancelled) return;
          try {
            const url = await renderDiagram(id, source);
            if (!cancelled) setResult({ source, url, error: "" });
          } catch (cause) {
            if (!cancelled)
              setResult({
                source,
                url: "",
                error:
                  cause instanceof Error
                    ? cause.message
                    : "Diagram could not be rendered",
              });
          }
        };
        queue = queue.then(job, job);
      },
      streaming ? 600 : 0,
    );
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [id, source, streaming]);
  const error = result.source === source && !streaming ? result.error : "";
  return (
    <figure className="mermaid-diagram">
      <div className="diagram-toolbar">
        <span>Mermaid</span>
        <button
          type="button"
          aria-pressed={showSource}
          onClick={() => setShowSource(!showSource)}
        >
          {showSource ? "Diagram" : "Source"}
        </button>
      </div>
      {!showSource && result.url && (
        <img src={result.url} alt="Mermaid diagram" />
      )}
      {!showSource && !result.url && !error && (
        <span className="diagram-status">
          {streaming ? "Receiving diagram…" : "Rendering diagram…"}
        </span>
      )}
      {error && !showSource && (
        <details className="diagram-error">
          <summary>Diagram could not be rendered</summary>
          <pre>{error}</pre>
        </details>
      )}
      {(showSource || !!error) && (
        <pre>
          <code className="language-mermaid">{source}</code>
        </pre>
      )}
    </figure>
  );
}
