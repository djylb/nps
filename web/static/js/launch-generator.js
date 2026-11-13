// The start command generator: the one place that turns a saved form and
// the current keys into npc commands, for its page and for list details.
(function (root) {
    'use strict';
    var CARRIERS = ['auto', 'tcp', 'tls', 'ws', 'wss', 'http', 'https', 'quic'];
    var SCHEDULERS = ['', 'affinity', 'session_affinity', 'spread_eta', 'spread_round_robin', 'spread_rate_weighted'];
    var PATH_FIELDS = {addr: '', port: '', path: '', alpn: '', sni: '', host: '', http: '', subprotocol: '', tier: '', weight: '', standby: false, count: '', dirs: '', kinds: ''};

    var DEFAULT_SUBPROTOCOL = 'binary';

    function trim(v) { return v == null ? '' : String(v).trim(); }
    function isWS(c) { return c === 'ws' || c === 'wss'; }
    function isHTTP(c) { return isWS(c) || c === 'http' || c === 'https'; }
    function usesALPN(c) { return c === 'tls' || c === 'quic'; }
    function accountType(t) { return t === 'mixed' || t === 'socks5' || t === 'http'; }
    function proxyType(t) { return accountType(t) || t === 'transparent'; }
    // list splits a comma list into its trimmed, non-empty items.
    function list(v) { return trim(v).split(',').map(trim).filter(Boolean); }
    // the values a field offers: what the profile of the connection accepts (lib/connprofile)
    var KINDS_HTTP = ['websocket', 'streaming', 'split', 'exchange'];
    function httpOptions(c) { return c === 'https' ? ['http/1.1', 'h2', 'h3'] : c === 'http' ? ['http/1.1'] : ['http/1.1', 'h2']; }
    function kindOptions(c) { return isWS(c) ? ['websocket'] : KINDS_HTTP; }
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
            var a = /(?:^|&)alpn=([^&]*)/.exec(m[4] || ''), k = /(?:^|&)admit_key=([^&]*)/.exec(m[4] || '');
            out[typ] = {host: m[1].replace(/^\[|\]$/g, ''), port: m[2], path: decodeURI(m[3] || ''), alpn: a ? decodeURIComponent(a[1]) : '', admitKey: k ? decodeURIComponent(k[1]) : ''};
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
        return {run: 'temp', os: ctx._windows ? 'powershell' : 'linux', bin: 'dot', binPath: '', named: false, name: '', resident: false, pipes: 'single', scheduler: '',
            paths: [newPath(ctx._primary)], access: ctx.kind === 'tunnel' ? [newAccess(ctx)] : []};
    }

    // normalize makes a saved form complete; it may come from another version.
    function normalize(saved, ctx) {
        var form = defaultForm(ctx), s = saved;
        if (typeof s === 'string') { try { s = s ? JSON.parse(s) : null; } catch (e) { s = null; } }
        if (!s || typeof s !== 'object') return form;
        ['run', 'os', 'bin', 'binPath', 'pipes', 'scheduler', 'name'].forEach(function (k) { if (typeof s[k] === 'string') form[k] = s[k]; });
        form.named = !!s.named;
        form.resident = !!s.resident;
        if (Array.isArray(s.paths) && s.paths.length) form.paths = s.paths.map(function (p) {
            var o = assign(newPath(CARRIERS.indexOf(p.carrier) >= 0 ? p.carrier : ctx._primary), p);
            // bidi alone is what no dirs means
            if (trim(o.dirs) === 'bidi') o.dirs = '';
            return o;
        });
        if (Array.isArray(s.access)) form.access = s.access.map(function (a) { var o = assign(newAccess(ctx), a); o.auth = Array.isArray(a.auth) ? a.auth : []; return o; });
        return form;
    }

    function effective(p, ctx) {
        var d = ctx._def[p.carrier] || {};
        var usesDefault = !trim(p.addr);
        var path = isHTTP(p.carrier) || p.carrier === 'auto' ? (trim(p.path) || d.path || '') : '';
        if (path && path.charAt(0) !== '/') path = '/' + path;
        // On the bridge path the server shares with old npc, the core protocol is
        // told by the subprotocol; a custom address or path may be another path
        // (new_protocol_ws_paths), so nothing is added there.
        var shared = !trim(p.addr) && !trim(p.port) && !trim(p.path);
        var sub = shared && (isHTTP(p.carrier) || (p.carrier === 'auto' && path)) ? DEFAULT_SUBPROTOCOL : '';
        return {carrier: p.carrier, subprotocol: sub, host: trim(p.addr) || d.host || '', port: trim(p.port) || d.port || '', path: path,
            alpn: usesALPN(p.carrier) ? (trim(p.alpn) || (usesDefault ? d.alpn || '' : '')) : '',
            admitKey: d.admitKey || ''};
    }
    function hostPort(e) { return (e.host.indexOf(':') >= 0 ? '[' + e.host + ']' : e.host) + ':' + e.port; }

    function pathParams(p, e, multi) {
        var q = [];
        function add(k, v) { if (trim(v) !== '') q.push([k, trim(v)]); }
        add('host', p.host); add('sni', p.sni); add('alpn', e.alpn);
        if (isHTTP(p.carrier)) { add('http', p.http); add('subprotocol', trim(p.subprotocol) || e.subprotocol); add('kinds', p.kinds); }
        else if (p.carrier === 'auto') add('subprotocol', e.subprotocol);
        if (isHTTP(p.carrier) || p.carrier === 'auto') add('admit_key', e.admitKey);
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
        if (residentOf(form, ctx)) s += '&role=client,visitor';
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
    // residentOf: the connection also joins as an ordinary client. A visitor
    // key cannot, and without local accesses it is one anyway.
    function residentOf(form, ctx) { return !!form.resident && !ctx.visitorKey && form.access.length > 0; }
    function accessKey(a, ctx) { return ctx.kind === 'tunnel' ? ctx.password || '' : trim(a.key); }
    function accessArg(a, ctx, via) {
        var s = a.type + '://' + (a.ip.indexOf(':') >= 0 ? '[' + a.ip + ']' : a.ip) + ':' + trim(a.port) + '?access=' + encKey(accessKey(a, ctx));
        if (trim(a.target) && !proxyType(a.type)) s += '&target=' + enc(trim(a.target));
        if (a.mode && a.mode !== 'auto') s += '&conn_mode=' + a.mode;
        if (a.local_proxy) s += '&local_proxy=true';
        if (accountType(a.type)) (a.auth || []).forEach(function (u) { if (trim(u.user)) s += '&auth=' + enc(trim(u.user)) + ':' + enc(u.pass || ''); });
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
        if (residentOf(form, ctx)) doc.role = 'client,visitor';
        if (form.access.length) doc.access = form.access.map(function (a) {
            var o = {access: accessKey(a, ctx), local_type: a.type, local_port: Number(a.port) || 0};
            if (trim(a.ip)) o.local_ip = trim(a.ip);
            if (trim(a.target) && !proxyType(a.type)) o.target = trim(a.target);
            if (a.mode && a.mode !== 'auto') o.conn_mode = a.mode;
            if (a.local_proxy) o.local_proxy = true;
            if (accountType(a.type)) {
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
            if (isHTTP(p.carrier) && list(p.http).some(function (h) { return httpOptions(p.carrier).indexOf(h) < 0; })) errs.push({at: at + 'http', id: 'lg-err-http'});
            if (isHTTP(p.carrier) && list(p.kinds).some(function (k) { return kindOptions(p.carrier).indexOf(k) < 0; })) errs.push({at: at + 'kinds', id: 'lg-err-kinds'});
            var dirs = trim(p.dirs);
            if (dirs && dirs.split(',').some(function (d) { return ['bidi', 'up', 'down'].indexOf(trim(d)) < 0; })) errs.push({at: at + 'dirs', id: 'lg-err-dirs'});
            else if (dirs && (p.carrier === 'http' || p.carrier === 'https') && dirs !== 'bidi') errs.push({at: at + 'dirs', id: 'lg-err-dirs-http'});
            if (!dirs || dirs.split(',').map(trim).indexOf('bidi') >= 0) bidi = true;
        });
        if (!bidi) errs.push({at: 'p0.dirs', id: 'lg-err-bidi'});
        var seen = {};
        form.access.forEach(function (a, i) {
            var at = 'a' + i + '.';
            if (!trim(a.port)) errs.push({at: at + 'port', id: 'lg-err-port'}); else port(a.port, at + 'port');
            if (ctx.noRelay && a.mode === 'relay') errs.push({at: at + 'mode', id: 'lg-err-norelay'});
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
            ctx.visitorKey = !!base.visitor;
            ctx.password = row.Password;
            ctx.noRelay = !!row.NoRelay;
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
    function boolSelect(bind, value) {
        return '<select class="form-control" data-type="bool" data-bind="' + bind + '">'
            + '<option value="false" langtag="word-no"' + (value ? '' : ' selected') + '></option><option value="true" langtag="word-yes"' + (value ? ' selected' : '') + '></option></select>';
    }
    function hint(lang) { return '<div class="small text-muted" langtag="' + lang + '"></div>'; }
    function numField(bind, value, at, label, ph, min, hintLang) {
        return field(label, '<input class="form-control" type="number" step="1"' + (min != null ? ' min="' + min + '"' : '') + ' data-bind="' + bind + '" value="' + esc(value) + '"' + (ph ? ' placeholder="' + esc(ph) + '"' : '') + '>'
            + (hintLang ? hint(hintLang) : ''), at);
    }
    // checks is a field of fixed values to tick; the value is the comma list of the
    // ticked ones in the order they were ticked. Values the page does not offer but
    // the form holds stay in view, so that they can be unticked.
    function checks(bind, value, options, at, label, hintLang) {
        var chosen = list(value), all = options.concat(chosen.filter(function (v) { return options.indexOf(v) < 0; }));
        return '<div class="lg-field lg-wide lg-checks" data-multi="' + bind + '"><div class="control-label font-bold"><span langtag="' + label + '"></span></div><div>'
            + all.map(function (v) { return '<label><input type="checkbox" data-multi-value="' + esc(v) + '"' + (chosen.indexOf(v) >= 0 ? ' checked' : '') + '> ' + esc(v) + '</label>'; }).join('')
            + '</div><div class="small text-muted"><span langtag="' + hintLang + '"></span> <b class="lg-order">' + esc(chosen.join(' → ')) + '</b></div>'
            + '<div class="text-danger small" data-err="' + at + '"></div></div>';
    }
    function langText(id, fallback) {
        var l = typeof languages !== 'undefined' && languages.content && languages.content[id];
        return (l && (l[languages.current] || l[languages['default']])) || fallback;
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
        // advanced is the folded part of a card; what it is (kind, index) is on the
        // element, so that its state is kept for the object the card shows.
        function advanced(obj, kind, i, inner) { return '<details class="lg-adv" data-adv="' + kind + '" data-i="' + i + '"' + (open.get(obj) ? ' open' : '') + '><summary><span langtag="lg-advanced"></span></summary><div class="lg-grid">' + inner + '</div></details>'; }

        function pathCard(p, i) {
            var d = ctx._def[p.carrier] || {}, at = 'p' + i + '.', b = 'paths.' + i + '.', multi = form.paths.length > 1, c = p.carrier;
            // carriers the server listens on come first; the others need an address
            var on = CARRIERS.filter(function (x) { return ctx._def[x]; }), off = CARRIERS.filter(function (x) { return !ctx._def[x]; });
            var top = field('lg-carrier', '<select class="form-control" data-bind="' + b + 'carrier">' + on.concat(off).map(function (x) {
                return '<option value="' + x + '"' + (x === c ? ' selected' : '') + (ctx._def[x] ? '>' + x : ' langtag="lg-carrier-addr" langprefix="' + x + '">' + x) + '</option>';
            }).join('') + '</select>', at + 'carrier')
                + textField(b + 'addr', p.addr, at + 'addr', 'lg-addr', d.host || '')
                + textField(b + 'port', p.port, at + 'port', 'lg-port', d.port || '');
            var adv = '';
            if (isHTTP(c)) adv += textField(b + 'path', p.path, at + 'path', 'lg-path', d.path || '/ws');
            if (usesALPN(c)) adv += plain(b + 'alpn', p.alpn, at + 'alpn', 'alpn', d.alpn || '');
            adv += plain(b + 'sni', p.sni, at + 'sni', 'sni') + plain(b + 'host', p.host, at + 'host', 'host');
            if (isHTTP(c)) adv += checks(b + 'http', p.http, httpOptions(c), at + 'http', 'lg-f-http', 'lg-hint-order') + plain(b + 'subprotocol', p.subprotocol, at + 'subprotocol', 'subprotocol', effective(p, ctx).subprotocol)
                + checks(b + 'kinds', p.kinds, kindOptions(c), at + 'kinds', 'lg-f-kinds', 'lg-hint-kinds');
            if (multi) adv += numField(b + 'tier', p.tier, at + 'tier', 'lg-f-tier', '0', null, 'lg-hint-tier');
            adv += numField(b + 'weight', p.weight, at + 'weight', 'lg-f-weight', '0', 0, 'lg-hint-weight') + numField(b + 'count', p.count, at + 'count', 'lg-f-count', '1', 1, 'lg-hint-count')
                + field('lg-f-dirs', dirsSelect(b + 'dirs', p) + hint('lg-hint-dirs'), at + 'dirs')
                + field('lg-standby', boolSelect(b + 'standby', p.standby) + hint('lg-hint-standby'), at + 'standby');
            return '<div class="lg-card">' + toolbar('path', i, form.paths.length) + '<div class="lg-grid">' + top + '</div>' + advanced(p, 'path', i, adv) + '</div>';
        }
        function dirsSelect(bind, p) {
            var v = trim(p.dirs), opts = [['', 'lg-dirs-bidi'], ['up', 'lg-dirs-up'], ['down', 'lg-dirs-down']];
            // http and https carry both directions only
            if (p.carrier === 'http' || p.carrier === 'https') opts = opts.slice(0, 1);
            else if (v && ['up', 'down'].indexOf(v) < 0) opts.push([v, '', v]);
            return select(bind, p.carrier === 'http' || p.carrier === 'https' ? '' : v, opts);
        }
        function plain(bind, value, at, name, ph) {
            return '<div class="lg-field"><label class="control-label font-bold">' + esc(name) + '</label>' + input(bind, value, ph) + '<div class="text-danger small" data-err="' + at + '"></div></div>';
        }
        function accessCard(a, i) {
            var at = 'a' + i + '.', b = 'access.' + i + '.', types = ['tcp', 'udp', 'tcp_udp', 'mixed', 'socks5', 'http'];
            if (form.os === 'linux') types.push('transparent');
            var top = field('lg-local-type', select(b + 'type', a.type, types.map(function (t) { return [t, '', t]; })), at + 'type')
                + textField(b + 'ip', a.ip, at + 'ip', 'lg-listen', proxyType(a.type) ? '127.0.0.1' : '0.0.0.0')
                + textField(b + 'port', a.port, at + 'port', 'lg-port');
            if (ctx.kind !== 'tunnel') top += textField(b + 'key', a.key, at + 'key', 'lg-key');
            if (!proxyType(a.type)) {
                var ph = ctx.kind === 'tunnel' && ctx.defaultTarget ? langDefault() + ctx.defaultTarget : '';
                top += textField(b + 'target', a.target, at + 'target', 'lg-target', ph);
            }
            var modes = [['auto', 'lg-cm-auto'], ['relay', 'lg-cm-relay'], ['direct', 'lg-cm-direct']];
            if (ctx.noRelay && a.mode !== 'relay') modes.splice(1, 1);
            var adv = field('lg-conn-mode', select(b + 'mode', a.mode, modes) + (ctx.noRelay ? hint('lg-hint-norelay') : ''), at + 'mode')
                + field('lg-local-proxy', boolSelect(b + 'local_proxy', a.local_proxy) + hint('lg-hint-local-proxy'), at + 'local_proxy');
            if (accountType(a.type)) {
                adv += '<div class="lg-field lg-wide"><label class="control-label font-bold"><span langtag="lg-accounts"></span></label>'
                    + (a.auth || []).map(function (u, j) {
                        return '<div class="lg-account">' + input(b + 'auth.' + j + '.user', u.user, '') + input(b + 'auth.' + j + '.pass', u.pass, '', 'text')
                            + '<button type="button" class="btn btn-outline btn-secondary btn-xs" data-act="auth-del" data-i="' + i + '" data-j="' + j + '"><i class="fa fa-trash" aria-hidden="true"></i><span class="sr-only" langtag="lg-delete"></span></button></div><div class="text-danger small" data-err="' + at + 'auth' + j + '"></div>';
                    }).join('')
                    + '<button type="button" class="btn btn-outline btn-primary btn-xs" data-act="auth-add" data-i="' + i + '"><i class="fa fa-plus" aria-hidden="true"></i> <span langtag="lg-add-account"></span></button></div>';
            }
            return '<div class="lg-card">' + toolbar('access', i, form.access.length) + '<div class="lg-grid">' + top + '</div>'
                + '<div class="text-warning small" data-note="' + i + '"></div>' + advanced(a, 'access', i, adv) + '</div>';
        }
        function langDefault() { return langText('lg-default', 'Default') + ': '; }
        function runSelect() { return select('run', form.run, [['temp', 'lg-run-temp'], ['service', 'lg-run-service'], ['docker', 'lg-run-docker']]); }
        function osSelect() { return select('os', form.os, [['linux', '', 'Linux'], ['macos', '', 'macOS'], ['cmd', '', 'Windows cmd'], ['powershell', '', 'PowerShell']]); }

        // focusKey names the control that has the focus, so that the page drawn
        // again puts it back.
        function focusKey() {
            var a = document.activeElement;
            if (!a || a === document.body || !$root[0].contains(a)) return null;
            var sel = a.hasAttribute('data-bind') ? '[data-bind="' + a.getAttribute('data-bind') + '"]'
                : a.hasAttribute('data-act') ? '[data-act="' + a.getAttribute('data-act') + '"][data-i="' + a.getAttribute('data-i') + '"]' + (a.hasAttribute('data-j') ? '[data-j="' + a.getAttribute('data-j') + '"]' : '')
                : a.hasAttribute('data-copy') ? '[data-copy="' + a.getAttribute('data-copy') + '"]' : null;
            if (!sel) return null;
            var range = typeof a.selectionStart === 'number' && /^(text|search|url|tel|password)$/.test(a.type) ? [a.selectionStart, a.selectionEnd] : null;
            return {sel: sel, range: range};
        }
        function restoreFocus(key) {
            if (!key) return;
            var el;
            [].concat(key.sel).some(function (sel) { el = $root.find(sel).filter(':not(:disabled)')[0]; return el; });
            if (!el) return;
            el.focus({preventScroll: true});
            if (key.range && typeof el.setSelectionRange === 'function') { try { el.setSelectionRange(key.range[0], key.range[1]); } catch (e) { /* not a text control */ } }
        }
        function render(focus) {
            var kept = focus === undefined ? focusKey() : focus;
            var html = '<form class="form-horizontal resource-form" onsubmit="return false"><div class="lg-grid">'
                + field('lg-run', runSelect()) + field('lg-os', osSelect());
            if (form.run !== 'docker') {
                html += field('lg-bin', select('bin', form.bin, [['dot', 'lg-bin-dot'], ['plain', 'lg-bin-plain'], ['custom', 'lg-bin-custom']]));
                if (form.bin === 'custom') html += textField('binPath', form.binPath, 'binPath', 'lg-bin-custom');
            }
            html += '</div>';
            html += '<h4 class="lg-title"><span langtag="lg-paths"></span></h4>' + form.paths.map(pathCard).join('')
                + '<button type="button" class="btn btn-outline btn-primary btn-sm" data-act="path-add"><i class="fa fa-plus" aria-hidden="true"></i> <span langtag="lg-add-path"></span></button>'
                + '<details class="lg-adv" data-adv="form" data-i="0"' + (open.get(form) ? ' open' : '') + '><summary><span langtag="lg-advanced"></span></summary><div class="lg-grid">'
                + field('lg-usage', select('pipes', form.pipes, [['single', 'lg-mode-single'], ['multi', 'lg-mode-multi']]))
                + field('lg-scheduler', select('scheduler', form.scheduler, SCHEDULERS.map(function (s) { return [s, s ? 'lg-sch-' + s : 'lg-default', s]; })))
                + '<div class="lg-field"><label><input type="checkbox" data-bind="named"' + (form.named ? ' checked' : '') + '> <span langtag="lg-named"></span></label>'
                + (form.named ? input('name', form.name, '') : '') + '</div>'
                + '</div></details>';
            html += '<h4 class="lg-title"><span langtag="lg-access"></span></h4>' + (ctx.visitorKey ? '<p class="small text-muted" langtag="lg-note-visitor"></p>' : '') + form.access.map(accessCard).join('')
                + '<button type="button" class="btn btn-outline btn-primary btn-sm" data-act="access-add"><i class="fa fa-plus" aria-hidden="true"></i> <span langtag="lg-add-access"></span></button>';
            if (form.access.length && !ctx.visitorKey) html += '<div class="lg-field"><label><input type="checkbox" data-bind="resident"' + (form.resident ? ' checked' : '') + '> <span langtag="lg-resident"></span></label>' + hint('lg-hint-resident') + '</div>';
            html += '<h4 class="lg-title"><span langtag="lg-output"></span></h4><div class="alert alert-danger lg-errs" role="alert" hidden><span langtag="lg-errs"></span></div>'
                + '<p class="text-warning small" langtag="lg-note-secret"></p><div id="lg-outputs"></div>';
            html += '<div class="lg-actions"><button type="button" class="btn btn-primary" data-act="save"><span langtag="lg-save"></span></button> '
                + '<button type="button" class="btn btn-outline btn-secondary" data-act="cancel"><span langtag="lg-cancel"></span></button> '
                + '<button type="button" class="btn btn-outline btn-secondary" data-act="restore"><span langtag="lg-restore"></span></button>'
                + '<span class="small text-muted lg-restore-hint" langtag="lg-restore-hint"></span></div></form>';
            $root.html(html);
            refresh();
            if ($.fn.setLang) $('body').setLang('#launch-root');
            restoreFocus(kept);
        }
        // The outputs are tabs: one shown at a time, the one-line command first.
        var OUTS = [['one', 'lg-oneline'], ['multi', 'lg-multiline'], ['jsonc', 'lg-jsonc'], ['base64', 'lg-base64']], shown = 'one';
        function outputTabs() {
            return '<div class="lg-output"><div class="lg-output-head"><div class="btn-group lg-tabs" role="tablist">' + OUTS.map(function (o) {
                return '<button type="button" role="tab" class="btn btn-sm ' + (o[0] === shown ? 'btn-primary' : 'btn-outline btn-secondary') + '" aria-selected="' + (o[0] === shown) + '" data-tab="' + o[0] + '"><span langtag="' + o[1] + '"></span></button>';
            }).join('') + '</div><button type="button" class="btn btn-primary btn-sm" data-copy="out"><i class="fa fa-copy" aria-hidden="true"></i> <span langtag="lg-copy"></span></button></div>'
                + '<pre id="lg-out" tabindex="0"></pre><div class="small text-muted" data-tabnote></div></div>';
        }
        function refresh() {
            var out = outputs(form, ctx), errs = validate(form, ctx);
            var box = $root.find('#lg-outputs');
            if (!box.children().length) box.html(outputTabs() + '<div class="text-muted small" data-note="service"></div>');
            $root.find('#lg-out').text(out[shown]).toggleClass('lg-bad', errs.length > 0);
            $root.find('[data-tabnote]').html(shown === 'jsonc' ? '<span langtag="lg-note-jsonc"></span>' : shown === 'base64' ? '<span langtag="lg-note-base64"></span>' : '');
            $root.find('.lg-errs').prop('hidden', !errs.length);
            $root.find('[data-err]').empty();
            errs.forEach(function (e) { $root.find('[data-err="' + e.at + '"]').append('<span langtag="' + e.id + '"></span> '); });
            form.access.forEach(function (a, i) {
                var msg = '', ip = trim(a.ip), loop = ip === '127.0.0.1' || ip === '::1';
                var hasAuth = (a.auth || []).some(function (u) { return trim(u.user); });
                if (accountType(a.type) && !loop && !hasAuth) msg = '<span langtag="lg-warn-noauth"></span>';
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
            if ($.fn.setLang) $('body').setLang('[data-tabnote]');
        }
        function setBind(path, value) {
            var keys = path.split('.'), o = form;
            for (var i = 0; i < keys.length - 1; i++) o = o[keys[i]];
            o[keys[keys.length - 1]] = value;
        }
        function afterType(i) {
            var a = form.access[i];
            if (proxyType(a.type)) { a.target = ''; if (!trim(a.ip)) a.ip = '127.0.0.1'; }
            if (!accountType(a.type)) a.auth = [];
        }
        var structural = /^(run|os|bin|named|paths\.\d+\.carrier|access\.\d+\.type|pipes|scheduler)$/;
        $root.on('input change', '[data-bind]', function (ev) {
            var bind = this.getAttribute('data-bind'), value = this.type === 'checkbox' ? this.checked : this.getAttribute('data-type') === 'bool' ? this.value === 'true' : this.value;
            setBind(bind, value);
            if (ev.type === 'change' && structural.test(bind)) {
                var m = /^access\.(\d+)\.type$/.exec(bind);
                if (m) afterType(+m[1]);
                var c = /^paths\.(\d+)\.carrier$/.exec(bind);
                if (c && (value === 'http' || value === 'https')) form.paths[+c[1]].dirs = '';
                if (bind === 'os' && form.os !== 'linux') form.access.forEach(function (a) { if (a.type === 'transparent') a.type = 'mixed'; });
                render();
                return;
            }
            refresh();
        });
        $root.on('change', '[data-multi-value]', function () {
            var group = $(this).closest('[data-multi]'), bind = group.attr('data-multi'), v = this.getAttribute('data-multi-value');
            var o = form;
            bind.split('.').forEach(function (k) { o = o[k]; });
            var cur = list(o), at = cur.indexOf(v);
            if (this.checked && at < 0) cur.push(v);
            else if (!this.checked && at >= 0) cur.splice(at, 1);
            setBind(bind, cur.join(','));
            group.find('.lg-order').text(cur.join(' → '));
            refresh();
        });
        // toggle does not bubble: it is caught on the way down. The state is kept
        // for the object of the card, so that drawing the page again leaves every
        // advanced part as the user left it.
        $root[0].addEventListener('toggle', function (ev) {
            var el = ev.target;
            if (!el.matches || !el.matches('details[data-adv]') || !el.isConnected) return;
            var kind = el.getAttribute('data-adv'), i = +el.getAttribute('data-i');
            var obj = kind === 'form' ? form : kind === 'path' ? form.paths[i] : form.access[i];
            if (obj) open.set(obj, el.open);
        }, true);
        function move(list, i, d) { var t = list[i]; list[i] = list[i + d]; list[i + d] = t; }
        function post(data, done) {
            $.post(ctx.base + '/' + ctx.save, data, function (res) {
                showMsg(langreply(res && res.msg), res && res.status ? 'success' : 'error', 2000);
                if (res && res.status && done) done();
            });
        }
        $root.on('click', '[data-act]', function () {
            var act = this.getAttribute('data-act'), i = +this.getAttribute('data-i'), j = +this.getAttribute('data-j');
            var focus;
            switch (act) {
                case 'path-add': form.paths.push(newPath(ctx._primary)); break;
                case 'path-del': if (form.paths.length > 1) form.paths.splice(i, 1); break;
                case 'path-up': move(form.paths, i, -1); focus = {sel: ['[data-act="path-up"][data-i="' + (i - 1) + '"]', '[data-act="path-down"][data-i="' + (i - 1) + '"]']}; break;
                case 'path-down': move(form.paths, i, 1); focus = {sel: ['[data-act="path-down"][data-i="' + (i + 1) + '"]', '[data-act="path-up"][data-i="' + (i + 1) + '"]']}; break;
                case 'access-add': form.access.push(newAccess(ctx)); break;
                case 'access-del': form.access.splice(i, 1); break;
                case 'access-up': move(form.access, i, -1); focus = {sel: ['[data-act="access-up"][data-i="' + (i - 1) + '"]', '[data-act="access-down"][data-i="' + (i - 1) + '"]']}; break;
                case 'access-down': move(form.access, i, 1); focus = {sel: ['[data-act="access-down"][data-i="' + (i + 1) + '"]', '[data-act="access-up"][data-i="' + (i + 1) + '"]']}; break;
                case 'auth-add': form.access[i].auth.push({user: '', pass: ''}); open.set(form.access[i], true); break;
                case 'auth-del': form.access[i].auth.splice(j, 1); break;
                case 'save': post({id: ctx.id, form: JSON.stringify(form)}); return;
                // restore only resets the form on the page; it takes effect when saved
                case 'restore': form = defaultForm(ctx); open = new WeakMap(); render(); return;
                case 'cancel': if (typeof ctx.cancel === 'function') ctx.cancel(); else window.history.back(); return;
            }
            render(focus);
        });
        $root.on('click', '[data-copy]', function () { copyText($('#lg-out').text()); });
        $root.on('click', '[data-tab]', function () {
            shown = this.getAttribute('data-tab');
            $root.find('[data-tab]').each(function () {
                var on = this.getAttribute('data-tab') === shown;
                $(this).toggleClass('btn-primary', on).toggleClass('btn-outline btn-secondary', !on).attr('aria-selected', on);
            });
            refresh();
        });
        render();
    }

    root.LaunchGen = {defaultForm: defaultForm, normalize: normalize, outputs: outputs, validate: validate, detail: detail, fill: fill, mount: mount};
    if (typeof module !== 'undefined' && module.exports) module.exports = root.LaunchGen;
})(typeof window !== 'undefined' ? window : globalThis);
