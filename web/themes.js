// PLC Monitor themes, shared by every window (main, Network, Topology, Export,
// Help). Loaded in <head> so the page is drawn in the right colours at once.
//
// The choice is kept in localStorage (instant, per browser) and, on the
// computer running PLC Monitor, in config.yaml (web.theme), which the server
// passes in as window.S7_SERVER_THEME. The desktop app's private web server
// gets a new port every run, which empties localStorage, so the config value
// is what makes the theme stick there.
(function () {
  'use strict';
  // Each theme: background, panel (card), text, secondary text, lines, accent,
  // and good/bad/warning colours. The rest is derived. Optional: font.
  const T = (id, name, group, dark, bg, card, fg, muted, line, accent, good, bad, warn, extra) =>
    Object.assign({ id, name, group, dark, bg, card, fg, muted, line, accent, good, bad, warn }, extra || {});
  const MONO = 'ui-monospace, "Cascadia Mono", Consolas, monospace';
  const THEMES = [
    { id: 'system', name: 'Follow Windows (light / dark)', group: 'Automatic' },

    T('light', 'Light', 'Light', false, '#f6f7f9', '#ffffff', '#1d2330', '#6b7280', '#e5e7eb', '#2563eb', '#15803d', '#b91c1c', '#b45309'),
    T('sky', 'Sky', 'Light', false, '#eef6fc', '#ffffff', '#0c2a40', '#5a7488', '#d6e6f2', '#0284c7', '#15803d', '#b91c1c', '#b45309'),
    T('mint', 'Mint', 'Light', false, '#eef7f3', '#ffffff', '#12312a', '#5b7a70', '#d3e8df', '#0d9488', '#15803d', '#be123c', '#b45309'),
    T('lavender', 'Lavender', 'Light', false, '#f3f1fb', '#ffffff', '#251f3d', '#6e6890', '#e2ddf3', '#7c3aed', '#15803d', '#be123c', '#b45309'),
    T('rose', 'Rose', 'Light', false, '#fbf1f4', '#ffffff', '#3a1d25', '#8a6570', '#f0dce1', '#db2777', '#15803d', '#b91c1c', '#b45309'),
    T('paper', 'Paper', 'Light', false, '#f4efe6', '#fbf8f2', '#2b2622', '#7a6f63', '#e3dacb', '#9a3412', '#3f6212', '#b91c1c', '#a16207',
      { font: 'Georgia, "Leelawadee UI", serif' }),
    T('solarized-light', 'Solarized Light', 'Light', false, '#eee8d5', '#fdf6e3', '#073642', '#657b83', '#d9d2bd', '#268bd2', '#5f7a00', '#dc322f', '#b58900'),
    T('hmi-gray', 'HMI Gray (high-performance HMI)', 'Light', false, '#d2d2d2', '#e4e4e4', '#1a1a1a', '#4d4d4d', '#b5b5b5', '#3b5b85', '#2f6f3e', '#c00000', '#b07000'),
    T('contrast-light', 'High Contrast Light', 'Light', false, '#ffffff', '#ffffff', '#000000', '#2b2b2b', '#000000', '#0000c8', '#006400', '#b00000', '#7a4f00'),

    T('dark', 'Dark', 'Dark', true, '#0f1218', '#171b23', '#e5e7eb', '#9ca3af', '#2a303b', '#60a5fa', '#4ade80', '#f87171', '#fbbf24'),
    T('midnight', 'Midnight', 'Dark', true, '#0b1020', '#121a33', '#e3e8ff', '#8f9bc2', '#24305a', '#7aa2ff', '#4ade80', '#ff6b81', '#fbbf24'),
    T('graphite', 'Graphite', 'Dark', true, '#1b1d21', '#24272d', '#e7e9ec', '#9aa1ab', '#363a42', '#f97316', '#4ade80', '#f87171', '#facc15'),
    T('ocean', 'Deep Ocean', 'Dark', true, '#071a24', '#0c2633', '#d8eef7', '#7fa3b3', '#16394a', '#22d3ee', '#34d399', '#f87171', '#fbbf24'),
    T('forest', 'Forest', 'Dark', true, '#0f1a14', '#16241c', '#dcefe2', '#8fae9a', '#24382b', '#a3e635', '#34d399', '#f87171', '#fbbf24'),
    T('coffee', 'Coffee', 'Dark', true, '#1c1612', '#261e18', '#efe3d6', '#b09c88', '#3a2e25', '#d4a373', '#8fbf6a', '#e5675a', '#e9b949'),
    T('sunset', 'Sunset', 'Dark', true, '#1f1225', '#2a1830', '#fbe7ef', '#c49ab0', '#42263f', '#fb923c', '#4ade80', '#f43f5e', '#facc15'),
    T('nord', 'Nord', 'Dark', true, '#2e3440', '#3b4252', '#eceff4', '#a3adbf', '#4c566a', '#88c0d0', '#a3be8c', '#bf616a', '#ebcb8b'),
    T('dracula', 'Dracula', 'Dark', true, '#21222c', '#282a36', '#f8f8f2', '#a1a5c2', '#44475a', '#bd93f9', '#50fa7b', '#ff5555', '#f1fa8c'),
    T('gruvbox', 'Gruvbox', 'Dark', true, '#1d2021', '#282828', '#ebdbb2', '#a89984', '#3c3836', '#83a598', '#b8bb26', '#fb4934', '#fabd2f'),
    T('solarized-dark', 'Solarized Dark', 'Dark', true, '#002b36', '#073642', '#eee8d5', '#93a1a1', '#0f4a57', '#268bd2', '#859900', '#dc322f', '#b58900'),
    T('neon', 'Neon', 'Dark', true, '#0a0a12', '#12121f', '#e6f7ff', '#8a93b8', '#25253d', '#ff2bd6', '#00ffa3', '#ff3b5c', '#ffe14d'),
    T('terminal-green', 'Green Terminal', 'Dark', true, '#020a04', '#04140a', '#39ff6a', '#22a24c', '#0e3d1e', '#39ff6a', '#39ff6a', '#ff5050', '#e5ff33', { font: MONO }),
    T('terminal-amber', 'Amber Terminal', 'Dark', true, '#0d0700', '#170d02', '#ffb000', '#b07a10', '#3a2606', '#ffb000', '#ffd166', '#ff5a36', '#ffe066', { font: MONO }),
    T('contrast-dark', 'High Contrast Dark', 'Dark', true, '#000000', '#000000', '#ffffff', '#dddddd', '#ffffff', '#ffd400', '#3dff6e', '#ff4d4d', '#ffd400'),
  ];
  const BY_ID = Object.fromEntries(THEMES.map(t => [t.id, t]));
  const KEY = 's7m.theme';

  function luminance(hex) {
    const n = parseInt(hex.slice(1), 16);
    const c = [n >> 16 & 255, n >> 8 & 255, n & 255].map(v => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; });
    return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
  }
  // Text colour on a coloured badge or button: black or white, whichever reads better.
  const onColor = hex => (luminance(hex) > 0.4 ? '#0b0d12' : '#ffffff');
  const mix = (a, b, pct) => `color-mix(in srgb, ${a} ${pct}%, ${b})`;

  function vars(t) {
    const d = t.dark;
    return {
      '--bg': t.bg, '--card': t.card, '--fg': t.fg, '--muted': t.muted, '--line': t.line,
      '--accent': t.accent, '--accent-fg': onColor(t.accent),
      '--good': t.good, '--bad': t.bad, '--warn': t.warn,
      // Text on the green/red status pills: one colour that suits both.
      '--on-badge': (luminance(t.good) + luminance(t.bad)) / 2 > 0.3 ? '#0b0d12' : '#ffffff',
      '--flash': mix(t.warn, t.card, d ? 18 : 20),
      '--menu-hover': mix(t.accent, t.card, d ? 18 : 10),
      '--soft': mix(t.accent, t.card, d ? 16 : 10),
      '--code': mix(t.fg, t.card, d ? 6 : 4),
      '--chart': t.accent, '--chart-fill': mix(t.accent, 'transparent', 14),
      '--grid': mix(t.line, t.card, 55),
      // Help diagrams: one colour per topic, kept apart from the accent.
      '--s7': d ? '#2dd4bf' : '#0f766e', '--mb': d ? '#a78bfa' : '#7c3aed',
      '--ix': d ? '#fbbf24' : '#b45309', '--mq': d ? '#22d3ee' : '#0e7490',
      '--on-topic': d ? '#0b0d12' : '#ffffff',
    };
  }

  let current = 'system';
  function apply(id) {
    const t = BY_ID[id] || BY_ID.system;
    current = t.id;
    const root = document.documentElement;
    root.removeAttribute('style');
    let font = document.getElementById('s7-theme-font');
    if (t.id === 'system') {
      root.style.colorScheme = 'light dark';
      if (font) font.textContent = '';
    } else {
      for (const [k, v] of Object.entries(vars(t))) root.style.setProperty(k, v);
      root.style.colorScheme = t.dark ? 'dark' : 'light';
      if (!font) {
        font = document.createElement('style');
        font.id = 's7-theme-font';
        (document.head || root).appendChild(font);
      }
      font.textContent = t.font ? `body, input, select, button, textarea { font-family: ${t.font} !important; }` : '';
    }
    root.dataset.theme = t.id;
    document.dispatchEvent(new CustomEvent('s7-theme', { detail: t.id }));
  }

  function saved() {
    let id = null;
    try { id = localStorage.getItem(KEY); } catch (e) {}
    return id || window.S7_SERVER_THEME || 'system';
  }

  function set(id) {
    if (!BY_ID[id]) return;
    apply(id);
    try { localStorage.setItem(KEY, id); } catch (e) {}
    // Remember it in config.yaml too (only works on the computer running PLC Monitor).
    fetch('api/ui/theme', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ theme: id }) }).catch(() => {});
  }

  // Other windows of the same program follow at once.
  window.addEventListener('storage', e => { if (e.key === KEY && e.newValue) apply(e.newValue); });

  // ---- theme picker ----
  const CSS = `
  .s7tp-back { position:fixed; inset:0; background:rgb(0 0 0 / .45); z-index:1000; display:flex; align-items:center; justify-content:center; padding:16px; }
  .s7tp { background:var(--card); color:var(--fg); border:1px solid var(--line); border-radius:12px; width:min(900px, 100%);
          max-height:calc(100vh - 32px); display:flex; flex-direction:column; box-shadow:0 20px 50px rgb(0 0 0 / .35); }
  .s7tp header { display:flex; align-items:center; gap:10px; padding:14px 18px; border-bottom:1px solid var(--line); background:none; position:static; }
  .s7tp header h2 { margin:0; font-size:17px; flex:1; }
  .s7tp header small { color:var(--muted); font-size:12px; }
  .s7tp .body { overflow:auto; padding:6px 18px 18px; }
  .s7tp h3 { margin:14px 0 8px; font-size:12px; text-transform:uppercase; letter-spacing:.05em; color:var(--muted); }
  .s7tp .grid { display:grid; grid-template-columns:repeat(auto-fill, minmax(150px, 1fr)); gap:10px; }
  .s7tp .sw { border:2px solid var(--line); border-radius:10px; padding:0; overflow:hidden; cursor:pointer; background:none; text-align:left; font:inherit; color:inherit; }
  .s7tp .sw:hover, .s7tp .sw:focus-visible { border-color:var(--accent); outline:none; }
  .s7tp .sw.on { border-color:var(--accent); box-shadow:0 0 0 2px var(--accent); }
  .s7tp .pv { height:78px; padding:8px; display:flex; flex-direction:column; gap:5px; }
  .s7tp .pv .bar { height:8px; border-radius:3px; width:55%; }
  .s7tp .pv .panel { flex:1; border-radius:5px; border:1px solid; padding:5px 6px; display:flex; gap:4px; align-items:center; font-size:10px; }
  .s7tp .pv .dot { width:9px; height:9px; border-radius:50%; flex:none; }
  .s7tp .pv .line { flex:1; height:2px; border-radius:1px; }
  .s7tp .nm { display:block; padding:6px 9px; font-size:12px; font-weight:600; border-top:1px solid var(--line); background:var(--card); }
  .s7tp .auto .pv { background:linear-gradient(120deg, #f6f7f9 50%, #0f1218 50%); }
  .s7tp footer { padding:10px 18px; border-top:1px solid var(--line); display:flex; justify-content:flex-end; gap:8px; position:static; background:none; }
  .s7tp footer button { font:inherit; padding:6px 14px; border-radius:6px; border:1px solid var(--line); background:var(--card); color:var(--fg); cursor:pointer; }
  .s7tp footer button.primary { background:var(--accent); border-color:var(--accent); color:var(--accent-fg); font-weight:600; }`;

  function swatch(t) {
    if (t.id === 'system') {
      return `<button type="button" class="sw auto" data-id="system"><div class="pv"></div><span class="nm">${t.name}</span></button>`;
    }
    return `<button type="button" class="sw" data-id="${t.id}" title="${t.name}">
      <div class="pv" style="background:${t.bg}">
        <div class="bar" style="background:${t.accent}"></div>
        <div class="panel" style="background:${t.card};border-color:${t.line};color:${t.fg}${t.font ? ';font-family:' + t.font : ''}">
          <span class="dot" style="background:${t.good}"></span><span class="dot" style="background:${t.bad}"></span>
          <span class="dot" style="background:${t.warn}"></span><span class="line" style="background:${t.accent}"></span>
          <span style="color:${t.muted}">42.5</span>
        </div>
      </div><span class="nm">${t.name}</span></button>`;
  }

  function openPicker() {
    if (document.querySelector('.s7tp-back')) return;
    if (!document.getElementById('s7tp-css')) {
      const st = document.createElement('style');
      st.id = 's7tp-css'; st.textContent = CSS;
      document.head.appendChild(st);
    }
    const start = current;
    const groups = ['Automatic', 'Light', 'Dark'];
    const back = document.createElement('div');
    back.className = 's7tp-back';
    back.innerHTML = `<div class="s7tp" role="dialog" aria-modal="true" aria-label="Theme">
      <header><h2>Theme</h2><small>${THEMES.length} themes · applies to every PLC Monitor window</small></header>
      <div class="body">${groups.map(g => `<h3>${g}</h3><div class="grid">${THEMES.filter(t => t.group === g).map(swatch).join('')}</div>`).join('')}</div>
      <footer><button type="button" data-act="cancel">Cancel</button><button type="button" class="primary" data-act="ok">OK</button></footer></div>`;
    document.body.appendChild(back);
    const mark = () => back.querySelectorAll('.sw').forEach(b => b.classList.toggle('on', b.dataset.id === current));
    mark();
    const close = keep => {
      if (!keep) apply(start); else set(current);
      back.remove();
      document.removeEventListener('keydown', onKey, true);
    };
    function onKey(e) {
      if (e.key === 'Escape') { e.stopPropagation(); close(false); }
      if (e.key === 'Enter' && e.target.classList?.contains('sw')) { e.preventDefault(); apply(e.target.dataset.id); close(true); }
    }
    document.addEventListener('keydown', onKey, true);
    back.addEventListener('click', e => {
      if (e.target === back) return close(false);
      const sw = e.target.closest('.sw');
      if (sw) { apply(sw.dataset.id); mark(); return; } // preview; OK keeps it
      const act = e.target.dataset.act;
      if (act) close(act === 'ok');
    });
    back.addEventListener('dblclick', e => { const sw = e.target.closest('.sw'); if (sw) { apply(sw.dataset.id); close(true); } });
    (back.querySelector('.sw.on') || back.querySelector('.sw')).focus();
  }

  window.S7Themes = { list: THEMES, apply, set, openPicker, get current() { return current; } };
  apply(saved());
})();
