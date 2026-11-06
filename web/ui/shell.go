package ui

import (
	"bytes"
	"encoding/json"
	"html/template"
)

type shellData struct {
	BootstrapJSON  template.JS
	HeadCustomCode template.HTML
	Assets         ManagementShellAssets
}

type ManagementShellMetadata struct {
	BaseURL        string
	HeadCustomCode string
}

func RenderManagementShell(bootstrap interface{}, metadata ManagementShellMetadata) (string, error) {
	return RenderManagementShellWithAssets(bootstrap, metadata, DefaultManagementShellAssets(metadata.BaseURL))
}

func RenderManagementShellWithAssets(bootstrap interface{}, metadata ManagementShellMetadata, assets ManagementShellAssets) (string, error) {
	payload, err := marshalShellBootstrap(bootstrap)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := managementShellTemplate.Execute(&out, shellData{
		BootstrapJSON:  template.JS(payload),
		HeadCustomCode: template.HTML(metadata.HeadCustomCode),
		Assets:         assets.Clone(),
	}); err != nil {
		return "", err
	}
	return out.String(), nil
}

func marshalShellBootstrap(bootstrap interface{}) (string, error) {
	if bootstrap == nil {
		bootstrap = struct{}{}
	}
	raw, err := json.Marshal(bootstrap)
	if err != nil {
		return "", err
	}
	var escaped bytes.Buffer
	json.HTMLEscape(&escaped, raw)
	return escaped.String(), nil
}

var managementShellTemplate = template.Must(template.New("management-shell").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>NPS Management</title>
  {{.HeadCustomCode}}
  {{range .Assets.Styles}}<link rel="stylesheet" href="{{.}}">
  {{end}}
  <style>
    :root { color-scheme: light; --bg:#f5f7fb; --card:#ffffff; --text:#1f2937; --muted:#6b7280; --line:#d8e1ee; --accent:#0f766e; }
    * { box-sizing: border-box; }
    body { margin:0; font-family: "Segoe UI", "PingFang SC", sans-serif; background:linear-gradient(180deg,#eef4ff 0%,var(--bg) 35%,#eef7f4 100%); color:var(--text); }
    a { color:inherit; text-decoration:none; }
    .wrap { max-width:1100px; margin:0 auto; padding:32px 20px 48px; }
    .hero { display:flex; gap:16px; align-items:flex-start; justify-content:space-between; margin-bottom:24px; }
    .hero-card, .panel { background:rgba(255,255,255,.88); backdrop-filter: blur(12px); border:1px solid var(--line); border-radius:20px; box-shadow:0 18px 50px rgba(15,23,42,.08); }
    .hero-card { padding:24px; flex:1; }
    .hero-title { margin:0 0 6px; font-size:30px; line-height:1.1; }
    .hero-sub { margin:0; color:var(--muted); font-size:14px; }
    .hero-meta { display:flex; gap:12px; flex-wrap:wrap; margin-top:16px; }
    .chip { display:inline-flex; align-items:center; padding:6px 10px; border-radius:999px; background:#eff6ff; color:#1d4ed8; font-size:12px; }
    .grid { display:grid; gap:16px; grid-template-columns: 1.5fr 1fr; }
    .panel { padding:20px; }
    .panel h2 { margin:0 0 12px; font-size:16px; }
    .links { display:grid; gap:10px; }
    .link { display:flex; justify-content:space-between; align-items:center; padding:12px 14px; border:1px solid var(--line); border-radius:14px; background:#fff; }
    .link small { color:var(--muted); }
    .meta-list { display:grid; gap:10px; }
    .meta-row { display:flex; justify-content:space-between; gap:10px; font-size:13px; }
    .meta-row span:last-child { color:var(--muted); text-align:right; }
    .footer { margin-top:18px; color:var(--muted); font-size:12px; }
    .shell-loading { max-width:1100px; margin:0 auto; padding:48px 20px; color:var(--muted); font-size:14px; }
    @media (max-width: 820px) { .hero { flex-direction:column; } .grid { grid-template-columns:1fr; } .hero-title { font-size:24px; } }
  </style>
</head>
<body{{if .Assets.Ready}} data-nps-shell="external"{{else}} data-nps-shell="fallback"{{end}}>
  {{if .Assets.Ready}}
  <div id="nps-app-root"><div class="shell-loading">Loading management app...</div></div>
  {{else}}
  <div id="nps-app-root"></div>
  {{end}}
  <script id="nps-bootstrap" type="application/json">{{.BootstrapJSON}}</script>
  <script>
  (function () {
    function byId(id) { return document.getElementById(id); }
    var payload = {};
    try {
      payload = JSON.parse(byId("nps-bootstrap").textContent || "{}");
    } catch (e) {
      payload = {};
    }
    window.__NPS_BOOTSTRAP__ = payload;
  })();
  </script>
  {{if not .Assets.Ready}}
  <script>
  (function () {
    function byId(id) { return document.getElementById(id); }
    function text(value, fallback) { return value === undefined || value === null || value === "" ? (fallback || "") : String(value); }
    function clear(node) { while (node.firstChild) { node.removeChild(node.firstChild); } }
    function el(tag, className, textValue) {
      var node = document.createElement(tag);
      if (className) node.className = className;
      if (textValue !== undefined) node.textContent = text(textValue);
      return node;
    }
    function append(parent) {
      for (var i = 1; i < arguments.length; i++) {
        if (arguments[i]) parent.appendChild(arguments[i]);
      }
      return parent;
    }
    var payload = window.__NPS_BOOTSTRAP__ || {};
    var root = byId("nps-app-root");
    root.className = "wrap";
    clear(root);

    var app = payload.app || {};
    var session = payload.session || {};
    var routes = payload.routes || {};
    var ui = payload.ui || {};
    var pages = Array.isArray(payload.pages) ? payload.pages.filter(function (page) { return page && page.navigation; }) : [];
    var actions = Array.isArray(payload.actions) ? payload.actions : [];

    document.title = text(app.name, "NPS") + " Management";

    var hero = el("section", "hero");
    var heroCard = el("div", "hero-card");
    append(heroCard,
      el("h1", "hero-title", text(app.name, "NPS") + " Management"),
      el("p", "hero-sub", "Server-hosted management shell. Current pages remain available while the frontend transitions to React."),
      append(el("div", "hero-meta"),
        el("span", "chip", "mode: " + text(ui.mode, "hybrid")),
        el("span", "chip", "user: " + text(session.username, session.is_admin ? "admin" : "anonymous")),
        el("span", "chip", "api: " + text(routes.api_base, "/api/v1"))
      )
    );
    hero.appendChild(heroCard);
    root.appendChild(hero);

    var grid = el("section", "grid");
    var navPanel = el("div", "panel");
    navPanel.appendChild(el("h2", "", "Available Management Pages"));
    var links = el("div", "links");
    if (pages.length === 0) {
      links.appendChild(el("div", "link", "No visible pages for the current session."));
    } else {
      pages.forEach(function (page) {
        var link = el("a", "link");
        link.href = text(page.direct_path, routes.login || "/login/index");
        append(link,
          append(el("div", ""), el("strong", "", text(page.menu, page.action)), el("small", "", text(page.direct_path))),
          el("span", "", text(page.section, "page"))
        );
        links.appendChild(link);
      });
    }
    navPanel.appendChild(links);

    var metaPanel = el("div", "panel");
    metaPanel.appendChild(el("h2", "", "Runtime Contract"));
    var meta = el("div", "meta-list");
    append(meta,
      append(el("div", "meta-row"), el("span", "", "Version"), el("span", "", text(app.version, "-"))),
      append(el("div", "meta-row"), el("span", "", "Shell"), el("span", "", text(routes.app_shell, "/app"))),
      append(el("div", "meta-row"), el("span", "", "Pages"), el("span", "", String(pages.length))),
      append(el("div", "meta-row"), el("span", "", "Actions"), el("span", "", String(actions.length))),
      append(el("div", "meta-row"), el("span", "", "Legacy templates"), el("span", "", ui.legacy_pages_enabled ? "enabled" : "disabled")),
      append(el("div", "meta-row"), el("span", "", "React handoff"), el("span", "", ui.react_bootstrap_ready ? "ready" : "pending"))
    );
    metaPanel.appendChild(meta);
    if (routes.logout) {
      var footer = el("div", "footer");
      var logout = el("a", "", "Logout");
      logout.href = text(routes.logout, "/login/out");
      footer.appendChild(logout);
      metaPanel.appendChild(footer);
    }

    append(grid, navPanel, metaPanel);
    root.appendChild(grid);
  })();
  </script>
  {{end}}
  {{range .Assets.Scripts}}<script defer src="{{.}}"></script>
  {{end}}
</body>
</html>`))
