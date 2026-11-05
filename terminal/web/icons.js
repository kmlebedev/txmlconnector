// icons.js — inline SVG icon set (no external assets, currentColor strokes).

const P = {
  spark:
    '<path d="M12 2.6l1.9 5.6 5.6 1.9-5.6 1.9L12 17.6l-1.9-5.6L4.5 10.1l5.6-1.9L12 2.6z" fill="currentColor" stroke="none"/>' +
    '<path d="M18.6 15.2l.9 2.5 2.5.9-2.5.9-.9 2.5-.9-2.5-2.5-.9 2.5-.9.9-2.5z" fill="currentColor" stroke="none" opacity=".7"/>',
  search: '<circle cx="11" cy="11" r="6.5"/><path d="M15.8 15.8L21 21"/>',
  star: '<path d="M12 3.6l2.6 5.3 5.9.8-4.3 4.1 1 5.8-5.2-2.8-5.2 2.8 1-5.8-4.3-4.1 5.9-.8L12 3.6z"/>',
  starFilled:
    '<path d="M12 3.6l2.6 5.3 5.9.8-4.3 4.1 1 5.8-5.2-2.8-5.2 2.8 1-5.8-4.3-4.1 5.9-.8L12 3.6z" fill="currentColor" stroke="none"/>',
  wallet:
    '<rect x="3.2" y="6.2" width="17.6" height="13.2" rx="2.6"/><path d="M3.2 9.8h17.6"/>' +
    '<path d="M16.4 13.4h2.2" stroke-width="2.4"/>',
  candles:
    '<path d="M7 4.5v3M7 16.5v3M17 4.5v3M17 16.5v3"/>' +
    '<rect x="5" y="7.5" width="4" height="9" rx="1"/><rect x="15" y="7.5" width="4" height="6.5" rx="1"/>',
  bulb:
    '<path d="M9 17.2h6M10 20.2h4M12 3.2a6 6 0 00-3.4 10.9c.5.4.8 1 .8 1.6v.5h5.2v-.5c0-.6.3-1.2.8-1.6A6 6 0 0012 3.2z"/>',
  calendar:
    '<rect x="3.4" y="5.2" width="17.2" height="15.4" rx="2.4"/><path d="M3.4 9.8h17.2M8 3.4v3.4M16 3.4v3.4"/>' +
    '<path d="M7.6 13.2h2M11 13.2h2M14.4 13.2h2M7.6 16.8h2M11 16.8h2"/>',
  bell:
    '<path d="M6.2 16.4V11a5.8 5.8 0 1111.6 0v5.4l1.6 2.2H4.6l1.6-2.2z"/><path d="M10 21a2.2 2.2 0 004 0"/>',
  mail:
    '<rect x="3.2" y="5.4" width="17.6" height="13.2" rx="2.4"/><path d="M3.8 7.2l8.2 6 8.2-6"/>',
  globe:
    '<circle cx="12" cy="12" r="8.6"/><path d="M3.4 12h17.2M12 3.4c2.4 2.4 3.6 5.3 3.6 8.6s-1.2 6.2-3.6 8.6c-2.4-2.4-3.6-5.3-3.6-8.6S9.6 5.8 12 3.4z"/>',
  chat:
    '<path d="M20.4 12.2c0 4-3.8 7.2-8.4 7.2-1 0-2-.1-2.9-.4L4.2 20.6l1.2-4A6.6 6.6 0 013.6 12.2C3.6 8.2 7.4 5 12 5s8.4 3.2 8.4 7.2z"/>',
  graduation:
    '<path d="M2.8 9.2L12 4.6l9.2 4.6L12 13.8 2.8 9.2z"/><path d="M6.4 11.2v5.2c0 1.5 2.5 2.8 5.6 2.8s5.6-1.3 5.6-2.8v-5.2"/>' +
    '<path d="M21.2 9.4v5.2"/>',
  help:
    '<circle cx="12" cy="12" r="8.6"/><path d="M9.6 9.3a2.5 2.5 0 114.2 1.9c-.9.7-1.8 1.2-1.8 2.4"/>' +
    '<path d="M12 16.9v.2" stroke-width="2.4"/>',
  plus: '<path d="M12 5.4v13.2M5.4 12h13.2"/>',
  close: '<path d="M6.2 6.2l11.6 11.6M17.8 6.2L6.2 17.8"/>',
  chevronDown: '<path d="M6.4 9.4l5.6 5.6 5.6-5.6"/>',
  chevronRight: '<path d="M9.4 6.4l5.6 5.6-5.6 5.6"/>',
  dots: '<circle cx="12" cy="5.6" r="1.6" fill="currentColor" stroke="none"/><circle cx="12" cy="12" r="1.6" fill="currentColor" stroke="none"/><circle cx="12" cy="18.4" r="1.6" fill="currentColor" stroke="none"/>',
  refresh: '<path d="M20 12a8 8 0 10-2.3 5.6"/><path d="M20.4 5.6v5h-5"/>',
  sliders:
    '<path d="M5 7.4h14M5 16.6h14"/><circle cx="9.2" cy="7.4" r="2.2" fill="var(--panel, #fff)"/>' +
    '<circle cx="15.4" cy="16.6" r="2.2" fill="var(--panel, #fff)"/>',
  grid:
    '<rect x="4" y="4" width="6.6" height="6.6" rx="1.4"/><rect x="13.4" y="4" width="6.6" height="6.6" rx="1.4"/>' +
    '<rect x="4" y="13.4" width="6.6" height="6.6" rx="1.4"/><rect x="13.4" y="13.4" width="6.6" height="6.6" rx="1.4"/>',
  lineChart:
    '<path d="M3.8 19.2h16.4"/><path d="M4.6 15.4l4.2-5 3.6 3 2.8-4.2 4.2 2.4"/>',
  barChart: '<path d="M4.6 19.4V13M9.6 19.4V7.4M14.6 19.4v-4M19.4 19.4V9.2"/>',
  expand: '<path d="M8.6 4.4H4.4v4.2M15.4 4.4h4.2v4.2M19.6 15.4v4.2h-4.2M4.4 15.4v4.2h4.2"/>',
  send: '<path d="M12 19.4V5.2M6.4 10.8L12 5.2l5.6 5.6"/>',
  play: '<path d="M8.4 5.6l10 6.4-10 6.4V5.6z" fill="currentColor" stroke="none"/>',
  stop: '<rect x="6.4" y="6.4" width="11.2" height="11.2" rx="1.8" fill="currentColor" stroke="none"/>',
  warning: '<path d="M12 3.8l8.8 15.4H3.2L12 3.8z"/><path d="M12 9.2v4.4M12 16.6v.2" stroke-width="2.2"/>',
  check: '<path d="M5 12.6l4.6 4.4L19 7.2"/>',
  shield: '<path d="M12 3.4l7.4 2.8v5.2c0 4.2-2.8 7.2-7.4 9.2-4.6-2-7.4-5-7.4-9.2V6.2L12 3.4z"/><path d="M9.2 12l2 2 3.6-3.8"/>',
  clock: '<circle cx="12" cy="12" r="8.4"/><path d="M12 7.2V12l3.2 2"/>',
  info: '<circle cx="12" cy="12" r="8.6"/><path d="M12 10.8v5.4M12 7.8v.2" stroke-width="2.2"/>',
  bolt: '<path d="M13.2 2.8L5.6 13.4h5l-1.6 7.8 7.8-10.8h-5l1.4-7.6z"/>',
  eye: '<path d="M2.8 12S6.2 5.8 12 5.8 21.2 12 21.2 12 17.8 18.2 12 18.2 2.8 12 2.8 12z"/><circle cx="12" cy="12" r="2.8"/>',
  trash: '<path d="M4.8 6.8h14.4M9.2 6.8V4.6h5.6v2.2M7 6.8l.9 12.4h8.2L17 6.8"/>',
};

export function icon(name, size = 18, cls = '') {
  const body = P[name];
  if (!body) return '';
  return `<svg class="icon ${cls}" width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" ` +
    `stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" ` +
    `aria-hidden="true" focusable="false">${body}</svg>`;
}

/** The AI Terminal logo mark: a spark on a rounded dark tile. */
export function logoSvg(size = 26) {
  return `<svg width="${size}" height="${size}" viewBox="0 0 32 32" aria-hidden="true" focusable="false">` +
    `<rect x="1" y="1" width="30" height="30" rx="9" fill="#23262b"/>` +
    `<path d="M16 6.2l2.3 6.8 6.8 2.3-6.8 2.3L16 24.4l-2.3-6.8-6.8-2.3 6.8-2.3L16 6.2z" fill="#ffcf33"/>` +
    `<circle cx="25" cy="25" r="2.1" fill="#0f9d6b"/>` +
    `</svg>`;
}

/** Yellow spark used on the AI button / assistant avatar. */
export function sparkSvg(size = 16, cls = '') {
  return `<svg class="icon ${cls}" width="${size}" height="${size}" viewBox="0 0 24 24" ` +
    `fill="currentColor" aria-hidden="true" focusable="false">` +
    `<path d="M11.4 2.4l1.8 5.4 5.4 1.8-5.4 1.8-1.8 5.4-1.8-5.4-5.4-1.8 5.4-1.8 1.8-5.4z"/>` +
    `<path d="M18.4 14.6l.9 2.4 2.4.9-2.4.9-.9 2.4-.9-2.4-2.4-.9 2.4-.9.9-2.4z" opacity=".65"/>` +
    `</svg>`;
}
