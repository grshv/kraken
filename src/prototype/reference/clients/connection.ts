// tlsUnavailable explains why the TLS option is closed, or "" when it is open.
export function tlsUnavailable(cert: boolean | null, accounts: number): string {
  if (cert === false)
    return "Сертификат прокси не установлен — TLS-подключение недоступно, см. справку «TLS-прокси с логином»";
  if (!accounts) return "Сначала создайте логин прокси в разделе «Аккаунты»";
  return "";
}
