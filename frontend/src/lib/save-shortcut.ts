/** Ctrl/Cmd+S → save(). An event something inside the page already handled
 *  (defaultPrevented: e.g. the «Tu WhatsApp» field saving itself in Perfil) is left alone,
 *  so the page's form never claims «guardado» for a field it does not save. */
export function saveOnCmdS(save: () => void, canSave?: () => boolean) {
	return (e: KeyboardEvent) => {
		if (e.defaultPrevented) return;
		if ((e.metaKey || e.ctrlKey) && (e.key === 's' || e.key === 'S')) {
			e.preventDefault();
			if (!canSave || canSave()) save();
		}
	};
}
