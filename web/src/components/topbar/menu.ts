/** Close the nearest open <details> ancestor, so a menu item both
 *  acts and dismisses its dropdown in one click. */
export function closeAncestorDetails(target: HTMLElement) {
  target.closest('details')?.removeAttribute('open');
}
