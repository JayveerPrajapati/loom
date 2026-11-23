import { useEffect, useRef, useState, type ReactNode } from 'react';

/**
 * The row kebab: one menu for every secondary action so a row keeps
 * exactly two visible actions. The native <details> toggle gives
 * keyboard open/close; while open, an outside press or Escape closes
 * the menu and Escape returns focus to the trigger. Selecting an item
 * runs its action, closes the menu, and parks focus back on the
 * trigger — actions that open dialogs (delete confirms, history) then
 * take focus as usual.
 */
export function AssetRowMenu({
  label,
  children,
  testId = 'asset-actions',
}: {
  label: string;
  children: ReactNode;
  testId?: string;
}) {
  const detailsRef = useRef<HTMLDetailsElement>(null);
  const summaryRef = useRef<HTMLElement>(null);
  const [open, setOpen] = useState(false);

  const close = (restoreFocus: boolean) => {
    const details = detailsRef.current;
    if (!details?.open) return;
    details.open = false;
    setOpen(false);
    if (restoreFocus) summaryRef.current?.focus();
  };

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (event: PointerEvent) => {
      if (detailsRef.current?.contains(event.target as Node)) return;
      close(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      close(true);
    };
    document.addEventListener('pointerdown', onPointerDown);
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('pointerdown', onPointerDown);
      document.removeEventListener('keydown', onKeyDown);
    };
  }, [open]);

  return (
    <details
      className="action-menu"
      data-testid={testId}
      ref={detailsRef}
      onToggle={(event) => {
        const details = event.target as HTMLDetailsElement;
        setOpen(details.open);
        if (!details.open) return;
        // The body clips, so a menu that would open past the viewport
        // bottom is unreachable. When the space below the trigger is
        // short and the space above is enough, flip the list upward.
        const list = details.querySelector('.action-menu-list') as HTMLElement | null;
        if (!list) return;
        const triggerBox = details.getBoundingClientRect();
        const height = list.getBoundingClientRect().height;
        const below = window.innerHeight - triggerBox.bottom;
        if (below < height + 12 && triggerBox.top > height + 12) {
          list.classList.add('action-menu-up');
        } else {
          list.classList.remove('action-menu-up');
        }
      }}
    >
      <summary className="button subtle" ref={summaryRef} aria-label={label}>
        ⋯
      </summary>
      <div
        className="action-menu-list"
        onClick={() => {
          close(true);
        }}
      >
        {children}
      </div>
    </details>
  );
}
