/* Shared behaviour of the bootstrap-table lists (clients, tunnels, domains):
 * per-page column choice kept in localStorage, a pinned option column, a
 * header that stays in view while the page scrolls, and a horizontal scroll
 * bar fixed to the bottom of the screen. The page scrolls inside #wrapper and
 * the table scrolls inside .fixed-table-body, both of which clip, so the
 * header and the bar are floating copies kept in step by script.
 * Card view (narrow screens) turns all of it off. */
(function (window, $) {
    'use strict';

    var STORAGE_PREFIX = 'nps.list.columns.';

    function readSaved(key) {
        try {
            var saved = JSON.parse(window.localStorage.getItem(STORAGE_PREFIX + key));
            return saved && typeof saved === 'object' && !Array.isArray(saved) ? saved : null;
        } catch (e) {
            return null;
        }
    }

    function writeSaved(key, map) {
        try {
            window.localStorage.setItem(STORAGE_PREFIX + key, JSON.stringify(map));
        } catch (e) { /* storage unavailable: the defaults apply on the next visit */ }
    }

    function leafColumns(table) {
        var rows = table.bootstrapTable('getOptions').columns || [];
        return [].concat.apply([], rows).filter(function (c) { return c && c.field && !c.checkbox; });
    }

    function fieldValue(row, field) {
        return field.split('.').reduce(function (v, k) { return v == null ? undefined : v[k]; }, row);
    }

    function isOptionColumn(column) { return column.field === 'option'; }

    // init starts the table with the saved column choice applied to options.columns.
    // opts.key names the page; opts.columns need not carry the pinned option class.
    function init(table, options, opts) {
        table = $(table);
        var key = opts.key, saved = readSaved(key);
        options.columns.forEach(function (column) {
            if (!column.field || column.checkbox) return;
            if (isOptionColumn(column)) {
                column.switchable = false;
                column.class = ((column.class || '') + ' npslist-action').trim();
                return;
            }
            if (saved && typeof saved[column.field] === 'boolean' && column.switchable !== false) column.visible = saved[column.field];
        });
        table.on('column-switch.bs.table column-switch-all.bs.table', function () {
            var map = {};
            leafColumns(table).forEach(function (c) {
                if (c.switchable !== false) map[c.field] = !!c.visible;
            });
            writeSaved(key, map);
        });
        table.bootstrapTable(options);
        new Enhancer(table);
        return table;
    }

    // detailColumns renders the given columns of a row for the detail view, with
    // the formatter of the column, so that a column hidden from the table is
    // still readable there. Fields without a column on this page are skipped.
    function detailColumns(table, row, index, fields) {
        table = $(table);
        var columns = leafColumns(table), html = '';
        fields.forEach(function (field) {
            var column = columns.filter(function (c) { return c.field === field; })[0];
            if (!column) return;
            var value = fieldValue(row, field);
            var text = typeof column.formatter === 'function' ? column.formatter.call(column, value, row, index, field) : escapeHtml(value == null ? '' : value);
            html += '<span class="detail-column" data-detail-column="' + escapeHtml(field) + '"><b>' + column.title + '</b>: <span class="detail-column-value">' + text + '</span></span>&emsp;';
        });
        return html;
    }

    function Enhancer(table) {
        var self = this;
        self.table = table;
        table.data('npsListEnhancer', self);
        self.host = table.closest('.bootstrap-table');
        // the class goes on an element bootstrap-table leaves alone
        self.page = table.closest('.ibox-content, .content');
        if (!self.page.length) self.page = self.host.parent();
        self.scroller = table.closest('.fixed-table-body');
        self.head = null;
        self.bar = null;
        self.pending = 0;
        self.page.addClass('npslist');
        self.build();
        table.on('post-body.bs.table reset-view.bs.table column-switch.bs.table column-switch-all.bs.table sort.bs.table ' +
            'check.bs.table uncheck.bs.table check-all.bs.table uncheck-all.bs.table expand-row.bs.table collapse-row.bs.table ' +
            'load-success.bs.table toggle.bs.table', function () { self.schedule(true); });
        self.bar.on('scroll', function () { self.syncFromBar(); });
        // the page scrolls in #wrapper; capture catches any scroller on the page
        document.addEventListener('scroll', function (e) {
            if (e.target === self.table.closest('.fixed-table-body')[0]) self.scroller = $(e.target), self.syncFromScroller();
            self.schedule(false);
        }, true);
        $(window).on('resize orientationchange', function () { self.schedule(true); });
        if (window.ResizeObserver) {
            var observer = new ResizeObserver(function () { self.schedule(true); });
            observer.observe(table[0]);
            observer.observe(self.page[0]);
        }
        if (window.MutationObserver) {
            // sorting marks, translations and column switches change the header
            new MutationObserver(function () { self.schedule(true); })
                .observe(table.find('thead')[0] || table[0], {subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ['class']});
        }
        self.schedule(true);
    }

    Enhancer.prototype.build = function () {
        var self = this;
        self.head = $('<div class="npslist-head" aria-hidden="true" inert></div>').hide().appendTo(document.body);
        self.bar = $('<div class="npslist-hbar" aria-hidden="true"><div class="npslist-hbar-fill"></div></div>').hide().appendTo(document.body);
        // a click on the floating header acts on the real header cell at the same position
        self.head.on('click', 'th', function (e) {
            var index = $(this).index();
            var real = self.table.find('> thead > tr').last().children('th').eq(index);
            if (!real.length) return;
            // bootstrap-table listens on the cell content, not on the cell
            var box = real.find('input[type=checkbox]').first(), inner = real.find('.th-inner').first();
            if (box.length) {
                box[0].click();
            } else if (inner.length) {
                inner[0].click();
            }
        });
    };

    Enhancer.prototype.schedule = function (rebuild) {
        var self = this;
        if (rebuild) self.dirty = true;
        if (self.pending) return;
        // a timer backs the frame up: hidden tabs do not run animation frames
        function run() {
            if (!self.pending) return;
            self.pending = 0;
            self.update();
        }
        self.pending = 1;
        window.requestAnimationFrame(run);
        window.setTimeout(run, 60);
    };

    Enhancer.prototype.cardView = function () {
        var options = this.table.bootstrapTable('getOptions');
        return !!options.cardView || this.page.find('.card-views').length > 0;
    };

    Enhancer.prototype.topOffset = function () {
        var nav = document.querySelector('.navbar-fixed-top');
        if (nav && getComputedStyle(nav).position === 'fixed') return Math.max(0, nav.getBoundingClientRect().bottom);
        return 0;
    };

    Enhancer.prototype.update = function () {
        var self = this;
        self.scroller = self.table.closest('.fixed-table-body');
        var off = !self.table.is(':visible') || self.cardView();
        self.page.toggleClass('npslist-card', self.cardView());
        if (off) {
            self.head.hide();
            self.bar.hide();
            self.dirty = false;
            return;
        }
        var el = self.scroller[0], vh = window.innerHeight;
        var rect = el.getBoundingClientRect();
        var overflowing = el.scrollWidth > el.clientWidth + 1;

        // floating header
        var thead = self.table.find('> thead')[0];
        var show = false;
        if (thead) {
            var headRect = thead.getBoundingClientRect(), top = self.topOffset();
            var tableRect = self.table[0].getBoundingClientRect();
            show = headRect.top < top && tableRect.bottom - headRect.height > top && rect.top < vh;
            if (show) {
                if (self.dirty || !self.head.children().length) self.rebuildHead(thead);
                var inner = self.head.children('table');
                self.head.css({top: top + 'px', left: rect.left + 'px', width: el.clientWidth + 'px'}).show();
                inner.css('transform', 'translateX(' + (-el.scrollLeft) + 'px)');
            }
        }
        if (!show) self.head.hide();

        // fixed horizontal scroll bar, only while the real one is below the screen
        var barShow = overflowing && rect.bottom > vh && rect.top < vh - 40;
        if (barShow) {
            self.bar.css({left: rect.left + 'px', width: el.clientWidth + 'px'}).show();
            self.bar.children().css('width', el.scrollWidth + 'px');
            if (self.bar[0].scrollLeft !== el.scrollLeft) self.bar[0].scrollLeft = el.scrollLeft;
        } else {
            self.bar.hide();
        }
        self.dirty = false;
    };

    Enhancer.prototype.rebuildHead = function (thead) {
        var table = this.table, clone = $('<table></table>').attr('class', table.attr('class')).addClass('npslist-head-table');
        var copy = $(thead).clone(false, false);
        copy.find('[id]').removeAttr('id');
        copy.find('[tabindex]').removeAttr('tabindex');
        var realCells = $(thead).find('> tr').last().children('th');
        copy.children('tr').last().children('th').each(function (i) {
            var real = realCells.eq(i);
            if (real.length) $(this).css({width: real[0].getBoundingClientRect().width + 'px', minWidth: real[0].getBoundingClientRect().width + 'px'});
            var box = $(this).find('input[type=checkbox]')[0], realBox = real.find('input[type=checkbox]')[0];
            if (box && realBox) box.checked = realBox.checked;
        });
        clone.append(copy).css({width: table[0].getBoundingClientRect().width + 'px', tableLayout: 'fixed'});
        this.head.empty().append(clone);
    };

    Enhancer.prototype.syncFromScroller = function () {
        var el = this.scroller[0];
        if (this.bar.is(':visible') && this.bar[0].scrollLeft !== el.scrollLeft) this.bar[0].scrollLeft = el.scrollLeft;
        if (this.head.is(':visible')) this.head.children('table').css('transform', 'translateX(' + (-el.scrollLeft) + 'px)');
    };

    Enhancer.prototype.syncFromBar = function () {
        var el = this.scroller[0];
        if (el.scrollLeft !== this.bar[0].scrollLeft) el.scrollLeft = this.bar[0].scrollLeft;
    };

    window.npsList = {init: init, detailColumns: detailColumns};
})(window, jQuery);
