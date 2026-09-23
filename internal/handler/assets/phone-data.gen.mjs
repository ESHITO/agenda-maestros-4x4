// Regenerates phone-data.json (not embedded; the go:embed patterns only take the .json and flags/*.svg).
//
//   mkdir tmp && cd tmp && npm i libphonenumber-js flag-icons
//   node ../internal/handler/assets/phone-data.gen.mjs ../internal/handler/assets/phone-data.json
//
// countries = libphonenumber's calling codes. tz2cc = the zones ICU lists for each of THOSE
// countries only. Walking every AA..ZZ code instead lets withdrawn codes ICU still knows win
// zones alphabetically (DD, East Germany, took Europe/Berlin from DE), and the script now
// refuses to write a map that names any country outside the list.

import fs from 'node:fs';
import { getCountries, getCountryCallingCode } from 'libphonenumber-js';

// Solo los países con código telefónico vigente: así ningún código retirado (DD, CS, VD…)
// puede quedarse con una zona. El bug de la primera versión fue recorrer AA..ZZ, donde el
// ICU todavía reconoce códigos históricos y DD (Alemania del Este) ganaba a DE por orden.
const countries = getCountries().map((cc) => [cc, getCountryCallingCode(cc)]);
const tz2cc = {};
for (const [cc] of countries) {
  let zones = [];
  try { const l = new Intl.Locale('und-' + cc); zones = l.getTimeZones?.() || l.timeZones || []; } catch {}
  for (const z of zones) if (!tz2cc[z]) tz2cc[z] = cc;
}
// Alias modernos que el ICU guarda con el nombre antiguo (Firefox/Safari usan el moderno).
const aliases = {
  'Asia/Kolkata': 'Asia/Calcutta', 'Asia/Ho_Chi_Minh': 'Asia/Saigon', 'Asia/Kathmandu': 'Asia/Katmandu',
  'Asia/Yangon': 'Asia/Rangoon', 'America/Argentina/Buenos_Aires': 'America/Buenos_Aires',
  'America/Argentina/Cordoba': 'America/Cordoba', 'America/Argentina/Mendoza': 'America/Mendoza',
  'America/Argentina/Jujuy': 'America/Jujuy', 'America/Argentina/Catamarca': 'America/Catamarca',
  'America/Indiana/Indianapolis': 'America/Indianapolis', 'America/Kentucky/Louisville': 'America/Louisville',
  'America/Nuuk': 'America/Godthab', 'America/Atikokan': 'America/Coral_Harbour',
  'Atlantic/Faroe': 'Atlantic/Faeroe', 'Europe/Kyiv': 'Europe/Kiev', 'Pacific/Chuuk': 'Pacific/Truk',
  'Pacific/Pohnpei': 'Pacific/Ponape', 'Pacific/Kanton': 'Pacific/Enderbury', 'Africa/Asmara': 'Africa/Asmera',
};
for (const [modern, legacy] of Object.entries(aliases)) if (tz2cc[legacy] && !tz2cc[modern]) tz2cc[modern] = tz2cc[legacy];

const known = new Set(countries.map((c) => c[0]));
const invalid = Object.entries(tz2cc).filter(([, c]) => !known.has(c));
if (invalid.length) { console.error('ERROR: códigos fuera de la lista:', invalid); process.exit(1); }

const dest = process.argv[2];
fs.writeFileSync(dest, JSON.stringify({ countries, tz2cc }));
console.log('países:', countries.length, '| zonas:', Object.keys(tz2cc).length, '| códigos inválidos:', invalid.length);
for (const z of ['Europe/Berlin','Europe/Belgrade','Asia/Ho_Chi_Minh','Asia/Yangon','America/Curacao','Africa/Harare','Asia/Aden','Pacific/Efate','America/Lima','Asia/Kolkata']) console.log('  ' + z.padEnd(22) + ' → ' + tz2cc[z]);
