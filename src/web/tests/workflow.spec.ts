import { expect, test as base, type Page } from "@playwright/test";
import { randomBytes } from "node:crypto";
import { mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { startFixture } from "./runtime-fixture";

type Appliance = Awaited<ReturnType<typeof startFixture>>;
const test = base.extend<{ appliance: Appliance }>({
  appliance: async ({}, use) => {
    const appliance = await startFixture();
    try {
      await use(appliance);
    } finally {
      appliance.cleanup();
    }
  },
});
const errors = new WeakMap<Page, string[]>();
test.beforeEach(({ page }) => {
  const captured: string[] = [];
  errors.set(page, captured);
  page.on("pageerror", (error) => captured.push(error.message));
});
test.afterEach(({ page }) => {
  expect(errors.get(page)).toEqual([]);
});

async function login(page: Page, appliance: Appliance) {
  await page.goto(appliance.origin);
  await page.getByLabel("Имя пользователя").fill(appliance.username);
  await page.getByLabel("Пароль", { exact: true }).fill(appliance.password);
  await page.getByRole("button", { name: "Войти в панель" }).click();
  await expect(
    page.getByRole("heading", { name: "Обзор", exact: true }),
  ).toBeVisible();
}
async function navigate(page: Page, section: string) {
  const button = page
    .getByRole("navigation", { name: "Разделы панели" })
    .getByRole("button", { name: section, exact: true });
  if (!(await button.isVisible())) {
    await page
      .getByRole("button", { name: "Открыть или скрыть меню", exact: true })
      .click();
  }
  await button.click();
  await expect(
    page.getByRole("heading", { name: section, exact: true, level: 1 }),
  ).toBeVisible();
}
const screenshotDirectory =
  process.env.SC_SCREENSHOT_DIR || join(tmpdir(), "squid-control-ui");
async function screenshot(page: Page, filename: string, fullPage = true) {
  mkdirSync(screenshotDirectory, { recursive: true });
  await page.evaluate(async () => {
    await document.fonts.ready;
    window.scrollTo(0, 0);
  });
  await page.screenshot({
    path: join(screenshotDirectory, filename),
    fullPage,
    animations: "disabled",
  });
}

test("theme follows the system, switches explicitly and survives reload", async ({
  page,
  appliance,
}) => {
  await page.emulateMedia({ colorScheme: "dark", reducedMotion: "reduce" });
  await login(page, appliance);
  const theme = page.getByRole("button", { name: "Тёмная тема", exact: true });
  await expect(theme).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator("html")).toHaveClass(/\bdark\b/);
  // Until a manual selection, changes to the OS preference remain effective.
  await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" });
  await expect(theme).toHaveAttribute("aria-pressed", "false");
  await expect(page.locator("html")).not.toHaveClass(/\bdark\b/);
  await page.emulateMedia({ colorScheme: "dark", reducedMotion: "reduce" });
  await expect(theme).toHaveAttribute("aria-pressed", "true");
  await theme.click();
  await expect(theme).toHaveAttribute("aria-pressed", "false");
  await expect(page.locator("html")).not.toHaveClass(/\bdark\b/);
  await theme.click();
  await expect(theme).toHaveAttribute("aria-pressed", "true");
  await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" });
  await expect(page.locator("html")).toHaveClass(/\bdark\b/);
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "Обзор", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Тёмная тема", exact: true }),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator("html")).toHaveClass(/\bdark\b/);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await screenshot(page, "workflow-dark-desktop.png");
  await page.setViewportSize({ width: 390, height: 844 });
  await screenshot(page, "workflow-dark-mobile.png");
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBeTruthy();
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole("button", { name: "Тёмная тема", exact: true }).click();
  await expect(page.locator("html")).not.toHaveClass(/\bdark\b/);
  await page.emulateMedia({ colorScheme: "dark", reducedMotion: "reduce" });
  await expect(page.locator("html")).not.toHaveClass(/\bdark\b/);
  await screenshot(page, "workflow-light-desktop.png");
});

test("desktop menu control stays in the sidebar and mobile navigation opens separately", async ({
  page,
  appliance,
}) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await login(page, appliance);
  const sidebar = page.locator('[data-slot="sidebar"]');
  const trigger = sidebar.getByRole("button", {
    name: "Открыть или скрыть меню",
    exact: true,
  });
  await expect(trigger).toBeVisible();
  await expect(page.locator(".console-toolbar")).toBeHidden();
  await trigger.click();
  await expect(sidebar).toHaveAttribute("data-state", "collapsed");
  // The trigger must remain reachable in the collapsed sidebar.
  await trigger.click();
  await expect(sidebar).toHaveAttribute("data-state", "expanded");
  await page.setViewportSize({ width: 390, height: 844 });
  const mobileTrigger = page.getByRole("button", {
    name: "Открыть или скрыть меню",
    exact: true,
  });
  await expect(mobileTrigger).toBeVisible();
  await mobileTrigger.click();
  await expect(page.getByRole("dialog", { name: "Навигация" })).toBeVisible();
  await screenshot(page, "workflow-mobile-menu.png", false);
  await navigate(page, "Клиенты");
  await expect(page.getByRole("dialog", { name: "Навигация" })).toBeHidden();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBeTruthy();
});

test("overview shows real retained request statistics and no client creation action", async ({
  page,
  appliance,
}) => {
  await login(page, appliance);
  await expect(
    page.getByRole("button", { name: "Добавить клиента", exact: true }),
  ).toHaveCount(0);
  const now = (Date.now() / 1000).toFixed(3);
  const records = ["TCP_MISS/200", "TCP_DENIED/403", "TCP_MISS/200"]
    .map(
      (result, index) =>
        `${now} 12 198.51.100.${index + 10} ${result} 128 GET http://stats-${index}.example.test/ - HIER_NONE/- -\n`,
    )
    .join("");
  appliance.docker(
    [
      "exec",
      "-i",
      "--user",
      "13:13",
      appliance.name,
      "tee",
      "-a",
      "/data/logs/access.log",
    ],
    records,
  );
  await expect
    .poll(async () => {
      const response = await page.request.get(
        appliance.origin + "/api/v1/statistics",
      );
      expect(response.status()).toBe(200);
      return (await response.json()).requests;
    })
    .toBe(3);
  await page.getByRole("button", { name: "Обновить", exact: true }).click();
  const stats = page.getByRole("region", { name: "Статистика за 24 часа" });
  await expect(
    stats.getByText("Запросы за 24 часа", { exact: true }),
  ).toBeVisible();
  await expect(
    stats.locator("dt").filter({ hasText: /^Отказы$/ }),
  ).toBeVisible();
  await expect(
    stats
      .locator("dt")
      .filter({ hasText: /^Запросы за 24 часа$/ })
      .locator("+ dd"),
  ).toHaveText("3");
  await expect(
    stats
      .locator("dt")
      .filter({ hasText: /^Отказы$/ })
      .locator("+ dd"),
  ).toHaveText("1");
  await screenshot(page, "workflow-statistics.png");
});

test("a recovered panel connection clears the network warning automatically", async ({
  page,
  appliance,
}) => {
  await login(page, appliance);
  const statusRoute = /\/api\/v1\/status(?:\?|$)/;
  await page.route(statusRoute, (route) => route.abort("failed"));
  await page.getByRole("button", { name: "Обновить", exact: true }).click();
  await expect(page.getByText(/^Нет связи с панелью/)).toBeVisible();
  await expect(page.getByText("Failed to fetch", { exact: true })).toHaveCount(
    0,
  );
  await page.unroute(statusRoute);
  await page.getByRole("button", { name: "Обновить", exact: true }).click();
  await expect(page.getByText(/^Нет связи с панелью/)).toHaveCount(0);
  await expect(
    page.getByRole("region", { name: "Состояние прокси" }),
  ).toBeVisible();
});

test("all entity editors use dialogs and passwords clear on dismissal and reopen", async ({
  page,
  appliance,
}) => {
  await login(page, appliance);
  await navigate(page, "Клиенты");
  await page
    .getByRole("button", { name: "Добавить клиента", exact: true })
    .click();
  let dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await dialog.getByLabel("Название", { exact: true }).fill("workflow-client");
  await dialog
    .getByLabel("IP / CIDR", { exact: true })
    .fill("198.51.100.42/32");
  await dialog
    .getByLabel("Режим доступа", { exact: true })
    .selectOption("all_web");
  await dialog.getByRole("button", { name: /^Сохранить/ }).click();
  await expect(dialog).toBeHidden();
  await expect(
    page.getByRole("cell", { name: "workflow-client", exact: true }),
  ).toBeVisible();

  for (const entry of [
    {
      section: "Пользователи",
      add: "Добавить пользователя",
      username: "workflow-viewer",
    },
    {
      section: "Логины прокси",
      add: "Добавить логин",
      username: "workflow-proxy",
    },
  ]) {
    await navigate(page, entry.section);
    await page.getByRole("button", { name: entry.add, exact: true }).click();
    dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    await dialog
      .getByLabel("Логин учётной записи", { exact: true })
      .fill(entry.username);
    await dialog
      .getByLabel("Пароль учётной записи", { exact: true })
      .fill(randomBytes(24).toString("base64url"));
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(
      page.getByRole("button", { name: entry.add, exact: true }),
    ).toBeFocused();
    await page.getByRole("button", { name: entry.add, exact: true }).click();
    await expect(
      dialog.getByLabel("Пароль учётной записи", { exact: true }),
    ).toHaveValue("");
    await dialog
      .getByLabel("Логин учётной записи", { exact: true })
      .fill(entry.username);
    await dialog
      .getByLabel("Пароль учётной записи", { exact: true })
      .fill(randomBytes(24).toString("base64url"));
    await dialog
      .getByRole("button", { name: "Сохранить", exact: true })
      .click();
    await expect(dialog).toBeHidden();
    const row = page.getByRole("row").filter({
      has: page.getByRole("cell", { name: entry.username, exact: true }),
    });
    await row.getByRole("button", { name: "Изменить", exact: true }).click();
    await expect(
      dialog.getByLabel("Пароль учётной записи", { exact: true }),
    ).toHaveValue("");
    const save = dialog.getByRole("button", { name: "Сохранить", exact: true });
    await expect(save).toBeDisabled();
    await dialog
      .getByLabel("Пароль учётной записи", { exact: true })
      .fill(randomBytes(24).toString("base64url"));
    await expect(save).toBeEnabled();
    await dialog.getByLabel("Пароль учётной записи", { exact: true }).fill("");
    await expect(save).toBeDisabled();
    await dialog.getByRole("button", { name: "Отмена", exact: true }).click();
  }
  await expect(
    page.getByText("Назначьте логин клиенту в его настройках.", {
      exact: false,
    }),
  ).toHaveCount(0);
});

test("settings keep deployment ports out of the editor and load expert configuration automatically", async ({
  page,
  appliance,
}) => {
  await login(page, appliance);
  const previewResponses: { status: number; generated: string }[] = [];
  page.on("response", async (response) => {
    if (
      response.url().includes("/api/v1/configuration/preview") &&
      response.request().method() === "GET"
    ) {
      try {
        const body = await response.json();
        previewResponses.push({
          status: response.status(),
          generated: body.generated_config,
        });
      } catch {
        // Responses can be cancelled during fixture teardown; the assertion
        // below still requires a completed successful preview response.
      }
    }
  });
  await navigate(page, "Настройки");
  await expect(
    page.getByLabel("HTTP-порт внутри контейнера", { exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByLabel("TLS-порт внутри контейнера", { exact: true }),
  ).toHaveCount(0);
  await page.getByRole("tab", { name: "squid.conf", exact: true }).click();
  await page
    .getByLabel("Режим конфигурации", { exact: true })
    .selectOption("expert");
  const editor = page.getByLabel("Экспертная конфигурация", { exact: true });
  await expect(editor).not.toHaveValue("");
  await expect(editor).toHaveValue(/http_access deny all/);
  await page.getByRole("button", { name: /^Сохранить настройки/ }).click();
  await page.getByText("Итоговый squid.conf", { exact: true }).click();
  await expect
    .poll(() =>
      previewResponses.some(
        (response) =>
          response.status === 200 &&
          response.generated.includes("http_access deny all"),
      ),
    )
    .toBeTruthy();
  await expect(page.locator("details[open] pre")).toContainText(
    "http_access deny all",
  );
  await screenshot(page, "workflow-expert.png");
});

test("opening Changes previews a fresh draft without creating a revision or a no-op apply", async ({
  page,
  appliance,
}) => {
  await login(page, appliance);
  const revisions = () =>
    page.request
      .get(appliance.origin + "/api/v1/revisions")
      .then((response) => response.json());
  const before = await revisions();
  const created: string[] = [];
  page.on("request", (request) => {
    if (
      request.method() === "POST" &&
      /\/api\/v1\/revisions$/.test(request.url())
    )
      created.push(request.url());
  });
  const result = await page.request.get(
    appliance.origin + "/api/v1/configuration/preview",
  );
  expect(result.status()).toBe(200);
  const initial = await result.json();
  expect(initial.user_changes).toBe(false);
  expect(initial.runtime_changes).toBe(false);
  expect(initial).not.toHaveProperty("revision_id");
  await navigate(page, "Изменения");
  await expect(
    page.getByText("Нет неприменённых изменений", { exact: true }),
  ).toBeVisible();
  const after = await revisions();
  expect(after.items).toHaveLength(before.items.length);
  expect(created).toEqual([]);
  const apply = page.getByRole("button", { name: "Применить", exact: true });
  expect(
    (await apply.count()) === 0 || (await apply.isDisabled()),
  ).toBeTruthy();
});

test("leaving unsaved settings requires an explicit discard", async ({
  page,
  appliance,
}) => {
  await login(page, appliance);
  await navigate(page, "Настройки");
  await page.getByRole("tab", { name: "DNS и таймауты" }).click();
  const timeout = page.getByLabel("Подключение к серверу, секунды");
  const original = await timeout.inputValue();
  const changed = String(Number(original) + 1);
  await timeout.fill(changed);
  const overview = page
    .getByRole("navigation")
    .getByRole("button", { name: "Обзор", exact: true });
  await overview.click();
  const confirm = page.getByRole("dialog", { name: "Несохранённые изменения" });
  await expect(confirm).toBeVisible();
  await confirm.getByRole("button", { name: "Остаться", exact: true }).click();
  await expect(timeout).toHaveValue(changed);
  await overview.click();
  await confirm
    .getByRole("button", { name: "Не сохранять", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Обзор", level: 1 }),
  ).toBeVisible();
  await navigate(page, "Настройки");
  await page.getByRole("tab", { name: "DNS и таймауты" }).click();
  await expect(timeout).toHaveValue(original);
});

test("validation polling recovers without submitting the operation again", async ({
  page,
  appliance,
}) => {
  await login(page, appliance);
  await navigate(page, "Клиенты");
  await page
    .getByRole("button", { name: "Добавить клиента", exact: true })
    .click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("Название", { exact: true }).fill("poll-recovery");
  await dialog.getByLabel("IP / CIDR").fill("192.0.2.22/32");
  await dialog.getByLabel("Разрешённые домены").fill("packages.example.test");
  await dialog
    .getByRole("button", { name: "Сохранить черновик", exact: true })
    .click();
  await expect(dialog).toBeHidden();
  await navigate(page, "Изменения");
  const posts: string[] = [];
  let interrupted = false;
  page.on("request", (request) => {
    if (
      request.method() === "POST" &&
      request.url().endsWith("/api/v1/operations")
    )
      posts.push(request.url());
  });
  await page.route(/\/api\/v1\/operations\/[a-zA-Z0-9-]+$/, async (route) => {
    if (!interrupted) {
      interrupted = true;
      await route.abort("failed");
    } else await route.continue();
  });
  await page.getByRole("button", { name: "Проверить", exact: true }).click();
  await expect(
    page.getByText("Эта ревизия прошла проверку", { exact: true }),
  ).toBeVisible({ timeout: 15000 });
  expect(interrupted).toBeTruthy();
  expect(posts).toHaveLength(1);
});
