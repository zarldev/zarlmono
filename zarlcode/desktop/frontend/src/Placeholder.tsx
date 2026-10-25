type Shape = "plan" | "files" | "inspector" | "conversation" | "output";

// Empty panes stay blank. Only an unsuccessful search needs visible feedback.
export function Placeholder({ shape, label }: { shape: Shape; label: string }) {
  if (shape !== 'conversation' || !label) return null;
  return <p className="empty-search" role="status">{label}</p>;
}
