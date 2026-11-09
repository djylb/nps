/* Floating "back to top" button shared by all pages of the layout. The page
 * scrolls inside div.wrapper-content (the window itself does not), so that is
 * the element watched and scrolled. The button sits at the lower right of the
 * visible page area (the footer lies below it) and, on lists, above the
 * horizontal scroll bar (list-table.js announces it with npslist:hbar). */
(function (window, $) {
    'use strict';

    var SHOW_AFTER = 300; // px scrolled before the button appears
    var GAP = 16;
    var button, hbar = 0, pending = false;

    function scroller() {
        return document.querySelector('.wrapper-content');
    }

    function scrolled() {
        var el = scroller();
        return el ? el.scrollTop : (window.pageYOffset || document.documentElement.scrollTop || 0);
    }

    function place() {
        pending = false;
        if (!button) return;
        var el = scroller(), right = GAP, bottom = GAP;
        if (el) {
            var box = el.getBoundingClientRect();
            right = window.innerWidth - (box.left + el.clientWidth) + GAP;
            bottom = window.innerHeight - Math.min(box.bottom, window.innerHeight) + GAP;
        }
        button.style.right = right + 'px';
        button.style.bottom = (bottom + hbar) + 'px';
        button.classList.toggle('is-visible', scrolled() > SHOW_AFTER);
    }

    function schedule() {
        if (pending) return;
        pending = true;
        window.requestAnimationFrame(place);
        window.setTimeout(function () { if (pending) place(); }, 60);
    }

    function toTop() {
        var el = scroller();
        var calm = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
        var options = {top: 0, behavior: calm ? 'auto' : 'smooth'};
        if (el) el.scrollTo(options); else window.scrollTo(options);
    }

    $(function () {
        button = document.getElementById('back-to-top');
        if (!button) return;
        button.addEventListener('click', toTop);
        // scroll does not bubble; capture sees the page scroller and the window
        document.addEventListener('scroll', schedule, true);
        window.addEventListener('resize', schedule);
        window.addEventListener('orientationchange', schedule);
        window.addEventListener('npslist:hbar', function (e) {
            hbar = e.detail && e.detail.visible ? e.detail.height : 0;
            schedule();
        });
        if (window.ResizeObserver) {
            var observer = new ResizeObserver(schedule);
            observer.observe(document.body);
            var el = scroller();
            if (el) observer.observe(el);
        }
        place();
    });
})(window, jQuery);
