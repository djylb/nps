/* Foreground-only polling shared by lists and the dashboard. No saved state. */
(function (window, $) {
    'use strict';
    function foreground() {
        var doc = document;
        if (typeof doc.hidden === 'boolean') return !doc.hidden;
        if (typeof doc.webkitHidden === 'boolean') return !doc.webkitHidden;
        if (typeof doc.msHidden === 'boolean') return !doc.msHidden;
        return typeof doc.hasFocus === 'function' && doc.hasFocus();
    }
    function selectable() {
        var selection = window.getSelection && window.getSelection();
        var active = document.activeElement;
        return (selection && !selection.isCollapsed && String(selection).length > 0)
            || (active && ($(active).is('input,textarea,select,[contenteditable="true"]')))
            || $('.modal.show,.modal.in,.list-batch-running').length > 0;
    }
    window.npsAutoRefresh = function (task, container) {
        var enabled = true, pending = null, timer = null, stopped = false, dragging = false, failures = 0;
        var button = $('<button type="button" class="btn btn-success btn-xs nps-auto-refresh" aria-pressed="true" style="margin-left:12px;white-space:nowrap;padding:3px 8px;font-size:12px;line-height:1.4"></button>');
        var host = $(container).first();
        if (!host.length) {
            host = $('<div class="nps-refresh-control" style="margin-left:auto;flex-shrink:0"></div>').appendTo($('.wrapper .ibox-title').first());
        }
        host.append(button);
        function eligible() { return !stopped && enabled && foreground() && !dragging && !selectable(); }
        function label() {
            button.toggleClass('btn-success', enabled).toggleClass('btn-default', !enabled)
                .attr('aria-pressed', String(enabled)).empty()
                .append($('<i aria-hidden="true" class="fa"></i>').addClass(enabled ? 'fa-refresh' : 'fa-pause'))
                .append(' ').append($('<span></span>').attr('langtag', enabled ? 'word-autorefresh' : 'word-refreshpaused').text(enabled ? 'Refreshing' : 'Paused'));
            if ($.fn.setLang && window.languages && window.languages.content) $('body').setLang('.nps-auto-refresh');
        }
        function schedule(delay) {
            clearTimeout(timer);
            if (!stopped) timer = setTimeout(tick, delay);
        }
        function tick() {
            if (pending || !eligible()) { schedule(3000); return; }
            var request = task(eligible);
            if (!request || !request.always) { schedule(3000); return; }
            pending = request;
            request.done(function () { failures = 0; }).fail(function (xhr, status) {
                if (status !== 'abort') failures = Math.min(failures + 1, 4);
            }).always(function () { pending = null; schedule(Math.min(30000, 3000 * Math.pow(2, failures))); });
        }
        function visibility() {
            clearTimeout(timer);
            if (!foreground() && pending && pending.abort) pending.abort();
            schedule(foreground() ? 0 : 3000);
        }
        button.on('click', function () {
            enabled = !enabled; label();
            if (!enabled && pending && pending.abort) pending.abort();
            schedule(enabled ? 0 : 3000);
        });
        $(document).on('visibilitychange.npslive webkitvisibilitychange.npslive msvisibilitychange.npslive', visibility)
            .on('mousedown.npslive touchstart.npslive', function () { dragging = true; })
            .on('mouseup.npslive touchend.npslive touchcancel.npslive', function () { dragging = false; });
        $(window).on('focus.npslive blur.npslive', visibility).on('pagehide.npslive', function () {
            stopped = true; clearTimeout(timer); if (pending && pending.abort) pending.abort();
        }).on('pageshow.npslive', function () { if (stopped) { stopped = false; enabled = true; failures = 0; label(); schedule(0); } });
        label(); schedule(3000);
        return { eligible: eligible };
    };
    var dependencies = {
        Speed: ['Speed'], UploadRate: ['UploadRate'], DownloadRate: ['DownloadRate'],
        InletFlow: ['Flow.InletFlow'], ExportFlow: ['Flow.ExportFlow'],
        TotalFlow: ['TotalFlow'], ActualFlow: ['ActualFlow'],
        FlowRemain: ['FlowRemain'], TimeRemain: [],
        NowConn: ['NowConn'], IsConnect: ['IsConnect'], 'Client.IsConnect': ['Client.IsConnect'],
        Status: ['Status'], RunStatus: ['RunStatus'], IsClose: ['IsClose']
    };
    function path(row, field) { return field.split('.').reduce(function (v, key) { return v == null ? undefined : v[key]; }, row); }
    function assign(row, field, value) {
        var keys = field.split('.'), target = row;
        for (var i = 0; i < keys.length - 1; i++) { target = target[keys[i]] || (target[keys[i]] = {}); }
        target[keys[keys.length - 1]] = value;
    }
    window.npsLiveValue = function (field, html) {
        return '<span data-live-field="' + field + '">' + html + '</span>';
    };
    window.npsHistoryHTML = function (events) {
        if (!events || !events.length) return '';
        return '<b langtag="word-connectionhistory"></b>: <div style="overflow-x:auto;margin-top:4px"><div style="display:grid;grid-template-columns:repeat(5,max-content);column-gap:14px;row-gap:2px;white-space:nowrap;line-height:1.4">'
            + events.slice().reverse().map(function (event, index) {
                return '<span style="text-align:right">' + (index + 1) + '.</span><span langtag="word-online"></span><span>' + escapeHtml(event.OnlineTime)
                    + '</span><span langtag="word-offline"></span><span>' + escapeHtml(event.OfflineTime || '-') + '</span>';
            }).join('') + '</div></div>';
    };
    window.initListLive = function (resource, baseURL) {
        var table = $('#table'), serial = 0, cursor = 0;
        table.on('load-success.bs.table page-change.bs.table search.bs.table sort.bs.table column-switch.bs.table expand-row.bs.table collapse-row.bs.table refresh.bs.table', function () { serial++; });
        function rowsOnScreen() {
            var data = table.bootstrapTable('getData'), rows = [];
            table.find('> tbody > tr[data-index]').each(function () {
                if ($(this).is(':visible')) { var index = Number($(this).attr('data-index')); if (data[index]) rows.push({row: data[index], tr: $(this), index: index}); }
            });
            return rows;
        }
        npsAutoRefresh(function (eligible) {
            if (!table.is(':visible')) return null;
            var rows = rowsOnScreen(), columns = table.bootstrapTable('getVisibleColumns').filter(function (column) { return !table.bootstrapTable('getOptions').cardView || column.cardVisible !== false; }), items = [];
            columns.forEach(function (column) {
                if (column.field !== 'TimeRemain' || typeof column.formatter !== 'function') return;
                var header = table.find('> thead > tr').last().children('th').filter(function () { return $(this).attr('data-field') === column.field; });
                rows.forEach(function (entry) {
                    var cell = table.bootstrapTable('getOptions').cardView ? entry.tr.find('.card-view').filter(function () { return $(this).hasClass('card-view-field-' + column.field); }).find('.card-view-value') : (header.length ? entry.tr.children('td').eq(header.index()) : $());
                    var html = column.formatter.call(column, path(entry.row, column.field), entry.row, entry.index, column.field);
                    if (cell.html() !== String(html)) cell.html(html);
                });
            });
            rows.forEach(function (entry) {
                var fields = {};
                columns.forEach(function (column) { (dependencies[column.field] || []).forEach(function (f) { fields[f] = true; }); });
                entry.tr.next('.detail-view').find('[data-live-field]').each(function () { if ($(this).closest('.detail-view').is(':visible')) fields[$(this).attr('data-live-field')] = true; });
                if (Object.keys(fields).length) items.push({id: entry.row.Id, fields: Object.keys(fields)});
            });
            if (items.length > 200) { var all = items; items = all.slice(cursor, cursor + 200); cursor = (cursor + 200 >= all.length) ? 0 : cursor + 200; }
            var global = resource === 'client' && $('#global_traffic').is(':visible') ? 'In,Out,InBps,OutBps' : '';
            if (!items.length && !global) return null;
            var generation = serial;
            return $.ajax({type: 'POST', url: baseURL + '/live/status', dataType: 'json', timeout: 10000,
                data: {resource: resource, items: JSON.stringify(items), global: global}
            }).done(function (result) {
                if (!eligible() || generation !== serial || !result || !Array.isArray(result.rows)) return;
                result.rows.forEach(function (patch) {
                    var entry = rows.filter(function (e) { return e.row.Id === patch.Id; })[0];
                    if (!entry || !$.contains(document, entry.tr[0])) return;
                    var changed = {};
                    Object.keys(patch).forEach(function (field) { if (field !== 'Id') { changed[field] = JSON.stringify(path(entry.row, field)) !== JSON.stringify(patch[field]); assign(entry.row, field, patch[field]); } });
                    var headers = table.find('> thead > tr').last().children('th');
                    columns.forEach(function (column, index) {
                        if (!Object.prototype.hasOwnProperty.call(dependencies, column.field)) return;
                        if (column.field !== 'TimeRemain' && !(dependencies[column.field] || []).some(function (f) { return changed[f]; })) return;
                        var value = path(entry.row, column.field), html = typeof column.formatter === 'function' ? column.formatter.call(column, value, entry.row, entry.index, column.field) : value;
                        var header = headers.filter(function () { return $(this).attr('data-field') === column.field; });
                        var cell = table.bootstrapTable('getOptions').cardView ? entry.tr.find('.card-view').filter(function () { return $(this).hasClass('card-view-field-' + column.field); }).find('.card-view-value') : (header.length ? entry.tr.children('td').eq(header.index()) : $());
                        if (cell.html() !== String(html)) cell.html(html).addClass('nps-live-changed');
                    });
                    entry.tr.next('.detail-view').find('[data-live-field]').each(function () {
                        var field = $(this).attr('data-live-field'); if (!Object.prototype.hasOwnProperty.call(patch, field) || !changed[field]) return;
                        var value = patch[field], html = field === 'ConnectionHistory' ? npsHistoryHTML(value) : field.indexOf('Flow.') === 0 ? changeunit(value) : escapeHtml(value);
                        if ($(this).html() !== html) $(this).html(html).addClass('nps-live-changed');
                    });
                });
                if (result.traffic && typeof showGlobalTraffic === 'function') showGlobalTraffic({in: result.traffic.In, out: result.traffic.Out, inBps: result.traffic.InBps, outBps: result.traffic.OutBps, limit: Number($('#global_traffic').data('limit')) || 0});
                if ($.fn.setLang) $('body').setLang('.nps-live-changed');
                $('.nps-live-changed').removeClass('nps-live-changed');
            });
        });
    };
})(window, jQuery);
