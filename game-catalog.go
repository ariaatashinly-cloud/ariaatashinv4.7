package main

import "sort"

// Publisher/service suffixes are starting points for DNS/login routing, not
// an exhaustive list of regional match-server IPs or a claim of Iran sanctions.
type GamePreset struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Domains   []string `json:"domains"`
	Source    string   `json:"source"`
	Platforms []string `json:"platforms,omitempty"`
	Packages  []string `json:"packages,omitempty"`
	Processes []string `json:"processes,omitempty"`
	Note      string   `json:"note,omitempty"`
}

var gamePresets = []GamePreset{
	{ID: "clash-of-clans", Name: "Clash of Clans · کلش آو کلنز", Domains: []string{"clashofclans.com", "supercell.com", "supercellgames.com", "supercellid.com"}, Source: "https://play.google.com/store/apps/details?id=com.supercell.clashofclans", Platforms: []string{"android", "ios"}, Packages: []string{"com.supercell.clashofclans"}, Note: "برای Android حالت فقط برنامه، IPهای مستقیم بازی را هم عبور می‌دهد. Smart DNS به‌تنهایی اتصال مسابقه/سرور بازی را پوشش نمی‌دهد."},
	{ID: "rainbow-six", Name: "Rainbow Six Siege · رینبو سیکس", Domains: []string{"ubisoft.com", "ubisoftconnect.com", "ubi.com"}, Source: "https://www.ubisoft.com/en-gb/help/rainbow-six-siege/connectivity-and-performance/article/troubleshooting-connectivity-issues-in-rainbow-six-siege-pc/000078839", Platforms: []string{"windows", "console"}, Note: "برای PC نام exe واقعی نصب خودت را در خروجی فقط برنامه وارد کن؛ برای کنسول یا نام نامشخص، تونل کامل را انتخاب کن."},
	{ID: "call-of-duty", Name: "Call of Duty / Warzone · کالاف", Domains: []string{"callofduty.com", "activision.com", "demonware.net", "battle.net", "blizzard.com"}, Source: "https://support.activision.com/articles/ports-used-for-call-of-duty-games", Platforms: []string{"windows", "console"}, Note: "نسخه‌های PC/کنسول؛ ترافیک TCP و UDP و NAT مهم است. نام exe بین نسخه‌ها فرق می‌کند؛ تونل کامل بدون فهرست IP کار می‌کند."},
	{ID: "cod-mobile", Name: "Call of Duty Mobile · کالاف موبایل", Domains: []string{"callofduty.com", "activision.com", "demonware.net"}, Source: "https://play.google.com/store/apps/details?id=com.activision.callofduty.shooter", Platforms: []string{"android", "ios"}, Packages: []string{"com.activision.callofduty.shooter"}, Note: "بستهٔ Android نسخهٔ جهانی است؛ نسخهٔ منطقه‌ای/Garena به package واقعی خودش نیاز دارد. UDP بازی به تونل نیاز دارد."},
	{ID: "battlefield", Name: "Battlefield · بتلفیلد", Domains: []string{"ea.com", "easports.com", "origin.com", "battlefield.com"}, Source: "https://help.ea.com/en/games/battlefield/battlefield-6/", Platforms: []string{"windows", "console"}, Note: "دامنه‌های EA میان چند بازی مشترک‌اند؛ برای جداسازی دقیق روی PC، exe نسخهٔ نصب‌شده را وارد کن. DNS تنها همهٔ سرورهای مسابقه را عبور نمی‌دهد."},
	{ID: "marvel-rivals", Name: "Marvel Rivals · مارول رایولز", Domains: []string{"marvelrivals.com", "loadingbay.com", "easebar.com"}, Source: "https://www.marvelrivals.com/guide/20241201/41348_1197223.html", Platforms: []string{"windows", "console"}, Processes: []string{"Marvel-Win64-Shipping.exe", "Marvel.exe"}, Note: "نام‌های Windows از راهنمای رسمی بازی هستند؛ در نسخه/لانچر متفاوت، نام واقعی فرایند را بررسی کن. برای IP مستقیم مسابقه، برنامه یا تونل کامل را انتخاب کن."},
	{ID: "steam", Name: "Steam · فروشگاه و دانلود", Domains: []string{"steampowered.com", "steamcommunity.com", "steamgames.com", "steamusercontent.com", "steamcontent.com", "steamstatic.com"}, Source: "https://help.steampowered.com/en/faqs/view/2EA8-4D75-DA21-31EB", Platforms: []string{"windows", "linux", "macos"}, Note: "دامنه‌های فروشگاه/دانلود، همهٔ بازی‌های Steam و IPهای مسابقهٔ آن‌ها را پوشش نمی‌دهند."},
	{ID: "epic", Name: "Epic · لانچر و ورود", Domains: []string{"epicgames.com", "unrealengine.com"}, Source: "https://www.epicgames.com/help/c-36624475/c-35761596/which-domains-need-to-be-whitelisted-to-reach-the-epic-servers-a15422130?lang=en-US", Platforms: []string{"windows", "macos"}, Note: "برای بازی‌های دیگر Epic از تونل کامل یا نام برنامهٔ واقعی استفاده کن."},
}

func gamePreset(id string) (GamePreset, bool) {
	for _, p := range gamePresets {
		if p.ID == id {
			return p, true
		}
	}
	return GamePreset{}, false
}
func gamePolicy(c DNSClient, id string) string {
	if c.GameRoutes[id] == "direct" {
		return "direct"
	}
	return "proxy"
}
func smartGameDomains(c DNSClient) []string { proxy, _, _, _ := gameGroups(c); return proxy }
func uniqueGameValues(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
func gameGroups(c DNSClient) (proxyDomains, directDomains, packages, processes []string) {
	proxyDomains = append([]string{}, c.Domains...)
	for _, id := range c.Presets {
		p, ok := gamePreset(id)
		if !ok {
			continue
		}
		if gamePolicy(c, id) == "direct" {
			directDomains = append(directDomains, p.Domains...)
		} else {
			proxyDomains = append(proxyDomains, p.Domains...)
			packages = append(packages, p.Packages...)
			processes = append(processes, p.Processes...)
		}
	}
	proxyDomains = uniqueGameValues(proxyDomains)
	directDomains = uniqueGameValues(directDomains)
	// Protect shared publisher domains when two games have different policies.
	filtered := []string{}
	for _, d := range directDomains {
		overlap := false
		for _, p := range proxyDomains {
			if gameDomainMatches(d, []string{p}) || gameDomainMatches(p, []string{d}) {
				overlap = true
				break
			}
		}
		if !overlap {
			filtered = append(filtered, d)
		}
	}
	return proxyDomains, filtered, uniqueGameValues(packages), uniqueGameValues(processes)
}
