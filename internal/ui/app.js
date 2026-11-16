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
    calendar: svg('<rect width="18" height="18" x="3" y="4" rx="2"/><path d="M16 2v4"/><path d="M8 2v4"/><path d="M3 10h18"/>', 19),
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
  function setNav(which) {
    Array.prototype.forEach.call(document.querySelectorAll('#topnav a'), function (a) { a.classList.toggle('active', a.getAttribute('data-nav') === which); });
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
      return Promise.all([api('/api/v1/overview'), api('/api/v1/demos?scope=upcoming&limit=5').catch(function () { return []; })]).then(function (res) {
        var data = res[0], envs = data.environments || [];
        var html = '<div class="spotlight"></div><div class="page' + (first ? '' : ' static') + '">' +
          '<header class="page-head rise"><div><h1>Demo environments</h1><div class="sub">' + envs.length + ' environment' + (envs.length === 1 ? '' : 's') + ' in <span class="mono dim">' + esc(data.environmentsDir) + '</span>, probed every <span class="mono dim">' + esc(data.probeInterval) + '</span></div></div>' +
          '<div class="actions"><button class="btn primary" id="act-new"><span>New environment</span></button></div></header>';
        if (data.loadError) html += '<div class="alert rise">' + I.alert + '<div><b>Some environments failed to load</b><span class="small dim mono">' + esc(data.loadError) + '</span></div></div>';
        if (!envs.length) {
          html += '<div class="card empty rise" style="margin-top:24px"><span class="icon-tile">' + I.folder + '</span><h2>No environments yet</h2><p>Start from a template with the product modules you want to show, or run <span class="mono dim">greenroom init</span> for the bundled example.</p></div>';
        } else {
          html += demoStrip(res[1]) + '<div class="grid stagger">' + envs.map(envCard).join('') + '</div>';
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
      (e.busy ? badge('blue', 'busy', { dot: true }) : '') + (e.liveDemo ? badge('green', 'live demo', { dot: true }) : '') + '</h2>' +
      '<dl>' +
      '<div><dt>Pinned</dt><dd><b class="mono">' + esc(e.pinned || 'none') + '</b></dd></div>' +
      '<div><dt>Applied</dt><dd>' + applied + '</dd></div>' +
      '<div><dt>Dependencies</dt><dd><b class="num">' + e.deps + '</b>' + chips + '</dd></div>' +
      '<div><dt>Stale</dt><dd style="color:' + (e.stale ? 'var(--gr-amber)' : 'var(--gr-green)') + '">' + (e.stale ? e.stale + ' item' + (e.stale === 1 ? '' : 's') : 'fresh') + '</dd></div>' +
      '<div><dt>Uptime 24h</dt><dd>' + (e.lastProbe ? '<b class="num">' + pct(e.uptime24h) + '</b>' : '<span class="muted">no samples</span>') + '</dd></div>' +
      '<div><dt>Next demo</dt><dd>' + (e.liveDemo ? badge('green', 'live until ' + hm(e.liveDemo.end), { dot: true }) : e.nextDemo ? '<b>' + esc(dayLabel(e.nextDemo.start) + ', ' + hm(e.nextDemo.start)) + '</b>' : '<span class="muted">none booked</span>') + '</dd></div>' +
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

  // ---------- brand kits ----------
  function kitLogo(k, size) {
    var s = size || 40;
    var a = k.assets && (k.assets.logo || k.assets.icon);
    var initial = esc((k.name || k.slug || '?').charAt(0).toUpperCase());
    if (!a) return '<span class="kit-logo empty" style="width:' + s + 'px;height:' + s + 'px">' + initial + '</span>';
    return '<span class="kit-logo" style="width:' + s + 'px;height:' + s + 'px"><img alt="" src="/api/v1/brands/' + encodeURIComponent(k.slug) + '/assets/' + encodeURIComponent(a.name) + '?v=' + esc(a.sha256.slice(0, 8)) + '"></span>';
  }
  function swatch(c, label) {
    if (!c) return '';
    return '<span class="swatch" title="' + esc(label + ' ' + c) + '"><i style="background:' + esc(c) + '"></i><span class="mono xs">' + esc(c) + '</span></span>';
  }
  function kitCard(k, i) {
    var names = Object.keys(k.assets || {}).sort();
    return '<article class="card kit-card pad" style="--i:' + i + '"><div class="kit-head">' + kitLogo(k, 44) +
      '<div style="min-width:0;flex:1"><b class="kit-name">' + esc(k.name) + '</b><div class="small muted mono">' + esc(k.slug) + (k.domain ? ' · ' + esc(k.domain) : '') + '</div></div></div>' +
      (k.tagline ? '<p class="small dim kit-tag">' + esc(k.tagline) + '</p>' : '') +
      '<div class="kit-meta">' + swatch(k.primary, 'primary') + swatch(k.accent, 'accent') + '</div>' +
      '<div class="kit-meta small muted">' + names.length + ' asset' + (names.length === 1 ? '' : 's') + (names.length ? ': ' + esc(names.join(', ')) : '') + ' · updated ' + ago(k.updatedAt) + '</div>' +
      '<div class="actions" style="margin-top:12px"><button class="btn small" data-kit-edit="' + esc(k.slug) + '"><span>Edit</span></button>' +
      (k.url ? '<button class="btn small ghost" data-kit-refresh="' + esc(k.slug) + '"><span>Fetch again</span></button>' : '') +
      '<button class="btn small ghost" data-kit-delete="' + esc(k.slug) + '"><span>Delete</span></button></div></article>';
  }
  function renderBrands() {
    stopLive();
    envView = null;
    setCrumbs([{ label: 'Brand kits' }]);
    var draw = function (animate) {
      return api('/api/v1/brands').then(function (list) {
        app.innerHTML = '<div class="spotlight"></div><div class="page' + (animate ? '' : ' static') + '">' +
          '<header class="page-head rise"><div><h1>Brand kits</h1><div class="sub">Paste a prospect\'s link; Greenroom reads their name, colors, logo and icon. A reset can then put them into the demo.</div></div></header>' +
          '<form class="card pad fetch-form rise" id="kit-fetch" autocomplete="off"><div class="field grow"><label for="kf-url">Website</label><input type="text" id="kf-url" class="mono" placeholder="https://www.prospect.example" required></div>' +
          '<div class="field"><label for="kf-name">Name <span class="muted">(optional)</span></label><input type="text" id="kf-name" maxlength="80" placeholder="from the site"></div>' +
          '<div class="field"><label for="kf-slug">Slug <span class="muted">(optional)</span></label><input type="text" id="kf-slug" class="mono" maxlength="63" placeholder="from the domain"></div>' +
          '<button class="btn primary" type="submit" id="kf-go"><span>Fetch brand</span></button></form>' +
          (list.length ? '<div class="grid kit-grid stagger">' + list.map(kitCard).join('') + '</div>'
            : '<div class="card empty rise" style="margin-top:16px"><span class="icon-tile">' + I.folder + '</span><h2>No brand kits yet</h2><p>Fetch one from a prospect\'s website. Only public sites are reachable unless the workspace allows private ones.</p></div>') +
          '</div>';
        wireBrands(list);
      });
    };
    draw(true).catch(function (e) { app.innerHTML = '<div class="alert">' + I.alert + '<div><b>Could not load brand kits</b>' + esc(e.message) + '</div></div>'; });
    var wireBrands = function (list) {
      var bySlug = {};
      list.forEach(function (k) { bySlug[k.slug] = k; });
      var form = document.getElementById('kit-fetch');
      form.addEventListener('submit', function (ev) {
        ev.preventDefault();
        var go = document.getElementById('kf-go');
        busy(go, 'Fetching');
        api('/api/v1/brands', { method: 'POST', body: { url: document.getElementById('kf-url').value.trim(), name: document.getElementById('kf-name').value.trim(), slug: document.getElementById('kf-slug').value.trim() } })
          .then(function (k) { toast('Brand kit ready', 'ok', k.name + ' (' + Object.keys(k.assets).length + ' assets)'); return draw(false).then(function () { editKit(k, draw); }); })
          .catch(function (e) { toast('Fetch failed', 'error', e.message); go.disabled = false; go.innerHTML = '<span>Fetch brand</span>'; });
      });
      Array.prototype.forEach.call(app.querySelectorAll('[data-kit-edit]'), function (b) {
        b.addEventListener('click', function () { editKit(bySlug[b.getAttribute('data-kit-edit')], draw); });
      });
      Array.prototype.forEach.call(app.querySelectorAll('[data-kit-refresh]'), function (b) {
        b.addEventListener('click', function () {
          var k = bySlug[b.getAttribute('data-kit-refresh')];
          busy(b, 'Fetching');
          api('/api/v1/brands', { method: 'POST', body: { url: k.url, slug: k.slug, replace: true } })
            .then(function () { toast('Brand kit refreshed', 'ok', k.name + '; extra assets and edits were replaced'); draw(false); })
            .catch(function (e) { toast('Fetch failed', 'error', e.message); draw(false); });
        });
      });
      Array.prototype.forEach.call(app.querySelectorAll('[data-kit-delete]'), function (b) {
        b.addEventListener('click', function () {
          var k = bySlug[b.getAttribute('data-kit-delete')];
          modal({
            width: 440,
            title: '<h2>Delete ' + esc(k.name) + '</h2>',
            body: '<p class="lead">The kit and its ' + Object.keys(k.assets).length + ' stored files are removed for good. Environments personalized with it report the brand as missing until their next reset.</p>',
            footer: '<button class="btn ghost" data-close><span>Cancel</span></button><button class="btn danger" id="kd-go"><span>Delete kit</span></button>'
          }, function (root, close) {
            root.querySelector('#kd-go').addEventListener('click', function () {
              api('/api/v1/brands/' + encodeURIComponent(k.slug), { method: 'DELETE' })
                .then(function () { close(); toast('Brand kit deleted', 'ok', k.slug); draw(false); })
                .catch(function (e) { close(); toast('Delete failed', 'error', e.message); });
            });
          });
        });
      });
    };
  }
  function editKit(k, redraw) {
    var assetsHtml = function (kit) {
      var names = Object.keys(kit.assets || {}).sort();
      if (!names.length) return '<p class="small muted">No assets.</p>';
      return '<ul class="asset-list">' + names.map(function (n) {
        var a = kit.assets[n];
        return '<li><span class="kit-logo" style="width:36px;height:36px"><img alt="" src="/api/v1/brands/' + encodeURIComponent(kit.slug) + '/assets/' + encodeURIComponent(n) + '?v=' + esc(a.sha256.slice(0, 8)) + '"></span>' +
          '<span style="min-width:0;flex:1"><b>' + esc(n) + '</b> <span class="xs muted mono">' + esc(a.file) + ', ' + esc(a.type) + (a.width ? ', ' + a.width + 'x' + a.height : '') + '</span>' +
          (a.source ? '<span class="xs muted mono block ellipsis" title="' + esc(a.source) + '">' + esc(a.source) + '</span>' : '') + '</span>' +
          '<button class="btn small ghost" type="button" data-asset-rm="' + esc(n) + '"><span>Remove</span></button></li>';
      }).join('') + '</ul>';
    };
    var colorField = function (id, label, val) {
      return '<div class="field"><label for="' + id + '">' + label + '</label><div class="color-field"><input type="color" id="' + id + '-pick" value="' + esc(val || '#22c55e') + '" aria-label="' + label + ' picker">' +
        '<input type="text" class="mono" id="' + id + '" value="' + esc(val || '') + '" placeholder="house color" maxlength="7"></div></div>';
    };
    modal({
      width: 640,
      title: kitLogo(k, 32) + '<div><h2>' + esc(k.name) + '</h2><p class="mono">' + esc(k.slug) + (k.domain ? ' · ' + esc(k.domain) : '') + '</p></div>',
      body: '<div class="fields"><div class="field"><label for="ke-name">Name</label><input type="text" id="ke-name" maxlength="80" value="' + esc(k.name) + '"></div>' +
        '<div class="field"><label for="ke-tag">Tagline</label><input type="text" id="ke-tag" maxlength="200" value="' + esc(k.tagline || '') + '"></div></div>' +
        '<div class="fields">' + colorField('ke-primary', 'Primary', k.primary) + colorField('ke-accent', 'Accent', k.accent) + '</div>' +
        '<h4 style="margin-top:18px">Assets</h4><div id="ke-assets">' + assetsHtml(k) + '</div>' +
        '<div class="fields add-asset"><div class="field"><label for="ke-an">Asset name</label><input type="text" id="ke-an" class="mono" maxlength="32" placeholder="hero"></div>' +
        '<div class="field"><label for="ke-au">Image link</label><input type="text" id="ke-au" class="mono" placeholder="https://cdn.prospect.example/hero.png"></div></div>' +
        '<div class="actions" style="margin-top:8px"><button class="btn small" type="button" id="ke-add"><span>Fetch asset</span></button></div>' +
        ((k.notes || []).length ? '<details class="params"><summary>Fetch notes <span class="muted">(' + k.notes.length + ')</span></summary><ul class="plain small muted">' + k.notes.map(function (n) { return '<li>' + esc(n) + '</li>'; }).join('') + '</ul></details>' : '') +
        '<p class="small muted" style="margin-top:14px">Seed files use <span class="mono">{{brand.name}}</span>, <span class="mono">{{brand.primary}}</span>, <span class="mono">{{brand.logo}}</span> and the other keys; asset steps copy the logo or any asset by name.</p>',
      footer: '<button class="btn ghost" data-close><span>Close</span></button><button class="btn primary" id="ke-save"><span>Save</span></button>'
    }, function (root, close) {
      ['ke-primary', 'ke-accent'].forEach(function (id) {
        var pick = root.querySelector('#' + id + '-pick'), text = root.querySelector('#' + id);
        pick.addEventListener('input', function () { text.value = pick.value; });
        text.addEventListener('input', function () { if (/^#[0-9a-fA-F]{6}$/.test(text.value)) pick.value = text.value; });
      });
      var wireAssets = function (kit) {
        Array.prototype.forEach.call(root.querySelectorAll('[data-asset-rm]'), function (b) {
          b.addEventListener('click', function () {
            busy(b, 'Removing');
            api('/api/v1/brands/' + encodeURIComponent(kit.slug) + '/assets/' + encodeURIComponent(b.getAttribute('data-asset-rm')), { method: 'DELETE' })
              .then(function (nk) { root.querySelector('#ke-assets').innerHTML = assetsHtml(nk); wireAssets(nk); redraw(false); })
              .catch(function (e) { toast('Remove failed', 'error', e.message); });
          });
        });
      };
      wireAssets(k);
      root.querySelector('#ke-add').addEventListener('click', function () {
        var b = root.querySelector('#ke-add');
        busy(b, 'Fetching');
        api('/api/v1/brands/' + encodeURIComponent(k.slug) + '/assets', { method: 'POST', body: { name: root.querySelector('#ke-an').value.trim(), url: root.querySelector('#ke-au').value.trim() } })
          .then(function (nk) { root.querySelector('#ke-assets').innerHTML = assetsHtml(nk); wireAssets(nk); root.querySelector('#ke-an').value = ''; root.querySelector('#ke-au').value = ''; redraw(false); })
          .catch(function (e) { toast('Asset not added', 'error', e.message); })
          .then(function () { b.disabled = false; b.innerHTML = '<span>Fetch asset</span>'; });
      });
      root.querySelector('#ke-save').addEventListener('click', function () {
        var b = root.querySelector('#ke-save');
        busy(b, 'Saving');
        api('/api/v1/brands/' + encodeURIComponent(k.slug), { method: 'PATCH', body: {
          name: root.querySelector('#ke-name').value, tagline: root.querySelector('#ke-tag').value,
          primary: root.querySelector('#ke-primary').value.trim(), accent: root.querySelector('#ke-accent').value.trim() } })
          .then(function () { close(); toast('Brand kit saved', 'ok', k.slug); redraw(false); })
          .catch(function (e) { toast('Not saved', 'error', e.message); b.disabled = false; b.innerHTML = '<span>Save</span>'; });
      });
    });
  }

  // ---------- demos ----------
  var DEMO_STAGES = ['intro', 'discovery', 'deep-dive', 'poc', 'executive', 'other'];
  var DEMO_OUTCOMES = ['positive', 'neutral', 'negative', 'no-show', 'rescheduled'];
  var DEMO_KINDS = ['note', 'issue', 'question', 'follow-up', 'feedback'];
  var demoScope = 'upcoming', demoEnv = '';
  function demoFinal(st) { return st === 'done' || st === 'missed' || st === 'cancelled'; }
  function demoBadge(st) {
    var map = { scheduled: 'blue', preparing: 'blue', ready: 'green', 'prep-failed': 'red', live: 'green', done: 'grey', missed: 'amber', cancelled: 'grey' };
    return badge(map[st] || 'grey', st, { dot: st === 'preparing' || st === 'live' });
  }
  function outcomeChip(o) {
    if (!o) return '<span class="muted small">none</span>';
    var map = { positive: 'green', neutral: 'grey', negative: 'red', 'no-show': 'amber', rescheduled: 'blue' };
    return chip(map[o] || 'grey', o);
  }
  function kindChip(k) {
    var map = { note: 'blue', issue: 'red', question: 'amber', 'follow-up': 'green', feedback: 'blue', system: 'grey' };
    return chip(map[k] || 'grey', k);
  }
  function hm(iso) { var d = new Date(iso); return isNaN(d.getTime()) ? '' : d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }); }
  function dayLabel(iso) { var d = new Date(iso); return isNaN(d.getTime()) ? '' : d.toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric' }); }
  function slotText(b) {
    var s = dayLabel(b.start) + ', ' + hm(b.start);
    if (b.end && new Date(b.end).getTime() > new Date(b.start).getTime()) s += ' to ' + (dayLabel(b.end) === dayLabel(b.start) ? '' : dayLabel(b.end) + ', ') + hm(b.end);
    return s;
  }
  function span(ms) {
    var m = Math.max(0, Math.round(ms / 60000));
    if (m < 60) return m + 'm';
    var h = Math.floor(m / 60);
    if (h < 48) return h + 'h' + (m % 60 ? ' ' + (m % 60) + 'm' : '');
    return Math.floor(h / 24) + 'd';
  }
  function countdown(b) {
    var now = Date.now(), s = new Date(b.start).getTime(), e = new Date(b.end).getTime();
    if (b.status === 'cancelled') return 'cancelled';
    if (now < s) return 'in ' + span(s - now);
    if (now < e && !demoFinal(b.status)) return 'on now, ends in ' + span(e - now);
    return 'ended ' + span(now - Math.max(e, s)) + ' ago';
  }
  function setChip(b) { return b.persona ? chip('green', b.persona + '@v' + b.version) : ''; }
  function loadKits() {
    return api('/api/v1/brands').then(function (list) { var m = {}; (list || []).forEach(function (k) { m[k.slug] = k; }); return m; }).catch(function () { return {}; });
  }
  function prospectCell(b, kits) {
    var k = b.brand && kits[b.brand];
    var name = b.prospect || (k ? k.name : '');
    return '<span class="prospect-cell">' + (k ? kitLogo(k, 22) : '') + '<span class="ellipsis">' + (name ? esc(name) : '<span class="muted">none</span>') + '</span></span>';
  }
  function brandChip(d, kits) {
    var k = d.brand && kits[d.brand];
    if (k) return '<span class="brand-chip">' + kitLogo(k, 18) + '<span>' + esc(k.name) + '</span></span>';
    return d.brand ? chip('blue', d.brand) : chip('grey', 'house brand');
  }
  function demoRows(list, kits, withEnv) {
    return list.map(function (b, i) {
      return '<tr style="--i:' + i + '"><td class="nowrap"><div class="small">' + esc(slotText(b)) + '</div><div class="xs muted">' + esc(countdown(b)) + '</div></td>' +
        '<td><a href="#/demos/' + esc(b.id) + '" class="demo-title">' + esc(b.title) + '</a>' + (b.booked ? '' : ' ' + chip('grey', 'logged')) +
        '<div class="xs muted">' + esc(b.stage) + (b.owner ? ', ' + esc(b.owner) : '') + '</div></td>' +
        '<td>' + prospectCell(b, kits) + '</td>' +
        (withEnv ? '<td><a class="mono small" href="#/env/' + esc(b.env) + '">' + esc(b.env) + '</a></td>' : '') +
        '<td>' + setChip(b) + '</td><td>' + demoBadge(b.status) + '</td>' +
        '<td>' + (b.readiness ? readinessBadge(b.readiness) : '<span class="muted small">none</span>') + '</td>' +
        '<td>' + outcomeChip(b.outcome) + '</td><td class="num">' + esc(b.journalCount) + '</td></tr>';
    }).join('');
  }
  function demoTable(list, kits, withEnv) {
    return '<div class="scroll"><table class="demo-table" style="min-width:' + (withEnv ? 1040 : 920) + 'px"><thead><tr><th>When</th><th>Title</th><th>Prospect</th>' + (withEnv ? '<th>Environment</th>' : '') +
      '<th>Seed set</th><th>Status</th><th>Readiness</th><th>Outcome</th><th class="num">Journal</th></tr></thead><tbody>' + demoRows(list, kits, withEnv) + '</tbody></table></div>';
  }
  function demoStrip(list) {
    if (!list || !list.length) return '';
    return '<section class="demo-strip rise"><div class="strip-head"><h3>Upcoming demos</h3><a class="small" href="#/demos">All demos</a></div><div class="strip-list stagger">' + list.map(function (b, i) {
      return '<a class="card link demo-chip" href="#/demos/' + esc(b.id) + '" style="--i:' + i + '"><span class="dc-top">' + demoBadge(b.status) + '<span class="xs muted">' + esc(countdown(b)) + '</span></span>' +
        '<b class="dc-title">' + esc(b.title) + '</b><span class="xs muted dc-when">' + esc(dayLabel(b.start) + ', ' + hm(b.start)) + ' on <span class="mono">' + esc(b.env) + '</span></span></a>';
    }).join('') + '</div></section>';
  }

  function renderDemos() {
    stopLive();
    envView = null;
    setCrumbs([{ label: 'Demos' }]);
    app.innerHTML = '<div class="spotlight"></div><div class="page"><header class="rise"><h1>Demos</h1><div class="skel" style="width:300px;height:12px;margin-top:10px"></div></header></div>';
    var first = true;
    var draw = function () {
      var q = '?scope=' + encodeURIComponent(demoScope) + (demoEnv ? '&env=' + encodeURIComponent(demoEnv) : '');
      return Promise.all([api('/api/v1/demos' + q), api('/api/v1/overview'), loadKits()]).then(function (r) {
        var list = r[0] || [], envs = r[1].environments || [], kits = r[2];
        if (demoEnv && !envs.some(function (e) { return e.name === demoEnv; })) envs.push({ name: demoEnv, title: demoEnv });
        var seg = [['upcoming', 'Upcoming'], ['past', 'Past'], ['all', 'All']].map(function (s) {
          return '<button type="button" class="seg-btn' + (s[0] === demoScope ? ' active' : '') + '" data-scope="' + s[0] + '" aria-pressed="' + (s[0] === demoScope) + '">' + s[1] + '</button>';
        }).join('');
        var envOpts = '<option value="">All environments</option>' + envs.map(function (e) {
          return '<option value="' + esc(e.name) + '"' + (e.name === demoEnv ? ' selected' : '') + '>' + esc(e.title || e.name) + '</option>';
        }).join('');
        var word = { upcoming: 'upcoming', past: 'past', all: '' }[demoScope];
        var html = '<div class="spotlight"></div><div class="page' + (first ? '' : ' static') + '">' +
          '<header class="page-head rise"><div><h1>Demos</h1><div class="sub">' + list.length + (word ? ' ' + word : '') + ' demo' + (list.length === 1 ? '' : 's') + (demoEnv ? ' on <span class="mono dim">' + esc(demoEnv) + '</span>' : '') + '</div></div>' +
          '<div class="actions"><a class="btn ghost" href="/api/v1/demos.ics' + (demoEnv ? '?env=' + encodeURIComponent(demoEnv) : '') + '" download><span>.ics export</span></a>' +
          '<button class="btn" id="act-log"><span>Log a past demo</span></button><button class="btn primary" id="act-book"><span>Book a demo</span></button></div></header>' +
          '<div class="demo-filters rise"><div class="seg" role="group" aria-label="Which demos">' + seg + '</div>' +
          '<label class="sr-only" for="df-env">Environment</label><select id="df-env" class="select">' + envOpts + '</select></div>';
        if (!list.length) {
          html += '<div class="card empty rise" style="margin-top:16px"><span class="icon-tile">' + I.calendar + '</span><h2>' + (demoScope === 'upcoming' ? 'Nothing booked' : 'No demos here yet') + '</h2>' +
            '<p>A booked demo gets its environment reset to the seed set and checked before the call, and held while it is live.</p></div>';
        } else {
          html += '<div class="stagger" style="margin-top:16px"><section class="card table-wrap" style="--i:0">' + demoTable(list, kits, true) + '</section></div>';
        }
        app.innerHTML = html + '</div>';
        first = false;
        Array.prototype.forEach.call(app.querySelectorAll('[data-scope]'), function (b) {
          b.addEventListener('click', function () { demoScope = b.getAttribute('data-scope'); draw().catch(function (e) { toast('Could not load demos', 'error', e.message); }); });
        });
        document.getElementById('df-env').addEventListener('change', function (ev) { demoEnv = ev.target.value; draw().catch(function (e) { toast('Could not load demos', 'error', e.message); }); });
        document.getElementById('act-book').addEventListener('click', function () { demoDialog('book', demoEnv); });
        document.getElementById('act-log').addEventListener('click', function () { demoDialog('log', demoEnv); });
      });
    };
    draw().catch(function (e) { app.innerHTML = '<div class="alert">' + I.alert + '<div><b>Could not load demos</b>' + esc(e.message) + '</div></div>'; });
    timer = setInterval(function () { if (!document.getElementById('modal-root').innerHTML) draw().catch(function () {}); }, 30000);
  }

  function demoHeadHtml(d, kits) {
    var runs = [['prepRunId', 'prep run'], ['checkRunId', 'pre-call check'], ['cleanupRunId', 'cleanup run']].filter(function (r) { return d[r[0]]; });
    var prepAt = d.booked && d.status === 'scheduled' ? new Date(new Date(d.start).getTime() - (d.prepLeadMs || 0)) : null;
    return '<div class="titles"><h1><span>' + esc(d.title) + '</span>' + demoBadge(d.status) + (d.booked ? '' : chip('grey', 'logged')) + '</h1>' +
      '<p>' + esc(slotText(d)) + ' <span class="muted">(' + esc(countdown(d)) + ')</span>' + (prepAt ? '<span class="muted">, prep at ' + esc(hm(prepAt.toISOString())) + '</span>' : '') + '</p>' +
      '<div class="tpl-line"><a class="mono" href="#/env/' + esc(d.env) + '">' + esc(d.env) + '</a>' + setChip(d) + brandChip(d, kits) +
      (d.prospect ? '<span class="dim">' + esc(d.prospect) + '</span>' : '') + (d.readiness ? readinessBadge(d.readiness) : '') + (d.outcome ? outcomeChip(d.outcome) : '') + '</div>' +
      (runs.length ? '<div class="run-links small">' + runs.map(function (r) { return '<a href="#/env/' + esc(d.env) + '/run/' + esc(d[r[0]]) + '">' + esc(r[1]) + '</a>'; }).join('') + '</div>' : '') + '</div>' +
      '<div class="actions">' + (d.booked && d.status === 'scheduled' ? '<button class="btn" id="act-prepare"><span>Prepare now</span></button>' : '') +
      (demoFinal(d.status) ? '' : '<button class="btn ghost" id="act-cancel"><span>Cancel demo</span></button>') + '</div>';
  }
  function demoNoticesHtml(d) {
    var html = '';
    if (d.error) html += '<div class="alert rise">' + I.alert + '<div><b>Preparation problem</b><span class="small">' + esc(d.error) + '</span></div></div>';
    var now = Date.now();
    if (d.booked && (d.status === 'live' || d.status === 'ready' || d.status === 'prep-failed') && now >= new Date(d.start).getTime() && now < new Date(d.end).getTime()) {
      html += '<div class="alert info rise">' + I.info + '<div><b>Live until ' + esc(hm(d.end)) + '</b><span class="small">Resets and snapshots of ' + esc(d.env) + ' are refused unless someone overrides the live demo.</span></div></div>';
    }
    return html;
  }
  function journalHtml(d) {
    var list = (d.journal || []).slice().reverse();
    if (!list.length) return '<li class="muted small">No entries yet.</li>';
    return list.map(function (e, i) {
      var sys = e.kind === 'system';
      return '<li class="entry' + (sys ? ' system' : '') + '" style="--i:' + i + '"><div class="entry-head">' + kindChip(e.kind) + '<span class="small dim">' + esc(e.author) + '</span>' +
        '<span class="xs muted" title="' + esc(when(e.at)) + '">' + esc(ago(e.at)) + '</span>' +
        (e.runId ? '<a class="xs" href="#/env/' + esc(d.env) + '/run/' + esc(e.runId) + '">run</a>' : '') + '<span class="spacer"></span>' +
        (sys ? '' : '<button class="btn small ghost" type="button" data-entry-rm="' + esc(e.id) + '"><span>Delete</span></button>') + '</div>' +
        '<div class="entry-text">' + esc(e.text) + '</div></li>';
    }).join('');
  }
  function demoEditHtml(d) {
    var opts = function (list, cur, empty) {
      return (empty ? '<option value="">' + esc(empty) + '</option>' : '') + list.map(function (v) { return '<option value="' + esc(v) + '"' + (v === cur ? ' selected' : '') + '>' + esc(v) + '</option>'; }).join('');
    };
    return '<h3>Details</h3>' +
      '<div class="field"><label for="de-stage">Stage</label><select id="de-stage">' + opts(DEMO_STAGES, d.stage) + '</select></div>' +
      '<div class="field"><label for="de-outcome">Outcome</label><select id="de-outcome">' + opts(DEMO_OUTCOMES, d.outcome, 'Not yet') + '</select></div>' +
      '<div class="field"><label for="de-next">Next step</label><input type="text" id="de-next" maxlength="500" value="' + esc(d.nextStep || '') + '"></div>' +
      '<div class="field"><label for="de-prospect">Prospect</label><input type="text" id="de-prospect" maxlength="120" value="' + esc(d.prospect || '') + '"></div>' +
      '<div class="field"><label for="de-att">Attendees <span class="muted">(one per line)</span></label><textarea id="de-att" rows="3">' + esc((d.attendees || []).join('\n')) + '</textarea></div>' +
      '<div class="actions" style="margin-top:12px"><button class="btn primary" type="button" id="de-save"><span>Save</span></button></div>' +
      '<dl class="kv" style="margin-top:16px">' +
      '<div><dt>Owner</dt><dd>' + esc(d.owner || '') + '</dd></div>' +
      (d.booked ? '<div><dt>Prep lead</dt><dd class="mono small">' + esc(String(d.prepLead || '').replace(/([hm])0m0s$|([hm])0s$/, '$1$2')) + '</dd></div><div><dt>After the demo</dt><dd class="small">' + (d.resetAfter ? 'reset to the pin' + (d.cleanup ? ' (' + esc(d.cleanup) + ')' : '') : 'left as is') + '</dd></div>' : '') +
      '<div><dt>Created</dt><dd class="small">' + esc(when(d.createdAt)) + (d.createdBy ? ' by ' + esc(d.createdBy) : '') + '</dd></div>' +
      '<div><dt>Id</dt><dd class="mono small">' + esc(d.id) + '</dd></div></dl>';
  }
  function parseGoDuration(s) {
    var ms = 0, re = /([0-9.]+)(ms|h|m|s)/g, m;
    while ((m = re.exec(String(s || '')))) ms += parseFloat(m[1]) * { h: 3600000, m: 60000, s: 1000, ms: 1 }[m[2]];
    return ms;
  }

  function renderDemo(id) {
    stopLive();
    envView = null;
    setCrumbs([{ label: 'Demos', href: '#/demos' }, { label: id }]);
    app.innerHTML = '<div class="spotlight"></div><div class="page"><div class="skel" style="width:280px;height:22px"></div><div class="skel" style="width:380px;height:12px;margin-top:10px"></div></div>';
    var st = { d: null, kits: {} };
    var url = '/api/v1/demos/' + encodeURIComponent(id);
    var wireJournal = function () {
      Array.prototype.forEach.call(app.querySelectorAll('[data-entry-rm]'), function (b) {
        b.addEventListener('click', function () {
          busy(b, 'Deleting');
          api(url + '/journal/' + encodeURIComponent(b.getAttribute('data-entry-rm')), { method: 'DELETE' })
            .then(function (d) { show(d, false); })
            .catch(function (e) { toast('Not deleted', 'error', e.message); b.disabled = false; b.innerHTML = '<span>Delete</span>'; });
        });
      });
    };
    var wireHeadActions = function () {
      var prep = document.getElementById('act-prepare');
      if (prep) prep.addEventListener('click', function () {
        busy(prep, 'Starting');
        api(url + '/prepare', { method: 'POST' }).then(function (r) { toast('Preparation started', 'info', r.demo.persona + '@v' + r.demo.version); show(r.demo, false); })
          .catch(function (e) { toast('Not prepared', 'error', e.message); prep.disabled = false; prep.innerHTML = '<span>Prepare now</span>'; });
      });
      var cancel = document.getElementById('act-cancel');
      if (cancel) cancel.addEventListener('click', function () {
        modal({
          width: 440,
          title: '<h2>Cancel ' + esc(st.d.title) + '</h2>',
          body: '<p class="lead">The demo stays in the tracker with its journal, marked cancelled. Its slot on ' + esc(st.d.env) + ' is freed' + (st.d.status === 'preparing' ? '; the preparation run already started and finishes on its own' : '') + '.</p>',
          footer: '<button class="btn ghost" data-close><span>Keep it</span></button><button class="btn danger" id="dc-go"><span>Cancel demo</span></button>'
        }, function (root, close) {
          root.querySelector('#dc-go').addEventListener('click', function () {
            busy(root.querySelector('#dc-go'), 'Cancelling');
            api(url + '/cancel', { method: 'POST' }).then(function (d) { close(); toast('Demo cancelled', 'ok', d.title); show(d, false); })
              .catch(function (e) { close(); toast('Not cancelled', 'error', e.message); });
          });
        });
      });
    };
    var show = function (d, full) {
      d.prepLeadMs = parseGoDuration(d.prepLead);
      st.d = d;
      setCrumbs([{ label: 'Demos', href: '#/demos' }, { label: d.title }]);
      if (full) {
        app.innerHTML = '<div class="spotlight"></div><div class="page">' +
          '<header class="demo-head rise" id="demo-head">' + demoHeadHtml(d, st.kits) + '</header><div id="demo-notices">' + demoNoticesHtml(d) + '</div>' +
          '<div class="demo-grid"><section class="card pad rise" style="animation-delay:40ms"><h3>Journal <span class="muted num small" id="je-total">' + (d.journal || []).length + '</span></h3>' +
          '<form class="entry-form" id="je-form" autocomplete="off"><div class="row"><label class="sr-only" for="je-kind">Kind</label><select id="je-kind" class="select small-select">' +
          DEMO_KINDS.map(function (k) { return '<option value="' + k + '">' + k + '</option>'; }).join('') + '</select><span class="xs muted num" id="je-count">0 / 4000</span></div>' +
          '<label class="sr-only" for="je-text">Entry</label><textarea id="je-text" rows="3" maxlength="4000" placeholder="What they asked, what broke, what to follow up on"></textarea>' +
          '<div class="actions"><button class="btn primary small" type="submit" id="je-go"><span>Add entry</span></button></div></form>' +
          '<ol class="timeline stagger" id="journal">' + journalHtml(d) + '</ol></section>' +
          '<aside class="card pad rise" style="animation-delay:80ms" id="demo-edit">' + demoEditHtml(d) + '</aside></div></div>';
        var text = document.getElementById('je-text'), count = document.getElementById('je-count');
        text.addEventListener('input', function () { count.textContent = text.value.length + ' / 4000'; });
        document.getElementById('je-form').addEventListener('submit', function (ev) {
          ev.preventDefault();
          var go = document.getElementById('je-go');
          if (!text.value.trim()) { text.focus(); return; }
          busy(go, 'Adding');
          api(url + '/journal', { method: 'POST', body: { kind: document.getElementById('je-kind').value, text: text.value } })
            .then(function (nd) { text.value = ''; count.textContent = '0 / 4000'; show(nd, false); })
            .catch(function (e) { toast('Entry not added', 'error', e.message); })
            .then(function () { go.disabled = false; go.innerHTML = '<span>Add entry</span>'; });
        });
        wireEdit();
      } else {
        var head = document.getElementById('demo-head');
        if (head) { head.classList.add('static'); head.innerHTML = demoHeadHtml(d, st.kits); }
        var notices = document.getElementById('demo-notices');
        if (notices) notices.innerHTML = demoNoticesHtml(d);
        var j = document.getElementById('journal');
        if (j) { j.classList.remove('stagger'); j.innerHTML = journalHtml(d); }
        var total = document.getElementById('je-total');
        if (total) total.textContent = (d.journal || []).length;
      }
      wireHeadActions();
      wireJournal();
    };
    var wireEdit = function () {
      var b = document.getElementById('de-save');
      if (!b) return;
      b.addEventListener('click', function () {
        busy(b, 'Saving');
        var att = document.getElementById('de-att').value.split('\n').map(function (s) { return s.trim(); }).filter(Boolean);
        api(url, { method: 'PATCH', body: { stage: document.getElementById('de-stage').value, outcome: document.getElementById('de-outcome').value,
          nextStep: document.getElementById('de-next').value, prospect: document.getElementById('de-prospect').value, attendees: att } })
          .then(function (nd) { toast('Demo saved', 'ok', nd.title); show(nd, false); document.getElementById('demo-edit').innerHTML = demoEditHtml(nd); wireEdit(); })
          .catch(function (e) { toast('Not saved', 'error', e.message); b.disabled = false; b.innerHTML = '<span>Save</span>'; });
      });
    };
    Promise.all([api(url), loadKits()]).then(function (r) { st.kits = r[1]; show(r[0], true); })
      .catch(function (e) { app.innerHTML = '<div class="alert">' + I.alert + '<div><b>Could not load the demo</b>' + esc(e.message) + '</div></div>'; });
    timer = setInterval(function () {
      if (!st.d || (demoFinal(st.d.status) && st.d.cleanup !== 'pending')) return;
      api(url).then(function (d) { if (st.d && st.d.id === d.id && location.hash.indexOf('#/demos/') === 0) show(d, false); }).catch(function () {});
    }, 15000);
  }

  // ---------- book or log a demo ----------
  function pad2(n) { return (n < 10 ? '0' : '') + n; }
  function localInput(d) { return d.getFullYear() + '-' + pad2(d.getMonth() + 1) + '-' + pad2(d.getDate()) + 'T' + pad2(d.getHours()) + ':' + pad2(d.getMinutes()); }
  // isoWithOffset turns a datetime-local value (local time) into RFC 3339
  // with the browser's offset.
  function isoWithOffset(v) {
    var d = new Date(v);
    if (!v || isNaN(d.getTime())) return '';
    var off = -d.getTimezoneOffset(), sign = off >= 0 ? '+' : '-';
    off = Math.abs(off);
    return localInput(d) + ':00' + sign + pad2(Math.floor(off / 60)) + ':' + pad2(off % 60);
  }
  function demoDialog(mode, preEnv) {
    var book = mode === 'book';
    Promise.all([api('/api/v1/overview'), loadKits()]).then(function (r) {
      var envs = r[0].environments || [], kits = r[1];
      if (!envs.length) { toast('No environments yet', 'error', 'create one first'); return; }
      var envName = envs.some(function (e) { return e.name === preEnv; }) ? preEnv : envs[0].name;
      var start = new Date();
      if (book) { start.setMinutes(0, 0, 0); start.setHours(start.getHours() + 2); } else { start.setSeconds(0, 0); start.setMinutes(start.getMinutes() - 60); }
      var durs = [['30m', '30 minutes'], ['45m', '45 minutes'], ['1h', '1 hour'], ['1h30m', '90 minutes'], ['2h', '2 hours']];
      var sel = function (id, list, cur) { return '<select id="' + id + '">' + list.map(function (o) { return '<option value="' + esc(o[0]) + '"' + (o[0] === cur ? ' selected' : '') + '>' + esc(o[1]) + '</option>'; }).join('') + '</select>'; };
      var pairs = function (list) { return list.map(function (v) { return [v, v]; }); };
      var kitList = Object.keys(kits).map(function (k) { return kits[k]; }).sort(function (a, b) { return a.name.localeCompare(b.name); });
      modal({
        width: 640,
        title: '<h2>' + (book ? 'Book a demo' : 'Log a past demo') + '</h2>',
        body: '<p class="lead">' + (book ? 'Greenroom resets the environment to the seed set before the start, runs the pre-call check, and holds the environment while the demo is live. Preparation runs while the portal is up.'
          : 'Record a demo that already happened so its outcome and notes sit with the rest.') + '</p>' +
          '<div class="fields"><div class="field"><label for="bd-title">Title</label><input type="text" id="bd-title" maxlength="120" placeholder="Larkspur discovery call"></div>' +
          '<div class="field"><label for="bd-prospect">Prospect</label><input type="text" id="bd-prospect" maxlength="120" placeholder="Larkspur"></div></div>' +
          '<div class="fields"><div class="field"><label for="bd-env">Environment</label>' + sel('bd-env', envs.map(function (e) { return [e.name, e.title || e.name]; }), envName) + '</div>' +
          '<div class="field"><label for="bd-at">' + (book ? 'Starts' : 'Started') + '</label><input type="datetime-local" id="bd-at" value="' + esc(localInput(start)) + '"></div></div>' +
          '<div class="fields"><div class="field"><label for="bd-dur">Length</label>' + sel('bd-dur', durs, '45m') + '</div>' +
          (book ? '<div class="field"><label for="bd-prep">Prepare</label>' + sel('bd-prep', [['15m', '15 minutes before'], ['30m', '30 minutes before'], ['1h', '1 hour before']], '30m') + '</div>'
            : '<div class="field"><label for="bd-outcome">Outcome</label>' + sel('bd-outcome', [['', 'Not yet']].concat(pairs(DEMO_OUTCOMES)), '') + '</div>') + '</div>' +
          '<div class="fields"><div class="field"><label for="bd-persona">Persona</label><select id="bd-persona"></select></div>' +
          '<div class="field"><label for="bd-version">Version</label><select id="bd-version"></select></div></div>' +
          '<div class="fields"><div class="field"><label for="bd-brand">Brand kit</label>' + sel('bd-brand', [['', 'House brand (no prospect)']].concat(kitList.map(function (k) { return [k.slug, k.name + (k.domain ? ' (' + k.domain + ')' : '')]; })), '') + '</div>' +
          '<div class="field"><label for="bd-stage">Stage</label>' + sel('bd-stage', pairs(DEMO_STAGES), 'discovery') + '</div></div>' +
          (book ? '<label class="check-row"><input type="checkbox" id="bd-after"> Reset to the pin with the house brand when the demo ends</label>' : '') +
          '<div id="bd-error"></div>',
        footer: '<button class="btn ghost" data-close><span>Cancel</span></button><button class="btn primary" id="bd-go"><span>' + (book ? 'Book demo' : 'Log demo') + '</span></button>'
      }, function (root, close) {
        var q = function (s) { return root.querySelector(s); };
        var sets = {};
        var fillVersions = function () {
          var p = q('#bd-persona').value, list = (sets[p] || []).slice().reverse();
          q('#bd-version').innerHTML = list.map(function (s) {
            return '<option value="' + s.version + '"' + (s.pinned ? ' selected' : '') + '>v' + s.version + ', ' + esc(s.title) + (s.pinned ? ' (pinned)' : '') + ((s.problems || []).length ? ' (has problems)' : '') + '</option>';
          }).join('');
        };
        var loadSets = function (name) {
          q('#bd-persona').innerHTML = '<option>loading</option>';
          q('#bd-version').innerHTML = '';
          api('/api/v1/environments/' + encodeURIComponent(name)).then(function (d) {
            if (q('#bd-env').value !== name) return;
            sets = {};
            d.seeds.forEach(function (s) { (sets[s.persona] = sets[s.persona] || []).push(s); });
            var names = Object.keys(sets), pinned = d.pinned ? d.pinned.split('@')[0] : names[0];
            q('#bd-persona').innerHTML = names.length ? names.map(function (p) { return '<option value="' + esc(p) + '"' + (p === pinned ? ' selected' : '') + '>' + esc(p) + '</option>'; }).join('') : '<option value="">no seed sets</option>';
            fillVersions();
          }).catch(function (e) { q('#bd-persona').innerHTML = '<option value="">could not load</option>'; toast('Could not load seed sets', 'error', e.message); });
        };
        q('#bd-env').addEventListener('change', function () { loadSets(q('#bd-env').value); });
        q('#bd-persona').addEventListener('change', fillVersions);
        q('#bd-brand').addEventListener('change', function () {
          var k = kits[q('#bd-brand').value];
          if (k && !q('#bd-prospect').value.trim()) q('#bd-prospect').value = k.name;
        });
        loadSets(envName);
        q('#bd-title').focus();
        q('#bd-go').addEventListener('click', function () {
          var go = q('#bd-go'), errEl = q('#bd-error');
          var body = { env: q('#bd-env').value, title: q('#bd-title').value.trim(), prospect: q('#bd-prospect').value.trim(), start: isoWithOffset(q('#bd-at').value),
            duration: q('#bd-dur').value, persona: q('#bd-persona').value, version: parseInt(q('#bd-version').value, 10) || 0, brand: q('#bd-brand').value, stage: q('#bd-stage').value };
          if (book) { body.prepLead = q('#bd-prep').value; body.resetAfter = q('#bd-after').checked; } else { body.booked = false; body.outcome = q('#bd-outcome').value; }
          errEl.innerHTML = '';
          if (!body.title) { errEl.innerHTML = '<div class="alert">' + I.alert + '<div><b>Title is required</b></div></div>'; q('#bd-title').focus(); return; }
          busy(go, book ? 'Booking' : 'Saving');
          api('/api/v1/demos', { method: 'POST', body: body }).then(function (d) {
            close();
            toast(book ? 'Demo booked' : 'Demo logged', 'ok', d.title + ', ' + slotText(d));
            location.hash = '#/demos/' + d.id;
          }).catch(function (e) {
            errEl.innerHTML = '<div class="alert">' + I.alert + '<div><b>' + (book ? 'Not booked' : 'Not logged') + '</b>' + esc(e.message) + '</div></div>';
            go.disabled = false; go.innerHTML = '<span>' + (book ? 'Book demo' : 'Log demo') + '</span>';
          });
        });
      });
    }).catch(function (e) { toast('Could not open the dialog', 'error', e.message); });
  }

  function demosTab(d) {
    var list = d.demos || [];
    var head = '<div class="table-title"><h3>Demos on this environment</h3><button class="btn small primary" id="act-book-tab"><span>Book a demo</span></button></div>';
    if (!list.length) return '<div class="stagger"><section class="card" style="--i:0">' + head + '<div class="empty" style="padding:36px 24px"><span class="icon-tile">' + I.calendar + '</span><h2>No demos yet</h2><p>Book one and it is prepared, checked and held here.</p></div></section></div>';
    return '<div class="stagger"><section class="card table-wrap" style="--i:0">' + head + demoTable(list, {}, false) + '</section></div>';
  }

  // ---------- environment ----------
  var TABS = [['deps', 'Dependencies'], ['seeds', 'Seed sets'], ['stale', 'Stale'], ['runs', 'Runs'], ['demos', 'Demos'], ['targets', 'Targets']];
  function tabCounts(d) {
    return { deps: d.deps, seeds: d.seeds.length, stale: d.stale ? d.stale.items.length : 0, runs: d.runs.length, demos: (d.demos || []).length, targets: d.targets.length };
  }
  function stat(i, label, value, hint, cls, count) {
    var v = count ? '<div class="value ' + (cls || '') + '" data-count="' + count.v + '" data-decimals="' + (count.d || 0) + '" data-suffix="' + esc(count.s || '') + '">' + count.v.toFixed(count.d || 0) + esc(count.s || '') + '</div>'
      : '<div class="value ' + (cls || '') + '">' + value + '</div>';
    return '<div class="card stat" style="--i:' + i + '"><div class="label">' + esc(label) + '</div>' + v + (hint ? '<div class="hint">' + esc(hint) + '</div>' : '') + '</div>';
  }
  function statsHtml(d) {
    var stale = d.stale ? d.stale.stale : 0;
    return stat(0, 'Pinned seed set', '<span class="mono">' + esc(d.pinned || 'none') + '</span>', '', 'mono green') +
      stat(1, 'Applied', '<span class="mono">' + esc(d.applied ? d.applied.setId : 'none') + '</span>', d.applied ? ago(d.applied.at) + (d.applied.brand ? ' for ' + d.applied.brand.name : '') : 'never reset', 'mono') +
      stat(2, 'Dependencies', '', d.down ? d.down + ' down' : d.warn ? d.warn + ' warn' : 'all up', d.down ? 'amber' : '', { v: d.deps }) +
      stat(3, 'Uptime 24h', '', d.lastProbe ? 'all dependencies' : 'no samples yet', '', { v: d.lastProbe ? d.uptime24h : 0, d: 2, s: '%' }) +
      stat(4, 'Stale items', '', 'limit ' + d.staleAfter, stale ? 'amber' : '', { v: stale });
  }
  function headHtml(d) {
    return ring(d.readiness) +
      '<div class="titles"><h1>' + esc(d.title) + '</h1><p>' + esc(d.summary) + (d.lastProbe ? ' <span class="muted">(probed ' + ago(d.lastProbe) + ')</span>' : '') + '</p>' +
      (d.description ? '<p class="desc">' + esc(d.description) + '</p>' : '') + templateLine(d.template) + '</div>' +
      '<div class="actions"><button class="btn ghost" id="act-book"><span>Book a demo</span></button><button class="btn" id="act-check"><span>Pre-call check</span></button>' +
      '<button class="btn primary shimmer" id="act-reset"' + (d.busy ? ' disabled' : '') + '><span>Reset to known good</span></button></div>';
  }
  function noticesHtml(d, name) {
    var html = '';
    if (d.liveDemo) html += '<div class="alert info rise">' + I.info + '<div><b>Live demo until ' + esc(hm(d.liveDemo.end)) + ': ' + esc(d.liveDemo.title) + '</b><a href="#/demos/' + esc(d.liveDemo.id) + '" class="small">Resets and snapshots are refused until it ends; open the demo</a></div></div>';
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
      case 'demos': html = demosTab(d); break;
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
          '<td>' + esc(r.kind) + (r.persona ? ' ' + chip('green', r.persona + '@v' + r.version) : '') + (r.brand ? ' ' + chip('blue', r.brand) : '') + '</td><td>' + statusBadge(r.status) + (r.failed ? ' <span class="small muted">' + r.failed + ' failed</span>' : '') + '</td>' +
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
    var bookBtn = document.getElementById('act-book');
    if (bookBtn) bookBtn.addEventListener('click', function () { demoDialog('book', name); });
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
            '<div class="muted" style="margin-top:4px">previous: ' + (view.lastSnapshot ? esc(when(view.lastSnapshot.at)) + ' · ' + esc(view.lastSnapshot.ref) : 'none') + '</div></div>' +
            liveWarning(envView.data, 'snap-override'),
          footer: '<button class="btn ghost" data-close><span>Cancel</span></button><button class="btn primary" id="snap-go"><span>Snapshot now</span></button>'
        }, function (root, close) {
          root.querySelector('#snap-go').addEventListener('click', function () {
            busy(root.querySelector('#snap-go'), 'Starting');
            var ov = root.querySelector('#snap-override');
            api('/api/v1/environments/' + encodeURIComponent(name) + '/snapshot', { method: 'POST', body: { dependency: dep, override: !!(ov && ov.checked) } })
              .then(function (r) { close(); location.hash = '#/env/' + name + '/run/' + r.runId; })
              .catch(function (e) { toast('Snapshot failed', 'error', e.message); close(); });
          });
        });
      });
    });
    var bookTab = document.getElementById('act-book-tab');
    if (bookTab) bookTab.addEventListener('click', function () { demoDialog('book', name); });
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

  // liveWarning is the notice and override checkbox shown in the reset and
  // snapshot dialogs while a booked demo is live on the environment.
  function liveWarning(d, id) {
    if (!d || !d.liveDemo) return '';
    var l = d.liveDemo;
    return '<div class="alert" style="margin-top:14px">' + I.alert + '<div><b>Live demo until ' + esc(hm(l.end)) + ': ' + esc(l.title) + '</b><span class="small">The server refuses this unless you override; the override is written to the demo journal.</span>' +
      '<label class="check-row"><input type="checkbox" id="' + esc(id) + '"> Override the live demo</label></div></div>';
  }
  function resetDialog(d, name, persona, version, brand) {
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
        '<div class="field"><label for="rs-brand">Personalize for</label><select id="rs-brand"><option value="">House brand (no prospect)</option></select></div>' +
        '<label class="check-row"><input type="checkbox" id="rs-force"> Continue even if the preflight finds dependencies down</label>' +
        '<label class="check-row"><input type="checkbox" id="rs-skip"> Skip reset strategies (apply seed steps only)</label>' +
        liveWarning(d, 'rs-override'),
      footer: '<button class="btn ghost" data-close><span>Cancel</span></button><button class="btn danger" id="rs-go"><span>Reset now</span></button>'
    }, function (root, close) {
      var ps = root.querySelector('#rs-persona'), vs = root.querySelector('#rs-version'), bs = root.querySelector('#rs-brand');
      ps.addEventListener('change', function () { vs.innerHTML = versions(ps.value, 0); });
      api('/api/v1/brands').then(function (kits) {
        var last = d.applied && d.applied.brand ? d.applied.brand.slug : '';
        kits.forEach(function (k) {
          var o = document.createElement('option');
          o.value = k.slug; o.textContent = k.name + (k.domain ? ' (' + k.domain + ')' : '');
          if (k.slug === (brand || last)) o.selected = true;
          bs.appendChild(o);
        });
      }).catch(function () {});
      root.querySelector('#rs-go').addEventListener('click', function () {
        busy(root.querySelector('#rs-go'), 'Starting');
        api('/api/v1/environments/' + encodeURIComponent(name) + '/reset', { method: 'POST', body: { persona: ps.value, version: parseInt(vs.value, 10), force: root.querySelector('#rs-force').checked, skipReset: root.querySelector('#rs-skip').checked, brand: bs.value, override: !!(root.querySelector('#rs-override') || {}).checked } })
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
        '<div class="card run-summary rise" style="animation-delay:60ms">' + statusBadge(r.status) + '<span>' + esc(r.summary || r.error || (live ? 'in progress' : '')) + '</span>' + (r.persona ? chip('green', r.persona + '@v' + r.version) : '') + (r.brand ? chip('blue', 'for ' + r.brand) : '') + '</div>' +
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
    setNav(parts[0] === 'brands' ? 'brands' : parts[0] === 'demos' ? 'demos' : 'envs');
    var go = function (fn) { try { fn(); } catch (e) { app.innerHTML = '<div class="alert">' + I.alert + '<div><b>Error</b>' + esc(e.message) + '</div></div>'; } };
    if (parts[0] === 'brands') return go(renderBrands);
    if (parts[0] === 'demos') return go(parts[1] ? function () { renderDemo(parts[1]); } : renderDemos);
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
