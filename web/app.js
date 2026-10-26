'use strict';
// SinarPanel UI — vanilla JS, tanpa library & tanpa innerHTML untuk data
// dari server (semua teks lewat textContent), cocok dengan CSP ketat.
(() => {
  const state = { me: null, csrf: '', timer: null, fm: { path: '', entries: [], sel: new Set() } };
  const $root = document.getElementById('root');
  const $modals = document.getElementById('modal-root');
  const $toasts = document.getElementById('toast-root');

  // ---------- DOM helpers ----------
  function h(tag, props, ...kids) {
    const el = document.createElement(tag);
    if (props) {
      for (const [k, v] of Object.entries(props)) {
        if (v == null || v === false) continue;
        if (k === 'class') el.className = v;
        else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
        else if (['value', 'checked', 'disabled', 'selected', 'htmlFor', 'title', 'type', 'name', 'placeholder', 'autocomplete', 'required', 'multiple', 'spellcheck', 'readOnly'].includes(k)) el[k] = v;
        else el.setAttribute(k, v === true ? '' : v);
      }
    }
    append(el, kids);
    return el;
  }
  function append(el, kids) {
    for (const c of kids.flat(Infinity)) {
      if (c == null || c === false) continue;
      el.append(c instanceof Node ? c : document.createTextNode(String(c)));
    }
    return el;
  }
  function clear(el) { while (el.firstChild) el.firstChild.remove(); return el; }
  // Pengganti el.append() yang melewati null/false (append bawaan menulis "null").
  function put(el, ...kids) { return append(el, kids); }
  function fill(el, ...kids) { return append(clear(el), kids); }

  const ICONS = {
    home: [['path', 'M3 9l9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z'], ['polyline', '9 22 9 12 15 12 15 22']],
    globe: [['circle', 12, 12, 10], ['line', 2, 12, 22, 12], ['path', 'M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z']],
    folder: [['path', 'M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z']],
    file: [['path', 'M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z'], ['polyline', '13 2 13 9 20 9']],
    shield: [['path', 'M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z']],
    lock: [['rect', 3, 11, 18, 11, 2], ['path', 'M7 11V7a5 5 0 0 1 10 0v4']],
    unlock: [['rect', 3, 11, 18, 11, 2], ['path', 'M7 11V7a5 5 0 0 1 9.9-1']],
    logout: [['path', 'M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4'], ['polyline', '16 17 21 12 16 7'], ['line', 21, 12, 9, 12]],
    plus: [['line', 12, 5, 12, 19], ['line', 5, 12, 19, 12]],
    refresh: [['polyline', '23 4 23 10 17 10'], ['polyline', '1 20 1 14 7 14'], ['path', 'M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15']],
    upload: [['path', 'M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4'], ['polyline', '17 8 12 3 7 8'], ['line', 12, 3, 12, 15]],
    download: [['path', 'M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4'], ['polyline', '7 10 12 15 17 10'], ['line', 12, 15, 12, 3]],
    trash: [['polyline', '3 6 5 6 21 6'], ['path', 'M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6m3 0V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2']],
    edit: [['path', 'M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7'], ['path', 'M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z']],
    up: [['line', 12, 19, 12, 5], ['polyline', '5 12 12 5 19 12']],
    more: [['circle', 12, 12, 1], ['circle', 19, 12, 1], ['circle', 5, 12, 1]],
    link: [['path', 'M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71'], ['path', 'M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71']],
    power: [['path', 'M18.36 6.64a9 9 0 1 1-12.73 0'], ['line', 12, 2, 12, 12]],
    server: [['rect', 2, 2, 20, 8, 2], ['rect', 2, 14, 20, 8, 2], ['line', 6, 6, 6.01, 6], ['line', 6, 18, 6.01, 18]],
    menu: [['line', 3, 12, 21, 12], ['line', 3, 6, 21, 6], ['line', 3, 18, 21, 18]],
    x: [['line', 18, 6, 6, 18], ['line', 6, 6, 18, 18]],
    external: [['path', 'M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6'], ['polyline', '15 3 21 3 21 9'], ['line', 10, 14, 21, 3]],
    archive: [['polyline', '21 8 21 21 3 21 3 8'], ['rect', 1, 3, 22, 5, 0], ['line', 10, 12, 14, 12]],
    copy: [['rect', 9, 9, 13, 13, 2], ['path', 'M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1']],
    terminal: [['polyline', '4 17 10 11 4 5'], ['line', 12, 19, 20, 19]],
    check: [['polyline', '20 6 9 17 4 12']],
    key: [['circle', 7.5, 15.5, 5.5], ['path', 'M11.4 11.6L21 2m-4 4l3 3m-6 0l2 2']],
  };
  const SVGNS = 'http://www.w3.org/2000/svg';
  function icon(name, cls) {
    const s = document.createElementNS(SVGNS, 'svg');
    s.setAttribute('viewBox', '0 0 24 24');
    s.setAttribute('fill', 'none');
    s.setAttribute('stroke', 'currentColor');
    s.setAttribute('stroke-width', '2');
    s.setAttribute('stroke-linecap', 'round');
    s.setAttribute('stroke-linejoin', 'round');
    s.setAttribute('class', 'ic' + (cls ? ' ' + cls : ''));
    s.setAttribute('aria-hidden', 'true');
    for (const [tag, ...a] of ICONS[name] || []) {
      const e = document.createElementNS(SVGNS, tag);
      if (tag === 'path') e.setAttribute('d', a[0]);
      else if (tag === 'polyline') e.setAttribute('points', a[0]);
      else if (tag === 'circle') { e.setAttribute('cx', a[0]); e.setAttribute('cy', a[1]); e.setAttribute('r', a[2]); }
      else if (tag === 'line') { e.setAttribute('x1', a[0]); e.setAttribute('y1', a[1]); e.setAttribute('x2', a[2]); e.setAttribute('y2', a[3]); }
      else if (tag === 'rect') { e.setAttribute('x', a[0]); e.setAttribute('y', a[1]); e.setAttribute('width', a[2]); e.setAttribute('height', a[3]); e.setAttribute('rx', a[4]); }
      s.append(e);
    }
    return s;
  }
  function btn(label, opts = {}) {
    return h('button', { type: 'button', class: opts.class || '', title: opts.title, disabled: opts.disabled, onclick: opts.onclick },
      opts.icon ? icon(opts.icon) : null, label);
  }
  function spinner() { return h('span', { class: 'spin' }); }

  // ---------- Format ----------
  function fmtBytes(n) {
    if (n == null) return '-';
    const u = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return (i === 0 ? n : n.toFixed(n < 10 ? 1 : 0)) + ' ' + u[i];
  }
  function fmtDate(v) {
    const d = typeof v === 'number' ? new Date(v * 1000) : new Date(v);
    return d.toLocaleString('id-ID', { dateStyle: 'medium', timeStyle: 'short' });
  }
  function fmtDay(v) { return new Date(v).toLocaleDateString('id-ID', { dateStyle: 'medium' }); }
  function fmtUptime(s) {
    const d = Math.floor(s / 86400), hh = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60);
    return (d ? d + ' hari ' : '') + (hh ? hh + ' jam ' : '') + m + ' menit';
  }

  // ---------- API ----------
  async function api(method, url, body) {
    const opt = { method, headers: {}, credentials: 'same-origin' };
    if (method !== 'GET') opt.headers['X-CSRF-Token'] = state.csrf;
    if (body !== undefined) { opt.headers['Content-Type'] = 'application/json'; opt.body = JSON.stringify(body); }
    let res;
    try { res = await fetch(url, opt); } catch (e) { throw new Error('Tidak bisa menghubungi server'); }
    let data = {};
    try { data = await res.json(); } catch (e) { /* bukan JSON */ }
    if (res.status === 401 && url !== '/api/login') {
      state.me = null;
      renderLogin('Sesi berakhir, silakan login lagi.');
      throw Object.assign(new Error('Sesi berakhir'), { silent: true });
    }
    if (data.code === '2fa_setup_required') location.hash = '#/security';
    if (!res.ok) throw Object.assign(new Error(data.error || 'HTTP ' + res.status), { data, status: res.status });
    return data;
  }
  const q = (o) => new URLSearchParams(o).toString();

  // ---------- Toast & modal ----------
  function toast(msg, kind = '') {
    const t = h('div', { class: 'toast ' + kind }, msg);
    $toasts.append(t);
    setTimeout(() => t.remove(), kind === 'err' ? 7000 : 3500);
  }
  function showErr(e) { if (!e.silent) toast(e.message, 'err'); }

  const modalStack = [];
  function modal({ title, body, footer, size = '', canClose }) {
    const closeBtn = h('button', { type: 'button', class: 'ghost icon-only', title: 'Tutup', onclick: () => tryClose() }, icon('x'));
    const $body = h('div', { class: 'body' }, body);
    const $foot = h('footer', null, footer);
    if (!footer) $foot.classList.add('hidden');
    const box = h('div', { class: 'modal ' + size, role: 'dialog', 'aria-modal': 'true' },
      h('header', null, h('h2', null, title), closeBtn), $body, $foot);
    const back = h('div', { class: 'modal-back' }, box);
    let downOnBack = false;
    back.addEventListener('mousedown', (e) => { downOnBack = e.target === back; });
    back.addEventListener('click', (e) => { if (e.target === back && downOnBack) tryClose(); });
    $modals.append(back);
    const m = {
      locked: false,
      close() { back.remove(); const i = modalStack.indexOf(m); if (i >= 0) modalStack.splice(i, 1); },
      setBody(...c) { append(clear($body), c); },
      setFooter(...c) { append(clear($foot), c); $foot.classList.toggle('hidden', c.flat().filter(Boolean).length === 0); },
      tryClose,
      box,
    };
    function tryClose() {
      if (m.locked) return;
      if (canClose && !canClose()) return;
      m.close();
    }
    modalStack.push(m);
    setTimeout(() => { const f = box.querySelector('[autofocus], input, textarea, select'); if (f) f.focus(); }, 30);
    return m;
  }
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && modalStack.length) modalStack[modalStack.length - 1].tryClose();
  });

  function confirmDialog({ title, message, confirmText = 'Lanjut', danger = false, requireText, checkbox }) {
    return new Promise((resolve) => {
      let done = false;
      const input = requireText ? h('input', { type: 'text', placeholder: requireText, autocomplete: 'off', spellcheck: false }) : null;
      const chk = checkbox ? h('input', { type: 'checkbox' }) : null;
      const ok = btn(confirmText, { class: danger ? 'danger solid' : 'primary' });
      if (input) { ok.disabled = true; input.addEventListener('input', () => { ok.disabled = input.value.trim() !== requireText; }); }
      const m = modal({
        title,
        body: h('div', { class: 'stack' },
          h('div', null, message),
          input ? h('label', { class: 'field' }, h('span', null, 'Ketik ', h('code', null, requireText), ' untuk konfirmasi'), input) : null,
          chk ? h('label', { class: 'check' }, chk, h('span', null, checkbox)) : null),
        footer: [btn('Batal', { onclick: () => m.tryClose() }), ok],
        canClose: () => { if (!done) { done = true; resolve(null); } return true; },
      });
      ok.addEventListener('click', () => { done = true; m.close(); resolve({ checked: chk ? chk.checked : false }); });
      if (input) input.addEventListener('keydown', (e) => { if (e.key === 'Enter' && !ok.disabled) ok.click(); });
    });
  }

  function promptDialog({ title, label, value = '', hint, okText = 'Simpan', selectUntilDot }) {
    return new Promise((resolve) => {
      let done = false;
      const input = h('input', { type: 'text', value, spellcheck: false, autocomplete: 'off' });
      const ok = btn(okText, { class: 'primary' });
      const m = modal({
        title,
        body: h('label', { class: 'field' }, h('span', null, label), input, hint ? h('small', null, hint) : null),
        footer: [btn('Batal', { onclick: () => m.tryClose() }), ok],
        canClose: () => { if (!done) { done = true; resolve(null); } return true; },
      });
      const submit = () => { const v = input.value.trim(); if (!v) return; done = true; m.close(); resolve(v); };
      ok.addEventListener('click', submit);
      input.addEventListener('keydown', (e) => { if (e.key === 'Enter') submit(); });
      setTimeout(() => {
        input.focus();
        const dot = selectUntilDot ? value.lastIndexOf('.') : -1;
        input.setSelectionRange(0, dot > 0 ? dot : value.length);
      }, 40);
    });
  }

  // Jalankan operasi server yang mengembalikan {log}, tampilkan output-nya.
  async function opModal(title, fn, { onDone } = {}) {
    const m = modal({ title, size: 'wide', body: h('div', { class: 'row' }, spinner(), 'Sedang berjalan… jangan tutup halaman ini.') });
    m.locked = true;
    try {
      const res = await fn();
      m.locked = false;
      m.setBody(
        h('div', { class: 'stack' },
          h('div', { class: 'alert info' }, '✓ Berhasil'),
          res.ssl_error ? h('div', { class: 'alert warn' }, res.ssl_error) : null,
          res.log ? h('pre', { class: 'log' }, res.log) : null));
      m.setFooter(btn('Tutup', { class: 'primary', onclick: () => m.close() }));
      if (onDone) onDone(res);
    } catch (e) {
      m.locked = false;
      if (e.silent) { m.close(); return; }
      m.setBody(h('div', { class: 'stack' },
        h('div', { class: 'alert err' }, e.message),
        e.data && e.data.log ? h('pre', { class: 'log' }, e.data.log) : null));
      m.setFooter(btn('Tutup', { onclick: () => m.close() }));
      if (onDone) onDone(null, e);
    }
  }

  // ---------- Login ----------
  function renderLogin(msg) {
    stopTimer();
    while (modalStack.length) modalStack[0].close();
    const user = h('input', { type: 'text', name: 'username', autocomplete: 'username', required: true, spellcheck: false, autofocus: true });
    const pass = h('input', { type: 'password', name: 'password', autocomplete: 'current-password', required: true });
    const otp = h('input', { type: 'text', name: 'otp', autocomplete: 'one-time-code', inputmode: 'numeric', maxlength: '6', placeholder: '6 digit, kosongkan jika belum aktif' });
    const err = h('div', { class: 'alert err hidden' });
    const submit = h('button', { type: 'submit', class: 'primary' }, icon('lock'), 'Masuk');
    const form = h('form', { class: 'stack', autocomplete: 'on' },
      msg ? h('div', { class: 'alert warn' }, msg) : null,
      err,
      h('label', { class: 'field' }, h('span', null, 'User SSH'), user),
      h('label', { class: 'field' }, h('span', null, 'Password'), pass),
      h('label', { class: 'field' }, h('span', null, 'Kode 2FA'), otp),
      submit);
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      err.classList.add('hidden');
      submit.disabled = true;
      fill(submit, spinner(), 'Memeriksa…');
      try {
        await api('POST', '/api/login', { username: user.value.trim(), password: pass.value, otp: otp.value.trim() });
        pass.value = '';
        await boot();
      } catch (ex) {
        err.textContent = ex.message;
        err.classList.remove('hidden');
        pass.value = '';
        pass.focus();
      } finally {
        submit.disabled = false;
        fill(submit, icon('lock'), 'Masuk');
      }
    });
    fill($root, h('div', { class: 'login-wrap' },
      h('div', { class: 'login-card' },
        h('div', { class: 'brand' }, h('img', { src: '/assets/favicon.svg', alt: '' }), 'SinarPanel'),
        h('div', { class: 'sub' }, 'Login dengan user Linux/SSH server ini'),
        form)));
    user.focus();
  }

  // ---------- Layout ----------
  const NAV = [
    ['dashboard', 'Dashboard', 'home'],
    ['sites', 'Situs & SSL', 'globe'],
    ['files', 'File Manager', 'folder'],
    ['security', 'Keamanan', 'shield'],
  ];
  let $main, $nav, $sidebar;

  function renderApp() {
    const me = state.me;
    $nav = h('nav', { class: 'nav' }, NAV.map(([id, label, ic]) => h('a', { href: '#/' + id, 'data-id': id }, icon(ic), label)));
    $sidebar = h('aside', { class: 'sidebar' },
      h('div', { class: 'brand' }, h('img', { src: '/assets/favicon.svg', alt: '' }), 'SinarPanel'),
      $nav,
      h('div', { class: 'foot' },
        h('div', { class: 'who' }, h('div', { class: 'avatar' }, me.user.slice(0, 1).toUpperCase()),
          h('div', { class: 'ellipsis' }, h('div', null, h('strong', null, me.user)), h('div', { class: 'muted small ellipsis' }, me.hostname))),
        btn('Logout', { icon: 'logout', class: 'sm', onclick: logout })));
    $main = h('main', { class: 'main' });
    fill($root, h('div', { class: 'layout' }, $sidebar, $main));
    $sidebar.addEventListener('click', (e) => { if (e.target.closest('a')) $sidebar.classList.remove('open'); });
    route();
  }

  function pageHead(title, ...actions) {
    return h('div', { class: 'page-head' },
      h('button', { type: 'button', class: 'menu-toggle ghost icon-only', onclick: () => $sidebar.classList.toggle('open') }, icon('menu')),
      h('h1', null, title), h('div', { class: 'grow' }), actions);
  }

  async function logout() {
    try { await api('POST', '/api/logout'); } catch (e) { /* abaikan */ }
    state.me = null; state.csrf = '';
    renderLogin();
  }

  function stopTimer() { if (state.timer) { clearInterval(state.timer); state.timer = null; } }

  function parseHash() {
    const raw = location.hash.replace(/^#\/?/, '');
    const [id, qs] = raw.split('?');
    return { id: id || 'dashboard', params: new URLSearchParams(qs || '') };
  }

  function route() {
    if (!state.me) return;
    stopTimer();
    let { id, params } = parseHash();
    if (state.me.needs_2fa_setup) id = 'security';
    if (!VIEWS[id]) id = 'dashboard';
    for (const a of $nav.querySelectorAll('a')) a.classList.toggle('active', a.dataset.id === id);
    clear($main);
    VIEWS[id](params);
  }
  window.addEventListener('hashchange', route);

  // ---------- Dashboard ----------
  function usageCard(title, used, total, extra) {
    const pct = total ? Math.round((used / total) * 100) : 0;
    const fill = h('i', { class: pct > 90 ? 'danger' : pct > 75 ? 'warn' : '' });
    fill.style.width = pct + '%';
    return h('div', { class: 'card' },
      h('h3', null, title),
      h('div', { class: 'stat-big' }, pct + '%'),
      h('div', { class: 'bar' }, fill),
      h('div', { class: 'muted small' }, fmtBytes(used) + ' dari ' + fmtBytes(total) + (extra ? ' · ' + extra : '')));
  }

  function viewDashboard() {
    const body = h('div', { class: 'grid' }, h('div', { class: 'muted' }, 'Memuat…'));
    put($main, pageHead('Dashboard', btn('', { icon: 'refresh', class: 'icon-only', title: 'Refresh', onclick: () => load() })), body);
    async function load() {
      let s, u;
      try { [s, u] = await Promise.all([api('GET', '/api/system'), api('GET', '/api/update').catch(() => null)]); } catch (e) { showErr(e); return; }
      const memUsed = s.mem_total - s.mem_avail;
      const disk = s.disk || { total: 0, free: 0 };
      const apacheOk = s.apache === 'active';
      fill(body,
        h('div', { class: 'card' },
          h('h3', null, 'Server'),
          h('dl', { class: 'kv' },
            h('dt', null, 'Hostname'), h('dd', null, s.hostname),
            h('dt', null, 'OS'), h('dd', null, s.os),
            h('dt', null, 'Kernel'), h('dd', null, s.kernel || '-'),
            h('dt', null, 'CPU'), h('dd', null, s.cpus + ' core · ' + s.arch),
            h('dt', null, 'Uptime'), h('dd', null, fmtUptime(s.uptime)),
            h('dt', null, 'IP'), h('dd', { class: 'mono' }, (s.server_ips || []).join(', ') || '-'))),
        h('div', { class: 'card' },
          h('h3', null, 'Load'),
          h('div', { class: 'stat-big' }, (s.load && s.load[0]) || '-'),
          h('div', { class: 'muted small' }, '1 / 5 / 15 menit: ' + (s.load || []).join(' / ')),
          h('div', { class: 'muted small' }, s.cpus + ' core CPU')),
        usageCard('Memori', memUsed, s.mem_total, s.swap_total ? 'swap ' + fmtBytes(s.swap_total - s.swap_free) + ' terpakai' : ''),
        usageCard('Disk /', disk.total - disk.free, disk.total, fmtBytes(disk.free) + ' bebas'),
        h('div', { class: 'card stack' },
          h('h3', null, 'Apache'),
          h('div', { class: 'row' }, h('span', { class: 'dot ' + (apacheOk ? 'ok' : 'danger') }), h('strong', null, s.apache), h('span', { class: 'muted small' }, s.apache_ver || '')),
          h('div', { class: 'row' },
            btn('Configtest', { icon: 'check', class: 'sm', onclick: () => opModal('apache2ctl configtest', () => api('POST', '/api/apache/configtest')) }),
            btn('Reload', { icon: 'refresh', class: 'sm', onclick: () => opModal('Reload Apache', () => api('POST', '/api/apache/reload'), { onDone: load }) }),
            btn('Restart', {
              icon: 'power', class: 'sm danger', onclick: async () => {
                if (await confirmDialog({ title: 'Restart Apache?', message: 'Semua situs akan terputus sebentar.', confirmText: 'Restart', danger: true }))
                  opModal('Restart Apache', () => api('POST', '/api/apache/restart'), { onDone: load });
              },
            }))),
        h('div', { class: 'card stack' },
          h('h3', null, "Certbot (Let's Encrypt)"),
          s.certbot
            ? h('div', { class: 'row' }, h('span', { class: 'dot ok' }), h('strong', null, s.certbot))
            : h('div', { class: 'stack' },
              h('div', { class: 'row' }, h('span', { class: 'dot danger' }), 'Belum terinstall'),
              btn('Install certbot', {
                icon: 'download', class: 'accent sm', onclick: async () => {
                  if (await confirmDialog({ title: 'Install certbot?', message: 'Menjalankan: apt-get install -y certbot python3-certbot-apache (1-3 menit).', confirmText: 'Install' }))
                    opModal('Install certbot', () => api('POST', '/api/certbot/install'), { onDone: load });
                },
              })),
          h('div', { class: 'muted small' }, 'Sertifikat diperpanjang otomatis oleh timer systemd certbot.')),
        panelCard(s, u),
      );
    }
    load();
    state.timer = setInterval(() => { if (!modalStack.length) load(); }, 15000);
  }

  // ---------- Update panel ----------
  function panelCard(s, u) {
    const card = h('div', { class: 'card stack' },
      h('h3', null, 'SinarPanel'),
      h('div', { class: 'row' }, h('strong', null, 'v' + s.panel_ver), u && u.installed ? h('code', { title: u.installed }, u.installed.slice(0, 7)) : null));
    if (!u) return card;
    if (u.running) {
      put(card, h('div', { class: 'row' }, spinner(), 'Update sedang berjalan…'),
        btn('Lihat progres', { icon: 'refresh', class: 'sm', onclick: () => updateProgress() }));
      return card;
    }
    if (u.log_time) {
      const ok = /diupdate ke|sudah versi terbaru/.test(u.log || '');
      const result = u.state === 'failed' ? h('span', { class: 'badge danger' }, 'gagal') : ok ? h('span', { class: 'badge ok' }, 'berhasil') : null;
      put(card, h('div', { class: 'row muted small' }, 'Update terakhir ' + fmtDay(u.log_time), result,
        h('a', { href: '#', onclick: (e) => { e.preventDefault(); showUpdateLog(u.log); } }, 'lihat log')));
    }
    if (!u.updater) {
      put(card, h('div', { class: 'muted small' }, 'Updater belum terpasang. Jalankan sekali dari SSH: ', h('code', null, 'sudo bash install.sh'), ' (versi terbaru).'));
    } else {
      put(card, h('div', null, btn('Cek update', { icon: 'refresh', class: 'sm', onclick: () => updateCheckModal() })));
    }
    return card;
  }

  function showUpdateLog(log) {
    const m = modal({ title: 'Log update terakhir', size: 'wide', body: h('pre', { class: 'log' }, log || '(kosong)'), footer: [btn('Tutup', { class: 'primary', onclick: () => m.close() })] });
  }

  function updateCheckModal() {
    const m = modal({ title: 'Update SinarPanel', size: 'wide', body: h('div', { class: 'row' }, spinner(), 'Mengecek versi terbaru di GitHub…') });
    m.locked = true;
    const close = btn('Tutup', { onclick: () => m.close() });
    api('POST', '/api/update/check').then((res) => {
      m.locked = false;
      const short = res.latest.slice(0, 7);
      if (!res.available) {
        m.setBody(h('div', { class: 'alert info' }, '✓ Sudah versi terbaru (' + short + ', branch ' + res.branch + ').'));
        m.setFooter(close);
        return;
      }
      const list = h('ul', { class: 'commits' }, res.commits.map((c) => h('li', null,
        h('code', null, c.hash.slice(0, 7)), ' ', c.subject,
        h('span', { class: 'muted small' }, ' — ' + c.author + ', ' + fmtDay(c.date)))));
      m.setBody(h('div', { class: 'stack' },
        h('div', null, 'Versi baru tersedia (branch ', h('code', null, res.branch), '): ',
          h('code', null, res.installed ? res.installed.slice(0, 7) : '?'), ' → ', h('code', null, short)),
        res.installed ? null : h('div', { class: 'muted small' }, 'Commit yang terpasang tidak diketahui (install manual), jadi hanya commit terbaru yang ditampilkan.'),
        list,
        h('div', { class: 'alert warn' }, 'Panel akan di-build ulang lalu restart (±1–3 menit), dan kamu perlu login ulang setelahnya. Apache & situs tidak terganggu.')));
      const go = btn('Update ke ' + short, { icon: 'download', class: 'accent' });
      go.addEventListener('click', async () => {
        go.disabled = true;
        try { await api('POST', '/api/update/run'); } catch (e) { go.disabled = false; showErr(e); return; }
        m.close();
        updateProgress();
      });
      m.setFooter(btn('Batal', { onclick: () => m.close() }), go);
    }).catch((e) => {
      m.locked = false;
      if (e.silent) { m.close(); return; }
      m.setBody(h('div', { class: 'stack' }, h('div', { class: 'alert err' }, e.message), e.data && e.data.log ? h('pre', { class: 'log' }, e.data.log) : null));
      m.setFooter(close);
    });
  }

  // Pantau unit sinarpanel-update. Saat install.sh me-restart panel, sesi
  // (di memori) hilang → 401 berarti panel baru sudah jalan.
  function updateProgress() {
    const log = h('pre', { class: 'log' });
    const status = h('div', { class: 'row' }, spinner(), 'Update berjalan… jangan tutup halaman ini.');
    const m = modal({ title: 'Update SinarPanel', size: 'wide', body: h('div', { class: 'stack' }, status, log) });
    m.locked = true;
    const finish = (kind, msg, action) => {
      m.locked = false;
      fill(status, h('div', { class: 'alert ' + kind }, msg));
      m.setFooter(action || btn('Tutup', { class: 'primary', onclick: () => m.close() }));
    };
    const poll = async () => {
      let res;
      try { res = await fetch('/api/update', { credentials: 'same-origin' }); } catch (e) { res = null; }
      if (res && res.status === 401) {
        finish('info', '✓ Update selesai dan panel sudah direstart. Silakan login ulang.', btn('Login ulang', { class: 'primary', onclick: () => location.reload() }));
        return;
      }
      if (!res || !res.ok) {
        fill(status, spinner(), 'Panel sedang restart…');
        setTimeout(poll, 2000);
        return;
      }
      const u = await res.json().catch(() => ({}));
      log.textContent = u.log || '';
      log.scrollTop = log.scrollHeight;
      if (u.running) { setTimeout(poll, 1500); return; }
      if (u.state === 'failed') finish('err', 'Update gagal — lihat log di atas. Panel versi lama tetap berjalan.');
      else finish('info', '✓ Update selesai.');
    };
    poll();
  }

  // ---------- Situs ----------
  function sslBadge(site) {
    const c = site.cert;
    if (!c) return h('span', { class: 'badge' }, icon('unlock'), 'Tanpa SSL');
    if (c.days_left < 0) return h('span', { class: 'badge danger' }, icon('lock'), 'SSL kedaluwarsa');
    if (c.days_left < 15) return h('span', { class: 'badge warn' }, icon('lock'), 'SSL ' + c.days_left + ' hari lagi');
    return h('span', { class: 'badge ok', title: c.issuer }, icon('lock'), 'SSL s/d ' + fmtDay(c.not_after));
  }

  function viewSites() {
    const list = h('div', { class: 'site-list' }, h('div', { class: 'muted' }, 'Memuat…'));
    put($main,
      pageHead('Situs & SSL',
        btn('', { icon: 'refresh', class: 'icon-only', title: 'Refresh', onclick: () => load() }),
        btn('Tambah Subdomain', { icon: 'plus', class: 'accent', onclick: () => createSiteModal(load) })),
      list);

    async function load() {
      let res;
      try { res = await api('GET', '/api/sites'); } catch (e) { fill(list, h('div', { class: 'alert err' }, e.message)); return; }
      clear(list);
      if (!res.sites.length) {
        put(list, h('div', { class: 'empty' }, 'Belum ada situs. Klik "Tambah Subdomain".'));
        return;
      }
      for (const s of res.sites) put(list, siteRow(s, load));
    }
    load();
  }

  function siteRow(s, reload) {
    const domain = s.server_name || s.name;
    const scheme = s.cert && s.ssl_enabled ? 'https://' : 'http://';
    const meta = [];
    if (s.proxy_target) meta.push(h('span', null, icon('link'), 'Proxy → ', h('code', null, s.proxy_target)));
    else if (s.document_root) meta.push(h('span', null, icon('folder'), h('code', null, s.document_root)));
    if (s.aliases.length) meta.push(h('span', null, 'Alias: ' + s.aliases.join(', ')));
    meta.push(h('span', null, icon('file'), s.name + '.conf' + (s.ssl_conf ? ' + ' + s.ssl_conf + '.conf' : '')));

    const actions = [];
    if (s.server_name && s.enabled) actions.push(h('a', { class: 'btn sm', href: scheme + s.server_name + '/', target: '_blank', rel: 'noopener noreferrer' }, icon('external'), 'Buka'));
    if (s.document_root) actions.push(btn('File', { icon: 'folder', class: 'sm', onclick: () => { location.hash = '#/files?' + q({ path: s.document_root }); } }));
    if (s.enabled && s.server_name) actions.push(btn(s.cert ? 'Perbarui SSL' : 'Pasang SSL', { icon: 'lock', class: 'sm' + (s.cert ? '' : ' accent'), onclick: () => sslModal(s, reload) }));
    if (s.server_name) actions.push(btn('Alias', { icon: 'link', class: 'sm', onclick: () => aliasModal(s, reload) }));
    actions.push(btn('Config', { icon: 'edit', class: 'sm', onclick: () => configEditor(s.name, reload) }));
    if (s.ssl_conf) actions.push(btn('Config SSL', { icon: 'edit', class: 'sm', onclick: () => configEditor(s.ssl_conf, reload) }));
    actions.push(btn(s.enabled ? 'Nonaktifkan' : 'Aktifkan', {
      icon: 'power', class: 'sm',
      onclick: async () => {
        if (s.enabled && !(await confirmDialog({ title: 'Nonaktifkan ' + domain + '?', message: 'Situs tidak akan bisa diakses sampai diaktifkan lagi.', confirmText: 'Nonaktifkan', danger: true }))) return;
        opModal((s.enabled ? 'Nonaktifkan ' : 'Aktifkan ') + domain, () => api('POST', '/api/sites/' + encodeURIComponent(s.name) + (s.enabled ? '/disable' : '/enable')), { onDone: reload });
      },
    }));
    if (s.managed) {
      actions.push(btn('', {
        icon: 'trash', class: 'sm danger icon-only', title: 'Hapus situs',
        onclick: async () => {
          const r = await confirmDialog({
            title: 'Hapus situs ' + domain + '?',
            message: 'Config Apache dihapus. File di document root TIDAK dihapus.',
            confirmText: 'Hapus', danger: true, requireText: domain,
            checkbox: s.cert ? 'Hapus juga sertifikat SSL (certbot delete)' : null,
          });
          if (r) opModal('Hapus ' + domain, () => api('DELETE', '/api/sites/' + encodeURIComponent(s.name) + (r.checked ? '?delete_cert=1' : '')), { onDone: reload });
        },
      }));
    }

    return h('div', { class: 'site' + (s.enabled ? '' : ' off') },
      h('div', { class: 'grow' },
        h('div', { class: 'name' }, domain,
          h('span', { class: 'badge ' + (s.enabled ? 'ok' : '') }, s.enabled ? 'Aktif' : 'Nonaktif'),
          sslBadge(s),
          s.proxy_target ? h('span', { class: 'badge info' }, 'Reverse proxy') : null,
          s.managed ? null : h('span', { class: 'badge', title: 'Config dibuat di luar SinarPanel' }, 'Manual')),
        h('div', { class: 'meta' }, meta)),
      h('div', { class: 'actions' }, actions));
  }

  function createSiteModal(reload) {
    const me = state.me;
    let type = 'static', docTouched = false;
    const domain = h('input', { type: 'text', placeholder: 'blog.domainku.com', spellcheck: false, autocomplete: 'off' });
    const aliases = h('input', { type: 'text', placeholder: 'www.blog.domainku.com (opsional, pisah koma)', spellcheck: false });
    const docroot = h('input', { type: 'text', value: me.webroot_base + '/', spellcheck: false });
    const proxy = h('input', { type: 'text', value: 'http://127.0.0.1:3000', spellcheck: false });
    const ws = h('input', { type: 'checkbox' });
    const ssl = h('input', { type: 'checkbox', checked: true });
    const email = h('input', { type: 'email', value: me.certbot_email || '', placeholder: 'email@domain.com (untuk notifikasi Let\'s Encrypt)' });
    const err = h('div', { class: 'alert err hidden' });

    domain.addEventListener('input', () => { if (!docTouched) docroot.value = me.webroot_base + '/' + domain.value.trim().toLowerCase(); });
    docroot.addEventListener('input', () => { docTouched = true; });

    const staticBox = h('label', { class: 'field' }, h('span', null, 'Document root'), docroot,
      h('small', null, 'Folder dibuat otomatis & dimiliki user ' + me.user + '. Harus di dalam ' + me.webroot_base + '/'));
    const proxyBox = h('div', { class: 'stack hidden' },
      h('label', { class: 'field' }, h('span', null, 'Target aplikasi'), proxy, h('small', null, 'Contoh: aplikasi Node/Go/Python yang listen di port lokal')),
      h('label', { class: 'check' }, ws, h('span', null, 'Dukung WebSocket (Apache ≥ 2.4.47)')));
    const emailBox = h('label', { class: 'field' }, h('span', null, 'Email Let\'s Encrypt'), email);
    ssl.addEventListener('change', () => emailBox.classList.toggle('hidden', !ssl.checked));

    const segStatic = btn('Static / PHP-FPM / HTML', { class: 'on' });
    const segProxy = btn('Reverse proxy');
    const setType = (t) => {
      type = t;
      segStatic.classList.toggle('on', t === 'static');
      segProxy.classList.toggle('on', t === 'proxy');
      staticBox.classList.toggle('hidden', t !== 'static');
      proxyBox.classList.toggle('hidden', t !== 'proxy');
    };
    segStatic.addEventListener('click', () => setType('static'));
    segProxy.addEventListener('click', () => setType('proxy'));

    const create = btn('Buat Situs', { icon: 'plus', class: 'accent' });
    const m = modal({
      title: 'Tambah Subdomain',
      body: h('div', { class: 'stack' },
        err,
        h('label', { class: 'field' }, h('span', null, 'Domain / subdomain'), domain,
          h('small', null, 'Pastikan DNS A record sudah mengarah ke IP server ini.')),
        h('label', { class: 'field' }, h('span', null, 'Alias'), aliases),
        h('div', null, h('div', { class: 'seg' }, segStatic, segProxy)),
        staticBox, proxyBox,
        h('label', { class: 'check' }, ssl, h('span', null, h('strong', null, 'Langsung pasang SSL'), h('br'), h('span', { class: 'muted small' }, 'certbot --apache + redirect HTTP → HTTPS'))),
        emailBox),
      footer: [btn('Batal', { onclick: () => m.tryClose() }), create],
    });

    create.addEventListener('click', () => {
      err.classList.add('hidden');
      const d = domain.value.trim().toLowerCase();
      if (!d) { err.textContent = 'Domain wajib diisi'; err.classList.remove('hidden'); return; }
      const body = {
        domain: d,
        aliases: aliases.value.split(/[\s,]+/).map((x) => x.trim()).filter(Boolean),
        type,
        document_root: type === 'static' ? docroot.value.trim() : '',
        proxy_url: type === 'proxy' ? proxy.value.trim() : '',
        websocket: ws.checked,
        ssl: ssl.checked,
        email: email.value.trim(),
      };
      m.close();
      opModal('Membuat ' + d, () => api('POST', '/api/sites', body), { onDone: () => reload() });
    });
  }

  function sslModal(s, reload) {
    const me = state.me;
    const dnsBox = h('div', { class: 'stack' }, h('div', { class: 'row muted' }, spinner(), 'Cek DNS…'));
    const email = h('input', { type: 'email', value: me.certbot_email || '', placeholder: 'email@domain.com' });
    const redirect = h('input', { type: 'checkbox', checked: true });
    const go = btn(s.cert ? 'Perbarui / Pasang ulang' : 'Pasang SSL', { icon: 'lock', class: 'accent' });
    const m = modal({
      title: "SSL Let's Encrypt — " + s.server_name,
      body: h('div', { class: 'stack' },
        s.cert ? h('div', { class: 'alert info' }, 'Sertifikat saat ini berlaku s/d ' + fmtDay(s.cert.not_after) + ' (' + s.cert.days_left + ' hari), issuer ' + s.cert.issuer + '.') : null,
        h('div', null, h('strong', null, 'Cek DNS'), dnsBox),
        h('label', { class: 'field' }, h('span', null, 'Email'), email, h('small', null, 'Opsional, untuk peringatan kedaluwarsa dari Let\'s Encrypt')),
        h('label', { class: 'check' }, redirect, h('span', null, 'Redirect otomatis HTTP → HTTPS'))),
      footer: [btn('Batal', { onclick: () => m.tryClose() }), go],
    });
    go.addEventListener('click', () => {
      m.close();
      opModal('certbot — ' + s.server_name, () => api('POST', '/api/sites/' + encodeURIComponent(s.name) + '/ssl', { email: email.value.trim(), redirect: redirect.checked }), { onDone: reload });
    });
    api('GET', '/api/sites/' + encodeURIComponent(s.name) + '/dns').then((res) => {
      const ips = res.server_ips || [];
      clear(dnsBox);
      for (const r of res.results || []) {
        const match = r.ips.some((ip) => ips.includes(ip));
        put(dnsBox, h('div', { class: 'row' },
          h('code', null, r.domain), '→',
          r.error ? h('span', { class: 'badge danger' }, 'tidak resolve')
            : h('span', null, h('code', null, r.ips.join(', ')), ' ', h('span', { class: 'badge ' + (match ? 'ok' : 'warn') }, match ? 'cocok' : 'beda / NAT?'))));
      }
      put(dnsBox, h('div', { class: 'muted small' }, 'IP di interface server: ' + (ips.join(', ') || '-') + '. Jika server di belakang NAT/Cloudflare proxy, "beda" bisa normal.'));
    }).catch((e) => { fill(dnsBox, h('div', { class: 'alert err' }, e.message)); });
  }

  function aliasModal(s, reload) {
    const me = state.me;
    const aliases = h('textarea', { rows: 3, spellcheck: false, placeholder: 'www.' + s.server_name }, s.aliases.join('\n'));
    const canSSL = s.enabled;
    const ssl = h('input', { type: 'checkbox', checked: canSSL && !!s.cert, disabled: !canSSL });
    const email = h('input', { type: 'email', value: me.certbot_email || '', placeholder: 'email@domain.com' });
    const redirect = h('input', { type: 'checkbox', checked: true });
    const sslBox = h('div', { class: 'stack' + (ssl.checked ? '' : ' hidden') },
      h('label', { class: 'field' }, h('span', null, 'Email'), email, h('small', null, 'Opsional, untuk peringatan kedaluwarsa dari Let\'s Encrypt')),
      h('label', { class: 'check' }, redirect, h('span', null, 'Redirect otomatis HTTP → HTTPS')));
    ssl.addEventListener('change', () => sslBox.classList.toggle('hidden', !ssl.checked));
    const err = h('div', { class: 'alert err hidden' });
    const save = btn('Simpan', { icon: 'check', class: 'accent' });
    const m = modal({
      title: 'Alias — ' + s.server_name,
      body: h('div', { class: 'stack' },
        err,
        h('label', { class: 'field' }, h('span', null, 'Alias (ServerAlias)'), aliases,
          h('small', null, 'Satu per baris atau pisah koma. Kosongkan untuk menghapus semua alias. Pastikan DNS alias sudah mengarah ke server ini.')),
        h('label', { class: 'check' }, ssl, h('span', null,
          h('strong', null, s.cert ? 'Perbarui sertifikat SSL' : 'Pasang SSL'), h('br'),
          h('span', { class: 'muted small' }, canSSL ? 'certbot dijalankan ulang untuk domain utama + semua alias' : 'Aktifkan situs dulu untuk memasang SSL'))),
        sslBox),
      footer: [btn('Batal', { onclick: () => m.tryClose() }), save],
    });
    save.addEventListener('click', () => {
      const list = aliases.value.split(/[\s,]+/).map((x) => x.trim().toLowerCase()).filter(Boolean);
      const body = { aliases: list, ssl: ssl.checked, email: email.value.trim(), redirect: redirect.checked };
      m.close();
      opModal('Alias — ' + s.server_name, () => api('PUT', '/api/sites/' + encodeURIComponent(s.name) + '/aliases', body), { onDone: reload });
    });
  }

  async function configEditor(name, reload) {
    let res;
    try { res = await api('GET', '/api/sites/' + encodeURIComponent(name) + '/config'); } catch (e) { showErr(e); return; }
    textEditor({
      title: res.path,
      content: res.content,
      info: res.enabled ? 'Disimpan → configtest → reload. Jika error, otomatis dikembalikan.' : 'Situs nonaktif: disimpan tanpa reload.',
      save: (content) => api('PUT', '/api/sites/' + encodeURIComponent(name) + '/config', { content }),
      onSaved: reload,
    });
  }

  // ---------- Editor teks ----------
  function textEditor({ title, content, info, save, onSaved }) {
    let original = content;
    const ta = h('textarea', { class: 'code', spellcheck: false });
    ta.value = content;
    const status = h('span', { class: 'muted small grow' }, info || '');
    const errBox = h('div', { class: 'hidden' });
    const saveBtn = btn('Simpan', { icon: 'check', class: 'primary', title: 'Ctrl/Cmd + S' });
    const dirty = () => ta.value !== original;
    const m = modal({
      title, size: 'full',
      body: [errBox, ta],
      footer: [status, btn('Tutup', { onclick: () => m.tryClose() }), saveBtn],
      canClose: () => !dirty() || window.confirm('Ada perubahan yang belum disimpan. Tutup tanpa menyimpan?'),
    });
    ta.addEventListener('keydown', (e) => {
      if (e.key === 'Tab' && !e.shiftKey) {
        e.preventDefault();
        ta.setRangeText('\t', ta.selectionStart, ta.selectionEnd, 'end');
      }
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') { e.preventDefault(); doSave(); }
    });
    async function doSave() {
      if (saveBtn.disabled) return;
      saveBtn.disabled = true;
      errBox.className = 'hidden';
      const val = ta.value;
      try {
        await save(val);
        original = val;
        toast('Tersimpan', 'ok');
        status.textContent = 'Tersimpan ' + new Date().toLocaleTimeString('id-ID');
        if (onSaved) onSaved();
      } catch (e) {
        if (e.silent) return;
        fill(errBox, h('div', { class: 'alert err' }, e.message), e.data && e.data.log ? h('pre', { class: 'log' }, e.data.log) : null);
        errBox.className = 'stack';
        errBox.style.marginBottom = '10px';
      } finally {
        saveBtn.disabled = false;
      }
    }
    saveBtn.addEventListener('click', doSave);
    setTimeout(() => { ta.focus(); ta.setSelectionRange(0, 0); ta.scrollTop = 0; }, 50);
  }

  // ---------- File manager ----------
  let openMenu = null;
  function closeMenu() { if (openMenu) { openMenu.remove(); openMenu = null; } }
  document.addEventListener('click', closeMenu);
  window.addEventListener('scroll', closeMenu, true);
  window.addEventListener('hashchange', closeMenu);
  const joinPath = (dir, name) => (dir === '/' ? '/' : dir + '/') + name;
  const parentPath = (p) => { const i = p.replace(/\/+$/, '').lastIndexOf('/'); return i <= 0 ? '/' : p.slice(0, i); };
  const isArchive = (n) => /\.(zip|tar|tar\.gz|tgz)$/i.test(n);

  function viewFiles(params) {
    const fm = state.fm;
    const start = params.get('path') || fm.path || state.me.home || '/';
    const crumbs = h('div', { class: 'crumbs' });
    const tbody = h('tbody');
    const checkAll = h('input', { type: 'checkbox', title: 'Pilih semua' });
    const statusBar = h('div', { class: 'fm-status' });
    const fileInput = h('input', { type: 'file', multiple: true, class: 'hidden' });
    const delSel = btn('Hapus', { icon: 'trash', class: 'danger', disabled: true });
    const dlSel = btn('Download', { icon: 'download', disabled: true });
    const tableWrap = h('div', { class: 'table-wrap' },
      h('table', { class: 'files' },
        h('thead', null, h('tr', null,
          h('th', null, checkAll), h('th', null, 'Nama'), h('th', { class: 'hide-m' }, 'Ukuran'),
          h('th', { class: 'hide-m' }, 'Izin'), h('th', { class: 'hide-m' }, 'Pemilik'), h('th', { class: 'hide-m' }, 'Diubah'), h('th', null, ''))),
        tbody));

    put($main,
      pageHead('File Manager'),
      h('div', { class: 'fm-bar' },
        btn('', { icon: 'up', class: 'icon-only', title: 'Folder atas', onclick: () => go(parentPath(fm.path)) }),
        btn('', { icon: 'refresh', class: 'icon-only', title: 'Refresh', onclick: () => go(fm.path) }),
        crumbs,
        btn('Folder', { icon: 'plus', onclick: newFolder }),
        btn('File', { icon: 'plus', onclick: newFile }),
        btn('Upload', { icon: 'upload', class: 'primary', onclick: () => fileInput.click() }),
        dlSel, delSel, fileInput),
      tableWrap, statusBar);

    function updateSel() {
      const n = fm.sel.size;
      delSel.disabled = dlSel.disabled = n === 0;
      checkAll.checked = n > 0 && n === fm.entries.length;
      checkAll.indeterminate = n > 0 && n < fm.entries.length;
      for (const tr of tbody.children) tr.classList.toggle('sel', fm.sel.has(tr.dataset.name));
    }
    checkAll.addEventListener('change', () => {
      fm.sel = new Set(checkAll.checked ? fm.entries.map((e) => e.name) : []);
      for (const cb of tbody.querySelectorAll('input[type=checkbox]')) cb.checked = checkAll.checked;
      updateSel();
    });

    function renderCrumbs() {
      clear(crumbs);
      const parts = fm.path.split('/').filter(Boolean);
      put(crumbs, h('a', { href: '#', onclick: (e) => { e.preventDefault(); go('/'); } }, '/'));
      let acc = '';
      parts.forEach((p, i) => {
        acc += '/' + p;
        const target = acc;
        if (i > 0) put(crumbs, h('span', { class: 'sep' }, '/'));
        put(crumbs, h('a', { href: '#', onclick: (e) => { e.preventDefault(); go(target); } }, p));
      });
      crumbs.title = 'Klik area kosong untuk mengetik path';
    }
    crumbs.addEventListener('click', (e) => {
      if (e.target !== crumbs) return;
      const input = h('input', { type: 'text', value: fm.path, spellcheck: false });
      fill(crumbs, input);
      input.focus();
      input.select();
      input.addEventListener('keydown', (ev) => {
        if (ev.key === 'Enter') go(input.value.trim() || '/');
        if (ev.key === 'Escape') { ev.stopPropagation(); renderCrumbs(); }
      });
      input.addEventListener('blur', () => setTimeout(renderCrumbs, 150));
    });

    async function go(path) {
      let res;
      try { res = await api('GET', '/api/fs/list?' + q({ path })); } catch (e) {
        showErr(e);
        if (!fm.entries.length && path !== '/') { if (path !== state.me.home && state.me.home) go(state.me.home); else go('/'); }
        return;
      }
      fm.path = res.path;
      fm.entries = res.entries;
      fm.sel = new Set();
      const want = '#/files?' + q({ path: fm.path });
      if (location.hash !== want) history.replaceState(null, '', want);
      renderCrumbs();
      renderRows();
      fill(statusBar,
        h('span', null, res.entries.length + ' item'),
        res.truncated ? h('span', { class: 'badge warn' }, 'daftar dipotong (terlalu banyak file)') : null,
        res.writable ? null : h('span', { class: 'badge warn' }, 'read-only untuk user ' + state.me.user),
        h('span', null, 'Login sebagai ', h('strong', null, state.me.user), ' — akses file mengikuti permission user ini'));
      updateSel();
    }

    function renderRows() {
      clear(tbody);
      if (!fm.entries.length) {
        put(tbody, h('tr', null, h('td', { colspan: '7', class: 'muted' }, 'Folder kosong — seret file ke sini untuk upload.')));
        return;
      }
      for (const e of fm.entries) {
        const full = joinPath(fm.path, e.name);
        const isDir = e.type === 'dir' || (e.type === 'link' && e.to_dir);
        const cb = h('input', { type: 'checkbox' });
        cb.addEventListener('change', () => { cb.checked ? fm.sel.add(e.name) : fm.sel.delete(e.name); updateSel(); });
        const ic = icon(isDir ? 'folder' : e.type === 'link' ? 'link' : 'file', isDir ? 'dir' : e.type === 'link' ? 'link' : 'file');
        const nameEl = h('a', {
          href: '#', title: e.name,
          onclick: (ev) => { ev.preventDefault(); isDir ? go(full) : openFile(full, e); },
        }, ic, h('span', { class: 'lbl' }, e.name));
        const more = btn('', { icon: 'more', class: 'ghost icon-only sm', title: 'Aksi' });
        more.addEventListener('click', (ev) => { ev.stopPropagation(); rowMenu(more, e, full, isDir); });
        const tr = h('tr', { 'data-name': e.name },
          h('td', null, cb),
          h('td', { class: 'name' }, nameEl, e.type === 'link' ? h('span', { class: 'link-target' }, '→ ' + e.target) : null),
          h('td', { class: 'hide-m muted' }, isDir ? '—' : fmtBytes(e.size)),
          h('td', { class: 'hide-m mono', title: e.mode }, e.perm),
          h('td', { class: 'hide-m muted' }, e.owner + ':' + e.group),
          h('td', { class: 'hide-m muted' }, fmtDate(e.mtime)),
          h('td', { class: 'act' }, more));
        tr.addEventListener('contextmenu', (ev) => { ev.preventDefault(); rowMenu(null, e, full, isDir, ev); });
        put(tbody, tr);
      }
    }

    function rowMenu(anchor, e, full, isDir, ev) {
      closeMenu();
      const item = (label, ic, fn, cls) => h('button', { type: 'button', class: cls || '', onclick: () => { closeMenu(); fn(); } }, icon(ic), label);
      const menu = h('div', { class: 'menu' },
        isDir ? item('Buka', 'folder', () => go(full)) : item('Edit', 'edit', () => openFile(full, e)),
        item(isDir ? 'Download (zip)' : 'Download', 'download', () => download([full], isDir)),
        h('hr'),
        item('Rename / pindah', 'edit', () => renameItem(full, e.name)),
        item('Salin', 'copy', () => copyItem(full, e.name)),
        e.type !== 'link' ? item('Ubah izin (chmod)', 'key', () => chmodItem(full, e)) : null,
        !isDir && isArchive(e.name) ? item('Extract di sini', 'archive', () => extractItem(full)) : null,
        h('hr'),
        item('Hapus', 'trash', () => deletePaths([full]), 'danger'));
      document.body.append(menu);
      openMenu = menu;
      const r = anchor ? anchor.getBoundingClientRect() : { left: ev.clientX, right: ev.clientX, bottom: ev.clientY, top: ev.clientY };
      const mw = menu.offsetWidth, mh = menu.offsetHeight;
      let x = Math.min(r.right - mw, window.innerWidth - mw - 8);
      if (!anchor) x = Math.min(r.left, window.innerWidth - mw - 8);
      let y = r.bottom + 4;
      if (y + mh > window.innerHeight - 8) y = Math.max(8, r.top - mh - 4);
      menu.style.left = Math.max(8, x) + 'px';
      menu.style.top = y + 'px';
    }

    async function openFile(full, e) {
      if (e.size > state.me.max_edit_mb * 1048576 && state.me.max_edit_mb) { download([full], false); return; }
      let res;
      try { res = await api('GET', '/api/fs/read?' + q({ path: full })); } catch (ex) {
        if (ex.silent) return;
        if (/biner|terlalu besar/i.test(ex.message)) {
          if (await confirmDialog({ title: e.name, message: ex.message, confirmText: 'Download' })) download([full], false);
        } else showErr(ex);
        return;
      }
      textEditor({
        title: full,
        content: res.content,
        info: fmtBytes(res.size) + ' · ' + res.perm,
        save: (content) => api('POST', '/api/fs/write', { path: full, content }),
        onSaved: () => go(fm.path),
      });
    }

    function download(paths, zip) {
      const p = new URLSearchParams();
      for (const x of paths) p.append('path', x);
      if (zip) p.set('zip', '1');
      const a = h('a', { href: '/api/fs/download?' + p.toString(), download: '' });
      document.body.append(a);
      a.click();
      a.remove();
    }

    async function simple(op, body, okMsg) {
      try {
        const res = await api('POST', '/api/fs/' + op, body);
        if (okMsg) toast(typeof okMsg === 'function' ? okMsg(res) : okMsg, 'ok');
        await go(fm.path);
        return res;
      } catch (e) { showErr(e); return null; }
    }

    async function newFolder() {
      const n = await promptDialog({ title: 'Folder baru', label: 'Nama folder', okText: 'Buat' });
      if (n) simple('mkdir', { path: joinPath(fm.path, n) }, 'Folder dibuat');
    }
    async function newFile() {
      const n = await promptDialog({ title: 'File baru', label: 'Nama file', value: '', okText: 'Buat' });
      if (n) simple('touch', { path: joinPath(fm.path, n) }, 'File dibuat');
    }
    async function renameItem(full, name) {
      const v = await promptDialog({ title: 'Rename / pindah', label: 'Nama baru atau path tujuan', value: name, selectUntilDot: true, hint: 'Isi nama saja untuk rename, atau path absolut (/var/www/…) untuk memindah.' });
      if (!v || v === name) return;
      simple('rename', { path: full, dest: v.startsWith('/') ? v : joinPath(fm.path, v) }, 'Berhasil dipindah');
    }
    async function copyItem(full, name) {
      const dot = name.lastIndexOf('.');
      const sug = dot > 0 ? name.slice(0, dot) + '-copy' + name.slice(dot) : name + '-copy';
      const v = await promptDialog({ title: 'Salin ' + name, label: 'Nama salinan atau path tujuan', value: sug, okText: 'Salin' });
      if (v) simple('copy', { path: full, dest: v.startsWith('/') ? v : joinPath(fm.path, v) }, 'Tersalin');
    }
    async function chmodItem(full, e) {
      const v = await promptDialog({ title: 'chmod ' + e.name, label: 'Mode oktal', value: e.perm, hint: 'Contoh: 0644 file, 0755 folder/script. Saat ini: ' + e.mode });
      if (v) simple('chmod', { path: full, mode: v }, 'Izin diubah');
    }
    async function extractItem(full) {
      const r = await confirmDialog({ title: 'Extract arsip', message: 'Extract ke ' + fm.path + '/ ?', confirmText: 'Extract', checkbox: 'Timpa file yang sudah ada' });
      if (!r) return;
      simple('extract', { path: full, dest: fm.path, overwrite: r.checked },
        (res) => `Extract: ${res.files} file, ${res.dirs} folder` + (res.skipped ? `, ${res.skipped} dilewati (sudah ada)` : '') + (res.unsafe ? `, ${res.unsafe} entri tidak aman diabaikan` : ''));
    }
    async function deletePaths(paths) {
      const names = paths.map((p) => p.split('/').pop());
      const r = await confirmDialog({
        title: 'Hapus ' + (paths.length === 1 ? names[0] : paths.length + ' item') + '?',
        message: h('div', null, 'Dihapus permanen (termasuk isi folder). Tidak bisa di-undo.',
          paths.length > 1 ? h('div', { class: 'muted small mono' }, names.slice(0, 10).join(', ') + (names.length > 10 ? ', …' : '')) : null),
        confirmText: 'Hapus permanen', danger: true,
      });
      if (r) simple('delete', { paths }, (res) => res.deleted + ' item dihapus');
    }
    delSel.addEventListener('click', () => deletePaths([...fm.sel].map((n) => joinPath(fm.path, n))));
    dlSel.addEventListener('click', () => {
      const sel = [...fm.sel];
      const single = sel.length === 1 && fm.entries.find((e) => e.name === sel[0] && e.type === 'file');
      download(sel.map((n) => joinPath(fm.path, n)), !single);
    });

    // Upload dengan progress (XHR), tanya dulu jika file sudah ada.
    function uploadFiles(files, overwrite = false) {
      files = [...files];
      if (!files.length) return;
      const max = state.me.max_upload_mb * 1048576;
      const total = files.reduce((s, f) => s + f.size, 0);
      if (total > max) { toast('Total upload melebihi batas ' + state.me.max_upload_mb + ' MB', 'err'); return; }
      const fd = new FormData();
      for (const f of files) fd.append('files', f, f.name);
      const fill = h('i');
      fill.style.width = '0%';
      const label = h('div', { class: 'small' }, 'Upload ' + files.length + ' file (' + fmtBytes(total) + ')…');
      const box = h('div', { class: 'progress' }, label, h('div', { class: 'bar' }, fill));
      document.body.append(box);
      const xhr = new XMLHttpRequest();
      xhr.open('POST', '/api/fs/upload?' + q({ dir: fm.path, overwrite: overwrite ? '1' : '0' }));
      xhr.setRequestHeader('X-CSRF-Token', state.csrf);
      xhr.upload.onprogress = (ev) => { if (ev.lengthComputable) fill.style.width = Math.round((ev.loaded / ev.total) * 100) + '%'; };
      xhr.onloadend = async () => {
        box.remove();
        let data = {};
        try { data = JSON.parse(xhr.responseText); } catch (e) { /* - */ }
        if (xhr.status === 401) { renderLogin('Sesi berakhir, silakan login lagi.'); return; }
        if (xhr.status !== 200) { toast(data.error || 'Upload gagal (HTTP ' + xhr.status + ')', 'err'); return; }
        const results = data.results || [];
        const exists = results.filter((r) => r.error && /^Sudah ada/.test(r.error)).map((r) => r.name);
        const failed = results.filter((r) => r.error && !/^Sudah ada/.test(r.error));
        const ok = results.filter((r) => !r.error).length;
        if (ok) toast(ok + ' file terupload', 'ok');
        for (const f of failed) toast(f.name + ': ' + f.error, 'err');
        await go(fm.path);
        if (exists.length) {
          const r = await confirmDialog({ title: 'File sudah ada', message: exists.join(', ') + ' sudah ada di folder ini. Timpa?', confirmText: 'Timpa', danger: true });
          if (r) uploadFiles(files.filter((f) => exists.includes(f.name)), true);
        }
      };
      xhr.send(fd);
    }
    fileInput.addEventListener('change', () => { uploadFiles(fileInput.files); fileInput.value = ''; });
    tableWrap.addEventListener('dragover', (e) => { if (e.dataTransfer.types.includes('Files')) { e.preventDefault(); tableWrap.classList.add('drop'); } });
    tableWrap.addEventListener('dragleave', (e) => { if (!tableWrap.contains(e.relatedTarget)) tableWrap.classList.remove('drop'); });
    tableWrap.addEventListener('drop', (e) => {
      e.preventDefault();
      tableWrap.classList.remove('drop');
      if (e.dataTransfer.files.length) uploadFiles(e.dataTransfer.files);
    });

    go(start);
  }

  // ---------- Keamanan ----------
  function viewSecurity() {
    const me = state.me;
    const box2fa = h('div', { class: 'card stack' });
    put($main, pageHead('Keamanan'),
      me.needs_2fa_setup ? h('div', { class: 'alert warn' }, 'Server ini mewajibkan 2FA. Aktifkan dulu untuk memakai panel.') : null,
      h('div', { class: 'grid' }, box2fa,
        h('div', { class: 'card' },
          h('h3', null, 'Sesi ini'),
          h('dl', { class: 'kv' },
            h('dt', null, 'User'), h('dd', null, me.user + ' (uid ' + me.uid + ')'),
            h('dt', null, 'Home'), h('dd', { class: 'mono' }, me.home),
            h('dt', null, 'Server'), h('dd', null, me.hostname),
            h('dt', null, 'Auto logout'), h('dd', null, 'setelah ' + me.idle_minutes + ' menit tidak aktif'),
            h('dt', null, 'Versi'), h('dd', null, 'SinarPanel ' + me.version))),
        h('div', { class: 'card' },
          h('h3', null, 'Tips hardening'),
          h('ul', { class: 'small' },
            h('li', null, 'Batasi port panel di firewall hanya untuk IP kamu, atau isi ', h('code', null, 'allowed_ips'), ' di /etc/sinarpanel/config.json.'),
            h('li', null, 'Aktifkan 2FA, lalu set ', h('code', null, '"require_2fa": true'), '.'),
            h('li', null, 'Pakai user non-root anggota grup sudo; biarkan ', h('code', null, 'allow_root'), ' = false.'),
            h('li', null, 'Log audit: ', h('code', null, 'journalctl -u sinarpanel'), '.')))));
    render2fa();

    function render2fa() {
      fill(box2fa, h('h3', null, 'Two-factor authentication (TOTP)'));
      if (state.me.has_2fa) {
        put(box2fa,
          h('div', { class: 'row' }, h('span', { class: 'badge ok' }, icon('check'), 'Aktif'), h('span', { class: 'muted small' }, 'Kode dari aplikasi authenticator wajib saat login.')),
          me.require_2fa ? h('div', { class: 'muted small' }, 'Diwajibkan oleh config server.') :
            btn('Nonaktifkan 2FA', {
              class: 'danger sm', onclick: async () => {
                const code = await promptDialog({ title: 'Nonaktifkan 2FA', label: 'Masukkan kode 2FA saat ini', okText: 'Nonaktifkan' });
                if (!code) return;
                try { await api('POST', '/api/2fa/disable', { code }); state.me.has_2fa = false; toast('2FA dinonaktifkan', 'ok'); render2fa(); } catch (e) { showErr(e); }
              },
            }));
        return;
      }
      put(box2fa,
        h('div', { class: 'muted' }, 'Lindungi panel dengan kode 6 digit dari Google Authenticator, Authy, 2FAS, Bitwarden, dll.'),
        btn('Aktifkan 2FA', { icon: 'shield', class: 'accent', onclick: startSetup }));
    }

    async function startSetup() {
      let res;
      try { res = await api('POST', '/api/2fa/setup'); } catch (e) { showErr(e); return; }
      const code = h('input', { type: 'text', inputmode: 'numeric', maxlength: '6', placeholder: '123456', autocomplete: 'one-time-code' });
      const err = h('div', { class: 'alert err hidden' });
      const uri = h('input', { type: 'text', value: res.uri, readOnly: true, spellcheck: false });
      uri.addEventListener('focus', () => uri.select());
      const verify = btn('Verifikasi & aktifkan', { class: 'accent' });
      const m = modal({
        title: 'Aktifkan 2FA',
        body: h('div', { class: 'stack' },
          h('div', null, '1. Di aplikasi authenticator pilih ', h('strong', null, '"Masukkan kunci penyiapan" / "Enter setup key"'), ', tipe ', h('strong', null, 'Time-based'), ', lalu ketik kunci ini:'),
          h('div', { class: 'secret' }, res.secret.match(/.{1,4}/g).join(' ')),
          h('label', { class: 'field' }, h('span', { class: 'muted small' }, 'Atau tempel URI ini di aplikasi yang mendukung (mis. Bitwarden, 1Password):'), uri),
          h('div', null, '2. Masukkan kode 6 digit yang muncul:'),
          err, code),
        footer: [btn('Batal', { onclick: () => m.tryClose() }), verify],
      });
      const submit = async () => {
        err.classList.add('hidden');
        try {
          await api('POST', '/api/2fa/enable', { code: code.value.trim() });
          m.close();
          state.me.has_2fa = true;
          state.me.needs_2fa_setup = false;
          toast('2FA aktif. Simpan kunci di tempat aman!', 'ok');
          render2fa();
        } catch (e) { err.textContent = e.message; err.classList.remove('hidden'); }
      };
      verify.addEventListener('click', submit);
      code.addEventListener('keydown', (e) => { if (e.key === 'Enter') submit(); });
      setTimeout(() => code.focus(), 60);
    }
  }

  const VIEWS = { dashboard: viewDashboard, sites: viewSites, files: viewFiles, security: viewSecurity };

  // ---------- Boot ----------
  async function boot() {
    try {
      const me = await fetch('/api/me', { credentials: 'same-origin' });
      if (!me.ok) { renderLogin(); return; }
      state.me = await me.json();
      state.csrf = state.me.csrf;
      renderApp();
    } catch (e) {
      renderLogin('Tidak bisa menghubungi server.');
    }
  }
  boot();
})();
