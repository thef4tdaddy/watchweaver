export type Media = {
  id: number;
  type: string;
  title: string;
  year?: number;
  show_title?: string;
  season_number?: number;
  episode_number?: number;
  season_id?: number;
  external_ids: Record<string, string>;
};
export type Task = {
  id: number;
  type: string;
  state: string;
  snoozed_until?: string;
  created_at: string;
  media: Media;
};
export type HistoryItem = {
  id: number;
  source: string;
  source_event_id?: string;
  watched_at: string;
  media: Media;
};
export type Page<T> = {
  page: number;
  per_page: number;
  total: number;
  total_pages: number;
  items: T[];
};
export type Rating = { media_id: number; rating: number; stars: number };
export type Review = { media_id: number; body: string; updated_at?: string };
export type Settings = {
  timezone: string;
  trakt_poll_minutes: number;
  prompt_movies_enabled: boolean;
  prompt_tv_enabled: boolean;
  serializd_enabled: boolean;
  serializd_reminder_changes: number;
  serializd_reminder_days: number;
  update_checks_enabled: boolean;
};
export type SetupStatus = {
  complete: boolean;
  encrypted_storage: boolean;
  trakt: {
    configured: boolean;
    authorization_status: string;
    client_id_overridden: boolean;
    client_secret_overridden: boolean;
  };
  discord: {
    configured: boolean;
    enabled: boolean;
    webhook_overridden: boolean;
  };
  jellyfin?: { configured: boolean; protocol_version: number };
};
export type UpdateStatus = {
  state: "up_to_date" | "beta_update_available" | "stable_update_available" | "unable" | "disabled" | "development";
  running_version: string;
  revision?: string;
  latest_version?: string;
  release_url?: string;
  channel: "beta" | "stable" | "development";
  checked_at?: string;
  enabled: boolean;
  cached?: boolean;
};
export type TraktSyncResult = {
  started_at: string;
  completed_at: string;
  history_changes: number;
  rating_changes: number;
  pending_ratings_completed: number;
  pending_ratings_remaining: number;
};
export type TraktStatus = {
  authorization: {
    status: string;
    user_code?: string;
    verification_url?: string;
    poll_after_seconds?: number;
  };
  poll: {
    phase?: string;
    last_success?: string;
    last_error?: string;
    consecutive_failures: number;
  };
  sync: {
    running: boolean;
    last_result?: TraktSyncResult;
    last_error?: string;
    next_run?: string;
    can_sync: boolean;
    retry_allowed: boolean;
  };
};
export type Integrations = {
	trakt: TraktStatus;
	jellyfin?: {
		configured: boolean;
		protocol_version: number;
		accepted_count: number;
		auth_failure_count: number;
		last_accepted_at?: string;
		last_server_version?: string;
		last_plugin_version?: string;
		last_rejection_at?: string;
		last_rejection_code?: string;
	};
  letterboxd: { enabled: boolean; status: string };
  serializd: { enabled: boolean; status: string };
  discord: { enabled: boolean; status: string };
};
export type OperationalComponent = {
  state: "working" | "needs_attention" | "disabled";
  label: string;
  detail: string;
  action?: string;
};
export type OperationalStatus = {
  overall: "working" | "needs_attention";
  checked_at: string;
  components: Record<string, OperationalComponent>;
  backup: OperationalComponent & { last_backup?: string; size_bytes?: number };
};
export type LetterboxdStatus = {
  pending_rows: number;
  pending_events: number;
  duplicate_warnings: number;
  generated_batches: number;
};
export type BatchFile = {
  part_number: number;
  filename: string;
  size_bytes: number;
};
export type Batch = {
  id: number;
  state: string;
  timezone: string;
  generated_at: string;
  confirmed_at?: string;
  row_count: number;
  event_count: number;
  duplicate_warnings: number;
  files: BatchFile[];
};
export type SerializdStatus = {
  enabled: boolean;
  last_confirmed_at?: string;
  pending_changes: number;
  pending_episode_watches: number;
  pending_rating_changes: number;
  tracked_episode_watches: number;
  oldest_pending_at?: string;
  count_threshold_reached: boolean;
  elapsed_threshold_reached: boolean;
  due: boolean;
  unsupported_season_ratings: number;
  unsupported_tv_reviews: number;
  reminder_changes: number;
  reminder_days: number;
  import_url: string;
};
export class APIError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}
export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: { "Content-Type": "application/json", ...(init?.headers || {}) },
  });
  if (!response.ok) {
    let message = `Request failed (${response.status})`;
    try {
      const body = (await response.json()) as { error?: string };
      if (body.error) message = body.error;
    } catch {
      /* keep status message */
    }
    throw new APIError(response.status, message);
  }
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}
