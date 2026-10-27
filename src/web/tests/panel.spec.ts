import { test, expect, type Page } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { startFixture } from "./runtime-fixture";

let fixture: Awaited<ReturnType<typeof startFixture>>;
let page: Page;
// errors accumulates across the whole serial flow. A pageerror in early
// steps used to invalidate the rest of the giant single-test() body, hiding
// downstream regressions; the serial split lets later steps run and surface
// their own failures instead of being skipped on the first error.
const errors: string[] = [];

test.beforeAll(async ({ browser }) => {
  fixture = await startFixture();
  page = await browser.newPage();
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto(fixture.origin);
  await expect(
    page.getByRole("heading", { name: "Squid Control", exact: true }),
  ).toBeVisible();
  await page.getByLabel("Имя пользователя").fill(fixture.username);
  await page.getByLabel("Пароль", { exact: true }).fill(fixture.password);
  await page.getByRole("button", { name: "Войти в панель" }).click();
  await expect(
    page.getByRole("heading", { name: "Прокси под контролем" }),
  ).toBeVisible();
});

test.afterAll(async () => {
  await page?.close();
  fixture?.cleanup();
});

test.describe.serial("panel e2e flow", () => {
  test("clients: add or update build-fixture, then apply", async () => {
    const existing = page.getByRole("row").filter({
      has: page.getByRole("cell", { name: "build-fixture", exact: true }),
    });
    if (await existing.count())
      await existing.getByRole("button", { name: "Изменить" }).click();
    else
      await page
        .getByRole("button", { name: "Добавить клиента" })
        .first()
        .click();
    await page.getByLabel("Название", { exact: true }).fill("build-fixture");
    await page.getByLabel("IP / CIDR").fill("192.0.2.42/32");
    await page.getByLabel("Режим доступа").selectOption("allowlist");
    await page.getByLabel("Разрешённые домены").fill("packages.example.test");
    await page.getByRole("button", { name: "Сохранить черновик" }).click();
    await expect(
      page.getByRole("cell", { name: "build-fixture", exact: true }),
    ).toBeVisible();
    await page.getByRole("button", { name: "Посмотреть изменения" }).click();
    await page.getByRole("button", { name: "Проверить", exact: true }).click();
    await expect(page.getByText("Эта ревизия прошла проверку")).toBeVisible();
    await page.getByRole("button", { name: "Применить", exact: true }).click();
    await expect(
      page.locator(".operation").getByText("Применено", { exact: true }),
    ).toBeVisible();
  });

  test("settings: change, apply, then rollback to previous revision", async () => {
    await page.getByRole("button", { name: "Обзор", exact: true }).click();
    await page.getByRole("button", { name: "Настройки", exact: true }).click();
    await page.getByRole("tab", { name: "DNS и таймауты" }).click();
    await page.getByLabel("Подключение к серверу, секунды").fill("45");
    await page
      .getByRole("button", { name: "Сохранить настройки в черновик" })
      .click();
    await expect(
      page.getByText("Есть неприменённые изменения", { exact: true }),
    ).toBeVisible();
    await page
      .getByRole("button", { name: "Посмотреть diff", exact: true })
      .first()
      .click();
    await expect(
      page.getByText("Изменены настройки Squid", { exact: true }),
    ).toBeVisible();
    await page.getByRole("button", { name: "Проверить", exact: true }).click();
    await expect(page.getByText("Эта ревизия прошла проверку")).toBeVisible();
    await page.getByRole("button", { name: "Применить", exact: true }).click();
    await expect(
      page.locator(".operation").getByText("Применено", { exact: true }),
    ).toBeVisible();
    await page
      .getByRole("button", { name: "Сравнить и откатить", exact: true })
      .first()
      .click();
    await page
      .getByRole("button", { name: "Откатить к этой ревизии", exact: true })
      .click();
    await expect(
      page.locator(".operation").getByText("Применено", { exact: true }),
    ).toBeVisible();
  });

  test("expert mode apply, responsive screenshots, logout", async () => {
    await page.getByRole("button", { name: "Настройки", exact: true }).click();
    await page.getByRole("tab", { name: "Экспертный режим" }).click();
    await page
      .getByLabel("Режим конфигурации", { exact: true })
      .selectOption("expert");
    await page
      .getByRole("button", { name: "Загрузить визуальные правила" })
      .click();
    await expect(
      page.getByLabel("Экспертная конфигурация", { exact: true }),
    ).not.toHaveValue("");
    await page
      .getByRole("button", { name: "Сохранить настройки в черновик" })
      .click();
    await page
      .getByRole("button", { name: "Посмотреть diff", exact: true })
      .first()
      .click();
    await page.getByRole("button", { name: "Проверить", exact: true }).click();
    await expect(page.getByText("Эта ревизия прошла проверку")).toBeVisible();
    await page.getByRole("button", { name: "Применить", exact: true }).click();
    await expect(
      page.locator(".operation").getByText("Применено", { exact: true }),
    ).toBeVisible();
    await page.getByRole("button", { name: "Обзор", exact: true }).click();
    await page.setViewportSize({ width: 1440, height: 1050 });
    // Use os.tmpdir() so screenshots land in the OS-approved temporary
    // directory (e.g. /var/folders/.../T on macOS) instead of hardcoding /tmp
    // which AGENTS.md treats as a less preferred location.
    const dir = process.env.SC_SCREENSHOT_DIR || join(tmpdir(), "squid-control-ui");
    mkdirSync(dir, { recursive: true });
    await page.screenshot({ path: join(dir, "desktop.png"), fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(
      page.getByRole("heading", { name: "Прокси под контролем" }),
    ).toBeVisible();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBeTruthy();
    await page.screenshot({ path: join(dir, "mobile.png"), fullPage: true });
    expect(errors).toEqual([]);
    await page.getByRole("button", { name: "Выйти", exact: true }).click();
    await expect(
      page.getByRole("button", { name: "Войти в панель" }),
    ).toBeVisible();
  });
});


test("expired GET and mutation sessions can log in again without reload", async () => {
  async function signIn() {
    await page.getByLabel("Имя пользователя").fill(fixture.username);
    await page.getByLabel("Пароль", { exact: true }).fill(fixture.password);
    await page.getByRole("button", { name: "Войти в панель" }).click();
    await expect(page.getByRole("button", { name: "Выйти", exact: true })).toBeVisible();
  }
  await signIn();
  await page.context().clearCookies();
  await expect(page.getByRole("button", { name: "Войти в панель" })).toBeVisible({ timeout: 15000 });
  await signIn();
  await page.getByRole("button", { name: "Настройки", exact: true }).click();
  await page.getByRole("tab", { name: "DNS и таймауты" }).click();
  await page.getByLabel("Подключение к серверу, секунды").fill("46");
  await page.context().clearCookies();
  await page.getByRole("button", { name: "Сохранить настройки в черновик" }).click();
  await expect(page.getByRole("button", { name: "Войти в панель" })).toBeVisible();
  // A wrong password must retain the refreshed anonymous CSRF holder so the
  // next attempt can succeed on the same page.
  await page.getByLabel("Имя пользователя").fill(fixture.username);
  await page.getByLabel("Пароль", { exact: true }).fill("wrong-test-password");
  await page.getByRole("button", { name: "Войти в панель" }).click();
  await expect(page.getByText("Неверное имя или пароль")).toBeVisible();
  await signIn();
  expect(errors).toEqual([]);
});
