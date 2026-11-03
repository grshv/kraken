package httpapi

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"kraken.local/kraken/internal/policy"
)

// Change details are the review sheet's lines under each change: what exactly
// differs between the applied (a) and the draft (b) snapshot, in the words of
// the UI. Names come from the snapshots and are untrusted display text.

var accessLabels = map[string]string{
	policy.AccessList:   "По списку",
	policy.AccessAllWeb: "Весь веб",
	policy.AccessNone:   "Нет доступа",
}

func accessLabel(access string) string {
	if label, ok := accessLabels[access]; ok {
		return label
	}
	return access
}

func destinationLabel(d policy.Destination) string {
	if d.IncludeSubdomains {
		return "*." + d.Domain
	}
	return d.Domain
}

func destinationLabels(in []policy.Destination) []string {
	out := make([]string, 0, len(in))
	for _, d := range in {
		out = append(out, destinationLabel(d))
	}
	return out
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// changedLine reports "label: before → after" when the values differ.
func changedLine(label, before, after string) []string {
	if before == after {
		return nil
	}
	return []string{label + ": " + orDash(before) + " → " + orDash(after)}
}

func onOff(v bool) string {
	if v {
		return "Включено"
	}
	return "Выключено"
}

func joinInts(in []int) string {
	out := make([]string, 0, len(in))
	for _, n := range in {
		out = append(out, strconv.Itoa(n))
	}
	return strings.Join(out, ", ")
}

// listLines reports added and removed members of a set-like list.
func listLines(label string, before, after []string) []string {
	var added, removed []string
	for _, v := range after {
		if !slices.Contains(before, v) {
			added = append(added, v)
		}
	}
	for _, v := range before {
		if !slices.Contains(after, v) {
			removed = append(removed, v)
		}
	}
	var out []string
	if len(added) > 0 {
		out = append(out, label+": добавлено "+strings.Join(added, ", "))
	}
	if len(removed) > 0 {
		out = append(out, label+": удалено "+strings.Join(removed, ", "))
	}
	return out
}

func transportLabel(s policy.Snapshot, profileID string) string {
	for _, p := range s.TLSProfiles {
		if p.ID == profileID && p.Transport == "tls" {
			return "TLS"
		}
	}
	return "HTTP"
}

func policyNames(s policy.Snapshot, ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		name := id
		for _, p := range s.Policies {
			if p.ID == id {
				name = p.Name
				break
			}
		}
		out = append(out, name)
	}
	return out
}

func clientDetails(before *policy.Client, after policy.Client, a, b policy.Snapshot) []string {
	var prev policy.Client
	if before != nil {
		prev = *before
	}
	access, transport := "—", "—"
	if before != nil {
		access, transport = accessLabel(prev.Access), transportLabel(a, prev.TLSProfileID)
	}
	out := []string{}
	out = append(out, changedLine("Имя", prev.Name, after.Name)...)
	out = append(out, changedLine("Описание", prev.Description, after.Description)...)
	if before == nil || prev.Enabled != after.Enabled {
		was := "—"
		if before != nil {
			was = onOff(prev.Enabled)
		}
		out = append(out, changedLine("Клиент включён", was, onOff(after.Enabled))...)
	}
	out = append(out, changedLine("Доступ", access, accessLabel(after.Access))...)
	// The widest mode gets its own line so it is not lost among the details.
	if after.Access == policy.AccessAllWeb && (before == nil || prev.Access != policy.AccessAllWeb) {
		out = append(out, "Открывает весь веб: любые внешние сайты, кроме запрещённых для всех")
	}
	out = append(out, changedLine("Подключение", transport, transportLabel(b, after.TLSProfileID))...)
	out = append(out, changedLine("Логин прокси", prev.ProxyUsername, after.ProxyUsername)...)
	out = append(out, listLines("Источники", prev.Networks, after.Networks)...)
	out = append(out, listLines("Политики", policyNames(a, prev.PolicyIDs), policyNames(b, after.PolicyIDs))...)
	out = append(out, listLines("Свои домены", destinationLabels(prev.Destinations), destinationLabels(after.Destinations))...)
	return out
}

func removedClientDetails(c policy.Client, a policy.Snapshot) []string {
	return []string{
		"Источники: " + orDash(strings.Join(c.Networks, ", ")),
		"Доступ: " + accessLabel(c.Access),
		"Подключение: " + transportLabel(a, c.TLSProfileID),
		"Политики: " + orDash(strings.Join(policyNames(a, c.PolicyIDs), ", ")),
		"Свои домены: " + orDash(strings.Join(destinationLabels(c.Destinations), ", ")),
	}
}

func policyDetails(before *policy.Policy, after policy.Policy) []string {
	var prev policy.Policy
	if before != nil {
		prev = *before
	}
	out := []string{}
	if before != nil {
		out = append(out, changedLine("Имя", prev.Name, after.Name)...)
	}
	return append(out, listLines("Домены", destinationLabels(prev.Destinations), destinationLabels(after.Destinations))...)
}

func removedPolicyDetails(p policy.Policy, a policy.Snapshot) []string {
	var users []string
	for _, c := range a.Clients {
		if slices.Contains(c.PolicyIDs, p.ID) {
			users = append(users, c.Name)
		}
	}
	return []string{
		"Домены: " + orDash(strings.Join(destinationLabels(p.Destinations), ", ")),
		"Подключена у: " + orDash(strings.Join(users, ", ")),
	}
}

func upstreamLabel(u *policy.UpstreamProxy) string {
	if u == nil {
		return "Выключен"
	}
	out := u.Address + ":" + strconv.Itoa(u.Port)
	if u.Exclusive {
		out += " · без прямого подключения"
	}
	return out
}

// settingsDetails lists changed Squid settings and the global deny list.
func settingsDetails(a, b policy.Settings) []string {
	n := func(label string, x, y int) []string { return changedLine(label, strconv.Itoa(x), strconv.Itoa(y)) }
	out := []string{}
	out = append(out, n("Порт HTTP", a.HTTPPort, b.HTTPPort)...)
	out = append(out, n("Порт TLS", a.TLSPort, b.TLSPort)...)
	out = append(out, changedLine("Порты сайтов HTTP", joinInts(a.SafePorts), joinInts(b.SafePorts))...)
	out = append(out, changedLine("Порты HTTPS", joinInts(a.ConnectPorts), joinInts(b.ConnectPorts))...)
	out = append(out, changedLine("Кеш в памяти", onOff(a.CacheEnabled), onOff(b.CacheEnabled))...)
	out = append(out, n("Объём кеша, МБ", a.CacheMemoryMB, b.CacheMemoryMB)...)
	out = append(out, n("Размер объекта, КБ", a.MaximumObjectKB, b.MaximumObjectKB)...)
	out = append(out, changedLine("DNS-серверы", strings.Join(a.DNSNameservers, ", "), strings.Join(b.DNSNameservers, ", "))...)
	out = append(out, n("TTL DNS, с", a.PositiveDNSTTL, b.PositiveDNSTTL)...)
	out = append(out, n("TTL отрицательных ответов, с", a.NegativeDNSTTL, b.NegativeDNSTTL)...)
	out = append(out, n("Ожидание подключения, с", a.ConnectTimeout, b.ConnectTimeout)...)
	out = append(out, n("Ожидание чтения, с", a.ReadTimeout, b.ReadTimeout)...)
	out = append(out, n("Время запроса, с", a.RequestTimeout, b.RequestTimeout)...)
	out = append(out, n("Тело запроса, МБ", a.MaxRequestBodyMB, b.MaxRequestBodyMB)...)
	out = append(out, n("Ротации access.log", a.LogRotations, b.LogRotations)...)
	out = append(out, changedLine("Доступ к внутренним адресам", onOff(a.AllowPrivateDestinations), onOff(b.AllowPrivateDestinations))...)
	out = append(out, listLines("Запрещено для всех", destinationLabels(a.BlockedDomains), destinationLabels(b.BlockedDomains))...)
	out = append(out, changedLine("Скрывать адрес клиента", onOff(a.HideClientAddress), onOff(b.HideClientAddress))...)
	return append(out, changedLine("Вышестоящий прокси", upstreamLabel(a.Upstream), upstreamLabel(b.Upstream))...)
}

// settingsText names the global deny list apart: it is an access rule, not a
// Squid setting.
func settingsText(a, b policy.Settings) string {
	blocked := !sameJSON(a.BlockedDomains, b.BlockedDomains)
	a.BlockedDomains, b.BlockedDomains = nil, nil
	rest := !sameJSON(a, b)
	switch {
	case blocked && !rest:
		return "Изменено правило «Запрещено для всех»"
	case blocked:
		return "Изменены «Запрещено для всех» и настройки Squid"
	default:
		return "Изменены настройки Squid"
	}
}

func configurationDetails(a, b policy.Snapshot) []string {
	mode := func(m string) string {
		if m == "expert" {
			return "Вручную"
		}
		return "Визуальные настройки"
	}
	out := changedLine("Режим", mode(a.Mode), mode(b.Mode))
	if a.Expert != b.Expert {
		out = append(out, "Изменён текст squid.conf")
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func tlsProfileDetails(a, b policy.Snapshot) []string {
	transport := func(t string) string {
		switch t {
		case "tls":
			return "TLS"
		case "http":
			return "HTTP"
		}
		return t
	}
	mode := func(m string) string {
		if m == "tunnel" {
			return "Сквозной CONNECT"
		}
		return m
	}
	cert := func(ref *string) string {
		if ref == nil || *ref == "" {
			return "не задан"
		}
		return *ref
	}
	context := func(p policy.TLSProfile) string {
		return fmt.Sprintf("подключение %s, режим %s, сертификат %s", transport(p.Transport), mode(p.Mode), cert(p.CertRef))
	}
	out := []string{}
	for _, p := range b.TLSProfiles {
		i := slices.IndexFunc(a.TLSProfiles, func(x policy.TLSProfile) bool { return x.ID == p.ID })
		if i < 0 {
			out = append(out, fmt.Sprintf("Добавлен профиль «%s»: %s", p.Name, context(p)))
			continue
		}
		prev := a.TLSProfiles[i]
		var lines []string
		lines = append(lines, changedLine("Имя", prev.Name, p.Name)...)
		lines = append(lines, changedLine("Подключение", transport(prev.Transport), transport(p.Transport))...)
		lines = append(lines, changedLine("Режим", mode(prev.Mode), mode(p.Mode))...)
		lines = append(lines, changedLine("Сертификат", cert(prev.CertRef), cert(p.CertRef))...)
		for _, line := range lines {
			out = append(out, fmt.Sprintf("Профиль «%s»: %s", prev.Name, line))
		}
	}
	for _, p := range a.TLSProfiles {
		if !slices.ContainsFunc(b.TLSProfiles, func(x policy.TLSProfile) bool { return x.ID == p.ID }) {
			out = append(out, fmt.Sprintf("Удалён профиль «%s»: %s", p.Name, context(p)))
		}
	}
	return out
}

func sameJSON(a, b any) bool {
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(aa) == string(bb)
}
