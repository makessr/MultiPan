// Keep keyboard navigation inside the visible dialog until it is dismissed.
export function trapDialogFocus(event: KeyboardEvent) {
  const dialog = event.currentTarget as HTMLElement
  const controls = Array.from(dialog.querySelectorAll<HTMLElement>('button:not([disabled]),input:not([disabled]),select:not([disabled]),summary,[tabindex="0"]')).filter(node=>node.getClientRects().length>0)
  if (!controls.length) { event.preventDefault(); return }
  const first=controls[0]!,last=controls[controls.length-1]!
  if (event.shiftKey && (document.activeElement===first||!dialog.contains(document.activeElement))) {event.preventDefault();last.focus()}
  else if (!event.shiftKey && document.activeElement===last) {event.preventDefault();first.focus()}
}
