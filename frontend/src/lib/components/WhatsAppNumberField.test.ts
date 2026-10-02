// Fork: the WhatsApp number field (Perfil «Tu WhatsApp», Miembros) at phone width - most of
// the team uses the panel on a phone. The number shows grouped with its country, the form
// starts from the stored number (or the profile's country), says what will be saved, sends
// E.164 and shows the server's Spanish error under the field.
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { render, cleanup } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';
import type { MemberWhatsApp, PhoneData } from '$lib/whatsapp-phone';

const phoneData: PhoneData = {
	countries: [
		['MX', '52'],
		['PE', '51'],
		['US', '1'],
		['CA', '1']
	],
	tz2cc: { 'America/Lima': 'PE', 'America/Mexico_City': 'MX' },
	visitor_country: ''
};

vi.mock('$lib/whatsapp-phone', async (importOriginal) => {
	const mod = await importOriginal<typeof import('$lib/whatsapp-phone')>();
	return { ...mod, loadPhoneData: () => Promise.resolve(phoneData) };
});

const { default: WhatsAppNumberField } = await import('./WhatsAppNumberField.svelte');

const answer = (phone: string | null): MemberWhatsApp => ({
	user_id: 'u1',
	phone,
	whatsapp: phone ? phone.slice(1) : null,
	country: phone ? 'PE' : null,
	country_name: phone ? 'Perú' : null,
	updated_at: phone ? '2026-10-02T10:00:00Z' : null
});

async function settle(check: () => void) {
	await expect.poll(() => {
		try {
			check();
			return true;
		} catch {
			return false;
		}
	}).toBe(true);
	check();
}

function setInput(el: HTMLInputElement, value: string) {
	el.value = value;
	el.dispatchEvent(new Event('input', { bubbles: true }));
}

beforeEach(async () => {
	await page.viewport(375, 812);
	document.body.style.margin = '0';
	document.body.style.padding = '0 16px'; // the admin shell's px-4 gutter
});

afterEach(() => {
	cleanup();
	document.body.style.padding = '';
});

describe('WhatsAppNumberField at 375 px', () => {
	test('shows the stored number grouped, and edits it into E.164', async () => {
		const save = vi.fn(async (p: string | null) => answer(p));
		const { container } = await render(WhatsAppNumberField, {
			id: 't1',
			phone: '+51987654321',
			country: 'PE',
			zone: 'America/Lima',
			personName: 'Ana Torres',
			save
		});

		await settle(() => expect(container.textContent).toContain('+51 987 654 321'));
		expect(container.textContent).toContain('Perú');
		expect(container.textContent).toContain('America/Lima');
		expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(375);

		const change = [...container.querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Cambiar')!;
		change.click();
		const input = await vi.waitFor(() => {
			const el = container.querySelector<HTMLInputElement>('#t1-number');
			if (!el) throw new Error('no input yet');
			return el;
		});
		expect(input.value).toBe('987654321');
		expect(container.textContent).toContain('+51');

		setInput(input, '912 345 678');
		await settle(() => expect(container.textContent).toContain('Se guardará como +51 912 345 678 (Perú).'));
		expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(375);

		input.form!.requestSubmit();
		await settle(() => expect(save).toHaveBeenCalledWith('+51912345678'));
	});

	test('with no number, Perfil opens the form on the profile country', async () => {
		const save = vi.fn(async (p: string | null) => answer(p));
		const { container } = await render(WhatsAppNumberField, {
			id: 't2',
			self: true,
			openWhenEmpty: true,
			phone: null,
			zone: 'America/Mexico_City',
			save
		});

		await settle(() => expect(container.querySelector('#t2-number')).not.toBeNull());
		expect(container.textContent).toContain('+52');
		// Nothing to go back to: no Cancelar.
		expect([...container.querySelectorAll('button')].some((b) => b.textContent?.trim() === 'Cancelar')).toBe(false);
	});

	test('a typed "+" wins, and the server error shows under the field', async () => {
		const save = vi.fn(async () => {
			throw new Error('Ese código de país no existe.');
		});
		const { container } = await render(WhatsAppNumberField, { id: 't3', phone: null, zone: 'UTC', save });

		await settle(() => expect(container.textContent).toContain('Sin WhatsApp'));
		[...container.querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Poner su WhatsApp')!.click();
		const input = await vi.waitFor(() => {
			const el = container.querySelector<HTMLInputElement>('#t3-number');
			if (!el) throw new Error('no input yet');
			return el;
		});

		// No country picked (UTC names none) and no "+": it asks for one, without a request.
		setInput(input, '987654321');
		input.form!.requestSubmit();
		await settle(() => expect(container.textContent).toMatch(/Elige el país/));
		expect(save).not.toHaveBeenCalled();

		setInput(input, '+1 416 555 0123');
		input.form!.requestSubmit();
		await settle(() => expect(container.textContent).toContain('Ese código de país no existe.'));
		expect(save).toHaveBeenCalledWith('+14165550123');
	});
});
