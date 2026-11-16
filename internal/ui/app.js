/* Greenroom portal. Plain JS, hash routing, no build step.
   Visual system designed in Magic Patterns and ported to vanilla CSS. */
(function () {
  'use strict';

  var app = document.getElementById('app');
  var crumbs = document.getElementById('crumbs');
  var me = { actor: '', auth: false, version: '' };
  var timer = null;
  var source = null;
  var envView = null;
  var reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  // ---------- theme ----------
  var theme = null;
  try { theme = localStorage.getItem('gr-theme'); } catch (e) { /* storage unavailable */ }
  if (theme !== 'light' && theme !== 'dark') theme = window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
  function applyTheme() {
    document.documentElement.setAttribute('data-theme', theme);
    var b = document.getElementById('btn-theme');
    if (b) b.innerHTML = theme === 'dark' ? I.sun : I.moon;
  }

  // ---------- icons ----------
  function svg(inner, size) {
    return '<svg width="' + (size || 15) + '" height="' + (size || 15) + '" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' + inner + '</svg>';
  }
  var I = {
    chevron: svg('<path d="m9 18 6-6-6-6"/>', 13),
    refresh: svg('<path d="M21 12a9 9 0 0 0-9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/><path d="M3 12a9 9 0 0 0 9 9 9.75 9.75 0 0 0 6.74-2.74L21 16"/><path d="M16 16h5v5"/>', 13),
    logout: svg('<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><polyline points="16 17 21 12 16 7"/><line x1="21" x2="9" y1="12" y2="12"/>', 13),
    sun: svg('<circle cx="12" cy="12" r="4"/><path d="M12 2v2"/><path d="M12 20v2"/><path d="m4.93 4.93 1.41 1.41"/><path d="m17.66 17.66 1.41 1.41"/><path d="M2 12h2"/><path d="M20 12h2"/><path d="m6.34 17.66-1.41 1.41"/><path d="m19.07 4.93-1.41 1.41"/>'),
    moon: svg('<path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z"/>'),
    x: svg('<path d="M18 6 6 18"/><path d="m6 6 12 12"/>'),
    alert: svg('<path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3Z"/><path d="M12 9v4"/><path d="M12 17h.01"/>', 16),
    folder: svg('<path d="M12 10v6"/><path d="M9 13h6"/><path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/>', 19),
    check: svg('<circle cx="12" cy="12" r="10"/><path d="m9 12 2 2 4-4"/>'),
    info: svg('<circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/>'),
    xcircle: svg('<circle cx="12" cy="12" r="10"/><path d="m15 9-6 6"/><path d="m9 9 6 6"/>'),
    ring: '<svg width="13" height="13" viewBox="0 0 16 16" class="ring" aria-hidden="true"><circle cx="8" cy="8" r="6.4" stroke="currentColor" stroke-width="2" fill="none" opacity="0.25"/><path d="M8 1.6a6.4 6.4 0 0 1 6.4 6.4" stroke="currentColor" stroke-width="2" stroke-linecap="round" fill="none"/></svg>'
  };
  document.getElementById('btn-reload').innerHTML = I.refresh + '<span>Reload config</span>';
  document.getElementById('btn-logout').innerHTML = I.logout + '<span>Sign out</span>';
  applyTheme();

  // ---------- helpers ----------
  var esc = function (s) { return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) { return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]; }); };

  function api(path, opts) {
    opts = opts || {};
    var headers = Object.assign({ 'X-Requested-With': 'greenroom' }, opts.headers || {});
    if (opts.body && typeof opts.body !== 'string') { opts.body = JSON.stringify(opts.body); headers['Content-Type'] = 'application/json'; }
    return fetch(path, Object.assign({}, opts, { headers: headers })).then(function (res) {
      if (res.status === 401) { location.href = '/login'; throw new Error('signed out'); }
      return res.text().then(function (text) {
        var data = null;
        try { data = text ? JSON.parse(text) : null; } catch (e) { data = { error: text }; }
        if (!res.ok) throw new Error((data && data.error) || res.statusText);
        return data;
      });
    });
  }

  function toast(title, kind, body) {
    var el = document.createElement('div');
    el.className = 'toast card ' + (kind || 'info');
    el.setAttribute('role', 'status');
    el.innerHTML = (kind === 'ok' ? I.check : kind === 'error' ? I.xcircle : I.info) +
      '<div style="min-width:0;flex:1"><div class="tt">' + esc(title) + '</div>' + (body ? '<div class="tb">' + esc(body) + '</div>' : '') + '</div>' +
      '<button class="btn icon ghost x" aria-label="Dismiss">' + I.x + '</button>';
    var remove = function () { if (el.classList.contains('leaving')) return; el.classList.add('leaving'); setTimeout(function () { el.remove(); }, 180); };
    el.querySelector('.x').addEventListener('click', remove);
    document.getElementById('toasts').appendChild(el);
    setTimeout(remove, 5200);
  }

  function ago(iso) {
    if (!iso) return 'never';
    var t = new Date(iso).getTime();
    if (isNaN(t) || t <= 0 || new Date(iso).getFullYear() < 2000) return 'never';
    var s = Math.max(0, (Date.now() - t) / 1000);
    if (s < 60) return Math.floor(s) + 's ago';
    if (s < 3600) return Math.floor(s / 60) + 'm ago';
    if (s < 86400 * 2) return Math.floor(s / 3600) + 'h ' + Math.floor((s % 3600) / 60) + 'm ago';
    return Math.floor(s / 86400) + 'd ago';
  }
  function when(iso) {
    if (!iso) return '';
    var d = new Date(iso);
    if (isNaN(d.getTime()) || d.getFullYear() < 2000) return '';
    return d.toLocaleString();
  }
  function dur(ns) {
    if (ns == null) return '';
    var ms = ns / 1e6;
    if (ms < 1000) return Math.round(ms) + 'ms';
    if (ms < 60000) return (ms / 1000).toFixed(1) + 's';
    var m = Math.floor(ms / 60000), s = Math.round((ms % 60000) / 1000);
    return m + 'm' + (s ? ' ' + s + 's' : '');
  }
  function pct(v) { return v == null ? '' : (Math.round(v * 100) / 100).toFixed(2) + '%'; }
  function badge(kind, label, o) {
    o = o || {};
    return '<span class="badge ' + esc(kind) + (o.size ? ' ' + o.size : '') + (o.upper ? ' upper' : '') + '">' +
      (o.dot ? '<span class="dot' + (o.pulse === false ? '' : ' pulse') + '"></span>' : '') + esc(label) + '</span>';
  }
  function chip(kind, label) { return '<span class="chip ' + esc(kind) + '">' + esc(label) + '</span>'; }
  function statusBadge(st) {
    var map = { ok: 'green', failed: 'red', running: 'blue', cancelled: 'grey', skipped: 'grey' };
    return badge(map[st] || 'grey', st, { dot: st === 'running' });
  }
  function readinessBadge(r, size) {
    var label = { green: 'GREEN', amber: 'AMBER', red: 'RED' }[r] || String(r || '').toUpperCase();
    return badge(r || 'grey', label, { dot: true, size: size, upper: true });
  }
  function ring(r) {
    var size = 96, rr = size / 2 - 4, c = 2 * Math.PI * rr;
    var frac = { green: 1, amber: 0.62, red: 0.28 }[r] || 0.28;
    var dash = (c * frac).toFixed(2);
    return '<div class="ringwrap"><svg width="' + size + '" height="' + size + '" aria-hidden="true">' +
      '<circle class="track" cx="48" cy="48" r="' + rr + '" fill="none" stroke-width="2"/>' +
      '<circle class="arc ' + esc(r) + '" cx="48" cy="48" r="' + rr + '" fill="none" stroke-width="2.5" style="--gr-dash:' + dash + ';stroke-dasharray:' + dash + ' ' + c.toFixed(2) + '"/>' +
      '</svg>' + readinessBadge(r, 'lg') + '</div>';
  }
  function bucketLabel(i, n, hours) {
    var minutesAgo = Math.round((n - 1 - i) * (hours * 60 / n));
    if (minutesAgo === 0) return 'now';
    var h = Math.floor(minutesAgo / 60), m = minutesAgo % 60;
    if (h === 0) return m + 'm ago';
    return m === 0 ? h + 'h ago' : h + 'h ' + m + 'm ago';
  }
  function spark(points, hours) {
    if (!points || !points.length) return '<span class="muted small">no data</span>';
    var n = points.length;
    return '<span class="spark" role="img" aria-label="uptime sparkline">' + points.map(function (p, i) {
      var cls = p === 1 ? 'ok' : p === 0 ? 'bad' : '';
      var h = p === 1 ? (58 + ((i * 13) % 42)) + '%' : '';
      var word = p === 1 ? 'up' : p === 0 ? 'down' : 'no sample';
      return '<i class="' + cls + '" style="--i:' + i + (h ? ';height:' + h : '') + '" title="' + esc(bucketLabel(i, n, hours || 24) + ': ' + word) + '"></i>';
    }).join('') + '</span>';
  }
  function countUp(container) {
    var els = container.querySelectorAll('[data-count]');
    Array.prototype.forEach.call(els, function (el) {
      var target = parseFloat(el.getAttribute('data-count'));
      var dec = parseInt(el.getAttribute('data-decimals') || '0', 10);
      var suffix = el.getAttribute('data-suffix') || '';
      if (reduceMotion || !isFinite(target)) { el.textContent = target.toFixed(dec) + suffix; return; }
      var start = performance.now(), d = 900;
      var tick = function (now) {
        var t = Math.min(1, (now - start) / d), e = 1 - Math.pow(1 - t, 3);
        el.textContent = (target * e).toFixed(dec) + suffix;
        if (t < 1) requestAnimationFrame(tick);
      };
      requestAnimationFrame(tick);
    });
  }
  function setCrumbs(parts) {
    crumbs.innerHTML = parts.map(function (p) {
      return I.chevron + (p.href ? '<a href="' + esc(p.href) + '">' + esc(p.label) + '</a>' : '<span>' + esc(p.label) + '</span>');
    }).join('');
  }
  function stopLive() {
    if (timer) { clearInterval(timer); timer = null; }
    if (source) { source.close(); source = null; }
  }
  function busy(btn, label) {
    btn.disabled = true;
    btn.innerHTML = I.ring + '<span>' + esc(label) + '</span>';
  }

  // ---------- modal ----------
  function modal(o, onMount) {
    var root = document.getElementById('modal-root');
    root.innerHTML = '<div class="modal-layer"><div class="modal-back" data-close></div>' +
      '<div class="modal card" role="dialog" aria-modal="true" style="max-width:' + (o.width || 520) + 'px">' +
      '<div class="m-head"><div class="t">' + o.title + '</div><button class="btn icon ghost" data-close aria-label="Close dialog">' + I.x + '</button></div>' +
      '<div class="m-body">' + o.body + '</div>' + (o.footer ? '<div class="m-foot">' + o.footer + '</div>' : '') + '</div></div>';
    var onKey = function (e) { if (e.key === 'Escape') close(); };
    var close = function () { root.innerHTML = ''; document.removeEventListener('keydown', onKey); };
    document.addEventListener('keydown', onKey);
    Array.prototype.forEach.call(root.querySelectorAll('[data-close]'), function (b) { b.addEventListener('click', close); });
    if (onMount) onMount(root, close);
    return close;
  }

  // ---------- overview ----------
  function skeletonCard(i) {
    var rows = [68, 84, 56, 62, 74, 70].map(function (w, j) {
      return '<div class="row"><div class="skel" style="width:' + w + 'px;height:10px"></div><div class="skel" style="width:' + (90 - j * 4) + 'px;height:10px"></div></div>';
    }).join('');
    return '<div class="card pad skel-card" style="--i:' + i + '"><div style="display:flex;gap:8px;align-items:center"><div class="skel" style="width:72px;height:20px;border-radius:999px"></div><div class="skel" style="width:150px;height:16px"></div></div>' +
      '<div class="skel" style="width:72%;height:11px;margin-top:12px"></div><div style="margin-top:16px;padding-top:14px;border-top:1px solid var(--gr-border)">' + rows + '</div></div>';
  }
  function renderOverview() {
    stopLive();
    envView = null;
    setCrumbs([{ label: 'Environments' }]);
    app.innerHTML = '<div class="spotlight"></div><div class="page"><header class="rise"><h1>Demo environments</h1><div class="skel" style="width:340px;height:12px;margin-top:10px"></div></header>' +
      '<div class="grid stagger">' + [0, 1, 2].map(skeletonCard).join('') + '</div></div>';
    var first = true;
    var draw = function () {
      return api('/api/v1/overview').then(function (data) {
        var envs = data.environments || [];
        var html = '<div class="spotlight"></div><div class="page' + (first ? '' : ' static') + '">' +
          '<header class="page-head rise"><div><h1>Demo environments</h1><div class="sub">' + envs.length + ' environment' + (envs.length === 1 ? '' : 's') + ' in <span class="mono dim">' + esc(data.environmentsDir) + '</span>, probed every <span class="mono dim">' + esc(data.probeInterval) + '</span></div></div>' +
          '<div class="actions"><button class="btn primary" id="act-new"><span>New environment</span></button></div></header>';
        if (data.loadError) html += '<div class="alert rise">' + I.alert + '<div><b>Some environments failed to load</b><span class="small dim mono">' + esc(data.loadError) + '</span></div></div>';
        if (!envs.length) {
          html += '<div class="card empty rise" style="margin-top:24px"><span class="icon-tile">' + I.folder + '</span><h2>No environments yet</h2><p>Start from a template with the product modules you want to show, or run <span class="mono dim">greenroom init</span> for the bundled example.</p></div>';
        } else {
          html += '<div class="grid stagger">' + envs.map(envCard).join('') + '</div>';
        }
        app.innerHTML = html + '</div>';
        var nb = document.getElementById('act-new');
        if (nb) nb.addEventListener('click', newEnvDialog);
        first = false;
      });
    };
    draw().catch(function (e) { app.innerHTML = '<div class="alert">' + I.alert + '<div><b>Could not load</b>' + esc(e.message) + '</div></div>'; });
    timer = setInterval(function () { draw().catch(function () {}); }, 30000);
  }

  function envCard(e, i) {
    var applied = e.applied ? '<b class="mono">' + esc(e.applied.setId) + '</b> <span class="muted">· ' + ago(e.applied.at) + '</span>' : '<span class="muted">never reset</span>';
    var chips = (e.down ? ' ' + chip('red', e.down + ' down') : '') + (e.warn ? ' ' + chip('amber', e.warn + ' warn') : '');
    return '<a class="card link env-card pad" href="#/env/' + esc(e.name) + '" style="--i:' + i + '">' +
      '<h2>' + readinessBadge(e.readiness) + '<span class="title"><b>' + esc(e.title) + '</b><span>' + esc(e.summary) + '</span></span>' +
      (e.busy ? badge('blue', 'busy', { dot: true }) : '') + '</h2>' +
      '<dl>' +
      '<div><dt>Pinned</dt><dd><b class="mono">' + esc(e.pinned || 'none') + '</b></dd></div>' +
      '<div><dt>Applied</dt><dd>' + applied + '</dd></div>' +
      '<div><dt>Dependencies</dt><dd><b class="num">' + e.deps + '</b>' + chips + '</dd></div>' +
      '<div><dt>Stale</dt><dd style="color:' + (e.stale ? 'var(--gr-amber)' : 'var(--gr-green)') + '">' + (e.stale ? e.stale + ' item' + (e.stale === 1 ? '' : 's') : 'fresh') + '</dd></div>' +
      '<div><dt>Uptime 24h</dt><dd>' + (e.lastProbe ? '<b class="num">' + pct(e.uptime24h) + '</b>' : '<span class="muted">no samples</span>') + '</dd></div>' +
      '<div><dt>Owner</dt><dd>' + esc(e.owner || '') + '</dd></div>' +
      '</dl>' + templateLine(e.template) + '</a>';
  }

  function templateLine(t) {
    if (!t || !t.name) return '';
    return '<div class="tpl-line"><span class="muted">' + esc(t.productType || t.name) + '</span>' + (t.modules || []).map(function (m) { return chip('green', m); }).join('') + '</div>';
  }

  // ---------- new environment from a template ----------
  function slug(s) { return String(s || '').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 63); }
  function tplModule(t, name) { return t.modules.filter(function (m) { return m.name === name; })[0]; }
  function resolveModules(t, picked) {
    var want = {};
    t.modules.forEach(function (m) { if (m.required || picked[m.name]) want[m.name] = true; });
    for (var changed = true; changed;) {
      changed = false;
      Object.keys(want).forEach(function (n) {
        (tplModule(t, n).requires || []).forEach(function (r) { if (!want[r]) { want[r] = true; changed = true; } });
      });
    }
    return t.modules.filter(function (m) { return want[m.name]; }).map(function (m) { return m.name; });
  }
  function personasFor(t, mods) {
    return t.personas.filter(function (p) { return p.modules.indexOf('*') >= 0 || p.modules.some(function (m) { return mods.indexOf(m) >= 0; }); });
  }
  // dropModule removes a module and, transitively, every picked module that needs it.
  function dropModule(t, picked, name) {
    delete picked[name];
    Object.keys(picked).forEach(function (k) {
      if (picked[k] && (tplModule(t, k).requires || []).indexOf(name) >= 0) dropModule(t, picked, k);
    });
  }
  function newEnvDialog() {
    api('/api/v1/templates').then(function (data) {
      var list = (data.templates || []).filter(function (t) { return !t.problems.length; });
      if (!list.length) { toast('No usable templates', 'error', data.loadError || 'none found'); return; }
      var defaults = function (t) { var p = {}; t.modules.forEach(function (m) { if (m.default) p[m.name] = true; }); return p; };
      var st = { t: list[0], picked: defaults(list[0]), nameEdited: false };
      var cards = list.map(function (t, i) {
        return '<button type="button" class="tpl-card" role="radio" data-tpl="' + i + '"><span class="badge blue">' + esc(t.productType || 'template') + '</span><b>' + esc(t.title) + '</b><span class="small muted">' + esc(t.description || '') + '</span>' +
          '<span class="xs muted">' + t.modules.length + ' modules, ' + t.personas.length + ' personas' + (t.source === 'workspace' ? ', from the workspace' : '') + '</span></button>';
      }).join('');
      modal({
        width: 780,
        title: '<h2>New environment</h2>',
        body: '<p class="lead">Pick the product, then the modules this demo will show. Greenroom writes the environment file, one seed set per persona and whatever the template runs next to it.</p>' +
          '<div class="tpl-grid" role="radiogroup" aria-label="Template">' + cards + '</div>' +
          '<div id="ne-detail"></div><div id="ne-error"></div>',
        footer: '<span class="small muted foot-note" id="ne-summary"></span><button class="btn ghost" data-close><span>Cancel</span></button><button class="btn primary" id="ne-go"><span>Create environment</span></button>'
      }, function (root, close) {
        var detail = root.querySelector('#ne-detail');
        var drawModules = function () {
          var t = st.t, mods = resolveModules(t, st.picked);
          var pers = personasFor(t, mods);
          var deps = t.baseDependencies + mods.reduce(function (n, m) { return n + tplModule(t, m).dependencies.length; }, 0);
          root.querySelector('#ne-summary').textContent = deps + ' dependencies, ' + pers.length + ' seed set' + (pers.length === 1 ? '' : 's');
          root.querySelector('#ne-modules').innerHTML = t.modules.map(function (m) {
            var on = mods.indexOf(m.name) >= 0;
            var why = m.required ? 'always included' : m.requires.length ? 'needs ' + m.requires.join(', ') : '';
            return '<label class="check-row module-row"><input type="checkbox" data-mod="' + esc(m.name) + '"' + (on ? ' checked' : '') + (m.required ? ' disabled' : '') + '>' +
              '<span><b>' + esc(m.title) + '</b>' + (why ? ' <span class="xs muted">' + esc(why) + '</span>' : '') + '<span class="small muted block">' + esc(m.description || '') + '</span></span></label>';
          }).join('');
          var sel = root.querySelector('#ne-persona'), cur = sel.value;
          sel.innerHTML = pers.map(function (p) { return '<option value="' + esc(p.name) + '"' + (p.name === cur ? ' selected' : '') + '>' + esc(p.title || p.name) + '</option>'; }).join('');
          Array.prototype.forEach.call(root.querySelectorAll('[data-mod]'), function (cb) {
            cb.addEventListener('change', function () {
              var n = cb.getAttribute('data-mod');
              if (cb.checked) st.picked[n] = true; else dropModule(st.t, st.picked, n);
              drawModules();
            });
          });
        };
        var drawDetail = function () {
          var t = st.t;
          Array.prototype.forEach.call(root.querySelectorAll('.tpl-card'), function (c) {
            var on = list[+c.getAttribute('data-tpl')] === t;
            c.classList.toggle('selected', on);
            c.setAttribute('aria-checked', on ? 'true' : 'false');
          });
          var val = function (id) { var el = root.querySelector(id); return el ? el.value : ''; };
          var keep = { name: val('#ne-name'), title: val('#ne-title'), owner: val('#ne-owner') };
          detail.innerHTML = '<div class="fields" style="margin-top:6px"><div class="field"><label for="ne-title">Title</label><input type="text" id="ne-title" maxlength="80" placeholder="Prospect demo" value="' + esc(keep.title) + '"></div>' +
            '<div class="field"><label for="ne-name">Name</label><input type="text" id="ne-name" maxlength="63" class="mono" placeholder="prospect-demo" value="' + esc(keep.name) + '"></div></div>' +
            '<div class="fields"><div class="field"><label for="ne-persona">Pinned persona</label><select id="ne-persona"></select></div>' +
            '<div class="field"><label for="ne-owner">Owner</label><input type="text" id="ne-owner" maxlength="80" placeholder="sales-engineering" value="' + esc(keep.owner) + '"></div></div>' +
            '<h4 style="margin-top:18px">Modules</h4><div id="ne-modules" class="module-list"></div>' +
            (t.params.length ? '<details class="params"><summary>Template settings <span class="muted">(' + t.params.length + ')</span></summary><div class="fields">' + t.params.map(function (p) {
              return '<div class="field"><label for="ne-p-' + esc(p.name) + '">' + esc(p.title || p.name) + '</label><input type="text" class="mono" id="ne-p-' + esc(p.name) + '" data-param="' + esc(p.name) + '" value="' + esc(p.default || '') + '"></div>';
            }).join('') + '</div></details>' : '');
          var ti = root.querySelector('#ne-title'), ni = root.querySelector('#ne-name');
          ti.addEventListener('input', function () { if (!st.nameEdited) ni.value = slug(ti.value); });
          ni.addEventListener('input', function () { st.nameEdited = ni.value !== ''; });
          drawModules();
        };
        Array.prototype.forEach.call(root.querySelectorAll('.tpl-card'), function (c) {
          c.addEventListener('click', function () { st.t = list[+c.getAttribute('data-tpl')]; st.picked = defaults(st.t); drawDetail(); });
        });
        drawDetail();
        root.querySelector('#ne-go').addEventListener('click', function () {
          var go = root.querySelector('#ne-go'), errEl = root.querySelector('#ne-error');
          var params = {};
          Array.prototype.forEach.call(root.querySelectorAll('[data-param]'), function (i) { params[i.getAttribute('data-param')] = i.value.trim(); });
          var name = root.querySelector('#ne-name').value.trim() || slug(root.querySelector('#ne-title').value);
          var body = { template: st.t.name, name: name, title: root.querySelector('#ne-title').value.trim(), owner: root.querySelector('#ne-owner').value.trim(),
            modules: resolveModules(st.t, st.picked), persona: root.querySelector('#ne-persona').value, params: params };
          errEl.innerHTML = '';
          busy(go, 'Creating');
          api('/api/v1/environments', { method: 'POST', body: body }).then(function (r) {
            close();
            toast('Environment created', 'ok', r.name + ': ' + r.plan.personas.length + ' seed sets, ' + r.plan.dependencies + ' dependencies');
            location.hash = '#/env/' + r.name;
          }).catch(function (e) {
            errEl.innerHTML = '<div class="alert">' + I.alert + '<div><b>Not created</b>' + esc(e.message) + '</div></div>';
            go.disabled = false; go.innerHTML = '<span>Create environment</span>';
          });
        });
      });
    }).catch(function (e) { toast('Could not load templates', 'error', e.message); });
  }

  // ---------- environment ----------
  var TABS = [['deps', 'Dependencies'], ['seeds', 'Seed sets'], ['stale', 'Stale'], ['runs', 'Runs'], ['targets', 'Targets']];
  function tabCounts(d) {
    return { deps: d.deps, seeds: d.seeds.length, stale: d.stale ? d.stale.items.length : 0, runs: d.runs.length, targets: d.targets.length };
  }
  function stat(i, label, value, hint, cls, count) {
    var v = count ? '<div class="value ' + (cls || '') + '" data-count="' + count.v + '" data-decimals="' + (count.d || 0) + '" data-suffix="' + esc(count.s || '') + '">' + count.v.toFixed(count.d || 0) + esc(count.s || '') + '</div>'
      : '<div class="value ' + (cls || '') + '">' + value + '</div>';
    return '<div class="card stat" style="--i:' + i + '"><div class="label">' + esc(label) + '</div>' + v + (hint ? '<div class="hint">' + esc(hint) + '</div>' : '') + '</div>';
  }
  function statsHtml(d) {
    var stale = d.stale ? d.stale.stale : 0;
    return stat(0, 'Pinned seed set', '<span class="mono">' + esc(d.pinned || 'none') + '</span>', '', 'mono green') +
      stat(1, 'Applied', '<span class="mono">' + esc(d.applied ? d.applied.setId : 'none') + '</span>', d.applied ? ago(d.applied.at) : 'never reset', 'mono') +
      stat(2, 'Dependencies', '', d.down ? d.down + ' down' : d.warn ? d.warn + ' warn' : 'all up', d.down ? 'amber' : '', { v: d.deps }) +
      stat(3, 'Uptime 24h', '', d.lastProbe ? 'all dependencies' : 'no samples yet', '', { v: d.lastProbe ? d.uptime24h : 0, d: 2, s: '%' }) +
      stat(4, 'Stale items', '', 'limit ' + d.staleAfter, stale ? 'amber' : '', { v: stale });
  }
  function headHtml(d) {
    return ring(d.readiness) +
      '<div class="titles"><h1>' + esc(d.title) + '</h1><p>' + esc(d.summary) + (d.lastProbe ? ' <span class="muted">(probed ' + ago(d.lastProbe) + ')</span>' : '') + '</p>' +
      (d.description ? '<p class="desc">' + esc(d.description) + '</p>' : '') + templateLine(d.template) + '</div>' +
      '<div class="actions"><button class="btn" id="act-check"><span>Pre-call check</span></button>' +
      '<button class="btn primary shimmer" id="act-reset"' + (d.busy ? ' disabled' : '') + '><span>Reset to known good</span></button></div>';
  }
  function noticesHtml(d, name) {
    var html = '';
    if (d.busy) html += '<div class="alert info rise">' + I.info + '<div><b>A run is in progress</b><a href="#/env/' + esc(name) + '/run/' + esc(d.busy) + '" class="mono small">' + esc(d.busy) + '</a></div></div>';
    if (d.problems && d.problems.length) html += '<div class="alert rise">' + I.alert + '<div><b>Configuration problems</b>' + d.problems.map(esc).join('<br>') + '</div></div>';
    return html;
  }
  function tabsHtml(name, d, tab) {
    var counts = tabCounts(d);
    return '<div class="tabs"><div class="list">' + TABS.map(function (t) {
      return '<a href="#/env/' + esc(name) + '/' + t[0] + '" data-tab="' + t[0] + '" class="' + (t[0] === tab ? 'active' : '') + '">' + t[1] + '<span class="count">' + counts[t[0]] + '</span></a>';
    }).join('') + '<span class="tab-indicator"></span></div></div>';
  }
  function positionIndicator(animate) {
    var bar = app.querySelector('.tab-indicator');
    var active = app.querySelector('.tabs a.active');
    if (!bar || !active) return;
    if (!animate) bar.style.transition = 'none';
    bar.style.transform = 'translateX(' + active.offsetLeft + 'px)';
    bar.style.width = active.offsetWidth + 'px';
    if (!animate) requestAnimationFrame(function () { bar.style.transition = ''; });
    // Keep the active tab in view when the strip scrolls on narrow screens.
    var list = active.parentNode;
    if (list && list.scrollWidth > list.clientWidth) {
      var left = active.offsetLeft, right = left + active.offsetWidth;
      if (left < list.scrollLeft) list.scrollLeft = Math.max(0, left - 12);
      else if (right > list.scrollLeft + list.clientWidth) list.scrollLeft = right - list.clientWidth + 12;
    }
  }

  function renderEnv(name, tab) {
    tab = tab || 'deps';
    if (envView && envView.name === name && envView.data && app.querySelector('#panel')) {
      envView.tab = tab;
      Array.prototype.forEach.call(app.querySelectorAll('.tabs a'), function (a) { a.classList.toggle('active', a.getAttribute('data-tab') === tab); });
      positionIndicator(true);
      renderPanel(true);
      return;
    }
    stopLive();
    envView = { name: name, tab: tab, data: null, drift: false };
    setCrumbs([{ label: 'Environments', href: '#/' }, { label: name }]);
    app.innerHTML = '<div class="spotlight"></div><div class="page"><header class="env-head rise"><div class="ringwrap"><div class="skel" style="width:72px;height:24px;border-radius:999px"></div></div><div class="titles"><div class="skel" style="width:280px;height:22px"></div><div class="skel" style="width:360px;height:12px;margin-top:10px"></div></div></header>' +
      '<div class="stats stagger">' + [0, 1, 2, 3, 4].map(function (i) { return '<div class="card stat" style="--i:' + i + '"><div class="skel" style="width:80px;height:10px"></div><div class="skel" style="width:60px;height:18px;margin-top:8px"></div></div>'; }).join('') + '</div></div>';
    var draw = function (first, drift) {
      var v = envView;
      return api('/api/v1/environments/' + encodeURIComponent(name) + (drift ? '?drift=true' : '')).then(function (d) {
        if (envView !== v) return;
        v.data = d;
        v.drift = !!drift;
        setCrumbs([{ label: 'Environments', href: '#/' }, { label: d.title }]);
        if (first) {
          app.innerHTML = '<div class="spotlight"></div><div class="page">' +
            '<header class="env-head rise" id="env-head">' + headHtml(d) + '</header><div id="notices">' + noticesHtml(d, name) + '</div>' +
            '<div class="stats stagger" id="stats">' + statsHtml(d) + '</div>' + tabsHtml(name, d, v.tab) +
            '<div class="panel" id="panel"></div></div>';
          countUp(app);
          positionIndicator(false);
          renderPanel(true);
        } else {
          var head = document.getElementById('env-head');
          if (head) { head.innerHTML = headHtml(d); head.classList.add('static'); }
          var notices = document.getElementById('notices');
          if (notices) notices.innerHTML = noticesHtml(d, name);
          var stats = document.getElementById('stats');
          if (stats) { stats.classList.add('static'); stats.innerHTML = statsHtml(d); }
          var counts = tabCounts(d);
          Array.prototype.forEach.call(app.querySelectorAll('.tabs a'), function (a) { a.querySelector('.count').textContent = counts[a.getAttribute('data-tab')]; });
          renderPanel(false);
        }
        wireHead(name);
      });
    };
    envView.draw = draw;
    draw(true, false).catch(function (e) { app.innerHTML = '<div class="alert">' + I.alert + '<div><b>Could not load environment</b>' + esc(e.message) + '</div></div>'; });
    timer = setInterval(function () { draw(false, false).catch(function () {}); }, 30000);
  }

  function renderPanel(animate) {
    var v = envView, d = v.data, panel = document.getElementById('panel');
    if (!panel || !d) return;
    var html = '';
    switch (v.tab) {
      case 'seeds': html = seedsTab(d); break;
      case 'stale': html = staleTab(d, v.drift); break;
      case 'runs': html = runsTab(d); break;
      case 'targets': html = '<div id="targets-tab">' + targetsTab(d, v.targetStatus || null) + '</div>'; break;
      default: html = depsTab(d);
    }
    panel.className = 'panel ' + (animate ? 'rise' : 'static');
    panel.innerHTML = html;
    wirePanel(v.name);
    if (v.tab === 'targets' && !v.targetStatus) loadTargets(v.name);
  }

  function depsTab(d) {
    if (!d.dependencies.length) return '<div class="card empty"><h2>No dependencies</h2></div>';
    var rows = d.dependencies.map(function (dep, i) {
      var l = dep.latest;
      var st = !l ? badge('grey', 'not probed') : !l.ok ? badge('red', 'down', { dot: true }) : l.warn ? badge('amber', 'warn', { dot: true }) : badge('green', 'up', { dot: true });
      var target = dep.address || dep.url || dep.container || dep.resource || dep.instanceId || dep.dsn || (dep.via ? 'via ' + dep.via.target : '') || (dep.command ? dep.command.join(' ') : '');
      var u = dep.uptime24h, u7 = dep.uptime7d;
      var inc = u.incidents.filter(function (x) { return x.open; }).length;
      var detail = l && l.detail ? Object.keys(l.detail).filter(function (k) { return ['version', 'status', 'image', 'ready', 'desired', 'state', 'daysLeft', 'health', 'privateIp', 'phase'].indexOf(k) >= 0; })
        .map(function (k) { var val = l.detail[k]; return k + '=' + (Array.isArray(val) ? val.join(',') : val); }).join('  ') : '';
      return '<tr style="--i:' + i + '">' +
        '<td><div class="dep-name">' + esc(dep.title || dep.name) + (dep.critical ? chip('red', 'critical') : '') + (dep.autoHeal ? chip('blue', 'auto-heal') : '') + '</div>' +
        '<div class="dep-kind"><span>' + esc(dep.kind) + '</span><span class="mono" title="' + esc(target) + '">' + esc(target) + '</span></div></td>' +
        '<td><div class="status-line">' + st + (l ? '<span>' + esc(l.message) + '</span>' : '') + '</div>' + (detail ? '<div class="detail">' + esc(detail) + '</div>' : '') + '</td>' +
        '<td class="num nowrap">' + (l ? dur(l.latency) : '') + '</td>' +
        '<td>' + spark(u.points, 24) + '<div class="small muted num" style="margin-top:4px">' + (u.total ? pct(u.percent) + ' of ' + u.total + (inc ? ', <span style="color:var(--gr-red)">incident open</span>' : '') : 'no samples') + '</div></td>' +
        '<td class="num nowrap">' + (u7.total ? pct(u7.percent) : '') + '</td>' +
        '<td><div class="cell-actions"><div class="row">' +
        (dep.hasHeal ? '<button class="btn small" data-heal="' + esc(dep.name) + '"><span>Heal (' + esc(dep.heal.action) + ')</span></button>' : '') +
        (dep.canSnapshot ? '<button class="btn small ghost" data-snap="' + esc(dep.name) + '" title="' + (dep.lastSnapshot ? 'last snapshot ' + esc(ago(dep.lastSnapshot.at)) : 'no snapshot yet') + '"><span>Snapshot</span></button>' : '') +
        '</div>' + (dep.hasReset ? '<span class="xs muted mono">reset: ' + esc(dep.reset.strategy) + '</span>' : '') + '</div></td></tr>';
    }).join('');
    var heals = d.heals.length ? '<section class="card table-wrap section" style="--i:1"><div class="table-title"><h3>Recent heals</h3></div><div class="scroll"><table class="heals" style="min-width:720px"><thead><tr><th>When</th><th>Dependency</th><th>Action</th><th>Trigger</th><th>Result</th></tr></thead><tbody>' +
      d.heals.map(function (h, i) {
        return '<tr style="--i:' + i + '"><td class="mono small muted nowrap">' + esc(when(h.at)) + '</td><td>' + esc(h.dep) + '</td><td class="mono small">' + esc(h.action) + '</td><td>' + chip(h.auto ? 'blue' : 'grey', h.auto ? 'automatic' : 'manual') + '</td><td>' + (h.ok ? badge('green', 'ok') : badge('red', 'failed') + ' <span class="small muted">' + esc(h.error || '') + '</span>') + '</td></tr>';
      }).join('') + '</tbody></table></div></section>' : '';
    return '<div class="stagger"><section class="card table-wrap" style="--i:0"><div class="scroll"><table style="min-width:1080px"><thead><tr><th style="width:27%">Dependency</th><th style="width:27%">Status</th><th>Latency</th><th style="width:17%">Uptime 24h</th><th>7d</th><th class="num">Actions</th></tr></thead><tbody>' + rows + '</tbody></table></div></section>' + heals + '</div>';
  }

  function seedsTab(d) {
    if (!d.seeds.length) return '<div class="card empty"><span class="icon-tile">' + I.folder + '</span><h2>No seed sets</h2><p>Add <span class="mono dim">seeds/&lt;persona&gt;/v1/seed.json</span> under the environment directory.</p></div>';
    var personas = {};
    d.seeds.forEach(function (s) {
      s.problems = s.problems || []; s.tags = s.tags || []; s.verify = s.verify || []; s.files = s.files || []; s.steps = s.steps || [];
      (personas[s.persona] = personas[s.persona] || []).push(s);
    });
    return '<div class="stagger">' + Object.keys(personas).map(function (p, gi) {
      var sets = personas[p];
      return '<section class="persona" style="--i:' + gi + '"><div class="persona-head"><h2>Persona: <span class="mono">' + esc(p) + '</span></h2><span class="small muted num">' + sets.length + ' version' + (sets.length === 1 ? '' : 's') + '</span></div>' +
        sets.slice().reverse().map(function (s, i) {
          var stepDetail = function (st) { return st.file || st.url || (st.command ? st.command.join(' ') : '') || (st.sql ? 'inline sql' : '') || (st.script ? 'inline script' : ''); };
          return '<article class="card seed" style="--i:' + i + '"><div class="head"><div style="min-width:0"><div class="id">' + esc(s.id) +
            (s.pinned ? badge('green', 'pinned') : '') + (s.applied ? badge('blue', s.changed ? 'applied, files changed since' : 'applied') : '') + (s.latest ? badge('grey', 'latest') : '') + '</div>' +
            '<div class="title">' + esc(s.title) + '</div></div>' +
            '<button class="btn small' + (s.pinned ? ' primary' : '') + '" data-reset-persona="' + esc(s.persona) + '" data-reset-version="' + s.version + '"><span>Apply</span></button></div>' +
            (s.description ? '<p class="desc">' + esc(s.description) + '</p>' : '') +
            (s.problems.length ? '<div class="alert">' + I.alert + '<div><b>Problems</b>' + s.problems.map(esc).join('<br>') + '</div></div>' : '') +
            '<div class="cols"><div><h4>Steps</h4><ol>' + s.steps.map(function (st, k) {
              return '<li><span class="n">' + (k + 1) + '</span><span style="min-width:0"><b>' + esc(st.name) + '</b> <span class="muted">' + esc(st.kind) + (st.dependency ? ' on ' + esc(st.dependency) : '') + (st.target ? ' via ' + esc(st.target) : '') + '</span><span class="file">' + esc(stepDetail(st)) + '</span></span></li>';
            }).join('') + '</ol></div>' +
            '<div><h4>Verify</h4>' + (s.verify.length ? '<ul>' + s.verify.map(function (c) { return '<li><span style="min-width:0"><b>' + esc(c.name) + '</b> <span class="muted">' + esc(c.kind) + '</span>' + (c.query ? '<span class="file">' + esc(c.query) + '</span>' : c.url ? '<span class="file">' + esc(c.url) + '</span>' : '') + '</span></li>'; }).join('') + '</ul>' : '<p class="small muted" style="margin-top:8px">none</p>') + '</div>' +
            '<div><h4>Files</h4><ul class="files">' + s.files.map(function (f) { return '<li><span class="mono dim" style="white-space:nowrap;overflow:hidden;text-overflow:ellipsis">' + esc(f.path) + '</span><span class="xs muted num">' + f.size + ' B</span></li>'; }).join('') + '</ul>' +
            '<div class="hash">hash <span class="mono dim">' + esc(s.hash.slice(0, 16)) + '</span></div></div></div></article>';
        }).join('') + '</section>';
    }).join('') + '</div>';
  }

  function staleTab(d, drift) {
    var s = d.stale;
    var count = s ? s.stale : 0;
    var html = '<div class="stale-grid stagger"><section style="--i:0"><div class="stale-head"><div style="display:flex;align-items:center;gap:10px">' +
      badge(count === 0 ? 'green' : 'red', count === 0 ? 'fresh' : count + ' stale', { dot: true, size: 'md' }) +
      '<span class="small muted">' + (drift ? 'drift queries included' : 'compared against the applied fingerprints; drift queries not run yet') + '</span></div>' +
      '<button class="btn small" id="act-stale"><span>Check with drift queries</span></button></div>';
    if (!s || !s.items.length) {
      html += '<div class="card empty"><span class="icon-tile">' + I.check + '</span><h2>Nothing stale.</h2><p>' + (s && s.applied ? 'Every fingerprint matches what was recorded when <span class="mono dim">' + esc(s.applied.setId) + '</span> was applied.' : 'The applied seed set matches the pin and the files on disk.') + '</p></div>';
    } else {
      var sev = { stale: 'red', warn: 'amber', info: 'blue' };
      html += '<div class="stagger">' + s.items.map(function (it, i) {
        return '<div class="card stale-item" style="--i:' + i + '">' + badge(sev[it.severity] || 'grey', it.severity) + '<div style="min-width:0"><div>' + (it.dep ? '<b>' + esc(it.dep) + '</b> · ' : '') + esc(it.message) + '</div>' + (it.fix ? '<div class="fix">' + esc(it.fix) + '</div>' : '') + '</div></div>';
      }).join('') + '</div>';
    }
    html += '</section>';
    if (s && s.applied) {
      var a = s.applied;
      html += '<aside class="card pad" style="--i:1;height:max-content"><h3>Applied record</h3><dl class="kv">' +
        '<div><dt>Seed set</dt><dd class="mono" style="color:var(--gr-text)">' + esc(a.setId) + '</dd></div>' +
        '<div><dt>Hash</dt><dd class="mono small">' + esc(a.setHash.slice(0, 24)) + '</dd></div>' +
        '<div><dt>At</dt><dd class="small">' + esc(when(a.at)) + ' <span class="muted">(' + ago(a.at) + ')</span></dd></div>' +
        '<div><dt>Run</dt><dd><a class="mono small" href="#/env/' + esc(d.name) + '/run/' + esc(a.runId) + '">' + esc(a.runId) + '</a></dd></div>' +
        '<div><dt>By</dt><dd class="mono small">' + esc(a.actor || '') + '</dd></div></dl>' +
        '<h4 style="margin-top:16px">Fingerprints</h4><ul class="plain">' + Object.keys(a.fingerprints || {}).map(function (k) { var f = a.fingerprints[k]; return '<li><b style="font-weight:600">' + esc(k) + '</b><span class="mono xs muted" style="white-space:nowrap;overflow:hidden;text-overflow:ellipsis">' + esc(f.kind + ' ' + f.value) + '</span></li>'; }).join('') + '</ul>' +
        '<h4 style="margin-top:16px">Baselines</h4><ul class="plain">' + Object.keys(a.baselines || {}).map(function (k) { return Object.keys(a.baselines[k]).map(function (n) { return '<li class="mono small dim">' + esc(k + '.' + n) + ' = <span style="color:var(--gr-text)">' + esc(a.baselines[k][n]) + '</span></li>'; }).join(''); }).join('') + '</ul></aside>';
    }
    return html + '</div>';
  }

  function runsTab(d) {
    if (!d.runs.length) return '<div class="card empty"><h2>No runs yet</h2><p>Runs appear here after a check, reset, heal or snapshot.</p></div>';
    return '<div class="stagger"><section class="card table-wrap" style="--i:0"><div class="scroll"><table style="min-width:920px"><thead><tr><th style="width:20%">Started</th><th>Kind</th><th>Status</th><th style="width:30%">Summary</th><th class="num">Duration</th><th>By</th></tr></thead><tbody>' +
      d.runs.map(function (r, i) {
        return '<tr style="--i:' + i + '"><td class="nowrap"><a class="mono small" href="#/env/' + esc(d.name) + '/run/' + esc(r.id) + '">' + esc(when(r.startedAt)) + '</a><div class="small muted">' + ago(r.startedAt) + '</div></td>' +
          '<td>' + esc(r.kind) + (r.persona ? ' ' + chip('green', r.persona + '@v' + r.version) : '') + '</td><td>' + statusBadge(r.status) + (r.failed ? ' <span class="small muted">' + r.failed + ' failed</span>' : '') + '</td>' +
          '<td>' + esc(r.summary || r.error || '') + '</td><td class="num nowrap">' + dur(r.duration) + '</td><td class="mono small muted nowrap">' + esc(r.actor || '') + '</td></tr>';
      }).join('') + '</tbody></table></div></section></div>';
  }

  function targetsTab(d, status) {
    if (!d.targets.length) return '<div class="card empty"><h2>No targets</h2><p>This environment declares none. Probes that need none (tcp, http, direct SQL) still work.</p></div>';
    return '<div class="stagger"><section class="card table-wrap" style="--i:0"><div class="scroll"><table style="min-width:860px"><thead><tr><th>Target</th><th>Kind</th><th style="width:34%">Endpoint</th><th style="width:34%">Connectivity</th></tr></thead><tbody>' +
      d.targets.map(function (t, i) {
        var s = status && status.filter(function (x) { return x.name === t.name; })[0];
        var conn = !status ? '<span class="muted" style="display:inline-flex;align-items:center;gap:6px">' + I.ring + ' checking</span>'
          : s.ok ? badge('green', 'reachable', { dot: true }) + ' <span class="mono xs muted">' + esc(s.message) + ' (' + dur(s.latency) + ')</span>'
          : badge('red', 'unreachable', { dot: true }) + ' <span class="mono xs muted">' + esc(s.message) + '</span>';
        return '<tr style="--i:' + i + '"><td style="color:var(--gr-text);font-weight:500">' + esc(t.name) + '</td><td>' + esc(t.kind) + '</td><td class="mono small">' + esc(t.host || '') + '</td><td>' + conn + '</td></tr>';
      }).join('') + '</tbody></table></div></section></div>';
  }
  function loadTargets(name) {
    var v = envView;
    api('/api/v1/environments/' + encodeURIComponent(name) + '/targets').then(function (st) {
      if (envView !== v) return;
      v.targetStatus = st;
      var el = document.getElementById('targets-tab');
      if (el) el.innerHTML = targetsTab(v.data, st);
    }).catch(function (e) { toast('Target check failed', 'error', e.message); });
  }

  function wireHead(name) {
    var check = document.getElementById('act-check');
    if (check) check.addEventListener('click', function () {
      busy(check, 'Checking');
      api('/api/v1/environments/' + encodeURIComponent(name) + '/check?record=true&drift=true', { method: 'POST' }).then(function (rep) {
        showCheck(name, rep);
        if (envView && envView.draw) envView.draw(false, false).catch(function () {});
      }).catch(function (e) { toast('Check failed', 'error', e.message); })
        .then(function () { check.disabled = false; check.innerHTML = '<span>Pre-call check</span>'; });
    });
    var reset = document.getElementById('act-reset');
    if (reset) reset.addEventListener('click', function () { resetDialog(envView.data, name); });
  }
  function wirePanel(name) {
    var panel = document.getElementById('panel');
    if (!panel) return;
    Array.prototype.forEach.call(panel.querySelectorAll('[data-reset-persona]'), function (b) {
      b.addEventListener('click', function () { resetDialog(envView.data, name, b.getAttribute('data-reset-persona'), parseInt(b.getAttribute('data-reset-version'), 10)); });
    });
    Array.prototype.forEach.call(panel.querySelectorAll('[data-heal]'), function (b) {
      b.addEventListener('click', function () {
        busy(b, 'Healing');
        api('/api/v1/environments/' + encodeURIComponent(name) + '/dependencies/' + encodeURIComponent(b.getAttribute('data-heal')) + '/heal', { method: 'POST' })
          .then(function (r) { location.hash = '#/env/' + name + '/run/' + r.runId; })
          .catch(function (e) { toast('Heal failed', 'error', e.message); b.disabled = false; b.innerHTML = '<span>Heal</span>'; });
      });
    });
    Array.prototype.forEach.call(panel.querySelectorAll('[data-snap]'), function (b) {
      b.addEventListener('click', function () {
        var dep = b.getAttribute('data-snap');
        var view = envView.data.dependencies.filter(function (x) { return x.name === dep; })[0] || {};
        modal({
          width: 460,
          title: '<h2>Take a new snapshot</h2>',
          body: '<p class="lead">The current state of <b style="color:var(--gr-text)">' + esc(dep) + '</b> becomes the known-good baseline the reset strategy restores. Postgres is cloned into a template database (open sessions are terminated); a Docker container is stopped briefly, committed, and its mounts archived.</p>' +
            '<div class="note">' + (view.kind === 'docker' ? 'target: commit + volume archive' : 'target: ' + esc((view.reset && view.reset.template) || (view.snapshot && view.snapshot.template) || 'template database')) +
            '<div class="muted" style="margin-top:4px">previous: ' + (view.lastSnapshot ? esc(when(view.lastSnapshot.at)) + ' · ' + esc(view.lastSnapshot.ref) : 'none') + '</div></div>',
          footer: '<button class="btn ghost" data-close><span>Cancel</span></button><button class="btn primary" id="snap-go"><span>Snapshot now</span></button>'
        }, function (root, close) {
          root.querySelector('#snap-go').addEventListener('click', function () {
            busy(root.querySelector('#snap-go'), 'Starting');
            api('/api/v1/environments/' + encodeURIComponent(name) + '/snapshot', { method: 'POST', body: { dependency: dep } })
              .then(function (r) { close(); location.hash = '#/env/' + name + '/run/' + r.runId; })
              .catch(function (e) { toast('Snapshot failed', 'error', e.message); close(); });
          });
        });
      });
    });
    var stale = document.getElementById('act-stale');
    if (stale) stale.addEventListener('click', function () {
      busy(stale, 'Running drift queries');
      envView.draw(false, true).then(function () { renderPanel(true); }).catch(function (e) { toast('Stale check failed', 'error', e.message); });
    });
  }

  function showCheck(name, rep) {
    var rows = rep.results.map(function (r) {
      return '<tr><td><div class="dim">' + esc(r.dep) + '</div><div class="mono xs muted">' + esc(r.kind) + (r.critical ? ' · critical' : '') + '</div></td><td style="width:80px">' + (!r.ok ? badge('red', 'down', { dot: true }) : r.warn ? badge('amber', 'warn', { dot: true }) : badge('green', 'up', { dot: true })) + '</td><td class="num muted small right" style="width:56px">' + dur(r.latency) + '</td></tr>' +
        (!r.ok || r.warn ? '<tr><td colspan="3" class="small muted" style="padding-top:0">' + esc(r.message) + '</td></tr>' : '');
    }).join('');
    var stale = rep.stale && rep.stale.items.length
      ? '<h4 style="margin-top:14px">Stale</h4>' + rep.stale.items.map(function (it) { return '<div class="stale-item" style="padding:8px 0;margin:0;border-bottom:1px solid var(--gr-border)">' + badge(it.severity === 'stale' ? 'red' : it.severity === 'warn' ? 'amber' : 'blue', it.severity) + '<div style="min-width:0">' + (it.dep ? '<b>' + esc(it.dep) + '</b> · ' : '') + esc(it.message) + '</div></div>'; }).join('')
      : '<p style="margin-top:14px;color:var(--gr-green)">Nothing stale.</p>';
    modal({
      width: 560,
      title: readinessBadge(rep.readiness, 'md') + '<div><h2>Pre-call check</h2><p>' + esc(rep.summary) + ' (' + dur(rep.duration) + ')</p></div>',
      body: '<table class="mini-table"><tbody>' + rows + '</tbody></table>' + stale,
      footer: '<a class="btn" href="#/env/' + esc(name) + '/run/' + esc(rep.runId) + '" data-close><span>Open run</span></a><button class="btn primary" data-close><span>Close</span></button>'
    });
  }

  function resetDialog(d, name, persona, version) {
    var personas = {};
    d.seeds.forEach(function (s) { (personas[s.persona] = personas[s.persona] || []).push(s); });
    var names = Object.keys(personas);
    if (!names.length) { toast('This environment has no seed sets', 'error'); return; }
    var pinnedPersona = persona || (d.pinned ? d.pinned.split('@')[0] : names[0]);
    if (!personas[pinnedPersona]) pinnedPersona = names[0];
    var options = names.map(function (p) { return '<option value="' + esc(p) + '"' + (p === pinnedPersona ? ' selected' : '') + '>' + esc(p) + '</option>'; }).join('');
    var versions = function (p, v) {
      return personas[p].slice().reverse().map(function (s) {
        return '<option value="' + s.version + '"' + ((v ? s.version === v : s.pinned) ? ' selected' : '') + '>v' + s.version + ' · ' + esc(s.title) + (s.pinned ? ' (pinned)' : '') + (s.applied ? ' (applied)' : '') + ((s.problems || []).length ? ' (has problems)' : '') + '</option>';
      }).join('');
    };
    modal({
      title: '<h2>Reset ' + esc(d.title) + '</h2>',
      body: '<p class="lead">Greenroom runs the reset strategy of every dependency that has one, applies the selected seed set, verifies it, and records fingerprints. Anything demoed since the last reset is discarded.</p>' +
        '<div class="fields"><div class="field"><label for="rs-persona">Persona</label><select id="rs-persona">' + options + '</select></div>' +
        '<div class="field"><label for="rs-version">Version</label><select id="rs-version">' + versions(pinnedPersona, version) + '</select></div></div>' +
        '<label class="check-row"><input type="checkbox" id="rs-force"> Continue even if the preflight finds dependencies down</label>' +
        '<label class="check-row"><input type="checkbox" id="rs-skip"> Skip reset strategies (apply seed steps only)</label>',
      footer: '<button class="btn ghost" data-close><span>Cancel</span></button><button class="btn danger" id="rs-go"><span>Reset now</span></button>'
    }, function (root, close) {
      var ps = root.querySelector('#rs-persona'), vs = root.querySelector('#rs-version');
      ps.addEventListener('change', function () { vs.innerHTML = versions(ps.value, 0); });
      root.querySelector('#rs-go').addEventListener('click', function () {
        busy(root.querySelector('#rs-go'), 'Starting');
        api('/api/v1/environments/' + encodeURIComponent(name) + '/reset', { method: 'POST', body: { persona: ps.value, version: parseInt(vs.value, 10), force: root.querySelector('#rs-force').checked, skipReset: root.querySelector('#rs-skip').checked } })
          .then(function (r) { close(); toast('Reset started', 'info', 'following the run'); location.hash = '#/env/' + name + '/run/' + r.runId; })
          .catch(function (e) { toast('Reset refused', 'error', e.message); close(); });
      });
    });
  }

  // ---------- run viewer ----------
  function stepIcon(status) {
    if (status === 'running') return '<svg class="ico ring" viewBox="0 0 16 16" aria-label="running"><circle cx="8" cy="8" r="6.4" fill="none" stroke="var(--gr-blue)" stroke-width="2" opacity="0.25"/><path d="M8 1.6a6.4 6.4 0 0 1 6.4 6.4" fill="none" stroke="var(--gr-blue)" stroke-width="2" stroke-linecap="round"/></svg>';
    if (status === 'ok') return '<svg class="ico" viewBox="0 0 16 16" aria-label="ok"><circle cx="8" cy="8" r="6.6" fill="none" stroke="var(--gr-green-line)" stroke-width="1.5"/><path class="check" d="M4.8 8.3 7 10.5l4.2-4.6" fill="none" stroke="var(--gr-green)" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg>';
    if (status === 'failed') return '<svg class="ico" viewBox="0 0 16 16" aria-label="failed"><circle cx="8" cy="8" r="6.6" fill="none" stroke="var(--gr-red-line)" stroke-width="1.5"/><path d="m5.6 5.6 4.8 4.8M10.4 5.6l-4.8 4.8" stroke="var(--gr-red)" stroke-width="1.8" stroke-linecap="round"/></svg>';
    return '<svg class="ico" viewBox="0 0 16 16" aria-label="skipped"><circle cx="8" cy="8" r="6.6" fill="none" stroke="var(--gr-border-strong)" stroke-width="1.5" stroke-dasharray="2.5 2.5"/></svg>';
  }
  function stepHtml(s, i) {
    return '<div class="card step ' + esc(s.status) + '" style="--i:' + i + '">' + stepIcon(s.status) + '<div class="body"><div class="name">' + esc(s.name) + '</div><div class="kind">' + esc(s.kind) + '</div>' +
      (s.error ? '<div class="err">' + esc(s.error) + '</div>' : '') + (s.detail ? '<div class="detail mono small">' + esc(s.detail) + '</div>' : '') + '</div>' +
      '<span class="dur">' + (s.status === 'running' ? '' : dur(s.duration)) + '</span></div>';
  }
  function lineHtml(l) {
    var t = new Date(l.at);
    var ts = isNaN(t.getTime()) ? '' : t.toTimeString().slice(0, 8);
    return '<div class="l"><span class="t">' + ts + '</span>' + (l.step ? '<span class="tag">[' + esc(l.step) + ']</span>' : '') + '<span class="' + esc(l.level) + '">' + esc(l.text) + '</span></div>';
  }
  function renderRun(name, id) {
    stopLive();
    envView = null;
    setCrumbs([{ label: 'Environments', href: '#/' }, { label: name, href: '#/env/' + name }, { label: 'Runs', href: '#/env/' + name + '/runs' }, { label: id }]);
    app.innerHTML = '<div class="spotlight"></div><div class="page"><div class="skel" style="width:260px;height:22px"></div><div class="skel" style="width:420px;height:12px;margin-top:10px"></div></div>';
    var run = null;
    var logEl = function () { return document.getElementById('log'); };
    var draw = function (animate) {
      var r = run, live = r.status === 'running';
      app.innerHTML = '<div class="spotlight"></div><div class="page' + (animate ? '' : ' static') + '"><header class="run-head rise"><div style="min-width:0"><h1>' + esc(r.kind) + (r.persona ? ' <span class="mono">' + esc(r.persona) + '@v' + r.version + '</span>' : '') + statusBadge(r.status) + '</h1>' +
        '<p class="small muted num" style="margin-top:4px">' + esc(when(r.startedAt)) + (r.actor ? ' by <span class="mono">' + esc(r.actor) + '</span>' : '') + (live ? ', running' : ', took ' + dur(r.duration)) + '</p></div>' +
        '<div class="right"><div class="xs muted" style="text-transform:uppercase;letter-spacing:.06em">Run</div><div class="mono small dim">' + esc(r.id) + '</div></div></header>' +
        '<div class="card run-summary rise" style="animation-delay:60ms">' + statusBadge(r.status) + '<span>' + esc(r.summary || r.error || (live ? 'in progress' : '')) + '</span>' + (r.persona ? chip('green', r.persona + '@v' + r.version) : '') + '</div>' +
        '<div class="run-grid"><section><h2>Steps <span class="muted num" id="step-count">' + r.steps.length + '</span></h2><div class="steps stagger" id="steps">' + r.steps.map(stepHtml).join('') + (r.steps.length ? '' : '<div class="small muted">no steps yet</div>') + '</div></section>' +
        '<section style="min-width:0"><h2>Log <span class="muted small">' + r.log.length + ' lines</span></h2><div class="log" id="log" role="log" aria-live="polite">' + r.log.map(lineHtml).join('') + (live ? '<div class="stream" id="stream"><i></i>streaming</div>' : '') + '</div></section></div></div>';
      var l = logEl(); if (l) l.scrollTop = l.scrollHeight;
    };
    var es = new EventSource('/api/v1/environments/' + encodeURIComponent(name) + '/runs/' + encodeURIComponent(id) + '/events');
    source = es;
    es.addEventListener('snapshot', function (e) { run = JSON.parse(e.data); draw(true); });
    es.addEventListener('log', function (e) {
      var ev = JSON.parse(e.data); if (!run) return;
      run.log.push(ev.line);
      var l = logEl(); if (l) { var s = document.getElementById('stream'); if (s) s.insertAdjacentHTML('beforebegin', lineHtml(ev.line)); else l.insertAdjacentHTML('beforeend', lineHtml(ev.line)); l.scrollTop = l.scrollHeight; }
    });
    es.addEventListener('step', function (e) {
      var ev = JSON.parse(e.data); if (!run) return;
      var i = -1;
      run.steps.forEach(function (s, k) { if (s.name === ev.step.name && s.kind === ev.step.kind) i = k; });
      var isNew = i < 0;
      if (isNew) { run.steps.push(ev.step); i = run.steps.length - 1; } else { run.steps[i] = ev.step; }
      var el = document.getElementById('steps');
      if (!el) return;
      el.classList.remove('stagger');
      // Replace only the step that changed so finished checkmarks are not redrawn.
      var tmp = document.createElement('div');
      tmp.innerHTML = stepHtml(ev.step, 0);
      var node = tmp.firstChild;
      if (isNew) {
        var placeholder = el.querySelector('.small.muted');
        if (placeholder) placeholder.remove();
        el.appendChild(node);
      } else if (el.children[i]) {
        el.replaceChild(node, el.children[i]);
      }
      var count = document.getElementById('step-count');
      if (count) count.textContent = run.steps.length;
    });
    es.addEventListener('done', function () {
      es.close(); source = null;
      api('/api/v1/environments/' + encodeURIComponent(name) + '/runs/' + encodeURIComponent(id)).then(function (r) { run = r; draw(false); var st = document.getElementById('stream'); if (st) st.remove(); }).catch(function () { if (run) { run.status = run.status === 'running' ? 'ok' : run.status; draw(false); } });
    });
    es.onerror = function () { if (run && run.status !== 'running') { es.close(); source = null; } };
  }

  // ---------- router ----------
  function route() {
    var h = location.hash.replace(/^#\/?/, '');
    var parts = h.split('/').filter(Boolean).map(decodeURIComponent);
    var go = function (fn) { try { fn(); } catch (e) { app.innerHTML = '<div class="alert">' + I.alert + '<div><b>Error</b>' + esc(e.message) + '</div></div>'; } };
    if (parts[0] === 'env' && parts[1]) {
      if (parts[2] === 'run' && parts[3]) return go(function () { renderRun(parts[1], parts[3]); });
      return go(function () { renderEnv(parts[1], parts[2]); });
    }
    return go(renderOverview);
  }
  window.addEventListener('hashchange', route);
  window.addEventListener('resize', function () { positionIndicator(false); });
  document.getElementById('btn-reload').addEventListener('click', function () {
    var b = document.getElementById('btn-reload');
    busy(b, 'Reloading');
    api('/api/v1/reload', { method: 'POST' }).then(function (r) {
      toast(r.ok ? 'Configuration reloaded' : 'Reload finished with errors', r.ok ? 'ok' : 'error', r.ok ? r.environments + ' environment' + (r.environments === 1 ? '' : 's') : r.error);
      envView = null; route();
    }).catch(function (e) { toast('Reload failed', 'error', e.message); })
      .then(function () { b.disabled = false; b.innerHTML = I.refresh + '<span>Reload config</span>'; });
  });
  document.getElementById('btn-theme').addEventListener('click', function () {
    theme = theme === 'dark' ? 'light' : 'dark';
    try { localStorage.setItem('gr-theme', theme); } catch (e) { /* ignore */ }
    applyTheme();
  });
  setInterval(function () { document.getElementById('clock').textContent = new Date().toLocaleTimeString('en-US', { hour12: false }); }, 1000);
  api('/api/v1/me').then(function (m) { me = m; if (m.auth) document.getElementById('logout-form').hidden = false; }).catch(function () {});
  route();
})();
