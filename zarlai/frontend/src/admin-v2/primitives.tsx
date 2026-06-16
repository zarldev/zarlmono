import type { ReactNode, CSSProperties } from 'react'
import { T, type GroupId } from './tokens'

// Shared primitives for admin-v2 views. Every view that used to declare its
// own local Card / MicroLabel / button trio should import from here instead.
// Keeps chrome (padding, border radius, font scale, accent usage) consistent
// across the dashboard — and means a future polish pass only has to touch
// this file to lift the entire admin look.

// ── Chrome ──────────────────────────────────────────────────────────────────

export function Card({ children, className = '', style, padding = 'normal' }: {
  children: ReactNode
  className?: string
  style?: CSSProperties
  // "normal" — standard 20px padding. "tight" — 12px, for nested cards /
  // table wraps. "flush" — no padding, caller controls; use when you want
  // Card chrome around a table that owns its own row padding.
  padding?: 'normal' | 'tight' | 'flush'
}) {
  const pad = padding === 'normal' ? 'p-5' : padding === 'tight' ? 'p-3' : ''
  return (
    <section
      className={`flex flex-col gap-4 ${pad} rounded-xl border ${className}`}
      style={{ background: T.raised, borderColor: T.border, ...style }}
    >
      {children}
    </section>
  )
}

export function MicroLabel({ children, className = '' }: {
  children: ReactNode
  className?: string
}) {
  return (
    <div
      className={`text-[10px] uppercase tracking-[0.12em] ${className}`}
      style={{ color: T.textDim }}
    >
      {children}
    </div>
  )
}

export function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="flex flex-col gap-1.5">
      <MicroLabel>{label}</MicroLabel>
      {children}
    </label>
  )
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="text-[12px]" style={{ color: T.textDim }}>{children}</div>
}

export const fieldCls = 'w-full px-3 py-2 rounded-lg border bg-transparent text-[13px] outline-none'
export const fieldStyle: CSSProperties = { color: T.textBright, borderColor: T.border }

// ── Colour helpers ──────────────────────────────────────────────────────────

export function accentForGroup(group: GroupId): string {
  return T.accent[group]
}

// ── Buttons ─────────────────────────────────────────────────────────────────

type ButtonProps = {
  onClick?: () => void
  disabled?: boolean
  children: ReactNode
  title?: string
  className?: string
  type?: 'button' | 'submit'
}

// PrimaryButton — filled-accent CTA. Pass `group` to pick the accent colour
// that matches the sidebar group the view belongs to (identity / runtime /
// review / history). Defaults to identity since most admin CTAs are
// "create" / "save" actions on identity-shaped data.
export function PrimaryButton({ group = 'identity', onClick, disabled, children, title, className = '', type = 'button' }: ButtonProps & { group?: GroupId }) {
  const accent = T.accent[group]
  return (
    <button
      type={type}
      onClick={onClick}
      disabled={disabled}
      title={title}
      className={`text-[11px] px-3 py-1.5 rounded-lg border font-semibold transition-colors disabled:opacity-40 ${className}`}
      style={{
        color: accent,
        borderColor: `${accent}66`,
        background: `${accent}14`,
      }}
    >{children}</button>
  )
}

// SecondaryButton — neutral bordered control for non-primary actions
// (Cancel, Edit, etc.). No group colour.
export function SecondaryButton({ onClick, disabled, children, title, className = '', type = 'button' }: ButtonProps) {
  return (
    <button
      type={type}
      onClick={onClick}
      disabled={disabled}
      title={title}
      className={`text-[11px] px-3 py-1.5 rounded-lg border transition-colors disabled:opacity-40 ${className}`}
      style={{ color: T.textBright, borderColor: T.borderStrong }}
    >{children}</button>
  )
}

// GhostButton — flat text for minor/tertiary actions (Back, Add member,
// dismiss). No border, no fill.
export function GhostButton({ onClick, disabled, children, title, className = '', type = 'button' }: ButtonProps) {
  return (
    <button
      type={type}
      onClick={onClick}
      disabled={disabled}
      title={title}
      className={`text-[11px] px-3 py-1.5 rounded-lg transition-colors disabled:opacity-40 ${className}`}
      style={{ color: T.textDim }}
    >{children}</button>
  )
}

// DangerButton — destructive actions (Delete, Remove). Error-accent border
// and text. Keep ConfirmationDialog semantics at the call site.
export function DangerButton({ onClick, disabled, children, title, className = '', type = 'button' }: ButtonProps) {
  return (
    <button
      type={type}
      onClick={onClick}
      disabled={disabled}
      title={title}
      className={`text-[11px] px-3 py-1.5 rounded-lg border transition-colors disabled:opacity-40 ${className}`}
      style={{ color: T.status.error, borderColor: `${T.status.error}40` }}
    >{children}</button>
  )
}

// ── Selection row ───────────────────────────────────────────────────────────

// SelectRow is the shared "row in a list that can be selected" primitive.
// Left-border accent + subtle bg tint when selected, matching the pattern
// ProfilesView and PromptsView already use for their list panes.
export function SelectRow({
  selected, group = 'identity', onClick, children, title,
}: {
  selected: boolean
  group?: GroupId
  onClick: () => void
  children: ReactNode
  title?: string
}) {
  const accent = T.accent[group]
  return (
    <button
      type="button"
      onClick={onClick}
      title={title}
      className="w-full text-left px-3 py-2 rounded-lg transition-colors"
      style={{
        borderLeft: `2px solid ${selected ? accent : 'transparent'}`,
        background: selected ? `${accent}14` : 'transparent',
        color: selected ? T.textBright : T.text,
      }}
    >
      {children}
    </button>
  )
}
