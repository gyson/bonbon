export interface DividerDrag {
  // Return the size of the controlled pane when a drag starts.
  start(): number;
  // Apply a drag. The offset is the pointer movement along the axis since the start.
  move(start: number, offset: number): void;
}

// Let one pointer drag a divider. The returned function stops the current drag.
export function dragDivider(divider: HTMLElement, axis: 'x' | 'y', bodyClass: string, drag: DividerDrag): () => void {
  let active: { pointer: number; position: number; size: number } | null = null;
  const position = (event: PointerEvent) => axis === 'x' ? event.clientX : event.clientY;
  const stop = () => {
    const current = active;
    active = null;
    if (current && divider.hasPointerCapture(current.pointer)) divider.releasePointerCapture(current.pointer);
    document.body.classList.remove(bodyClass);
  };
  divider.onpointerdown = event => {
    if (event.button !== 0 || !event.isPrimary || active) return;
    event.preventDefault();
    divider.focus({ preventScroll: true });
    active = { pointer: event.pointerId, position: position(event), size: drag.start() };
    divider.setPointerCapture(event.pointerId);
    document.body.classList.add(bodyClass);
  };
  divider.onpointermove = event => {
    if (active?.pointer === event.pointerId) drag.move(active.size, position(event) - active.position);
  };
  divider.onpointerup = divider.onpointercancel = event => {
    if (active?.pointer === event.pointerId) stop();
  };
  divider.onlostpointercapture = stop;
  window.addEventListener('blur', stop);
  return stop;
}
