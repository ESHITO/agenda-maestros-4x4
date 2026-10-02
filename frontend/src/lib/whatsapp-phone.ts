// Fork (Agenda Maestros 4x4): each person's WhatsApp number, for the notices to the host
// ("nueva sesión agendada" and "faltan 5 minutos", internal/webhook/fork_host.go).
//
// The number is stored by the server in E.164 (fork_member_phones) through
//   GET/PUT /v1/users/me/whatsapp   (anyone signed in: Perfil → «Tu WhatsApp»)
//   PUT /v1/users/{id}/whatsapp     (the owner for anyone; an admin for non-admin members)
// and it MUST carry the country code: without it nobody can tell the country, and
// FunnelChat/wa.me would read its first digits as one. The server is the judge
// (webhook.NormalizePhone + the country table); everything here only helps the person type
// it right and shows it back. Pure functions, tested in whatsapp-phone.test.ts.
//
// The country data is the same phone-data.json the booking page's picker uses, served by
// GET /v1/phone-countries ([ISO, dial] pairs + an IANA zone → ISO map). The helpers below
// mirror booking-logic.js's phoneCombine / phoneFilter / phoneDetectCountry in spirit (that
// file is a framework-free IIFE for the public pages and is not importable here).

import { api } from './api';

/** One [ISO, dial code] pair, "PE" / "51". */
export type PhoneCountry = [iso: string, dial: string];

/** GET /v1/phone-countries. */
export type PhoneData = {
	countries: PhoneCountry[];
	tz2cc: Record<string, string>;
	visitor_country?: string;
};

/** GET/PUT /v1/users/{me|id}/whatsapp: nulls when the person has no number. */
export type MemberWhatsApp = {
	user_id: string;
	phone: string | null; // E.164, "+51987654321"
	whatsapp: string | null; // digits, "51987654321"
	country: string | null; // ISO, "PE"
	country_name: string | null; // "Perú"
	updated_at: string | null;
};

/** The countries pinned at the top of the picker: where most of the team and clients are. */
export const PINNED_COUNTRIES = ['PE', 'MX', 'CO', 'AR', 'CL', 'EC', 'BO', 'VE', 'ES', 'US'];

/** A dial code several countries share → the one meant when nothing else tells
 *  (internal/webhook/fork_phone_table.go mainCountryForCode, the same choices). */
const MAIN_FOR_CODE: Record<string, string> = {
	'1': 'US',
	'7': 'RU',
	'39': 'IT',
	'44': 'GB',
	'47': 'NO',
	'61': 'AU',
	'212': 'MA',
	'262': 'RE',
	'290': 'SH',
	'358': 'FI',
	'590': 'GP',
	'599': 'CW'
};

/** +1 area codes of the Spanish-speaking NANP countries (the server's exceptions too). */
const NANP_AREAS: Record<string, string> = { '809': 'DO', '829': 'DO', '849': 'DO', '787': 'PR', '939': 'PR' };

/** Dial codes whose numbers keep their leading 0 after the code (see combinePhone). */
const KEEPS_LEADING_ZERO = new Set(['39', '378', '225']);

let regionNames: Intl.DisplayNames | null | undefined;

/** "PE" → "Perú" (Intl, Spanish); the ISO code itself when the browser has no names. */
export function countryName(iso: string | null | undefined): string {
	const code = String(iso ?? '').toUpperCase();
	if (!/^[A-Z]{2}$/.test(code)) return '';
	if (regionNames === undefined) {
		try {
			regionNames = new Intl.DisplayNames(['es'], { type: 'region' });
		} catch {
			regionNames = null;
		}
	}
	try {
		return regionNames?.of(code) || code;
	} catch {
		return code;
	}
}

/** The flag image the server serves for the pickers (flag-icons SVG; AC and TA have none).
 *  An image, not the emoji: Windows shows emoji flags as two letters. */
export function flagUrl(iso: string | null | undefined): string {
	const code = String(iso ?? '').toLowerCase();
	return /^[a-z]{2}$/.test(code) ? `/assets/flags/${code}.svg` : '';
}

/** The digits of s, Arabic/Persian/full-width digits read as 0-9 (as booking-logic.js does). */
function asciiDigits(s: string): string {
	return s.replace(/[٠-٩۰-۹０-９＋]/g, (ch) => {
		const c = ch.charCodeAt(0);
		if (c === 0xff0b) return '+';
		return String(c <= 0x0669 ? c - 0x0660 : c <= 0x06f9 ? c - 0x06f0 : c - 0xff10);
	});
}

/**
 * The string sent to the server: "+<dial><national digits>", or '' when nothing can be sent.
 *
 *   combinePhone('51', '987 654-321')        → '+51987654321'
 *   combinePhone('51', '+34 612 34 56 78')   → '+34612345678'  (a typed '+' carries its own code)
 *   combinePhone('51', '0051 987 654 321')   → '+51987654321'  ('00' is the international prefix)
 *   combinePhone('', '987654321')            → ''              (no country: never bare digits)
 *
 * A national trunk "0" (UK "07700 900123", Perú "0 987…") is dropped after a picked code:
 * WhatsApp numbers do not carry it after the country code. The exceptions keep it: Italy
 * and San Marino (landlines start with 0) and Côte d'Ivoire (every number since 2021).
 */
export function combinePhone(dial: string | null | undefined, raw: string | null | undefined): string {
	const text = asciiDigits(String(raw ?? '')).trim();
	let digits = text.replace(/\D/g, '');
	if (!digits) return '';
	if (text.startsWith('+')) return '+' + digits;
	if (digits.startsWith('00')) return digits.length > 2 ? '+' + digits.slice(2) : '';
	const code = asciiDigits(String(dial ?? '')).replace(/\D/g, '');
	if (!code) return '';
	// The code is never stripped from the national digits ("51 987…" after picking Perú):
	// a Mexican number may start with 52 too. The field shows what will be saved instead.
	if (!KEEPS_LEADING_ZERO.has(code)) digits = digits.replace(/^0+/, '');
	return digits ? '+' + code + digits : '';
}

/** Why the draft cannot be saved yet (Spanish), or '' when it may be sent. The server still
 *  decides; this only spares the obvious round trips. */
export function phoneDraftError(dial: string | null | undefined, raw: string | null | undefined): string {
	const text = asciiDigits(String(raw ?? '')).trim();
	const digits = text.replace(/\D/g, '');
	if (!digits) return 'Escribe el número.';
	if (/\p{L}/u.test(text)) return 'El número solo lleva dígitos (puedes usar espacios o guiones).';
	const e164 = combinePhone(dial, raw);
	if (!e164) return 'Elige el país o escribe el número con su código, por ejemplo +51 987 654 321.';
	const n = e164.length - 1;
	if (n < 6) return 'Ese número es demasiado corto.';
	if (n > 15) return 'Ese número es demasiado largo.';
	return '';
}

/**
 * A non-blocking warning (Spanish) when the person seems to have typed the picked country's
 * code again in front of the number - «51 987 654 321» with Perú picked, which combinePhone
 * deliberately keeps ('+5151987654321'): a very common habit on phones. '' otherwise.
 *
 * Only when the number was typed without '+'/'00' (those carry their own code), its national
 * digits start with the picked code, and they are longer than a national number usually is
 * (more than 10 digits): a real Peruvian number never has 11, and a Puno landline that
 * starts with 51 has 8. The server still saves what was typed; this only asks.
 */
export function doubledCodeHint(dial: string | null | undefined, raw: string | null | undefined): string {
	const text = asciiDigits(String(raw ?? '')).trim();
	if (text.startsWith('+')) return '';
	let digits = text.replace(/\D/g, '');
	if (!digits || digits.startsWith('00')) return '';
	const code = asciiDigits(String(dial ?? '')).replace(/\D/g, '');
	if (!code) return '';
	if (!KEEPS_LEADING_ZERO.has(code)) digits = digits.replace(/^0+/, '');
	if (!digits.startsWith(code) || digits.length <= 10) return '';
	return `¿Pusiste el código +${code} dos veces? Escribe el número sin el ${code} o con «+» delante.`;
}

/**
 * Splits an E.164 number by its country code: the longest dial code of the list that
 * starts it. A code several countries share (+1, +7, +44…) is told apart by the area codes
 * of República Dominicana and Puerto Rico (+1 809/829/849, 787/939), then by `preferIso`
 * (the country the server worked out, from the person's zone) when it uses that code, else
 * the main country of the code. null when no code matches.
 */
export function splitPhone(
	e164: string | null | undefined,
	countries: PhoneCountry[],
	preferIso?: string | null
): { iso: string; dial: string; national: string } | null {
	const digits = String(e164 ?? '').replace(/\D/g, '');
	if (!digits) return null;
	for (let len = 3; len >= 1; len--) {
		const code = digits.slice(0, len);
		if (code.length < len) continue;
		const isos = countries.filter((c) => c[1] === code).map((c) => c[0]);
		if (isos.length === 0) continue;
		const prefer = String(preferIso ?? '').toUpperCase();
		const nanp = code === '1' ? NANP_AREAS[digits.slice(1, 4)] : undefined;
		const iso =
			nanp && isos.includes(nanp)
				? nanp
				: isos.includes(prefer)
					? prefer
					: MAIN_FOR_CODE[code] && isos.includes(MAIN_FOR_CODE[code])
						? MAIN_FOR_CODE[code]
						: isos[0];
		return { iso, dial: code, national: digits.slice(len) };
	}
	return null;
}

/** "987654321" → "987 654 321": groups of three, a lone last digit joining the group before. */
export function groupNational(national: string): string {
	const d = national.replace(/\D/g, '');
	const groups: string[] = [];
	for (let i = 0; i < d.length; i += 3) groups.push(d.slice(i, i + 3));
	if (groups.length > 1 && groups[groups.length - 1].length === 1) {
		const last = groups.pop()!;
		groups[groups.length - 1] += last;
	}
	return groups.join(' ');
}

/** "+51987654321" → "+51 987 654 321" (as is when no code of the list matches). */
export function formatPhone(e164: string | null | undefined, countries: PhoneCountry[], preferIso?: string | null): string {
	const s = String(e164 ?? '').trim();
	if (!s) return '';
	const parts = splitPhone(s, countries, preferIso);
	if (!parts) return s;
	return `+${parts.dial} ${groupNational(parts.national)}`.trim();
}

/** The wa.me link of a number, to open a chat and check it is the right one. */
export function waMeUrl(e164OrDigits: string | null | undefined): string {
	const d = String(e164OrDigits ?? '').replace(/\D/g, '');
	return d ? `https://wa.me/${d}` : '';
}

/** The country the picker starts on when the person has no number yet: their profile zone
 *  in tz2cc (a stored "UTC" names none), else the visitor's country the server guessed,
 *  else ''. Only a country the list offers. */
export function guessCountry(
	zone: string | null | undefined,
	data: Pick<PhoneData, 'countries' | 'tz2cc' | 'visitor_country'>
): string {
	const known = new Set(data.countries.map((c) => c[0]));
	const z = String(zone ?? '');
	if (z && z !== 'UTC' && z !== 'Etc/UTC') {
		const byZone = String(data.tz2cc?.[z] ?? '').toUpperCase();
		if (known.has(byZone)) return byZone;
	}
	const hint = String(data.visitor_country ?? '').toUpperCase();
	return known.has(hint) ? hint : '';
}

function fold(s: string): string {
	return s
		.toLowerCase()
		.normalize('NFD')
		.replace(/[̀-ͯ]/g, '')
		.replace(/\s+/g, ' ')
		.trim();
}

/** The picker's order: `pinned` first (in that order), then the rest by Spanish name. */
export function sortCountries(list: PhoneCountry[], pinned: string[] = PINNED_COUNTRIES, nameOf = countryName): PhoneCountry[] {
	const byIso = new Map(list.map((c) => [c[0], c] as const));
	const head: PhoneCountry[] = [];
	for (const p of pinned) {
		const c = byIso.get(p);
		if (c && !head.includes(c)) head.push(c);
	}
	const collator = new Intl.Collator('es');
	const names = new Map(list.map((c) => [c[0], nameOf(c[0])] as const));
	const rest = list
		.filter((c) => !head.includes(c))
		.sort((a, b) => collator.compare(names.get(a[0]) ?? a[0], names.get(b[0]) ?? b[0]));
	return [...head, ...rest];
}

/**
 * The entries matching the picker's search box. Letters: the Spanish name, ignoring case and
 * accents ("peru" finds Perú), or the exact ISO code; digits (an optional '+', spaces): the
 * dial code ("+34" and "34" find España; a whole pasted number finds the code it starts
 * with). Exact matches first, then prefixes, then the rest, each keeping `list`'s order.
 */
export function filterCountries(list: PhoneCountry[], query: string, nameOf = countryName): PhoneCountry[] {
	const q = fold(String(query ?? ''));
	if (!q) return list.slice();
	const groups: PhoneCountry[][] = [[], [], []];
	if (/^\+?[\d\s().-]*$/.test(q)) {
		const qd = q.replace(/\D/g, '');
		if (!qd) return list.slice();
		for (const c of list) {
			if (c[1] === qd) groups[0].push(c);
			else if (c[1].startsWith(qd)) groups[1].push(c);
			else if (qd.startsWith(c[1])) groups[2].push(c);
		}
	} else {
		const iso = q.toUpperCase();
		for (const c of list) {
			const name = fold(nameOf(c[0]) || c[0]);
			if (c[0] === iso || name === q) groups[0].push(c);
			else if (name.startsWith(q) || name.includes(' ' + q)) groups[1].push(c);
			else if (name.includes(q)) groups[2].push(c);
		}
	}
	return [...groups[0], ...groups[1], ...groups[2]];
}

/**
 * Whose time the host notices will show (the server's rule, webhook HostDateTexts): the
 * person's profile zone, else - while it says UTC - the main zone of their number's country.
 */
export function noticeClockHint(zone: string | null | undefined, countryLabel: string | null | undefined, self: boolean): string {
	const z = String(zone ?? '');
	const tus = self ? 'tu' : 'su';
	if (z && z !== 'UTC' && z !== 'Etc/UTC') {
		return `Los avisos ${self ? 'te' : 'le'} muestran la fecha y la hora en ${tus} zona horaria (${z.replaceAll('_', ' ')}).`;
	}
	if (countryLabel) {
		return `Como ${tus} zona horaria dice UTC, los avisos usarán la hora de ${countryLabel}.`;
	}
	return `Como ${tus} zona horaria dice UTC, los avisos usarán la hora universal (UTC).`;
}

let phoneDataPromise: Promise<PhoneData> | null = null;

/** GET /v1/phone-countries, once per page load (a failure is retried on the next call). */
export function loadPhoneData(): Promise<PhoneData> {
	if (!phoneDataPromise) {
		phoneDataPromise = api.get<PhoneData>('/v1/phone-countries').then(
			(d) => ({
				countries: (d?.countries ?? []).filter(
					(c): c is PhoneCountry => Array.isArray(c) && typeof c[0] === 'string' && typeof c[1] === 'string'
				),
				tz2cc: d?.tz2cc ?? {},
				visitor_country: d?.visitor_country ?? ''
			}),
			(e) => {
				phoneDataPromise = null;
				throw e;
			}
		);
	}
	return phoneDataPromise;
}

/** PUT body helper: the API answers like GET. */
export const memberWhatsAppApi = {
	mine: () => api.get<MemberWhatsApp>('/v1/users/me/whatsapp'),
	putMine: (phone: string | null) => api.put<MemberWhatsApp>('/v1/users/me/whatsapp', { phone }),
	putUser: (userId: string, phone: string | null) => api.put<MemberWhatsApp>(`/v1/users/${userId}/whatsapp`, { phone })
};
