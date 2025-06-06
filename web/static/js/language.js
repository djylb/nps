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

    function setCookie(c_name, value, expiredays) {
        var exdate = new Date();
        exdate.setDate(exdate.getDate() + expiredays);
        document.cookie = c_name + '=' + escape(value) + ((expiredays == null) ? '' : ';expires=' + exdate.toGMTString()) + '; path=' + window.nps.web_base_url + '/;';
    }

    function getCookie(c_name) {
        if (document.cookie.length > 0) {
            c_start = document.cookie.indexOf(c_name + '=');
            if (c_start != -1) {
                c_start = c_start + c_name.length + 1;
                c_end = document.cookie.indexOf(';', c_start);
                if (c_end == -1) c_end = document.cookie.length;
                return unescape(document.cookie.substring(c_start, c_end));
            }
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
                // languages['navigator'] = (getCookie('lang') || navigator.language || navigator.browserLanguage);
                var navLang = (getCookie('lang') || navigator.language || navigator.browserLanguage || '');
                languages['navigator'] = navLang.startsWith('zh') ? 'zh-CN' : navLang;
                for (var key in languages['menu']) {
                    $('#languagemenu').next().append('<li lang="' + key + '"><a><img src="' + window.nps.web_base_url + '/static/img/flag/' + key + '.png"> ' + languages['menu'][key] + '</a></li>');
                    if (key == languages['navigator']) languages['current'] = key;
                }
                $('#languagemenu').attr('lang', (languages['current'] || languages['default']));
                $('body').setLang('');

                if ($.fn.selectpicker != null) {
                    $('.selectpicker').selectpicker('refresh');
                }
            }
        });
    };

    $.fn.setLang = function (dom) {
        languages['current'] = $('#languagemenu').attr('lang');
        if (dom == '') {
            $('#languagemenu span').text(' ' + languages['menu'][languages['current']]);
            if (languages['current'] != getCookie('lang')) setCookie('lang', languages['current']);
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

$(document).ready(function () {
    const savedTheme = localStorage.getItem('nps-theme');
    const html = document.documentElement;
    const icon = document.querySelector('#theme-toggle i');

    if (savedTheme === 'dark') {
        html.setAttribute('theme', 'dark-mode');
        icon.classList.remove('fa-moon');
        icon.classList.add('fa-sun');
    } else {
        html.removeAttribute('theme');
        icon.classList.remove('fa-sun');
        icon.classList.add('fa-moon');
    }
    $('body').cloudLang();
    $('body').on('click', 'li[lang]', function () {
        $('#languagemenu').attr('lang', $(this).attr('lang'));
        $('body').setLang('');
    });
});

var languages = {};
var charts = {};
var chartdatas = {};
var postsubmit;

function langreply(langstr) {
    var langobj = languages['content']['reply'][langstr.replace(/[\s,\.\?]*/g, "").toLowerCase()];
    if ($.type(langobj) == 'undefined') return langstr
    langobj = (langobj[languages['current']] || langobj[languages['default']] || langstr);
    return langobj
}

function goback() {
    history.back();
}

function submitform(action, url, postdata) {
    postsubmit = false;
    $.each(postdata, function (i, v) {
        if (v['value']) {
            v['value'] = v['value'].trim();
        }
    });
    switch (action) {
        case 'turn':
        case 'clear':
        case 'delete':
            var langobj = languages['content']['confirm'][action];
            action = (langobj[languages['current']] || langobj[languages['default']] || 'Are you sure you want to ' + action + ' it?');
            if (!confirm(action)) return;
        case 'start':
        case 'stop':
            postsubmit = true;
        case 'add':
        case 'edit':
            $.ajax({
                type: "POST",
                url: url,
                data: postdata,
                success: function (res) {
                    if (res.status) {
                        showMsg(langreply(res.msg), 'success', 1000, function() {
                          if (postsubmit) {
                              document.location.reload();
                          } else {
                              window.location.href = document.referrer;
                          }
                        });
                    } else {
                        showMsg(langreply(res.msg), 'error', 5000);
                    }
                }
            });
            return;
        case 'global':
            $.ajax({
                type: "POST",
                url: url,
                data: postdata,
                success: function (res) {
                    if (res.status) {
                        showMsg(langreply(res.msg), 'success', 1000, function() {
                            document.location.reload();
                        });
                    } else {
                        showMsg(langreply(res.msg), 'error', 5000);
                    }
                }
            });
    }
}

function changeunit(limit) {
    var size = "";
    if (limit < 0.1 * 1024) {
        size = limit.toFixed(2) + "B";
    } else if (limit < 0.1 * 1024 * 1024) {
        size = (limit / 1024).toFixed(2) + "KB";
    } else if (limit < 0.1 * 1024 * 1024 * 1024) {
        size = (limit / (1024 * 1024)).toFixed(2) + "MB";
    } else {
        size = (limit / (1024 * 1024 * 1024)).toFixed(2) + "GB";
    }

    var sizeStr = size + "";
    var index = sizeStr.indexOf(".");
    var dou = sizeStr.substr(index + 1, 2);
    if (dou == "00") {
        return sizeStr.substring(0, index) + sizeStr.substr(index + 3, 2);
    }
    return size;
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
    var svg = type === 'error'
      ? '<svg style="width:3rem;height:3rem;padding:1rem;box-sizing:content-box;" viewBox="0 0 1024 1024" xmlns="http://www.w3.org/2000/svg"><path d="M696.832 326.656c-12.8-12.8-33.28-12.8-46.08 0L512 465.92 373.248 327.168c-12.8-12.8-33.28-12.8-46.08 0s-12.8 33.28 0 46.08L466.432 512l-139.264 139.264c-12.8 12.8-12.8 33.28 0 46.08s33.28 12.8 46.08 0L512 558.08l138.752 139.264c12.288 12.8 32.768 12.8 45.568 0.512l0.512-0.512c12.8-12.8 12.8-33.28 0-45.568L557.568 512l139.264-139.264c12.8-12.8 12.8-33.28 0-46.08 0 0.512 0 0 0 0zM512 51.2c-254.464 0-460.8 206.336-460.8 460.8s206.336 460.8 460.8 460.8 460.8-206.336 460.8-460.8-206.336-460.8-460.8-460.8z m280.064 740.864c-74.24 74.24-175.104 116.224-280.064 115.712-104.96 0-205.824-41.472-280.064-115.712S115.712 616.96 115.712 512s41.472-205.824 116.224-280.064C306.176 157.696 407.04 115.712 512 116.224c104.96 0 205.824 41.472 280.064 116.224 74.24 74.24 116.224 175.104 115.712 280.064 0.512 104.448-41.472 205.312-115.712 279.552z" fill="#ffffff"></path></svg>'
      : '<svg style="width:3rem;height:3rem;padding:1rem;box-sizing:content-box;" viewBox="0 0 1024 1024"><path d="M384 887.456L25.6 529.056 145.056 409.6 384 648.544 878.944 153.6 998.4 273.056z" fill="#ffffff"/></svg>';
    var w = document.createElement('div');
    w.id = 'wangmarket_loading';
    w.style = 'position:fixed;top:0;z-index: 2147483647;width:100%;height:100%;display:flex;flex-direction:column;justify-content:center;align-items:center';
    w.innerHTML =
      '<div id="loading">'
        +'<div style="background-color:#2e2d3c;border-radius:0.3rem;opacity:0.8;min-width:6rem;min-height:4.8rem;max-width:20rem;display:flex;flex-wrap:wrap;align-items:center;'+(!isLong? 'flex-direction:column;':'')+'">'
          +'<div style="display:flex;">'+svg+'</div>'
          +'<div style="font-size:1rem;box-sizing:border-box;color:white;flex:1;padding:1rem;'+(!isLong? 'padding-top:0;':'')+'">'
            + text +
          '</div>'
        +'</div>'
      +'</div>';
    document.body.appendChild(w);
    setTimeout(function(){
        w.parentNode.removeChild(w);
        if (typeof cb === 'function') cb();
    }, dur);
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
