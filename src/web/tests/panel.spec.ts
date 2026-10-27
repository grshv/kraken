import { test, expect } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { startFixture } from "./runtime-fixture";
let fixture: Awaited<ReturnType<typeof startFixture>>;
test.beforeAll(async () => {
  fixture = await startFixture();
});
test.afterAll(() => fixture?.cleanup());
test("clients, settings, expert config, rollback and responsive panel", async ({
  page,
}) => {
  const errors: string[] = [];
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
  const dir = process.env.SC_SCREENSHOT_DIR || "/tmp/squid-control-ui";
  mkdirSync(dir, { recursive: true });
  await page.screenshot({ path: dir + "/desktop.png", fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(
    page.getByRole("heading", { name: "Прокси под контролем" }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBeTruthy();
  await page.screenshot({ path: dir + "/mobile.png", fullPage: true });
  expect(errors).toEqual([]);
  await page.getByRole("button", { name: "Выйти", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Войти в панель" }),
  ).toBeVisible();
});
