// 16px stroke icons on the text's optical centre. Paths only; the Icon
// component and the markdown renderer both draw from this map.
export const ICONS = {
  x: 'M4 4l8 8M12 4l-8 8', plus: 'M8 3.5v9M3.5 8h9', check: 'M3 8.5 6.5 12 13 4.5', open: 'M6 3.5h6.5V10M12.5 3.5 4 12',
  ship: 'M3 8.5 6.5 12 13 4.5', cancel: 'M4 4l8 8M12 4l-8 8', comment: 'M2.5 3.5h11v7h-6l-3 2.5v-2.5h-2z',
  chevL: 'M10 3 5 8l5 5', chevR: 'M6 3l5 5-5 5', chevD: 'M4 6l4 4 4-4', copy: ['M6 6h7v7H6z', 'M3 10V3h7'],
  link: ['M6.5 9.5 9.5 6.5', 'M7 4.5 8.5 3a2.5 2.5 0 0 1 3.5 3.5L10.5 8', 'M9 11.5 7.5 13A2.5 2.5 0 0 1 4 9.5L5.5 8'],
  bold: ['M5 3h4a2.5 2.5 0 0 1 0 5H5z', 'M5 8h4.5a2.5 2.5 0 0 1 0 5H5z'], italic: ['M7 3h5M4 13h5M9.5 3l-3 10'],
  strike: ['M3 8h10', 'M5.5 5.5c0-1.5 1.2-2.5 3-2.5 1.4 0 2.3.6 2.7 1.5', 'M10.5 10.5c0 1.5-1.4 2.5-3 2.5-1.5 0-2.6-.7-3-1.8'],
  code: ['M5.5 4.5 2 8l3.5 3.5', 'M10.5 4.5 14 8l-3.5 3.5'], codeblock: ['M2.5 3.5h11v9h-11z', 'M6 6.5 4.5 8 6 9.5', 'M10 6.5 11.5 8 10 9.5'],
  quote: ['M4 10.5c-1 0-1.5-.7-1.5-1.5V5.5h3v3.5c0 1-.5 1.5-1.5 1.5z', 'M11 10.5c-1 0-1.5-.7-1.5-1.5V5.5h3v3.5c0 1-.5 1.5-1.5 1.5z'],
  ul: ['M6 4h7M6 8h7M6 12h7', 'M3 4h.01M3 8h.01M3 12h.01'], ol: ['M6 4h7M6 8h7M6 12h7', 'M2.5 3.5h1v2M2.5 8h1.5l-1.5 1.5h1.5M2.5 11.5h1.5v1h-1.5v1h1.5'],
  task: ['M6 4h7M6 8h7M6 12h7', 'M2 8l1 1 2-2', 'M2 3.5h2v2H2zM2 11h2v2H2z'], h: ['M3 3v10M13 3v10M3 8h10'],
  image: ['M2.5 3.5h11v9h-11z', 'M5.5 7a1 1 0 1 0 0-.01', 'M2.5 11l3-3 2.5 2.5 2-2 3.5 3.5'], blocked: ['M8 2a6 6 0 1 0 0 12A6 6 0 0 0 8 2z', 'M3.8 3.8l8.4 8.4'],
  calendar: ['M2.5 4.5h11v9h-11z', 'M2.5 7.5h11', 'M5.5 3v3M10.5 3v3'], trash: ['M3 4.5h10', 'M6.5 4.5V3h3v1.5', 'M4.5 4.5l.5 8.5h6l.5-8.5'],
  eye: ['M1.5 8s2.5-4.5 6.5-4.5S14.5 8 14.5 8s-2.5 4.5-6.5 4.5S1.5 8 1.5 8z', 'M8 9.5a1.5 1.5 0 1 0 0-3 1.5 1.5 0 0 0 0 3z'], pencil: ['M11.5 2.5l2 2-8 8H3.5v-2z'],
  sun: ['M8 11a3 3 0 1 0 0-6 3 3 0 0 0 0 6z', 'M8 1.5v1.5M8 13v1.5M1.5 8H3M13 8h1.5M3.4 3.4l1 1M11.6 11.6l1 1M3.4 12.6l1-1M11.6 4.4l1-1'],
  grip: ['M6 4h.01M10 4h.01M6 8h.01M10 8h.01M6 12h.01M10 12h.01'],
  minus: 'M3.5 8h9', lock: ['M3.5 7.5h9v6h-9z', 'M5.5 7.5V5a2.5 2.5 0 0 1 5 0v2.5'],
  sparkle: ['M6.5 2.5 7.6 5.4 10.5 6.5 7.6 7.6 6.5 10.5 5.4 7.6 2.5 6.5 5.4 5.4z', 'M11.5 9.5l.6 1.4 1.4.6-1.4.6-.6 1.4-.6-1.4-1.4-.6 1.4-.6z'],
  gear: ['M8 5.9a2.1 2.1 0 1 0 0 4.2 2.1 2.1 0 0 0 0-4.2z', 'M8 1.7l.9 1.6 1.8-.4.6 1.8 1.8.6-.4 1.8L13.9 8l-1.2 1.4.4 1.8-1.8.6-.6 1.8-1.8-.4L8 14.3l-.9-1.6-1.8.4-.6-1.8-1.8-.6.4-1.8L2.1 8l1.2-1.4-.4-1.8 1.8-.6.6-1.8 1.8.4z'],
  upload: ['M8 11V3', 'M5 6l3-3 3 3', 'M2.5 11v2h11v-2'],
  sync: ['M13.5 8a5.5 5.5 0 0 1-9.6 3.6', 'M2.5 8a5.5 5.5 0 0 1 9.6-3.6', 'M2.5 4.8v3.4h3.4', 'M13.5 11.2V7.8h-3.4'],
  alert: ['M8 2.6 14.4 13.2H1.6z', 'M8 6.4v3', 'M8 11.3h.01'],
  search: ['M7 7m-4.5 0a4.5 4.5 0 1 0 9 0a4.5 4.5 0 1 0-9 0', 'M10.5 10.5 14 14'],
  filter: 'M2 4h12M4 8h8M6 12h4', terminal: 'M3 5.5 6 8l-3 2.5M8 11h5',
  help: ['M8 8m-6 0a6 6 0 1 0 12 0a6 6 0 1 0-12 0', 'M6.2 6.4a1.9 1.9 0 1 1 2.6 1.8c-.5.2-.8.5-.8 1v.3', 'M8 11.3h.01'],
  sliders: ['M2.5 4.5h5M10.5 4.5h3M2.5 8h9M13.5 8h0M2.5 11.5h2M7.5 11.5h6', 'M8.5 4.5m-1.5 0a1.5 1.5 0 1 0 3 0a1.5 1.5 0 1 0-3 0', 'M12.5 8m-1.5 0a1.5 1.5 0 1 0 3 0a1.5 1.5 0 1 0-3 0', 'M5.5 11.5m-1.5 0a1.5 1.5 0 1 0 3 0a1.5 1.5 0 1 0-3 0'],
};
export const iconPaths = (name) => { const d = ICONS[name]; return Array.isArray(d) ? d : [d]; };
// The same icon as markup, for HTML the markdown renderer assembles.
export function iconSvg(name, size = 16) {
  return `<svg width="${size}" height="${size}" viewBox="0 0 16 16" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">${iconPaths(name).map((p) => `<path d="${p}"></path>`).join('')}</svg>`;
}
