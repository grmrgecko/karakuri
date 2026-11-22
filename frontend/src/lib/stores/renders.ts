import { api } from '../api/client';

export interface RenderJob {
  id: string;
  dashboard_id: string;
  status: string;
  width: number;
  height: number;
  format: string;
  error_message?: string;
  webhook_url?: string;
  schedule_id?: number;
  expires_at: string;
  created_at: string;
  updated_at: string;
}

export interface RenderJobResult extends RenderJob {
  /** Shown once when the job is created; it is stored hashed. */
  token: string;
  result_url: string;
}

export interface RenderSchedule {
  id: number;
  dashboard_id: string;
  name: string;
  cron_expr: string;
  location: string;
  format: string;
  width: number;
  height: number;
  enabled: boolean;
  webhook_url?: string;
  created_at: string;
  updated_at: string;
}

export const RENDER_FORMATS = ['png', 'jpg', 'pdf', 'gif', 'bmp', 'tiff'] as const;

function createRendersStore() {
  return {
    listJobs(dashboardId: string): Promise<RenderJob[]> {
      return api.renders.listJobs<RenderJob[]>(dashboardId);
    },

    create(dashboardId: string, body: Partial<RenderJob>): Promise<RenderJobResult> {
      return api.renders.create<RenderJobResult>(dashboardId, body as Record<string, unknown>);
    },

    cancel(dashboardId: string, jobId: string): Promise<void> {
      return api.renders.cancel(dashboardId, jobId);
    },

    listSchedules(dashboardId: string): Promise<RenderSchedule[]> {
      return api.renders.listSchedules<RenderSchedule[]>(dashboardId);
    },

    createSchedule(dashboardId: string, body: Partial<RenderSchedule>): Promise<RenderSchedule> {
      return api.renders.createSchedule<RenderSchedule>(
        dashboardId,
        body as Record<string, unknown>,
      );
    },

    updateSchedule(
      dashboardId: string,
      schedId: number,
      body: Partial<RenderSchedule>,
    ): Promise<RenderSchedule> {
      return api.renders.updateSchedule<RenderSchedule>(
        dashboardId,
        schedId,
        body as Record<string, unknown>,
      );
    },

    removeSchedule(dashboardId: string, schedId: number): Promise<void> {
      return api.renders.removeSchedule(dashboardId, schedId);
    },
  };
}

export const renders = createRendersStore();

// The render URLs are addresses rather than API calls: what points at them is
// an <img> tag, a wiki page, or a photo frame. They are built here so the page
// showing them and the page copying them cannot disagree.

/** renderUrl renders on demand and answers with the file. */
export function renderUrl(slug: string, width: number, height: number, format: string): string {
  return `/dashboard/${slug}/render/${width}x${height}.${format}`;
}

/** scheduleUrl serves a schedule's latest render and never changes. */
export function scheduleUrl(slug: string, scheduleId: number, format: string): string {
  return `/dashboard/${slug}/render/schedule/${scheduleId}.${format}`;
}
