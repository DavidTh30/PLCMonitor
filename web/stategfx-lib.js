// Library of ready-made state graphics for the State graphics window: SVG
// drawings of panel and plant parts, each with its states and conditions.
// window.STATE_LIB = [{cat, items: [{name, w, h, states: () => [{label, when, image, blink}]}]}]
(function () {
  'use strict';
  const url = s => 'data:image/svg+xml;base64,' + btoa(unescape(encodeURIComponent(s)));
  const svg = (w, h, vb, body) => `<svg xmlns="http://www.w3.org/2000/svg" width="${w}" height="${h}" viewBox="${vb}">${body}</svg>`;
  const E = v => ({ op: 'eq', value: v }), ELSE = { op: 'else' }, R = (a, b) => ({ op: 'range', min: a, max: b }), BIT = n => ({ op: 'bit', bit: n });
  const C = { off: '#6b7280', green: '#22c55e', red: '#ef4444', yellow: '#facc15', amber: '#f59e0b', blue: '#3b82f6', white: '#f8fafc',
    dark: '#1f2937', metal: '#94a3b8', body: '#d1d5db', black: '#111827' };
  const T = (x, y, s, t, fill = C.dark, w = 700) => `<text x="${x}" y="${y}" font-family="Segoe UI,Arial,sans-serif" font-size="${s}" font-weight="${w}" fill="${fill}" text-anchor="middle" dominant-baseline="middle">${t}</text>`;
  const spin = (cx, cy, dur = '1s') => `<animateTransform attributeName="transform" type="rotate" from="0 ${cx} ${cy}" to="360 ${cx} ${cy}" dur="${dur}" repeatCount="indefinite"/>`;
  const st = (label, when, s, blink) => ({ label, when, image: url(s), ...(blink ? { blink: true } : {}) });
  // The "off" look of a colour: a fixed dark tint (color-mix is not in every SVG renderer).
  const shade = c => ({ [C.green]: '#1f4d33', [C.red]: '#5b2424', [C.yellow]: '#5c5222', [C.amber]: '#5c4220', [C.blue]: '#1f3561', [C.white]: '#4b5563' }[c] || '#374151');

  // ---- LEDs and lamps ----
  const ledRound = (c, on) => svg(64, 64, '0 0 100 100',
    `<defs><radialGradient id="g" cx="38%" cy="32%" r="70%"><stop offset="0" stop-color="#fff" stop-opacity="${on ? .9 : .25}"/><stop offset=".4" stop-color="${on ? c : shade(c)}"/><stop offset="1" stop-color="${on ? c : shade(c)}"/></radialGradient>` +
    `<radialGradient id="h"><stop offset=".55" stop-color="${c}" stop-opacity=".55"/><stop offset="1" stop-color="${c}" stop-opacity="0"/></radialGradient></defs>` +
    (on ? `<circle cx="50" cy="50" r="49" fill="url(#h)"/>` : '') + `<circle cx="50" cy="50" r="34" fill="#374151"/><circle cx="50" cy="50" r="29" fill="url(#g)"/>`);
  const ledSquare = (c, on) => svg(64, 64, '0 0 100 100',
    `<rect x="14" y="14" width="72" height="72" rx="8" fill="#374151"/><rect x="22" y="22" width="56" height="56" rx="4" fill="${on ? c : shade(c)}"/>` +
    `<rect x="26" y="26" width="22" height="10" rx="3" fill="#fff" opacity="${on ? .6 : .15}"/>`);
  const pilot = (c, on) => svg(72, 72, '0 0 100 100',
    `<defs><linearGradient id="m" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#f1f5f9"/><stop offset=".5" stop-color="#94a3b8"/><stop offset="1" stop-color="#475569"/></linearGradient>` +
    `<radialGradient id="g" cx="40%" cy="35%" r="70%"><stop offset="0" stop-color="#fff" stop-opacity="${on ? .95 : .3}"/><stop offset=".45" stop-color="${on ? c : shade(c)}"/><stop offset="1" stop-color="${on ? c : shade(c)}"/></radialGradient></defs>` +
    `<circle cx="50" cy="50" r="46" fill="url(#m)"/><circle cx="50" cy="50" r="38" fill="#1f2937"/><circle cx="50" cy="50" r="33" fill="url(#g)"/>` +
    (on ? `<circle cx="50" cy="50" r="33" fill="${c}" opacity=".25"/>` : ''));
  const ledBar = (n) => svg(40, 100, '0 0 40 100', `<rect x="2" y="2" width="36" height="96" rx="5" fill="#1f2937"/>` +
    Array.from({ length: 10 }, (_, i) => { const lit = 9 - i < n, col = i < 2 ? C.red : i < 4 ? C.yellow : C.green; return `<rect x="8" y="${6 + i * 9.2}" width="24" height="7" rx="2" fill="${lit ? col : shade(col)}"/>`; }).join(''));
  const LED = [
    ...[['green', C.green], ['red', C.red], ['yellow', C.yellow], ['blue', C.blue], ['white', C.white]].map(([n, c]) =>
      ({ name: `Round LED – ${n}`, w: 64, h: 64, states: () => [st('Off', E(0), ledRound(c, false)), st('On', ELSE, ledRound(c, true))] })),
    { name: 'Square LED – green', w: 64, h: 64, states: () => [st('Off', E(0), ledSquare(C.green, false)), st('On', ELSE, ledSquare(C.green, true))] },
    { name: 'Square LED – red, blinks', w: 64, h: 64, states: () => [st('Off', E(0), ledSquare(C.red, false)), st('Alarm', ELSE, ledSquare(C.red, true), true)] },
    { name: 'Pilot lamp 22 mm – green', w: 72, h: 72, states: () => [st('Off', E(0), pilot(C.green, false)), st('On', ELSE, pilot(C.green, true))] },
    { name: 'Pilot lamp 22 mm – amber', w: 72, h: 72, states: () => [st('Off', E(0), pilot(C.amber, false)), st('On', ELSE, pilot(C.amber, true))] },
    { name: 'Bi-colour LED – off / green / red', w: 64, h: 64, states: () => [st('Off', E(0), ledRound(C.green, false)), st('Green', E(1), ledRound(C.green, true)), st('Red', ELSE, ledRound(C.red, true))] },
    { name: 'LED bar 0…100 %', w: 40, h: 100, states: () => Array.from({ length: 11 }, (_, i) => st(`${i * 10} %`, R(i ? i * 10 - 5 : -1e9, i === 10 ? 1e9 : i * 10 + 4.999), ledBar(i))) },
  ];

  // ---- push buttons (front, 22 mm) ----
  const pb = (c, label, pressed, lit) => svg(80, 80, '0 0 100 100',
    `<defs><linearGradient id="m" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#e2e8f0"/><stop offset="1" stop-color="#475569"/></linearGradient>` +
    `<radialGradient id="f" cx="45%" cy="${pressed ? 55 : 38}%" r="70%"><stop offset="0" stop-color="#fff" stop-opacity="${lit ? .85 : pressed ? .1 : .45}"/><stop offset=".5" stop-color="${lit || !pressed ? c : shade(c)}"/><stop offset="1" stop-color="${shade(c)}"/></radialGradient></defs>` +
    `<circle cx="50" cy="50" r="47" fill="url(#m)"/><circle cx="50" cy="50" r="39" fill="#111827"/><circle cx="50" cy="50" r="${pressed ? 31 : 35}" fill="url(#f)"/>` +
    (lit ? `<circle cx="50" cy="50" r="35" fill="none" stroke="${c}" stroke-width="3" opacity=".8"/>` : '') + T(50, 52, label.length > 4 ? 13 : 17, label, '#fff'));
  const estop = pressed => svg(96, 96, '0 0 100 100',
    `<defs><radialGradient id="r" cx="42%" cy="${pressed ? 52 : 36}%" r="65%"><stop offset="0" stop-color="#fecaca"/><stop offset=".45" stop-color="#dc2626"/><stop offset="1" stop-color="#7f1d1d"/></radialGradient></defs>` +
    `<circle cx="50" cy="50" r="48" fill="#facc15" stroke="#a16207" stroke-width="2"/>` +
    `<path id="a" d="M14 50 A36 36 0 0 1 86 50" fill="none"/><text font-family="Segoe UI,Arial" font-size="9" font-weight="800" fill="#111827"><textPath href="#a" startOffset="50%" text-anchor="middle">EMERGENCY STOP</textPath></text>` +
    `<circle cx="50" cy="52" r="${pressed ? 27 : 31}" fill="url(#r)" stroke="#450a0a" stroke-width="2"/>` + (pressed ? T(50, 53, 9, 'PRESSED', '#fff') : ''));
  const pbSide = (c, pressed) => svg(90, 70, '0 0 90 70',
    `<rect x="10" y="40" width="70" height="8" fill="#64748b"/><rect x="30" y="48" width="30" height="18" fill="#334155"/>` +
    `<rect x="${pressed ? 24 : 22}" y="${pressed ? 28 : 12}" width="${pressed ? 42 : 46}" height="${pressed ? 12 : 28}" rx="6" fill="${c}" stroke="#1f2937" stroke-width="2"/>`);
  const BUTTONS = [
    { name: 'Push button – START (green)', w: 80, h: 80, states: () => [st('Released', E(0), pb(C.green, 'START', false)), st('Pressed', ELSE, pb(C.green, 'START', true))] },
    { name: 'Push button – STOP (red)', w: 80, h: 80, states: () => [st('Released', E(0), pb(C.red, 'STOP', false)), st('Pressed', ELSE, pb(C.red, 'STOP', true))] },
    { name: 'Push button – RESET (blue)', w: 80, h: 80, states: () => [st('Released', E(0), pb(C.blue, 'RESET', false)), st('Pressed', ELSE, pb(C.blue, 'RESET', true))] },
    { name: 'Push button – I / O', w: 80, h: 80, states: () => [st('O', E(0), pb('#475569', 'O', false)), st('I', ELSE, pb(C.green, 'I', true))] },
    { name: 'Illuminated push button – green', w: 80, h: 80, states: () => [st('Off', E(0), pb(C.green, 'RUN', false)), st('Lit', ELSE, pb(C.green, 'RUN', false, true))] },
    { name: 'Illuminated push button – amber, blinks', w: 80, h: 80, states: () => [st('Off', E(0), pb(C.amber, 'ACK', false)), st('Blink', ELSE, pb(C.amber, 'ACK', false, true), true)] },
    { name: 'Emergency stop (mushroom)', w: 96, h: 96, states: () => [st('Released', E(0), estop(false)), st('Pressed', ELSE, estop(true), true)] },
    { name: 'Push button – side view', w: 90, h: 70, states: () => [st('Released', E(0), pbSide(C.green, false)), st('Pressed', ELSE, pbSide(C.green, true))] },
  ];

  // ---- relays ----
  const coil = (on, label = 'K1') => svg(80, 90, '0 0 80 90',
    `<line x1="40" y1="2" x2="40" y2="24" stroke="${C.dark}" stroke-width="3"/><line x1="40" y1="66" x2="40" y2="88" stroke="${C.dark}" stroke-width="3"/>` +
    `<rect x="16" y="24" width="48" height="42" fill="${on ? C.green : '#f8fafc'}" stroke="${C.dark}" stroke-width="3"/><line x1="16" y1="66" x2="64" y2="24" stroke="${C.dark}" stroke-width="2"/>` +
    T(12, 10, 10, 'A1') + T(12, 80, 10, 'A2') + T(72, 45, 11, label));
  const contactNO = (closed, c = C.dark) => svg(60, 90, '0 0 60 90',
    `<line x1="30" y1="2" x2="30" y2="30" stroke="${c}" stroke-width="4"/><line x1="30" y1="60" x2="30" y2="88" stroke="${c}" stroke-width="4"/>` +
    `<line x1="30" y1="60" x2="${closed ? 30 : 12}" y2="${closed ? 28 : 32}" stroke="${closed ? C.green : c}" stroke-width="4" stroke-linecap="round"/>` +
    `<circle cx="30" cy="60" r="3" fill="${c}"/>`);
  const contactNC = (open, c = C.dark) => svg(60, 90, '0 0 60 90',
    `<line x1="30" y1="2" x2="30" y2="30" stroke="${c}" stroke-width="4"/><line x1="30" y1="30" x2="44" y2="30" stroke="${c}" stroke-width="4"/><line x1="30" y1="60" x2="30" y2="88" stroke="${c}" stroke-width="4"/>` +
    `<line x1="30" y1="60" x2="${open ? 14 : 46}" y2="${open ? 32 : 26}" stroke="${open ? C.red : c}" stroke-width="4" stroke-linecap="round"/><circle cx="30" cy="60" r="3" fill="${c}"/>`);
  const relayBox = (on, label = 'K1') => svg(70, 90, '0 0 70 90',
    `<rect x="6" y="4" width="58" height="82" rx="4" fill="#e5e7eb" stroke="#475569" stroke-width="2"/><rect x="12" y="10" width="46" height="36" rx="3" fill="#cbd5e1"/>` +
    `<circle cx="22" cy="58" r="6" fill="${on ? C.red : '#7f1d1d'}"/>` + (on ? `<circle cx="22" cy="58" r="10" fill="${C.red}" opacity=".3"/>` : '') +
    T(35, 28, 14, label) + T(46, 60, 9, on ? 'ON' : 'OFF', on ? C.green : C.off) +
    [12, 26, 40, 54].map(x => `<rect x="${x}" y="72" width="8" height="10" fill="#475569"/>`).join(''));
  const timerRelay = (state) => svg(80, 90, '0 0 80 90', `<rect x="8" y="4" width="64" height="82" rx="5" fill="#e5e7eb" stroke="#475569" stroke-width="2"/>` +
    `<circle cx="40" cy="36" r="18" fill="#fff" stroke="#475569" stroke-width="2"/><line x1="40" y1="36" x2="${state === 2 ? 54 : state === 1 ? 46 : 40}" y2="${state === 2 ? 30 : state === 1 ? 22 : 20}" stroke="${C.dark}" stroke-width="3"/>` +
    `<circle cx="24" cy="70" r="5" fill="${state ? C.green : '#14532d'}"/><circle cx="56" cy="70" r="5" fill="${state === 2 ? C.amber : '#78350f'}"/>` + T(40, 80, 8, ['IDLE', 'TIMING', 'DONE'][state]));
  const RELAYS = [
    { name: 'Relay coil (IEC)', w: 80, h: 90, states: () => [st('Off', E(0), coil(false)), st('Energised', ELSE, coil(true))] },
    { name: 'Contact NO (IEC)', w: 60, h: 90, states: () => [st('Open', E(0), contactNO(false)), st('Closed', ELSE, contactNO(true))] },
    { name: 'Contact NC (IEC)', w: 60, h: 90, states: () => [st('Closed', E(0), contactNC(false)), st('Open', ELSE, contactNC(true))] },
    { name: 'DIN rail relay with LED', w: 70, h: 90, states: () => [st('Off', E(0), relayBox(false)), st('On', ELSE, relayBox(true))] },
    { name: 'Timer relay – idle / timing / done', w: 80, h: 90, states: () => [st('Idle', E(0), timerRelay(0)), st('Timing', E(1), timerRelay(1)), st('Done', ELSE, timerRelay(2))] },
  ];

  // ---- contactors ----
  const contactor = (state, label = 'KM1') => svg(80, 100, '0 0 80 100',
    `<rect x="6" y="6" width="68" height="88" rx="5" fill="#e5e7eb" stroke="#475569" stroke-width="2"/>` +
    [16, 34, 52].map(x => `<rect x="${x}" y="10" width="12" height="10" fill="#475569"/><rect x="${x}" y="80" width="12" height="10" fill="#475569"/>`).join('') +
    `<rect x="18" y="32" width="44" height="26" rx="3" fill="#cbd5e1" stroke="#64748b"/><rect x="${state === 1 ? 38 : 22}" y="38" width="20" height="14" rx="2" fill="${state === 2 ? C.red : state === 1 ? C.green : C.off}"/>` +
    T(32, 66, 10, state === 1 ? 'I' : 'O', state === 1 ? C.green : C.off) + T(48, 66, 10, label));
  const contactor3P = closed => svg(110, 100, '0 0 110 100',
    [25, 55, 85].map(x => `<line x1="${x}" y1="4" x2="${x}" y2="34" stroke="${C.dark}" stroke-width="4"/><line x1="${x}" y1="66" x2="${x}" y2="96" stroke="${C.dark}" stroke-width="4"/>` +
      `<line x1="${x}" y1="66" x2="${closed ? x : x - 16}" y2="${closed ? 32 : 36}" stroke="${closed ? C.green : C.dark}" stroke-width="4" stroke-linecap="round"/><path d="M${x - 5} 34 a5 5 0 0 0 10 0" fill="none" stroke="${C.dark}" stroke-width="3"/>`).join('') +
    `<line x1="${closed ? 25 : 12}" y1="50" x2="${closed ? 85 : 72}" y2="50" stroke="${C.dark}" stroke-width="2" stroke-dasharray="5 4"/>`);
  const CONTACTORS = [
    { name: 'Contactor – off / on', w: 80, h: 100, states: () => [st('Off', E(0), contactor(0)), st('On', ELSE, contactor(1))] },
    { name: 'Contactor – off / on / fault', w: 80, h: 100, states: () => [st('Off', E(0), contactor(0)), st('On', E(1), contactor(1)), st('Fault', ELSE, contactor(2), true)] },
    { name: 'Contactor 3-pole (IEC symbol)', w: 110, h: 100, states: () => [st('Open', E(0), contactor3P(false)), st('Closed', ELSE, contactor3P(true))] },
    { name: 'Contactor coil (IEC)', w: 80, h: 90, states: () => [st('Off', E(0), coil(false, 'KM1')), st('Energised', ELSE, coil(true, 'KM1'))] },
  ];

  // ---- magnetic starters (contactor + overload relay) ----
  const magnetic = state => svg(80, 130, '0 0 80 130',
    `<rect x="6" y="4" width="68" height="76" rx="5" fill="#e5e7eb" stroke="#475569" stroke-width="2"/>` +
    [16, 34, 52].map(x => `<rect x="${x}" y="8" width="12" height="9" fill="#475569"/>`).join('') +
    `<rect x="18" y="28" width="44" height="24" rx="3" fill="#cbd5e1" stroke="#64748b"/><rect x="${state === 1 ? 38 : 22}" y="33" width="20" height="14" rx="2" fill="${state === 1 ? C.green : C.off}"/>` +
    T(40, 66, 10, 'MS1') +
    `<rect x="10" y="82" width="60" height="44" rx="4" fill="#cbd5e1" stroke="#475569" stroke-width="2"/><circle cx="26" cy="100" r="8" fill="#94a3b8" stroke="#475569"/>` +
    `<rect x="44" y="92" width="18" height="12" rx="2" fill="${state === 2 ? C.red : '#e5e7eb'}" stroke="#475569"/>` + T(40, 118, 8, state === 2 ? 'OL TRIP' : 'OL', state === 2 ? C.red : C.dark));
  const overload = tripped => svg(80, 70, '0 0 80 70', `<rect x="6" y="6" width="68" height="58" rx="5" fill="#cbd5e1" stroke="#475569" stroke-width="2"/>` +
    `<circle cx="26" cy="30" r="11" fill="#94a3b8" stroke="#475569"/><line x1="26" y1="30" x2="26" y2="21" stroke="#1f2937" stroke-width="3"/>` +
    `<rect x="46" y="20" width="20" height="16" rx="2" fill="${tripped ? C.red : '#e5e7eb'}" stroke="#475569"/>` + T(40, 54, 9, tripped ? 'TRIPPED' : 'NORMAL', tripped ? C.red : C.dark));
  const MAGNETIC = [
    { name: 'Magnetic starter – off / on / overload trip', w: 80, h: 130, states: () => [st('Off', E(0), magnetic(0)), st('On', E(1), magnetic(1)), st('Overload trip', ELSE, magnetic(2), true)] },
    { name: 'Magnetic starter – off / on', w: 80, h: 130, states: () => [st('Off', E(0), magnetic(0)), st('On', ELSE, magnetic(1))] },
    { name: 'Overload relay – normal / tripped', w: 80, h: 70, states: () => [st('Normal', E(0), overload(false)), st('Tripped', ELSE, overload(true), true)] },
  ];

  // ---- motors ----
  const col = s => [C.off, C.green, C.red, C.amber][s];
  const motorSym = s => svg(90, 90, '0 0 100 100',
    `<circle cx="50" cy="50" r="40" fill="#fff" stroke="${col(s)}" stroke-width="7"/>` + T(50, 44, 30, 'M', s === 2 ? C.red : C.dark) + T(50, 70, 14, '3~', C.dark, 600) +
    (s === 1 ? `<g><path d="M50 4 A46 46 0 0 1 92 36" fill="none" stroke="${C.green}" stroke-width="4"/><path d="M92 36 l-9 -2 l4 -8 z" fill="${C.green}"/>${spin(50, 50, '1.5s')}</g>` : ''));
  const motorBody = s => svg(120, 80, '0 0 120 80',
    `<rect x="18" y="14" width="66" height="52" rx="8" fill="${col(s)}" stroke="#1f2937" stroke-width="3"/>` +
    [26, 36, 46, 56, 66, 76].map(x => `<line x1="${x}" y1="16" x2="${x}" y2="64" stroke="#000" stroke-opacity=".18" stroke-width="3"/>`).join('') +
    `<rect x="84" y="34" width="26" height="10" fill="#64748b"/><rect x="6" y="20" width="12" height="40" rx="3" fill="#475569"/><rect x="38" y="66" width="28" height="8" fill="#475569"/>` +
    `<g><rect x="104" y="26" width="5" height="26" fill="#1f2937"/>${s === 1 ? spin(106, 39, '.4s') : ''}</g>` + (s === 2 ? T(51, 40, 20, '!', '#fff') : ''));
  const motorSpeed = n => svg(90, 90, '0 0 100 100', `<circle cx="50" cy="50" r="40" fill="#fff" stroke="${n ? C.green : C.off}" stroke-width="7"/>` + T(50, 42, 28, 'M') +
    T(50, 70, 13, ['STOP', 'SLOW', 'FAST'][n], n ? C.green : C.off) +
    (n ? `<g><path d="M50 4 A46 46 0 0 1 92 36" fill="none" stroke="${C.green}" stroke-width="4"/>${spin(50, 50, n === 1 ? '3s' : '.8s')}</g>` : ''));
  const MOTORS = [
    { name: 'Motor symbol – stopped / running / fault', w: 90, h: 90, states: () => [st('Stopped', E(0), motorSym(0)), st('Running', E(1), motorSym(1)), st('Fault', ELSE, motorSym(2), true)] },
    { name: 'Motor – side view', w: 120, h: 80, states: () => [st('Stopped', E(0), motorBody(0)), st('Running', E(1), motorBody(1)), st('Fault', ELSE, motorBody(2), true)] },
    { name: 'Motor – speed (0 / slow < 50 / fast)', w: 90, h: 90, states: () => [st('Stopped', R(-1e9, 0), motorSpeed(0)), st('Slow', R(0.001, 49.999), motorSpeed(1)), st('Fast', ELSE, motorSpeed(2))] },
    { name: 'Motor symbol – off / on', w: 90, h: 90, states: () => [st('Off', E(0), motorSym(0)), st('On', ELSE, motorSym(1))] },
  ];

  // ---- pumps ----
  const pumpSym = s => svg(90, 90, '0 0 100 100',
    `<line x1="0" y1="80" x2="20" y2="80" stroke="#475569" stroke-width="8"/><line x1="50" y1="12" x2="96" y2="12" stroke="#475569" stroke-width="8"/>` +
    `<circle cx="50" cy="56" r="34" fill="${s === 1 ? '#dcfce7' : s === 2 ? '#fee2e2' : '#f1f5f9'}" stroke="${col(s)}" stroke-width="6"/>` +
    `<g><path d="M50 30 L72 68 L28 68 Z" fill="${col(s)}"/>${s === 1 ? spin(50, 56, '1s') : ''}</g>`);
  const pumpBody = s => svg(130, 80, '0 0 130 80', `<circle cx="34" cy="42" r="26" fill="${col(s)}" stroke="#1f2937" stroke-width="3"/>` +
    `<g>${[0, 60, 120].map(a => `<line x1="34" y1="42" x2="${34 + 18 * Math.cos(a * Math.PI / 180)}" y2="${42 + 18 * Math.sin(a * Math.PI / 180)}" stroke="#fff" stroke-width="4"/>`).join('')}${s === 1 ? spin(34, 42, '.6s') : ''}</g>` +
    `<rect x="24" y="4" width="20" height="14" fill="#475569"/><rect x="60" y="22" width="60" height="40" rx="6" fill="#94a3b8" stroke="#1f2937" stroke-width="3"/>` + T(90, 42, 14, 'M') + `<rect x="10" y="68" width="110" height="8" fill="#475569"/>`);
  const PUMPS = [
    { name: 'Pump symbol – stopped / running / fault', w: 90, h: 90, states: () => [st('Stopped', E(0), pumpSym(0)), st('Running', E(1), pumpSym(1)), st('Fault', ELSE, pumpSym(2), true)] },
    { name: 'Pump with motor – side view', w: 130, h: 80, states: () => [st('Stopped', E(0), pumpBody(0)), st('Running', E(1), pumpBody(1)), st('Fault', ELSE, pumpBody(2), true)] },
    { name: 'Pump symbol – off / on', w: 90, h: 90, states: () => [st('Off', E(0), pumpSym(0)), st('On', ELSE, pumpSym(1))] },
  ];

  // ---- blowers and fans ----
  const fan = s => svg(90, 90, '0 0 100 100', `<circle cx="50" cy="50" r="46" fill="#f1f5f9" stroke="${col(s)}" stroke-width="6"/>` +
    `<g>${[0, 90, 180, 270].map(a => `<path d="M50 50 Q${50 + 30 * Math.cos((a - 20) * Math.PI / 180)} ${50 + 30 * Math.sin((a - 20) * Math.PI / 180)} ${50 + 38 * Math.cos(a * Math.PI / 180)} ${50 + 38 * Math.sin(a * Math.PI / 180)} Q${50 + 20 * Math.cos((a + 25) * Math.PI / 180)} ${50 + 20 * Math.sin((a + 25) * Math.PI / 180)} 50 50" fill="${col(s)}"/>`).join('')}${s === 1 ? spin(50, 50, '.5s') : ''}</g>` +
    `<circle cx="50" cy="50" r="7" fill="#1f2937"/>`);
  const blower = s => svg(110, 90, '0 0 110 90', `<path d="M48 82 A36 36 0 1 1 84 46 L84 10 L104 10 L104 46 A56 56 0 0 1 48 82 Z" fill="#e5e7eb" stroke="${col(s)}" stroke-width="4"/>` +
    `<circle cx="48" cy="46" r="26" fill="#fff" stroke="#475569" stroke-width="2"/><g>${Array.from({ length: 8 }, (_, i) => { const a = i * 45 * Math.PI / 180; return `<line x1="${48 + 8 * Math.cos(a)}" y1="${46 + 8 * Math.sin(a)}" x2="${48 + 22 * Math.cos(a + .4)}" y2="${46 + 22 * Math.sin(a + .4)}" stroke="${col(s)}" stroke-width="4"/>`; }).join('')}${s === 1 ? spin(48, 46, '.6s') : ''}</g>`);
  const BLOWERS = [
    { name: 'Axial fan – stopped / running / fault', w: 90, h: 90, states: () => [st('Stopped', E(0), fan(0)), st('Running', E(1), fan(1)), st('Fault', ELSE, fan(2), true)] },
    { name: 'Centrifugal blower – stopped / running / fault', w: 110, h: 90, states: () => [st('Stopped', E(0), blower(0)), st('Running', E(1), blower(1)), st('Fault', ELSE, blower(2), true)] },
    { name: 'Axial fan – off / on', w: 90, h: 90, states: () => [st('Off', E(0), fan(0)), st('On', ELSE, fan(1))] },
  ];

  // ---- levels ----
  const lc = p => p >= 90 ? C.red : p >= 20 ? C.blue : C.amber;
  const tank = p => svg(80, 110, '0 0 80 110', `<rect x="8" y="6" width="64" height="98" rx="8" fill="#e5e7eb" stroke="#334155" stroke-width="4"/>` +
    `<rect x="12" y="${10 + 90 * (1 - p / 100)}" width="56" height="${90 * p / 100}" rx="4" fill="${lc(p)}"/>` + T(40, 58, 16, p + '%'));
  const silo = p => svg(80, 120, '0 0 80 120', `<clipPath id="s"><path d="M10 8 H70 V70 L46 104 H34 L10 70 Z"/></clipPath>` +
    `<rect x="0" y="${8 + 96 * (1 - p / 100)}" width="80" height="${96 * p / 100}" fill="${lc(p)}" clip-path="url(#s)"/>` +
    `<path d="M10 8 H70 V70 L46 104 H34 L10 70 Z" fill="none" stroke="#334155" stroke-width="4"/><rect x="34" y="104" width="12" height="12" fill="#475569"/>` + T(40, 46, 15, p + '%'));
  const hbar = p => svg(140, 34, '0 0 140 34', `<rect x="2" y="4" width="136" height="26" rx="5" fill="#e5e7eb" stroke="#334155" stroke-width="2"/>` +
    `<rect x="5" y="7" width="${130 * p / 100}" height="20" rx="3" fill="${lc(p)}"/>` + T(70, 18, 13, p + ' %'));
  const gauge = p => { const a = (-210 + 240 * p / 100) * Math.PI / 180; return svg(100, 90, '0 0 100 90',
    `<path d="M14 70 A40 40 0 1 1 86 70" fill="none" stroke="#e5e7eb" stroke-width="10"/><path d="M14 70 A40 40 0 0 1 22 26" fill="none" stroke="${C.amber}" stroke-width="10"/><path d="M80 28 A40 40 0 0 1 86 70" fill="none" stroke="${C.red}" stroke-width="10"/>` +
    `<line x1="50" y1="52" x2="${50 + 34 * Math.cos(a)}" y2="${52 + 34 * Math.sin(a)}" stroke="#111827" stroke-width="4" stroke-linecap="round"/><circle cx="50" cy="52" r="6" fill="#111827"/>` + T(50, 80, 14, p + '%')); };
  const switches = bits => svg(90, 110, '0 0 90 110', `<rect x="20" y="6" width="50" height="98" rx="6" fill="#e5e7eb" stroke="#334155" stroke-width="4"/>` +
    `<rect x="24" y="${bits & 2 ? 20 : 70}" width="42" height="${bits & 2 ? 80 : 30}" rx="3" fill="${C.blue}" opacity=".8"/>` +
    [['H', 26, 2], ['L', 82, 1]].map(([n, y, b]) => `<circle cx="80" cy="${y}" r="7" fill="${bits & b ? C.green : C.off}"/>` + T(10, y, 12, n)).join(''));
  const pctStates = (draw, step) => Array.from({ length: 100 / step + 1 }, (_, i) => { const p = i * step; return st(`${p} %`, R(i ? p - step / 2 : -1e9, p === 100 ? 1e9 : p + step / 2 - 0.001), draw(p)); });
  const LEVELS = [
    { name: 'Tank – 0…100 % in steps of 10', w: 80, h: 110, states: () => pctStates(tank, 10) },
    { name: 'Tank – 0…100 % in steps of 25', w: 80, h: 110, states: () => pctStates(tank, 25) },
    { name: 'Silo (cone) – 0…100 %', w: 80, h: 120, states: () => pctStates(silo, 10) },
    { name: 'Horizontal bar – 0…100 %', w: 140, h: 34, states: () => pctStates(hbar, 10) },
    { name: 'Dial gauge – 0…100 %', w: 100, h: 90, states: () => pctStates(gauge, 10) },
    { name: 'Level switches – bit 0 low, bit 1 high', w: 90, h: 110, states: () => [st('High', BIT(1), switches(3)), st('Low', BIT(0), switches(1)), st('Empty', ELSE, switches(0), true)] },
  ];

  // ---- alarm messages ----
  const banner = (bg, fg, icon, text) => svg(240, 44, '0 0 240 44', `<rect x="1" y="1" width="238" height="42" rx="6" fill="${bg}" stroke="#00000033"/>` +
    `<circle cx="22" cy="22" r="13" fill="${fg}"/>` + T(22, 23, 17, icon, bg, 800) + `<text x="44" y="23" font-family="Segoe UI,Arial" font-size="15" font-weight="700" fill="${fg}" dominant-baseline="middle">${text}</text>`);
  const bell = ring => svg(80, 80, '0 0 100 100', `<g>${ring ? '<animateTransform attributeName="transform" type="rotate" values="-14 50 20;14 50 20;-14 50 20" dur=".5s" repeatCount="indefinite"/>' : ''}` +
    `<path d="M50 12 C30 12 26 30 26 46 L20 68 H80 L74 46 C74 30 70 12 50 12 Z" fill="${ring ? C.red : C.off}" stroke="#1f2937" stroke-width="3"/><circle cx="50" cy="76" r="8" fill="${ring ? C.red : C.off}" stroke="#1f2937" stroke-width="3"/></g>` +
    (ring ? `<path d="M14 30 Q8 46 14 62 M86 30 Q92 46 86 62" fill="none" stroke="${C.red}" stroke-width="4"/>` : ''));
  const tower = s => svg(50, 130, '0 0 50 130', [['#dc2626', 3], ['#f59e0b', 2], ['#22c55e', 1]].map(([c, k], i) =>
    `<rect x="10" y="${6 + i * 32}" width="30" height="30" rx="4" fill="${s === k ? c : shade(c)}" stroke="#1f2937" stroke-width="2"/>` + (s === k ? `<rect x="14" y="${10 + i * 32}" width="8" height="22" rx="3" fill="#fff" opacity=".45"/>` : '')).join('') +
    `<rect x="12" y="102" width="26" height="12" fill="#475569"/><rect x="6" y="114" width="38" height="10" rx="2" fill="#1f2937"/>`);
  const ALARMS = [
    { name: 'Message banner – normal / warning / alarm', w: 240, h: 44, states: () => [st('Normal', E(0), banner('#dcfce7', '#15803d', '✓', 'No alarm')), st('Warning', E(1), banner('#fef3c7', '#b45309', '!', 'WARNING')), st('Alarm', ELSE, banner('#fee2e2', '#b91c1c', '!', 'ALARM – check the plant'), true)] },
    { name: 'Message banner – fault active', w: 240, h: 44, states: () => [st('OK', E(0), banner('#f1f5f9', '#475569', 'i', 'System OK')), st('Fault', ELSE, banner('#b91c1c', '#ffffff', '!', 'FAULT ACTIVE'), true)] },
    { name: 'Alarm bell – quiet / ringing', w: 80, h: 80, states: () => [st('Quiet', E(0), bell(false)), st('Ringing', ELSE, bell(true))] },
    { name: 'Signal tower – off / green / amber / red', w: 50, h: 130, states: () => [st('Off', E(0), tower(0)), st('Run (green)', E(1), tower(1)), st('Warning (amber)', E(2), tower(2)), st('Alarm (red)', ELSE, tower(3), true)] },
    { name: 'Alarm word – bit 0 / 1 / 2', w: 240, h: 44, states: () => [st('Bit 2 – Overload', BIT(2), banner('#fee2e2', '#b91c1c', '!', 'Motor overload'), true), st('Bit 1 – High level', BIT(1), banner('#fef3c7', '#b45309', '!', 'High level')), st('Bit 0 – Door open', BIT(0), banner('#e0f2fe', '#0369a1', 'i', 'Door open')), st('No alarm', ELSE, banner('#dcfce7', '#15803d', '✓', 'No alarm'))] },
  ];

  // ---- valves ----
  const vcol = s => [C.off, C.green, C.amber, C.red][s];
  const valve2 = s => svg(90, 80, '0 0 100 90', `<path d="M8 30 L50 55 L8 80 Z M92 30 L50 55 L92 80 Z" fill="${vcol(s)}" stroke="#1f2937" stroke-width="4" stroke-linejoin="round"/>` +
    `<line x1="50" y1="55" x2="50" y2="20" stroke="#1f2937" stroke-width="4"/><path d="M34 20 H66 L60 6 H40 Z" fill="#94a3b8" stroke="#1f2937" stroke-width="3"/>`);
  const butterfly = s => svg(90, 90, '0 0 100 100', `<circle cx="50" cy="50" r="40" fill="#f1f5f9" stroke="#1f2937" stroke-width="5"/>` +
    `<line x1="${s === 1 ? 50 : 18}" y1="${s === 1 ? 14 : 50}" x2="${s === 1 ? 50 : 82}" y2="${s === 1 ? 86 : 50}" stroke="${vcol(s)}" stroke-width="10" stroke-linecap="round"${s === 2 ? ' transform="rotate(45 50 50)"' : ''}/>` +
    `<circle cx="50" cy="50" r="6" fill="#1f2937"/><rect x="0" y="44" width="10" height="12" fill="#475569"/><rect x="90" y="44" width="10" height="12" fill="#475569"/>`);
  const solenoid = s => svg(90, 100, '0 0 100 110', `<path d="M8 50 L50 75 L8 100 Z M92 50 L50 75 L92 100 Z" fill="${vcol(s)}" stroke="#1f2937" stroke-width="4" stroke-linejoin="round"/>` +
    `<line x1="50" y1="75" x2="50" y2="40" stroke="#1f2937" stroke-width="4"/><rect x="30" y="8" width="40" height="32" fill="${s === 1 ? '#fde68a' : '#e5e7eb'}" stroke="#1f2937" stroke-width="3"/>` +
    `<path d="M34 34 L66 14" stroke="#1f2937" stroke-width="3"/>`);
  const motorValve = s => svg(90, 110, '0 0 100 120', `<path d="M8 60 L50 85 L8 110 Z M92 60 L50 85 L92 110 Z" fill="${vcol(s)}" stroke="#1f2937" stroke-width="4" stroke-linejoin="round"/>` +
    `<line x1="50" y1="85" x2="50" y2="48" stroke="#1f2937" stroke-width="4"/><circle cx="50" cy="28" r="20" fill="#e5e7eb" stroke="#1f2937" stroke-width="3"/>` + T(50, 29, 20, 'M'));
  const ctrlValve = p => svg(90, 110, '0 0 100 120', `<path d="M8 60 L50 85 L8 110 Z M92 60 L50 85 L92 110 Z" fill="${p ? C.green : C.off}" stroke="#1f2937" stroke-width="4" stroke-linejoin="round"/>` +
    `<line x1="50" y1="85" x2="50" y2="40" stroke="#1f2937" stroke-width="4"/><path d="M20 40 A30 30 0 0 1 80 40 Z" fill="#e5e7eb" stroke="#1f2937" stroke-width="3"/>` + T(50, 30, 14, p + '%'));
  const valve3 = s => svg(100, 100, '0 0 100 100', `<path d="M6 30 L50 52 L6 74 Z" fill="${s === 0 ? C.green : C.off}" stroke="#1f2937" stroke-width="3"/><path d="M94 30 L50 52 L94 74 Z" fill="${s === 0 ? C.green : C.off}" stroke="#1f2937" stroke-width="3"/>` +
    `<path d="M28 96 L50 52 L72 96 Z" fill="${s === 1 ? C.green : C.off}" stroke="#1f2937" stroke-width="3"/>` + T(50, 14, 12, s === 0 ? 'A ↔ B' : 'A → C'));
  // Flat valve (bow tie): a thick dark outline, grey when closed, green when open.
  const BOW = '#1e293b', BOWOFF = '#6b7584';
  const bowPath = (l, r) => `<path d="M8 8 L50 35 L8 62 Z" fill="${l}" stroke="${BOW}" stroke-width="6" stroke-linejoin="round"/><path d="M92 8 L50 35 L92 62 Z" fill="${r}" stroke="${BOW}" stroke-width="6" stroke-linejoin="round"/>`;
  const bow = (l, r = l) => svg(100, 70, '0 0 100 70', bowPath(l, r));
  const bowV = (l, r = l) => svg(70, 100, '0 0 70 100', `<g transform="translate(70 0) rotate(90)">${bowPath(l, r)}</g>`);
  const bowAct = (c, label) => svg(100, 100, '0 0 100 100', `<g transform="translate(0 30)">${bowPath(c, c)}</g><line x1="50" y1="65" x2="50" y2="26" stroke="${BOW}" stroke-width="6"/>` +
    `<rect x="28" y="6" width="44" height="22" rx="4" fill="#e5e7eb" stroke="${BOW}" stroke-width="5"/>` + (label ? T(50, 18, 12, label, BOW, 800) : ''));
  const VALVES = [
    { name: 'Valve (flat) – closed / open', w: 100, h: 70, states: () => [st('Closed', E(0), bow(BOWOFF)), st('Open', ELSE, bow(C.green))] },
    { name: 'Valve (flat) – closed / open / moving / fault', w: 100, h: 70, states: () => [st('Closed', E(0), bow(BOWOFF)), st('Open', E(1), bow(C.green)), st('Moving', E(2), bow(C.amber), true), st('Fault', ELSE, bow(C.red), true)] },
    { name: 'Valve (flat) – opening / closing halves', w: 100, h: 70, states: () => [st('Closed', E(0), bow(BOWOFF)), st('Opening', E(1), bow(C.green, BOWOFF), true), st('Open', E(2), bow(C.green)), st('Closing', E(3), bow(BOWOFF, C.green), true), st('Fault', ELSE, bow(C.red), true)] },
    { name: 'Valve (flat) – vertical', w: 70, h: 100, states: () => [st('Closed', E(0), bowV(BOWOFF)), st('Open', E(1), bowV(C.green)), st('Fault', ELSE, bowV(C.red), true)] },
    { name: 'Valve (flat) – with actuator', w: 100, h: 100, states: () => [st('Closed', E(0), bowAct(BOWOFF)), st('Open', E(1), bowAct(C.green)), st('Moving', E(2), bowAct(C.amber), true), st('Fault', ELSE, bowAct(C.red), true)] },
    { name: 'Valve (flat) – motor actuator M', w: 100, h: 100, states: () => [st('Closed', E(0), bowAct(BOWOFF, 'M')), st('Open', E(1), bowAct(C.green, 'M')), st('Fault', ELSE, bowAct(C.red, 'M'), true)] },
    { name: 'Valve – closed / open / moving / fault', w: 90, h: 80, states: () => [st('Closed', E(0), valve2(0)), st('Open', E(1), valve2(1)), st('Moving', E(2), valve2(2), true), st('Fault', ELSE, valve2(3), true)] },
    { name: 'Butterfly valve – closed / open / moving', w: 90, h: 90, states: () => [st('Closed', E(0), butterfly(0)), st('Open', E(1), butterfly(1)), st('Moving', ELSE, butterfly(2), true)] },
    { name: 'Solenoid valve – off / on', w: 90, h: 100, states: () => [st('Closed', E(0), solenoid(0)), st('Open', ELSE, solenoid(1))] },
    { name: 'Motorised valve – closed / open / moving / fault', w: 90, h: 110, states: () => [st('Closed', E(0), motorValve(0)), st('Open', E(1), motorValve(1)), st('Moving', E(2), motorValve(2), true), st('Fault', ELSE, motorValve(3), true)] },
    { name: 'Control valve – 0…100 %', w: 90, h: 110, states: () => pctStates(ctrlValve, 25) },
    { name: '3-way valve – A↔B / A→C', w: 100, h: 100, states: () => [st('A ↔ B', E(0), valve3(0)), st('A → C', ELSE, valve3(1))] },
  ];

  // ---- disconnect switches (knife switch, safety switch) ----
  const knife = closed => svg(70, 110, '0 0 70 110', `<rect x="10" y="94" width="50" height="12" fill="#78350f"/><line x1="35" y1="4" x2="35" y2="24" stroke="${C.dark}" stroke-width="4"/>` +
    `<path d="M28 24 h14 v8 h-14 z" fill="#b45309"/><circle cx="35" cy="84" r="5" fill="#b45309"/><line x1="35" y1="84" x2="35" y2="94" stroke="${C.dark}" stroke-width="4"/>` +
    `<line x1="35" y1="84" x2="${closed ? 35 : 6}" y2="${closed ? 28 : 46}" stroke="${closed ? '#d97706' : '#92400e'}" stroke-width="7" stroke-linecap="round"/><circle cx="${closed ? 35 : 6}" cy="${closed ? 28 : 46}" r="6" fill="#111827"/>`);
  const safety = on => svg(80, 110, '0 0 80 110', `<rect x="8" y="6" width="58" height="98" rx="5" fill="#9ca3af" stroke="#374151" stroke-width="3"/><rect x="16" y="16" width="42" height="50" rx="3" fill="#d1d5db"/>` +
    `<rect x="64" y="${on ? 20 : 60}" width="10" height="30" rx="3" fill="${on ? C.red : '#374151'}"/>` + T(37, 40, 13, on ? 'ON' : 'OFF', on ? C.red : C.dark) + T(37, 86, 9, 'DISCONNECT'));
  const knifeSym = closed => svg(60, 90, '0 0 60 90', `<line x1="30" y1="2" x2="30" y2="28" stroke="${C.dark}" stroke-width="4"/><line x1="22" y1="28" x2="38" y2="28" stroke="${C.dark}" stroke-width="3"/>` +
    `<line x1="30" y1="62" x2="30" y2="88" stroke="${C.dark}" stroke-width="4"/><line x1="30" y1="62" x2="${closed ? 30 : 10}" y2="${closed ? 28 : 36}" stroke="${closed ? C.green : C.dark}" stroke-width="4"/><circle cx="30" cy="62" r="3" fill="${C.dark}"/>`);
  const DISCONNECT = [
    { name: 'Knife switch (สะพานไฟ) – open / closed', w: 70, h: 110, states: () => [st('Open', E(0), knife(false)), st('Closed', ELSE, knife(true))] },
    { name: 'Safety switch box – off / on', w: 80, h: 110, states: () => [st('Off', E(0), safety(false)), st('On', ELSE, safety(true))] },
    { name: 'Disconnector (IEC symbol) – open / closed', w: 60, h: 90, states: () => [st('Open', E(0), knifeSym(false)), st('Closed', ELSE, knifeSym(true))] },
  ];

  // ---- circuit breakers ----
  const mcb = s => svg(50, 100, '0 0 50 100', `<rect x="4" y="4" width="42" height="92" rx="4" fill="#f8fafc" stroke="#475569" stroke-width="3"/><rect x="10" y="30" width="30" height="40" rx="3" fill="#cbd5e1"/>` +
    `<rect x="16" y="${s === 1 ? 32 : s === 2 ? 42 : 52}" width="18" height="16" rx="3" fill="${s === 1 ? C.green : s === 2 ? C.amber : '#374151'}"/>` +
    T(25, 16, 10, 'C16') + T(25, 84, 9, ['OFF', 'ON', 'TRIP'][s], s === 2 ? C.red : C.dark) + (s === 2 ? `<rect x="36" y="74" width="6" height="8" fill="${C.red}"/>` : ''));
  const mccb = s => svg(90, 110, '0 0 90 110', `<rect x="6" y="6" width="78" height="98" rx="6" fill="#374151" stroke="#111827" stroke-width="3"/><rect x="22" y="36" width="46" height="38" rx="3" fill="#1f2937"/>` +
    `<rect x="35" y="${s === 1 ? 30 : s === 2 ? 46 : 60}" width="20" height="22" rx="3" fill="${s === 1 ? C.red : s === 2 ? C.amber : '#9ca3af'}"/>` + T(45, 20, 10, 'MCCB 250A', '#e5e7eb') +
    T(45, 90, 11, ['O  OFF', 'I  ON', 'TRIPPED'][s], s === 2 ? C.amber : '#e5e7eb'));
  const cbSym = s => svg(60, 90, '0 0 60 90', `<line x1="30" y1="2" x2="30" y2="28" stroke="${C.dark}" stroke-width="4"/><path d="M24 22 L36 34 M36 22 L24 34" stroke="${C.dark}" stroke-width="3"/>` +
    `<line x1="30" y1="62" x2="30" y2="88" stroke="${C.dark}" stroke-width="4"/><line x1="30" y1="62" x2="${s === 1 ? 30 : 10}" y2="${s === 1 ? 28 : 36}" stroke="${s === 1 ? C.green : s === 2 ? C.red : C.dark}" stroke-width="4"/>` +
    (s === 2 ? T(48, 50, 11, 'TRIP', C.red) : ''));
  const BREAKERS = [
    { name: 'MCB – off / on / tripped', w: 50, h: 100, states: () => [st('Off', E(0), mcb(0)), st('On', E(1), mcb(1)), st('Tripped', ELSE, mcb(2), true)] },
    { name: 'MCCB – off / on / tripped', w: 90, h: 110, states: () => [st('Off', E(0), mccb(0)), st('On', E(1), mccb(1)), st('Tripped', ELSE, mccb(2), true)] },
    { name: 'Circuit breaker (IEC symbol)', w: 60, h: 90, states: () => [st('Open', E(0), cbSym(0)), st('Closed', E(1), cbSym(1)), st('Tripped', ELSE, cbSym(2), true)] },
    { name: 'MCB – off / on', w: 50, h: 100, states: () => [st('Off', E(0), mcb(0)), st('On', ELSE, mcb(1))] },
  ];

  // ---- switches ----
  const toggle = on => svg(60, 90, '0 0 60 90', `<rect x="10" y="30" width="40" height="30" rx="6" fill="#94a3b8" stroke="#334155" stroke-width="3"/><circle cx="30" cy="45" r="8" fill="#334155"/>` +
    `<line x1="30" y1="45" x2="30" y2="${on ? 8 : 82}" stroke="#cbd5e1" stroke-width="8" stroke-linecap="round"/>` + T(52, 12, 10, 'ON') + T(52, 80, 10, 'OFF'));
  const rocker = on => svg(70, 90, '0 0 70 90', `<rect x="8" y="6" width="54" height="78" rx="6" fill="#1f2937"/>` +
    `<path d="M16 ${on ? 14 : 18} H54 V44 H16 Z" fill="${on ? C.red : '#374151'}"/><path d="M16 46 H54 V${on ? 72 : 76} H16 Z" fill="${on ? '#374151' : '#4b5563'}"/>` + T(35, 30, 14, 'I', '#fff') + T(35, 62, 14, 'O', '#fff'));
  const limitSw = act => svg(80, 100, '0 0 80 100', `<rect x="20" y="40" width="44" height="56" rx="4" fill="#facc15" stroke="#334155" stroke-width="3"/>` +
    `<line x1="42" y1="40" x2="${act ? 62 : 42}" y2="${act ? 14 : 8}" stroke="#334155" stroke-width="5"/><circle cx="${act ? 62 : 42}" cy="${act ? 14 : 8}" r="7" fill="#64748b" stroke="#334155" stroke-width="2"/>` +
    `<circle cx="32" cy="80" r="5" fill="${act ? C.green : '#14532d'}"/>` + T(50, 80, 9, act ? 'ACT' : ''));
  const prox = on => svg(110, 50, '0 0 110 50', `<rect x="20" y="12" width="70" height="26" rx="3" fill="#94a3b8" stroke="#334155" stroke-width="2"/>` +
    [30, 42, 54, 66, 78].map(x => `<line x1="${x}" y1="12" x2="${x}" y2="38" stroke="#64748b" stroke-width="2"/>`).join('') +
    `<rect x="4" y="16" width="16" height="18" rx="3" fill="${on ? C.amber : '#78350f'}"/><rect x="90" y="18" width="14" height="14" fill="#1f2937"/>` + (on ? `<path d="M2 8 Q-4 25 2 42" stroke="${C.amber}" stroke-width="3" fill="none"/>` : ''));
  const SWITCHES = [
    { name: 'Toggle switch – off / on', w: 60, h: 90, states: () => [st('Off', E(0), toggle(false)), st('On', ELSE, toggle(true))] },
    { name: 'Rocker switch – O / I', w: 70, h: 90, states: () => [st('O', E(0), rocker(false)), st('I', ELSE, rocker(true))] },
    { name: 'Limit switch – free / actuated', w: 80, h: 100, states: () => [st('Free', E(0), limitSw(false)), st('Actuated', ELSE, limitSw(true))] },
    { name: 'Proximity sensor – off / detecting', w: 110, h: 50, states: () => [st('Off', E(0), prox(false)), st('Detecting', ELSE, prox(true))] },
  ];

  // ---- selector switches ----
  const selector = (n, pos, labels, key) => {
    const a = n === 2 ? [-45, 45][pos] : n === 3 ? [-60, 0, 60][pos] : [-90, -30, 30, 90][pos];
    const lab = labels.map((t, i) => { const b = (n === 2 ? [-45, 45][i] : n === 3 ? [-60, 0, 60][i] : [-90, -30, 30, 90][i]) - 90, r = 38; return T(50 + r * Math.cos(b * Math.PI / 180), 58 + r * Math.sin(b * Math.PI / 180), 9, t, i === pos ? '#15803d' : C.dark, i === pos ? 800 : 600); }).join('');
    return svg(100, 100, '0 0 100 100', `<circle cx="50" cy="58" r="24" fill="#e5e7eb" stroke="#475569" stroke-width="3"/>` + lab +
      `<g transform="rotate(${a} 50 58)">` + (key ? `<rect x="45" y="38" width="10" height="40" rx="4" fill="#94a3b8" stroke="#1f2937" stroke-width="2"/><circle cx="50" cy="42" r="3" fill="#1f2937"/>`
        : `<rect x="43" y="35" width="14" height="46" rx="6" fill="#1f2937"/><rect x="47.5" y="38" width="5" height="13" rx="2" fill="#fff"/>`) + `</g>`);
  };
  const SELECTORS = [
    { name: 'Selector 2 positions – 0 / 1', w: 100, h: 100, states: () => [st('0', E(0), selector(2, 0, ['0', '1'])), st('1', ELSE, selector(2, 1, ['0', '1']))] },
    { name: 'Selector 3 positions – HAND / OFF / AUTO', w: 100, h: 100, states: () => [st('Hand', E(0), selector(3, 0, ['HAND', 'OFF', 'AUTO'])), st('Off', E(1), selector(3, 1, ['HAND', 'OFF', 'AUTO'])), st('Auto', ELSE, selector(3, 2, ['HAND', 'OFF', 'AUTO']))] },
    { name: 'Selector 3 positions – MAN / 0 / AUTO (−1 / 0 / 1)', w: 100, h: 100, states: () => [st('Manual', E(-1), selector(3, 0, ['MAN', '0', 'AUTO'])), st('Off', E(0), selector(3, 1, ['MAN', '0', 'AUTO'])), st('Auto', ELSE, selector(3, 2, ['MAN', '0', 'AUTO']))] },
    { name: 'Key selector – LOCAL / REMOTE', w: 100, h: 100, states: () => [st('Local', E(0), selector(2, 0, ['LOC', 'REM'], true)), st('Remote', ELSE, selector(2, 1, ['LOC', 'REM'], true))] },
    { name: 'Rotary selector 4 positions – 1 / 2 / 3 / 4', w: 100, h: 100, states: () => [0, 1, 2, 3].map(i => st(String(i + 1), i === 3 ? ELSE : E(i + 1), selector(4, i, ['1', '2', '3', '4']))) },
  ];

  // ---- DIP switches ----
  const dip = (n, v) => svg(18 + n * 18, 70, `0 0 ${18 + n * 18} 70`, `<rect x="2" y="2" width="${14 + n * 18}" height="66" rx="4" fill="#dc2626"/>` + T(14, 10, 8, 'ON', '#fff') +
    Array.from({ length: n }, (_, i) => { const on = (v >> i) & 1; return `<rect x="${11 + i * 18}" y="16" width="12" height="36" rx="2" fill="#f8fafc"/><rect x="${12 + i * 18}" y="${on ? 18 : 34}" width="10" height="16" rx="2" fill="#1f2937"/>` + T(17 + i * 18, 60, 9, i + 1, '#fff'); }).join(''));
  const DIPS = [
    { name: 'DIP switch 4 positions – value 0…15', w: 90, h: 70, states: () => Array.from({ length: 16 }, (_, v) => st(`${v} (${v.toString(2).padStart(4, '0')})`, E(v), dip(4, v))) },
    { name: 'DIP switch 2 positions – value 0…3', w: 54, h: 70, states: () => Array.from({ length: 4 }, (_, v) => st(`${v} (${v.toString(2).padStart(2, '0')})`, E(v), dip(2, v))) },
    { name: 'DIP lever – one bit (off / on)', w: 36, h: 70, states: () => [st('Off', E(0), dip(1, 0)), st('On', ELSE, dip(1, 1))] },
    { name: 'DIP switch 8 positions – one graphic per lever (bit 0)', w: 36, h: 70, states: () => [st('On', BIT(0), dip(1, 1)), st('Off', ELSE, dip(1, 0))] },
  ];

  // ---- timers ----
  const timerFB = (kind, s) => svg(110, 90, '0 0 110 90', `<rect x="22" y="8" width="66" height="74" rx="4" fill="#f8fafc" stroke="${C.dark}" stroke-width="3"/>` +
    T(55, 20, 14, kind) + `<line x1="2" y1="38" x2="22" y2="38" stroke="${s ? C.green : C.dark}" stroke-width="4"/><line x1="2" y1="58" x2="22" y2="58" stroke="${C.dark}" stroke-width="3"/>` +
    `<line x1="88" y1="38" x2="108" y2="38" stroke="${s === 2 ? C.green : C.dark}" stroke-width="4"/><line x1="88" y1="58" x2="108" y2="58" stroke="${C.dark}" stroke-width="3"/>` +
    T(34, 38, 9, 'IN') + T(34, 58, 9, 'PT') + T(80, 38, 9, 'Q') + T(78, 58, 9, 'ET') +
    `<rect x="28" y="68" width="54" height="8" rx="2" fill="#e5e7eb"/>` +
    (s === 1 ? `<rect x="28" y="68" width="0" height="8" rx="2" fill="${C.amber}"><animate attributeName="width" from="0" to="54" dur="2s" repeatCount="indefinite"/></rect>` : s === 2 ? `<rect x="28" y="68" width="54" height="8" rx="2" fill="${C.green}"/>` : ''));
  const TFB = kind => [st('Idle', E(0), timerFB(kind, 0)), st('Timing', E(1), timerFB(kind, 1)), st('Done (Q)', ELSE, timerFB(kind, 2))];
  const ring = p => svg(90, 90, '0 0 100 100', `<circle cx="50" cy="50" r="40" fill="#fff" stroke="#e5e7eb" stroke-width="11"/>` +
    `<circle cx="50" cy="50" r="40" fill="none" stroke="${p >= 100 ? C.green : C.blue}" stroke-width="11" stroke-dasharray="${(251.33 * p / 100).toFixed(2)} 251.33" transform="rotate(-90 50 50)"/>` + T(50, 51, 18, p + '%'));
  const wedge = (p, c) => { if (p <= 0) return ''; if (p >= 100) return `<circle cx="50" cy="50" r="38" fill="${c}"/>`; const a = p / 100 * 2 * Math.PI;
    return `<path d="M50 50 L50 12 A38 38 0 ${p > 50 ? 1 : 0} 1 ${(50 + 38 * Math.sin(a)).toFixed(2)} ${(50 - 38 * Math.cos(a)).toFixed(2)} Z" fill="${c}"/>`; };
  const clock = p => { const a = p / 100 * 2 * Math.PI; return svg(90, 90, '0 0 100 100', `<circle cx="50" cy="50" r="44" fill="#fff" stroke="${C.dark}" stroke-width="4"/>` + wedge(p, p >= 100 ? C.green : '#fcd34d') +
    Array.from({ length: 12 }, (_, i) => { const b = i * Math.PI / 6; return `<line x1="${50 + 38 * Math.sin(b)}" y1="${50 - 38 * Math.cos(b)}" x2="${50 + 43 * Math.sin(b)}" y2="${50 - 43 * Math.cos(b)}" stroke="${C.dark}" stroke-width="2"/>`; }).join('') +
    `<line x1="50" y1="50" x2="${(50 + 34 * Math.sin(a)).toFixed(2)}" y2="${(50 - 34 * Math.cos(a)).toFixed(2)}" stroke="${C.dark}" stroke-width="4" stroke-linecap="round"/><circle cx="50" cy="50" r="4" fill="${C.dark}"/>`); };
  const countdown = p => svg(90, 90, '0 0 100 100', `<circle cx="50" cy="50" r="44" fill="#fff" stroke="${C.dark}" stroke-width="4"/>` + wedge(100 - p, p >= 100 ? C.red : '#fca5a5') + T(50, 52, 16, (100 - p) + '%', C.dark));
  const hourglass = s => svg(60, 100, '0 0 60 100', `<rect x="6" y="4" width="48" height="8" rx="2" fill="${C.dark}"/><rect x="6" y="88" width="48" height="8" rx="2" fill="${C.dark}"/>` +
    (s === 0 ? `<path d="M14 14 H46 L32 46 H28 Z" fill="${C.amber}"/>` : s === 1 ? `<path d="M20 30 H40 L32 46 H28 Z" fill="${C.amber}"/><path d="M16 86 L30 72 L44 86 Z" fill="${C.amber}"/><line x1="30" y1="48" x2="30" y2="84" stroke="${C.amber}" stroke-width="2" stroke-dasharray="3 3"><animate attributeName="stroke-dashoffset" from="6" to="0" dur=".4s" repeatCount="indefinite"/></line>` : `<path d="M12 86 L30 60 L48 86 Z" fill="${C.green}"/>`) +
    `<path d="M12 12 H48 Q48 34 32 50 Q48 66 48 88 H12 Q12 66 28 50 Q12 34 12 12 Z" fill="none" stroke="${C.dark}" stroke-width="4"/>`);
  const tdisp = s => svg(140, 50, '0 0 140 50', `<rect x="2" y="2" width="136" height="46" rx="6" fill="#111827" stroke="#475569" stroke-width="2"/>` +
    `<text x="70" y="27" font-family="Consolas,monospace" font-size="20" font-weight="700" text-anchor="middle" dominant-baseline="middle" fill="${[C.off, C.amber, C.green][s]}">${['IDLE', 'RUN', 'DONE'][s]}</text>` +
    `<circle cx="16" cy="25" r="5" fill="${s === 1 ? C.amber : s === 2 ? C.green : '#374151'}"/>`);
  const tbar = p => svg(140, 40, '0 0 140 40', `<rect x="2" y="8" width="136" height="20" rx="4" fill="#e5e7eb" stroke="${C.dark}" stroke-width="2"/>` +
    `<rect x="5" y="11" width="${130 * p / 100}" height="14" rx="2" fill="${p >= 100 ? C.green : C.amber}"/>` + T(70, 18, 11, `ET ${p}%`) +
    [0, 25, 50, 75, 100].map(t => `<line x1="${5 + 1.3 * t}" y1="30" x2="${5 + 1.3 * t}" y2="36" stroke="${C.dark}" stroke-width="2"/>`).join(''));
  const TIMERS = [
    { name: 'Timer TON (on delay) – idle / timing / done', w: 110, h: 90, states: () => TFB('TON') },
    { name: 'Timer TOF (off delay) – idle / timing / done', w: 110, h: 90, states: () => TFB('TOF') },
    { name: 'Timer TP (pulse) – idle / timing / done', w: 110, h: 90, states: () => TFB('TP') },
    { name: 'Timer TONR (retentive) – idle / timing / done', w: 110, h: 90, states: () => TFB('TONR') },
    { name: 'Progress ring – elapsed 0…100 %', w: 90, h: 90, states: () => pctStates(ring, 10) },
    { name: 'Clock dial – elapsed 0…100 %', w: 90, h: 90, states: () => pctStates(clock, 10) },
    { name: 'Countdown – remaining 100…0 %', w: 90, h: 90, states: () => pctStates(countdown, 10) },
    { name: 'Timer bar – ET 0…100 %', w: 140, h: 40, states: () => pctStates(tbar, 10) },
    { name: 'Hourglass – idle / running / done', w: 60, h: 100, states: () => [st('Idle', E(0), hourglass(0)), st('Running', E(1), hourglass(1)), st('Done', ELSE, hourglass(2))] },
    { name: 'Timer display – IDLE / RUN / DONE', w: 140, h: 50, states: () => [st('Idle', E(0), tdisp(0)), st('Run', E(1), tdisp(1)), st('Done', ELSE, tdisp(2))] },
  ];

  // ---- flap valves ----
  const flapAng = s => [90, 0, 45, 90][s]; // closed across the flow, open along it
  const flapCore = (s, dy = 0) => `<g transform="translate(0 ${dy})"><line x1="0" y1="18" x2="100" y2="18" stroke="${C.dark}" stroke-width="5"/><line x1="0" y1="62" x2="100" y2="62" stroke="${C.dark}" stroke-width="5"/>` +
    `<circle cx="50" cy="40" r="27" fill="#f1f5f9" stroke="${C.dark}" stroke-width="4"/>` +
    `<line x1="28" y1="40" x2="72" y2="40" stroke="${vcol(s)}" stroke-width="9" stroke-linecap="round" transform="rotate(${flapAng(s)} 50 40)"/><circle cx="50" cy="40" r="5" fill="${C.dark}"/></g>`;
  const flap = s => svg(100, 80, '0 0 100 80', flapCore(s));
  const flapPneu = s => svg(100, 110, '0 0 100 110', flapCore(s, 30) + `<line x1="50" y1="43" x2="50" y2="${s === 1 ? 14 : s === 2 ? 22 : 30}" stroke="${C.dark}" stroke-width="4"/>` +
    `<rect x="30" y="2" width="40" height="22" rx="3" fill="${s === 3 ? '#fee2e2' : '#e5e7eb'}" stroke="${C.dark}" stroke-width="3"/><rect x="32" y="${s === 1 ? 4 : s === 2 ? 10 : 16}" width="36" height="5" fill="#64748b"/>`);
  const damper = s => svg(100, 90, '0 0 100 90', `<rect x="8" y="6" width="84" height="78" rx="3" fill="#f1f5f9" stroke="${C.dark}" stroke-width="4"/>` +
    [21, 45, 69].map(y => `<line x1="38" y1="${y}" x2="62" y2="${y}" stroke="${vcol(s)}" stroke-width="7" stroke-linecap="round" transform="rotate(${flapAng(s)} 50 ${y})"/><circle cx="50" cy="${y}" r="3.5" fill="${C.dark}"/>`).join('') +
    `<line x1="50" y1="21" x2="50" y2="69" stroke="${C.dark}" stroke-width="1.5" stroke-dasharray="3 3"/>`);
  const checkFlap = s => svg(100, 80, '0 0 100 80', `<line x1="0" y1="18" x2="100" y2="18" stroke="${C.dark}" stroke-width="5"/><line x1="0" y1="62" x2="100" y2="62" stroke="${C.dark}" stroke-width="5"/>` +
    `<line x1="50" y1="20" x2="${s === 1 ? 82 : 50}" y2="${s === 1 ? 34 : 60}" stroke="${s === 1 ? C.green : s === 2 ? C.red : C.off}" stroke-width="8" stroke-linecap="round"/><circle cx="50" cy="21" r="4" fill="${C.dark}"/>` +
    (s === 1 ? `<path d="M10 44 H34 M28 38 L34 44 L28 50" fill="none" stroke="${C.green}" stroke-width="3"/>` : ''));
  const slide = s => svg(120, 100, '0 0 120 100', `<path d="M10 4 L36 46 H64 L90 4" fill="#f1f5f9" stroke="${C.dark}" stroke-width="4" stroke-linejoin="round"/><line x1="36" y1="58" x2="36" y2="96" stroke="${C.dark}" stroke-width="4"/><line x1="64" y1="58" x2="64" y2="96" stroke="${C.dark}" stroke-width="4"/>` +
    `<rect x="${[30, 70, 50, 30][s]}" y="47" width="40" height="10" rx="2" fill="${vcol(s)}" stroke="${C.dark}" stroke-width="2"/>` + (s === 1 ? `<path d="M44 66 V88 M38 82 L44 88 L50 82 M56 66 V88 M50 82 L56 88 L62 82" fill="none" stroke="${C.amber}" stroke-width="3"/>` : ''));
  const hopFlap = s => svg(90, 110, '0 0 90 110', `<path d="M8 6 H82 V44 L56 78 H34 L8 44 Z" fill="#f1f5f9" stroke="${C.dark}" stroke-width="4" stroke-linejoin="round"/>` +
    `<line x1="34" y1="80" x2="${s === 1 ? 40 : 56}" y2="${s === 1 ? 104 : 80}" stroke="${vcol(s)}" stroke-width="7" stroke-linecap="round"/><circle cx="34" cy="80" r="4" fill="${C.dark}"/>`);
  const F4 = f => [st('Closed', E(0), f(0)), st('Open', E(1), f(1)), st('Moving', E(2), f(2), true), st('Fault', ELSE, f(3), true)];
  const FLAPS = [
    { name: 'Flap valve – closed / open / moving / fault', w: 100, h: 80, states: () => F4(flap) },
    { name: 'Flap valve – pneumatic actuator', w: 100, h: 110, states: () => F4(flapPneu) },
    { name: 'Multi-blade damper (duct)', w: 100, h: 90, states: () => F4(damper) },
    { name: 'Non-return flap (check) – closed / open / stuck', w: 100, h: 80, states: () => [st('Closed', E(0), checkFlap(0)), st('Open (flow)', E(1), checkFlap(1)), st('Stuck', ELSE, checkFlap(2), true)] },
    { name: 'Slide gate (silo outlet)', w: 120, h: 100, states: () => F4(slide) },
    { name: 'Hopper bottom flap', w: 90, h: 110, states: () => F4(hopFlap) },
    { name: 'Flap valve – closed / open', w: 100, h: 80, states: () => [st('Closed', E(0), flap(0)), st('Open', ELSE, flap(1))] },
  ];

  // ---- diverter valves ----
  const pipe = (d, c) => `<path d="${d}" fill="none" stroke="${C.dark}" stroke-width="22" stroke-linejoin="round"/><path d="${d}" fill="none" stroke="${c}" stroke-width="14" stroke-linejoin="round"/>`;
  const divY = (s, leds) => { // 0 → A, 1 → B, 2 moving, 3 fault
    const A = 'M50 40 L18 96', B = 'M50 40 L82 96', on = s === 3 ? C.red : C.green, inC = s === 2 ? C.amber : s === 3 ? C.red : C.green;
    const flapEnd = s === 0 ? [66, 68] : s === 1 ? [34, 68] : [50, 72];
    return svg(100, 104, '0 0 100 104', pipe(A, s === 0 ? on : '#e5e7eb') + pipe(B, s === 1 ? on : '#e5e7eb') + pipe('M50 0 V40', inC) +
      `<line x1="50" y1="40" x2="${flapEnd[0]}" y2="${flapEnd[1]}" stroke="${C.dark}" stroke-width="6" stroke-linecap="round"/><circle cx="50" cy="40" r="5" fill="${C.dark}"/>` +
      T(10, 80, 13, 'A') + T(90, 80, 13, 'B') +
      (leds ? `<circle cx="12" cy="10" r="7" fill="${s === 0 || s === 3 ? C.green : '#14532d'}" stroke="${C.dark}"/><circle cx="88" cy="10" r="7" fill="${s === 1 || s === 3 ? C.green : '#14532d'}" stroke="${C.dark}"/>` + T(26, 10, 9, 'ZSA') + T(74, 10, 9, 'ZSB') : ''));
  };
  const plug3 = s => svg(100, 100, '0 0 100 100', pipe('M0 40 H100', '#e5e7eb') + pipe('M50 40 V100', '#e5e7eb') +
    `<circle cx="50" cy="40" r="20" fill="#cbd5e1" stroke="${C.dark}" stroke-width="4"/>` +
    `<path d="${s === 0 ? 'M30 40 H70' : 'M30 40 H50 V60'}" fill="none" stroke="${s === 2 ? C.red : C.green}" stroke-width="10" stroke-linejoin="round"/>` +
    `<path d="${s === 0 ? 'M0 40 H30 M70 40 H100' : 'M0 40 H30 M50 60 V100'}" fill="none" stroke="${s === 2 ? C.red : C.green}" stroke-width="14"/>` + T(92, 22, 12, 'A') + T(66, 92, 12, 'B'));
  const rot3 = s => { const ang = [-35, 0, 35][s] ?? 0, fault = s === 3;
    return svg(100, 100, '0 0 100 100', [-35, 0, 35].map((a, i) => `<g transform="rotate(${a} 50 34)">${pipe('M50 60 V100', i === s ? C.green : '#e5e7eb')}</g>`).join('') +
      `<circle cx="50" cy="34" r="28" fill="${fault ? '#fee2e2' : '#f1f5f9'}" stroke="${fault ? C.red : C.dark}" stroke-width="4"/>` + pipe('M50 0 V12', fault ? C.red : C.green) +
      `<g transform="rotate(${ang} 50 34)"><path d="M42 20 H58 L56 58 H44 Z" fill="${fault ? C.red : C.green}" stroke="${C.dark}" stroke-width="3"/></g>`);
  };
  const D4 = (f, leds) => [st('Position A', E(0), f(0, leds)), st('Position B', E(1), f(1, leds)), st('Moving', E(2), f(2, leds), true), st('Fault', ELSE, f(3, leds), true)];
  const DIVERTERS = [
    { name: 'Diverter (Y) – A / B / moving / fault', w: 100, h: 104, states: () => D4(divY) },
    { name: 'Diverter (Y) with position switches ZSA / ZSB', w: 100, h: 104, states: () => D4(divY, true) },
    { name: 'Diverter (Y) – A / B', w: 100, h: 104, states: () => [st('Position A', E(0), divY(0)), st('Position B', ELSE, divY(1))] },
    { name: '3-way plug diverter – A / B / fault', w: 100, h: 100, states: () => [st('Position A', E(0), plug3(0)), st('Position B', E(1), plug3(1)), st('Fault', ELSE, plug3(2), true)] },
    { name: 'Rotary diverter – outlet 1 / 2 / 3 / fault', w: 100, h: 100, states: () => [st('Outlet 1', E(0), rot3(0)), st('Outlet 2', E(1), rot3(1)), st('Outlet 3', E(2), rot3(2)), st('Fault', ELSE, rot3(3), true)] },
  ];

  // ---- symbols (P&ID / HMI) ----
  const tint = s => ['#f1f5f9', '#dcfce7', '#fee2e2'][s];
  const S3 = f => [st('Stopped', E(0), f(0)), st('Running', E(1), f(1)), st('Fault', ELSE, f(2), true)];
  const sym = (s, w, h, body) => svg(w, h, `0 0 ${w} ${h}`, body(col(s), tint(s)));
  const heater = s => sym(s, 100, 80, (c, t) => `<rect x="6" y="10" width="88" height="60" rx="6" fill="${t}" stroke="${c}" stroke-width="5"/><polyline points="14,40 24,22 34,58 44,22 54,58 64,22 74,58 84,40" fill="none" stroke="${s === 1 ? C.red : C.dark}" stroke-width="4" stroke-linejoin="round"/>`);
  const hx = s => sym(s, 100, 100, (c, t) => `<circle cx="50" cy="50" r="40" fill="${t}" stroke="${c}" stroke-width="5"/><polyline points="4,50 24,50 34,30 46,70 58,30 70,70 78,50 96,50" fill="none" stroke="${C.dark}" stroke-width="4" stroke-linejoin="round"/>`);
  const filt = s => sym(s, 100, 100, (c, t) => `<path d="M50 6 L94 50 L50 94 L6 50 Z" fill="${t}" stroke="${c}" stroke-width="5" stroke-linejoin="round"/><line x1="28" y1="28" x2="72" y2="72" stroke="${C.dark}" stroke-width="3" stroke-dasharray="6 5"/>`);
  const cyclone = s => sym(s, 90, 110, (c, t) => `<path d="M12 8 H78 V40 L52 100 H38 L12 40 Z" fill="${t}" stroke="${c}" stroke-width="5" stroke-linejoin="round"/><rect x="34" y="0" width="22" height="12" fill="${C.dark}"/><path d="M0 22 H14" stroke="${C.dark}" stroke-width="8"/>` +
    `<g><path d="M30 30 Q45 46 60 30 Q48 58 45 72" fill="none" stroke="${C.dark}" stroke-width="2.5"/>${s === 1 ? spin(45, 46, '1.2s') : ''}</g>`);
  const mixer = s => sym(s, 90, 110, (c, t) => `<path d="M8 14 H82 V96 Q82 104 74 104 H16 Q8 104 8 96 Z" fill="${t}" stroke="${c}" stroke-width="5"/><rect x="34" y="0" width="22" height="14" rx="2" fill="${C.dark}"/>` +
    `<line x1="45" y1="14" x2="45" y2="80" stroke="${C.dark}" stroke-width="4"/><g transform="translate(45 80)"><g><path d="M-24 -6 L0 0 L-24 6 Z M24 -6 L0 0 L24 6 Z" fill="${C.dark}"/>${s === 1 ? '<animateTransform attributeName="transform" type="scale" values="1 1;0.15 1;-1 1;0.15 1;1 1" dur=".8s" repeatCount="indefinite"/>' : ''}</g></g>`);
  const conveyor = s => sym(s, 140, 60, (c, t) => `<rect x="6" y="12" width="128" height="36" rx="18" fill="${t}" stroke="${c}" stroke-width="5"/>` +
    [24, 116].map(x => `<circle cx="${x}" cy="30" r="12" fill="#fff" stroke="${C.dark}" stroke-width="3"/><g><line x1="${x - 10}" y1="30" x2="${x + 10}" y2="30" stroke="${C.dark}" stroke-width="3"/><line x1="${x}" y1="20" x2="${x}" y2="40" stroke="${C.dark}" stroke-width="3"/>${s === 1 ? spin(x, 30, '.8s') : ''}</g>`).join('') +
    `<line x1="40" y1="14" x2="100" y2="14" stroke="${C.dark}" stroke-width="3" stroke-dasharray="8 6">${s === 1 ? '<animate attributeName="stroke-dashoffset" from="14" to="0" dur=".5s" repeatCount="indefinite"/>' : ''}</line>`);
  const screw = s => sym(s, 140, 60, (c, t) => `<rect x="6" y="12" width="128" height="36" rx="4" fill="${t}" stroke="${c}" stroke-width="5"/>` +
    `<path d="M12 30 ${Array.from({ length: 8 }, (_, i) => `Q${19 + i * 15} ${i % 2 ? 46 : 14} ${27 + i * 15} 30`).join(' ')}" fill="none" stroke="${C.dark}" stroke-width="3" stroke-dasharray="10 4">${s === 1 ? '<animate attributeName="stroke-dashoffset" from="14" to="0" dur=".4s" repeatCount="indefinite"/>' : ''}</path>` +
    `<line x1="8" y1="30" x2="132" y2="30" stroke="${C.dark}" stroke-width="2"/>`);
  const airlock = s => sym(s, 100, 100, (c, t) => `<rect x="10" y="10" width="80" height="80" rx="6" fill="${t}" stroke="${c}" stroke-width="5"/><circle cx="50" cy="50" r="30" fill="#fff" stroke="${C.dark}" stroke-width="3"/>` +
    `<g>${Array.from({ length: 6 }, (_, i) => { const a = i * Math.PI / 3; return `<line x1="50" y1="50" x2="${(50 + 28 * Math.cos(a)).toFixed(1)}" y2="${(50 + 28 * Math.sin(a)).toFixed(1)}" stroke="${C.dark}" stroke-width="4"/>`; }).join('')}${s === 1 ? spin(50, 50, '1.5s') : ''}</g><circle cx="50" cy="50" r="5" fill="${C.dark}"/>`);
  const compressor = s => sym(s, 100, 100, (c, t) => `<circle cx="50" cy="50" r="40" fill="${t}" stroke="${c}" stroke-width="5"/><path d="M22 22 L80 36 V64 L22 78" fill="none" stroke="${C.dark}" stroke-width="4" stroke-linejoin="round"/>`);
  const hopper = s => sym(s, 90, 110, (c, t) => `<path d="M8 8 H82 V50 L54 96 H36 L8 50 Z" fill="${t}" stroke="${c}" stroke-width="5" stroke-linejoin="round"/><rect x="36" y="96" width="18" height="12" fill="${C.dark}"/>` +
    (s === 1 ? `<path d="M40 100 V108 M45 100 V108 M50 100 V108" stroke="${C.amber}" stroke-width="2"/>` : ''));
  const scale = s => sym(s, 120, 80, (c, t) => `<rect x="10" y="50" width="100" height="14" rx="3" fill="${t}" stroke="${c}" stroke-width="5"/><path d="M20 64 L14 76 M100 64 L106 76" stroke="${C.dark}" stroke-width="4"/>` +
    `<rect x="34" y="6" width="52" height="30" rx="4" fill="#111827"/><text x="60" y="22" font-family="Consolas,monospace" font-size="14" font-weight="700" fill="${col(s) === C.off ? '#9ca3af' : col(s)}" text-anchor="middle" dominant-baseline="middle">kg</text><line x1="60" y1="36" x2="60" y2="50" stroke="${C.dark}" stroke-width="4"/>`);
  const photo = s => sym(s, 140, 50, (c, t) => `<rect x="4" y="10" width="26" height="30" rx="3" fill="${t}" stroke="${c}" stroke-width="4"/><rect x="110" y="10" width="26" height="30" rx="3" fill="${t}" stroke="${c}" stroke-width="4"/>` +
    `<line x1="30" y1="25" x2="110" y2="25" stroke="${s === 1 ? C.red : '#cbd5e1'}" stroke-width="${s === 1 ? 3 : 2}" stroke-dasharray="${s === 1 ? '0' : '6 5'}"/>`);
  const flow = s => sym(s, 140, 40, (c) => `<line x1="4" y1="20" x2="120" y2="20" stroke="${c}" stroke-width="10" stroke-dasharray="${s === 1 ? '14 8' : '0'}">${s === 1 ? '<animate attributeName="stroke-dashoffset" from="22" to="0" dur=".6s" repeatCount="indefinite"/>' : ''}</line><path d="M116 6 L136 20 L116 34 Z" fill="${c}"/>`);
  const instr = (code, s) => svg(80, 80, '0 0 80 80', `<circle cx="40" cy="40" r="34" fill="${['#fff', '#fef3c7', '#fee2e2', '#ede9fe'][s]}" stroke="${[C.dark, C.amber, C.red, '#7c3aed'][s]}" stroke-width="4"/>` +
    `<line x1="6" y1="40" x2="74" y2="40" stroke="${C.dark}" stroke-width="2"/>` + T(40, 27, 15, code) + T(40, 54, 13, '101', C.dark, 600));
  const I4 = code => [st('Normal', E(0), instr(code, 0)), st('High', E(1), instr(code, 1)), st('High-high', E(2), instr(code, 2), true), st('Fault', ELSE, instr(code, 3), true)];
  const SYMBOLS = [
    { name: 'Heater', w: 100, h: 80, states: () => S3(heater) },
    { name: 'Heat exchanger', w: 100, h: 100, states: () => S3(hx) },
    { name: 'Filter', w: 100, h: 100, states: () => S3(filt) },
    { name: 'Cyclone', w: 90, h: 110, states: () => S3(cyclone) },
    { name: 'Mixer / agitator', w: 90, h: 110, states: () => S3(mixer) },
    { name: 'Belt conveyor', w: 140, h: 60, states: () => S3(conveyor) },
    { name: 'Screw conveyor', w: 140, h: 60, states: () => S3(screw) },
    { name: 'Rotary valve (airlock)', w: 100, h: 100, states: () => S3(airlock) },
    { name: 'Compressor', w: 100, h: 100, states: () => S3(compressor) },
    { name: 'Hopper – idle / discharging / fault', w: 90, h: 110, states: () => S3(hopper) },
    { name: 'Weigh scale', w: 120, h: 80, states: () => S3(scale) },
    { name: 'Photo eye – clear / blocked / fault', w: 140, h: 50, states: () => [st('Clear', E(0), photo(0)), st('Blocked', E(1), photo(1)), st('Fault', ELSE, photo(2), true)] },
    { name: 'Flow line – no flow / flow / fault', w: 140, h: 40, states: () => [st('No flow', E(0), flow(0)), st('Flow', E(1), flow(1)), st('Fault', ELSE, flow(2), true)] },
    ...['PT', 'TT', 'LT', 'FT'].map(k => ({ name: `Instrument ${k} – normal / high / high-high / fault`, w: 80, h: 80, states: () => I4(k) })),
  ];

  window.STATE_LIB = [
    { cat: 'LED', items: LED }, { cat: 'Push buttons', items: BUTTONS }, { cat: 'Relays', items: RELAYS }, { cat: 'Contactors', items: CONTACTORS },
    { cat: 'Magnetic starters', items: MAGNETIC }, { cat: 'Motors', items: MOTORS }, { cat: 'Pumps', items: PUMPS }, { cat: 'Blowers / fans', items: BLOWERS },
    { cat: 'Levels', items: LEVELS }, { cat: 'Alarm messages', items: ALARMS }, { cat: 'Valves', items: VALVES }, { cat: 'Disconnect switches (สะพานไฟ)', items: DISCONNECT },
    { cat: 'Circuit breakers', items: BREAKERS }, { cat: 'Switches', items: SWITCHES }, { cat: 'Selector switches', items: SELECTORS }, { cat: 'DIP switches', items: DIPS },
    { cat: 'Timers', items: TIMERS }, { cat: 'Flap valves', items: FLAPS }, { cat: 'Diverter valves', items: DIVERTERS }, { cat: 'Symbols', items: SYMBOLS },
  ];
  window.STATE_LIB_SVG = { url, svg, T }; // for other windows (Alarm messages)

  // ---- colours: read and change the colours of SVG state images ----
  const dec = u => { const m = /^data:image\/svg\+xml;base64,(.*)$/.exec(u || ''); return m ? decodeURIComponent(escape(atob(m[1]))) : null; };
  const HEX = /(?<!url\()(?<!href=")#([0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{3})(?![0-9a-zA-Z_-])/g;
  const norm = h => { h = h.toLowerCase().replace('#', ''); return '#' + (h.length === 3 ? h.split('').map(c => c + c).join('') : h.slice(0, 6)); };
  const colorsOfSvg = s => { const n = new Map(); for (const m of s.matchAll(HEX)) { const c = norm(m[0]); n.set(c, (n.get(c) || 0) + 1); } return n; };
  const recolorSvg = (s, map) => s.replace(HEX, m => map[norm(m)] || m);
  // colorsOf(images) → [[#rrggbb, count]] most used first; recolor(image, {old: new}) → image.
  const colorsOf = images => { const n = new Map(); for (const u of images) { const s = dec(u); if (s) for (const [c, k] of colorsOfSvg(s)) n.set(c, (n.get(c) || 0) + k); } return [...n].sort((a, b) => b[1] - a[1]); };
  const recolor = (u, map) => { const s = dec(u); return s ? url(recolorSvg(s, map)) : u; };
  const hsl = h => { const [r, g, b] = [1, 3, 5].map(i => parseInt(h.slice(i, i + 2), 16) / 255), mx = Math.max(r, g, b), mn = Math.min(r, g, b), l = (mx + mn) / 2, d = mx - mn;
    if (!d) return [0, 0, l]; const s = d / (1 - Math.abs(2 * l - 1)); let H = mx === r ? ((g - b) / d) % 6 : mx === g ? (b - r) / d + 2 : (r - g) / d + 4; return [(H * 60 + 360) % 360, s, l]; };
  const hex = (H, s, l) => { const c = (1 - Math.abs(2 * l - 1)) * s, x = c * (1 - Math.abs((H / 60) % 2 - 1)), m = l - c / 2;
    const [r, g, b] = H < 60 ? [c, x, 0] : H < 120 ? [x, c, 0] : H < 180 ? [0, c, x] : H < 240 ? [0, x, c] : H < 300 ? [x, 0, c] : [c, 0, x];
    return '#' + [r, g, b].map(v => Math.round((v + m) * 255).toString(16).padStart(2, '0')).join(''); };

  // ---- operating modes: every graphic in the colour of a mode, with a badge ----
  const MODES = [
    { key: 'manual', label: 'Manual', badge: 'M', color: '#f59e0b' }, { key: 'auto', label: 'Auto', badge: 'A', color: '#0ea5e9' },
    { key: 'disable', label: 'Disable', badge: 'D', color: '#9ca3af' }, { key: 'enable', label: 'Enable', badge: 'E', color: '#22c55e' },
    { key: 'alarm', label: 'Alarm', badge: '!', color: '#ef4444', blink: true }, { key: 'warning', label: 'Warning', badge: 'W', color: '#facc15' },
    { key: 'fault', label: 'Fault', badge: 'F', color: '#b91c1c', blink: true }, { key: 'interlock', label: 'Interlock', badge: 'IL', color: '#a855f7' },
    { key: 'lock', label: 'Lock', badge: 'L', color: '#475569' },
  ];
  const badgeSvg = (s, m, color) => {
    const vb = (/viewBox="([^"]+)"/.exec(s) || [, '0 0 100 100'])[1].split(/[\s,]+/).map(Number), r = Math.max(7, Math.min(vb[2], vb[3]) * .16);
    const cx = vb[0] + vb[2] - r - 1, cy = vb[1] + r + 1, fg = hsl(color)[2] > .62 ? '#111827' : '#ffffff';
    const icon = m.key === 'lock'
      ? `<rect x="${cx - r * .45}" y="${cy - r * .05}" width="${r * .9}" height="${r * .65}" rx="${r * .1}" fill="${fg}"/><path d="M${cx - r * .28} ${cy} v${-r * .22} a${r * .28} ${r * .28} 0 0 1 ${r * .56} 0 v${r * .22}" fill="none" stroke="${fg}" stroke-width="${r * .16}"/>`
      : `<text x="${cx}" y="${cy + r * .06}" font-family="Segoe UI,Arial,sans-serif" font-size="${r * (m.badge.length > 1 ? .95 : 1.25)}" font-weight="800" fill="${fg}" text-anchor="middle" dominant-baseline="middle">${m.badge}</text>`;
    return s.replace(/<\/svg>\s*$/, `<circle cx="${cx}" cy="${cy}" r="${r}" fill="${color}" stroke="#ffffff" stroke-width="${r * .14}"/>${icon}</svg>`);
  };
  // The parts that change between the off and the on image take the mode
  // colour (light / dark shades keep their offset); without such parts the
  // graphic gets a frame in the mode colour.
  const modeImage = (offSvg, onSvg, m, color, badge) => {
    // A colour the on image uses more often than the off image (an LED's
    // colour is in both, but lights up only when on).
    const off = colorsOfSvg(offSvg), diff = [...colorsOfSvg(onSvg)].filter(([c, n]) => n > (off.get(c) || 0)).map(([c]) => c);
    let s = onSvg;
    if (diff.length) {
      const sat = c => { const [, S, L] = hsl(c); return S * (1 - Math.abs(2 * L - 1)); };
      const main = hsl(diff.reduce((a, b) => sat(b) > sat(a) ? b : a)), [mh, ms, ml] = hsl(color), map = {};
      for (const c of diff) { const [, S, L] = hsl(c); map[c] = hex(mh, S < .1 ? S : ms, Math.min(.97, Math.max(.04, ml + (L - main[2])))); }
      s = recolorSvg(onSvg, map);
    } else {
      const vb = (/viewBox="([^"]+)"/.exec(s) || [, '0 0 100 100'])[1].split(/[\s,]+/).map(Number), w = Math.max(2, Math.min(vb[2], vb[3]) * .04);
      s = s.replace(/<\/svg>\s*$/, `<rect x="${vb[0] + w / 2}" y="${vb[1] + w / 2}" width="${vb[2] - w}" height="${vb[3] - w}" rx="${w * 2}" fill="none" stroke="${color}" stroke-width="${w}"/></svg>`);
    }
    return badge ? badgeSvg(s, m, color) : s;
  };
  // modeStates(item, colors, badge): Off (value 0) and one state per mode
  // (values 1…9), built from the item's first (off) and second (on) image.
  const modeStates = (item, colors = {}, badge = true) => {
    const ss = item.states(), offSvg = dec(ss[0].image), onSvg = dec((ss[1] || ss[0]).image);
    return [{ label: ss[0].label, when: E(0), image: ss[0].image },
      ...MODES.map((m, i) => ({ label: m.label, when: E(i + 1), image: url(modeImage(offSvg, onSvg, m, colors[m.key] || m.color, badge)), ...(m.blink ? { blink: true } : {}) }))];
  };
  // ---- restyle: per colour a fill (colour, none, pattern …) and transparency;
  // for the whole image line thickness, brightness, glow and transparency ----
  const PATTERNS = {
    diag: ['Diagonal /', (s, w, c) => `<path d="M0 ${s} L${s} 0 M${-s / 4} ${s / 4} L${s / 4} ${-s / 4} M${s * .75} ${s * 1.25} L${s * 1.25} ${s * .75}" stroke="${c}" stroke-width="${w}"/>`],
    back: ['Diagonal \\', (s, w, c) => `<path d="M0 0 L${s} ${s} M${s * .75} ${-s / 4} L${s * 1.25} ${s / 4} M${-s / 4} ${s * .75} L${s / 4} ${s * 1.25}" stroke="${c}" stroke-width="${w}"/>`],
    cross: ['Cross-hatch ×', (s, w, c) => PATTERNS.diag[1](s, w, c) + PATTERNS.back[1](s, w, c)],
    horiz: ['Horizontal lines', (s, w, c) => `<path d="M0 ${s / 2} H${s}" stroke="${c}" stroke-width="${w}"/>`],
    vert: ['Vertical lines', (s, w, c) => `<path d="M${s / 2} 0 V${s}" stroke="${c}" stroke-width="${w}"/>`],
    grid: ['Grid +', (s, w, c) => `<path d="M0 ${s / 2} H${s} M${s / 2} 0 V${s}" stroke="${c}" stroke-width="${w}"/>`],
    dots: ['Dots', (s, w, c) => `<circle cx="${s / 2}" cy="${s / 2}" r="${Math.max(.5, w)}" fill="${c}"/>`],
    checker: ['Checker', (s, w, c) => `<rect width="${s / 2}" height="${s / 2}" fill="${c}"/><rect x="${s / 2}" y="${s / 2}" width="${s / 2}" height="${s / 2}" fill="${c}"/>`],
    zigzag: ['Zigzag', (s, w, c) => `<path d="M0 ${s * .7} L${s / 2} ${s * .3} L${s} ${s * .7}" fill="none" stroke="${c}" stroke-width="${w}"/>`],
  };
  const GRAD = /<(radialGradient|linearGradient)\b[^>]*\bid="([^"]+)"[^>]*>[\s\S]*?<\/\1>/g;
  const isHalo = g => { const st = g.match(/<stop\b[^>]*>/g) || []; return st.length > 0 && st.every(x => { const o = /stop-opacity\s*=\s*"([\d.]+)"/.exec(x); return o && +o[1] < 1; }); };
  const hasHalo = u => { const s = dec(u); return !!s && [...s.matchAll(GRAD)].some(m => isHalo(m[0]) && s.includes(`url(#${m[2]})`)); };
  const alpha = (c, a) => a >= 1 ? c : c + Math.round(Math.max(0, a) * 255).toString(16).padStart(2, '0');
  // rules: {#rrggbb: {mode: color|none|pattern|colorpattern|patterncolor|keep, color, line, pattern, alpha}}
  // fx: {stroke, patSize, patWidth, bright, glow, glowColor, opacity}
  const restyleSvg = (s, rules, fx = {}, uid = 'q' + Math.random().toString(36).slice(2, 7)) => {
    const defs = [], pat = {}, size = fx.patSize || 8, pw = fx.patWidth || 1.5;
    const patFor = (old, r) => {
      if (pat[old]) return pat[old];
      const id = `${uid}p${Object.keys(pat).length}`, a = r.alpha ?? 1;
      const bg = r.mode === 'pattern' ? alpha(old, a) : r.mode === 'colorpattern' ? alpha(r.color || old, a) : null;
      const line = r.mode === 'patterncolor' ? alpha(r.color || old, a) : alpha(r.line || '#1f2937', a);
      defs.push(`<pattern id="${id}" width="${size}" height="${size}" patternUnits="userSpaceOnUse">${bg ? `<rect width="${size}" height="${size}" fill="${bg}"/>` : ''}${(PATTERNS[r.pattern] || PATTERNS.diag)[1](size, pw, line)}</pattern>`);
      return (pat[old] = id);
    };
    const value = (attr, h) => {
      const old = norm(h), r = rules[old];
      if (!r || (r.mode === 'keep' && (r.alpha ?? 1) >= 1)) return null;
      const a = r.alpha ?? 1, solid = attr === 'stop-color' || attr === 'flood-color';
      switch (r.mode) {
        case 'keep': return alpha(old, a);
        case 'color': return alpha(r.color || old, a);
        case 'none': return solid ? 'transparent' : 'none';
        default:
          if (solid) return r.mode === 'patterncolor' ? 'transparent' : r.mode === 'pattern' ? alpha(old, a) : alpha(r.color || old, a);
          return `url(#${patFor(old, r)})`;
      }
    };
    // A colour that is only in a gradient (an LED) cannot hold a pattern: the
    // shapes painted with that gradient get the pattern as a layer on top.
    const isPat = r => r && ['pattern', 'colorpattern', 'patterncolor'].includes(r.mode);
    const gradPat = {};
    for (const g of s.matchAll(/<(linearGradient|radialGradient)\b[^>]*\bid="([^"]+)"[^>]*>([\s\S]*?)<\/\1>/g))
      for (const st of g[3].matchAll(/<stop\b[^>]*>/g)) { // solid stops only – not a fading halo
        const c = /stop-color\s*=\s*"(#[0-9a-fA-F]{3,8})"/.exec(st[0]), op = /stop-opacity\s*=\s*"([\d.]+)"/.exec(st[0]);
        if (c && isPat(rules[norm(c[1])]) && (!op || +op[1] >= .9)) { gradPat[g[2]] = norm(c[1]); break; }
      }
    const overlay = {};
    const overlayFor = old => {
      if (overlay[old]) return overlay[old];
      const r = rules[old], id = `${uid}o${Object.keys(overlay).length}`, a = r.alpha ?? 1;
      const line = r.mode === 'patterncolor' ? alpha(r.color || old, a) : alpha(r.line || '#1f2937', a);
      defs.push(`<pattern id="${id}" width="${size}" height="${size}" patternUnits="userSpaceOnUse">${(PATTERNS[r.pattern] || PATTERNS.diag)[1](size, pw, line)}</pattern>`);
      return (overlay[old] = id);
    };
    for (const [gid, old] of Object.entries(gradPat)) {
      const ref = new RegExp(`<(circle|ellipse|rect|path|polygon)\\b[^>]*fill="url\\(#${gid.replace(/[^\w-]/g, '\\$&')}\\)"[^>]*\\/>`, 'g');
      s = s.replace(ref, el => el + el.replace(/fill="url\([^)]*\)"/, `fill="url(#${overlayFor(old)})"`).replace(/\s(stroke|stroke-width|opacity|fill-opacity)="[^"]*"/g, ''));
    }
    // Halo: a soft light around a lamp – a gradient whose every stop fades
    // (stop-opacity < 1). fx.halo scales it; 0 removes it.
    if (fx.halo != null && fx.halo !== 1) {
      const halos = [];
      s = s.replace(GRAD, (m, tag, id) => {
        if (!isHalo(m)) return m;
        halos.push(id);
        return fx.halo <= 0 ? m : m.replace(/stop-opacity\s*=\s*"([\d.]+)"/g, (x, v) => `stop-opacity="${Math.min(1, v * fx.halo).toFixed(3)}"`);
      });
      if (fx.halo <= 0) for (const id of halos)
        s = s.replace(new RegExp(`<(circle|ellipse|rect|path|polygon)\\b[^>]*fill="url\\(#${id.replace(/[^\w-]/g, '\\$&')}\\)"[^>]*\\/>`, 'g'), '');
    }
    s = s.replace(/(fill|stroke|stop-color|flood-color)(\s*=\s*"|\s*:\s*)(#[0-9a-fA-F]{8}|#[0-9a-fA-F]{6}|#[0-9a-fA-F]{3})(?![0-9a-zA-Z])/g, (m, attr, sep, h) => {
      const v = value(attr, h); return v == null ? m : attr + sep + v;
    });
    if (fx.stroke && fx.stroke !== 1) {
      s = s.replace(/stroke-width(\s*=\s*"|\s*:\s*)([\d.]+)/g, (m, sep, v) => 'stroke-width' + sep + +(v * fx.stroke).toFixed(2));
      // A border drawn as a ring – a plain-coloured circle with a smaller one
      // on the same centre right after it (an LED, a pilot lamp) – gets
      // thicker or thinner the same way.
      const num = (el, a) => { const m = new RegExp(`\\s${a}="([-\\d.]+)"`).exec(el); return m ? +m[1] : NaN; };
      const cs = [...s.matchAll(/<circle\b[^>]*\/>/g)].map(m => ({ el: m[0], cx: num(m[0], 'cx'), cy: num(m[0], 'cy'), r: num(m[0], 'r') }));
      const newR = new Map();
      for (let i = 0; i + 1 < cs.length; i++) {
        const o = cs[i], n = cs[i + 1], w = o.r - n.r;
        if (o.cx === n.cx && o.cy === n.cy && w > 0 && w <= o.r * .35 && /\sfill="#[0-9a-fA-F]{3,8}"/.test(o.el)) {
          const p = cs[i - 1], cap = p && p.cx === o.cx && p.cy === o.cy && p.r > o.r ? p.r - 1.5 : Infinity; // stay inside a bezel around it
          newR.set(i, +Math.min(cap, n.r + w * fx.stroke).toFixed(2));
        }
      }
      if (newR.size) { let k = -1; s = s.replace(/<circle\b[^>]*\/>/g, el => { k++; return newR.has(k) ? el.replace(/\sr="[-\d.]+"/, ` r="${newR.get(k)}"`) : el; }); }
    }
    const bright = fx.bright ?? 1, glow = fx.glow || 0, op = fx.opacity ?? 1;
    let open = '', close = '';
    if (bright !== 1 || glow > 0) {
      const lift = bright > 1 ? ((bright - 1) * .25).toFixed(3) : 0, f = k => `<feFunc${k} type="linear" slope="${bright}" intercept="${lift}"/>`;
      const src = bright !== 1 ? 'lit' : 'SourceGraphic';
      defs.push(`<filter id="${uid}f" x="-40%" y="-40%" width="180%" height="180%">` +
        (bright !== 1 ? `<feComponentTransfer in="SourceGraphic" result="lit">${f('R')}${f('G')}${f('B')}</feComponentTransfer>` : '') +
        (glow > 0 ? `<feGaussianBlur in="${src}" stdDeviation="${glow}" result="bl"/><feFlood flood-color="${fx.glowColor || '#fde047'}"/><feComposite in2="bl" operator="in" result="gl"/>` +
          `<feMerge><feMergeNode in="gl"/><feMergeNode in="gl"/><feMergeNode in="${src}"/></feMerge>` : '') + `</filter>`);
      open += `<g filter="url(#${uid}f)">`; close = '</g>' + close;
    }
    if (op < 1) { open = `<g opacity="${op}">` + open; close += '</g>'; }
    if (!defs.length && !open) return s;
    return s.replace(/(<svg\b[^>]*>)([\s\S]*)(<\/svg>\s*)$/, (m, a, b, c) => a + (defs.length ? `<defs>${defs.join('')}</defs>` : '') + open + b + close + c);
  };
  const restyle = (u, rules, fx, uid) => { const s = dec(u); return s ? url(restyleSvg(s, rules, fx, uid)) : u; };
  window.STATE_MODES = { MODES, modeStates, colorsOf, recolor, restyle, PATTERNS, hasHalo, isSvg: u => !!dec(u) };
})();
