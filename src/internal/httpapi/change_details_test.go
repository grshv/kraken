package httpapi

import (
	"slices"
	"testing"

	"kraken.local/kraken/internal/policy"
)

func detailsOf(t *testing.T, changes []Change, entity, action string) []string {
	t.Helper()
	for _, c := range changes {
		if c.Entity == entity && c.Action == action {
			return c.Details
		}
	}
	t.Fatalf("no %s %s change in %+v", action, entity, changes)
	return nil
}

func TestChangeDetailsClient(t *testing.T) {
	a := baseSnapshot()
	tls := a.TLSProfiles[0].ID
	a.Policies = []policy.Policy{{ID: "p1", Name: "Python", Destinations: []policy.Destination{{Domain: "pypi.org"}}}}
	a.Clients = []policy.Client{
		{ID: "c-a", Name: "alpha", Networks: []string{"10.0.0.1/32"}, Enabled: true, Access: policy.AccessList, TLSProfileID: tls, PolicyIDs: []string{}, Destinations: []policy.Destination{{Domain: "old.example"}}},
		{ID: "c-gone", Name: "gone", Networks: []string{"10.0.0.9/32"}, Enabled: true, Access: policy.AccessList, TLSProfileID: tls, PolicyIDs: []string{"p1"}, Destinations: []policy.Destination{}},
	}
	b := baseSnapshot()
	b.Policies = a.Policies
	b.Clients = []policy.Client{
		{ID: "c-a", Name: "alpha", Networks: []string{"10.0.0.1/32", "10.0.1.0/24"}, Enabled: false, Access: policy.AccessAllWeb, TLSProfileID: tls, PolicyIDs: []string{"p1"}, Destinations: []policy.Destination{{Domain: "new.example", IncludeSubdomains: true}}},
	}
	changes := semanticChanges(a, b)
	got := detailsOf(t, changes, "client", "mod")
	want := []string{
		"Клиент включён: Включено → Выключено",
		"Доступ: По списку → Весь веб",
		"Открывает весь веб: любые внешние сайты, кроме запрещённых для всех",
		"Источники: добавлено 10.0.1.0/24",
		"Политики: добавлено Python",
		"Свои домены: добавлено *.new.example",
		"Свои домены: удалено old.example",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("client details\n got %q\nwant %q", got, want)
	}
	removed := detailsOf(t, changes, "client", "del")
	if !slices.Contains(removed, "Политики: Python") || !slices.Contains(removed, "Свои домены: —") {
		t.Fatalf("removed client details %q", removed)
	}
}

func TestChangeDetailsPolicySettingsAndProfiles(t *testing.T) {
	a := baseSnapshot()
	a.Policies = []policy.Policy{{ID: "p1", Name: "Python", Destinations: []policy.Destination{{Domain: "pypi.org"}}}}
	b := baseSnapshot()
	b.Policies = []policy.Policy{{ID: "p1", Name: "PyPI", Destinations: []policy.Destination{{Domain: "pypi.org"}, {Domain: "files.pythonhosted.org"}}}}
	settings := policy.DefaultSettings()
	settings.BlockedDomains = []policy.Destination{{Domain: "evil.example"}}
	b.Settings = &settings
	changes := semanticChanges(a, b)
	if got := detailsOf(t, changes, "policy", "mod"); !slices.Equal(got, []string{"Имя: Python → PyPI", "Домены: добавлено files.pythonhosted.org"}) {
		t.Fatalf("policy details %q", got)
	}
	// Only the deny list changed: the text names the rule, not Squid settings.
	for _, c := range changes {
		if c.Entity == "settings" && (c.Text != "Изменено правило «Запрещено для всех»" || !slices.Equal(c.Details, []string{"Запрещено для всех: добавлено evil.example"})) {
			t.Fatalf("settings change %+v", c)
		}
	}
	settings.HTTPPort = 8080
	if got := semanticChanges(a, b); !slices.ContainsFunc(got, func(c Change) bool {
		return c.Text == "Изменены «Запрещено для всех» и настройки Squid" && slices.Contains(c.Details, "Порт HTTP: 3128 → 8080")
	}) {
		t.Fatalf("mixed settings change %+v", got)
	}

	c := baseSnapshot()
	c.TLSProfiles = append([]policy.TLSProfile{}, a.TLSProfiles...)
	c.TLSProfiles[0].Name = "renamed"
	if got := detailsOf(t, semanticChanges(a, c), "tls_profiles", "mod"); len(got) != 1 || got[0] != "Профиль «"+a.TLSProfiles[0].Name+"»: Имя: "+a.TLSProfiles[0].Name+" → renamed" {
		t.Fatalf("tls details %q", got)
	}
	e := baseSnapshot()
	e.Mode, e.Expert = "expert", "http_access deny all"
	if got := detailsOf(t, semanticChanges(a, e), "configuration", "mod"); !slices.Equal(got, []string{"Режим: Визуальные настройки → Вручную", "Изменён текст squid.conf"}) {
		t.Fatalf("configuration details %q", got)
	}
}
