## What changed

<!-- One paragraph. Name the user-visible behavior, not the files. -->

## Verification

- [ ] Affected tests run (list them):
- [ ] `npm run typecheck` (web changes)
- [ ] Desktop and narrow-width checked
- [ ] Keyboard and screen-reader checked
- [ ] Screenshots attached for visual changes

## Design contract

Reviewed against [`docs/product/design-contract.md`](../docs/product/design-contract.md), Appendix A.

- [ ] One obvious primary action
- [ ] Effective values and origins are honest (profile/bundle/node/resolved)
- [ ] Loading, empty, filtered-empty, error, offline and permission states covered
- [ ] Destructive actions use the shared dialog
- [ ] No new `window.confirm`, `window.prompt`, or silent primary catch; the grammar gate is green
- [ ] Visual-system constraints hold (tokens, no `transition: all`, no width/height/top/left animation, focus-visible, reduced motion)

## Risk and rollback

<!-- What can break, and how to undo it. -->
