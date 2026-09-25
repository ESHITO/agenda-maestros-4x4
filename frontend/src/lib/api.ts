export type User = {
	booking_accent: string;
	id: string;
	email: string;
	name: string;
	timezone: string;
	time_format: '12h' | '24h';
	week_start: number; // 0=Sunday, 1=Monday
	date_format: 'dmy' | 'mdy' | 'ymd';
	avatar_url?: string;
	is_admin: boolean;
	is_owner: boolean;
	/** Retired desk tier: the server always sends false now and it grants nothing. */
	is_support?: boolean;
	role: 'owner' | 'admin' | 'member';
	/** Fork: what this person attends ('' = nothing), independent of the tier. */
	area?: Area;
	/** Fork: the viewer's own booking link (their Mentoría copy; for the template's owner,
	 *  the template itself). null/absent = none. */
	personal_link?: PersonalLink | null;
	notify_confirmation: boolean;
	notify_cancellation: boolean;
	notify_reschedule: boolean;
	notify_reminder: boolean;
	notify_host_booking: boolean;
	notify_host_cancel: boolean;
	notify_host_reschedule: boolean;
};

export type EventType = {
	allow_phone_call: boolean;
	id: string;
	slug: string;
	name: string;
	description?: string;
	duration_minutes: number;
	// How often a booking can START, independent of how long it runs. Defaults to the
	// duration on create; editable so a 45-minute meeting can still be offered on the hour.
	slot_interval_minutes: number;
	is_active: boolean;
	is_public: boolean;
	/** Show already-booked times struck through on the booking page instead of hiding
	 *  them. Off by default: the slots endpoint is public, so this makes the host's
	 *  booked hours visible to anyone with the link. */
	show_taken_slots: boolean;
	location_type: string;
	location_value?: string;
	buffer_before_minutes: number;
	buffer_after_minutes: number;
	min_notice_minutes: number;
	max_future_days: number;
	max_active_bookings: number;
	price_cents: number; // 0 = free
	currency: string;    // ISO 4217, lowercase (e.g. "usd")
	created_at: string;
	subj_confirmation?: string;
	subj_cancellation?: string;
	subj_reschedule?: string;
	subj_reminder?: string;
	msg_confirmation?: string;
	msg_cancellation?: string;
	msg_reschedule?: string;
	msg_reminder?: string;
	/** Overrides the conversational assistant's opening chat line; unset = built-in translated default. */
	msg_greeting?: string;
	reminders: number[]; // hours_before values
	routing_mode: 'fixed' | 'round_robin' | 'collective';
	rr_strategy: 'even' | 'soonest' | 'priority';
	/** True when archived — hidden from the default list, is_active forced off. Reversible. */
	archived?: boolean;
	/** True if the current user owns this event type; false if they only host it (read-only). */
	owned?: boolean;
	/** Owner identity, returned only when the viewer is a read-only host. */
	owner_name?: string;
	owner_email?: string;
	/** Fork: set only on predefined types (absent for ordinary ones). */
	team?: EventTypeTeam;
	/** Fork, single GET of a copy: the mentor it belongs to (also accepted inside team). */
	mentor_name?: string;
};

/** Fork: what a person attends. '' = nothing ("Sin área"). */
export type Area = 'mentoria' | 'soporte' | '';

/** Fork: a personal booking link (GET /v1/users, /v1/users/me). */
export type PersonalLink = { slug: string; url: string; active: boolean };

/** Fork: one mentor's copy of the Mentoría template (owner only). */
export type CopyLink = { slug: string; url: string; mentor_name: string; active: boolean };

/** Fork: the `team` object of a predefined event type (GET /v1/event-types and /{slug}). */
export type EventTypeTeam = {
	kind: 'mentoria_template' | 'mentoria_copy' | 'soporte_shared';
	/** Copy only: the template it follows. */
	template_slug?: string;
	template_name?: string;
	/** Template only: how many mentor copies exist. */
	copies?: number;
	/** Copy: the mentor who attends it. */
	host_name?: string;
	mentor_name?: string;
	/** Template, owner only: every mentor's link. */
	copy_links?: CopyLink[];
	/** Soporte: its current rotation, by priority. */
	hosts?: { id: string; name: string }[];
};

export type EventTypeHost = {
	user_id: string;
	name: string;
	email: string;
	avatar_url?: string;
	role: 'required' | 'rotation' | 'optional';
	priority: number;
	archived: boolean;
};

export type Question = {
	id: string;
	event_type_id: string;
	label: string;
	type: 'text' | 'select' | 'checkbox';
	options?: string[];
	required: boolean;
	position: number;
};

export type Booking = {
	id: string;
	event_type_slug: string;
	start_at: string;
	end_at: string;
	status: 'confirmed' | 'cancelled' | 'rescheduled';
	attendees: { name: string; email: string }[];
	created_at: string;
	host_name?: string; // populated only in the admin "All bookings" view
	location_value?: string;
	/** Payment fields — present only for paid bookings (omitted when free). */
	payment_status?: 'pending' | 'paid' | 'refunded';
	amount_paid_cents?: number;
	amount_paid_currency?: string;
	cancellation_reason?: string;
	/** Fork, GET /v1/bookings only: the event type's name. */
	event_type_name?: string;
	/** Fork, GET /v1/bookings only: the four WhatsApp notices, in order (1-4). */
	whatsapp?: WhatsAppNotice[];
	/** Already serialized by bookingJSON: the primary host, the type and its location kind. */
	host_id?: string;
	event_type_id?: string;
	location_type?: string;
	/** Fork, GET /v1/bookings only: the área derived from the booking's type ('' / absent = none). */
	area?: Area;
	/** Fork, GET /v1/bookings only: who entered the video room. */
	attendance?: Attendance;
};

/** Fork: attendance of a LiveKit booking, computed per list page. */
export type Attendance = {
	status:
		| 'not_applicable'
		| 'pending'
		| 'in_progress'
		| 'attended'
		| 'attended_unverified'
		| 'client_absent'
		| 'host_absent'
		| 'nobody';
	host_joined_at?: string;
	client_joined_at?: string;
	/** attended only: minutes host and client were in the room together. */
	minutes_together?: number;
};

/** Fork: GET /v1/bookings/{id}/reassign-candidates. */
export type ReassignCandidate = { id: string; name: string; area?: Area };

/** Fork: one of a booking's four WhatsApp notices (booking_whatsapp_status.go). */
export type WhatsAppNotice = {
	kind: 'created' | 'morning' | '1h' | '5m';
	/** missed = the job ran too late and dropped it (worker down / instance asleep);
	 *  unknown = older than the 30-day record retention, nothing left to tell. */
	status: 'sent' | 'sending' | 'pending' | 'failed' | 'cancelled' | 'missed' | 'unknown' | 'not_applicable';
	/** RFC3339 UTC: when it was sent / last tried, planned (pending), or dropped (missed). */
	at?: string;
};

/** Fork: the WhatsApp text moments of an event type (fork_whatsapp.go). */
export type WhatsAppMoment =
	| 'created'
	| 'reminder_morning'
	| 'reminder_1h'
	| 'reminder_5m'
	| 'cancelled'
	| 'rescheduled';

/** GET/PUT /v1/event-types/{slug}/whatsapp-messages: saved texts ("" = default) + defaults. */
export type WhatsAppMessages = Record<WhatsAppMoment, string> & {
	defaults: Record<WhatsAppMoment, string>;
};

/** POST /v1/event-types/{slug}/whatsapp-messages/preview. */
export type WhatsAppPreview = Record<WhatsAppMoment, string> & {
	timezone: string;
	has_text_question: boolean;
};

/** Fork: GET/PUT /v1/webhooks/settings. */
export type WebhookSettings = {
	reminder_morning_hour: string;
	/** "" = each client's own zone. */
	reminder_morning_timezone: string;
	team_scope: boolean;
	can_edit: boolean;
	/** PUT only: upcoming bookings re-planned right after saving. */
	resynced_bookings?: number;
	resync_ok?: boolean;
};

export type APIKey = {
	id: string;
	name: string;
	created_at: string;
	last_used_at?: string;
};

export type OAuthConnection = {
	id: string;
	client_name: string;
	created_at: string;
	last_used_at?: string;
	expires_at: string;
};

export type Webhook = {
	id: string;
	url: string;
	events: string[];
	fields?: string[];
	/** Fork: event types this webhook is limited to; empty = every event type. */
	event_type_ids?: string[];
	is_active: boolean;
	created_at: string;
};

/** Fork: GET /v1/webhooks/event-types - the event types a webhook may be limited to. */
export type WebhookEventType = {
	id: string;
	slug: string;
	name: string;
	is_active: boolean;
	archived: boolean;
	owned: boolean;
	owner_name: string;
	/** Fork: on the Mentoría template, how many mentor copies it covers (copies themselves are omitted). */
	copies?: number;
};

export type WebhookDelivery = {
	id: string;
	webhook_id: string;
	event: string;
	status: string;
	booking_id?: string;
	response_status?: number;
	attempt_count: number;
	last_attempted_at?: string;
};

export type CalendarConnection = {
	id: string;
	provider: string;
	account_email: string;
	is_destination: boolean;
	check_conflicts: boolean;
};

export type CalendarPick = {
	id: string;
	name: string;
	primary: boolean;
	writable: boolean; // false for a read-only shared calendar: valid for conflicts, not as a write target
	check_conflicts: boolean;
	is_destination: boolean;
};

export type CalendarStatus = {
	connected: boolean;
	configured?: boolean;
	calendar_id?: string;
	provider?: string;    // destination provider name, when connected
	providers?: string[]; // configured providers available to connect
	connections?: CalendarConnection[]; // all connected calendars (many checked, one destination)
	unconfigured_providers?: string[]; // providers Calnode supports but this instance has no credentials for
};

export type EmailSettings = {
	smtp_host: string;
	smtp_port: string;
	smtp_user: string;
	smtp_pass_set: boolean; // true when a password is stored; never returned directly
	smtp_tls: boolean;
	smtp_starttls: boolean;
	email_from: string;
	email_from_name: string;
	resend_api_key_set: boolean; // true when a key is stored; never returned directly
	// Which path mail actually goes out over. "SMTP fields are filled in" and "mail is
	// being delivered over SMTP" can differ, so the server reports the live answer.
	transport: 'none' | 'smtp' | 'resend_api';
	enabled: boolean;
};

export type GoogleSettings = {
	client_id: string;
	client_secret_set: boolean;
	configured: boolean;
	/** Identity host the server builds OAuth redirect URIs from. */
	base_url: string;
};

export type ZoomSettings = {
	client_id: string;
	client_secret_set: boolean;
	configured: boolean;
	/** Exact redirect URI to register in the Zoom Marketplace app. */
	redirect_uri: string;
};

export type ZoomStatus = {
	configured: boolean; // a Zoom app is set up for the instance
	connected: boolean;  // the current host has connected their Zoom account
};

export type StripeSettings = {
	publishable_key: string;
	secret_key_set: boolean;
	webhook_secret_set: boolean;
	configured: boolean; // can take a payment AND verify the confirming webhook
	/** The endpoint to register in the Stripe dashboard. */
	webhook_url: string;
};

export type LLMSettings = {
	enabled: boolean;
	endpoint: string;
	model: string;
	api_key_set: boolean;
	configured: boolean;
	/** true when a live client is active (enabled + configured). */
	active: boolean;
	/** admin "additional instructions" appended to the base prompt. */
	extra_instructions: string;
	/** read-only, code-owned base system prompt (not editable). */
	base_prompt: string;
};

export type TeamMember = {
	id: string;
	email: string;
	name: string;
	timezone: string;
	is_admin: boolean;
	is_owner: boolean;
	/** Retired desk tier (always false now). */
	is_support?: boolean;
	role: 'owner' | 'admin' | 'member';
	/** Fork: what this person attends ('' = nothing). */
	area?: Area;
	/** Fork: their active copy's link (for the template's owner, the template). */
	personal_link?: PersonalLink | null;
	email_login: boolean;
	provider?: string;
	avatar_url?: string;
	created_at: string;
	archived: boolean;
	archived_at?: string;
	archived_by?: string;
	archived_by_name?: string;
	teams: { id: string; name: string }[];
};

export type Team = {
	id: string;
	name: string;
	slug: string;
	created_at: string;
	member_count: number;
	members?: TeamMemberRef[];
};

export type TeamMemberRef = {
	id: string;
	name: string;
	email: string;
	avatar_url?: string;
	routing_priority: number;
	archived: boolean;
};

export type UpcomingBooking = {
	id: string;
	start_at: string;
	end_at: string;
	event_type_name: string;
	event_type_slug: string;
	attendee_name: string;
	attendee_email: string;
};

export type Invite = {
	id: string;
	email: string;
	expires_at: string;
	created_by: string;
	/** Fork: the role the invitee gets on claiming (absent = none stored). */
	role?: InviteRole;
};

/** Fork: role carried by an invite. Same spelling as Area for the two áreas. */
export type InviteRole = 'mentoria' | 'soporte' | 'admin';

/** Fork: GET /v1/team/settings (admins; can_edit = owner). */
export type TeamSettings = {
	mentoria_template: {
		id: string;
		slug: string;
		name: string;
		copies: number;
		/** Owner only. */
		copy_links?: CopyLink[];
	} | null;
	soporte_shared: {
		id: string;
		slug: string;
		name: string;
		hosts: { id: string; name: string }[];
	} | null;
	can_edit: boolean;
	/** PUT only: what the save changed or should draw attention to (teamWarnings in Go). */
	warnings?: {
		copies_created: number;
		copies_deactivated: number;
		/** No active webhook of the owner receives the template's / Soporte type's bookings. */
		mentoria_template_no_webhook: boolean;
		soporte_shared_no_webhook: boolean;
	};
};

/** Fork: PUT /v1/users/{id}/team-role. */
export type TeamRoleResponse = {
	id: string;
	tier: 'owner' | 'admin' | 'member';
	area: Area;
	upcoming_in_previous_area: number;
};

export type AvailabilityRule = {
	id: string;
	event_type_id: string | null;
	day_of_week: number;
	start_time: string;
	end_time: string;
};

export type AvailabilityOverride = {
	id: string;
	date: string;
	is_available: boolean;
	reason: 'day_off' | 'out_of_office' | 'custom_hours';
	start_time: string | null;
	end_time: string | null;
	/** Set on per-date rows that belong to a multi-day out-of-office span. */
	group_id?: string;
};

async function apiFetch<T>(path: string, opts: RequestInit = {}): Promise<T> {
	const res = await fetch(path, {
		credentials: 'same-origin',
		headers: {
			...(opts.body && typeof opts.body === 'string'
				? { 'Content-Type': 'application/json' }
				: {}),
			...((opts.headers as Record<string, string>) ?? {})
		},
		...opts
	});

	if (res.status === 401) {
		window.location.href = '/admin/login';
		throw new Error('unauthenticated');
	}

	if (res.status === 204) return null as T;

	const data = await res.json().catch(() => ({ error: res.statusText }));
	if (!res.ok) {
		const err = new Error(data.error ?? `HTTP ${res.status}`) as Error & { status?: number };
		err.status = res.status;
		throw err;
	}
	return data as T;
}

export const api = {
	get: <T>(path: string) => apiFetch<T>(path),

	post: <T>(path: string, body?: unknown) =>
		apiFetch<T>(path, { method: 'POST', body: body ? JSON.stringify(body) : undefined }),

	postForm: <T>(path: string, data: FormData) =>
		apiFetch<T>(path, { method: 'POST', body: data }),

	patch: <T>(path: string, body?: unknown) =>
		apiFetch<T>(path, { method: 'PATCH', body: body ? JSON.stringify(body) : undefined }),

	put: <T>(path: string, body?: unknown) =>
		apiFetch<T>(path, { method: 'PUT', body: body ? JSON.stringify(body) : undefined }),

	del: <T = null>(path: string) => apiFetch<T>(path, { method: 'DELETE' })
};

// ── Fork: team áreas, predefined types and supervision (typed wrappers) ─────────────
// Thin helpers over `api` so every page calls the new endpoints with the same shapes.

/** Human label of each área, and of a role an invite can carry. */
export const AREA_LABELS: Record<Exclude<Area, ''>, string> = { mentoria: 'Mentoría', soporte: 'Soporte' };
export const INVITE_ROLE_LABELS: Record<InviteRole, string> = {
	mentoria: 'Mentor',
	soporte: 'Soporte',
	admin: 'Administrador'
};

export const teamApi = {
	/** GET /v1/team/settings (admins). */
	getSettings: () => api.get<TeamSettings>('/v1/team/settings'),

	/** PUT /v1/team/settings (owner). null = unset. Answers the settings + warnings. */
	putSettings: (body: { mentoria_template_id: string | null; soporte_shared_id: string | null }) =>
		api.put<TeamSettings>('/v1/team/settings', body),

	/** PUT /v1/users/{id}/team-role: tier and área in one call (see the matrix in Members). */
	putTeamRole: (userId: string, body: { tier: 'admin' | 'member'; area: Area }) =>
		api.put<TeamRoleResponse>(`/v1/users/${userId}/team-role`, body),

	/** GET /v1/users/{id}/upcoming-bookings (admins). */
	upcomingBookings: (userId: string) =>
		api.get<{ items: UpcomingBooking[] }>(`/v1/users/${userId}/upcoming-bookings`),

	/** GET /v1/bookings/{id}/reassign-candidates (admins): people of the booking's área. */
	reassignCandidates: async (bookingId: string) => {
		const res = await api.get<ReassignCandidate[] | { items: ReassignCandidate[] }>(
			`/v1/bookings/${bookingId}/reassign-candidates`
		);
		// The contract is a bare array; tolerate an { items } envelope too.
		return (Array.isArray(res) ? res : res?.items) ?? [];
	},

	/** POST /v1/bookings/{id}/reassign (admins). */
	reassign: (bookingId: string, hostId: string) =>
		api.post<Booking>(`/v1/bookings/${bookingId}/reassign`, { host_id: hostId }),

	/** POST /v1/invites with the role the invitee will get. */
	createInvite: (email: string, role: InviteRole) =>
		api.post<{ id: string; email: string; invite_url: string; expires_at: string; email_sent: boolean; note: string }>(
			'/v1/invites',
			{ email, role }
		)
};

/** Upstream reassign errors are English; the fork's guard answers Spanish already. */
const REASSIGN_ERRORS: Record<string, string> = {
	'new host not found or archived': 'Esa persona ya no está disponible (no existe o está archivada).',
	'the chosen host already has a booking at that time': 'Esa persona ya tiene otra reunión a esa hora.',
	'this booking has been cancelled': 'Esta reunión ya fue cancelada.',
	'booking not found': 'No se encontró la reunión (quizá ya se movió o se canceló).',
	'host_id is required': 'Elige a quién pasar la reunión.',
	'admin access required': 'Solo el propietario y los administradores pueden pasar reuniones a otra persona.'
};

export function reassignErrorText(e: unknown): string {
	const msg = e instanceof Error ? e.message : String(e ?? '');
	return REASSIGN_ERRORS[msg] ?? (msg || 'No se pudo pasar la reunión a otra persona.');
}

/** Copy text to the clipboard; false when the browser refuses (no permission / not https). */
export async function copyText(text: string): Promise<boolean> {
	try {
		await navigator.clipboard.writeText(text);
		return true;
	} catch {
		return false;
	}
}
