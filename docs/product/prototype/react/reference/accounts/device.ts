// deviceLabel turns a stored User-Agent into "Browser · OS" for the session
// list. The value is untrusted display data; unknown agents fall back to a
// neutral label rather than echoing a long raw string.
export function deviceLabel(ua: string | null | undefined): string {
  if (!ua) return "Неизвестное устройство";
  const browser = /Edg\//.test(ua)
    ? "Edge"
    : /OPR\/|Opera/.test(ua)
      ? "Opera"
      : /Firefox\//.test(ua)
        ? "Firefox"
        : /Chrome\/|CriOS\//.test(ua)
          ? "Chrome"
          : /Safari\//.test(ua)
            ? "Safari"
            : /curl\//.test(ua)
              ? "curl"
              : null;
  const os = /iPhone|iPad/.test(ua)
    ? "iOS"
    : /Android/.test(ua)
      ? "Android"
      : /Mac OS X|Macintosh/.test(ua)
        ? "macOS"
        : /Windows/.test(ua)
          ? "Windows"
          : /Linux/.test(ua)
            ? "Linux"
            : null;
  const parts = [browser, os].filter(Boolean);
  return parts.length ? parts.join(" · ") : "Другой клиент";
}
