import { useEffect, useRef, type RefObject } from 'react';

const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  'summary',
  '[tabindex]:not([tabindex="-1"])',
].join(',');

/**
 * Keeps keyboard focus inside a modal dialog while it is open: moves focus in,
 * wraps Tab and Shift+Tab, closes on Escape, and restores the previous focus.
 * Dialogs that render conditionally pass `active`.
 */
export function useDialogFocus<T extends HTMLElement>(
  active: boolean,
  onClose: () => void,
  closeEnabled = true,
): RefObject<T> {
  const ref = useRef<T>(null);
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  const closeEnabledRef = useRef(closeEnabled);
  closeEnabledRef.current = closeEnabled;

  useEffect(() => {
    if (!active) return;
    const node = ref.current;
    if (!node) return;
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    if (!node.hasAttribute('tabindex')) node.tabIndex = -1;

    const focusable = () =>
      Array.from(node.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
        (el) => el.getClientRects().length > 0 && el.getAttribute('aria-hidden') !== 'true',
      );

    const initial = node.querySelector<HTMLElement>('[data-autofocus]') ?? node;
    initial.focus();

    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        event.stopPropagation();
        // A destructive request that is already in flight owns the outcome;
        // dismissing the dialog would falsely report "cancelled".
        if (closeEnabledRef.current) closeRef.current();
        return;
      }
      if (event.key !== 'Tab') return;
      const items = focusable();
      if (items.length === 0) {
        event.preventDefault();
        node.focus();
        return;
      }
      const first = items[0];
      const last = items[items.length - 1];
      const current = document.activeElement;
      if (event.shiftKey) {
        if (current === first || current === node || !node.contains(current)) {
          event.preventDefault();
          last.focus();
        }
      } else if (current === last || !node.contains(current)) {
        event.preventDefault();
        first.focus();
      }
    };

    node.addEventListener('keydown', onKeyDown);
    return () => {
      node.removeEventListener('keydown', onKeyDown);
      if (previous && document.contains(previous)) previous.focus();
    };
  }, [active]);

  return ref;
}
