import { describe, expect, it, vi } from 'vitest';
import { saveOnCmdS } from './save-shortcut';

function key(init: KeyboardEventInit, handled = false): KeyboardEvent {
	const e = new KeyboardEvent('keydown', { cancelable: true, ...init });
	if (handled) e.preventDefault();
	return e;
}

describe('saveOnCmdS', () => {
	it('Ctrl/Cmd+S saves and stops the browser dialog', () => {
		const save = vi.fn();
		const e = key({ key: 's', ctrlKey: true });
		saveOnCmdS(save)(e);
		saveOnCmdS(save)(key({ key: 'S', metaKey: true }));
		expect(save).toHaveBeenCalledTimes(2);
		expect(e.defaultPrevented).toBe(true);
	});

	it('respects canSave and ignores other keys', () => {
		const save = vi.fn();
		saveOnCmdS(save, () => false)(key({ key: 's', ctrlKey: true }));
		saveOnCmdS(save)(key({ key: 's' }));
		expect(save).not.toHaveBeenCalled();
	});

	// Perfil: Ctrl+S inside «Tu WhatsApp» is handled (and prevented) by the field, which saves
	// the number itself; the page must not also save its form and claim «guardado».
	it('leaves alone an event something inside the page already handled', () => {
		const save = vi.fn();
		saveOnCmdS(save)(key({ key: 's', ctrlKey: true }, true));
		expect(save).not.toHaveBeenCalled();
	});
});
