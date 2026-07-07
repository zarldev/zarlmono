package browser

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chromedp/chromedp"
	"github.com/zarldev/zarlmono/zkit/agent/computer"
)

func (s *Session) runTargetScript(ctx context.Context, target *computer.TargetRef, op, value, key string) error {
	script := fmt.Sprintf(`(() => {
%s
const target = %s;
const el = resolveTarget(target);
if (!el) {
  throw new Error('target not found');
}
const op = %s;
if (op === 'click') {
  el.click();
  return true;
}
if (op === 'focus') {
  el.focus();
  return true;
}
if (op === 'fill') {
  el.focus();
  const value = %s;
  if ('value' in el) {
    el.value = value;
  } else {
    el.textContent = value;
  }
  el.dispatchEvent(new Event('input', { bubbles: true }));
  el.dispatchEvent(new Event('change', { bubbles: true }));
  return true;
}
throw new Error('unsupported target operation: ' + op);
})()`, browserJSLibrary(), targetJSON(target), jsString(op), jsString(value))

	var ok bool
	if err := s.run(ctx, chromedp.Evaluate(script, &ok)); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("target operation %q returned false", op)
	}
	_ = key
	return nil
}

func targetPredicateScript(target *computer.TargetRef, predicate, text, value string) string {
	return fmt.Sprintf(`(() => {
%s
const target = %s;
const predicate = %s;
const el = resolveTarget(target);
if (predicate === 'hidden') {
  return !el || !isVisible(el);
}
if (!el) {
  return false;
}
if (predicate === 'visible') {
  return isVisible(el);
}
if (predicate === 'focused') {
  return document.activeElement === el;
}
if (predicate === 'text_present') {
  const expected = %s;
  const haystack = target && (target.id || target.locator || target.role || target.name || target.text || target.position) ? (el.innerText || el.textContent || '') : (document.body ? document.body.innerText || '' : '');
  return haystack.includes(expected);
}
if (predicate === 'value_equals') {
  const expected = %s;
  return String(('value' in el) ? el.value : (el.textContent || '')) === expected;
}
return false;
})()`, browserJSLibrary(), targetJSON(target), jsString(predicate), jsString(text), jsString(value))
}

func targetJSON(target *computer.TargetRef) string {
	if target == nil {
		return "null"
	}
	by, err := json.Marshal(target)
	if err != nil {
		return "null"
	}
	return string(by)
}

func jsString(value string) string {
	by, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(by)
}

func browserJSLibrary() string {
	return `
function normalize(s) {
  return String(s || '').trim().replace(/\s+/g, ' ');
}
function isVisible(el) {
  if (!el || el.nodeType !== Node.ELEMENT_NODE) return false;
  const rect = el.getBoundingClientRect();
  const style = window.getComputedStyle(el);
  return rect.width > 0 && rect.height > 0 && style.visibility !== 'hidden' && style.display !== 'none' && Number(style.opacity || 1) !== 0;
}
function elementRole(el) {
  if (!el) return '';
  const explicit = el.getAttribute('role');
  if (explicit) return explicit;
  const tag = el.tagName ? el.tagName.toLowerCase() : '';
  if (tag === 'a') return 'link';
  if (tag === 'button') return 'button';
  if (tag === 'select') return 'combobox';
  if (tag === 'textarea') return 'textbox';
  if (tag === 'input') {
    const type = (el.getAttribute('type') || 'text').toLowerCase();
    if (type === 'button' || type === 'submit' || type === 'reset') return 'button';
    if (type === 'checkbox') return 'checkbox';
    if (type === 'radio') return 'radio';
    return 'textbox';
  }
  return tag;
}
function elementName(el) {
  if (!el) return '';
  return normalize(el.getAttribute('aria-label') || el.getAttribute('title') || el.getAttribute('alt') || el.getAttribute('placeholder') || el.name || el.innerText || el.textContent || el.value || '');
}
function describeTarget(el, i) {
  if (!el || el.nodeType !== Node.ELEMENT_NODE) return null;
  const rect = el.getBoundingClientRect();
  const role = elementRole(el);
  const name = elementName(el);
  const text = normalize(el.innerText || el.textContent || '');
  const id = el.id ? ('#' + el.id) : ('target-' + i);
  return {
    id: id,
    role: role,
    name: name,
    text: text,
    description: normalize(el.getAttribute('aria-description') || el.getAttribute('title') || ''),
    x: Math.round(rect.x),
    y: Math.round(rect.y),
    width: Math.round(rect.width),
    height: Math.round(rect.height),
    enabled: !el.disabled && el.getAttribute('aria-disabled') !== 'true',
    visible: isVisible(el),
    focused: document.activeElement === el,
    value: String(('value' in el) ? el.value : '')
  };
}
function resolveTarget(target) {
  if (!target) return document.activeElement || document.body;
  if (target.id) {
    let id = target.id.startsWith('#') ? target.id.slice(1) : target.id;
    const byID = document.getElementById(id);
    if (byID) return byID;
    if (target.id.startsWith('#')) {
      try {
        const bySelectorID = document.querySelector(target.id);
        if (bySelectorID) return bySelectorID;
      } catch (_) {}
    }
  }
  if (target.locator) {
    try {
      const byLocator = document.querySelector(target.locator);
      if (byLocator) return byLocator;
    } catch (_) {}
  }
  const candidates = Array.from(document.querySelectorAll('a,button,input,textarea,select,[role],[aria-label],[contenteditable="true"]'));
  for (const el of candidates) {
    if (target.role && elementRole(el) !== target.role) continue;
    if (target.name && !elementName(el).includes(target.name)) continue;
    if (target.text && !normalize(el.innerText || el.textContent || '').includes(target.text)) continue;
    return el;
  }
  if (target.position) {
    return document.elementFromPoint(target.position.x || 0, target.position.y || 0);
  }
  return null;
}
`
}
