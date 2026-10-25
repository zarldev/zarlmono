import { useLayoutEffect, useRef, useState } from "react";

// Smooth only new live suffixes, never archived text or the canonical value.
// Catch-up is time-bounded; large bursts and reduced-motion users skip it.
export function useStreamedText(text: string, streaming: boolean): string {
  const [display, setDisplay] = useState(text);
  const shown = useRef(text);
  const target = useRef(text);
  target.current = text;

  useLayoutEffect(() => {
    let frame = 0;
    const motion = window.matchMedia("(prefers-reduced-motion: reduce)");
    const publish = (value: string) => {
      shown.current = value;
      setDisplay(value);
    };
    const flush = () => {
      cancelAnimationFrame(frame);
      publish(target.current);
    };
    const interrupted = () => {
      if (motion.matches || document.hidden) flush();
    };
    const base = shown.current;
    if (
      !streaming ||
      motion.matches ||
      document.hidden ||
      !text.startsWith(base) ||
      text.length - base.length > 4096
    ) {
      flush();
      return;
    }
    if (text === base) return;

    // Reveal complete graphemes: emoji/combining marks never split mid-frame.
    const segmenter = new Intl.Segmenter(undefined, {
      granularity: "grapheme",
    });
    const suffix = text.slice(base.length);
    const ends = Array.from(
      segmenter.segment(suffix),
      (part) => part.index + part.segment.length,
    );
    const started = performance.now();
    let lastPaint = 0;
    const tick = (now: number) => {
      const progress = Math.min(1, (now - started) / 120);
      if (now - lastPaint >= 32 || progress === 1) {
        const count = Math.max(1, Math.ceil(ends.length * progress));
        publish(base + suffix.slice(0, ends[count - 1]));
        lastPaint = now;
      }
      if (progress < 1) frame = requestAnimationFrame(tick);
    };
    frame = requestAnimationFrame(tick);
    motion.addEventListener("change", interrupted);
    document.addEventListener("visibilitychange", interrupted);
    return () => {
      cancelAnimationFrame(frame);
      motion.removeEventListener("change", interrupted);
      document.removeEventListener("visibilitychange", interrupted);
    };
  }, [text, streaming]);

  // Final text and non-prefix replacements are exact in the very first render.
  return !streaming || !text.startsWith(display) ? text : display;
}
