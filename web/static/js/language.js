(function ($) {
    function xml2json(Xml) {
        var tempvalue, tempJson = {};
        $(Xml).each(function () {
            var tagName = ($(this).attr('id') || this.tagName);
            tempvalue = (this.childElementCount == 0) ? this.textContent : xml2json($(this).children());
            switch ($.type(tempJson[tagName])) {
                case 'undefined':
                    tempJson[tagName] = tempvalue;
                    break;
                case 'object':
                    tempJson[tagName] = Array(tempJson[tagName]);
                case 'array':
                    tempJson[tagName].push(tempvalue);
            }
        });
        return tempJson;
    }

    // Only an explicit choice from the language menu is persisted;
    // otherwise the browser preference decides on every visit.
    var LANG_KEY = 'nps-lang';

    function loadSavedLang() {
        try {
            return localStorage.getItem(LANG_KEY);
        } catch (e) {
            return null;
        }
    }

    function saveLang(lang) {
        try {
            localStorage.setItem(LANG_KEY, lang);
        } catch (e) {}
    }

    function matchBrowserLang(menu) {
        var prefs = (navigator.languages && navigator.languages.length) ? navigator.languages
            : [navigator.language || navigator.browserLanguage || ''];
        for (var i = 0; i < prefs.length; i++) {
            var pref = String(prefs[i] || '').toLowerCase();
            if (!pref) continue;
            var primary = pref.split('-')[0];
            var partial = null;
            for (var key in menu) {
                var k = key.toLowerCase();
                if (k == pref) return key;
                if (partial == null && k.split('-')[0] == primary) partial = key;
            }
            if (partial != null) return partial;
        }
        return null;
    }

    function setchartlang(langobj, chartobj) {
        if ($.type(langobj) == 'string') return langobj;
        if ($.type(langobj) == 'chartobj') return false;
        var flag = true;
        for (key in langobj) {
            var item = key;
            children = (chartobj.hasOwnProperty(item)) ? setchartlang(langobj[item], chartobj[item]) : setchartlang(langobj[item], undefined);
            switch ($.type(children)) {
                case 'string':
                    if ($.type(chartobj[item]) != 'string') continue;
                case 'object':
                    chartobj[item] = (children['value'] || children);
                default:
                    flag = false;
            }
        }
        if (flag) {
            return {'value': (langobj[languages['current']] || langobj[languages['default']] || 'N/A')}
        }
    }

    $.fn.cloudLang = function () {
        $.ajax({
            type: 'GET',
            url: window.nps.web_base_url + '/static/page/languages.xml?v=' + window.nps.version,
            dataType: 'xml',
            success: function (xml) {
                languages['content'] = xml2json($(xml).children())['content'];
                languages['menu'] = languages['content']['languages'];
                languages['default'] = languages['content']['default'];
                for (var key in languages['menu']) {
                    $('#languagemenu').next().append('<li lang="' + key + '"><a><img src="' + window.nps.web_base_url + '/static/img/flag/' + key + '.png"> ' + languages['menu'][key] + '</a></li>');
                }
                var saved = loadSavedLang();
                if (!saved || !languages['menu'].hasOwnProperty(saved)) {
                    saved = null;
                }
                languages['current'] = (saved || matchBrowserLang(languages['menu']) || languages['default']);
                $('#languagemenu').attr('lang', languages['current']);
                $('body').setLang('');

                if ($.fn.selectpicker != null) {
                    $('.selectpicker').selectpicker('refresh');
                }
            }
        });
    };

    $.fn.chooseLang = function (lang) {
        saveLang(lang);
        $('#languagemenu').attr('lang', lang);
        return this.setLang('');
    };

    $.fn.setLang = function (dom) {
        languages['current'] = $('#languagemenu').attr('lang');
        if (dom == '') {
            $('#languagemenu span').text(' ' + languages['menu'][languages['current']]);
            if ($("#table").length > 0) $('#table').bootstrapTable('refreshOptions', {'locale': languages['current']});
        }
        $.each($(dom + ' [langtag]'), function (i, item) {
            var index = $(item).attr('langtag');
            string = languages['content'][index.toLowerCase()];
            switch ($.type(string)) {
                case 'string':
                    break;
                case 'array':
                    string = string[Math.floor((Math.random() * string.length))];
                case 'object':
                    string = (string[languages['current']] || string[languages['default']] || null);
                    break;
                default:
                    string = 'Missing language string "' + index + '"';
                    $(item).css('background-color', '#ffeeba');
            }
            if ($.type($(item).attr('placeholder')) == 'undefined') {
                $(item).text(string);
            } else {
                $(item).attr('placeholder', string);
            }
        });

        if (!$.isEmptyObject(chartdatas)) {
            setchartlang(languages['content']['charts'], chartdatas);
            for (var key in chartdatas) {
                if ($('#' + key).length == 0) continue;
                if ($.type(chartdatas[key]) == 'object')
                    charts[key] = echarts.init(document.getElementById(key));
                charts[key].setOption(chartdatas[key], true);
            }
        }
        
        if(window.hasOwnProperty('internationalized')){
            internationalized(languages['current']);
        }
    }

})(jQuery);

(function () {
    var KEY = 'nps-theme';
    var html = document.documentElement;
    var saved = null;

    try {
        saved = localStorage.getItem(KEY);
    } catch (e) {}

    function setThemeAttr(mode) {
        if (mode === 'dark') html.setAttribute('theme', 'dark-mode');
        else html.removeAttribute('theme');
    }

    if (saved === 'dark' || saved === 'light') {
        setThemeAttr(saved);
    } else {
        var mql = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)');
        setThemeAttr(mql && mql.matches ? 'dark' : 'light');

        if (mql) {
            var onChange = function (e) { setThemeAttr(e.matches ? 'dark' : 'light'); };
            if (mql.addEventListener) mql.addEventListener('change', onChange);
            else if (mql.addListener) mql.addListener(onChange);
        }
    }
})();

$(document).ready(function () {
    const icon = document.querySelector('#theme-toggle i');

    function syncIconFromHtmlTheme() {
        const isDark = document.documentElement.getAttribute('theme') === 'dark-mode';
        if (icon) {
            icon.classList.toggle('fa-sun',  isDark);
            icon.classList.toggle('fa-moon', !isDark);
        }
    }

    syncIconFromHtmlTheme();

    new MutationObserver(syncIconFromHtmlTheme).observe(document.documentElement, {
        attributes: true,
        attributeFilter: ['theme']
    });

    $('body').cloudLang();
    $('body').on('click', 'li[lang]', function () {
        $('body').chooseLang($(this).attr('lang'));
    });
});

var languages = {};
var charts = {};
var chartdatas = {};
var submitLocks = [];

function langreply(langstr) {
    langstr = (langstr == null) ? '' : String(langstr);
    var replies = (languages['content'] && languages['content']['reply']) || {};
    var langobj = replies[langstr.replace(/[\s,.?]*/g, "").toLowerCase()];
    if ($.type(langobj) == 'undefined') return langstr
    langobj = (langobj[languages['current']] || langobj[languages['default']] || langstr);
    return langobj
}

function goback() {
    history.back();
}

// A submission is locked per form (all of its buttons) or, outside a form,
// per clicked element, so repeated clicks cannot send the same request twice.
// The lock is released on failure; on success it stays until the page
// navigates away or reloads.
function submitScope(trigger) {
    var $trigger = $(trigger || (window.event && window.event.currentTarget) || []);
    var $form = $trigger.closest('form');
    if ($form.length) return {scope: $form, controls: $form.find('button')};
    if ($trigger.length) return {scope: $trigger, controls: $trigger};
    return {scope: $(document), controls: $()};
}

function lockSubmit(lock) {
    lock.scope.data('submitting', true);
    lock.controls.prop('disabled', true).addClass('disabled');
    submitLocks.push(lock);
}

function unlockSubmit(lock) {
    lock.scope.removeData('submitting');
    lock.controls.prop('disabled', false).removeClass('disabled');
    var idx = submitLocks.indexOf(lock);
    if (idx >= 0) submitLocks.splice(idx, 1);
}

// A page restored from the back/forward cache must not keep stale locks.
$(window).on('pageshow', function (e) {
    if (e.originalEvent && e.originalEvent.persisted) {
        $.each(submitLocks.slice(), function (i, lock) {
            unlockSubmit(lock);
        });
    }
});

function submitform(action, url, postdata, trigger) {
    var lock = submitScope(trigger);
    if (lock.scope.data('submitting')) return;
    var reload = false;
    switch (action) {
        case 'turn':
        case 'clear':
        case 'delete':
        case 'update':
            var langobj = languages['content']['confirm'][action];
            var question = (langobj[languages['current']] || langobj[languages['default']] || 'Are you sure you want to ' + action + ' it?');
            if (!confirm(question)) return;
        case 'start':
        case 'stop':
        case 'global':
            reload = true;
        case 'add':
        case 'edit':
            break;
        default:
            return;
    }
    $.each(postdata, function (i, v) {
        if (v['value']) {
            v['value'] = v['value'].trim();
        }
    });
    lockSubmit(lock);
    var succeeded = false;
    $.ajax({
        type: "POST",
        url: url,
        data: postdata,
        success: function (res) {
            if (res && res.status) {
                succeeded = true;
                showMsg(langreply(res.msg), 'success', 1000, function () {
                    if (reload) {
                        document.location.reload();
                    } else {
                        window.location.href = document.referrer;
                    }
                });
            } else {
                showMsg(langreply(res && res.msg), 'error', 5000);
            }
        },
        error: function (xhr, textStatus) {
            showMsg(langreply(xhr.statusText || textStatus || 'error'), 'error', 5000);
        },
        complete: function () {
            if (!succeeded) unlockSubmit(lock);
        }
    });
}

// compareVersion compares dotted versions numerically segment by segment,
// ignoring a leading "v" and build metadata; a pre-release suffix sorts
// before the release with the same numbers. It matches version.Compare.
function compareVersion(a, b) {
    var split = function (v) {
        v = String(v == null ? '' : v).trim().replace(/^v/, '').split('+')[0];
        var i = v.indexOf('-');
        var core = i < 0 ? v : v.slice(0, i);
        return {nums: core.split('.').map(function (p) { return /^\d+$/.test(p) ? parseInt(p, 10) : 0; }), pre: i < 0 ? '' : v.slice(i + 1)};
    };
    var x = split(a), y = split(b);
    for (var i = 0; i < Math.max(x.nums.length, y.nums.length); i++) {
        var p = x.nums[i] || 0, q = y.nums[i] || 0;
        if (p !== q) return p < q ? -1 : 1;
    }
    if (x.pre === y.pre) return 0;
    if (x.pre === '') return 1;
    if (y.pre === '') return -1;
    return x.pre < y.pre ? -1 : 1;
}

// actualFlow estimates the traffic a flow costs the server: nps counts the
// visitor side once each way, and every byte also crosses the link to the
// client, so the total is doubled.
function actualFlow(flow) {
    return flow ? (flow.InletFlow + flow.ExportFlow) * 2 : 0;
}

// flowRemain returns what is left of a flow's limit, which counts the
// in/out traffic or, for a client with flow_limit_type=actual, the actual
// usage.
function flowRemain(flow, isClient) {
    if (!flow) return 0;
    var used = (isClient && window.nps && window.nps.flow_limit_actual) ? actualFlow(flow) : flow.InletFlow + flow.ExportFlow;
    return flow.FlowLimit * 1024 * 1024 - used;
}

function changeunit(limit) {
    const sign = limit < 0 ? "-" : "";
    const abs = Math.abs(limit);
    let size = "";
    if (abs < 0.1 * 1024) {
        size = abs.toFixed(2) + "B";
    } else if (abs < 0.1 * 1024 * 1024) {
        size = (abs / 1024).toFixed(2) + "KB";
    } else if (abs < 0.1 * 1024 * 1024 * 1024) {
        size = (abs / (1024 * 1024)).toFixed(2) + "MB";
    } else if (abs < 0.1 * 1024 * 1024 * 1024 * 1024) {
        size = (abs / (1024 * 1024 * 1024)).toFixed(2) + "GB";
    } else {
        size = (abs / (1024 * 1024 * 1024 * 1024)).toFixed(2) + "TB";
    }

    const idx = size.indexOf(".");
    const dec = size.substr(idx + 1, 2);
    if (dec === "00") {
        size = size.substring(0, idx) + size.substr(idx + 3);
    }
    return sign + size;
}

function getRemainingTime(str){
  if(str==='0001-01-01T00:00:00Z') return {totalMs:Infinity,days:Infinity,hours:Infinity,minutes:Infinity,seconds:Infinity,formatted:'∞'};
  const exp = new Date(str), diff = exp - Date.now();
  if(diff <= 0) return {totalMs:0,days:0,hours:0,minutes:0,seconds:0,formatted:'0'};
  const s       = Math.floor(diff/1e3),
        days    = Math.floor(s/86400),
        hours   = Math.floor((s%86400)/3600),
        minutes = Math.floor((s%3600)/60),
        seconds = s%60,
        parts   = [days?days+'d':null,hours?hours+'h':null,minutes?minutes+'m':null,seconds+'s'].filter(Boolean),
        formatted = parts.slice(0,2).join(' ');
  return {totalMs:diff,days,hours,minutes,seconds,formatted};
}

function oCopy(obj){
    var tempInput = document.createElement("input");
    document.body.appendChild(tempInput);
    tempInput.value = obj.innerText || obj.textContent;
    tempInput.select();
    document.execCommand('copy');
    document.body.removeChild(tempInput);
    showMsg(langreply('Copied'));
}

function copyText(text) {
    const textarea = document.createElement("textarea");
    textarea.value = text;
    document.body.appendChild(textarea);
    textarea.select();
    document.execCommand("copy");
    document.body.removeChild(textarea);
    showMsg(langreply('Copied'));
}

function showMsg(text, type = 'success', dur = 1500, cb) {
    var old = document.getElementById('wangmarket_loading');
    if (old) old.parentNode.removeChild(old);
    var isLong = text && text.length > 5;

    var svg;
    if (type === 'error') {
        svg = '<svg style="width:3rem;height:3rem;padding:1rem;box-sizing:content-box;" viewBox="0 0 1024 1024" xmlns="http://www.w3.org/2000/svg"><path d="M696.832 326.656c-12.8-12.8-33.28-12.8-46.08 0L512 465.92 373.248 327.168c-12.8-12.8-33.28-12.8-46.08 0s-12.8 33.28 0 46.08L466.432 512l-139.264 139.264c-12.8 12.8-12.8 33.28 0 46.08s33.28 12.8 46.08 0L512 558.08l138.752 139.264c12.288 12.8 32.768 12.8 45.568 0.512l0.512-0.512c12.8-12.8 12.8-33.28 0-45.568L557.568 512l139.264-139.264c12.8-12.8 12.8-33.28 0-46.08 0 0.512 0 0 0 0zM512 51.2c-254.464 0-460.8 206.336-460.8 460.8s206.336 460.8 460.8 460.8 460.8-206.336 460.8-460.8-206.336-460.8-460.8-460.8z m280.064 740.864c-74.24 74.24-175.104 116.224-280.064 115.712-104.96 0-205.824-41.472-280.064-115.712S115.712 616.96 115.712 512s41.472-205.824 116.224-280.064C306.176 157.696 407.04 115.712 512 116.224c104.96 0 205.824 41.472 280.064 116.224 74.24 74.24 116.224 175.104 115.712 280.064 0.512 104.448-41.472 205.312-115.712 279.552z" fill="#ffffff"></path></svg>';
    } else if (type === 'success') {
        svg = '<svg style="width:3rem;height:3rem;padding:1rem;box-sizing:content-box;" viewBox="0 0 1024 1024"><path d="M384 887.456L25.6 529.056 145.056 409.6 384 648.544 878.944 153.6 998.4 273.056z" fill="#ffffff"/></svg>';;
    } else {
        svg = '<img style="width:3rem;height:3rem;padding:1rem;box-sizing:content-box;" src="data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCAzMiAzMiIgd2lkdGg9IjY0IiBoZWlnaHQ9IjY0IiBmaWxsPSIjRjlGOUY5Ij4KICA8Y2lyY2xlIGN4PSIxNiIgY3k9IjMiIHI9IjAiPgogICAgPGFuaW1hdGUgYXR0cmlidXRlTmFtZT0iciIgdmFsdWVzPSIwOzM7MDswIiBkdXI9IjFzIiByZXBlYXRDb3VudD0iaW5kZWZpbml0ZSIgYmVnaW49IjAiIGtleVNwbGluZXM9IjAuMiAwLjIgMC40IDAuODswLjIgMC4yIDAuNCAwLjg7MC4yIDAuMiAwLjQgMC44IiBjYWxjTW9kZT0ic3BsaW5lIiAvPgogIDwvY2lyY2xlPgogIDxjaXJjbGUgdHJhbnNmb3JtPSJyb3RhdGUoNDUgMTYgMTYpIiBjeD0iMTYiIGN5PSIzIiByPSIwIj4KICAgIDxhbmltYXRlIGF0dHJpYnV0ZU5hbWU9InIiIHZhbHVlcz0iMDszOzA7MCIgZHVyPSIxcyIgcmVwZWF0Q291bnQ9ImluZGVmaW5pdGUiIGJlZ2luPSIwLjEyNXMiIGtleVNwbGluZXM9IjAuMiAwLjIgMC40IDAuODswLjIgMC4yIDAuNCAwLjg7MC4yIDAuMiAwLjQgMC44IiBjYWxjTW9kZT0ic3BsaW5lIiAvPgogIDwvY2lyY2xlPgogIDxjaXJjbGUgdHJhbnNmb3JtPSJyb3RhdGUoOTAgMTYgMTYpIiBjeD0iMTYiIGN5PSIzIiByPSIwIj4KICAgIDxhbmltYXRlIGF0dHJpYnV0ZU5hbWU9InIiIHZhbHVlcz0iMDszOzA7MCIgZHVyPSIxcyIgcmVwZWF0Q291bnQ9ImluZGVmaW5pdGUiIGJlZ2luPSIwLjI1cyIga2V5U3BsaW5lcz0iMC4yIDAuMiAwLjQgMC44OzAuMiAwLjIgMC40IDAuODswLjIgMC4yIDAuNCAwLjgiIGNhbGNNb2RlPSJzcGxpbmUiIC8+CiAgPC9jaXJjbGU+CiAgPGNpcmNsZSB0cmFuc2Zvcm09InJvdGF0ZSgxMzUgMTYgMTYpIiBjeD0iMTYiIGN5PSIzIiByPSIwIj4KICAgIDxhbmltYXRlIGF0dHJpYnV0ZU5hbWU9InIiIHZhbHVlcz0iMDszOzA7MCIgZHVyPSIxcyIgcmVwZWF0Q291bnQ9ImluZGVmaW5pdGUiIGJlZ2luPSIwLjM3NXMiIGtleVNwbGluZXM9IjAuMiAwLjIgMC40IDAuODswLjIgMC4yIDAuNCAwLjg7MC4yIDAuMiAwLjQgMC44IiBjYWxjTW9kZT0ic3BsaW5lIiAvPgogIDwvY2lyY2xlPgogIDxjaXJjbGUgdHJhbnNmb3JtPSJyb3RhdGUoMTgwIDE2IDE2KSIgY3g9IjE2IiBjeT0iMyIgcj0iMCI+CiAgICA8YW5pbWF0ZSBhdHRyaWJ1dGVOYW1lPSJyIiB2YWx1ZXM9IjA7MzswOzAiIGR1cj0iMXMiIHJlcGVhdENvdW50PSJpbmRlZmluaXRlIiBiZWdpbj0iMC41cyIga2V5U3BsaW5lcz0iMC4yIDAuMiAwLjQgMC44OzAuMiAwLjIgMC40IDAuODswLjIgMC4yIDAuNCAwLjgiIGNhbGNNb2RlPSJzcGxpbmUiIC8+CiAgPC9jaXJjbGU+CiAgPGNpcmNsZSB0cmFuc2Zvcm09InJvdGF0ZSgyMjUgMTYgMTYpIiBjeD0iMTYiIGN5PSIzIiByPSIwIj4KICAgIDxhbmltYXRlIGF0dHJpYnV0ZU5hbWU9InIiIHZhbHVlcz0iMDszOzA7MCIgZHVyPSIxcyIgcmVwZWF0Q291bnQ9ImluZGVmaW5pdGUiIGJlZ2luPSIwLjYyNXMiIGtleVNwbGluZXM9IjAuMiAwLjIgMC40IDAuODswLjIgMC4yIDAuNCAwLjg7MC4yIDAuMiAwLjQgMC44IiBjYWxjTW9kZT0ic3BsaW5lIiAvPgogIDwvY2lyY2xlPgogIDxjaXJjbGUgdHJhbnNmb3JtPSJyb3RhdGUoMjcwIDE2IDE2KSIgY3g9IjE2IiBjeT0iMyIgcj0iMCI+CiAgICA8YW5pbWF0ZSBhdHRyaWJ1dGVOYW1lPSJyIiB2YWx1ZXM9IjA7MzswOzAiIGR1cj0iMXMiIHJlcGVhdENvdW50PSJpbmRlZmluaXRlIiBiZWdpbj0iMC43NXMiIGtleVNwbGluZXM9IjAuMiAwLjIgMC40IDAuODswLjIgMC4yIDAuNCAwLjg7MC4yIDAuMiAwLjQgMC44IiBjYWxjTW9kZT0ic3BsaW5lIiAvPgogIDwvY2lyY2xlPgogIDxjaXJjbGUgdHJhbnNmb3JtPSJyb3RhdGUoMzE1IDE2IDE2KSIgY3g9IjE2IiBjeT0iMyIgcj0iMCI+CiAgICA8YW5pbWF0ZSBhdHRyaWJ1dGVOYW1lPSJyIiB2YWx1ZXM9IjA7MzswOzAiIGR1cj0iMXMiIHJlcGVhdENvdW50PSJpbmRlZmluaXRlIiBiZWdpbj0iMC44NzVzIiBrZXlTcGxpbmVzPSIwLjIgMC4yIDAuNCAwLjg7MC4yIDAuMiAwLjQgMC44OzAuMiAwLjIgMC40IDAuOCIgY2FsY01vZGU9InNwbGluZSIgLz4KICA8L2NpcmNsZT4KICA8Y2lyY2xlIHRyYW5zZm9ybT0icm90YXRlKDE4MCAxNiAxNikiIGN4PSIxNiIgY3k9IjMiIHI9IjAiPgogICAgPGFuaW1hdGUgYXR0cmlidXRlTmFtZT0iciIgdmFsdWVzPSIwOzM7MDswIiBkdXI9IjFzIiByZXBlYXRDb3VudD0iaW5kZWZpbml0ZSIgYmVnaW49IjAuNXMiIGtleVNwbGluZXM9IjAuMiAwLjIgMC40IDAuODswLjIgMC4yIDAuNCAwLjg7MC4yIDAuMiAwLjQgMC44IiBjYWxjTW9kZT0ic3BsaW5lIiAvPgogIDwvY2lyY2xlPgo8L3N2Zz4K" />';
    }

    var clickable = (type === 'error' || type === 'success');
    var w = document.createElement('div');
    w.id = 'wangmarket_loading';
    w.style = 'position:fixed;top:0;z-index: 2147483647;width:100%;height:100%;display:flex;flex-direction:column;justify-content:center;align-items:center';
    w.innerHTML =
      '<div id="loading">'
        +'<div id="loading_box" style="background-color:#2e2d3c;border-radius:0.3rem;opacity:0.8;min-width:6rem;min-height:4.8rem;max-width:20rem;display:flex;flex-wrap:wrap;align-items:center;'
          +(!isLong? 'flex-direction:column;':'') + (clickable ? 'cursor:pointer;' : '') +'">'
          +'<div style="display:flex;">'+svg+'</div>'
          +'<div style="font-size:1rem;box-sizing:border-box;color:white;flex:1;padding:1rem;'+(!isLong? 'padding-top:0;':'')+'">'
            + text +
          '</div>'
        +'</div>'
      +'</div>';
    document.body.appendChild(w);

    var timer = setTimeout(function(){
        if (w && w.parentNode) {
            w.parentNode.removeChild(w);
        }
        if (typeof cb === 'function') cb();
    }, dur);

    if (clickable) {
      var box = document.getElementById('loading_box');
      if (box) {
        box.addEventListener('click', function () {
          clearTimeout(timer);
          if (w && w.parentNode) {
            w.parentNode.removeChild(w);
          }
          if (typeof cb === 'function') cb();
        }, { once: true, passive: true });
      }
    }
}

function toggleTheme() {
    const html = document.documentElement;
    const icon = document.querySelector('#theme-toggle i');
    const isDark = html.getAttribute('theme') === 'dark-mode';

    if (isDark) {
        html.removeAttribute('theme');
        icon.classList.remove('fa-sun');
        icon.classList.add('fa-moon');
        localStorage.setItem('nps-theme', 'light');
    } else {
        html.setAttribute('theme', 'dark-mode');
        icon.classList.remove('fa-moon');
        icon.classList.add('fa-sun');
        localStorage.setItem('nps-theme', 'dark');
    }
}

// accessListDetail renders the source access lists of a client, tunnel or
// host for a detail view.
function accessListDetail(row) {
    var list = function (items) {
        return items && items.length ? escapeHtml(items.join(', ')) : '-';
    };
    return '<b langtag="word-iplistpriority"></b>: <span langtag="' + (row.IpListPriority === 1 ? 'word-allowfirst' : 'word-denyfirst') + '"></span>&emsp;'
        + '<b langtag="word-blackiplist"></b>: ' + list(row.BlackIpList) + '&emsp;'
        + '<b langtag="word-whiteiplist"></b>: ' + list(row.WhiteIpList) + '&emsp;'
        + '<b langtag="word-registeredonly"></b>: <span langtag="word-' + (row.RegisteredOnly ? 'yes' : 'no') + '"></span>&emsp;<br/><br/>';
}

function escapeHtml(str) {
    return String(str).replace(/[&<>"']/g, function (s) {
        return ({'&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'})[s];
    });
}

function getBridgeMode(data) {
    if (typeof data !== 'string') return '';
    const parts = data.split(',', 2);
    const first = parts[0] || '';
    const second = parts[1] || '';
    const escapedFirst = escapeHtml(first).toUpperCase();
    const escapedSecond = escapeHtml(second).toUpperCase();
    if (!second || first === second) {
        return escapedFirst;
    }
    return escapedSecond + ' → ' + escapedFirst;
}

// A closed details element still submits its controls. Only hide categories
// whose fields are all hidden for the selected tunnel or domain mode.
function refreshResourceSections(form) {
    $(form).find('.resource-section').each(function () {
        this.hidden = !$(this).find('.form-group').toArray().some(function (group) {
            return group.style.display !== 'none';
        });
    });
    $(form).find('.resource-form-tools').toggle(
        $(form).find('details.resource-advanced:not([hidden])').length > 0
    );
}

function setResourceSectionsExpanded(form, expanded) {
    $(form).find('details.resource-advanced:not([hidden])').prop('open', expanded);
}

function resourceDetailSection(title, content) {
    if (!content) return '';
    content = content.replace(/(?:<br\s*\/?>\s*){2,}/gi, '<br>')
        .replace(/(?:<br\s*\/?>(?:\s|&emsp;)*)+$/gi, '');
    return '<section class="resource-detail-section">'
        + '<h4 class="resource-detail-title" langtag="' + escapeHtml(title) + '"></h4>'
        + '<div class="resource-detail-body' + (title === 'section-commands' ? ' resource-detail-commands' : '') + '">' + content + '</div></section>';
}
