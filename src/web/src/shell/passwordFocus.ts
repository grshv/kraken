// releasePasswordFocus blurs a focused password field before the screen that
// holds it unmounts. Chrome on macOS turns on Secure Input while a password
// field has focus and keeps it on when that field is removed from the DOM
// still focused (Enter-submit of a login or password form), which blocks
// global hotkeys of other apps until the page is reloaded.
type FocusDoc = {
  activeElement: { tagName?: string; type?: string; blur?: () => void } | null;
};

export function releasePasswordFocus(
  doc: FocusDoc = document as unknown as FocusDoc,
): void {
  const el = doc.activeElement;
  if (el?.tagName === "INPUT" && el.type === "password") el.blur?.();
}
