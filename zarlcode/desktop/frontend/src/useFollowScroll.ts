import { useCallback, useEffect, useRef, useState } from "react";

// Follow measured content growth, not provider snapshot timing. The animation
// exists only while catching up. User navigation immediately relinquishes it.
export function useFollowScroll() {
  const viewport = useRef<HTMLDivElement>(null);
  const content = useRef<HTMLDivElement>(null);
  const following = useRef(true);
  const lastTop = useRef(0);
  const wake = useRef<() => void>(() => {});
  const [atBottom, setAtBottom] = useState(true);

  const pause = useCallback(() => {
    following.current = false;
    setAtBottom(false);
  }, []);
  const onScroll = useCallback(() => {
    const el = viewport.current;
    if (!el) return;
    // Programmatic writes set lastTop first, so only user upward movement
    // disables following. A growing scrollHeight is not a user scroll.
    if (el.scrollTop < lastTop.current - 2) pause();
    const bottom = el.scrollHeight - el.clientHeight - el.scrollTop < 4;
    if (bottom) {
      following.current = true;
      setAtBottom(true);
    }
    lastTop.current = el.scrollTop;
  }, [pause]);
  const jump = useCallback(() => {
    following.current = true;
    setAtBottom(true);
    const el = viewport.current;
    if (el) {
      el.scrollTop = el.scrollHeight;
      lastTop.current = el.scrollTop;
    }
    wake.current();
  }, []);

  useEffect(() => {
    const el = viewport.current!;
    const body = content.current!;
    const motion = window.matchMedia("(prefers-reduced-motion: reduce)");
    let frame = 0;
    let previousTime = 0;
    const tick = (now: number) => {
      frame = 0;
      if (!following.current) return;
      const goal = Math.max(0, el.scrollHeight - el.clientHeight);
      const gap = goal - el.scrollTop;
      if (Math.abs(gap) < 1) {
        previousTime = 0;
        return;
      }
      const dt = previousTime ? Math.min(now - previousTime, 64) : 16;
      previousTime = now;
      const immediate =
        motion.matches || document.hidden || Math.abs(gap) > el.clientHeight;
      const next =
        immediate || Math.abs(gap) < 2
          ? goal
          : el.scrollTop + gap * (1 - Math.exp(-dt / 45));
      el.scrollTop = next;
      lastTop.current = el.scrollTop;
      if (Math.abs(goal - el.scrollTop) >= 1)
        frame = requestAnimationFrame(tick);
      else previousTime = 0;
    };
    const schedule = () => {
      if (following.current && !frame) frame = requestAnimationFrame(tick);
    };
    wake.current = schedule;
    const observer = new ResizeObserver(schedule);
    observer.observe(body);
    observer.observe(el);
    const wheel = (event: WheelEvent) => {
      if (event.deltaY < 0) pause();
    };
    const key = (event: KeyboardEvent) => {
      if (["ArrowUp", "PageUp", "Home"].includes(event.key)) pause();
    };
    const selection = () => {
      const selected = window.getSelection();
      if (selected && !selected.isCollapsed && el.contains(selected.anchorNode))
        pause();
    };
    el.addEventListener("wheel", wheel, { passive: true });
    el.addEventListener("keydown", key);
    document.addEventListener("selectionchange", selection);
    motion.addEventListener("change", schedule);
    schedule();
    return () => {
      cancelAnimationFrame(frame);
      observer.disconnect();
      el.removeEventListener("wheel", wheel);
      el.removeEventListener("keydown", key);
      document.removeEventListener("selectionchange", selection);
      motion.removeEventListener("change", schedule);
      wake.current = () => {};
    };
  }, [pause]);

  return { viewport, content, onScroll, atBottom, jump };
}
