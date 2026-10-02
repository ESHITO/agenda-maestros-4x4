import { describe, expect, it } from 'vitest';
import {
	combinePhone,
	countryName,
	doubledCodeHint,
	filterCountries,
	flagUrl,
	formatPhone,
	groupNational,
	guessCountry,
	noticeClockHint,
	phoneDraftError,
	sortCountries,
	splitPhone,
	waMeUrl,
	type PhoneCountry
} from './whatsapp-phone';

// A slice of phone-data.json, shared codes included.
const COUNTRIES: PhoneCountry[] = [
	['AR', '54'],
	['CA', '1'],
	['CI', '225'],
	['CO', '57'],
	['DO', '1'],
	['ES', '34'],
	['GB', '44'],
	['IT', '39'],
	['JE', '44'],
	['KZ', '7'],
	['MX', '52'],
	['PE', '51'],
	['PR', '1'],
	['RU', '7'],
	['US', '1'],
	['VA', '39']
];

describe('combinePhone: what is sent to the server', () => {
	it('the picked code plus the national digits, separators dropped', () => {
		expect(combinePhone('51', '987 654-321')).toBe('+51987654321');
	});

	it('a typed "+" carries its own code and wins over the picker', () => {
		expect(combinePhone('51', '+34 612 34 56 78')).toBe('+34612345678');
		expect(combinePhone('', '+51 987 654 321')).toBe('+51987654321');
	});

	it('"00" is the international prefix', () => {
		expect(combinePhone('51', '0051 987 654 321')).toBe('+51987654321');
		expect(combinePhone('', '00')).toBe('');
	});

	it('no country and no "+": nothing (never bare digits)', () => {
		expect(combinePhone('', '987654321')).toBe('');
		expect(combinePhone(null, '987654321')).toBe('');
	});

	it('no digits: nothing', () => {
		expect(combinePhone('51', '')).toBe('');
		expect(combinePhone('51', ' - ')).toBe('');
		expect(combinePhone('51', undefined)).toBe('');
	});

	it('drops a national trunk 0, except where numbers keep it', () => {
		expect(combinePhone('44', '07700 900123')).toBe('+447700900123');
		expect(combinePhone('39', '06 1234 5678')).toBe('+390612345678');
		expect(combinePhone('225', '07 07 12 34 56')).toBe('+2250707123456');
	});

	it('never strips the code from the national digits (a Mexican number may start with 52)', () => {
		expect(combinePhone('52', '5212345678')).toBe('+525212345678');
	});

	it('reads Arabic-Indic and full-width digits', () => {
		expect(combinePhone('51', '٩٨٧٦٥٤٣٢١')).toBe('+51987654321');
		expect(combinePhone('', '＋５１９８７６５４３２１')).toBe('+51987654321');
	});
});

describe('phoneDraftError', () => {
	it('ok for a whole number', () => {
		expect(phoneDraftError('51', '987 654 321')).toBe('');
		expect(phoneDraftError('', '+51 987 654 321')).toBe('');
	});

	it('asks for the number, the country, digits only, a sane length', () => {
		expect(phoneDraftError('51', '')).toBe('Escribe el número.');
		expect(phoneDraftError('', '987654321')).toMatch(/^Elige el país/);
		expect(phoneDraftError('51', '987 abc 321')).toMatch(/solo lleva dígitos/);
		expect(phoneDraftError('51', '12')).toBe('Ese número es demasiado corto.');
		expect(phoneDraftError('51', '1234567890123456')).toBe('Ese número es demasiado largo.');
	});
});

describe('doubledCodeHint: the picked code typed again (non-blocking)', () => {
	it('warns when the national digits repeat the picked code and are too long', () => {
		const pe = '¿Pusiste el código +51 dos veces? Escribe el número sin el 51 o con «+» delante.';
		expect(doubledCodeHint('51', '51 987 654 321')).toBe(pe);
		expect(doubledCodeHint('51', '051987654321')).toBe(pe); // a trunk 0 is dropped first
		expect(doubledCodeHint('1', '1 305 555 1234')).toContain('¿Pusiste el código +1 dos veces?');
		expect(doubledCodeHint('52', '52 55 1234 5678')).toContain('+52 dos veces');
		expect(doubledCodeHint('54', '54 9 11 2345 6789')).toContain('+54 dos veces');
	});

	it('silent for a normal number, a typed code with + or 00, or a short number starting with the code', () => {
		expect(doubledCodeHint('51', '987 654 321')).toBe('');
		expect(doubledCodeHint('51', '+51 987 654 321')).toBe('');
		expect(doubledCodeHint('51', '0051 987 654 321')).toBe('');
		expect(doubledCodeHint('51', '51 234567')).toBe(''); // a Puno landline (area 51)
		expect(doubledCodeHint('52', '55 1234 5678')).toBe('');
		expect(doubledCodeHint('', '51987654321')).toBe('');
		expect(doubledCodeHint('51', '')).toBe('');
	});

	it('never blocks saving: phoneDraftError still accepts it', () => {
		expect(phoneDraftError('51', '51 987 654 321')).toBe('');
	});
});

describe('splitPhone', () => {
	it('the longest code that starts the number', () => {
		expect(splitPhone('+51987654321', COUNTRIES)).toEqual({ iso: 'PE', dial: '51', national: '987654321' });
		expect(splitPhone('+2250707123456', COUNTRIES)).toEqual({ iso: 'CI', dial: '225', national: '0707123456' });
	});

	it('a shared code: the server country, else the main one', () => {
		expect(splitPhone('+14165550123', COUNTRIES, 'CA')?.iso).toBe('CA');
		expect(splitPhone('+14165550123', COUNTRIES)?.iso).toBe('US');
		expect(splitPhone('+77001234567', COUNTRIES)?.iso).toBe('RU');
		expect(splitPhone('+77001234567', COUNTRIES, 'KZ')?.iso).toBe('KZ');
		expect(splitPhone('+447700900123', COUNTRIES)?.iso).toBe('GB');
		expect(splitPhone('+390612345678', COUNTRIES)?.iso).toBe('IT');
	});

	it('a prefer that does not use the code is ignored', () => {
		expect(splitPhone('+14165550123', COUNTRIES, 'PE')?.iso).toBe('US');
	});

	it('República Dominicana and Puerto Rico by their area codes', () => {
		expect(splitPhone('+18095551234', COUNTRIES)?.iso).toBe('DO');
		expect(splitPhone('+18295551234', COUNTRIES, 'US')?.iso).toBe('DO');
		expect(splitPhone('+17875551234', COUNTRIES)?.iso).toBe('PR');
	});

	it('null when nothing matches', () => {
		expect(splitPhone('', COUNTRIES)).toBeNull();
		expect(splitPhone(null, COUNTRIES)).toBeNull();
		expect(splitPhone('+999123', COUNTRIES)).toBeNull();
	});
});

describe('formatPhone / groupNational', () => {
	it('groups of three, a lone last digit joins the group before', () => {
		expect(groupNational('987654321')).toBe('987 654 321');
		expect(groupNational('6123456789')).toBe('612 345 6789');
		expect(groupNational('12345')).toBe('123 45');
		expect(groupNational('1')).toBe('1');
	});

	it('"+51 987 654 321"', () => {
		expect(formatPhone('+51987654321', COUNTRIES)).toBe('+51 987 654 321');
		expect(formatPhone('+525512345678', COUNTRIES)).toBe('+52 551 234 5678');
	});

	it('as is when no code matches; empty stays empty', () => {
		expect(formatPhone('+999123', COUNTRIES)).toBe('+999123');
		expect(formatPhone('', COUNTRIES)).toBe('');
		expect(formatPhone(null, COUNTRIES)).toBe('');
	});
});

describe('countries: names, flags, order, search', () => {
	it('Spanish names', () => {
		expect(countryName('PE')).toBe('Perú');
		expect(countryName('es')).toBe('España');
		expect(countryName('')).toBe('');
		expect(countryName('PER')).toBe('');
	});

	it('flag images served by the agenda', () => {
		expect(flagUrl('PE')).toBe('/assets/flags/pe.svg');
		expect(flagUrl('')).toBe('');
		expect(flagUrl('../x')).toBe('');
	});

	it('pinned first in their order, then alphabetical in Spanish', () => {
		const out = sortCountries(COUNTRIES, ['PE', 'MX']).map((c) => c[0]);
		expect(out.slice(0, 2)).toEqual(['PE', 'MX']);
		// Canadá before Colombia before Côte d'Ivoire… España before Estados Unidos.
		expect(out.indexOf('ES')).toBeLessThan(out.indexOf('US'));
		expect(out.indexOf('AR')).toBeLessThan(out.indexOf('CO'));
		expect(out).toHaveLength(COUNTRIES.length);
	});

	it('search by name without accents, by ISO, by code', () => {
		expect(filterCountries(COUNTRIES, 'peru').map((c) => c[0])).toEqual(['PE']);
		expect(filterCountries(COUNTRIES, 'mx').map((c) => c[0])[0]).toBe('MX');
		expect(filterCountries(COUNTRIES, '+51').map((c) => c[0])).toEqual(['PE']);
		expect(filterCountries(COUNTRIES, '34').map((c) => c[0])).toEqual(['ES']);
		// A whole pasted number finds its code.
		expect(filterCountries(COUNTRIES, '+51 987 654 321').map((c) => c[0])).toEqual(['PE']);
		// "Estados" matches a word start of «Estados Unidos».
		expect(filterCountries(COUNTRIES, 'unidos').map((c) => c[0])).toContain('US');
		expect(filterCountries(COUNTRIES, '')).toHaveLength(COUNTRIES.length);
	});
});

describe('guessCountry', () => {
	const data = { countries: COUNTRIES, tz2cc: { 'America/Lima': 'PE', 'Europe/Madrid': 'ES', 'Antarctica/Troll': 'AQ' } };

	it('the profile zone first', () => {
		expect(guessCountry('America/Lima', { ...data, visitor_country: 'MX' })).toBe('PE');
	});

	it('UTC names no country: then the visitor country, else none', () => {
		expect(guessCountry('UTC', { ...data, visitor_country: 'MX' })).toBe('MX');
		expect(guessCountry('UTC', data)).toBe('');
	});

	it('only a country the list offers', () => {
		expect(guessCountry('Antarctica/Troll', { ...data, visitor_country: 'ZZ' })).toBe('');
	});
});

describe('noticeClockHint', () => {
	it('the profile zone', () => {
		expect(noticeClockHint('America/Mexico_City', 'México', true)).toBe(
			'Los avisos te muestran la fecha y la hora en tu zona horaria (America/Mexico City).'
		);
		expect(noticeClockHint('America/Lima', 'Perú', false)).toMatch(/^Los avisos le muestran .* su zona horaria/);
	});

	it('UTC: the country of the number, else universal time', () => {
		expect(noticeClockHint('UTC', 'Perú', true)).toBe('Como tu zona horaria dice UTC, los avisos usarán la hora de Perú.');
		expect(noticeClockHint('', '', false)).toMatch(/hora universal/);
	});
});

describe('waMeUrl', () => {
	it('digits only', () => {
		expect(waMeUrl('+51 987 654 321')).toBe('https://wa.me/51987654321');
		expect(waMeUrl('')).toBe('');
	});
});
