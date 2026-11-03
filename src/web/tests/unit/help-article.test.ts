import { describe, it, expect } from "vitest";
import { splitArticle } from "../../src/help/markdown";

describe("splitArticle", () => {
  it("takes the first paragraph as the lead and lists ## headings", () => {
    const md = [
      "Чтобы apt-get ходил через прокси,",
      "достаточно одного файла.",
      "",
      "## Узнать адрес прокси",
      "Текст.",
      "```",
      "## не заголовок",
      "```",
      "## Проверить",
      "### подпункт",
    ].join("\n");
    const a = splitArticle(md);
    expect(a.lead).toBe(
      "Чтобы apt-get ходил через прокси, достаточно одного файла.",
    );
    expect(a.outline).toEqual(["Узнать адрес прокси", "Проверить"]);
    expect(a.body.startsWith("\n## Узнать")).toBe(true);
  });

  it("has no lead when the article starts with a heading or list", () => {
    const a = splitArticle("## Шаг\n- пункт");
    expect(a.lead).toBe("");
    expect(a.body).toBe("## Шаг\n- пункт");
  });
});
