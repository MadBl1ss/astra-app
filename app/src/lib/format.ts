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

const tm = ru.time;

/** How long ago, in one unit: "только что", "14 мин назад", "3 дня назад". */
export function ago(iso: string | null, now: number): string {
  if (!iso) return '';
  const ms = since(iso, now);
  const minutes = Math.floor(ms / 60_000);
  if (minutes < 1) return tm.now;
  if (minutes < 60) return tm.minutesAgo(minutes);
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return tm.hoursAgo(hours);
  const days = Math.floor(hours / 24);
  if (days < 30) return tm.daysAgo(days);
  return dayMonth(iso, now);
}

/** "1 декабря"; the year only when it is not this one. */
export function dayMonth(iso: string, now: number): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const text = `${d.getDate()} ${tm.months[d.getMonth()]}`;
  return d.getFullYear() === new Date(now).getFullYear() ? text : `${text} ${d.getFullYear()}`;
}

/** The local calendar day of a time, as a key: "2026-11-16". */
export function dayKey(iso: string): string {
  const d = new Date(iso);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

/** A day heading: "Сегодня", "Вчера", "12 ноября". */
export function dayTitle(iso: string, now: number): string {
  const key = dayKey(iso);
  if (key === dayKey(new Date(now).toISOString())) return ru.journal.today;
  if (key === dayKey(new Date(now - 86_400_000).toISOString())) return ru.journal.yesterday;
  return dayMonth(iso, now);
}

/** An amount of data: "512 МБ", "1,5 ГБ", "100 ГБ". */
export function bytes(n: number): string {
  let value = Math.max(0, n);
  let unit = 0;
  while (value >= 1024 && unit < tm.bytes.length - 1) {
    value /= 1024;
    unit++;
  }
  const digits = unit === 0 || value >= 10 ? 0 : 1;
  return `${decimal(value, digits).replace(/,0$/, '')} ${tm.bytes[unit]}`;
}

/** A large count with thin spaces: "18 432". */
export function count(n: number): string {
  return Math.round(n)
    .toString()
    .replace(/\B(?=(\d{3})+(?!\d))/g, ' ');
}
