// The start command generator: the one place that turns a saved form and
// the current keys into npc commands, for its page and for list details.
(function (root) {
    'use strict';
    var CARRIERS = ['tcp', 'tls', 'ws', 'wss', 'quic'];
    var SCHEDULERS = ['', 'affinity', 'session_affinity', 'spread_eta', 'spread_round_robin', 'spread_rate_weighted'];
    var PATH_FIELDS = {addr: '', port: '', path: '', alpn: '', sni: '', host: '', http: '', subprotocol: '', tier: '', weight: '', standby: false, count: '', dirs: '', kinds: ''};

    function trim(v) { return v == null ? '' : String(v).trim(); }
    function isWS(c) { return c === 'ws' || c === 'wss'; }
    function usesALPN(c) { return c === 'tls' || c === 'quic'; }
    function proxyType(t) { return t === 'mixed' || t === 'transparent'; }
    function assign(a, b) { for (var k in b) if (Object.prototype.hasOwnProperty.call(b, k)) a[k] = b[k]; return a; }

    // enc percent-encodes a value, leaving what is safe and easier to read.
    function enc(v) {
        return encodeURIComponent(v).replace(/[!'()*]/g, function (c) { return '%' + c.charCodeAt(0).toString(16).toUpperCase(); })
            .replace(/%2C/g, ',').replace(/%2F/g, '/').replace(/%3A/g, ':');
    }
    function encKey(v) { return encodeURIComponent(v).replace(/[!'()*]/g, function (c) { return '%' + c.charCodeAt(0).toString(16).toUpperCase(); }); }

    // carriers reads the server's launch links into {carrier: {host, port, path, alpn}}.
    function carriers(map) {
        var out = {};
        Object.keys(map || {}).forEach(function (typ) {
            var m = /^npc:\/\/@(\[[^\]]+\]|[^:/?]+):(\d+)([^?]*)(?:\?(.*))?$/.exec(map[typ] || '');
            if (!m) return;
            var a = /(?:^|&)alpn=([^&]*)/.exec(m[4] || '');
            out[typ] = {host: m[1].replace(/^\[|\]$/g, ''), port: m[2], path: decodeURI(m[3] || ''), alpn: a ? decodeURIComponent(a[1]) : ''};
        });
        return out;
    }

    function prepare(ctx) {
        if (ctx._def) return ctx;
        ctx._def = carriers(ctx.carriers);
        var m = /type=([a-z]+)/.exec(ctx.primary || '');
        ctx._primary = m && ctx._def[m[1]] ? m[1] : (CARRIERS.filter(function (c) { return ctx._def[c]; })[0] || 'tcp');
        ctx._windows = /\.exe$/.test(ctx.bin || '');
        return ctx;
    }

    function newPath(carrier) { return assign({carrier: carrier}, PATH_FIELDS); }
    function newAccess(ctx) { return {type: 'tcp', ip: '', port: '2000', target: '', mode: 'auto', local_proxy: false, key: '', auth: []}; }

    function defaultForm(ctx) {
        prepare(ctx);
        return {run: 'temp', os: ctx._windows ? 'powershell' : 'linux', bin: 'dot', binPath: '', named: false, name: '', pipes: 'single', scheduler: '',
            paths: [newPath(ctx._primary)], access: ctx.kind === 'tunnel' ? [newAccess(ctx)] : []};
    }

    // normalize makes a saved form complete; it may come from another version.
    function normalize(saved, ctx) {
        var form = defaultForm(ctx), s = saved;
        if (typeof s === 'string') { try { s = s ? JSON.parse(s) : null; } catch (e) { s = null; } }
        if (!s || typeof s !== 'object') return form;
        ['run', 'os', 'bin', 'binPath', 'pipes', 'scheduler', 'name'].forEach(function (k) { if (typeof s[k] === 'string') form[k] = s[k]; });
        form.named = !!s.named;
        if (Array.isArray(s.paths) && s.paths.length) form.paths = s.paths.map(function (p) { return assign(newPath(CARRIERS.indexOf(p.carrier) >= 0 ? p.carrier : ctx._primary), p); });
        if (Array.isArray(s.access)) form.access = s.access.map(function (a) { var o = assign(newAccess(ctx), a); o.auth = Array.isArray(a.auth) ? a.auth : []; return o; });
        return form;
    }

    function effective(p, ctx) {
        var d = ctx._def[p.carrier] || {};
        var usesDefault = !trim(p.addr);
        var path = isWS(p.carrier) ? (trim(p.path) || d.path || '') : '';
        if (path && path.charAt(0) !== '/') path = '/' + path;
        return {carrier: p.carrier, host: trim(p.addr) || d.host || '', port: trim(p.port) || d.port || '', path: path,
            alpn: usesALPN(p.carrier) ? (trim(p.alpn) || (usesDefault ? d.alpn || '' : '')) : ''};
    }
    function hostPort(e) { return (e.host.indexOf(':') >= 0 ? '[' + e.host + ']' : e.host) + ':' + e.port; }

    function pathParams(p, e, multi) {
        var q = [];
        function add(k, v) { if (trim(v) !== '') q.push([k, trim(v)]); }
        add('host', p.host); add('sni', p.sni); add('alpn', e.alpn);
        if (isWS(p.carrier)) { add('http', p.http); add('subprotocol', p.subprotocol); add('kinds', p.kinds); }
        if (multi) add('tier', p.tier);
        add('weight', p.weight); if (p.standby) q.push(['standby', 'true']);
        add('count', p.count); add('dirs', p.dirs);
        return q;
    }

    function linkOf(form, ctx) {
        var multi = form.paths.length > 1;
        var first = form.paths[0], fe = effective(first, ctx);
        var q = [['type', first.carrier]].concat(pathParams(first, fe, multi));
        var s = q.map(function (kv) { return kv[0] + '=' + enc(kv[1]); }).join('&');
        if (form.pipes === 'multi') s += '&pipes=multi';
        if (trim(form.scheduler)) s += '&scheduler_up=' + enc(form.scheduler);
        form.paths.slice(1).forEach(function (p) {
            var e = effective(p, ctx);
            var inner = pathParams(p, e, multi).map(function (kv) { return kv[0] + '=' + enc(kv[1]); }).join(';');
            // the alt value is decoded once as a parameter of the link
            s += '&alt=' + (p.carrier + '://' + hostPort(e) + e.path + (inner ? '?' + inner : '')).replace(/%/g, '%25');
        });
        var label = connName(form);
        return 'npc://' + encKey(ctx.vkey || '') + '@' + hostPort(fe) + fe.path + '?' + s + (label ? '#' + encKey(label) : '');
    }

    function connName(form) { return form.named ? trim(form.name) : ''; }
    function accessKey(a, ctx) { return ctx.kind === 'tunnel' ? ctx.password || '' : trim(a.key); }
    function accessArg(a, ctx, via) {
        var s = a.type + '://' + (a.ip.indexOf(':') >= 0 ? '[' + a.ip + ']' : a.ip) + ':' + trim(a.port) + '?access=' + encKey(accessKey(a, ctx));
        if (trim(a.target) && !proxyType(a.type)) s += '&target=' + enc(trim(a.target));
        if (a.mode && a.mode !== 'auto') s += '&conn_mode=' + a.mode;
        if (a.local_proxy) s += '&local_proxy=true';
        if (a.type === 'mixed') (a.auth || []).forEach(function (u) { if (trim(u.user)) s += '&auth=' + enc(trim(u.user)) + ':' + enc(u.pass || ''); });
        if (via) s += '&via=' + enc(via);
        return s;
    }

    function shellOf(form) { return form.os === 'cmd' ? 'cmd' : form.os === 'powershell' ? 'powershell' : 'bash'; }
    function quote(v, shell) {
        if (shell === 'bash') {
            if (/[$`"\\!]/.test(v)) return "'" + v.replace(/'/g, "'\\''") + "'";
            return '"' + v + '"';
        }
        if (shell === 'cmd') return '"' + v.replace(/"/g, '\\"') + '"';
        if (/[$`"]/.test(v)) return "'" + v.replace(/'/g, "''") + "'";
        return '"' + v + '"';
    }
    function programOf(form, shell) {
        var bin = form.bin === 'plain' ? 'npc' : './npc', windows = shell !== 'bash';
        if (form.bin === 'custom' && trim(form.binPath)) bin = trim(form.binPath);
        if (windows) {
            if (form.bin === 'dot') bin = '.\\npc.exe';
            else if (!/\.exe$/i.test(bin)) bin += '.exe';
        }
        if (!/[\s&()^;'"$`]/.test(bin)) return bin;
        return (shell === 'powershell' ? '& ' : '') + quote(bin, shell);
    }
    function headOf(form, shell) {
        if (form.run === 'docker') return ['docker', 'run', '-d', '--restart=always', '--name', 'npc', '--net=host', 'duan2001/npc'];
        return form.run === 'service' ? [programOf(form, shell), 'install'] : [programOf(form, shell)];
    }
    var CONT = {bash: ' \\', cmd: ' ^', powershell: ' `'};

    function document_(form, ctx) {
        var multi = form.paths.length > 1;
        var doc = {vkey: ctx.vkey || '', paths: form.paths.map(function (p) {
            var e = effective(p, ctx), o = {url: e.carrier + '://' + hostPort(e) + e.path};
            pathParams(p, e, multi).forEach(function (kv) {
                var n = Number(kv[1]);
                o[kv[0]] = ['tier', 'weight', 'count'].indexOf(kv[0]) >= 0 ? n : kv[0] === 'standby' ? true : kv[1];
            });
            return o;
        })};
        var session = {};
        if (form.pipes === 'multi') session.max_pipes = 2;
        if (trim(form.scheduler)) session.scheduler_up = trim(form.scheduler);
        if (Object.keys(session).length) doc.session = session;
        if (form.access.length) doc.access = form.access.map(function (a) {
            var o = {access: accessKey(a, ctx), local_type: a.type, local_port: Number(a.port) || 0};
            if (trim(a.ip)) o.local_ip = trim(a.ip);
            if (trim(a.target) && !proxyType(a.type)) o.target = trim(a.target);
            if (a.mode && a.mode !== 'auto') o.conn_mode = a.mode;
            if (a.local_proxy) o.local_proxy = true;
            if (a.type === 'mixed') {
                var auth = (a.auth || []).filter(function (u) { return trim(u.user); }).map(function (u) { return {user: trim(u.user), pass: u.pass || ''}; });
                if (auth.length) o.auth = auth;
            }
            return o;
        });
        // a named connection is a profile: its key is the name -L's via refers to
        if (connName(form)) { var named = {}; named[connName(form)] = doc; return {profiles: named}; }
        return doc;
    }

    function base64(text) {
        var b = typeof Buffer !== 'undefined' ? Buffer.from(text, 'utf8').toString('base64') : btoa(unescape(encodeURIComponent(text)));
        return b.replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
    }

    // outputs is everything the generator prints for form and the current keys.
    function outputs(form, ctx) {
        prepare(ctx);
        var shell = shellOf(form), head = headOf(form, shell);
        var args = ['-launch=' + quote(linkOf(form, ctx), shell)].concat(form.access.map(function (a) { return '-L=' + quote(accessArg(a, ctx, connName(form)), shell); }));
        var doc = document_(form, ctx);
        return {
            one: head.concat(args).join(' '),
            multi: head.join(' ') + CONT[shell] + '\n  ' + args.join(CONT[shell] + '\n  '),
            jsonc: '// npc -launch=@npc.jsonc\n// contains keys in clear text\n' + JSON.stringify(doc, null, 2),
            base64: head.concat(['-launch=' + base64(JSON.stringify(doc))]).join(' ')
        };
    }

    // validate returns [{at, id}]: id is a language string, at the field.
    function validate(form, ctx) {
        prepare(ctx);
        var errs = [];
        function port(v, at) { if (trim(v) !== '' && !(/^\d+$/.test(trim(v)) && +v >= 1 && +v <= 65535)) errs.push({at: at, id: 'lg-err-port'}); }
        function int(v, at, min, id) { if (trim(v) !== '' && !(/^-?\d+$/.test(trim(v)) && +v >= min)) errs.push({at: at, id: id}); }
        var bidi = false;
        form.paths.forEach(function (p, i) {
            var e = effective(p, ctx), at = 'p' + i + '.';
            if (!e.host || !e.port) errs.push({at: at + 'addr', id: 'lg-err-addr'});
            port(p.port, at + 'port');
            int(p.tier, at + 'tier', -1e9, 'lg-err-number');
            int(p.weight, at + 'weight', 0, 'lg-err-number');
            int(p.count, at + 'count', 1, 'lg-err-count');
            if (isWS(p.carrier) && trim(p.http) && trim(p.http).split(',').some(function (h) { return ['http/1.1', 'h2'].indexOf(trim(h)) < 0; })) errs.push({at: at + 'http', id: 'lg-err-http'});
            if (isWS(p.carrier) && trim(p.kinds) && trim(p.kinds) !== 'websocket') errs.push({at: at + 'kinds', id: 'lg-err-kinds'});
            var dirs = trim(p.dirs);
            if (dirs && dirs.split(',').some(function (d) { return ['bidi', 'up', 'down'].indexOf(trim(d)) < 0; })) errs.push({at: at + 'dirs', id: 'lg-err-dirs'});
            if (!dirs || dirs.split(',').map(trim).indexOf('bidi') >= 0) bidi = true;
        });
        if (!bidi) errs.push({at: 'p0.dirs', id: 'lg-err-bidi'});
        var seen = {};
        form.access.forEach(function (a, i) {
            var at = 'a' + i + '.';
            if (!trim(a.port)) errs.push({at: at + 'port', id: 'lg-err-port'}); else port(a.port, at + 'port');
            if (ctx.kind !== 'tunnel' && !trim(a.key)) errs.push({at: at + 'key', id: 'lg-err-key'});
            if (ctx.kind === 'tunnel' && !ctx.defaultTarget && !proxyType(a.type) && !trim(a.target)) errs.push({at: at + 'target', id: 'lg-err-target'});
            (a.auth || []).forEach(function (u, j) { if (!trim(u.user) || trim(u.user).indexOf(':') >= 0) errs.push({at: at + 'auth' + j, id: 'lg-err-user'}); });
            var net = a.type === 'udp' ? ['udp'] : a.type === 'tcp_udp' ? ['tcp', 'udp'] : ['tcp'];
            net.forEach(function (n) {
                var key = n + trim(a.port);
                if (seen[key] && trim(a.port)) errs.push({at: at + 'port', id: 'lg-err-dup-port'});
                seen[key] = true;
            });
        });
        return errs;
    }

    function esc(v) {
        return String(v == null ? '' : v).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
    }

    function rowContext(base, kind, row) {
        var ctx = {carriers: base.carriers, primary: base.primary, bin: base.bin, kind: kind};
        if (kind === 'client') ctx.vkey = row.VerifyKey;
        else {
            ctx.vkey = base.visitor || (row.Client && row.Client.VerifyKey) || '';
            ctx.password = row.Password;
            ctx.defaultTarget = row.Target && trim(row.Target.TargetStr).split('\n')[0];
        }
        return prepare(ctx);
    }

    function pathOf(kind) { return kind === 'client' ? 'client' : 'index'; }

    // detail is the command of a list row: the default, until fill has the saved form.
    function detail(base, kind, row) {
        var ctx = rowContext(base, kind, row);
        return '<code tabindex="0" data-launch-cmd="1" onclick="oCopy(this)">' + esc(outputs(defaultForm(ctx), ctx).one) + '</code>&emsp;'
            + '<a class="btn btn-outline btn-primary btn-xs" href="' + esc(base.base) + '/' + pathOf(kind) + '/launch?id=' + row.Id + '"><i class="fa fa-edit" aria-hidden="true"></i> <span langtag="lg-edit"></span></a>';
    }

    function fill(base, kind, row, $el) {
        if (!row.HasLaunch) return;
        $.post(base.base + '/' + pathOf(kind) + '/launchform', {id: row.Id}, function (res) {
            if (!res || !res.data) return;
            var ctx = rowContext(base, kind, row);
            $el.find('[data-launch-cmd]').text(outputs(normalize(res.data, ctx), ctx).one);
        });
    }

    // The page.
    function field(label, inner, at) {
        return '<div class="lg-field"><label class="control-label font-bold"><span langtag="' + label + '"></span></label>' + inner
            + '<div class="text-danger small" data-err="' + (at || '') + '"></div></div>';
    }
    function input(bind, value, placeholder, type) {
        return '<input class="form-control" type="' + (type || 'text') + '" data-bind="' + bind + '" value="' + esc(value) + '"' + (placeholder ? ' placeholder="' + esc(placeholder) + '"' : '') + '>';
    }
    function select(bind, value, options) {
        return '<select class="form-control" data-bind="' + bind + '">' + options.map(function (o) {
            var v = o[0], lang = o[1];
            return '<option value="' + esc(v) + '"' + (v === value ? ' selected' : '') + (lang ? ' langtag="' + lang + '">' : '>' + esc(o[2] != null ? o[2] : v)) + '</option>';
        }).join('') + '</select>';
    }
    function textField(bind, value, at, label, ph) { return field(label, input(bind, value, ph), at); }

    function mount(rootEl, ctx) {
        prepare(ctx);
        var form = normalize(ctx.form, ctx);
        var open = new WeakMap();
        var $root = $(rootEl);

        function toolbar(kind, i, n) {
            var b = function (act, icon, lang, disabled) {
                return '<button type="button" class="btn btn-outline btn-secondary btn-xs" data-act="' + kind + '-' + act + '" data-i="' + i + '"' + (disabled ? ' disabled' : '') + '><i class="fa ' + icon + '" aria-hidden="true"></i><span class="sr-only" langtag="' + lang + '"></span></button> ';
            };
            return '<div class="lg-tools">' + b('up', 'fa-arrow-up', 'lg-up', i === 0) + b('down', 'fa-arrow-down', 'lg-down', i === n - 1) + b('del', 'fa-trash', 'lg-delete', false) + '</div>';
        }
        function advanced(obj, inner) { return '<details class="lg-adv" data-adv="1"' + (open.get(obj) ? ' open' : '') + '><summary><span langtag="lg-advanced"></span></summary><div class="lg-grid">' + inner + '</div></details>'; }

        function pathCard(p, i) {
            var d = ctx._def[p.carrier] || {}, at = 'p' + i + '.', b = 'paths.' + i + '.', multi = form.paths.length > 1, c = p.carrier;
            var top = field('lg-carrier', select(b + 'carrier', c, CARRIERS.map(function (x) { return [x, '', x]; })), at + 'carrier')
                + textField(b + 'addr', p.addr, at + 'addr', 'lg-addr', d.host || '')
                + textField(b + 'port', p.port, at + 'port', 'lg-port', d.port || '');
            var adv = '';
            if (isWS(c)) adv += textField(b + 'path', p.path, at + 'path', 'lg-path', d.path || '/ws');
            if (usesALPN(c)) adv += plain(b + 'alpn', p.alpn, at + 'alpn', 'alpn', d.alpn || '');
            adv += plain(b + 'sni', p.sni, at + 'sni', 'sni') + plain(b + 'host', p.host, at + 'host', 'host');
            if (isWS(c)) adv += plain(b + 'http', p.http, at + 'http', 'http', 'http/1.1,h2') + plain(b + 'subprotocol', p.subprotocol, at + 'subprotocol', 'subprotocol') + plain(b + 'kinds', p.kinds, at + 'kinds', 'kinds', 'websocket');
            if (multi) adv += plain(b + 'tier', p.tier, at + 'tier', 'tier', '0');
            adv += plain(b + 'weight', p.weight, at + 'weight', 'weight', '0') + plain(b + 'count', p.count, at + 'count', 'count', '1')
                + plain(b + 'dirs', p.dirs, at + 'dirs', 'dirs', 'bidi');
            adv += '<div class="lg-field"><label><input type="checkbox" data-bind="' + b + 'standby"' + (p.standby ? ' checked' : '') + '> <span langtag="lg-standby"></span></label></div>';
            return '<div class="lg-card">' + toolbar('path', i, form.paths.length) + '<div class="lg-grid">' + top + '</div>' + advanced(p, adv) + '</div>';
        }
        function plain(bind, value, at, name, ph) {
            return '<div class="lg-field"><label class="control-label font-bold">' + esc(name) + '</label>' + input(bind, value, ph) + '<div class="text-danger small" data-err="' + at + '"></div></div>';
        }
        function accessCard(a, i) {
            var at = 'a' + i + '.', b = 'access.' + i + '.', types = ['tcp', 'udp', 'tcp_udp', 'mixed'];
            if (form.os === 'linux') types.push('transparent');
            var top = field('lg-local-type', select(b + 'type', a.type, types.map(function (t) { return [t, '', t]; })), at + 'type')
                + textField(b + 'ip', a.ip, at + 'ip', 'lg-listen', proxyType(a.type) ? '127.0.0.1' : '0.0.0.0')
                + textField(b + 'port', a.port, at + 'port', 'lg-port');
            if (ctx.kind !== 'tunnel') top += textField(b + 'key', a.key, at + 'key', 'lg-key');
            if (!proxyType(a.type)) {
                var ph = ctx.kind === 'tunnel' && ctx.defaultTarget ? langDefault() + ctx.defaultTarget : '';
                top += textField(b + 'target', a.target, at + 'target', 'lg-target', ph);
            }
            var adv = field('lg-conn-mode', select(b + 'mode', a.mode, [['auto', '', 'auto'], ['relay', '', 'relay'], ['direct', '', 'direct']]), at + 'mode')
                + '<div class="lg-field"><label><input type="checkbox" data-bind="' + b + 'local_proxy"' + (a.local_proxy ? ' checked' : '') + '> <span langtag="lg-local-proxy"></span></label></div>';
            if (a.type === 'mixed') {
                adv += '<div class="lg-field lg-wide"><label class="control-label font-bold"><span langtag="lg-accounts"></span></label>'
                    + (a.auth || []).map(function (u, j) {
                        return '<div class="lg-account">' + input(b + 'auth.' + j + '.user', u.user, '') + input(b + 'auth.' + j + '.pass', u.pass, '', 'text')
                            + '<button type="button" class="btn btn-outline btn-secondary btn-xs" data-act="auth-del" data-i="' + i + '" data-j="' + j + '"><i class="fa fa-trash" aria-hidden="true"></i><span class="sr-only" langtag="lg-delete"></span></button></div><div class="text-danger small" data-err="' + at + 'auth' + j + '"></div>';
                    }).join('')
                    + '<button type="button" class="btn btn-outline btn-primary btn-xs" data-act="auth-add" data-i="' + i + '"><i class="fa fa-plus" aria-hidden="true"></i> <span langtag="lg-add-account"></span></button></div>';
            }
            return '<div class="lg-card">' + toolbar('access', i, form.access.length) + '<div class="lg-grid">' + top + '</div>'
                + '<div class="text-warning small" data-note="' + i + '"></div>' + advanced(a, adv) + '</div>';
        }
        function langDefault() {
            var l = typeof languages !== 'undefined' && languages.content && languages.content['lg-default'];
            return ((l && (l[languages.current] || l[languages['default']])) || 'Default') + ': ';
        }
        function runSelect() { return select('run', form.run, [['temp', 'lg-run-temp'], ['service', 'lg-run-service'], ['docker', 'lg-run-docker']]); }
        function osSelect() { return select('os', form.os, [['linux', '', 'Linux'], ['macos', '', 'macOS'], ['cmd', '', 'Windows cmd'], ['powershell', '', 'PowerShell']]); }

        function render() {
            var html = '<form class="form-horizontal resource-form" onsubmit="return false"><div class="lg-grid">'
                + field('lg-run', runSelect()) + field('lg-os', osSelect());
            if (form.run !== 'docker') {
                html += field('lg-bin', select('bin', form.bin, [['dot', 'lg-bin-dot'], ['plain', 'lg-bin-plain'], ['custom', 'lg-bin-custom']]));
                if (form.bin === 'custom') html += textField('binPath', form.binPath, 'binPath', 'lg-bin-custom');
            }
            html += '</div>';
            html += '<h4 class="lg-title"><span langtag="lg-paths"></span></h4>' + form.paths.map(pathCard).join('')
                + '<button type="button" class="btn btn-outline btn-primary btn-sm" data-act="path-add"><i class="fa fa-plus" aria-hidden="true"></i> <span langtag="lg-add-path"></span></button>'
                + '<details class="lg-adv" data-adv="form"' + (open.get(form) ? ' open' : '') + '><summary><span langtag="lg-advanced"></span></summary><div class="lg-grid">'
                + field('lg-usage', select('pipes', form.pipes, [['single', 'lg-mode-single'], ['multi', 'lg-mode-multi']]))
                + field('lg-scheduler', select('scheduler', form.scheduler, SCHEDULERS.map(function (s) { return [s, s ? '' : 'lg-default', s]; })))
                + '<div class="lg-field"><label><input type="checkbox" data-bind="named"' + (form.named ? ' checked' : '') + '> <span langtag="lg-named"></span></label>'
                + (form.named ? input('name', form.name, '') : '') + '</div>'
                + '</div></details>';
            html += '<h4 class="lg-title"><span langtag="lg-access"></span></h4>' + form.access.map(accessCard).join('')
                + '<button type="button" class="btn btn-outline btn-primary btn-sm" data-act="access-add"><i class="fa fa-plus" aria-hidden="true"></i> <span langtag="lg-add-access"></span></button>';
            html += '<h4 class="lg-title"><span langtag="lg-output"></span></h4><p class="text-warning small" langtag="lg-note-secret"></p><div id="lg-outputs"></div>';
            html += '<div class="lg-actions"><button type="button" class="btn btn-primary" data-act="save"><span langtag="lg-save"></span></button> '
                + '<button type="button" class="btn btn-outline btn-secondary" data-act="restore"><span langtag="lg-restore"></span></button></div></form>';
            $root.html(html);
            refresh();
            if ($.fn.setLang) $('body').setLang('#launch-root');
        }
        function outputBlock(id, label) {
            return '<div class="lg-output"><div class="lg-output-head"><b langtag="' + label + '"></b> <button type="button" class="btn btn-outline btn-primary btn-xs" data-copy="' + id + '"><i class="fa fa-copy" aria-hidden="true"></i> <span langtag="lg-copy"></span></button></div><pre id="lg-out-' + id + '" tabindex="0"></pre></div>';
        }
        function refresh() {
            var out = outputs(form, ctx), errs = validate(form, ctx);
            var box = $root.find('#lg-outputs');
            if (!box.children().length) box.html(outputBlock('one', 'lg-oneline') + outputBlock('multi', 'lg-multiline') + outputBlock('jsonc', 'lg-jsonc') + outputBlock('base64', 'lg-base64')
                + '<div class="text-muted small" data-note="service"></div>');
            ['one', 'multi', 'jsonc', 'base64'].forEach(function (k) { $root.find('#lg-out-' + k).text(out[k]); });
            $root.find('[data-err]').empty();
            errs.forEach(function (e) { $root.find('[data-err="' + e.at + '"]').append('<span langtag="' + e.id + '"></span> '); });
            form.access.forEach(function (a, i) {
                var msg = '', ip = trim(a.ip), loop = ip === '127.0.0.1' || ip === '::1';
                var hasAuth = (a.auth || []).some(function (u) { return trim(u.user); });
                if (a.type === 'mixed' && !loop && !hasAuth) msg = '<span langtag="lg-warn-noauth"></span>';
                if (a.type === 'transparent') {
                    if (!loop) msg = '<span langtag="lg-warn-lan"></span><br>';
                    msg += '<span langtag="lg-note-transparent"></span><pre>iptables -t nat -A OUTPUT -p tcp -d 10.0.0.0/8 -j REDIRECT --to-ports ' + esc(trim(a.port)) + '</pre>';
                }
                $root.find('[data-note="' + i + '"]').html(msg);
            });
            $root.find('[data-note="service"]').html(form.run === 'service' ? '<span langtag="lg-note-service"></span>' : '');
            if ($.fn.setLang) $('body').setLang('#lg-outputs');
            if ($.fn.setLang) $('body').setLang('[data-err]');
            if ($.fn.setLang) $('body').setLang('[data-note]');
        }
        function setBind(path, value) {
            var keys = path.split('.'), o = form;
            for (var i = 0; i < keys.length - 1; i++) o = o[keys[i]];
            o[keys[keys.length - 1]] = value;
        }
        function afterType(i) {
            var a = form.access[i];
            if (proxyType(a.type)) { a.target = ''; if (!trim(a.ip)) a.ip = '127.0.0.1'; }
            if (a.type !== 'mixed') a.auth = [];
        }
        var structural = /^(run|os|bin|named|paths\.\d+\.carrier|access\.\d+\.type|pipes|scheduler)$/;
        $root.on('input change', '[data-bind]', function (ev) {
            var bind = this.getAttribute('data-bind'), value = this.type === 'checkbox' ? this.checked : this.value;
            setBind(bind, value);
            if (ev.type === 'change' && structural.test(bind)) {
                var m = /^access\.(\d+)\.type$/.exec(bind);
                if (m) afterType(+m[1]);
                if (bind === 'os' && form.os !== 'linux') form.access.forEach(function (a) { if (a.type === 'transparent') a.type = 'mixed'; });
                render();
                return;
            }
            refresh();
        });
        $root.on('toggle', 'details[data-adv]', function () {
            var el = this, m = el.getAttribute('data-adv');
            var obj = m === 'form' ? form : null;
            if (!obj) {
                var cards = $(el).closest('.lg-card').prevAll('.lg-card').length;
                obj = $(el).closest('.lg-card').find('[data-bind^="paths."]').length ? form.paths[cards] : form.access[cards];
            }
            if (obj) open.set(obj, el.open);
        });
        function move(list, i, d) { var t = list[i]; list[i] = list[i + d]; list[i + d] = t; }
        function post(data, done) {
            $.post(ctx.base + '/' + ctx.save, data, function (res) {
                showMsg(langreply(res && res.msg), res && res.status ? 'success' : 'error', 2000);
                if (res && res.status && done) done();
            });
        }
        $root.on('click', '[data-act]', function () {
            var act = this.getAttribute('data-act'), i = +this.getAttribute('data-i'), j = +this.getAttribute('data-j');
            switch (act) {
                case 'path-add': form.paths.push(newPath(ctx._primary)); break;
                case 'path-del': if (form.paths.length > 1) form.paths.splice(i, 1); break;
                case 'path-up': move(form.paths, i, -1); break;
                case 'path-down': move(form.paths, i, 1); break;
                case 'access-add': form.access.push(newAccess(ctx)); break;
                case 'access-del': form.access.splice(i, 1); break;
                case 'access-up': move(form.access, i, -1); break;
                case 'access-down': move(form.access, i, 1); break;
                case 'auth-add': form.access[i].auth.push({user: '', pass: ''}); open.set(form.access[i], true); break;
                case 'auth-del': form.access[i].auth.splice(j, 1); break;
                case 'save': post({id: ctx.id, form: JSON.stringify(form)}); return;
                case 'restore': post({id: ctx.id, form: ''}, function () { form = defaultForm(ctx); render(); }); return;
            }
            render();
        });
        $root.on('click', '[data-copy]', function () { copyText($('#lg-out-' + this.getAttribute('data-copy')).text()); });
        render();
    }

    root.LaunchGen = {defaultForm: defaultForm, normalize: normalize, outputs: outputs, validate: validate, detail: detail, fill: fill, mount: mount};
    if (typeof module !== 'undefined' && module.exports) module.exports = root.LaunchGen;
})(typeof window !== 'undefined' ? window : globalThis);
