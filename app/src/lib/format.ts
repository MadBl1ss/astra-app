import { ru } from './i18n/ru';

const u = ru.units;

function decimal(value: number, digits: number): string {
  return value.toFixed(digits).replace('.', u.decimal);
}

/** Bytes per second as "890 КБ/с" or "4,2 МБ/с". */
export function speed(bytesPerSec: number): string {
  const b = Math.max(0, bytesPerSec);
  if (b < 1000) return `${Math.round(b)} ${u.bps}`;
  const kb = b / 1024;
  if (kb < 1000) return `${Math.round(kb)} ${u.kbps}`;
  const mb = kb / 1024;
  if (mb < 1000) return `${decimal(mb, mb < 100 ? 1 : 0)} ${u.mbps}`;
  return `${decimal(mb / 1024, 1)} ${u.gbps}`;
}

/** A span of time in its two largest units: "40 с", "14 мин", "2 ч 14 мин". */
export function duration(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  if (total < 60) return `${total} ${u.sec}`;
  const minutes = Math.floor(total / 60);
  if (minutes < 60) return `${minutes} ${u.min}`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    const rest = minutes % 60;
    return rest ? `${hours} ${u.hour} ${rest} ${u.min}` : `${hours} ${u.hour}`;
  }
  const days = Math.floor(hours / 24);
  const rest = hours % 24;
  return rest ? `${days} ${u.day} ${rest} ${u.hour}` : `${days} ${u.day}`;
}

/** Local time of day, "12:02". */
export function clock(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
}

/** Milliseconds since an ISO time, 0 when there is none. */
export function since(iso: string | null, now: number): number {
  if (!iso) return 0;
  const t = Date.parse(iso);
  return Number.isNaN(t) ? 0 : Math.max(0, now - t);
}
