import { useEffect, useState } from "react";
import { BookOpen, Search } from "lucide-react";
import { Input } from "@/components/ui/input";
import {
  ListPane,
  ListPaneHeader,
  ListPaneBody,
  ListGroupLabel,
  ListRow,
  InspectorPane,
  InspectorHeader,
  InspectorBody,
} from "@/components/kraken/layout";
import { EmptyState } from "../shell/EmptyState";
import {
  renderInline,
  renderMarkdown,
  sectionID,
  splitArticle,
} from "./markdown";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
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
    <div className="flex w-full min-h-0">
      <ListPane>
        <ListPaneHeader title="Справка">
          <div className="relative flex items-center">
            <Search
              aria-hidden
              className="pointer-events-none absolute left-2.5 size-3.5 text-muted-foreground"
            />
            <Input
              aria-label="Поиск по справке"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Поиск…"
              className="pl-[30px]"
            />
          </div>
        </ListPaneHeader>
        <ListPaneBody>
          {grouped.length === 0 && (
            <ListGroupLabel>
              {index ? "Ничего не найдено" : "Загрузка…"}
            </ListGroupLabel>
          )}
          {grouped.map((g) => (
            <div key={g.section}>
              <ListGroupLabel>{g.section}</ListGroupLabel>
              {g.items.map((e) => (
                <ListRow
                  key={e.slug}
                  selected={slug === e.slug}
                  onClick={() => setSlug(e.slug)}
                  title={e.title}
                />
              ))}
            </div>
          ))}
        </ListPaneBody>
      </ListPane>
      <InspectorPane>
        {error ? (
          <EmptyState icon={<BookOpen />} title="Не удалось загрузить статью">
            {error}
          </EmptyState>
        ) : !article ? (
          <EmptyState icon={<BookOpen />} title="Загрузка…" />
        ) : (
          <ArticleView article={article} onHelpLink={setSlug} />
        )}
      </InspectorPane>
    </div>
  );
}

// ArticleView: section crumb, title and lead in the header, a numbered table
// of contents when the article has several sections, then the body in a
// readable column.
function ArticleView({
  article,
  onHelpLink,
}: {
  article: { section: string; title: string; markdown: string };
  onHelpLink: (slug: string) => void;
}) {
  const { lead, body, outline } = splitArticle(article.markdown);
  return (
    <article className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="border-b">
        <InspectorHeader
          crumbs={
            <span className="text-sm text-muted-foreground">
              {article.section}
            </span>
          }
          title={article.title}
          meta={lead ? renderInline(lead, onHelpLink) : undefined}
        />
      </div>
      <InspectorBody>
        {outline.length > 1 && (
          <Card>
            <CardHeader>
              <CardTitle>Содержание</CardTitle>
            </CardHeader>
            <CardContent>
              <ol className="m-0 flex list-none flex-col gap-1 p-0">
                {outline.map((h, i) => (
                  <li key={h} className="grid grid-cols-[24px_minmax(0,1fr)]">
                    <span className="text-muted-foreground tabular-nums">
                      {i + 1}
                    </span>
                    <button
                      type="button"
                      className="cursor-pointer text-left hover:underline"
                      onClick={() =>
                        document
                          .getElementById(sectionID(i))
                          ?.scrollIntoView({ behavior: "smooth" })
                      }
                    >
                      {h}
                    </button>
                  </li>
                ))}
              </ol>
            </CardContent>
          </Card>
        )}
        <div className="max-w-[720px] leading-[1.6] [&>h2]:mt-5 [&>h2]:mb-2 [&>h3]:mt-5 [&>h3]:mb-2 [&>h4]:mt-4 [&>h4]:mb-1.5">
          {renderMarkdown(body, onHelpLink)}
        </div>
      </InspectorBody>
    </article>
  );
}
