import { useEffect } from 'react';

/**
 * Menu behavior for `<details>`-based dropdowns (settings menu, row
 * kebabs): they close when the user clicks or taps anywhere outside
 * them, and on Escape. Content disclosures (diff rows, node groups,
 * collapsible sections) are deliberately not menus and stay open until
 * their own summary is clicked.
 */
const OPEN_MENU_SELECTOR = 'details.action-menu[open], details.settings-menu[open], details.mobile-nav[open]';

function closeMenusExcept(target: Node | null) {
  for (const menu of Array.from(
    document.querySelectorAll<HTMLDetailsElement>(OPEN_MENU_SELECTOR),
  )) {
    if (!target || !menu.contains(target)) menu.open = false;
  }
}

/** A modal dialog owns Escape and outside clicks; menus behind it must
 * stay open so focus can return to the control that opened it. */
function modalDialogOpen(): boolean {
  return document.querySelector('[role="dialog"][aria-modal="true"]') !== null;
}

export function useMenuDetailsClose() {
  useEffect(() => {
    // Capture-phase pointerdown so the menu closes before any click
    // handler on the outside element runs, matching native dropdowns.
    const onPointerDown = (event: PointerEvent) => {
      if (modalDialogOpen()) return;
      closeMenusExcept(event.target instanceof Node ? event.target : null);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || modalDialogOpen()) return;
      closeMenusExcept(null);
    };
    document.addEventListener('pointerdown', onPointerDown, true);
    document.addEventListener('keydown', onKeyDown, true);
    return () => {
      document.removeEventListener('pointerdown', onPointerDown, true);
      document.removeEventListener('keydown', onKeyDown, true);
    };
  }, []);
}
