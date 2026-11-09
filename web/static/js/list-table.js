/* Shared behaviour of the bootstrap-table lists (clients, tunnels, domains):
 * per-page column choice kept in localStorage, a pinned option column, a
 * header that stays in view while the page scrolls, and a horizontal scroll
 * bar fixed to the bottom of the screen. The page scrolls inside #wrapper and
 * the table scrolls inside .fixed-table-body, both of which clip, so the
 * header and the bar are floating copies kept in step by script.
 * Below CARD_MAX_WIDTH the table is shown as cards, following the window
 * width; card view turns the floating parts and the pinned column off. */
(function (window, $) {
    'use strict';

    var STORAGE_PREFIX = 'nps.list.columns.';
    // windows narrower than this show the rows as cards
    var CARD_MAX_WIDTH = 768;

    function narrowScreen() {
        if (window.matchMedia) return window.matchMedia('(max-width: ' + (CARD_MAX_WIDTH - 1) + 'px)').matches;
        return window.innerWidth < CARD_MAX_WIDTH;
    }

    // followScreen switches the table between card view and table view when the
    // window crosses CARD_MAX_WIDTH, keeping the loaded page, the selection and
    // the expanded state that toggleView itself keeps.
    function followScreen(table) {
        var pending = false;
        function sync() {
            pending = false;
            if (!table.data('bootstrap.table')) return;
            if (!!table.bootstrapTable('getOptions').cardView === narrowScreen()) return;
            // toggleView draws the rows again, which closes the detail rows
            var open = table.find('> tbody > tr.detail-view').map(function () { return $(this).prev().attr('data-index'); }).get();
            table.bootstrapTable('toggleView');
            open.forEach(function (index) { table.bootstrapTable('expandRow', Number(index)); });
        }
        function schedule() {
            if (pending) return;
            pending = true;
            window.requestAnimationFrame(sync);
            window.setTimeout(function () { if (pending) sync(); }, 60);
        }
        $(window).on('resize orientationchange', schedule);
        if (window.matchMedia) {
            var query = window.matchMedia('(max-width: ' + (CARD_MAX_WIDTH - 1) + 'px)');
            if (query.addEventListener) query.addEventListener('change', schedule);
        }
    }

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
        options.cardView = narrowScreen();
        table.bootstrapTable(options);
        new Enhancer(table);
        cardControls(table);
        followScreen(table);
        return table;
    }

    // detailColumns renders the given columns of a row for the detail view, with
    // the formatter of the column, so that a column hidden from the table is
    // still readable there. Fields without a column on this page are skipped,
    // and so are columns the page leaves out of the detail view: detail: false,
    // or a function(row, index) returning false for a row whose value is shown
    // elsewhere. The caller shows no heading when nothing is left.
    function detailColumns(table, row, index, fields) {
        table = $(table);
        var columns = leafColumns(table), html = '';
        fields.forEach(function (field) {
            var column = columns.filter(function (c) { return c.field === field; })[0];
            if (!column || column.detail === false) return;
            if (typeof column.detail === 'function' && !column.detail(row, index)) return;
            var value = fieldValue(row, field);
            var text = typeof column.formatter === 'function' ? column.formatter.call(column, value, row, index, field) : escapeHtml(value == null ? '' : value);
            html += '<span class="detail-column" data-detail-column="' + escapeHtml(field) + '"><b>' + column.title + '</b>: <span class="detail-column-value">' + text + '</span></span>&emsp;';
        });
        return html;
    }

    // cardControls gives card view what the table has in its header: an
    // expand button per card (bootstrap-table draws no detail icon there), a
    // select-all box and a sort choice, which has no header to click in card
    // view and calls the table's own sortBy. All are plain buttons and boxes,
    // reachable by touch and keyboard; CSS shows the bar only in card view.
    function cardControls(table) {
        var hasBoxes = (table.bootstrapTable('getOptions').columns || []).some(function (row) {
            return row.some(function (c) { return c && c.checkbox; });
        });
        var all = null;
        function sortBy(field, order) {
            table.bootstrapTable('sortBy', {field: field, sortOrder: order});
        }
        function fillSort(bar) {
            var options = table.bootstrapTable('getOptions');
            var select = bar.find('.npslist-sort'), count = 0;
            select.empty().append($('<option value="" langtag="word-sortby">Sort by</option>'));
            table.bootstrapTable('getVisibleColumns').forEach(function (c) {
                if (!c.sortable || !c.field) return;
                // the card's own title is the translated column title
                var title = $.trim(table.find('> tbody .card-view-field-' + c.field.replace(/\./g, '\\.') + ' .card-view-title').first().text());
                if (!title) return;
                select.append($('<option></option>').val(c.field).text(title));
                count++;
            });
            select.val(options.sortName || '');
            bar.find('.npslist-sort-dir i').attr('class', 'fa ' + (options.sortOrder === 'desc' ? 'fa-sort-amount-down' : 'fa-sort-amount-up'));
            bar.find('.npslist-sort-dir').prop('disabled', !options.sortName);
            bar.find('.npslist-card-sort').toggle(count > 0);
        }
        // bootstrap-table builds its container again on a locale change, so the
        // bar is checked after every body draw
        function ensureBar() {
            var host = table.closest('.bootstrap-table');
            all = null;
            if (!host.length) return;
            var bar = host.children('.npslist-card-bar');
            if (!bar.length) {
                bar = $('<div class="npslist-card-bar"></div>');
                if (hasBoxes) {
                    // the same box markup as the row boxes, so the theme styles it alike
                    var id = 'npslist-all-' + Math.random().toString(36).slice(2);
                    bar.append('<span class="npslist-card-all"><label><input type="checkbox" id="' + id + '"><span></span></label>' +
                        '<label for="' + id + '" langtag="word-selectall">Select all</label></span>');
                    bar.find('input').on('change', function () { table.bootstrapTable(this.checked ? 'checkAll' : 'uncheckAll'); });
                }
                bar.append('<span class="npslist-card-sort"><select class="form-control npslist-sort"></select>' +
                    '<button type="button" class="btn btn-default npslist-sort-dir"><i aria-hidden="true" class="fa fa-sort-amount-up"></i><span class="sr-only" langtag="word-sortdirection">Sort direction</span></button></span>');
                bar.find('.npslist-sort').on('change', function () {
                    var field = $(this).val(), options = table.bootstrapTable('getOptions');
                    if (field && field !== options.sortName) sortBy(field, 'asc');
                });
                bar.find('.npslist-sort-dir').on('click', function () {
                    var options = table.bootstrapTable('getOptions');
                    if (options.sortName) sortBy(options.sortName, options.sortOrder === 'asc' ? 'desc' : 'asc');
                });
                host.find('.fixed-table-container').first().before(bar);
            }
            fillSort(bar);
            if ($.fn.setLang && window.languages && window.languages.content) $('body').setLang('.npslist-card-bar');
            all = bar.find('.npslist-card-all input');
        }
        function syncAll() {
            if (!all || !all.length) return;
            var boxes = table.find('> tbody input[name=btSelectItem]:not(:disabled)');
            all.prop('checked', boxes.length > 0 && boxes.filter(':checked').length === boxes.length);
        }
        function expandState(index, open) {
            var button = table.find('> tbody > tr[data-index="' + index + '"] .npslist-expand');
            button.attr('aria-expanded', String(open));
            button.find('i').toggleClass('fa-chevron-down', !open).toggleClass('fa-chevron-up', open);
        }
        table.on('post-body.bs.table', function () {
            var options = table.bootstrapTable('getOptions');
            ensureBar();
            syncAll();
            if (!options.cardView || !options.detailView) return;
            table.find('> tbody > tr[data-index]').each(function () {
                var tr = $(this), cards = tr.find('.card-views').first();
                if (!cards.length || cards.find('.npslist-expand').length) return;
                var open = tr.next().is('tr.detail-view');
                // the click stays on the button: the table's cell handler would look up a column for it
                var button = $('<button type="button" class="btn btn-default npslist-expand"></button>').attr('aria-expanded', String(open))
                    .append('<i class="fa ' + (open ? 'fa-chevron-up' : 'fa-chevron-down') + '" aria-hidden="true"></i> ')
                    .append('<span langtag="word-details">Details</span>')
                    .on('click', function (e) {
                        e.stopPropagation();
                        table.bootstrapTable('toggleDetailView', Number(tr.attr('data-index')));
                    });
                cards.append($('<div class="card-view npslist-card-more"></div>').append(button));
            });
            if ($.fn.setLang && window.languages && window.languages.content) $('body').setLang('.npslist-expand');
        });
        table.on('check.bs.table uncheck.bs.table check-all.bs.table uncheck-all.bs.table', syncAll);
        table.on('expand-row.bs.table', function (e, index) {
            expandState(index, true);
            // a card is one cell spanning the columns; the detail cell must span as many
            if (!table.bootstrapTable('getOptions').cardView) return;
            var tr = table.find('> tbody > tr[data-index="' + index + '"]');
            tr.next('tr.detail-view').children('td').attr('colspan', tr.children('td').first().attr('colspan') || 1);
        });
        table.on('collapse-row.bs.table', function (e, index) { expandState(index, false); });
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
