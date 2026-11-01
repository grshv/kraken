import {expect, it} from 'vitest';
import {renderToStaticMarkup} from 'react-dom/server';
import {ResizablePanelGroup} from './components/ui/resizable';
import {NewAccountForm} from './reference/accounts/AccountsSection';
import {DomainEditor} from './reference/policies/DomainEditor';
import {BlockedDomainEditor} from './reference/settings/BlockedDomainEditor';

it('открывает форму пользователя с доступной группой ролей без ошибки Radix provider', () => {
  const html = renderToStaticMarkup(<ResizablePanelGroup orientation="horizontal"><NewAccountForm kind="panel" onCancel={() => {}} onCreate={async () => true} error={null}/></ResizablePanelGroup>);
  expect(html).toContain('Новый пользователь');
  expect(html).toMatch(/role="radiogroup"[^>]*aria-label="Роль пользователя"/);
  expect(html).toMatch(/role="radio"[^>]*aria-checked="true"[^>]*value="manager"/);
  expect(html).toContain('Администратор');
});

it('показывает форму логина прокси без выбора роли панели', () => {
  const html = renderToStaticMarkup(<ResizablePanelGroup orientation="horizontal"><NewAccountForm kind="proxy" onCancel={() => {}} onCreate={async () => true} error={null}/></ResizablePanelGroup>);
  expect(html).toContain('Новый логин прокси');
  expect(html).not.toContain('role="radiogroup"');
});

it('даёт полям доменов устойчивые имена и скрывает редактирование read-only политики', () => {
  const allowed = renderToStaticMarkup(<DomainEditor destinations={[]} onChange={() => {}}/>);
  const blocked = renderToStaticMarkup(<BlockedDomainEditor destinations={[]} onChange={() => {}}/>);
  const readonly = renderToStaticMarkup(<DomainEditor destinations={[]} readOnly onChange={() => {}}/>);
  expect(allowed).toContain('aria-label="Добавить домены"');
  expect(blocked).toContain('aria-label="Добавить запрещённые домены"');
  expect(readonly).not.toContain('aria-label="Добавить домены"');
});
