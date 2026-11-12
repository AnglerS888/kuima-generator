/* ============================================================
   轻量 layer：msg / alert / confirm / prompt / view / image
   用来替代 alert() / confirm() / prompt() —— 原生弹窗样式不受控、
   在移动端会阻塞渲染，而且 prompt 在 iOS 上还会被系统接管。

   为什么不用官方 layui：
   本项目刻意不引 CDN（内网/断网部署是常态），样式也要跟站点同一套
   蓝主色 + 16px 圆角 + 深浅色主题，所以自己实现一份，接口保持
   layer.msg / layer.alert / layer.confirm / layer.prompt 的习惯。
   ============================================================ */
(function () {
  if (window.layer && window.layer.__wx) return;

  var CSS = [
    '.wx-layer-mask{position:fixed;inset:0;z-index:9998;display:flex;align-items:center;',
    'justify-content:center;padding:1.2rem;background:rgba(15,23,42,.42);',
    '-webkit-backdrop-filter:blur(2px);backdrop-filter:blur(2px);',
    'animation:wxLayerIn .16s ease both}',
    '.wx-layer{--l-bg:#fff;--l-fg:#111827;--l-dim:#68707f;--l-line:#e5e9f0;--l-soft:#f6f8fc;',
    '--l-brand:#2f6bff;--l-brand2:#1e4fd8;--l-danger:#d93025;',
    'position:relative;width:100%;max-width:400px;background:var(--l-bg);color:var(--l-fg);',
    'border-radius:16px;padding:1.15rem 1.2rem 1rem;box-sizing:border-box;',
    'box-shadow:0 12px 40px -12px rgba(16,24,40,.35),0 2px 8px rgba(16,24,40,.08);',
    'animation:wxLayerPop .18s cubic-bezier(.2,.9,.3,1.2) both;',
    'font:14px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif}',
    'html[data-theme="dark"] .wx-layer{--l-bg:#151b26;--l-fg:#e9eef8;--l-dim:#93a1b6;',
    '--l-line:#26303f;--l-soft:#1b2330;--l-brand:#6ea0ff;--l-brand2:#4b83ff;--l-danger:#ff8a8a}',
    '.wx-layer-title{font-size:.95rem;font-weight:700;margin:0 0 .55rem;display:flex;',
    'align-items:center;gap:.5rem}',
    '.wx-layer-body{color:var(--l-dim);word-break:break-word;white-space:pre-wrap}',
    '.wx-layer-body b{color:var(--l-fg)}',
    '.wx-layer-foot{display:flex;justify-content:flex-end;gap:.55rem;margin-top:1.1rem}',
    '.wx-layer-btn{appearance:none;border:1px solid var(--l-line);background:var(--l-bg);',
    'color:var(--l-fg);border-radius:.6rem;padding:.5rem 1rem;font-size:.85rem;cursor:pointer;',
    'transition:.15s ease;min-width:4.6rem;font-family:inherit}',
    '.wx-layer-btn:hover{border-color:var(--l-brand);color:var(--l-brand)}',
    '.wx-layer-btn.primary{background:linear-gradient(180deg,var(--l-brand),var(--l-brand2));',
    'border-color:transparent;color:#fff;font-weight:600}',
    '.wx-layer-btn.primary:hover{color:#fff;filter:brightness(1.06)}',
    '.wx-layer-btn.danger{background:linear-gradient(180deg,#e5544b,#c8322a);border-color:transparent;',
    'color:#fff;font-weight:600}',
    '.wx-layer-btn.danger:hover{color:#fff;filter:brightness(1.06)}',
    '.wx-layer-input{width:100%;box-sizing:border-box;margin-top:.7rem;padding:.55rem .7rem;',
    'border:1px solid var(--l-line);border-radius:.6rem;background:var(--l-soft);color:var(--l-fg);',
    'font-size:.9rem;font-family:inherit;outline:none;transition:.15s ease}',
    '.wx-layer-input:focus{border-color:var(--l-brand);background:var(--l-bg);',
    'box-shadow:0 0 0 3px rgba(47,107,255,.12)}',
    '.wx-layer-hint{color:var(--l-dim);font-size:.76rem;margin-top:.4rem}',
    /* 长内容查看：加宽 + 可滚动 + 等宽字体，内容可选中复制 */
    '.wx-layer.wide{max-width:560px}',
    '.wx-layer.image{max-width:860px;max-height:calc(100vh - 2.4rem);overflow:auto}',
    '.wx-layer-image{display:block;width:100%;height:auto;max-height:calc(100vh - 12rem);',
    'object-fit:contain;background:var(--l-bg);border-radius:.6rem}',
    '.wx-layer-meta{color:var(--l-dim);font-size:.75rem;margin:0 0 .55rem;display:flex;',
    'flex-wrap:wrap;gap:.2rem .8rem}',
    '.wx-layer-view{max-height:min(58vh,26rem);overflow:auto;border:1px solid var(--l-line);',
    'border-radius:.65rem;background:var(--l-soft);padding:.7rem .85rem;color:var(--l-fg);',
    'font:12.5px/1.7 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;',
    'white-space:pre-wrap;word-break:break-all;user-select:text;-webkit-user-select:text}',
    '.wx-layer-view::-webkit-scrollbar{height:8px;width:8px}',
    '.wx-layer-view::-webkit-scrollbar-thumb{background:var(--l-line);border-radius:4px}',
    /* msg：顶部轻提示 */
    '.wx-layer-msg{position:fixed;left:50%;top:9%;transform:translateX(-50%);z-index:9999;',
    'display:flex;align-items:center;gap:.5rem;max-width:min(84vw,26rem);',
    'padding:.6rem .95rem;border-radius:.75rem;background:rgba(17,24,39,.92);color:#fff;',
    'font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;',
    'box-shadow:0 10px 30px -12px rgba(0,0,0,.5);animation:wxLayerMsg .2s ease both}',
    '.wx-layer-msg.bad{background:rgba(190,42,34,.95)}',
    '.wx-layer-msg .ic{width:1.05rem;height:1.05rem;flex:0 0 auto}',
    '@keyframes wxLayerIn{from{opacity:0}to{opacity:1}}',
    '@keyframes wxLayerPop{from{opacity:0;transform:translateY(6px) scale(.98)}',
    'to{opacity:1;transform:none}}',
    '@keyframes wxLayerMsg{from{opacity:0;transform:translate(-50%,-6px)}',
    'to{opacity:1;transform:translate(-50%,0)}}'
  ].join('');

  function ensureStyle() {
    if (document.getElementById('wx-layer-style')) return;
    var st = document.createElement('style');
    st.id = 'wx-layer-style';
    st.textContent = CSS;
    document.head.appendChild(st);
  }

  // icon: 0 无 / 1 成功 / 2 失败 / 3 问号 / 4 警告
  var ICONS = {
    1: '<svg viewBox="0 0 24 24" fill="none" stroke="#22c55e" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>',
    2: '<svg viewBox="0 0 24 24" fill="none" stroke="#ef4444" stroke-width="2.4" stroke-linecap="round"><path d="M18 6 6 18M6 6l12 12"/></svg>',
    3: '<svg viewBox="0 0 24 24" fill="none" stroke="#f59e0b" stroke-width="2.2" stroke-linecap="round"><circle cx="12" cy="12" r="9"/><path d="M12 8v.01M12 11v5"/></svg>',
    4: '<svg viewBox="0 0 24 24" fill="none" stroke="#f59e0b" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M10.3 3.9 2.4 18a2 2 0 0 0 1.7 3h15.8a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z"/><path d="M12 9v4M12 17h.01"/></svg>'
  };

  var open = null;   // 当前打开的对话框
  var msgEl = null, msgTimer = 0;
  var layerSeq = 0;

  // 复制到剪贴板：优先 async clipboard，老浏览器退回 execCommand
  function copyText(text, btn) {
    function done(ok) {
      if (!btn) return;
      var label = btn.textContent;
      btn.textContent = ok ? '已复制' : '复制失败';
      setTimeout(function () { btn.textContent = label; }, 1200);
    }
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(function () { done(true); },
        function () { done(false); });
      return;
    }
    var ta = document.createElement('textarea');
    ta.value = text;
    ta.style.cssText = 'position:fixed;left:-9999px;top:0;opacity:0';
    document.body.appendChild(ta);
    ta.select();
    var ok = false;
    try { ok = document.execCommand('copy'); } catch (e) {}
    document.body.removeChild(ta);
    done(ok);
  }

  function close(result) {
    if (!open) return;
    var cur = open;
    open = null;
    cur.mask.style.animation = 'wxLayerIn .12s ease reverse both';
    setTimeout(function () {
      if (cur.mask.parentNode) cur.mask.parentNode.removeChild(cur.mask);
      if (cur.prevFocus && cur.prevFocus.focus) {
        try { cur.prevFocus.focus(); } catch (e) {}
      }
    }, 110);
    if (cur.resolve) cur.resolve(result);
  }

  function build(opts) {
    var mask = document.createElement('div');
    mask.className = 'wx-layer-mask';

    var box = document.createElement('div');
    box.className = 'wx-layer' + (opts.wide ? ' wide' : '');
    box.setAttribute('role', 'dialog');
    box.setAttribute('aria-modal', 'true');
    box.setAttribute('aria-label', opts.title || '提示');

    if (opts.title) {
      var h = document.createElement('div');
      h.className = 'wx-layer-title';
      h.id = 'wx-layer-title-' + (++layerSeq);
      box.setAttribute('aria-labelledby', h.id);
      if (opts.icon && ICONS[opts.icon]) {
        var ic = document.createElement('span');
        ic.style.cssText = 'width:1.15rem;height:1.15rem;display:inline-flex;flex:0 0 auto';
        ic.innerHTML = ICONS[opts.icon];
        h.appendChild(ic);
      }
      h.appendChild(document.createTextNode(opts.title));
      box.appendChild(h);
    }

    var body = document.createElement('div');
    body.className = 'wx-layer-body';
    // 只做纯文本 + 换行，不解析 HTML：文案里可能带用户输入的内容
    body.textContent = opts.text || '';
    if (opts.text) box.appendChild(body);

    // meta：一行灰色补充信息（时间 / 类型 / 规格…）
    if (opts.meta) {
      var meta = document.createElement('div');
      meta.className = 'wx-layer-meta';
      meta.textContent = opts.meta;
      box.appendChild(meta);
    }

    // view：完整内容，固定高度滚动，可选中
    if (opts.view != null) {
      var view = document.createElement('div');
      view.className = 'wx-layer-view';
      view.textContent = opts.view;
      box.appendChild(view);
    }

    if (opts.image) {
      box.classList.add('image');
      var img = document.createElement('img');
      img.className = 'wx-layer-image';
      img.alt = opts.title || '图片预览';
      img.onerror = function () {
        if (opts.fallback && img.src !== opts.fallback) {
          img.src = opts.fallback;
          return;
        }
        img.onerror = null;
        img.hidden = true;
        var error = document.createElement('div');
        error.className = 'wx-layer-body';
        error.textContent = '图片加载失败，请关闭后重新生成';
        img.parentNode.replaceChild(error, img);
      };
      img.src = opts.image;
      box.appendChild(img);
    }

    var input = null;
    if (opts.prompt) {
      input = document.createElement('input');
      input.className = 'wx-layer-input';
      input.type = opts.inputType || 'text';
      input.value = opts.value == null ? '' : String(opts.value);
      if (opts.placeholder) input.placeholder = opts.placeholder;
      box.appendChild(input);
      if (opts.hint) {
        var hint = document.createElement('div');
        hint.className = 'wx-layer-hint';
        hint.textContent = opts.hint;
        box.appendChild(hint);
      }
    }

    var foot = document.createElement('div');
    foot.className = 'wx-layer-foot';

    var btns = [];
    if (opts.copyText) {
      var cp = document.createElement('button');
      cp.className = 'wx-layer-btn';
      cp.type = 'button';
      cp.textContent = opts.copyText;
      cp.style.marginRight = 'auto';
      cp.onclick = function () { copyText(opts.view == null ? (opts.text || '') : opts.view, cp); };
      foot.appendChild(cp);
    }
    if (opts.cancelText !== null && opts.cancelText !== undefined) {
      var cancel = document.createElement('button');
      cancel.className = 'wx-layer-btn';
      cancel.type = 'button';
      cancel.textContent = opts.cancelText;
      cancel.onclick = function () { close(opts.prompt ? null : false); };
      foot.appendChild(cancel);
      btns.push(cancel);
    }
    var ok = document.createElement('button');
    ok.className = 'wx-layer-btn ' + (opts.danger ? 'danger' : 'primary');
    ok.type = 'button';
    ok.textContent = opts.okText || '确定';
    ok.onclick = function () {
      if (opts.prompt) close(input.value);
      else close(true);
    };
    foot.appendChild(ok);
    btns.push(ok);
    box.appendChild(foot);
    mask.appendChild(box);

    // 点遮罩：confirm/prompt 视为取消，alert/msg 视为确认关闭
    mask.onclick = function (e) {
      if (e.target !== mask) return;
      close(opts.prompt ? null : (opts.cancelText != null ? false : true));
    };

    return { mask: mask, box: box, input: input, ok: ok, btns: btns };
  }

  function show(opts) {
    ensureStyle();
    if (open) close(opts.prompt ? null : false); // 同时只留一个，避免叠层
    var ui = build(opts);
    ui.prevFocus = document.activeElement;
    document.body.appendChild(ui.mask);
    open = ui;

    var p = new Promise(function (res) { ui.resolve = res; });

    // 键盘：Enter 确认、Esc 取消
    function onKey(e) {
      if (!open || open !== ui) { document.removeEventListener('keydown', onKey, true); return; }
      if (e.key === 'Escape') { e.preventDefault(); close(opts.prompt ? null : (opts.cancelText != null ? false : true)); return; }
      if (e.key === 'Enter' && !e.isComposing) { e.preventDefault(); ui.ok.click(); return; }
      if (e.key === 'Tab') {
        var focusable = Array.prototype.filter.call(
          ui.box.querySelectorAll('button,input,textarea,select,[tabindex]:not([tabindex="-1"])'),
          function (el) { return !el.disabled && el.offsetParent !== null; }
        );
        if (!focusable.length) return;
        var first = focusable[0], last = focusable[focusable.length - 1];
        if (e.shiftKey && (document.activeElement === first || !ui.box.contains(document.activeElement))) {
          e.preventDefault(); last.focus();
        } else if (!e.shiftKey && (document.activeElement === last || !ui.box.contains(document.activeElement))) {
          e.preventDefault(); first.focus();
        }
      }
    }
    document.addEventListener('keydown', onKey, true);

    setTimeout(function () {
      if (ui.input) { ui.input.focus(); ui.input.select(); }
      else ui.ok.focus();
    }, 30);
    return p;
  }

  var layer = {
    __wx: true,

    // 顶部轻提示，2.2 秒自动消失
    msg: function (text, opts) {
      ensureStyle();
      opts = opts || {};
      if (msgEl && msgEl.parentNode) msgEl.parentNode.removeChild(msgEl);
      clearTimeout(msgTimer);
      var el = document.createElement('div');
      el.className = 'wx-layer-msg' + (opts.error ? ' bad' : '');
      el.setAttribute('role', opts.error ? 'alert' : 'status');
      el.setAttribute('aria-live', opts.error ? 'assertive' : 'polite');
      el.setAttribute('aria-atomic', 'true');
      var iconId = opts.error ? 2 : (opts.icon || 1);
      if (ICONS[iconId]) {
        var s = document.createElement('span');
        s.className = 'ic';
        s.innerHTML = ICONS[iconId];
        el.appendChild(s);
      }
      el.appendChild(document.createTextNode(text == null ? '' : String(text)));
      document.body.appendChild(el);
      msgEl = el;
      msgTimer = setTimeout(function () {
        el.style.transition = 'opacity .18s ease';
        el.style.opacity = '0';
        setTimeout(function () { if (el.parentNode) el.parentNode.removeChild(el); if (msgEl === el) msgEl = null; }, 200);
      }, opts.time || 2200);
      return el;
    },

    // 只有一个「确定」，返回 Promise
    alert: function (text, opts) {
      opts = opts || {};
      return show({
        title: opts.title || (opts.icon === 2 ? '出错了' : '提示'),
        text: text, icon: opts.icon, danger: false,
        okText: opts.okText || '知道了', cancelText: null
      });
    },

    // 返回 Promise<boolean>
    confirm: function (text, opts) {
      opts = opts || {};
      return show({
        title: opts.title || '确认操作',
        text: text, icon: opts.icon || 3, danger: !!opts.danger,
        okText: opts.okText || '确定', cancelText: opts.cancelText || '取消'
      });
    },

    // 返回 Promise<string|null>，取消为 null
    prompt: function (text, value, opts) {
      if (value && typeof value === 'object') { opts = value; value = ''; }
      opts = opts || {};
      return show({
        title: opts.title || '请输入',
        text: text, icon: opts.icon, value: value,
        placeholder: opts.placeholder, hint: opts.hint,
        inputType: opts.inputType, danger: false,
        okText: opts.okText || '确定', cancelText: opts.cancelText || '取消',
        prompt: true
      });
    },

    // 放大图片，保留原始格式，支持临时地址失效后回退本地预览
    image: function (src, opts) {
      opts = opts || {};
      return show({
        title: opts.title || '图片预览',
        image: src, fallback: opts.fallback || '',
        okText: '关闭', cancelText: null
      });
    },

    // 查看长内容：等宽字体、可滚动、可一键复制
    //   layer.view('标题', 完整文本, { meta: '时间 · 类型', copy: true })
    view: function (title, text, opts) {
      opts = opts || {};
      return show({
        title: title || '查看内容',
        meta: opts.meta || '',
        view: text == null ? '' : String(text),
        wide: true,
        copyText: opts.copy === false ? '' : '复制',
        okText: opts.okText || '关闭',
        cancelText: null
      });
    }
  };

  window.layer = layer;
})();
