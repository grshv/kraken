import { useEffect, useState } from "react";
import { BookOpen, Search } from "lucide-react";
import { Input } from "@/components/ui/input";
import { EmptyState } from "../shell/EmptyState";
import { renderMarkdown } from "./markdown";
import type { HelpEntry, HelpArticle } from "../model";
import type { Request } from "../api";

const SECTION_ORDER = [
  "Начало работы",
  "Подключение машин",
  "Сценарии",
  "Как это работает",
];

// Help section: TOC on the left, article on the right. The index comes from
// GET /api/v1/help, one article from GET /api/v1/help/{slug}. help:slug links
// inside the markdown navigate without leaving the section.
export function HelpView({ request }: { request: Request }) {
  const [index, setIndex] = useState<HelpEntry[] | null>(null);
  const [article, setArticle] = useState<HelpArticle | null>(null);
  const [slug, setSlug] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");

  useEffect(() => {
    let active = true;
    request<{ articles: HelpEntry[] }>("/help")
      .then((r) => {
        if (!active) return;
        setIndex(r.articles);
        const first = r.articles[0]?.slug;
        if (first && slug === null) setSlug(first);
      })
      .catch((e) => {
        if (!active) return;
        setError((e as Error).message);
      });
    return () => {
      active = false;
    };
  }, [request]);

  useEffect(() => {
    if (!slug) return;
    let active = true;
    setArticle(null);
    setError("");
    request<HelpArticle>(`/help/${slug}`)
      .then((a) => {
        if (active) setArticle(a);
      })
      .catch((e) => {
        if (active) setError((e as Error).message);
      });
    return () => {
      active = false;
    };
  }, [request, slug]);

  const filtered = (index ?? []).filter(
    (e) =>
      !query ||
      e.title.toLowerCase().includes(query.toLowerCase()) ||
      e.section.toLowerCase().includes(query.toLowerCase()),
  );
  const grouped = SECTION_ORDER.map((section) => ({
    section,
    items: filtered.filter((e) => e.section === section),
  })).filter((g) => g.items.length > 0);

  return (
    <div className="help-view">
      <aside className="list-pane">
        <div className="list-head">
          <span className="list-title">Справка</span>
          <div className="search-input">
            <Search aria-hidden />
            <Input
              aria-label="Поиск по справке"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Поиск…"
            />
          </div>
        </div>
        <div className="list-body">
          {grouped.length === 0 && (
            <div className="list-group-label">
              {index ? "Ничего не найдено" : "Загрузка…"}
            </div>
          )}
          {grouped.map((g) => (
            <div key={g.section}>
              <div className="list-group-label">{g.section}</div>
              {g.items.map((e) => (
                <button
                  key={e.slug}
                  type="button"
                  className={"list-item" + (slug === e.slug ? " selected" : "")}
                  onClick={() => setSlug(e.slug)}
                >
                  <span className="ellipsis">{e.title}</span>
                </button>
              ))}
            </div>
          ))}
        </div>
      </aside>
      <section className="inspector-pane">
        {error ? (
          <EmptyState icon={<BookOpen />} title="Не удалось загрузить статью">
            {error}
          </EmptyState>
        ) : !article ? (
          <EmptyState icon={<BookOpen />} title="Загрузка…" />
        ) : (
          <article className="help-article">
            <header className="inspector-head">
              <div className="muted-text">{article.section}</div>
              <h1 className="page-title">{article.title}</h1>
            </header>
            <div className="inspector-body">
              {renderMarkdown(article.markdown, (s) => setSlug(s))}
            </div>
          </article>
        )}
      </section>
    </div>
  );
}
