export class ConnectionError extends Error {
  constructor() {
    super("Нет связи с панелью. Повторяем подключение…");
    this.name = "ConnectionError";
  }
}
export async function fetchPanel(
  path: string,
  init: RequestInit,
): Promise<Response> {
  const timeout = AbortSignal.timeout(15000);
  try {
    return await fetch(path, {
      ...init,
      signal: init.signal ? AbortSignal.any([init.signal, timeout]) : timeout,
    });
  } catch (e) {
    if (init.signal?.aborted) throw e;
    if (e instanceof TypeError || timeout.aborted) throw new ConnectionError();
    throw e;
  }
}
export type Request = <T>(
  path: string,
  method?: string,
  data?: unknown,
  key?: string,
  signal?: AbortSignal,
) => Promise<T>;
